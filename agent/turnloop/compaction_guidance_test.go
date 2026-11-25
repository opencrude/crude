package turnloop

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	rootprompt "crude/agent/prompt"
	"crude/agent/provider"
	"crude/agent/tools"
	"crude/internal/providersim"
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
	var final providersim.Request
	server := providersim.New(t)
	server.Respond(func(r providersim.Request) providersim.Turn {
		requests++
		switch {
		case requests == 1:

			return providersim.CallTools(providersim.ToolCall{ID: "mutate", Name: "write_file", Arguments: `{"path":"AGENTS.md","content":"current-rule"}`})
		case requests <= 1+stages:
			return providersim.Text(fmt.Sprintf("- stage: %d", requests-1))
		default:
			final = r
			return providersim.Text("done")
		}
	})
	p := provider.FromPreset("openai-chat", "key", server.BaseURL(providersim.Chat))
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
	if requests != stages+2 || len(final.System) == 0 {
		t.Fatalf("requests=%d want one task request, %d stages and one continuation", requests, stages)
	}
	system := simulatedText(final.System)
	if final.System[0].Role != "system" || !strings.Contains(system, "keep-me") {
		t.Fatalf("continuation lost the base system prompt: %q", system)
	}
	all := conversationText(final.Conversation)
	if !strings.Contains(system, "current-rule") || strings.Contains(all, "original-rule") {
		t.Fatalf("continuation does not carry current guidance; system=%q", system)
	}
}

// A child's assignment is not capped like recent human input: it keeps all
// available continuation space and only falls back to extraction when it
// cannot fit at all.
func TestChildAssignmentUsesAvailableSpaceBeyondRecentInputCap(t *testing.T) {
	const recentInputCap = 20_000
	assignment := provider.NewTextMessage("user", strings.Repeat("assignment detail ", 20_000))
	assignment.Origin = provider.OriginAssignment
	human := provider.NewTextMessage("user", "steer")
	cost := provider.EstimateMessageTokens(assignment, "model")
	if cost <= recentInputCap {
		t.Fatalf("fixture assignment %d tokens does not exceed the recent-input cap", cost)
	}
	kept := boundedRecentUserContext([]provider.ChatMessage{assignment, human}, "child", recentInputCap, cost+100, "model")
	if len(kept) != 2 || kept[0].EffectiveOrigin() != provider.OriginAssignment || kept[1].ContentString() != "steer" {
		t.Fatalf("assignment not retained within available space: %d messages", len(kept))
	}
	if dropped := boundedRecentUserContext([]provider.ChatMessage{assignment}, "child", recentInputCap, cost-1, "model"); len(dropped) != 0 {
		t.Fatal("assignment exceeding available space must be left to extraction")
	}
}
