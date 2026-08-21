package statemachine

import (
	"strings"
	"time"

	"crude/logger"
)

// GoalExecutionLog records one goal step execution for auditing.
type GoalExecutionLog struct {
	GoalID      string
	GoalIntent  string
	Status      StepStatus
	BlockerType *BlockingConditionType
	Timestamp   time.Time
	Response    string
}

// ConversationState holds all mutable state for one conversation.
type ConversationState struct {
	ID             string
	ConversationID string

	ThreadRoots map[string]*Goal
	FocusGoalID string
	ResumeStack []string

	ContextData      map[string]any
	ExecutionHistory []GoalExecutionLog

	CreatedAt     time.Time
	LastUpdatedAt time.Time
	GoalTTL       time.Duration

	logger *logger.Logger
}

// NewConversationState creates a fresh ConversationState.
func NewConversationState(conversationID string, lg *logger.Logger) *ConversationState {
	return &ConversationState{
		ID:             newID(),
		ConversationID: conversationID,
		ThreadRoots:    map[string]*Goal{},
		ResumeStack:    []string{},
		ContextData:    map[string]any{},
		CreatedAt:      time.Now().UTC(),
		LastUpdatedAt:  time.Now().UTC(),
		GoalTTL:        2 * time.Hour,
		logger:         lg,
	}
}

func (s *ConversationState) logIf(fn func(lg *logger.Logger)) {
	if s.logger != nil {
		fn(s.logger)
	}
}

// GetFocusGoal returns the currently focused root goal (and touches it).
func (s *ConversationState) GetFocusGoal() *Goal {
	if s.FocusGoalID == "" {
		return nil
	}
	root, ok := s.ThreadRoots[s.FocusGoalID]
	if !ok {
		return nil
	}
	root.Touch()
	return root
}

// HasActiveGoal returns true if there is a focused, non-completed goal.
func (s *ConversationState) HasActiveGoal() bool {
	focus := s.GetFocusGoal()
	return focus != nil && focus.Status != GoalCompleted
}

// HasBlocker returns true if the current goal has a blocker.
func (s *ConversationState) HasBlocker() bool {
	g := s.GetCurrentGoal()
	return g != nil && g.CurrentBlocker != nil
}

// GetCurrentGoal returns the innermost active goal (top of sub-goal stack, or
// the focus goal itself if the sub-goal stack is empty).
func (s *ConversationState) GetCurrentGoal() *Goal {
	focus := s.GetFocusGoal()
	if focus == nil {
		return nil
	}
	if top := focus.peekSubGoal(); top != nil {
		return top
	}
	return focus
}

// AddToContext stores a key-value pair in the shared conversation context.
func (s *ConversationState) AddToContext(key string, value any) {
	s.ContextData[key] = value
	s.LastUpdatedAt = time.Now().UTC()
}

// GetFromContext retrieves a value from the shared conversation context.
func (s *ConversationState) GetFromContext(key string) (any, bool) {
	v, ok := s.ContextData[key]
	return v, ok
}

// LogExecution appends a step execution record to the audit history.
func (s *ConversationState) LogExecution(goal *Goal, result *StepResult) {
	var blockerType *BlockingConditionType
	if result.Blocker != nil {
		bt := result.Blocker.Type
		blockerType = &bt
	}
	s.ExecutionHistory = append(s.ExecutionHistory, GoalExecutionLog{
		GoalID:      goal.ID,
		GoalIntent:  goal.Intent,
		Status:      result.Status,
		BlockerType: blockerType,
		Timestamp:   time.Now().UTC(),
		Response:    result.Response,
	})
	s.LastUpdatedAt = time.Now().UTC()
	s.logIf(func(lg *logger.Logger) {
		lg.Debug("STATE", "log_execution", "goal_id", goal.ID, "intent", goal.Intent, "status", result.Status.String(), "outcome", result.OutcomeCode)
	})
}

// PushToResumeStack pushes a goal ID onto the resume stack (deduplicating first).
func (s *ConversationState) PushToResumeStack(goalID string) {
	filtered := s.ResumeStack[:0]
	for _, id := range s.ResumeStack {
		if id != goalID {
			filtered = append(filtered, id)
		}
	}
	s.ResumeStack = append(filtered, goalID)
	s.logIf(func(lg *logger.Logger) {
		lg.Debug("STATE", "push_resume", "goal_id", goalID, "stack_depth", len(s.ResumeStack))
	})
}

// PopFromResumeStack pops the topmost valid (non-terminal) goal ID.
func (s *ConversationState) PopFromResumeStack() string {
	for len(s.ResumeStack) > 0 {
		top := s.ResumeStack[len(s.ResumeStack)-1]
		s.ResumeStack = s.ResumeStack[:len(s.ResumeStack)-1]
		g, ok := s.ThreadRoots[top]
		if ok && g.Status != GoalCompleted && g.Status != GoalFailed {
			s.logIf(func(lg *logger.Logger) {
				lg.Debug("STATE", "pop_resume", "goal_id", top, "stack_depth", len(s.ResumeStack))
			})
			return top
		}
		s.logIf(func(lg *logger.Logger) {
			lg.Debug("STATE", "pop_resume_skipped", "goal_id", top, "status", g.Status.String())
		})
	}
	return ""
}

// PeekResumeStack returns the topmost valid goal ID without removing it.
func (s *ConversationState) PeekResumeStack() string {
	for i := len(s.ResumeStack) - 1; i >= 0; i-- {
		id := s.ResumeStack[i]
		g, ok := s.ThreadRoots[id]
		if ok && g.Status != GoalCompleted && g.Status != GoalFailed {
			return id
		}
		// Trim invalid entries from the top.
		s.ResumeStack = s.ResumeStack[:i]
	}
	return ""
}

// CleanupExpiredGoals removes goals that have exceeded their TTL.
// Returns the number of goals removed.
func (s *ConversationState) CleanupExpiredGoals() int {
	var expired []string
	for id, g := range s.ThreadRoots {
		if g.IsExpired(s.GoalTTL) {
			expired = append(expired, id)
		}
	}
	for _, id := range expired {
		delete(s.ThreadRoots, id)
		filtered := s.ResumeStack[:0]
		for _, rid := range s.ResumeStack {
			if rid != id {
				filtered = append(filtered, rid)
			}
		}
		s.ResumeStack = filtered
	}
	if len(expired) > 0 {
		s.logIf(func(lg *logger.Logger) {
			lg.Info("STATE", "cleanup_expired", "expired_count", len(expired), "remaining", len(s.ThreadRoots))
		})
	}
	return len(expired)
}

// TransferSlotsToGoal copies transferable slots from source to target
// if target's intent is in source's RelatedIntents list.
func (s *ConversationState) TransferSlotsToGoal(source, target *Goal) {
	related := false
	for _, ri := range source.RelatedIntents {
		if ri == target.Intent {
			related = true
			break
		}
	}
	if !related {
		s.logIf(func(lg *logger.Logger) {
			lg.Debug("STATE", "transfer_skipped", "source", source.Intent, "target", target.Intent, "reason", "not_related")
		})
		return
	}
	transferred := 0
	for k, v := range source.GetTransferableSlotData() {
		if _, exists := target.Slots[k]; !exists {
			target.Slots[k] = v
			transferred++
		}
	}
	s.logIf(func(lg *logger.Logger) {
		lg.Debug("STATE", "transfer_slots", "source", source.Intent, "target", target.Intent, "transferred", transferred)
	})
}

// GoalContext

// GoalContext is passed to plugins during goal step execution.
// It provides convenient accessors over the underlying Goal and ConversationState.
type GoalContext struct {
	CurrentGoal       *Goal
	ConversationState *ConversationState
	UserQuery         string
	LastResponse      string
	ImageBytes        []byte
	ImageMimeType     string
	ImageURL          string
	ChatHistory       []string
}

// NewGoalContext creates a GoalContext for the given goal and state.
func NewGoalContext(goal *Goal, state *ConversationState, userQuery string) *GoalContext {
	return &GoalContext{
		CurrentGoal:       goal,
		ConversationState: state,
		UserQuery:         userQuery,
	}
}

// GetSlot retrieves a slot value by name.
func (c *GoalContext) GetSlot(slotName string) (any, bool) {
	v, ok := c.CurrentGoal.Slots[slotName]
	return v, ok
}

// SetSlot stores a slot value.
func (c *GoalContext) SetSlot(slotName string, value any) {
	c.CurrentGoal.Slots[slotName] = value
}

// AddToContext stores a value in the shared conversation context.
func (c *GoalContext) AddToContext(key string, value any) {
	c.ConversationState.AddToContext(key, value)
}

// GetFromContext retrieves a value from the shared conversation context.
func (c *GoalContext) GetFromContext(key string) (any, bool) {
	return c.ConversationState.GetFromContext(key)
}

// GetChatHistoryString returns the last 20 history lines joined by newlines.
func (c *GoalContext) GetChatHistoryString() string {
	h := c.ChatHistory
	if len(h) > 20 {
		h = h[len(h)-20:]
	}
	return strings.Join(h, "\n")
}
