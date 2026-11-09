package agent

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"crude/prompt"
	"crude/provider"
	"crude/safety"
	"crude/tools"
	"crude/types/toolresult"
)

func TestSettledToolPublishesOneFactualResultAndKeepsModelGuidance(t *testing.T) {
	for _, state := range []toolresult.State{toolresult.Succeeded, toolresult.Failed, toolresult.Denied, toolresult.Cancelled, toolresult.NotRun, toolresult.Uncertain} {
		t.Run(string(state), func(t *testing.T) {
			a := newPermissionLifecycleAgent(t)
			a.Yolo = true
			a.ToolFn = func(context.Context, string, map[string]any) (tools.ToolResult, error) {
				result := tools.ToolResult{Content: "Retained operation evidence", AgentHint: prompt.HintUncertain,
					Outcome: toolresult.Outcome{State: state, Summary: "Exact factual reason"}}
				if state != toolresult.Succeeded {
					return result, errors.New("Exact factual reason")
				}
				return result, nil
			}
			var settled []map[string]any
			result := a.executeOneToolCall(t.Context(), provider.ToolCall{ID: "call", InternalID: "stable-call",
				Function: provider.FunctionCall{Name: "read_file", Arguments: `{"path":"file"}`}}, func(event AgentEvent) {
				if event.Type == "tool_result" {
					settled = append(settled, event.Data.(map[string]any))
				}
				if event.Type == "tool_progress" {
					if event.Data.(map[string]any)["phase"] == "error" {
						t.Fatal("duplicate error progress projection")
					}
				}
			}, nil, new(string))
			if len(settled) != 1 || settled[0]["result"] != "Retained operation evidence" ||
				!reflect.DeepEqual(settled[0]["outcome"], result.result.Outcome) || result.result.Outcome.State != state || result.result.Outcome.CallID != "stable-call" {
				t.Fatalf("result boundary lost or duplicated facts: %+v %+v", settled, result)
			}
			message := toolResultMessage(result, 1)
			if message.DisplayContent != "Retained operation evidence" || strings.Contains(message.DisplayContent, prompt.HintUncertain) ||
				!strings.Contains(message.ContentString(), prompt.HintUncertain) {
				t.Fatalf("model and display channels crossed: %+v", message)
			}
		})
	}
}

func TestTypedSuccessCannotBecomeToolFailureFromLiteralEvidence(t *testing.T) {
	a := newPermissionLifecycleAgent(t)
	a.ToolFn = func(context.Context, string, map[string]any) (tools.ToolResult, error) {
		return tools.ToolResult{Content: "Error: literal evidence from a successful read",
			Outcome: toolresult.Outcome{State: toolresult.Succeeded}}, nil
	}
	result := a.executeOneToolCall(t.Context(), provider.ToolCall{ID: "call", InternalID: "operation",
		Function: provider.FunctionCall{Name: "read_file", Arguments: `{"path":"evidence"}`}}, func(event AgentEvent) {
		if event.Type == "tool_result" {
			if event.Data.(map[string]any)["error"] == "true" {
				t.Fatal("the live event contradicted the producer's success")
			}
		}
	}, nil, new(string))
	message := toolResultMessage(result, 1)
	if result.toolError || message.ToolError || message.ToolOutcome == nil || message.ToolOutcome.State != toolresult.Succeeded {
		t.Fatalf("literal output changed the typed result: %+v", message)
	}
}

func TestPatchAndDynamicPolicyRefusalsPublishDeniedWithoutRunning(t *testing.T) {
	for _, test := range []struct {
		tool, args string
		def        tools.ToolDef
	}{
		{"apply_patch", `{"input":"*** Begin Patch\n*** Add File: refused.txt\n+not written\n*** End Patch\n"}`, tools.ApplyPatchTool()},
		{"mcp__fixture__write", `{}`, tools.ToolDef{Function: tools.ToolFuncDef{Name: "mcp__fixture__write"}}},
	} {
		t.Run(test.tool, func(t *testing.T) {
			a := newPermissionLifecycleAgent(t)
			a.Policy = safety.ReadOnlyPolicy()
			a.FilteredTools = []tools.ToolDef{test.def}
			a.ToolFn = func(context.Context, string, map[string]any) (tools.ToolResult, error) {
				t.Fatal("a policy refusal executed its effect")
				return tools.ToolResult{}, nil
			}
			settled := 0
			result := a.executeOneToolCall(t.Context(), provider.ToolCall{ID: "call", InternalID: "denied-call",
				Function: provider.FunctionCall{Name: test.tool, Arguments: test.args}}, func(event AgentEvent) {
				if event.Type == "tool_result" {
					settled++
				}
			}, nil, new(string))
			message := toolResultMessage(result, 1)
			if settled != 1 || result.result.Outcome.State != toolresult.Denied ||
				result.result.AgentHint != prompt.HintDenied || !message.ToolError ||
				strings.Contains(message.DisplayContent, "[POLICY BLOCK]") ||
				strings.Contains(message.DisplayContent, prompt.HintDenied) ||
				!strings.Contains(message.ContentString(), prompt.HintDenied) {
				t.Fatalf("denial lost its fact, private advice or single completion: %+v", message)
			}
		})
	}
}
