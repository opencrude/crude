package turnloop

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"crude/agent/provider"
	"crude/agent/tools"
	"crude/types/conversation"
)

func TestPlanInvestigationHostPermissionNeedsExactHumanAnswer(t *testing.T) {
	for _, tool := range []string{"execute_command", "async_execute_command", "exec_command"} {
		t.Run(tool, func(t *testing.T) {
			for _, tc := range []struct {
				value, actor, saved string
				outcome             conversation.BlockOutcome
			}{
				{conversation.PlanInvestigationHostAllow, "", "allow", conversation.BlockAnswered},
				{"deny", "", "deny", conversation.BlockAnswered},
				{"allow", "", "", conversation.BlockAnswered},
				{"always", "", "", conversation.BlockAnswered},
				{conversation.PlanInvestigationHostAllow, "agent", "", conversation.BlockAnswered},
				{conversation.PlanInvestigationHostAllow, "external", "", conversation.BlockAnswered},
				{"", "", "", conversation.BlockUserCancelled},
			} {
				t.Run(fmt.Sprintf("%s/%s/%s", tc.value, tc.actor, tc.outcome), func(t *testing.T) {
					a := newPermissionLifecycleAgent(t)
					a.Yolo = true
					saved, ran := "", false
					root := t.TempDir()
					a.PlanInvestigation = &PlanInvestigationPolicy{HostCommand: func(context.Context, string, map[string]any) (*PlanInvestigationHostRequest, error) {
						return &PlanInvestigationHostRequest{Directory: root, Decide: func(value string) error { saved = value; return nil }}, nil
					}}
					a.ToolFn = func(context.Context, string, map[string]any) (tools.ToolResult, error) {
						ran = true
						return tools.ToolResult{Content: "ok"}, nil
					}
					responses := make(chan conversation.BlockResponse, 1)
					call := commandPermissionCall("go test ./...")
					call.Function.Name = tool
					if tool == "exec_command" {
						call.Function.Arguments = `{"cmd":"go test ./..."}`
					}
					if tool == "async_execute_command" {
						call.Function.Arguments = `{"command":"go test ./...", "handle":"plan-test"}`
					}
					asks := 0
					a.executeOneToolCall(t.Context(), call, func(event AgentEvent) {
						if event.Type != "blocking" {
							return
						}
						data := event.Data.(map[string]string)
						asks++
						if data["operation"] != conversation.PlanInvestigationHostOperation || data["block_id"] == call.InternalID || data["directory"] != root {
							t.Errorf("not an exact investigation request: %+v", data)
						}
						responses <- conversation.BlockResponse{RequestID: data["block_id"], Outcome: tc.outcome, Value: tc.value, AttributionKind: tc.actor}
					}, responses, new(string))
					if asks != 1 || saved != tc.saved || ran != (tc.saved == "allow") {
						t.Fatalf("asked=%d decision=%q ran=%t, want 1/%q/%t", asks, saved, ran, tc.saved, tc.saved == "allow")
					}
				})
			}
		})
	}
}

func TestPlanInvestigationHostAndCommandSafetyUseSeparateRequests(t *testing.T) {
	a := newPermissionLifecycleAgent(t)
	a.PlanInvestigation = &PlanInvestigationPolicy{HostCommand: func(context.Context, string, map[string]any) (*PlanInvestigationHostRequest, error) {
		return &PlanInvestigationHostRequest{Decide: func(string) error { return nil }}, nil
	}}
	a.ToolFn = func(context.Context, string, map[string]any) (tools.ToolResult, error) {
		t.Error("command ran after a separate safety denial")
		return tools.ToolResult{}, nil
	}
	call := commandPermissionCall("unknown-plan-project-command")
	responses := make(chan conversation.BlockResponse, 2)
	var requestIDs []string
	a.executeOneToolCall(t.Context(), call, func(event AgentEvent) {
		if event.Type != "blocking" {
			return
		}
		data := event.Data.(map[string]string)
		requestIDs = append(requestIDs, data["block_id"])
		if data["operation"] == conversation.PlanInvestigationHostOperation {
			responses <- conversation.BlockResponse{RequestID: data["block_id"], Outcome: conversation.BlockAnswered, Value: conversation.PlanInvestigationHostAllow}
			return
		}
		responses <- conversation.BlockResponse{RequestID: call.InternalID + ":plan-host", Outcome: conversation.BlockAnswered, Value: "allow"}
		responses <- conversation.BlockResponse{RequestID: call.InternalID, Outcome: conversation.BlockAnswered, Value: "deny"}
	}, responses, new(string))
	if !slices.Equal(requestIDs, []string{call.InternalID + ":plan-host", call.InternalID}) {
		t.Fatalf("requests = %v", requestIDs)
	}
}

func TestPlanInvestigationNativeCommandKeepsHumanResponseLane(t *testing.T) {
	a := newPermissionLifecycleAgent(t)
	a.PlanInvestigation = &PlanInvestigationPolicy{}
	call := provider.ToolCall{Function: provider.FunctionCall{Name: "exec_command", Arguments: `{"cmd":"go test ./..."}`}}
	if a.parallelCommandAllowed(call) {
		t.Fatal("native test bypasses the shared permission response lane")
	}
}
