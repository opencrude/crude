package sessiondb

import (
	"database/sql"
	"encoding/json"
	"time"

	"crude/provider"
)

type WireState struct {
	Epoch    uint64
	Reason   string
	Messages []provider.ChatMessage
}

type wireMessageRecord struct {
	Message provider.ChatMessage `json:"message"`
	Hidden  bool                 `json:"hidden,omitempty"`
}

func SaveWireState(db *sql.DB, sessionID string, state WireState) error {
	records := make([]wireMessageRecord, len(state.Messages))
	for i, message := range state.Messages {
		records[i] = wireMessageRecord{Message: message, Hidden: message.WireHidden}
	}
	payload, err := json.Marshal(records)
	if err != nil {
		return err
	}
	_, err = db.Exec(`
		INSERT INTO session_wire_state (session_id, epoch, reason, messages_json, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(session_id) DO UPDATE SET
			epoch = excluded.epoch,
			reason = excluded.reason,
			messages_json = excluded.messages_json,
			updated_at = excluded.updated_at
	`, sessionID, state.Epoch, state.Reason, payload, time.Now().UnixNano())
	return err
}

func LoadWireState(db *sql.DB, sessionID string) (WireState, bool, error) {
	var state WireState
	var payload []byte
	err := db.QueryRow(`SELECT epoch, reason, messages_json FROM session_wire_state WHERE session_id = ?`, sessionID).
		Scan(&state.Epoch, &state.Reason, &payload)
	if err == sql.ErrNoRows {
		return WireState{}, false, nil
	}
	if err != nil {
		return WireState{}, false, err
	}
	var records []wireMessageRecord
	if err := json.Unmarshal(payload, &records); err != nil {
		return WireState{}, false, err
	}
	state.Messages = make([]provider.ChatMessage, len(records))
	for i, record := range records {
		state.Messages[i] = record.Message
		state.Messages[i].WireHidden = record.Hidden
	}
	return state, true, nil
}
