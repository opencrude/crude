package agent

import (
	"context"
	"fmt"

	"crude/agent/prompt"
	"crude/agent/provider"
	"crude/agent/safety"
	"crude/agent/tools"
	ttypes "crude/types"
	"crude/types/toolresult"
)

// PermissionReview is advisory and request-local. Unknown/error decisions
// return to the existing human permission flow; no persistent grant is made.
// Reason explains every decision; for "ask" it tells the user why Auto
// Review did not decide, and is empty when Auto Review did not run.
type PermissionReview struct {
	Decision string
	Reason   string
}
type PermissionReviewer func(context.Context, provider.ToolCall, map[string]any, safety.CommandRisk, func(AgentEvent)) PermissionReview

// CommandApprovals is the session store of "Always allow" command answers,
// keyed by safety.CommandSafetyResult.ApprovalKeys.
type CommandApprovals interface {
	CommandApproved(keys []string) bool
	ApproveCommand(keys []string)
}

// reviewPermission asks Auto Review about one request. allowed reports that
// it may run; denied is the tool result of a reviewer denial; note explains
// to the user why Auto Review left the decision to them.
func (a *Agent) reviewPermission(ctx context.Context, tc provider.ToolCall, params map[string]any, risk safety.CommandRisk, emit func(AgentEvent)) (allowed bool, note string, denied *execResult) {
	if a.PermissionReviewer == nil || a.yoloEnabled() {
		return false, "", nil
	}
	review := a.PermissionReviewer(ctx, tc, params, risk, emit)
	if ctx.Err() != nil {
		return false, "", &execResult{id: tc.ID, name: tc.Function.Name, result: tools.ToolResult{Content: "Permission review interrupted because the active turn was cancelled.", Outcome: toolresult.Outcome{State: toolresult.Cancelled}}, toolError: true}
	}
	if review.Decision != "allow" && review.Decision != "deny" {
		return false, review.Reason, nil
	}
	tag := "reviewer-allowed"
	if review.Decision == "deny" {
		tag = "reviewer-denied"
	}
	emit(AgentEvent{Type: "permission_resolved", Data: map[string]any{"internal_id": tc.InternalID, "tool_id": tc.ID, "tool": tc.Function.Name, "decision": tag, "reason": review.Reason}})
	if review.Decision == "allow" {
		return true, "", nil
	}
	message := fmt.Sprintf("Automatic reviewer denied this operation. Reason: %s", review.Reason)
	return false, "", &execResult{id: tc.ID, name: tc.Function.Name, result: tools.ToolResult{Content: message, AgentHint: prompt.HintDenied, Outcome: toolresult.Outcome{State: toolresult.Denied}}, toolError: true}
}

// permissionWait is the outcome of a permission prompt.
type permissionWait struct {
	response ttypes.BlockResponse
	// answered reports a human answer in response.
	answered bool
	// reviewed reports that Auto Review decided after a policy change; the
	// request runs unless denied holds the reviewer's denial.
	reviewed bool
	denied   *execResult
}

// awaitPermission waits for the answer to a permission prompt. When the
// session permission policy changes while the prompt waits, review checks
// the request again under the new policy, so turning Auto Review on answers
// the requests that are already waiting. A human answer always wins.
func (a *Agent) awaitPermission(ctx context.Context, responses <-chan ttypes.BlockResponse, blockID string, review func() (bool, *execResult)) permissionWait {
	if responses == nil {
		return permissionWait{}
	}
	for {
		var changed <-chan struct{}
		if a.PermissionPolicyChanged != nil {
			changed = a.PermissionPolicyChanged()
		}
		select {
		case response, ok := <-responses:
			if !ok {
				return permissionWait{}
			}
			if response.RequestID == blockID {
				return permissionWait{response: response, answered: true}
			}
		case <-ctx.Done():
			return permissionWait{}
		case <-changed:
			allowed, denied := review()
			if ctx.Err() != nil {
				return permissionWait{}
			}
			if response, ok := takeBlockResponse(responses, blockID); ok {
				return permissionWait{response: response, answered: true}
			}
			if allowed || denied != nil {
				return permissionWait{reviewed: true, denied: denied}
			}
		}
	}
}

// takeBlockResponse returns an answer to blockID that is already queued.
func takeBlockResponse(responses <-chan ttypes.BlockResponse, blockID string) (ttypes.BlockResponse, bool) {
	for {
		select {
		case response, ok := <-responses:
			if !ok {
				return ttypes.BlockResponse{}, false
			}
			if response.RequestID == blockID {
				return response, true
			}
		default:
			return ttypes.BlockResponse{}, false
		}
	}
}
