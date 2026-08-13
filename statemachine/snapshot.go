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
	for _, key := range []string{"plan_step_statuses", "plan_step_labels"} {
		if raw, ok := state.ContextData[key].(map[string]any); ok {
			converted := make(map[string]string, len(raw))
			for name, value := range raw {
				if text, ok := value.(string); ok {
					converted[name] = text
				}
			}
			state.ContextData[key] = converted
		}
	}
	if rawSteps, ok := state.ContextData["plan_steps"].([]any); ok {
		steps := make([]map[string]any, 0, len(rawSteps))
		for _, raw := range rawSteps {
			if step, ok := raw.(map[string]any); ok {
				steps = append(steps, step)
			}
		}
		state.ContextData["plan_steps"] = steps
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
