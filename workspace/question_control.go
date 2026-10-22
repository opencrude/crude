package workspace

import (
	"crude/types"
	"crude/types/control"
)

// ResolveQuestionControl runs under the workspace actor after caller admission.
// The exact retained prompt prevents question input from approving permissions.
func (w *Workspace) ResolveQuestionControl(caller control.Caller, sessionID, agentID, blockID string, answers, questions map[string]string, cancelled bool) bool {
	prompt, ok := w.agentPrompts[agentID]
	owner := w.SessionForAgent(agentID)
	if !ok || !prompt.IsQuestion || prompt.BlockID != blockID || owner == nil || owner.SessionID != sessionID {
		return false
	}
	kind, id := "external", caller.ClientID
	if caller.Kind == control.AgentCaller {
		kind, id = "agent", caller.AgentID
	}
	reply := types.BlockResponse{RequestID: blockID, Outcome: types.BlockAnswered, AttributionKind: kind, AttributionID: id}
	if cancelled {
		reply.Outcome = types.BlockUserCancelled
	} else {
		text, ok := formatQuestionAnswers(answers, questions)
		if !ok {
			return false
		}
		reply.Value = text
	}
	return w.resolveAgentBlock(agentID, reply)
}
