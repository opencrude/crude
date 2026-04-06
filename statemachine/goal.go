package statemachine

import "time"

// GoalPriority controls execution order when multiple goals compete.
type GoalPriority int

const (
	PriorityCritical GoalPriority = iota
	PriorityHigh
	PriorityNormal
	PriorityLow
)

// GoalStatus is the lifecycle state of a goal.
type GoalStatus int

const (
	GoalPending GoalStatus = iota
	GoalActive
	GoalBlocked
	GoalCompleted
	GoalFailed
)

// Goal represents a single user intent being pursued.
type Goal struct {
	ID            string
	Intent        string
	OriginalQuery string
	Priority      GoalPriority
	IsBlocking    bool
	CreatedAt     time.Time
	LastAccessedAt time.Time

	Slots          map[string]any
	Status         GoalStatus
	LastPrompt     string
	CurrentBlocker *BlockingCondition

	// SubGoalStack is used as a stack; last element is the top.
	SubGoalStack []*Goal

	RelatedIntents       []string
	TransferableSlots    []string
	PendingRequiredSlots []string
	LastOutcomeCode      string
	LastErrorCode        string
	ExtractionAttempts   map[string]int
	AttachedImageURLs    []string
}

// NewGoal creates a new goal with the given intent and query.
func NewGoal(intent, originalQuery string, priority GoalPriority, isBlocking bool) *Goal {
	return &Goal{
		ID:                 newID(),
		Intent:             intent,
		OriginalQuery:      originalQuery,
		Priority:           priority,
		IsBlocking:         isBlocking,
		Status:             GoalPending,
		CreatedAt:          time.Now().UTC(),
		LastAccessedAt:     time.Now().UTC(),
		Slots:              map[string]any{},
		ExtractionAttempts: map[string]int{},
	}
}

// Touch updates the last-accessed timestamp.
func (g *Goal) Touch() {
	g.LastAccessedAt = time.Now().UTC()
}

// IsExpired returns true if the goal has not been accessed within ttl.
func (g *Goal) IsExpired(ttl time.Duration) bool {
	return time.Since(g.LastAccessedAt) > ttl
}

// GetTransferableSlotData returns the subset of slots marked as transferable.
func (g *Goal) GetTransferableSlotData() map[string]any {
	result := make(map[string]any, len(g.TransferableSlots))
	for _, slot := range g.TransferableSlots {
		if v, ok := g.Slots[slot]; ok {
			result[slot] = v
		}
	}
	return result
}

// pushSubGoal appends a sub-goal to the top of the stack.
func (g *Goal) pushSubGoal(sub *Goal) {
	g.SubGoalStack = append(g.SubGoalStack, sub)
}

// peekSubGoal returns the top sub-goal without removing it (nil if empty).
func (g *Goal) peekSubGoal() *Goal {
	if len(g.SubGoalStack) == 0 {
		return nil
	}
	return g.SubGoalStack[len(g.SubGoalStack)-1]
}

// popSubGoal removes and returns the top sub-goal (nil if empty).
func (g *Goal) popSubGoal() *Goal {
	if len(g.SubGoalStack) == 0 {
		return nil
	}
	top := g.SubGoalStack[len(g.SubGoalStack)-1]
	g.SubGoalStack = g.SubGoalStack[:len(g.SubGoalStack)-1]
	return top
}
