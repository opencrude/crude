package statemachine

import (
	"encoding/json"
	"fmt"
)

// Snapshot serializes one conversation's durable goal, blocker, plan, and
// context state. Logger/runtime synchronization fields are intentionally
// excluded by their unexported representation.
func (m *ConversationStateMachine) Snapshot(conversationID string) ([]byte, error) {
	if m == nil {
		return nil, fmt.Errorf("statemachine: nil machine")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.states[conversationID]
	if state == nil {
		return nil, nil
	}
	data, err := json.Marshal(state)
	if err != nil {
		return nil, fmt.Errorf("statemachine: snapshot %q: %w", conversationID, err)
	}
	return data, nil
}

// Restore installs a previously serialized conversation snapshot.
func (m *ConversationStateMachine) Restore(conversationID string, data []byte) error {
	if m == nil {
		return fmt.Errorf("statemachine: nil machine")
	}
	if len(data) == 0 {
		return nil
	}
	var state ConversationState
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("statemachine: restore %q: %w", conversationID, err)
	}
	if state.ThreadRoots == nil {
		state.ThreadRoots = make(map[string]*Goal)
	}
	if state.ContextData == nil {
		state.ContextData = make(map[string]any)
	}
	if state.ConversationID == "" {
		state.ConversationID = conversationID
	}
	state.logger = m.logger
	m.mu.Lock()
	m.states[conversationID] = &state
	m.mu.Unlock()
	return nil
}
