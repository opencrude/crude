package workspace

import (
	"crude/types/control"
	"crude/types/conversation"
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
	reply := conversation.BlockResponse{RequestID: blockID, Outcome: conversation.BlockAnswered, AttributionKind: kind, AttributionID: id}
	if cancelled {
		reply.Outcome = conversation.BlockUserCancelled
	} else {
		text, ok := formatQuestionAnswers(answers, questions)
		if !ok {
			return false
		}
		reply.Value = text
	}
	return w.resolveAgentBlock(agentID, reply)
}
