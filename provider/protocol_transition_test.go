package provider

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

func TestEveryWireProtocolReprojectsCanonicalHistoryWithoutMutation(t *testing.T) {
	fixtures := map[string]string{
		"openai":           `{"choices":[{"message":{"role":"assistant","content":"historical commentary","tool_calls":[{"id":"read-1","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"a.go\"}"}}]},"finish_reason":"tool_calls"}]}`,
		"openai-responses": `{"id":"resp-1","status":"completed","store":true,"output":[{"type":"reasoning","id":"reason-1","summary":[],"encrypted_content":"opaque-source-only"},{"type":"message","id":"message-1","role":"assistant","status":"completed","phase":"commentary","content":[{"type":"output_text","text":"historical commentary","annotations":[]}]},{"type":"function_call","call_id":"read-1","name":"read_file","arguments":"{\"path\":\"a.go\"}","status":"completed"}]}`,
		"anthropic":        `{"type":"message","id":"msg-1","role":"assistant","stop_reason":"tool_use","content":[{"type":"thinking","thinking":"private trace","signature":"opaque-source-only"},{"type":"text","text":"historical commentary"},{"type":"tool_use","id":"read-1","name":"read_file","input":{"path":"a.go"}}]}`,
		"ollama":           `{"model":"test-model","done":true,"done_reason":"stop","message":{"role":"assistant","content":"historical commentary","tool_calls":[{"id":"read-1","function":{"name":"read_file","arguments":{"path":"a.go"}}}]}}`,
	}
	fixtures["codex"] = fixtures["openai-responses"]
	tools := []ToolDef{
		{Type: "function", Function: FunctionDef{Name: "read_file", Parameters: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`)}},
		{Type: "custom", Function: FunctionDef{Name: "apply_patch", Description: "Apply a patch"}, Format: &ToolFormat{Type: "grammar", Syntax: "lark", Definition: "start: /.+/"}},
		{Type: "function", Function: FunctionDef{Name: "multi_tool_use.parallel", Parameters: json.RawMessage(`{"type":"object","properties":{"tool_uses":{"type":"array"}}}`)}},
	}
	for _, source := range ProtocolList() {
		for _, target := range ProtocolList() {
			t.Run(source.Info().ID+"_to_"+target.Info().ID, func(t *testing.T) {
				fixture, ok := fixtures[source.Info().ID]
				if !ok {
					t.Fatalf("new protocol %s needs a transition fixture", source.Info().ID)
				}
				decoded, err := source.DecodeResponse([]byte(fixture))
				if err != nil {
					t.Fatal(err)
				}
				producer := &Provider{ID: "configured-provider", BaseURL: "https://example.test/v1", StandardName: source.Info().ID}
				current := &Provider{ID: producer.ID, BaseURL: producer.BaseURL, StandardName: target.Info().ID}
				prefix := []ChatMessage{NewTextMessage("system", "stable system policy"), NewTextMessage("developer", "stable developer policy"), NewTextMessage("user", "historical human request")}
				assistant := decoded.Choices[0].Message
				stamp := RequestPrefixHash(prefix, tools)
				assistant.ProviderItems = StampProviderItems(assistant.ProviderItems, producer, "producing-model", stamp)
				assistant.ReplayScope, assistant.ReplayPrefixHash, assistant.ReplaySourceModel = producer.ReplayScope(), stamp, "producing-model"
				history := append(prefix, assistant,
					ChatMessage{Role: "tool", ToolCallID: "read-1", Content: TextContent("historical file evidence")},
					ChatMessage{Role: "assistant", Content: TextContent("historical patch commentary"), ToolCalls: []ToolCall{{ID: "patch-1", Type: "custom", Function: FunctionCall{Name: "apply_patch", Arguments: "*** Begin Patch\n*** Add File: a.go\n+package a\n*** End Patch"}}}},
					ChatMessage{Role: "tool", ToolCallID: "patch-1", Content: TextContent("historical patch outcome")},
					NewTextMessage("assistant", "historical terminal answer"), NewTextMessage("user", "new human request"))
				before, err := json.Marshal(history)
				if err != nil {
					t.Fatal(err)
				}
				request := ChatRequest{Model: "target-model", Tools: tools, Messages: FilterReplayMessages(history, tools, current)}
				body, err := target.EncodeRequest(&request)
				if err != nil {
					t.Fatal(err)
				}
				validateTransitionWire(t, target.Info().ID, body)
				for _, fact := range []string{"stable system policy", "stable developer policy", "historical human request", "historical commentary", "historical file evidence", "historical patch commentary", "historical patch outcome", "historical terminal answer", "new human request", "read-1", "patch-1"} {
					if !bytes.Contains(body, []byte(fact)) {
						t.Errorf("wire projection lost %q", fact)
					}
				}
				if source.Info().ID != target.Info().ID && bytes.Contains(body, []byte("opaque-source-only")) {
					t.Fatal("opaque producer state crossed a protocol boundary")
				}
				again, err := target.EncodeRequest(&request)
				if err != nil || !bytes.Equal(body, again) {
					t.Fatal("unchanged configuration produced different wire bytes")
				}
				request.Messages = FilterReplayMessages(history, tools, current)
				again, err = target.EncodeRequest(&request)
				if err != nil || !bytes.Equal(body, again) {
					t.Fatal("reprojection rewrote the warm request prefix")
				}
				after, err := json.Marshal(history)
				if err != nil || !bytes.Equal(before, after) {
					t.Fatal("protocol transition mutated canonical history")
				}
			})
		}
	}
}

func validateTransitionWire(t *testing.T, protocol string, body []byte) {
	t.Helper()
	var wire map[string]any
	if err := json.Unmarshal(body, &wire); err != nil {
		t.Fatal(err)
	}
	validName := regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
	for _, raw := range wire["tools"].([]any) {
		tool := raw.(map[string]any)
		if protocol == "openai-responses" || protocol == "codex" {
			continue
		}
		var name string
		var schema any
		if protocol == "anthropic" {
			name, _ = tool["name"].(string)
			schema = tool["input_schema"]
		} else {
			if tool["type"] != "function" {
				t.Errorf("unsupported tool kind: %v", tool["type"])
			}
			function := tool["function"].(map[string]any)
			name, _ = function["name"].(string)
			schema = function["parameters"]
		}
		if !validName.MatchString(name) {
			t.Errorf("illegal function name %q", name)
		}
		object, ok := schema.(map[string]any)
		if !ok || object["type"] != "object" {
			t.Errorf("function %q lacks an object input schema", name)
		}
	}
	if protocol == "openai-responses" || protocol == "codex" {
		for _, raw := range wire["input"].([]any) {
			item := raw.(map[string]any)
			if item["role"] != "assistant" {
				continue
			}
			if _, ok := item["content"].(string); ok {
				continue
			}
			for _, part := range item["content"].([]any) {
				kind := part.(map[string]any)["type"]
				if kind != "output_text" && kind != "refusal" {
					t.Errorf("assistant content uses invalid Responses kind %v", kind)
				}
			}
		}
	}
	if protocol == "ollama" {
		for _, raw := range wire["messages"].([]any) {
			role := raw.(map[string]any)["role"]
			if role != "system" && role != "user" && role != "assistant" && role != "tool" {
				t.Errorf("unsupported Ollama role %v", role)
			}
		}
	}
	if strings.Contains(string(body), "response_reference") || strings.Contains(string(body), "replay_scope") {
		t.Fatal("internal persistence metadata leaked into wire payload")
	}
}
