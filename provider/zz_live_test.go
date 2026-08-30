package provider

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

// TestZZLiveOllamaCloud exercises the real ollama.com endpoint. Requires
// CRUDE_LIVE_OLLAMA_KEY; skipped otherwise.
func TestZZLiveOllamaCloud(t *testing.T) {
	key := os.Getenv("CRUDE_LIVE_OLLAMA_KEY")
	if key == "" {
		t.Skip("CRUDE_LIVE_OLLAMA_KEY not set")
	}
	p := FromPreset("ollama-cloud", key, "")
	client, err := p.Client()
	if err != nil {
		t.Fatalf("Client: %v", err)
	}
	stream, err := client.Stream(t.Context(), &ChatRequest{
		Model:    "glm-5.3-flash:cloud",
		Messages: []ChatMessage{NewTextMessage("user", "Use the get_time tool to tell me the current time. Do not answer from memory.")},
		Tools: []ToolDef{{
			Type: "function",
			Function: FunctionDef{
				Name:        "get_time",
				Description: "Returns the current time.",
				Parameters:  json.RawMessage(`{"type":"object","properties":{}}`),
			},
		}},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer stream.Close()

	var content, reasoning strings.Builder
	var calls []ToolCallDelta
	for {
		u, err := stream.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		content.WriteString(u.ContentDelta)
		reasoning.WriteString(u.ReasoningDelta)
		calls = append(calls, u.ToolCalls...)
	}
	t.Logf("reasoning=%q content=%q calls=%+v", truncate(reasoning.String(), 80), content.String(), calls)
	if len(calls) == 0 {
		t.Errorf("expected a tool call, got none")
	}
	for _, c := range calls {
		if c.Arguments == "" {
			t.Errorf("tool call %q has empty arguments", c.Name)
		}
	}
}

// TestZZLiveOllamaCloudPrefixReuse sends a byte-identical, non-trivial prompt
// repeatedly and records Ollama's native prompt-evaluation timing. The API does
// not expose cache-read token counts, so timing is diagnostic rather than
// billing data; at least one warm request should evaluate materially faster.
func TestZZLiveOllamaCloudPrefixReuse(t *testing.T) {
	key := os.Getenv("CRUDE_LIVE_OLLAMA_KEY")
	if key == "" {
		t.Skip("CRUDE_LIVE_OLLAMA_KEY not set")
	}
	p := FromPreset("ollama-cloud", key, "")
	client, err := p.Client()
	if err != nil {
		t.Fatalf("Client: %v", err)
	}
	stablePrefix := strings.Repeat(
		"Cache probe fact: copper conducts electricity, basalt is volcanic, and this exact line must remain byte-identical.\n",
		600,
	)
	request := &ChatRequest{
		Model:           "glm-5.3-flash:cloud",
		MaxOutputTokens: 16,
		Messages: []ChatMessage{
			NewTextMessage("system", stablePrefix),
			NewTextMessage("user", "Reply with exactly PONG."),
		},
		Stream: true,
	}

	timings := make([]time.Duration, 0, 4)
	for attempt := 0; attempt < 4; attempt++ {
		started := time.Now()
		usage, content := liveOllamaRequest(t, client, request)
		wall := time.Since(started)
		if usage.PromptTokens == 0 {
			t.Fatalf("attempt %d reported no prompt usage", attempt+1)
		}
		t.Logf("attempt=%d prompt_tokens=%d prompt_eval=%s total=%s wall=%s content=%q",
			attempt+1, usage.PromptTokens, usage.PromptEvalDuration, usage.TotalDuration, wall, truncate(content, 40))
		if usage.PromptEvalDuration > 0 {
			timings = append(timings, usage.PromptEvalDuration)
		}
	}
	if len(timings) < 2 {
		t.Log("Ollama Cloud did not expose prompt_eval_duration; exact-prefix requests completed but cache billing remains unobservable")
		return
	}
	cold := timings[0]
	warm := timings[1]
	for _, timing := range timings[2:] {
		if timing < warm {
			warm = timing
		}
	}
	if warm*4 >= cold*3 {
		t.Fatalf("no material warm-prefix reuse observed: cold=%s best_warm=%s", cold, warm)
	}
}

func liveOllamaRequest(t *testing.T, client *Client, request *ChatRequest) (TokenUsage, string) {
	t.Helper()
	stream, err := client.Stream(t.Context(), request)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer stream.Close()
	var usage TokenUsage
	var content strings.Builder
	for {
		update, nextErr := stream.Next()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			t.Fatalf("Next: %v", nextErr)
		}
		content.WriteString(update.ContentDelta)
		if update.Usage != nil {
			usage = *update.Usage
		}
	}
	return usage, content.String()
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}
