package session

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
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

// Children and roots obey the same rule: journal-admitted controls and human
// steering may arrive outside the execution snapshot. Merge those exact facts;
// never acknowledge a stale snapshot by merely assigning it a newer revision.
func independentlyAdmittedContext(messages []provider.ChatMessage) (required, admitted []provider.ChatMessage) {
	steering := make(map[string]bool)
	for _, message := range messages {
		if message.Collaboration != nil && message.Collaboration.EventID != "" &&
			message.Collaboration.Kind == string(collaboration.EventHumanSteering) && message.EffectiveOrigin() == provider.OriginHumanSteering {
			steering[message.InternalID] = true
		}
	}
	for _, message := range messages {
		independent := message.Collaboration != nil && message.Collaboration.Kind == string(collaboration.EventLifecycleControl) || steering[message.InternalID]
		if !independent && message.EffectiveOrigin() == provider.OriginRuntimeContext && message.EffectiveVisibility() == provider.VisibilityContextOnly {
			for _, suffix := range []string{"-references", "-context"} {
				if strings.HasSuffix(message.InternalID, suffix) && steering[strings.TrimSuffix(message.InternalID, suffix)] {
					independent = true
				}
			}
		}
		if independent {
			admitted = append(admitted, message)
		} else {
			required = append(required, message)
		}
	}
	return required, admitted
}

func retainChildCheckpointSource(run *recoveryRunState, archive, active []provider.ChatMessage, current sessiondb.ContextState) error {
	_, generation, _ := run.contextState()
	if current.Generation != generation {
		return fmt.Errorf("child checkpoint context generation changed")
	}
	required, controls := independentlyAdmittedContext(current.Messages)
	if !retainsContextSource(active, required) {
		return fmt.Errorf("child checkpoint does not retain committed context revision %d", current.Revision)
	}
	var err error
	active, err = mergeCommittedContextMessages(canonicalContextMessages(active), canonicalContextMessages(current.Messages))
	if err != nil {
		return err
	}
	archive, err = mergeCommittedContextMessages(archive, controls)
	if err != nil {
		return err
	}
	run.snapshot(archive, active)
	return nil
}

func publishChildRecovery(db *sql.DB, sessionID string, run *recoveryRunState, status recoverytypes.Status) error {
	// Another exact-owner control may commit between load and publication. Rebase
	// only those admitted facts, with bounded revision retry and no effect replay.
	for attempt := 0; attempt < 8; attempt++ {
		run.mu.Lock()
		owner, revision := run.contextOwnerID, run.contextRevision
		run.mu.Unlock()
		if owner == "" {
			return run.publish(db, sessionID, status)
		}
		current, found, err := sessiondb.LoadContextState(db, sessionID, owner)
		if err != nil {
			return err
		}
		if found && current.Revision != revision {
			archive, active := run.contexts()
			if current.Revision < revision {
				return &sessiondb.ContextRevisionConflictError{SessionID: sessionID, AgentID: owner, Expected: revision, Actual: current.Revision}
			}
			if err := retainChildCheckpointSource(run, archive, active, current); err != nil {
				return err
			}
			run.mu.Lock()
			run.contextRevision = current.Revision
			run.mu.Unlock()
		}
		err = run.publish(db, sessionID, status)
		var conflict *sessiondb.ContextRevisionConflictError
		if !errors.As(err, &conflict) {
			return err
		}
	}
	return fmt.Errorf("child context publication remained busy after bounded revision reconciliation")
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
	source, controls := independentlyAdmittedContext(s.activeContext)
	if !retainsContextSource(active, s.activeContext) {
		// Only independently admitted journal events may be absent from a root
		// execution snapshot. All other committed source facts remain mandatory.
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
