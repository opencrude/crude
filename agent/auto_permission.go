package agent

import (
	"context"
	"fmt"

	"crude/provider"
	"crude/safety"
	"crude/tools"
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
		return false, &execResult{id: tc.ID, name: tc.Function.Name, result: tools.ToolResult{Content: "Permission review interrupted because the active turn was cancelled."}, toolError: true}
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
	message := fmt.Sprintf("[SAFETY BLOCK] The automatic permission reviewer denied this operation (not a human denial). Reason: %s. Do not retry variants to bypass the decision; ask the user or choose a safer approach.", review.Reason)
	emit(AgentEvent{Type: "tool_result", Data: map[string]any{"internal_id": tc.InternalID, "tool_id": tc.ID, "name": tc.Function.Name, "result": message, "error": "true"}})
	return false, &execResult{id: tc.ID, name: tc.Function.Name, result: tools.ToolResult{Content: message}, toolError: true}
}
