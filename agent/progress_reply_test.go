package agent

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"crude/provider"
	"crude/tools"
)

// A provider can schedule a progress reply alongside a slow tool. Completion
// order must not change its display order relative to durable wire results.
func TestProgressReplyInParallelToolBatchUsesProviderOrder(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		if requests == 1 {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"slow-1","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"one.go\"}"}},{"index":1,"id":"progress-1","type":"function","function":{"name":"reply","arguments":"{\"content\":\"Checking tests.\"}"}}]},"finish_reason":"tool_calls"}]}`+"\n\ndata: [DONE]\n\n")
			return
		}
		writeChatSSE(w, "Final result.", "stop")
	}))
	defer server.Close()
	p := provider.FromPreset("openai-chat", "key", server.URL)
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
