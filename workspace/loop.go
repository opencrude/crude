package workspace

import (
	"errors"
	"fmt"
	"time"

	"crude/types"
)

// LoopsForAgent returns the main agent's session-owned schedules.
func (w *Workspace) LoopsForAgent(agentID string) []types.Loop {
	if node := w.AgentMgr.Get(agentID); node == nil || node.Parent != nil {
		return nil
	}
	sess := w.SessionsByAgent[agentID]
	if sess == nil || sess.backend == nil {
		return nil
	}
	return sess.backend.Loops()
}

// PrepareLoopAction resolves the exact session on the workspace owner; the
// returned operation persists on a worker.
func (w *Workspace) PrepareLoopAction(agentID, action, id, task string, interval time.Duration) (func() error, error) {
	if node := w.AgentMgr.Get(agentID); node == nil || node.Parent != nil {
		return nil, errors.New("loops belong to a session's main agent")
	}
	sess := w.SessionsByAgent[agentID]
	if sess == nil || sess.backend == nil {
		return nil, errors.New("loops belong to a session's main agent")
	}
	backend := sess.backend
	switch action {
	case "create":
		return func() error {
			_, err := backend.CreateLoop(interval, task)
			return err
		}, nil
	case "pause", "resume", "clear":
		return func() error { return backend.ChangeLoop(action, id) }, nil
	default:
		return nil, fmt.Errorf("unknown loop action %q", action)
	}
}

func (w *Workspace) LoopAction(agentID, action, id, task string, interval time.Duration) error {
	run, err := w.PrepareLoopAction(agentID, action, id, task, interval)
	if err != nil {
		return err
	}
	return run()
}
