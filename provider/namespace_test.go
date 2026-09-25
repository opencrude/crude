package provider

import (
	"encoding/json"
	"testing"
)

func TestNativeParallelNamespaceRoundTrip(t *testing.T) {
	std := &OpenAIResponsesStandard{}
	def := ToolDef{Type: "function", Function: FunctionDef{Name: "multi_tool_use.parallel", Parameters: json.RawMessage(`{"type":"object"}`)}}
	response, err := std.DecodeResponse([]byte(`{"status":"completed","output":[{"type":"function_call","namespace":"multi_tool_use","name":"parallel","call_id":"p","arguments":"{}"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	msg := response.Choices[0].Message
	if msg.ToolCalls[0].Function.Name != "multi_tool_use.parallel" {
		t.Fatal(msg.ToolCalls)
	}
	msg.ProviderItems = nil
	body, err := std.EncodeRequest(&ChatRequest{Tools: []ToolDef{def}, Messages: []ChatMessage{msg}})
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Tools []map[string]any `json:"tools"`
		Input []map[string]any `json:"input"`
	}
	if err := json.Unmarshal(body, &wire); err != nil {
		t.Fatal(err)
	}
	if wire.Tools[0]["type"] != "namespace" || wire.Tools[0]["name"] != "multi_tool_use" || wire.Input[0]["namespace"] != "multi_tool_use" || wire.Input[0]["name"] != "parallel" {
		t.Fatalf("%s", body)
	}
	var call ToolCall
	if err := json.Unmarshal([]byte(`{"type":"function","function":{"name":"multi_tool_use_parallel","arguments":"{}"}}`), &call); err != nil {
		t.Fatal(err)
	}
	if call.Function.Name != "multi_tool_use.parallel" {
		t.Fatal(call)
	}
}
