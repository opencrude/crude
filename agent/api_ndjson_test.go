package agent

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"crude/provider"
	"crude/tools"
)

func TestCompleteStreamOllamaNDJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Errorf("path = %q, want /api/chat", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer ollama-key" {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		// Ollama streams one JSON object per line; the final line carries the
		// done flag and usage counts.
		_, _ = fmt.Fprint(w,
			`{"model":"llama3","message":{"role":"assistant","content":"Hel"}}`+"\n"+
				`{"model":"llama3","message":{"role":"assistant","content":"lo"}}`+"\n"+
				`{"model":"llama3","message":{"role":"assistant","content":""},"done":true,"done_reason":"stop","prompt_eval_count":5,"eval_count":2}`+"\n")
	}))
	defer server.Close()

	p := provider.FromPreset("ollama-cloud", "ollama-key", server.URL)
	p.Model = "compaction-test-model"
	provider.SetModelContext(p.Model, 65536)
	if got := p.ContextWindow(p.Model); got != 65536 {
		t.Fatalf("context window=%d want 65536", got)
	}
	agent := newTestAgent(t, p, p.Model)

	result, err := completeForTest(t, agent)
	if err != nil {
		t.Fatalf("completeStream: %v", err)
	}
	if result.Content != "Hello" {
		t.Errorf("content = %q, want Hello", result.Content)
	}
	if result.FinishReason != "stop" {
		t.Errorf("finish = %q, want stop", result.FinishReason)
	}
	if result.Usage.PromptTokens != 5 || result.Usage.CompletionTokens != 2 {
		t.Errorf("usage = %+v", result.Usage)
	}
}

func TestConfiguredAgentCompactsFromFullArchiveAtThreshold(t *testing.T) {
	var requestCount atomic.Int32
	var bodies [][]byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		bodies = append(bodies, body)
		w.Header().Set("Content-Type", "application/x-ndjson")
		if requestCount.Add(1) == 1 {
			_, _ = fmt.Fprint(w, `{"model":"llama3","message":{"role":"assistant","content":"","tool_calls":[{"function":{"name":"read_file","arguments":{"path":"go.mod"}}}]},"done":true,"done_reason":"tool_calls","prompt_eval_count":100000,"eval_count":10}`+"\n")
			return
		}
		_, _ = fmt.Fprint(w, `{"model":"llama3","message":{"role":"assistant","content":"done"},"done":true,"done_reason":"stop","prompt_eval_count":12000,"eval_count":1}`+"\n")
	}))
	defer server.Close()
	p := provider.FromPreset("ollama-cloud", "ollama-key", server.URL)
	p.Model = "compaction-test-model"
	provider.SetModelContext(p.Model, 65536)
	a := newTestAgent(t, p, p.Model)
	a.ContextCompactionPercent = 85
	var largeHistory []provider.ChatMessage
	for i := 0; i < 24; i++ {
		largeHistory = append(largeHistory,
			provider.NewTextMessage("user", fmt.Sprintf("request %d %s", i, strings.Repeat("context ", 1000))),
			provider.NewTextMessage("assistant", fmt.Sprintf("answer %d %s", i, strings.Repeat("analysis ", 1000))),
		)
	}
	largeHistory = append(largeHistory, provider.NewTextMessage("user", "current instruction must survive"))
	a.ToolFn = func(context.Context, string, map[string]any) (tools.ToolResult, error) {
		return tools.ToolResult{Content: "module crude"}, nil
	}
	archive := a.Run(context.Background(), largeHistory, "system", func(AgentEvent) {}, nil, nil, "", nil)
	if len(bodies) != 2 || len(bodies[0]) >= 400000 || !strings.Contains(string(bodies[0]), "context_checkpoint") {
		t.Fatalf("active context was not compacted: requests=%d sizes=%v", len(bodies), []int{len(bodies[0]), len(bodies[1])})
	}
	if len(archive) <= len(largeHistory) || !strings.Contains(archive[1].Content.String(), "request 0") {
		t.Fatal("full archive was lost during active epoch rotation")
	}
	active := a.ActiveContext()
	currentSurvived := false
	for _, message := range active {
		currentSurvived = currentSurvived || strings.Contains(message.Content.String(), "current instruction must survive")
	}
	if len(active) == 0 || active[0].Role != "system" || !strings.Contains(active[0].Content.String(), "context_checkpoint") || !currentSurvived {
		t.Fatalf("active context = %#v", active)
	}
}
