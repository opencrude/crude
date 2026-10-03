package workspace

import (
	"fmt"

	"crude/fileio"
)

func (w *Workspace) PrepareAsyncCommandTermination(agentID string, ref fileio.AsyncCmdRef) (func() error, error) {
	if w == nil || w.AgentMgr == nil {
		return nil, fmt.Errorf("background command owner is no longer available")
	}
	root := w.AgentMgr.OrchestratorFor(agentID)
	if root == nil || root.ID != agentID {
		return nil, fmt.Errorf("background command owner is no longer available")
	}
	owner := w.SessionsByAgent[agentID]
	if owner == nil || owner.backend == nil {
		return nil, fmt.Errorf("background command session is no longer available")
	}
	backend := owner.backend
	return func() error { return backend.KillAsyncCommand(ref) }, nil
}
