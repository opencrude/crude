package agent

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"crude/provider"
	"crude/tools"
	ttypes "crude/types"
)

const liveOllamaFlashModel = "glm-5.3-flash:cloud"

func liveOllamaAgent(t *testing.T, allowedTools ...string) *Agent {
	t.Helper()
	key := os.Getenv("CRUDE_LIVE_OLLAMA_KEY")
	if key == "" {
		t.Skip("CRUDE_LIVE_OLLAMA_KEY not set")
	}
	p := provider.FromPreset("ollama-cloud", key, "")
	p.Model = liveOllamaFlashModel
	a := newTestAgent(t, p, p.Model)
	a.FilteredTools = FilterTools(tools.AgentTools, allowedTools, nil, "", false)
	return a
}

func TestLiveOllamaAtomicWriteLifecycle(t *testing.T) {
	a := liveOllamaAgent(t, "write_file")
	a.Yolo = true
	var calls int
	a.ToolFn = func(_ context.Context, name string, params map[string]any) (tools.ToolResult, error) {
		if name != "write_file" {
			return tools.ToolResult{}, fmt.Errorf("unexpected tool %q", name)
		}
		calls++
		if params["path"] != "live-probe.txt" || params["content"] != "cache-safe" {
			return tools.ToolResult{}, fmt.Errorf("unexpected write arguments: %#v", params)
		}
		return tools.ToolResult{Content: "probe accepted", Added: 1}, nil
	}

	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	var events []AgentEvent
	a.Run(ctx, []provider.ChatMessage{provider.NewTextMessage("user",
		`Call write_file exactly once with path "live-probe.txt" and content "cache-safe". After the tool succeeds, reply DONE without another tool call.`)},
		"Follow the user exactly. Use the provided tool instead of describing it.",
		func(event AgentEvent) { events = append(events, event) }, nil, "", nil)

	if calls != 1 {
		t.Fatalf("write tool calls = %d, want exactly 1; events=%s", calls, summarizeLiveEvents(events))
	}
	want := []string{"tool_call_streaming", "tool_call", "tool_progress:writing", "tool_progress:done", "tool_result", "done"}
	if err := requireLiveEventOrder(events, want); err != nil {
		t.Fatalf("atomic write lifecycle: %v; events=%s", err, summarizeLiveEvents(events))
	}
}

func TestLiveOllamaStructuredQuestionLifecycle(t *testing.T) {
	a := liveOllamaAgent(t, "question")
	responses := make(chan ttypes.BlockResponse, 1)
	var events []AgentEvent
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()

	a.Run(ctx, []provider.ChatMessage{provider.NewTextMessage("user",
		`Use the question tool exactly once. Ask "Which runtime should we use?" with header "Runtime" and options "Go" and "Rust". Then acknowledge the answer without another tool call.`)},
		"Follow the user exactly and use the provided question tool.",
		func(event AgentEvent) {
			events = append(events, event)
			if event.Type != "blocking" {
				return
			}
			data, _ := event.Data.(map[string]any)
			blockID := fmt.Sprint(data["block_id"])
			questionsJSON, _ := data["questions_json"].(string)
			if blockID == "" || !strings.Contains(questionsJSON, `"header":"Runtime"`) ||
				!strings.Contains(questionsJSON, `"options":["Go","Rust"]`) {
				t.Errorf("invalid live question payload: block=%q questions=%s", blockID, questionsJSON)
			}
			responses <- ttypes.BlockResponse{
				RequestID: blockID,
				Outcome:   ttypes.BlockAnswered,
				Value:     "Q1: Which runtime should we use?\nA1: Go",
			}
		}, responses, "", nil)

	want := []string{"tool_call", "blocking", "blocking_complete", "tool_result", "done"}
	if err := requireLiveEventOrder(events, want); err != nil {
		t.Fatalf("question lifecycle: %v; events=%s", err, summarizeLiveEvents(events))
	}
	for _, event := range events {
		if event.Type != "tool_result" {
			continue
		}
		data, _ := event.Data.(map[string]any)
		result := fmt.Sprint(data["result"])
		if strings.Contains(result, "validation failed") || strings.Contains(result, "user cancelled") {
			t.Fatalf("valid question was misclassified: %s", result)
		}
	}
}

func TestLiveOllamaConfiguredContextCompaction(t *testing.T) {
	a := liveOllamaAgent(t)
	previousWindow := provider.LookupContextWindow(liveOllamaFlashModel)
	provider.SetModelContext(liveOllamaFlashModel, 16384)
	t.Cleanup(func() { provider.SetModelContext(liveOllamaFlashModel, previousWindow) })
	a.ContextCompactionPercent = 85
	var history []provider.ChatMessage
	for i := 0; i < 18; i++ {
		history = append(history,
			provider.NewTextMessage("user", fmt.Sprintf("Older request %d: %s", i, strings.Repeat("context ", 120))),
			provider.NewTextMessage("assistant", fmt.Sprintf("Older answer %d: %s", i, strings.Repeat("result ", 120))),
		)
	}
	history = append(history, provider.NewTextMessage("user", "Current instruction: reply COMPACT-OK without tools."))
	var errors []AgentEvent
	archive := a.Run(t.Context(), history, "Follow the current instruction exactly.", func(event AgentEvent) {
		if event.Type == "error" {
			errors = append(errors, event)
		}
	}, nil, "", nil)
	active := a.ActiveContext()
	currentSurvived := false
	for _, message := range active {
		currentSurvived = currentSurvived || strings.Contains(message.Content.String(), "Current instruction")
	}
	if len(errors) != 0 || len(active) == 0 || active[0].Role != "system" ||
		!strings.Contains(active[0].Content.String(), "<context_checkpoint") || !currentSurvived ||
		len(archive) <= len(history) || !strings.Contains(archive[1].Content.String(), "Older request 0") {
		t.Fatalf("live compact errors=%+v active=%d archive=%d", errors, len(active), len(archive))
	}
}

func requireLiveEventOrder(events []AgentEvent, want []string) error {
	next := 0
	for _, event := range events {
		label := event.Type
		if event.Type == "tool_progress" {
			if data, ok := event.Data.(map[string]any); ok {
				label += ":" + fmt.Sprint(data["phase"])
			}
		}
		if next < len(want) && label == want[next] {
			next++
		}
	}
	if next != len(want) {
		return fmt.Errorf("matched %d/%d, next=%q", next, len(want), want[next])
	}
	return nil
}

func summarizeLiveEvents(events []AgentEvent) string {
	labels := make([]string, 0, len(events))
	for _, event := range events {
		label := event.Type
		if event.Type == "tool_progress" {
			if data, ok := event.Data.(map[string]any); ok {
				label += ":" + fmt.Sprint(data["phase"])
			}
		}
		labels = append(labels, label)
	}
	return strings.Join(labels, ",")
}
