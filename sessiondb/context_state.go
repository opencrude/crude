package sessiondb

import (
	"database/sql"
	"encoding/json"
	"time"

	"crude/provider"
)

type ContextState struct {
	AgentID    string
	Generation uint64
	Reason     string
	Messages   []provider.ChatMessage
}

func SaveContextState(db *sql.DB, sessionID, agentID string, state ContextState) error {
	payload, err := json.Marshal(state.Messages)
	if err != nil {
		return err
	}
	_, err = db.Exec(`
		INSERT INTO agent_context_state (session_id, agent_id, generation, reason, messages_json, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(session_id, agent_id) DO UPDATE SET
			generation = excluded.generation,
			reason = excluded.reason,
			messages_json = excluded.messages_json,
			updated_at = excluded.updated_at
	`, sessionID, agentID, state.Generation, state.Reason, payload, time.Now().UnixNano())
	return err
}

func LoadContextState(db *sql.DB, sessionID, agentID string) (ContextState, bool, error) {
	state := ContextState{AgentID: agentID}
	var payload []byte
	err := db.QueryRow(`SELECT generation, reason, messages_json FROM agent_context_state WHERE session_id = ? AND agent_id = ?`, sessionID, agentID).
		Scan(&state.Generation, &state.Reason, &payload)
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
