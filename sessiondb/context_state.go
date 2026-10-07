package sessiondb

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"crude/provider"
)

type ContextState struct {
	AgentID    string
	Revision   uint64
	Generation uint64
	Reason     string
	Messages   []provider.ChatMessage
}

type ContextReplacement struct {
	Generation uint64
	Reason     string
	Messages   []provider.ChatMessage
}

type ContextMessageDelta struct {
	Message   provider.ChatMessage
	Meta      DisplayMeta
	Turn      int
	CreatedAt int64
	AgentID   string
}

type ContextMutation struct {
	Replace *ContextReplacement
	Append  []ContextMessageDelta
}

type ContextPublication struct {
	SessionID        string
	AgentID          string
	RuntimeAgentID   string
	ExpectedRevision uint64
	Mutation         ContextMutation
	TranscriptAppend []ContextMessageDelta
	Recovery         *RecoveryCheckpoint
	Admissions       []CollaborationAdmission
	Compaction       *CompactionRecord
	ArchivePrevious  bool
}

type ContextRevisionConflictError struct {
	SessionID, AgentID string
	Expected, Actual   uint64
}

type ContextMessageConflictError struct{ InternalID, Location string }

func (e *ContextMessageConflictError) Error() string {
	return fmt.Sprintf("sessiondb: context message %q conflicts with immutable %s", e.InternalID, e.Location)
}

func (e *ContextRevisionConflictError) Error() string {
	return fmt.Sprintf("sessiondb: context revision conflict for %s/%s: expected %d, actual %d", e.SessionID, e.AgentID, e.Expected, e.Actual)
}

// PublishContext is the single SQLite transaction owner for current context,
// recovery, local collaboration receipts, and compaction publication.
func PublishContext(db *sql.DB, p ContextPublication) (ContextState, error) {
	if db == nil || p.SessionID == "" || p.AgentID == "" {
		return ContextState{}, errors.New("sessiondb: invalid context publication")
	}
	if p.Mutation.Replace != nil && len(p.Mutation.Append) != 0 {
		return ContextState{}, errors.New("sessiondb: context publication cannot append and replace together")
	}
	tx, err := db.Begin()
	if err != nil {
		return ContextState{}, err
	}
	defer func() { _ = tx.Rollback() }()
	current, exists, raw, err := loadContextStateTx(tx, p.SessionID, p.AgentID)
	if err != nil {
		return ContextState{}, err
	}
	actual := uint64(0)
	if exists {
		actual = current.Revision
	}
	if actual != p.ExpectedRevision {
		return ContextState{}, &ContextRevisionConflictError{p.SessionID, p.AgentID, p.ExpectedRevision, actual}
	}
	if err := validateRelatedPublication(p); err != nil {
		return ContextState{}, err
	}
	if p.ArchivePrevious && exists {
		if err := registerImmutableMessagesTx(tx, p.SessionID, p.AgentID, current.Messages, "archive"); err != nil {
			return ContextState{}, err
		}
		var archivedReason string
		var archivedRaw []byte
		err := tx.QueryRow(`SELECT reason,messages_json FROM agent_context_generations WHERE session_id=? AND agent_id=? AND generation=?`, p.SessionID, p.AgentID, current.Generation).Scan(&archivedReason, &archivedRaw)
		if err == nil {
			if archivedReason != current.Reason || !reflect.DeepEqual(archivedRaw, raw) {
				return ContextState{}, errors.New("sessiondb: context archive identity conflict")
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return ContextState{}, err
		} else if _, err := tx.Exec(`INSERT INTO agent_context_generations(session_id,agent_id,generation,reason,messages_json,created_at) VALUES(?,?,?,?,?,?)`, p.SessionID, p.AgentID, current.Generation, current.Reason, raw, time.Now().UnixNano()); err != nil {
			return ContextState{}, err
		}
	}
	next := current
	next.AgentID = p.AgentID
	transcriptChanged := false
	for _, delta := range p.TranscriptAppend {
		appended, appendErr := appendTranscriptMessageTx(tx, p.SessionID, p.AgentID, delta)
		if appendErr != nil {
			return ContextState{}, appendErr
		}
		transcriptChanged = transcriptChanged || appended
	}
	semanticChanged := false
	if p.Mutation.Replace != nil {
		replacement := p.Mutation.Replace
		if replacement.Generation == 0 || replacement.Reason == "" {
			return ContextState{}, errors.New("sessiondb: invalid context replacement")
		}
		if err := validateReplacementMessagesTx(tx, p.SessionID, p.AgentID, replacement.Messages); err != nil {
			return ContextState{}, err
		}
		next.Generation, next.Reason = replacement.Generation, replacement.Reason
		next.Messages = append([]provider.ChatMessage(nil), replacement.Messages...)
		semanticChanged = !exists || current.Generation != next.Generation || current.Reason != next.Reason || !reflect.DeepEqual(current.Messages, next.Messages)
	} else {
		if !exists {
			next.Generation, next.Reason = 1, "append"
		}
		for _, delta := range p.Mutation.Append {
			appended, appendErr := appendContextMessageTx(tx, p.SessionID, &next, delta)
			if appendErr != nil {
				return ContextState{}, appendErr
			}
			semanticChanged = semanticChanged || appended
		}
	}
	next.Revision = actual + 1
	if !semanticChanged && !transcriptChanged && !p.ArchivePrevious && p.Recovery == nil && len(p.Admissions) == 0 && p.Compaction == nil {
		return current, nil
	}
	payload, err := json.Marshal(next.Messages)
	if err != nil {
		return ContextState{}, err
	}
	result, err := tx.Exec(`INSERT INTO agent_context_state(session_id,agent_id,revision,generation,reason,messages_json,updated_at) VALUES(?,?,?,?,?,?,?) ON CONFLICT(session_id,agent_id) DO UPDATE SET revision=excluded.revision,generation=excluded.generation,reason=excluded.reason,messages_json=excluded.messages_json,updated_at=excluded.updated_at WHERE agent_context_state.revision=?`, p.SessionID, p.AgentID, next.Revision, next.Generation, next.Reason, payload, time.Now().UnixNano(), actual)
	if err != nil {
		return ContextState{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return ContextState{}, err
	}
	if affected != 1 {
		return ContextState{}, &ContextRevisionConflictError{p.SessionID, p.AgentID, p.ExpectedRevision, actual}
	}
	if p.Recovery != nil {
		checkpoint := *p.Recovery
		checkpoint.ContextOwnerID, checkpoint.ContextRevision = p.AgentID, next.Revision
		checkpoint.ContextGeneration, checkpoint.ContextReason = next.Generation, next.Reason
		checkpoint.ActiveContext = append([]provider.ChatMessage(nil), next.Messages...)
		if err := saveRecoveryCheckpointTx(tx, p.SessionID, checkpoint); err != nil {
			return ContextState{}, err
		}
	}
	if err := saveAdmissionsTx(tx, p.SessionID, p.RuntimeAgentID, p.Admissions); err != nil {
		return ContextState{}, err
	}
	if p.Compaction != nil {
		if err := saveCompactionRecordTx(tx, *p.Compaction); err != nil {
			return ContextState{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return ContextState{}, err
	}
	return next, nil
}

func appendTranscriptMessageTx(tx *sql.Tx, sessionID, contextOwnerID string, delta ContextMessageDelta) (bool, error) {
	if delta.Message.InternalID == "" || delta.CreatedAt == 0 {
		return false, errors.New("sessiondb: transcript append requires message id and timestamp")
	}
	if delta.Meta.ContextOnly {
		return false, errors.New("sessiondb: transcript append cannot be context-only")
	}
	if err := registerImmutableMessageTx(tx, sessionID, contextOwnerID, delta.Message, "transcript"); err != nil {
		return false, err
	}
	desired := canonicalPersistedMessage(persistedMessage{Message: delta.Message, Meta: delta.Meta, Turn: delta.Turn, CreatedAt: delta.CreatedAt, AgentID: delta.AgentID})
	existing, exists, err := loadPersistedMessageByInternalIDTx(tx, delta.Message.InternalID)
	if err != nil {
		return false, err
	}
	if exists {
		if !persistedMessageEqual(existing, transcriptPersistedProjection(desired)) {
			return false, fmt.Errorf("%w: got=%#v want=%#v", &ContextMessageConflictError{delta.Message.InternalID, "transcript"}, existing, desired)
		}
		return false, nil
	}
	if err := appendMessageWithMetaTx(tx, sessionID, delta); err != nil {
		return false, err
	}
	return true, nil
}

func validateReplacementMessagesTx(tx *sql.Tx, sessionID, agentID string, messages []provider.ChatMessage) error {
	seen := make(map[string]provider.ChatMessage, len(messages))
	for _, message := range messages {
		if message.InternalID == "" {
			continue
		}
		if existing, ok := seen[message.InternalID]; ok {
			if !reflect.DeepEqual(existing, message) {
				return &ContextMessageConflictError{message.InternalID, "replacement"}
			}
			return &ContextMessageConflictError{message.InternalID, "duplicate replacement"}
		}
		seen[message.InternalID] = message
		if err := registerImmutableMessageTx(tx, sessionID, agentID, message, "replacement"); err != nil {
			return err
		}
		transcript, exists, err := loadPersistedMessageByInternalIDTx(tx, message.InternalID)
		if err != nil {
			return err
		}
		canonicalMessage := canonicalPersistedMessage(persistedMessage{Message: message}).Message
		transcriptProjection := transcriptPersistedProjection(persistedMessage{Message: canonicalMessage}).Message
		if exists && !reflect.DeepEqual(transcript.Message, transcriptProjection) {
			return &ContextMessageConflictError{message.InternalID, "transcript"}
		}
	}
	return nil
}

func SaveContextState(db *sql.DB, sessionID, agentID string, state ContextState) error {
	_, err := PublishContext(db, ContextPublication{SessionID: sessionID, AgentID: agentID, ExpectedRevision: state.Revision, Mutation: ContextMutation{Replace: &ContextReplacement{Generation: state.Generation, Reason: state.Reason, Messages: state.Messages}}})
	return err
}

func LoadContextState(db *sql.DB, sessionID, agentID string) (ContextState, bool, error) {
	state := ContextState{AgentID: agentID}
	var payload []byte
	err := db.QueryRow(`SELECT revision, generation, reason, messages_json FROM agent_context_state WHERE session_id = ? AND agent_id = ?`, sessionID, agentID).
		Scan(&state.Revision, &state.Generation, &state.Reason, &payload)
	if err == sql.ErrNoRows {
		return ContextState{}, false, nil
	}
	if err != nil {
		return ContextState{}, false, err
	}
	if err := json.Unmarshal(payload, &state.Messages); err != nil {
		return ContextState{}, false, err
	}
	return state, true, nil
}

func loadContextStateTx(tx *sql.Tx, sessionID, agentID string) (ContextState, bool, []byte, error) {
	state := ContextState{AgentID: agentID}
	var payload []byte
	err := tx.QueryRow(`SELECT revision,generation,reason,messages_json FROM agent_context_state WHERE session_id=? AND agent_id=?`, sessionID, agentID).Scan(&state.Revision, &state.Generation, &state.Reason, &payload)
	if errors.Is(err, sql.ErrNoRows) {
		return ContextState{}, false, nil, nil
	}
	if err != nil {
		return ContextState{}, false, nil, err
	}
	if err := json.Unmarshal(payload, &state.Messages); err != nil {
		return ContextState{}, false, nil, err
	}
	return state, true, payload, nil
}

func appendContextMessageTx(tx *sql.Tx, sessionID string, state *ContextState, delta ContextMessageDelta) (bool, error) {
	if delta.Message.InternalID == "" || delta.CreatedAt == 0 {
		return false, errors.New("sessiondb: context append requires message id and timestamp")
	}
	desired := canonicalPersistedMessage(persistedMessage{Message: delta.Message, Meta: delta.Meta, Turn: delta.Turn, CreatedAt: delta.CreatedAt, AgentID: delta.AgentID})
	if err := registerImmutableMessageTx(tx, sessionID, state.AgentID, delta.Message, "context"); err != nil {
		return false, err
	}
	inContext := false
	for _, existing := range state.Messages {
		if existing.InternalID != delta.Message.InternalID {
			continue
		}
		if !reflect.DeepEqual(existing, delta.Message) {
			return false, &ContextMessageConflictError{delta.Message.InternalID, "context"}
		}
		inContext = true
		break
	}
	transcript, inTranscript, err := loadPersistedMessageByInternalIDTx(tx, delta.Message.InternalID)
	if err != nil {
		return false, err
	}
	if delta.Meta.ContextOnly {
		if inTranscript {
			return false, &ContextMessageConflictError{delta.Message.InternalID, "transcript for context-only message"}
		}
	} else if inTranscript && !persistedMessageEqual(transcript, transcriptPersistedProjection(desired)) {
		return false, fmt.Errorf("%w: got=%#v want=%#v", &ContextMessageConflictError{delta.Message.InternalID, "transcript"}, transcript, desired)
	}
	if inContext {
		if !delta.Meta.ContextOnly && !inTranscript {
			return false, &ContextMessageConflictError{delta.Message.InternalID, "missing transcript"}
		}
		return false, nil
	}
	if !delta.Meta.ContextOnly && !inTranscript {
		if err := appendMessageWithMetaTx(tx, sessionID, delta); err != nil {
			return false, err
		}
	}
	state.Messages = append(state.Messages, delta.Message)
	return true, nil
}

type immutableMessageIdentity struct {
	Message             provider.ChatMessage `json:"message"`
	NilReferenceLabels  bool                 `json:"nil_reference_labels"`
	NilToolCalls        bool                 `json:"nil_tool_calls"`
	NilReasoningDetails bool                 `json:"nil_reasoning_details"`
	NilProviderItems    bool                 `json:"nil_provider_items"`
}

func immutableMessagePayload(message provider.ChatMessage) ([]byte, error) {
	message = canonicalPersistedMessage(persistedMessage{Message: message}).Message
	identity := immutableMessageIdentity{
		Message: message, NilReferenceLabels: message.ReferenceLabels == nil, NilToolCalls: message.ToolCalls == nil,
		NilReasoningDetails: message.ReasoningDetails == nil, NilProviderItems: message.ProviderItems == nil,
	}
	return json.Marshal(identity)
}

func registerImmutableMessagesTx(tx *sql.Tx, sessionID, agentID string, messages []provider.ChatMessage, location string) error {
	for _, message := range messages {
		if message.InternalID == "" {
			continue
		}
		if err := registerImmutableMessageTx(tx, sessionID, agentID, message, location); err != nil {
			return err
		}
	}
	return nil
}

func registerImmutableMessageTx(tx *sql.Tx, sessionID, agentID string, message provider.ChatMessage, location string) error {
	if message.InternalID == "" {
		return nil
	}
	payload, err := immutableMessagePayload(message)
	if err != nil {
		return err
	}
	var storedSessionID, storedAgentID string
	var storedPayload []byte
	err = tx.QueryRow(`SELECT session_id,agent_id,message_json FROM immutable_message_identities WHERE internal_id=?`, message.InternalID).Scan(&storedSessionID, &storedAgentID, &storedPayload)
	if errors.Is(err, sql.ErrNoRows) {
		_, err = tx.Exec(`INSERT INTO immutable_message_identities(internal_id,session_id,agent_id,message_json,created_at) VALUES(?,?,?,?,?)`, message.InternalID, sessionID, agentID, payload, time.Now().UnixNano())
		return err
	}
	if err != nil {
		return err
	}
	if storedSessionID != sessionID || storedAgentID != agentID || !reflect.DeepEqual(storedPayload, payload) {
		return &ContextMessageConflictError{message.InternalID, location}
	}
	return nil
}

func validateRelatedPublication(p ContextPublication) error {
	hasRuntimeRecord := p.Recovery != nil || len(p.Admissions) != 0 || p.Compaction != nil
	if hasRuntimeRecord && p.RuntimeAgentID == "" {
		return errors.New("sessiondb: related publication requires runtime agent")
	}
	if p.Recovery != nil {
		checkpoint := p.Recovery
		if checkpoint.Status.ID == "" || checkpoint.Status.AgentID == "" || checkpoint.Status.Generation == 0 {
			return errors.New("sessiondb: recovery publication identity conflict")
		}
		if checkpoint.Status.AgentID != p.RuntimeAgentID {
			return errors.New("sessiondb: recovery runtime agent conflict")
		}
		if checkpoint.ContextOwnerID != "" && checkpoint.ContextOwnerID != p.AgentID {
			return errors.New("sessiondb: recovery context owner conflict")
		}
	}
	for _, admission := range p.Admissions {
		if admission.SessionID != p.SessionID || admission.AgentID != p.RuntimeAgentID {
			return errors.New("sessiondb: collaboration admission owner conflict")
		}
	}
	if p.Compaction != nil && (p.Compaction.SessionID != p.SessionID || p.Compaction.AgentID != p.RuntimeAgentID) {
		return errors.New("sessiondb: compaction owner conflict")
	}
	return nil
}
