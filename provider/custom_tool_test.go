package provider

import (
	"encoding/json"
	"testing"
)

const customPatch = "*** Begin Patch\n*** Add File: a.txt\n+raw \\\" text\n*** End Patch"

func TestCustomToolResponsesRoundTrip(t *testing.T) {
	std := &OpenAIResponsesStandard{}
	item := map[string]any{"id": "item1", "type": "custom_tool_call", "call_id": "patch1", "name": "apply_patch", "input": customPatch, "status": "completed"}
	raw, err := json.Marshal(map[string]any{"status": "completed", "output": []any{item}})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := std.DecodeResponse(raw)
	if err != nil {
		t.Fatal(err)
	}
	msg := resp.Choices[0].Message
	if len(msg.ToolCalls) != 1 || msg.ToolCalls[0].Type != "custom" || msg.ToolCalls[0].Function.Arguments != customPatch {
		t.Fatalf("%+v", msg.ToolCalls)
	}
	params, err := ToolCallParams(msg.ToolCalls[0])
	if err != nil || params["input"] != customPatch {
		t.Fatalf("%v %v", params, err)
	}
	for _, preserved := range []bool{true, false} {
		if !preserved {
			msg.ProviderItems = nil
		}
		body, err := std.EncodeRequest(&ChatRequest{Model: "gpt-test", Messages: []ChatMessage{msg, {Role: "tool", ToolCallID: "patch1", Content: TextContent("Success.")}}, Tools: []ToolDef{{Type: "custom", Function: FunctionDef{Name: "apply_patch", Description: "FREEFORM"}, Format: &ToolFormat{Type: "grammar", Syntax: "lark", Definition: "grammar"}}}})
		if err != nil {
			t.Fatal(err)
		}
		var wire struct {
			Input []map[string]any `json:"input"`
			Tools []map[string]any `json:"tools"`
		}
		if err := json.Unmarshal(body, &wire); err != nil {
			t.Fatal(err)
		}
		if len(wire.Input) != 2 || wire.Input[0]["input"] != customPatch || wire.Input[0]["type"] != "custom_tool_call" || wire.Input[1]["type"] != "custom_tool_call_output" {
			t.Fatalf("%s", body)
		}
		if wire.Tools[0]["type"] != "custom" || wire.Tools[0]["parameters"] != nil {
			t.Fatalf("%s", body)
		}
	}
}

func TestCustomToolStreamingEvents(t *testing.T) {
	d := (&OpenAIResponsesStandard{}).NewStreamDecoder()
	decode := func(event any) StreamUpdate {
		t.Helper()
		raw, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		u, err := d.Decode(StreamEvent{Data: raw})
		if err != nil {
			t.Fatal(err)
		}
		return u
	}
	item := map[string]any{"id": "item1", "type": "custom_tool_call", "call_id": "patch1", "name": "apply_patch", "input": "", "status": "in_progress"}
	u := decode(map[string]any{"type": "response.output_item.added", "output_index": 2, "item": item})
	if u.ToolCalls[0].Type != "custom" || u.ToolCalls[0].ID != "patch1" {
		t.Fatal(u)
	}
	u = decode(map[string]any{"type": "response.custom_tool_call_input.delta", "output_index": 2, "delta": customPatch[:15]})
	if u.ToolCalls[0].Arguments != customPatch[:15] {
		t.Fatal(u)
	}
	u = decode(map[string]any{"type": "response.custom_tool_call_input.done", "output_index": 2, "input": customPatch})
	if !u.ToolCalls[0].ReplaceArguments || u.ToolCalls[0].Arguments != customPatch {
		t.Fatal(u)
	}
	item["input"] = customPatch
	item["status"] = "completed"
	u = decode(map[string]any{"type": "response.completed", "response": map[string]any{"status": "completed", "output": []any{item}}})
	if !u.Done || u.FinishReason != "tool_calls" || u.ToolCalls[0].Type != "custom" || u.ToolCalls[0].Arguments != customPatch {
		t.Fatal(u)
	}
}

func TestCustomChatCompletionsAndPersistence(t *testing.T) {
	input := `{"index":0,"id":"patch1","type":"custom","custom":{"name":"apply_patch","input":"*** Begin Patch\n*** End Patch"}}`
	var call ToolCall
	if err := json.Unmarshal([]byte(input), &call); err != nil {
		t.Fatal(err)
	}
	stored, err := json.Marshal(call)
	if err != nil {
		t.Fatal(err)
	}
	var restored ToolCall
	if err := json.Unmarshal(stored, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.Type != "custom" || restored.Function.Arguments != "*** Begin Patch\n*** End Patch" {
		t.Fatal(restored)
	}
	body, err := (&OpenAIStandard{}).EncodeRequest(&ChatRequest{Messages: []ChatMessage{{Role: "assistant", ToolCalls: []ToolCall{restored}}}, Tools: []ToolDef{{Type: "custom", Function: FunctionDef{Name: "apply_patch"}, Format: &ToolFormat{Type: "grammar", Syntax: "lark", Definition: "grammar"}}}})
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Tools []struct {
			Type     string      `json:"type"`
			Function FunctionDef `json:"function"`
			Custom   any         `json:"custom"`
		} `json:"tools"`
		Messages []struct {
			ToolCalls []struct {
				Type     string       `json:"type"`
				Function FunctionCall `json:"function"`
				Custom   any          `json:"custom"`
			} `json:"tool_calls"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &wire); err != nil {
		t.Fatal(err)
	}
	if len(wire.Tools) != 1 || wire.Tools[0].Type != "function" || wire.Tools[0].Custom != nil || wire.Tools[0].Function.Name != "apply_patch" {
		t.Fatalf("invalid Chat Completions tool definition: %s", body)
	}
	var schema struct {
		Properties map[string]struct {
			Type string `json:"type"`
		} `json:"properties"`
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(wire.Tools[0].Function.Parameters, &schema); err != nil || schema.Properties["input"].Type != "string" || len(schema.Required) != 1 || schema.Required[0] != "input" {
		t.Fatalf("invalid Chat Completions patch schema: %s (%v)", body, err)
	}
	if len(wire.Messages) != 1 || len(wire.Messages[0].ToolCalls) != 1 || wire.Messages[0].ToolCalls[0].Type != "function" || wire.Messages[0].ToolCalls[0].Custom != nil {
		t.Fatalf("invalid Chat Completions replay: %s", body)
	}
	var args map[string]string
	if err := json.Unmarshal([]byte(wire.Messages[0].ToolCalls[0].Function.Arguments), &args); err != nil || args["input"] != restored.Function.Arguments {
		t.Fatalf("patch lost on Chat Completions replay: %s (%v)", body, err)
	}
}
