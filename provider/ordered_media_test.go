package provider

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestOrderedToolEvidenceWireProjection(t *testing.T) {
	parts := []ContentPart{{Type: "text", Text: "ORDER_A"}, {Type: "image_url", ImageURL: &ImageURL{URL: "data:image/png;base64,aW1hZ2U="}}, {Type: "text", Text: "ORDER_B"}, {Type: "audio", MimeType: "audio/wav", Data: []byte("audio")}, {Type: "text", Text: "ORDER_C"}}
	messages := []ChatMessage{
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "remote-call", Function: FunctionCall{Name: "mcp__ordered", Arguments: "{}"}}, {ID: "sibling", Function: FunctionCall{Name: "other", Arguments: "{}"}}}},
		{Role: "tool", ToolCallID: "remote-call", Content: PartsContent(parts), ToolError: true},
		{Role: "tool", ToolCallID: "sibling", Content: TextContent("ACK")},
	}
	before, _ := json.Marshal(messages)
	wire, err := encodeOpenAIChatMessages(messages)
	if err != nil {
		t.Fatal(err)
	}
	if len(wire) != 4 || wire[1].(map[string]any)["tool_call_id"] != "remote-call" || wire[2].(map[string]any)["tool_call_id"] != "sibling" || wire[3].(map[string]any)["role"] != "user" {
		t.Fatalf("protocol association or sibling acknowledgement lost: %#v", wire)
	}
	if strings.Contains(wire[1].(map[string]any)["content"].(string), "ORDER_") {
		t.Fatal("flattened copy competes with ordered evidence")
	}
	raw, _ := json.Marshal(wire[3])
	last := -1
	for _, needle := range []string{"remote-call", "mcp__ordered", "not new human input", "ORDER_A", "aW1hZ2U=", "ORDER_B", "YXVkaW8=", "ORDER_C"} {
		index := strings.Index(string(raw), needle)
		if index <= last {
			t.Fatalf("lost/reordered %q: %s", needle, raw)
		}
		last = index
	}
	after, _ := json.Marshal(messages)
	if string(before) != string(after) {
		t.Fatal("wire projection mutated canonical transcript")
	}
}

func TestNativeOrderedMediaLimits(t *testing.T) {
	for _, standard := range []string{"anthropic", "ollama"} {
		proto, _ := ProtocolByID(standard)
		_, err := proto.EncodeRequest(&ChatRequest{Model: "fixture", Messages: []ChatMessage{NewMultiPartMessage("user", []ContentPart{{Type: "text", Text: "A"}, {Type: "audio", MimeType: "audio/wav", Data: []byte("audio")}, {Type: "text", Text: "B"}})}})
		if err == nil || !strings.Contains(err.Error(), "unsupported") {
			t.Fatalf("%s silently lost unsupported audio: %v", standard, err)
		}
	}
	parts := []ContentPart{{Type: "text", Text: "A"}, {Type: "image_url", ImageURL: &ImageURL{URL: "data:image/png;base64,aW1hZ2U="}}, {Type: "text", Text: "B"}}
	for _, standard := range []string{"openai-responses", "codex"} {
		proto, _ := ProtocolByID(standard)
		body, err := proto.EncodeRequest(&ChatRequest{Model: "fixture", Messages: []ChatMessage{{Role: "tool", ToolCallID: "remote-call", Content: PartsContent(parts)}}})
		if err != nil {
			t.Fatal(err)
		}
		var request map[string]any
		if err := json.Unmarshal(body, &request); err != nil {
			t.Fatal(err)
		}
		item := request["input"].([]any)[0].(map[string]any)
		output := item["output"].([]any)
		if item["call_id"] != "remote-call" || len(output) != 3 || output[0].(map[string]any)["text"] != "A" || output[1].(map[string]any)["type"] != "input_image" || output[2].(map[string]any)["text"] != "B" {
			t.Fatalf("%s lost attributed order: %s", standard, body)
		}
	}
	_, wire, err := encodeAnthropicMessages([]ChatMessage{{Role: "tool", ToolCallID: "remote-call", Content: PartsContent(parts), ToolError: true}})
	if err != nil {
		t.Fatal(err)
	}
	result := wire[0].Content[0].(map[string]any)
	blocks := result["content"].([]any)
	if result["tool_use_id"] != "remote-call" || result["is_error"] != true || len(blocks) != 3 || blocks[0].(map[string]any)["text"] != "A" || blocks[1].(map[string]any)["type"] != "image" || blocks[2].(map[string]any)["text"] != "B" {
		t.Fatalf("Anthropic reordered attributed result: %#v", result)
	}
	ollama, err := encodeOllamaMessages([]ChatMessage{NewMultiPartMessage("user", parts)})
	if err != nil || ollama[0]["content"] != "A\n[Image 1 in this message's images array]\nB" || ollama[0]["images"].([]string)[0] != "aW1hZ2U=" {
		t.Fatalf("Ollama lost positional image reference: %#v %v", ollama, err)
	}
}
