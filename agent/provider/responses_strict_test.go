package provider

import (
	"encoding/json"
	"testing"
)

// gpt-6.2 rejected every request whose tools carried uniqueItems ("Invalid
// schema for 'tools[10].parameters': Unimplemented keys: [uniqueItems]")
// because Responses compiles schemas in strict mode unless the request opts
// out. Every function tool must opt out and keep its schema unchanged.
func TestResponsesFunctionToolsOptOutOfStrictSchemas(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"options":{"type":"array","uniqueItems":true,"items":{"type":"string","minLength":1}}}}`)
	req := &ChatRequest{Model: "gpt-6.2-sol", Messages: []ChatMessage{{Role: "user", Content: TextContent("hi")}}, Tools: []ToolDef{
		{Type: "function", Function: FunctionDef{Name: "question", Description: "ask", Parameters: schema}},
		{Type: "function", Function: FunctionDef{Name: "multi_tool_use.parallel", Description: "parallel", Parameters: schema}},
		{Type: "custom", Function: FunctionDef{Name: "apply_patch", Description: "FREEFORM"}, Format: &ToolFormat{Type: "grammar", Syntax: "lark", Definition: "grammar"}},
	}}
	for name, std := range map[string]interface {
		EncodeRequest(*ChatRequest) ([]byte, error)
	}{"responses": &OpenAIResponsesStandard{}, "codex": &CodexStandard{}} {
		body, err := std.EncodeRequest(req)
		if err != nil {
			t.Fatal(err)
		}
		var wire struct {
			Tools []map[string]any `json:"tools"`
		}
		if err := json.Unmarshal(body, &wire); err != nil {
			t.Fatal(err)
		}
		functions := 0
		var visit func(tool map[string]any)
		visit = func(tool map[string]any) {
			switch tool["type"] {
			case "namespace":
				for _, nested := range tool["tools"].([]any) {
					visit(nested.(map[string]any))
				}
			case "function":
				functions++
				if strict, ok := tool["strict"].(bool); !ok || strict {
					t.Errorf("%s: function %v strict = %v, want false", name, tool["name"], tool["strict"])
				}
				params, _ := json.Marshal(tool["parameters"])
				var got, want any
				_ = json.Unmarshal(params, &got)
				_ = json.Unmarshal(schema, &want)
				gotJSON, _ := json.Marshal(got)
				wantJSON, _ := json.Marshal(want)
				if string(gotJSON) != string(wantJSON) {
					t.Errorf("%s: schema changed: %s", name, params)
				}
			case "custom":
				if _, ok := tool["strict"]; ok {
					t.Errorf("%s: custom tool has a strict field", name)
				}
			}
		}
		for _, tool := range wire.Tools {
			visit(tool)
		}
		if functions != 2 {
			t.Fatalf("%s: %d function tools, want 2: %s", name, functions, body)
		}
	}
}
