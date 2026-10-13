package workspace

import (
	"crude/provider"
	"errors"
)

// PrepareControlInputAdmission shares normal immutable admission, but does not
// use the human-only queue projection. Busy/compacting owners keep the draft.
func (w *Workspace) PrepareControlInputAdmission(sessionID, agentID string, input provider.UserInput, revision uint64) (*InputAdmissionIntent, error) {
	if w == nil || w.AgentMgr == nil {
		return nil, errors.New("control input workspace unavailable")
	}
	owner := w.SessionForAgent(agentID)
	node := w.AgentMgr.Get(agentID)
	if owner == nil || owner.SessionID != sessionID || owner.backend == nil || node == nil || !node.IsOrchestrator() || node.State == nil {
		return nil, errors.New("exact root input owner unavailable")
	}
	if input.EffectiveOrigin() != provider.OriginRuntimeContext {
		return nil, errors.New("control input must not be attributed as human")
	}
	if owner.backend.IsTurnActive() || len(owner.backend.QueuedInputs()) != 0 || node.State.ActiveCompaction.OperationID != "" {
		return nil, errors.New("control input owner busy or compacting; draft retained")
	}
	return w.PrepareAgentInputAdmission(agentID, input, revision)
}
