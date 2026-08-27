package provider

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
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
		Model:    "glm-5.3-flash",
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

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}
