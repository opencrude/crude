package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	rootprompt "crude/prompt"
	"crude/provider"
	"crude/tools"
	ttypes "crude/types"
)

// Guidance changed during a run is delivered as hidden runtime context. An
// automatic compaction replaces that context with a checkpoint, so the
// continuation must carry the current guidance in its system message rather
// than the run's initial render.
func TestAutomaticCompactionKeepsCurrentGuidanceAndBasePrompt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "AGENTS.md")
	if err := os.WriteFile(path, []byte("original-rule"), 0600); err != nil {
		t.Fatal(err)
	}
	stages := len(rootprompt.CompactionStages())
	requests := 0
	var final provider.ChatRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var request provider.ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		switch {
		case requests == 1:
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"mutate\",\"type\":\"function\",\"function\":{\"name\":\"write_file\",\"arguments\":\"{\\\"path\\\":\\\"AGENTS.md\\\",\\\"content\\\":\\\"current-rule\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")
		case requests <= 1+stages:
			writeChatSSE(w, fmt.Sprintf("- stage: %d", requests-1), "stop")
		default:
			final = request
			writeChatSSE(w, "done", "stop")
		}
	}))
	defer server.Close()
	p := provider.FromPreset("openai-chat", "key", server.URL)
	p.Model = "compaction-guidance-model"
	provider.SetModelContext(p.Model, 1_000_000)
	a := newTestAgent(t, p, p.Model)
	a.WorkDir, a.Yolo, a.ContextCompactionPercent = dir, true, 2
	a.ToolFn = func(_ context.Context, _ string, _ map[string]any) (tools.ToolResult, error) {
		// A large result pushes the next step over the compaction threshold.
		return tools.ToolResult{Content: strings.Repeat("evidence ", 25_000)}, os.WriteFile(path, []byte("current-rule"), 0600)
	}
	base := "<base_system>keep-me</base_system>"
	a.Run(t.Context(), []provider.ChatMessage{provider.NewTextMessage("user", "Change guidance")}, base, nil, nil, "", nil)
	if requests != stages+2 || len(final.Messages) == 0 {
		t.Fatalf("requests=%d want one task request, %d stages and one continuation", requests, stages)
	}
	system := final.Messages[0].ContentString()
	if final.Messages[0].Role != "system" || !strings.Contains(system, "keep-me") {
		t.Fatalf("continuation lost the base system prompt: %q", system)
	}
	var all strings.Builder
	for _, message := range final.Messages {
		all.WriteString(message.ContentString())
	}
	if !strings.Contains(system, "current-rule") || strings.Contains(all.String(), "original-rule") {
		t.Fatalf("continuation does not carry current guidance; system=%q", system)
	}
}

// A child's assignment is not capped like recent human input: it keeps all
// available continuation space and only falls back to extraction when it
// cannot fit at all.
func TestChildAssignmentUsesAvailableSpaceBeyondRecentInputCap(t *testing.T) {
	assignment := provider.NewTextMessage("user", strings.Repeat("assignment detail ", 20_000))
	assignment.Origin = provider.OriginAssignment
	human := provider.NewTextMessage("user", "steer")
	cost := provider.EstimateMessageTokens(assignment, "model")
	if cost <= ttypes.DefaultContextRetentionTokens {
		t.Fatalf("fixture assignment %d tokens does not exceed the recent-input cap", cost)
	}
	kept := boundedRecentUserContext([]provider.ChatMessage{assignment, human}, "child", ttypes.DefaultContextRetentionTokens, cost+100, "model")
	if len(kept) != 2 || kept[0].EffectiveOrigin() != provider.OriginAssignment || kept[1].ContentString() != "steer" {
		t.Fatalf("assignment not retained within available space: %d messages", len(kept))
	}
	if dropped := boundedRecentUserContext([]provider.ChatMessage{assignment}, "child", ttypes.DefaultContextRetentionTokens, cost-1, "model"); len(dropped) != 0 {
		t.Fatal("assignment exceeding available space must be left to extraction")
	}
}
