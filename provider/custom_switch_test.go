package provider

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSwitchFromCustomToolPreservesRawHistory(t *testing.T) {
	messages := []ChatMessage{{Role: "assistant", ToolCalls: []ToolCall{{Type: "custom", ID: "p", Function: FunctionCall{Name: "apply_patch", Arguments: customPatch}}}}, {Role: "tool", ToolCallID: "p", Content: TextContent("Success.")}}
	for _, std := range []Protocol{&AnthropicStandard{}, &OllamaStandard{}} {
		body, err := std.EncodeRequest(&ChatRequest{Model: "other-model", Messages: messages})
		if err != nil {
			t.Fatal(err)
		}
		encoded, _ := json.Marshal(customPatch)
		if !strings.Contains(string(body), string(encoded)) {
			t.Fatalf("%s lost raw patch history: %s", std.Info().ID, body)
		}
	}
}
