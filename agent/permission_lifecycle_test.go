package agent

import (
	"context"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"crude/logger"
	"crude/provider"
	"crude/safety"
	"crude/tools"
	ttypes "crude/types"
)

func newPermissionLifecycleAgent(t *testing.T) *Agent {
	t.Helper()
	lg, err := logger.New(logger.Options{HomeDir: t.TempDir(), SessionID: "permission-lifecycle"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lg.Close() })
	return NewAgent(nil, "test-model", lg)
}

func editPermissionCall() provider.ToolCall {
	return provider.ToolCall{
		ID: "tool-1", InternalID: "internal-1",
		Function: provider.FunctionCall{
			Name:      "edit_file",
			Arguments: `{"path":"main.go","old_string":"old","new_string":"new"}`,
		},
	}
}

func commandPermissionCall(command string) provider.ToolCall {
	return provider.ToolCall{
		ID: "tool-command", InternalID: "internal-command",
		Function: provider.FunctionCall{Name: "execute_command", Arguments: `{"command":` + strconv.Quote(command) + `}`},
	}
}

func TestPermissionEscapeCancellationIsNotDenial(t *testing.T) {
	agent := newPermissionLifecycleAgent(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	blockResponses := make(chan ttypes.BlockResponse, 1)
	toolCalled := false
	deniedEvent := false
	agent.ToolFn = func(context.Context, string, map[string]any) (tools.ToolResult, error) {
		toolCalled = true
		return tools.ToolResult{Content: "edited"}, nil
	}

	result := agent.executeOneToolCall(ctx, editPermissionCall(), func(evt AgentEvent) {
		if evt.Type == "blocking" {
			cancel()
		}
		if evt.Type == "permission_resolved" {
			deniedEvent = true
		}
	}, blockResponses, new(string))

	if toolCalled {
		t.Fatal("cancelled permission executed the tool")
	}
	if deniedEvent {
		t.Fatal("turn cancellation emitted an explicit permission decision")
	}
	if !strings.Contains(result.result.Content, "active turn was cancelled") || strings.Contains(result.result.Content, "user denied") {
		t.Fatalf("cancellation result = %q", result.result.Content)
	}
	select {
	case response := <-blockResponses:
		t.Fatalf("cancellation poisoned permission channel with %q", response)
	default:
	}
}

func TestExplicitPermissionDenialHasDistinctLifecycleEvent(t *testing.T) {
	agent := newPermissionLifecycleAgent(t)
	blockResponses := make(chan ttypes.BlockResponse, 1)
	decision := ""
	agent.ToolFn = func(context.Context, string, map[string]any) (tools.ToolResult, error) {
		t.Fatal("denied permission executed the tool")
		return tools.ToolResult{}, nil
	}

	result := agent.executeOneToolCall(context.Background(), editPermissionCall(), func(evt AgentEvent) {
		if evt.Type == "blocking" {
			blockResponses <- ttypes.BlockResponse{RequestID: "internal-1", Outcome: ttypes.BlockAnswered, Value: "deny"}
		}
		if evt.Type == "permission_resolved" {
			data, _ := evt.Data.(map[string]any)
			decision, _ = data["decision"].(string)
		}
	}, blockResponses, new(string))

	if decision != "denied" {
		t.Fatalf("permission decision = %q, want denied", decision)
	}
	if !strings.Contains(result.result.Content, "user denied") {
		t.Fatalf("denial result = %q", result.result.Content)
	}
}

func TestLiveYoloPolicyAppliesToAlreadyConstructedAgent(t *testing.T) {
	agent := newPermissionLifecycleAgent(t)
	var yolo atomic.Bool
	agent.YoloMode = yolo.Load
	toolCalled := false
	blocking := false
	agent.ToolFn = func(context.Context, string, map[string]any) (tools.ToolResult, error) {
		toolCalled = true
		return tools.ToolResult{Content: "edited"}, nil
	}

	// This simulates /yolo after the turn's Agent was already created.
	yolo.Store(true)
	result := agent.executeOneToolCall(context.Background(), editPermissionCall(), func(evt AgentEvent) {
		blocking = blocking || evt.Type == "blocking"
	}, make(chan ttypes.BlockResponse, 1), new(string))

	if !toolCalled || result.toolError {
		t.Fatalf("live YOLO did not execute tool: called=%v result=%+v", toolCalled, result)
	}
	if blocking {
		t.Fatal("live YOLO still opened a permission prompt")
	}
}

func TestQuestionRequestNormalizationAndValidation(t *testing.T) {
	encoded, err := normalizeQuestionRequest(map[string]any{
		"questions": []any{map[string]any{
			"question": "Choose a runtime",
			"header":   "Runtime",
			"options": []any{
				map[string]any{"label": "Go", "description": "Compiled"},
				"Rust",
			},
		}},
	})
	if err != nil {
		t.Fatalf("normalizeQuestionRequest: %v", err)
	}
	text := string(encoded)
	if !strings.Contains(text, `"options":["Go","Rust"]`) {
		t.Fatalf("normalized questions = %s", text)
	}

	_, err = normalizeQuestionRequest(map[string]any{
		"questions": []any{map[string]any{"question": "Missing a header"}},
	})
	if err == nil || !strings.Contains(err.Error(), "header must be non-empty") {
		t.Fatalf("missing header error = %v", err)
	}
}

func TestBlockResponseIgnoresStaleRequestIDs(t *testing.T) {
	responses := make(chan ttypes.BlockResponse, 2)
	responses <- ttypes.BlockResponse{RequestID: "old", Outcome: ttypes.BlockAnswered, Value: "stale"}
	responses <- ttypes.BlockResponse{RequestID: "current", Outcome: ttypes.BlockAnswered, Value: "fresh"}

	response, ok := waitForBlockResponse(context.Background(), responses, "current")
	if !ok || response.Value != "fresh" {
		t.Fatalf("response = %#v, ok=%v", response, ok)
	}
}

func TestQuestionValidationFailureIsNotUserCancellation(t *testing.T) {
	result, isError := questionBlockResult(ttypes.BlockResponse{
		Outcome: ttypes.BlockValidationError,
		Detail:  "header is missing",
	})
	if !isError || !strings.Contains(result, "validation failed") || strings.Contains(result, "user cancelled") {
		t.Fatalf("validation result = %q, error=%v", result, isError)
	}
}

func TestLocalScriptUsesConfigurableConfirmationInsteadOfBlindBlock(t *testing.T) {
	agent := newPermissionLifecycleAgent(t)
	agent.CommandPolicy = safety.DefaultCommandPolicy()
	responses := make(chan ttypes.BlockResponse, 1)
	blocked := false
	agent.ToolFn = func(context.Context, string, map[string]any) (tools.ToolResult, error) {
		t.Fatal("denied script executed")
		return tools.ToolResult{}, nil
	}
	result := agent.executeOneToolCall(context.Background(), commandPermissionCall("bash scripts/check.sh"), func(event AgentEvent) {
		if event.Type != "blocking" {
			return
		}
		blocked = true
		data, _ := event.Data.(map[string]string)
		if data["risk"] != "unknown" {
			t.Fatalf("script risk=%q", data["risk"])
		}
		responses <- ttypes.BlockResponse{RequestID: "internal-command", Outcome: ttypes.BlockAnswered, Value: "deny"}
	}, responses, new(string))
	if !blocked || !strings.Contains(result.result.Content, "user denied") {
		t.Fatalf("script confirmation blocked=%v result=%q", blocked, result.result.Content)
	}
}

func TestHardBlockedCommandEmitsPersistentSafetyAlert(t *testing.T) {
	agent := newPermissionLifecycleAgent(t)
	agent.CommandPolicy = safety.DefaultCommandPolicy()
	alerted := false
	result := agent.executeOneToolCall(context.Background(), commandPermissionCall("rm -rf /"), func(event AgentEvent) {
		if event.Type == "safety_alert" {
			alerted = true
		}
	}, nil, new(string))
	if !alerted || !result.toolError || !strings.Contains(result.result.Content, "SAFETY") {
		t.Fatalf("blocked alert=%v result=%#v", alerted, result)
	}
}
