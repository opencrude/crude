package turnloop

import (
	"context"
	"sync"
	"testing"

	"crude/agent/provider"
	"crude/agent/tools"
	"crude/internal/providersim"
)

// A provider can schedule a progress reply alongside a slow tool. Completion
// order must not change its display order relative to durable wire results.
func TestProgressReplyInParallelToolBatchUsesProviderOrder(t *testing.T) {
	server := providersim.New(t)
	server.Script(providersim.CallTools(providersim.ToolCall{ID: "slow-1", Name: "read_file", Arguments: `{"path":"one.go"}`}, providersim.ToolCall{ID: "progress-1", Name: "reply", Arguments: `{"content":"Checking tests."}`}), providersim.Text("Final result."))
	p := provider.FromPreset("openai-chat", "key", server.BaseURL(providersim.Chat))
	p.Model = "test-model"
	a := newTestAgent(t, p, p.Model)
	started, release := make(chan struct{}), make(chan struct{})
	a.ToolFn = func(_ context.Context, name string, _ map[string]any) (tools.ToolResult, error) {
		if name == "read_file" {
			close(started)
			<-release
			return tools.ToolResult{Content: "package one"}, nil
		}
		return tools.ToolResult{Content: "unexpected tool"}, nil
	}
	var mu sync.Mutex
	var events []AgentEvent
	finished := make(chan []provider.ChatMessage, 1)
	go func() {
		finished <- a.Run(context.Background(), nil, "system", func(evt AgentEvent) {
			mu.Lock()
			events = append(events, evt)
			mu.Unlock()
		}, nil, "", nil)
	}()
	<-started
	mu.Lock()
	for _, evt := range events {
		if evt.Type == "tool_result" {
			if data, ok := evt.Data.(map[string]any); ok && data["name"] == "reply" {
				mu.Unlock()
				t.Fatal("reply appeared before earlier parallel call completed")
			}
		}
	}
	mu.Unlock()
	close(release)
	wire := <-finished
	var toolResults []string
	for _, msg := range wire {
		if msg.Role == "tool" {
			toolResults = append(toolResults, msg.ToolCallID)
		}
	}
	if len(toolResults) != 2 || toolResults[0] != "slow-1" || toolResults[1] != "progress-1" {
		t.Fatalf("durable result order = %v", toolResults)
	}
	mu.Lock()
	defer mu.Unlock()
	progressEvents := 0
	for _, evt := range events {
		if evt.Type != "tool_result" {
			continue
		}
		data, ok := evt.Data.(map[string]any)
		if ok && data["name"] == "reply" {
			progressEvents++
			if data["tool_id"] != "progress-1" || data["result"] != "Checking tests." {
				t.Fatalf("incorrect progress event: %+v", data)
			}
		}
	}
	if progressEvents != 1 {
		t.Fatalf("progress events = %d want 1", progressEvents)
	}
}
