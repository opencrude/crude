package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"crude/provider"
	"crude/safety"
	"crude/tools"
	ttypes "crude/types"
	"crude/types/control"
)

// A mutating Crude control in manual mode must reach the human through the
// batch executor's shared response channel instead of resolving as a denial.
func TestCrudeControlApprovalPromptsHumanThroughBatchExecutor(t *testing.T) {
	a := newPermissionLifecycleAgent(t)
	request := control.Request{ID: "control-1", CommandID: "settings.set", Input: json.RawMessage(`{"path":"editor.tab_size","scope":"workspace","value":4}`)}
	approval := control.NewApproval(request, control.Descriptor{ID: "settings.set", Scope: control.Domain, Effect: control.Write})
	calls := 0
	a.ToolFn = func(ctx context.Context, _ string, _ map[string]any) (tools.ToolResult, error) {
		calls++
		if origin, ok := approval.Authorized(ctx); !ok || origin != "human" {
			return tools.ToolResult{}, approval
		}
		return tools.ToolResult{Content: `{"request_id":"control-1","operation_id":"control-1","state":"applied"}`}, nil
	}
	responses := make(chan ttypes.BlockResponse, 1)
	var block map[string]string
	call := provider.ToolCall{ID: "call", InternalID: "internal", Function: provider.FunctionCall{Name: "crude_control", Arguments: `{"command":"settings.set","arguments":{"scope":"workspace","path":"editor.tab_size","value":4}}`}}
	results := a.executeToolCalls(t.Context(), []provider.ToolCall{call}, func(event AgentEvent) {
		if event.Type == "blocking" {
			block = event.Data.(map[string]string)
			responses <- ttypes.BlockResponse{RequestID: "internal", Value: "allow", Outcome: ttypes.BlockAnswered}
		}
	}, responses, new(string), 1)
	result := <-results
	if result.toolError || calls != 2 || block == nil {
		t.Fatalf("control was not approved by a human prompt: calls=%d block=%v result=%+v", calls, block, result)
	}
	if block["operation"] != "Write" || block["risk"] != "project-write" || !strings.Contains(block["question"], "settings.set") {
		t.Fatalf("control prompt misclassified: %v", block)
	}
	if strings.Contains(block["reason"], "{") || !strings.Contains(block["reason"], "path: editor.tab_size") {
		t.Fatalf("control prompt shows raw JSON arguments: %q", block["reason"])
	}
}

func TestCrudeControlResolvedApprovalMatrix(t *testing.T) {
	for _, effect := range []control.Effect{control.Write, control.Delete, control.Execute} {
		for _, mode := range []string{"manual", "deny", "cancel", "reviewer", "yolo"} {
			t.Run(string(effect)+"/"+mode, func(t *testing.T) {
				a := newPermissionLifecycleAgent(t)
				a.Yolo = mode == "yolo"
				request := control.Request{ID: "exact", CommandID: "terminal.operation", Input: json.RawMessage(`{"id":"terminal-1"}`)}
				approval := control.NewApproval(request, control.Descriptor{ID: request.CommandID, Scope: control.Domain, Effect: effect})
				calls, effects, blocks := 0, 0, 0
				wantRisk := safety.RiskUnknown
				if effect == control.Write {
					wantRisk = safety.RiskProjectWrite
				}
				if effect == control.Delete {
					wantRisk = safety.RiskDestructive
				}
				if mode == "reviewer" {
					a.PermissionReviewer = func(_ context.Context, _ provider.ToolCall, evidence map[string]any, risk safety.CommandRisk, _ func(AgentEvent)) PermissionReview {
						if risk != wantRisk || evidence["effect"] != effect {
							t.Errorf("wrong reviewer evidence: %v %v", evidence, risk)
						}
						return PermissionReview{Decision: "allow"}
					}
				}
				a.ToolFn = func(ctx context.Context, _ string, _ map[string]any) (tools.ToolResult, error) {
					calls++
					origin, ok := approval.Authorized(ctx)
					if !ok {
						return tools.ToolResult{}, approval
					}
					want := mode
					if mode == "manual" {
						want = "human"
					}
					if origin != want {
						t.Errorf("authorization origin %q want %q", origin, want)
					}
					effects++
					return tools.ToolResult{Content: `{"request_id":"exact","state":"committed"}`}, nil
				}
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				responses := make(chan ttypes.BlockResponse, 1)
				call := provider.ToolCall{ID: "call", InternalID: "internal", Function: provider.FunctionCall{Name: "crude_control", Arguments: `{"command":"terminal.operation","arguments":{"id":"terminal-1"}}`}}
				results := a.executeToolCalls(ctx, []provider.ToolCall{call}, func(event AgentEvent) {
					if event.Type != "blocking" {
						return
					}
					blocks++
					data := event.Data.(map[string]string)
					if data["operation"] == "Read" || data["risk"] != wantRisk.String() {
						t.Errorf("wrong prompt: %v", data)
					}
					if mode == "cancel" {
						cancel()
						return
					}
					value := "allow"
					if mode == "deny" {
						value = "deny"
					}
					responses <- ttypes.BlockResponse{RequestID: "internal", Value: value, Outcome: ttypes.BlockAnswered}
				}, responses, new(string), 1)
				result := <-results
				denied := mode == "deny" || mode == "cancel"
				if denied && (effects != 0 || calls != 1 || !result.toolError) || !denied && (effects != 1 || calls != 2 || result.toolError) {
					t.Fatalf("calls=%d effects=%d result=%+v", calls, effects, result)
				}
				if (mode == "yolo" || mode == "reviewer") && blocks != 0 {
					t.Fatal("unnecessary human prompt")
				}
			})
		}
	}
}
