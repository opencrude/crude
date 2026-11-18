package provider

import (
	"encoding/json"
	"testing"
)

func TestResponsesNamespaceRoundTrip(t *testing.T) {
	std := &OpenAIResponsesStandard{}
	def := ToolDef{Type: "function", Function: FunctionDef{Name: "project.search", Parameters: json.RawMessage(`{"type":"object"}`)}}
	response, err := std.DecodeResponse([]byte(`{"status":"completed","output":[{"type":"function_call","namespace":"project","name":"search","call_id":"p","arguments":"{}"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	msg := response.Choices[0].Message
	if msg.ToolCalls[0].Function.Name != "project.search" {
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
	if wire.Tools[0]["type"] != "namespace" || wire.Tools[0]["name"] != "project" || wire.Input[0]["namespace"] != "project" || wire.Input[0]["name"] != "search" {
		t.Fatalf("%s", body)
	}
}

func TestResponsesParallelToolUsesLegalFunctionName(t *testing.T) {
	for _, protocol := range []Protocol{&OpenAIResponsesStandard{}, &CodexStandard{}} {
		t.Run(protocol.Info().ID, func(t *testing.T) {
			body, err := protocol.EncodeRequest(&ChatRequest{
				Tools:      []ToolDef{{Type: "function", Function: FunctionDef{Name: "multi_tool_use.parallel", Parameters: json.RawMessage(`{"type":"object"}`)}}},
				ToolChoice: map[string]any{"type": "function", "function": map[string]any{"name": "multi_tool_use.parallel"}},
				Messages:   []ChatMessage{{Role: "assistant", ToolCalls: []ToolCall{{ID: "p", Type: "function", Function: FunctionCall{Name: "multi_tool_use.parallel", Arguments: "{}"}}}}},
			})
			if err != nil {
				t.Fatal(err)
			}
			var wire struct {
				Tools  []map[string]any `json:"tools"`
				Input  []map[string]any `json:"input"`
				Choice map[string]any   `json:"tool_choice"`
			}
			if err := json.Unmarshal(body, &wire); err != nil {
				t.Fatal(err)
			}
			if wire.Tools[0]["type"] != "function" || wire.Tools[0]["name"] != "multi_tool_use_parallel" ||
				wire.Input[0]["namespace"] != nil || wire.Input[0]["name"] != "multi_tool_use_parallel" || wire.Choice["name"] != "multi_tool_use_parallel" {
				t.Fatalf("provider-reserved namespace was exposed: %s", body)
			}
			response, err := protocol.DecodeResponse([]byte(`{"status":"completed","output":[{"type":"function_call","name":"multi_tool_use_parallel","call_id":"p","arguments":"{}"}]}`))
			if err != nil {
				t.Fatal(err)
			}
			if got := response.Choices[0].Message.ToolCalls[0].Function.Name; got != "multi_tool_use.parallel" {
				t.Fatalf("canonical execution identity=%q", got)
			}
		})
	}
	var call ToolCall
	if err := json.Unmarshal([]byte(`{"type":"function","function":{"name":"multi_tool_use_parallel","arguments":"{}"}}`), &call); err != nil {
		t.Fatal(err)
	}
	if call.Function.Name != "multi_tool_use.parallel" {
		t.Fatal(call)
	}
}

func TestResponsesParallelStreamingKeepsCanonicalIdentity(t *testing.T) {
	decoder := (&OpenAIResponsesStandard{}).NewStreamDecoder()
	for _, data := range []string{
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","name":"multi_tool_use_parallel","call_id":"p","arguments":"","status":"in_progress"}}`,
		`{"type":"response.function_call_arguments.done","output_index":0,"name":"multi_tool_use_parallel","arguments":"{}"}`,
		`{"type":"response.completed","response":{"status":"completed","output":[{"type":"function_call","name":"multi_tool_use_parallel","call_id":"p","arguments":"{}","status":"completed"}]}}`,
	} {
		update, err := decoder.Decode(StreamEvent{Data: []byte(data)})
		if err != nil {
			t.Fatal(err)
		}
		if len(update.ToolCalls) != 1 || update.ToolCalls[0].Name != "multi_tool_use.parallel" {
			t.Fatalf("stream identity drift: %+v", update.ToolCalls)
		}
	}
}
