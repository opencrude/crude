package turnloop

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"crude/agent/provider"
	"crude/internal/providersim"
)

func TestCompleteStreamOllamaNDJSON(t *testing.T) {
	server := providersim.New(t)
	server.Respond(func(r providersim.Request) providersim.Turn {
		if r.Path != "/api/chat" {
			t.Errorf("path = %q, want /api/chat", r.Path)
		}
		if r.Header.Get("Authorization") != "Bearer ollama-key" {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}
		return providersim.Turn{}.WithTextDeltas("Hel", "lo").WithUsage(providersim.Usage{Input: 5, Output: 2})
	})

	p := provider.FromPreset("ollama-cloud", "ollama-key", server.BaseURL(providersim.Ollama))
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

func TestConfiguredAgentCompactsWithCurrentModelAtThreshold(t *testing.T) {
	server := providersim.New(t)
	server.Respond(func(r providersim.Request) providersim.Turn {
		if r.Index <= len(compactionStagesForTest) {
			return providersim.Text("- compact stage: preserved facts").WithUsage(providersim.Usage{Input: 100_000, Output: 100})
		}
		return providersim.Text("done").WithUsage(providersim.Usage{Input: 12_000, Output: 1})
	})
	p := provider.FromPreset("ollama-cloud", "ollama-key", server.BaseURL(providersim.Ollama))
	p.Model = "compaction-test-model"
	provider.SetModelContext(p.Model, 200000)
	a := newTestAgent(t, p, p.Model)
	a.ContextCompactionPercent = 40
	var largeHistory []provider.ChatMessage
	for i := 0; i < 24; i++ {
		largeHistory = append(largeHistory,
			provider.NewTextMessage("user", fmt.Sprintf("request %d %s", i, strings.Repeat("context ", 1000))),
			provider.NewTextMessage("assistant", fmt.Sprintf("answer %d %s", i, strings.Repeat("analysis ", 1000))),
		)
	}
	largeHistory = append(largeHistory, provider.NewTextMessage("user", "current instruction must survive"))
	archive := a.Run(context.Background(), largeHistory, "system", func(AgentEvent) {}, nil, "", nil)
	bodies := server.Requests()
	if len(bodies) != len(compactionStagesForTest)+1 || len(bodies[0].Raw) < 400000 || !strings.Contains(conversationText(bodies[0].Conversation), "compaction") ||
		!strings.Contains(conversationText(bodies[0].Conversation), "request 0") {
		t.Fatalf("summary request did not retain cached context: requests=%d", len(bodies))
	}
	postCompact := bodies[len(bodies)-1].Raw
	if len(postCompact) >= len(bodies[0].Raw)/2 || !strings.Contains(string(postCompact), "context_checkpoint") || strings.Contains(string(postCompact), "request 0") {
		t.Fatalf("post-compaction request was not rotated: first=%d final=%d", len(bodies[0].Raw), len(postCompact))
	}
	if len(archive) <= len(largeHistory) || !strings.Contains(archive[1].Content.String(), "request 0") {
		t.Fatal("full archive was lost during active epoch rotation")
	}
	active := a.ActiveContext()
	currentSurvived := false
	for _, message := range active {
		currentSurvived = currentSurvived || strings.Contains(message.Content.String(), "current instruction must survive")
	}
	checkpointSurvived := false
	for _, message := range active {
		checkpointSurvived = checkpointSurvived || message.EffectiveOrigin() == provider.OriginCheckpoint
	}
	if len(active) == 0 || !checkpointSurvived || !currentSurvived {
		t.Fatalf("active context = %#v", active)
	}
}
