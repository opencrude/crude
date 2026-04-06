package statemachine

import (
	"fmt"
	"log"
)

// ConversationStateMachine manages ConversationState instances keyed by conversation ID.
// It is NOT goroutine-safe; callers are responsible for synchronization if needed.
type ConversationStateMachine struct {
	states map[string]*ConversationState
	logger *log.Logger
}

// NewConversationStateMachine creates a state machine.
// If logger is nil, the default logger is used.
func NewConversationStateMachine(logger *log.Logger) *ConversationStateMachine {
	if logger == nil {
		logger = log.Default()
	}
	return &ConversationStateMachine{
		states: map[string]*ConversationState{},
		logger: logger,
	}
}

// GetOrCreateState retrieves or lazily creates the state for the given conversation ID.
func (m *ConversationStateMachine) GetOrCreateState(conversationID string) *ConversationState {
	if s, ok := m.states[conversationID]; ok {
		return s
	}
	s := NewConversationState(conversationID)
	m.states[conversationID] = s
	m.logger.Printf("[sm] created state for %s", conversationID)
	return s
}

// HasActiveGoal returns true if there is an active goal for the conversation.
func (m *ConversationStateMachine) HasActiveGoal(conversationID string) bool {
	return m.GetOrCreateState(conversationID).HasActiveGoal()
}

// HasBlocker returns true if the current goal is blocked.
func (m *ConversationStateMachine) HasBlocker(conversationID string) bool {
	return m.GetOrCreateState(conversationID).HasBlocker()
}

// CreateGoal creates a new goal, either as a root or a sub-goal of the current focus.
func (m *ConversationStateMachine) CreateGoal(
	conversationID, intent, originalQuery string,
	priority GoalPriority,
	transferSlots bool,
) *Goal {
	state := m.GetOrCreateState(conversationID)

	cleaned := state.CleanupExpiredGoals()
	if cleaned > 0 {
		m.logger.Printf("[sm] cleaned %d expired goals for %s", cleaned, conversationID)
	}

	goal := NewGoal(intent, originalQuery, priority, true)
	focus := state.GetFocusGoal()

	if focus == nil {
		state.ThreadRoots[goal.ID] = goal
		state.FocusGoalID = goal.ID
		m.logger.Printf("[sm] root goal %q (id=%s) for %s", intent, goal.ID, conversationID)
	} else {
		focus.pushSubGoal(goal)
		m.logger.Printf("[sm] sub-goal %q (id=%s) under %q for %s", intent, goal.ID, focus.Intent, conversationID)
	}

	if transferSlots && focus != nil {
		state.TransferSlotsToGoal(focus, goal)
		m.logger.Printf("[sm] transferred slots from %q to %q", focus.Intent, goal.Intent)
	}

	goal.Status = GoalActive
	return goal
}

// PauseWithBlocker marks the current goal as blocked.
func (m *ConversationStateMachine) PauseWithBlocker(conversationID string, blocker *BlockingCondition) {
	state := m.GetOrCreateState(conversationID)
	g := state.GetCurrentGoal()
	if g != nil {
		g.CurrentBlocker = blocker
		g.Status = GoalBlocked
		m.logger.Printf("[sm] paused %q (id=%s) blocker=%d", g.Intent, g.ID, blocker.Type)
	} else {
		m.logger.Printf("[sm] pause: no current goal for %s", conversationID)
	}
}

// TryResolveBlocker clears the blocker on the current goal if one exists.
// Returns true if a blocker was cleared.
func (m *ConversationStateMachine) TryResolveBlocker(conversationID, _ string) bool {
	state := m.GetOrCreateState(conversationID)
	g := state.GetCurrentGoal()
	if g == nil || g.CurrentBlocker == nil {
		return false
	}
	g.CurrentBlocker = nil
	g.Status = GoalActive
	m.logger.Printf("[sm] resolved blocker for %q (id=%s) in %s", g.Intent, g.ID, conversationID)
	return true
}

// GetActiveGoal returns the innermost active goal (nil if none).
func (m *ConversationStateMachine) GetActiveGoal(conversationID string) *Goal {
	return m.GetOrCreateState(conversationID).GetCurrentGoal()
}

// GetFocusGoal returns the focused root goal (nil if none).
func (m *ConversationStateMachine) GetFocusGoal(conversationID string) *Goal {
	return m.GetOrCreateState(conversationID).GetFocusGoal()
}

// GetCurrentBlocker returns the blocker on the current goal, or nil.
func (m *ConversationStateMachine) GetCurrentBlocker(conversationID string) *BlockingCondition {
	state := m.GetOrCreateState(conversationID)
	if g := state.GetCurrentGoal(); g != nil {
		return g.CurrentBlocker
	}
	return nil
}

// CompleteCurrentGoal marks the current goal as completed and stores result data in context.
func (m *ConversationStateMachine) CompleteCurrentGoal(conversationID string, resultData map[string]any) {
	state := m.GetOrCreateState(conversationID)
	focus := state.GetFocusGoal()
	g := state.GetCurrentGoal()

	if g == nil {
		m.logger.Printf("[sm] complete: no current goal for %s", conversationID)
		return
	}

	g.Status = GoalCompleted

	for k, v := range resultData {
		state.AddToContext(fmt.Sprintf("%s.%s", g.Intent, k), v)
	}

	if focus != nil && focus.peekSubGoal() == g {
		focus.popSubGoal()
		m.logger.Printf("[sm] completed sub-goal %q for %s", g.Intent, conversationID)
	} else if focus == g {
		delete(state.ThreadRoots, g.ID)
		state.FocusGoalID = ""
		m.logger.Printf("[sm] completed root goal %q for %s", g.Intent, conversationID)
	}
}

// FailCurrentGoal marks the current goal as failed.
func (m *ConversationStateMachine) FailCurrentGoal(conversationID, reason string) {
	state := m.GetOrCreateState(conversationID)
	focus := state.GetFocusGoal()
	g := state.GetCurrentGoal()

	if g == nil {
		m.logger.Printf("[sm] fail: no active goal for %s", conversationID)
		return
	}

	g.Status = GoalFailed
	m.logger.Printf("[sm] failed goal %q for %s: %s", g.Intent, conversationID, reason)

	if focus != nil && focus.peekSubGoal() == g {
		focus.popSubGoal()
	} else if focus == g {
		delete(state.ThreadRoots, g.ID)
		state.FocusGoalID = ""
	}
}

// ParkCurrentThread suspends the current thread by pushing it onto the resume stack.
func (m *ConversationStateMachine) ParkCurrentThread(conversationID string) {
	state := m.GetOrCreateState(conversationID)
	if state.FocusGoalID != "" {
		m.logger.Printf("[sm] parking thread %s for %s", state.FocusGoalID, conversationID)
		state.PushToResumeStack(state.FocusGoalID)
		state.FocusGoalID = ""
	}
}

// ResumeGoal sets the focus to a specific goal ID (must exist in ThreadRoots).
func (m *ConversationStateMachine) ResumeGoal(conversationID, goalID string) {
	state := m.GetOrCreateState(conversationID)
	if g, ok := state.ThreadRoots[goalID]; ok {
		g.Touch()
		state.FocusGoalID = goalID
		m.logger.Printf("[sm] resumed goal %s for %s", goalID, conversationID)
	}
}

// ResumeLastParkedGoal pops and resumes the last parked goal.
// Returns nil if there are no parked goals.
func (m *ConversationStateMachine) ResumeLastParkedGoal(conversationID string) *Goal {
	state := m.GetOrCreateState(conversationID)
	lastID := state.PopFromResumeStack()
	if lastID == "" {
		return nil
	}
	state.FocusGoalID = lastID
	g := state.ThreadRoots[lastID]
	g.Touch()
	m.logger.Printf("[sm] resumed parked goal %s (%q) for %s", lastID, g.Intent, conversationID)
	return g
}

// CanResumePreviousGoal returns true if there is at least one parked goal.
func (m *ConversationStateMachine) CanResumePreviousGoal(conversationID string) bool {
	return m.GetOrCreateState(conversationID).PeekResumeStack() != ""
}

// GetSuspendedGoals returns all root goals that are NOT the current focus.
func (m *ConversationStateMachine) GetSuspendedGoals(conversationID string) []*Goal {
	state := m.GetOrCreateState(conversationID)
	var result []*Goal
	for id, g := range state.ThreadRoots {
		if id != state.FocusGoalID {
			result = append(result, g)
		}
	}
	return result
}

// CancelGoal removes a goal from the thread roots.
func (m *ConversationStateMachine) CancelGoal(conversationID, goalID string) {
	state := m.GetOrCreateState(conversationID)
	if _, ok := state.ThreadRoots[goalID]; ok {
		delete(state.ThreadRoots, goalID)
		if state.FocusGoalID == goalID {
			state.FocusGoalID = ""
		}
		m.logger.Printf("[sm] cancelled goal %s for %s", goalID, conversationID)
	}
}

// ClearState removes the entire conversation state.
func (m *ConversationStateMachine) ClearState(conversationID string) {
	if _, ok := m.states[conversationID]; ok {
		delete(m.states, conversationID)
		m.logger.Printf("[sm] cleared state for %s", conversationID)
	}
}

// GetState returns the raw conversation state, or nil if it doesn't exist yet.
func (m *ConversationStateMachine) GetState(conversationID string) *ConversationState {
	return m.states[conversationID]
}
