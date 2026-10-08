package session

import (
	"errors"
	"fmt"
	"reflect"
	"time"

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
	run.mu.Lock()
	active := canonicalContextMessages(run.active)
	if run.sourceRevision == run.publishedRevision {
		active = append([]provider.ChatMessage(nil), s.activeContext...)
	} else if !retainsContextSource(active, s.activeContext) {
		run.mu.Unlock()
		return fmt.Errorf("recovery checkpoint does not retain committed context revision %d", s.contextRevision)
	}
	run.active = active
	run.contextRevision, run.contextGeneration, run.contextReason = s.contextRevision, max(uint64(1), s.contextGeneration), s.contextReason
	if reflect.DeepEqual(active, s.activeContext) {
		run.publishedRevision = run.sourceRevision
	}
	run.mu.Unlock()
	if err := run.publish(s.db, s.sessionID, status); err != nil {
		return err
	}
	s.contextRevision, s.contextGeneration, s.contextReason = run.contextState()
	_, s.activeContext = run.contexts()
	return nil
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
