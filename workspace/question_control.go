package workspace

import (
	"crude/types"
	"crude/types/control"
	"fmt"
	"strings"
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
		if len(answers) == 0 {
			return false
		}
		var text strings.Builder
		for i := 1; i <= len(answers); i++ {
			key := fmt.Sprint(i)
			answer, ok := answers[key]
			if !ok || strings.TrimSpace(answer) == "" {
				return false
			}
			if i > 1 {
				text.WriteString("\n\n")
			}
			if q := questions[key]; q != "" {
				fmt.Fprintf(&text, "Q%d: %s\n", i, q)
			}
			fmt.Fprintf(&text, "A%d: %s", i, answer)
		}
		reply.Value = text.String()
	}
	return w.resolveAgentBlock(agentID, reply)
}
