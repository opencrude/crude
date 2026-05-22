package statemachine

import (
	"fmt"
	"sync"

	"crude/logger"
)

type ConversationStateMachine struct {
	mu     sync.Mutex
	states map[string]*ConversationState
	logger *logger.Logger
}

func NewConversationStateMachine(lg *logger.Logger) *ConversationStateMachine {
	if lg == nil {
		panic("statemachine: logger cannot be nil")
	}
	return &ConversationStateMachine{
		states: map[string]*ConversationState{},
		logger: lg,
	}
}

func (m *ConversationStateMachine) getOrCreateState(conversationID string) *ConversationState {
	if s, ok := m.states[conversationID]; ok {
		return s
	}
	s := NewConversationState(conversationID, m.logger)
	m.states[conversationID] = s
	m.logger.Info("STATE", "goal_created", "goal_id", conversationID, "type", "state", "description", conversationID, "priority", "info")
	return s
}

func (m *ConversationStateMachine) GetOrCreateState(conversationID string) *ConversationState {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.getOrCreateState(conversationID)
}

func (m *ConversationStateMachine) HasActiveGoal(conversationID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.getOrCreateState(conversationID)
	has := state.HasActiveGoal()
	m.logger.Debug("STATE", "has_active_goal", "conversation_id", conversationID, "has", has)
	return has
}

func (m *ConversationStateMachine) HasBlocker(conversationID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.getOrCreateState(conversationID)
	has := state.HasBlocker()
	m.logger.Debug("STATE", "has_blocker", "conversation_id", conversationID, "has", has)
	return has
}

func (m *ConversationStateMachine) CreateGoal(
	conversationID, intent, originalQuery string,
	priority GoalPriority,
	transferSlots bool,
) *Goal {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.getOrCreateState(conversationID)

	cleaned := state.CleanupExpiredGoals()
	if cleaned > 0 {
		m.logger.Info("STATE", "goal_created", "goal_id", conversationID, "type", "cleanup", "description", fmt.Sprintf("cleaned %d expired goals", cleaned), "priority", "info")
	}

	goal := NewGoal(intent, originalQuery, priority, true)
	focus := state.GetFocusGoal()

	if focus == nil {
		state.ThreadRoots[goal.ID] = goal
		state.FocusGoalID = goal.ID
		m.logger.Info("STATE", "goal_created", "goal_id", goal.ID, "type", "root", "description", intent, "priority", priorityLabel(priority))
	} else {
		focus.pushSubGoal(goal)
		m.logger.Info("STATE", "goal_created", "goal_id", goal.ID, "type", "sub", "description", intent, "priority", priorityLabel(priority))
	}

	if transferSlots && focus != nil {
		state.TransferSlotsToGoal(focus, goal)
		m.logger.Info("STATE", "goal_created", "goal_id", goal.ID, "type", "slot_transfer", "description", fmt.Sprintf("transferred slots from %q to %q", focus.Intent, goal.Intent), "priority", priorityLabel(priority))
	}

	goal.Status = GoalActive
	return goal
}

func (m *ConversationStateMachine) PauseWithBlocker(conversationID string, blocker *BlockingCondition) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.getOrCreateState(conversationID)
	g := state.GetCurrentGoal()
	if g != nil {
		g.CurrentBlocker = blocker
		g.Status = GoalBlocked
		m.logger.Info("STATE", "blocked", "goal_id", g.ID, "blocker_type", blocker.Type.String(), "reason", blocker.Message)
	} else {
		m.logger.Info("STATE", "blocked", "goal_id", "", "blocker_type", blocker.Type.String(), "reason", blocker.Message, "conversation_id", conversationID)
	}
}

func (m *ConversationStateMachine) TryResolveBlocker(conversationID, _ string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.getOrCreateState(conversationID)
	g := state.GetCurrentGoal()
	if g == nil || g.CurrentBlocker == nil {
		return false
	}
	g.CurrentBlocker = nil
	g.Status = GoalActive
	m.logger.Info("STATE", "unblocked", "goal_id", g.ID, "reason", "blocker resolved")
	return true
}

func (m *ConversationStateMachine) GetActiveGoal(conversationID string) *Goal {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.getOrCreateState(conversationID).GetCurrentGoal()
}

func (m *ConversationStateMachine) GetFocusGoal(conversationID string) *Goal {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.getOrCreateState(conversationID).GetFocusGoal()
}

func (m *ConversationStateMachine) GetCurrentBlocker(conversationID string) *BlockingCondition {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.getOrCreateState(conversationID)
	if g := state.GetCurrentGoal(); g != nil {
		return g.CurrentBlocker
	}
	return nil
}

func (m *ConversationStateMachine) CompleteCurrentGoal(conversationID string, resultData map[string]any) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.getOrCreateState(conversationID)
	focus := state.GetFocusGoal()
	g := state.GetCurrentGoal()

	if g == nil {
		m.logger.Info("STATE", "goal_complete", "goal_id", "", "result", "no current goal", "conversation_id", conversationID)
		return
	}

	g.Status = GoalCompleted

	for k, v := range resultData {
		state.AddToContext(fmt.Sprintf("%s.%s", g.Intent, k), v)
	}

	if focus != nil && focus.peekSubGoal() == g {
		focus.popSubGoal()
		m.logger.Info("STATE", "goal_complete", "goal_id", g.ID, "result", "sub-goal completed")
	} else if focus == g {
		delete(state.ThreadRoots, g.ID)
		state.FocusGoalID = ""
		m.logger.Info("STATE", "goal_complete", "goal_id", g.ID, "result", "root goal completed")
	}
}

func (m *ConversationStateMachine) CompleteGoalByID(conversationID, goalID string, resultData map[string]any) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.getOrCreateState(conversationID)

	if g, ok := state.ThreadRoots[goalID]; ok {
		g.Status = GoalCompleted
		for k, v := range resultData {
			state.AddToContext(fmt.Sprintf("%s.%s", g.Intent, k), v)
		}
		delete(state.ThreadRoots, goalID)
		if state.FocusGoalID == goalID {
			state.FocusGoalID = ""
			for id := range state.ThreadRoots {
				state.FocusGoalID = id
				break
			}
		}
		m.logger.Info("STATE", "goal_complete", "goal_id", goalID, "result", "root goal completed by ID")
		return true
	}

	for _, root := range state.ThreadRoots {
		if root.completeSubGoalByID(goalID, resultData) {
			m.logger.Info("STATE", "goal_complete", "goal_id", goalID, "result", "sub-goal completed by ID")
			return true
		}
	}

	return false
}

func (m *ConversationStateMachine) FailCurrentGoal(conversationID, reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.getOrCreateState(conversationID)
	focus := state.GetFocusGoal()
	g := state.GetCurrentGoal()

	if g == nil {
		m.logger.Error("STATE", "goal_failed", "goal_id", "", "reason", "no active goal", "conversation_id", conversationID)
		return
	}

	g.Status = GoalFailed
	m.logger.Error("STATE", "goal_failed", "goal_id", g.ID, "reason", reason)

	if focus != nil && focus.peekSubGoal() == g {
		focus.popSubGoal()
	} else if focus == g {
		delete(state.ThreadRoots, g.ID)
		state.FocusGoalID = ""
	}
}

func (m *ConversationStateMachine) ParkCurrentThread(conversationID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.getOrCreateState(conversationID)
	if state.FocusGoalID != "" {
		m.logger.Info("STATE", "blocked", "goal_id", state.FocusGoalID, "blocker_type", "parked", "reason", fmt.Sprintf("parking thread %s for %s", state.FocusGoalID, conversationID))
		state.PushToResumeStack(state.FocusGoalID)
		state.FocusGoalID = ""
	}
}

func (m *ConversationStateMachine) ResumeGoal(conversationID, goalID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.getOrCreateState(conversationID)
	if g, ok := state.ThreadRoots[goalID]; ok {
		g.Touch()
		state.FocusGoalID = goalID
		m.logger.Info("STATE", "goal_start", "goal_id", goalID, "type", "resumed")
	}
}

func (m *ConversationStateMachine) ResumeLastParkedGoal(conversationID string) *Goal {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.getOrCreateState(conversationID)
	lastID := state.PopFromResumeStack()
	if lastID == "" {
		return nil
	}
	state.FocusGoalID = lastID
	g := state.ThreadRoots[lastID]
	g.Touch()
	m.logger.Info("STATE", "goal_start", "goal_id", lastID, "type", "resumed_parked")
	return g
}

func (m *ConversationStateMachine) CanResumePreviousGoal(conversationID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.getOrCreateState(conversationID).PeekResumeStack() != ""
}

func (m *ConversationStateMachine) GetSuspendedGoals(conversationID string) []*Goal {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.getOrCreateState(conversationID)
	var result []*Goal
	for id, g := range state.ThreadRoots {
		if id != state.FocusGoalID {
			result = append(result, g)
		}
	}
	return result
}

func (m *ConversationStateMachine) CancelGoal(conversationID, goalID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.getOrCreateState(conversationID)
	if _, ok := state.ThreadRoots[goalID]; ok {
		delete(state.ThreadRoots, goalID)
		if state.FocusGoalID == goalID {
			state.FocusGoalID = ""
		}
		m.logger.Info("STATE", "cancelled", "goal_id", goalID, "conversation_id", conversationID)
	}
}

func (m *ConversationStateMachine) ClearState(conversationID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if state, ok := m.states[conversationID]; ok {
		m.logger.Info("STATE", "clear", "goal_id", state.FocusGoalID, "conversation_id", conversationID, "root_count", len(state.ThreadRoots))
		delete(m.states, conversationID)
	}
}

func (m *ConversationStateMachine) GetState(conversationID string) *ConversationState {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.states[conversationID]
	if !ok {
		m.logger.Debug("STATE", "get_state_not_found", "conversation_id", conversationID)
		return nil
	}
	return s
}

func priorityLabel(p GoalPriority) string {
	switch p {
	case PriorityLow:
		return "low"
	case PriorityNormal:
		return "normal"
	case PriorityHigh:
		return "high"
	case PriorityCritical:
		return "critical"
	default:
		return "unknown"
	}
}
