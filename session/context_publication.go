package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"crude/collaboration"
	"crude/provider"
	"crude/sessiondb"
	recoverytypes "crude/types/recovery"
)

func contextDelta(message provider.ChatMessage, meta sessiondb.DisplayMeta, turn int, createdAt time.Time, agentID string) sessiondb.ContextMessageDelta {
	// Display text and reference labels belong to the immutable transcript
	// metadata, not provider/current-context message bytes. Keeping them in the
	// context copy makes a later revision-fenced replacement conflict with the
	// same message reloaded from the normalized transcript representation.
	message = canonicalContextMessage(message)
	return sessiondb.ContextMessageDelta{
		Message: message, Meta: meta, Turn: turn, CreatedAt: createdAt.UnixNano(), AgentID: agentID,
	}
}

func canonicalContextMessage(message provider.ChatMessage) provider.ChatMessage {
	message.DisplayContent = ""
	message.ReferenceLabels = nil
	message.Origin, message.Visibility = message.EffectiveOrigin(), message.EffectiveVisibility()
	if len(message.ToolCalls) == 0 {
		message.ToolCalls = nil
	}
	if len(message.ProviderItems) == 0 {
		message.ProviderItems = nil
	}
	if len(message.ReasoningDetails) == 0 {
		message.ReasoningDetails = nil
	}
	if message.Collaboration != nil {
		attribution := *message.Collaboration
		if len(attribution.RecipientIDs) == 0 {
			attribution.RecipientIDs = nil
		}
		if len(attribution.RecipientNames) == 0 {
			attribution.RecipientNames = nil
		}
		message.Collaboration = &attribution
	}
	return message
}

func canonicalContextMessages(messages []provider.ChatMessage) []provider.ChatMessage {
	result := append([]provider.ChatMessage(nil), messages...)
	for i := range result {
		result[i] = canonicalContextMessage(result[i])
	}
	return result
}

// A checkpoint may add accepted output between already committed deliveries,
// but it cannot omit, reorder or change any committed source message.
func retainsContextSource(snapshot, source []provider.ChatMessage) bool {
	i := 0
	for _, message := range snapshot {
		if i >= len(source) {
			break
		}
		if source[i].InternalID == "" {
			message.InternalID = ""
		}
		if reflect.DeepEqual(canonicalContextMessage(message), canonicalContextMessage(source[i])) {
			i++
		}
	}
	return i == len(source)
}

func (s *Session) publishRootRecovery(run *recoveryRunState, status recoverytypes.Status) error {
	s.msgsMu.Lock()
	defer s.msgsMu.Unlock()
	if err := s.reconcileRootRecoveryLocked(run); err != nil {
		return err
	}
	if err := run.publish(s.db, s.sessionID, status); err != nil {
		return err
	}
	s.contextRevision, s.contextGeneration, s.contextReason = run.contextState()
	_, s.activeContext = run.contexts()
	return nil
}

// Lifecycle controls commit through the context owner before cancellation. They
// can arrive after the execution snapshot, even after its turn has ended. Adopt
// those exact committed facts at checkpoints and finalization, never replay a
// second in-memory control queue into the next turn. Caller holds msgsMu.
func (s *Session) reconcileRootRecoveryLocked(run *recoveryRunState) error {
	run.mu.Lock()
	defer run.mu.Unlock()
	active := canonicalContextMessages(run.active)
	if run.sourceRevision == run.publishedRevision {
		active = append([]provider.ChatMessage(nil), s.activeContext...)
	}
	var controls []provider.ChatMessage
	for _, message := range s.activeContext {
		if message.Collaboration != nil && message.Collaboration.Kind == string(collaboration.EventLifecycleControl) {
			controls = append(controls, message)
		}
	}
	if !retainsContextSource(active, s.activeContext) {
		// Only independently admitted control events may be absent from a root
		// execution snapshot. All other committed source facts remain mandatory.
		source := make([]provider.ChatMessage, 0, len(s.activeContext)-len(controls))
		for _, message := range s.activeContext {
			if message.Collaboration == nil || message.Collaboration.Kind != string(collaboration.EventLifecycleControl) {
				source = append(source, message)
			}
		}
		if !retainsContextSource(active, source) {
			return fmt.Errorf("recovery checkpoint does not retain committed context revision %d", s.contextRevision)
		}
		var err error
		active, err = mergeCommittedContextMessages(active, canonicalContextMessages(s.activeContext))
		if err != nil {
			return err
		}
	}
	archive := run.archive
	if len(controls) > 0 {
		// The archive and active context can have different shapes (compaction),
		// and transcript metadata is not a full provider-message projection. Adopt
		// only the immutable controls; do not reproject the rest of the archive.
		var err error
		archive, err = mergeCommittedContextMessages(archive, controls)
		if err != nil {
			return err
		}
	}
	if !reflect.DeepEqual(run.active, active) || !reflect.DeepEqual(run.archive, archive) {
		run.sourceRevision++
	}
	run.active, run.archive = active, archive
	run.contextRevision, run.contextGeneration, run.contextReason = s.contextRevision, max(uint64(1), s.contextGeneration), s.contextReason
	if reflect.DeepEqual(active, s.activeContext) {
		run.publishedRevision = run.sourceRevision
	}
	return nil
}

// Committed messages are immutable order anchors. Preserve accepted local output
// while inserting late committed facts before the next shared anchor or at EOF.
func reconcileCommittedRecoveryContext(run *recoveryRunState, committed sessiondb.ContextState) error {
	run.mu.Lock()
	defer run.mu.Unlock()
	if committed.AgentID != run.contextOwnerID || committed.Revision < run.contextRevision || committed.Generation != run.contextGeneration {
		return fmt.Errorf("committed recovery context owner, revision or generation changed")
	}
	source := canonicalContextMessages(committed.Messages)
	active, err := mergeCommittedContextMessages(run.active, source)
	if err != nil {
		return err
	}
	archive, err := mergeCommittedContextMessages(run.archive, source)
	if err != nil {
		return err
	}
	active = canonicalContextMessages(active)
	if !reflect.DeepEqual(run.active, active) || !reflect.DeepEqual(run.archive, archive) {
		run.sourceRevision++
	}
	run.active, run.archive = active, archive
	run.contextRevision, run.contextGeneration, run.contextReason = committed.Revision, committed.Generation, committed.Reason
	if reflect.DeepEqual(active, source) {
		run.publishedRevision = run.sourceRevision
	}
	return nil
}

func mergeCommittedContextMessages(snapshot, source []provider.ChatMessage) ([]provider.ChatMessage, error) {
	// Stable IDs are sufficient for ordinary messages. Serialize full payloads
	// only when an anonymous source/snapshot needs exact occurrence matching;
	// one late control must not re-encode a dense tool archive at each checkpoint.
	anonymousSnapshot := false
	for _, message := range snapshot {
		anonymousSnapshot = anonymousSnapshot || message.InternalID == ""
	}
	anonymousSource := false
	for _, message := range source {
		anonymousSource = anonymousSource || message.InternalID == ""
	}
	key := func(message provider.ChatMessage) (string, error) {
		if message.InternalID != "" {
			return "id:" + message.InternalID, nil
		}
		encoded, err := json.Marshal(canonicalContextMessage(message))
		return "anonymous:" + string(encoded), err
	}
	indices := make(map[string][]int, len(source))
	matched := make([]bool, len(source))
	for i, message := range source {
		identity, err := key(message)
		if err != nil {
			return nil, err
		}
		indices[identity] = append(indices[identity], i)
		if message.InternalID != "" && anonymousSnapshot {
			message.InternalID = ""
			identity, err = key(message)
			if err != nil {
				return nil, err
			}
			indices[identity] = append(indices[identity], i)
		}
	}
	merged := make([]provider.ChatMessage, 0, len(snapshot)+len(source))
	cursor := 0
	for _, message := range snapshot {
		identity, err := key(message)
		if err != nil {
			return nil, err
		}
		matches, known := indices[identity]
		if known && len(matches) == 0 && message.InternalID != "" {
			return nil, &sessiondb.ContextMessageConflictError{InternalID: message.InternalID, Location: "duplicate committed recovery snapshot"}
		}
		if !known && message.InternalID != "" && anonymousSource {
			anonymous := message
			anonymous.InternalID = ""
			identity, err = key(anonymous)
			if err != nil {
				return nil, err
			}
			matches = indices[identity]
			for len(matches) > 0 && (source[matches[0]].InternalID != "" || matched[matches[0]]) {
				matches = matches[1:]
			}
		} else if message.InternalID == "" {
			for len(matches) > 0 && matched[matches[0]] {
				matches = matches[1:]
			}
		}
		if len(matches) == 0 {
			merged = append(merged, message)
			continue
		}
		index := matches[0]
		comparison := canonicalContextMessage(message)
		if message.InternalID == "" || source[index].InternalID == "" {
			comparison.InternalID = source[index].InternalID
		}
		if index < cursor || !reflect.DeepEqual(comparison, source[index]) {
			return nil, &sessiondb.ContextMessageConflictError{InternalID: message.InternalID, Location: "committed recovery snapshot order or bytes"}
		}
		merged = append(merged, source[cursor:index]...)
		if message.InternalID == "" {
			message.InternalID = source[index].InternalID
		}
		merged = append(merged, message)
		cursor = index + 1
		matched[index] = true
		indices[identity] = matches[1:]
	}
	return append(merged, source[cursor:]...), nil
}

// publishRootContext serializes the in-memory root projection with the exact
// revision captured alongside its source payload. Callers must not mutate the
// projection before this returns successfully.
func (s *Session) publishRootContext(publication sessiondb.ContextPublication) (sessiondb.ContextState, error) {
	if s == nil || s.db == nil {
		return sessiondb.ContextState{}, errors.New("session database is unavailable")
	}
	publication.SessionID = s.sessionID
	publication.AgentID = s.sessionID
	if publication.ExpectedRevision == 0 && s.contextRevision != 0 {
		publication.ExpectedRevision = s.contextRevision
	}
	state, err := sessiondb.PublishContext(s.db, publication)
	if err != nil {
		return sessiondb.ContextState{}, err
	}
	s.contextRevision = state.Revision
	s.contextGeneration = state.Generation
	s.contextReason = state.Reason
	s.activeContext = append([]provider.ChatMessage(nil), state.Messages...)
	return state, nil
}

func loadContextRevision(dbState sessiondb.ContextState, found bool) uint64 {
	if !found {
		return 0
	}
	return dbState.Revision
}
