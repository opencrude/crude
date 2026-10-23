package workspace

import (
	"errors"
	"fmt"

	"crude/types"
)

// GoalForAgent is the live goal of the session whose main agent is agentID.
// A goal belongs to the main agent; its children have none.
func (w *Workspace) GoalForAgent(agentID string) (types.Goal, bool) {
	sess := w.SessionsByAgent[agentID]
	if sess == nil || sess.backend == nil {
		return types.Goal{}, false
	}
	return sess.backend.Goal()
}

// Goal actions the user takes; the main agent settles its goal through its
// own tools.
const (
	GoalSet     = "set"
	GoalReplace = "replace"
	GoalEdit    = "edit"
	GoalPause   = "pause"
	GoalResume  = "resume"
	GoalClear   = "clear"
)

// PrepareGoalAction resolves the session on the workspace owner; the
// returned change persists off it. A cleared goal returns present false.
func (w *Workspace) PrepareGoalAction(agentID, action, objective string) (func() (types.Goal, bool, error), error) {
	sess := w.SessionsByAgent[agentID]
	if sess == nil || sess.backend == nil {
		return nil, errors.New("goals belong to a session's main agent")
	}
	backend := sess.backend
	switch action {
	case GoalSet, GoalReplace:
		return func() (types.Goal, bool, error) {
			// /goal sets no budget: a budget is set only through create_goal
			// when the user asks the agent for one.
			goal, err := backend.SetGoal(objective, 0, action == GoalReplace)
			return goal, err == nil, err
		}, nil
	case GoalEdit:
		return func() (types.Goal, bool, error) {
			goal, err := backend.EditGoal(objective)
			return goal, err == nil, err
		}, nil
	case GoalPause:
		return func() (types.Goal, bool, error) {
			goal, err := backend.PauseGoal("Paused from Crude. Resume it with /goal resume.")
			return goal, err == nil, err
		}, nil
	case GoalResume:
		return func() (types.Goal, bool, error) { goal, err := backend.ResumeGoal(); return goal, err == nil, err }, nil
	case GoalClear:
		return func() (types.Goal, bool, error) { return types.Goal{}, false, backend.ClearGoal() }, nil
	}
	return nil, fmt.Errorf("unknown goal action %q", action)
}

// GoalAction is PrepareGoalAction run in place, for callers off the owner.
func (w *Workspace) GoalAction(agentID, action, objective string) (types.Goal, bool, error) {
	run, err := w.PrepareGoalAction(agentID, action, objective)
	if err != nil {
		return types.Goal{}, false, err
	}
	return run()
}

// applyGoalEvent records the goal the session last published. Its transcript
// row arrives separately as a status_row the session stored.
func (a *AgentState) applyGoalEvent(data any) {
	if goal, ok := data.(types.Goal); ok {
		a.Goal = &goal
		return
	}
	a.Goal = nil
}
