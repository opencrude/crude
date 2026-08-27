package agent

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"crude/logger"
	"crude/provider"
	"crude/tools"
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

func TestPermissionEscapeCancellationIsNotDenial(t *testing.T) {
	agent := newPermissionLifecycleAgent(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	blockResponses := make(chan string, 1)
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
	if !strings.Contains(result.result.Content, "Turn cancelled") || strings.Contains(result.result.Content, "user denied") {
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
	blockResponses := make(chan string, 1)
	decision := ""
	agent.ToolFn = func(context.Context, string, map[string]any) (tools.ToolResult, error) {
		t.Fatal("denied permission executed the tool")
		return tools.ToolResult{}, nil
	}

	result := agent.executeOneToolCall(context.Background(), editPermissionCall(), func(evt AgentEvent) {
		if evt.Type == "blocking" {
			blockResponses <- "deny"
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
	}, make(chan string, 1), new(string))

	if !toolCalled || result.toolError {
		t.Fatalf("live YOLO did not execute tool: called=%v result=%+v", toolCalled, result)
	}
	if blocking {
		t.Fatal("live YOLO still opened a permission prompt")
	}
}
