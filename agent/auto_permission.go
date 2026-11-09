package agent

import (
	"context"
	"fmt"

	"crude/prompt"
	"crude/provider"
	"crude/safety"
	"crude/tools"
	"crude/types/toolresult"
)

// PermissionReview is advisory and request-local. Unknown/error decisions
// return to the existing human permission flow; no persistent grant is made.
type PermissionReview struct {
	Decision string
	Reason   string
}
type PermissionReviewer func(context.Context, provider.ToolCall, map[string]any, safety.CommandRisk, func(AgentEvent)) PermissionReview

func (a *Agent) reviewPermission(ctx context.Context, tc provider.ToolCall, params map[string]any, risk safety.CommandRisk, emit func(AgentEvent)) (bool, *execResult) {
	if a.PermissionReviewer == nil || a.yoloEnabled() {
		return false, nil
	}
	review := a.PermissionReviewer(ctx, tc, params, risk, emit)
	if ctx.Err() != nil {
		return false, &execResult{id: tc.ID, name: tc.Function.Name, result: tools.ToolResult{Content: "Permission review interrupted because the active turn was cancelled.", Outcome: toolresult.Outcome{State: toolresult.Cancelled}}, toolError: true}
	}
	if review.Decision != "allow" && review.Decision != "deny" {
		return false, nil
	}
	tag := "reviewer-allowed"
	if review.Decision == "deny" {
		tag = "reviewer-denied"
	}
	emit(AgentEvent{Type: "permission_resolved", Data: map[string]any{"internal_id": tc.InternalID, "tool_id": tc.ID, "tool": tc.Function.Name, "decision": tag, "reason": review.Reason}})
	if review.Decision == "allow" {
		return true, nil
	}
	message := fmt.Sprintf("Automatic reviewer denied this operation. Reason: %s", review.Reason)
	return false, &execResult{id: tc.ID, name: tc.Function.Name, result: tools.ToolResult{Content: message, AgentHint: prompt.HintDenied, Outcome: toolresult.Outcome{State: toolresult.Denied}}, toolError: true}
}
