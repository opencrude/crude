package provider

import (
	"encoding/json"
	"testing"
)

// TestCodexEncodeRequestRequiresStoreFalseStreamTrue pins the two flags the
// codex backend requires. Without store:false and stream:true it rejects the
// request with HTTP 400 (verified against the live backend).
func TestCodexEncodeRequestRequiresStoreFalseAndStreamTrue(t *testing.T) {
	std := &CodexStandard{}
	body, err := std.EncodeRequest(&ChatRequest{
		Model: "gpt-5.6-luna",
		Messages: []ChatMessage{
			{Role: "user", Content: TextContent("hi")},
		},
		Stream: true,
	})
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}
	var wire map[string]any
	if err := json.Unmarshal(body, &wire); err != nil {
		t.Fatalf("decode wire: %v", err)
	}
	store, _ := wire["store"].(bool)
	if store {
		t.Errorf("store = %v, want false (backend returns 400 without it)", store)
	}
	stream, _ := wire["stream"].(bool)
	if !stream {
		t.Errorf("stream = %v, want true (backend returns 400 without it)", stream)
	}
	if wire["model"] != "gpt-5.6-luna" {
		t.Errorf("model = %v", wire["model"])
	}
	include, ok := wire["include"].([]any)
	if !ok || len(include) != 1 || include[0] != "reasoning.encrypted_content" {
		t.Fatalf("stateless Codex request lost reasoning continuity: %v", wire["include"])
	}
}

// TestCodexEncodeStripsUnsupportedOutputParams pins that max_output_tokens /
// max_completion_tokens are removed because the backend rejects them with HTTP 400
// ({"detail":"Unsupported parameter: max_output_tokens"}).
func TestCodexEncodeStripsUnsupportedOutputParams(t *testing.T) {
	std := &CodexStandard{}
	body, err := std.EncodeRequest(&ChatRequest{
		Model: "gpt-5.6-luna",
		Messages: []ChatMessage{
			{Role: "user", Content: TextContent("hi")},
		},
		Stream:              true,
		MaxOutputTokens:     512,
		MaxCompletionTokens: 1024,
		MaxTokens:           2048,
	})
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}
	var wire map[string]any
	if err := json.Unmarshal(body, &wire); err != nil {
		t.Fatalf("decode wire: %v", err)
	}
	for _, key := range []string{"max_output_tokens", "max_completion_tokens", "max_tokens"} {
		if _, ok := wire[key]; ok {
			t.Errorf("wire still contains %q", key)
		}
	}
}

// TestCodexEncodeExtractsInstructions verifies the system/developer message is
// lifted out of input into a top-level instructions string (the backend
// rejects a missing instructions with 400 "Instructions are required").
func TestCodexEncodeExtractsInstructions(t *testing.T) {
	std := &CodexStandard{}
	body, err := std.EncodeRequest(&ChatRequest{
		Model: "gpt-5.6-luna",
		Messages: []ChatMessage{
			{Role: "system", Content: TextContent("You are helpful.")},
			{Role: "user", Content: TextContent("hi")},
		},
		Stream: true,
	})
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}
	var wire map[string]any
	if err := json.Unmarshal(body, &wire); err != nil {
		t.Fatalf("decode wire: %v", err)
	}
	if instructions, _ := wire["instructions"].(string); instructions != "You are helpful." {
		t.Errorf("instructions = %q, want 'You are helpful.'", instructions)
	}
	input, ok := wire["input"].([]any)
	if !ok {
		t.Fatalf("input = %T, want []any", wire["input"])
	}
	for _, item := range input {
		msg, _ := item.(map[string]any)
		if role, _ := msg["role"].(string); role == "system" || role == "developer" {
			t.Errorf("system/developer message left in input: %v", msg)
		}
	}
}

// TestCodexEncodeForcesStreamTrueEvenWhenRequestedStreamingIsFalse pins that
// the codex backend only accepts streaming, so the encoder must override the
// caller's Stream=false.
func TestCodexEncodeForcesStreamTrueEvenWhenRequestedStreamingIsFalse(t *testing.T) {
	std := &CodexStandard{}
	body, err := std.EncodeRequest(&ChatRequest{
		Model: "gpt-5.6-luna",
		Messages: []ChatMessage{
			{Role: "user", Content: TextContent("hi")},
		},
		Stream: false,
	})
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}
	var wire map[string]any
	if err := json.Unmarshal(body, &wire); err != nil {
		t.Fatalf("decode wire: %v", err)
	}
	if stream, _ := wire["stream"].(bool); !stream {
		t.Errorf("stream = %v, want forced true", stream)
	}
}
