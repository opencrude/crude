package session

import (
	"errors"
	"time"

	"crude/provider"
	"crude/sessiondb"
)

func contextDelta(message provider.ChatMessage, meta sessiondb.DisplayMeta, turn int, createdAt time.Time, agentID string) sessiondb.ContextMessageDelta {
	// Display text and reference labels belong to the immutable transcript
	// metadata, not provider/current-context message bytes. Keeping them in the
	// context copy makes a later revision-fenced replacement conflict with the
	// same message reloaded from the normalized transcript representation.
	message.DisplayContent = ""
	message.ReferenceLabels = nil
	return sessiondb.ContextMessageDelta{
		Message: message, Meta: meta, Turn: turn, CreatedAt: createdAt.UnixNano(), AgentID: agentID,
	}
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
