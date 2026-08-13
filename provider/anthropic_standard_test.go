package provider

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestAnthropicRequestEncoding(t *testing.T) {
	std := &AnthropicStandard{}
	thinking := json.RawMessage(`{"type":"thinking","thinking":"private","signature":"sig"}`)
	body, err := std.EncodeRequest(&ChatRequest{
		Model: "claude-test",
		Messages: []ChatMessage{
			{Role: "system", Content: TextContent("be precise"), CacheControl: &CacheControl{Type: "ephemeral"}},
			{Role: "user", Content: PartsContent([]ContentPart{
				{Type: "text", Text: "inspect"},
				{Type: "image_url", ImageURL: &ImageURL{URL: "data:image/png;base64,YWJj"}},
			})},
			{
				Role:    "assistant",
				Content: TextContent(""),
				ProviderItems: []ProviderItem{{
					Standard: "anthropic", Type: "thinking", Data: thinking,
				}},
				ToolCalls: []ToolCall{{
					ID: "toolu_1", Type: "function",
					Function: FunctionCall{Name: "read_file", Arguments: `{"path":"a.go"}`},
				}},
			},
			{Role: "tool", ToolCallID: "toolu_1", Content: TextContent("missing"), ToolError: true},
			{Role: "tool", ToolCallID: "toolu_2", Content: TextContent("ok")},
		},
		Tools: []ToolDef{{
			Type: "function",
			Function: FunctionDef{
				Name: "read_file", Description: "read",
				Parameters: json.RawMessage(`{"type":"object"}`),
			},
		}},
		Stream:          true,
		MaxOutputTokens: 4096,
	})
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}

	var request map[string]any
	if err := json.Unmarshal(body, &request); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	if request["max_tokens"].(float64) != 4096 {
		t.Fatalf("max_tokens = %v", request["max_tokens"])
	}
	system := request["system"].([]any)
	if len(system) != 1 || system[0].(map[string]any)["text"] != "be precise" {
		t.Fatalf("system = %#v", system)
	}
	if got := system[0].(map[string]any)["cache_control"]; !reflect.DeepEqual(got, map[string]any{"type": "ephemeral"}) {
		t.Fatalf("system cache_control = %#v", got)
	}
	messages := request["messages"].([]any)
	if len(messages) != 3 {
		t.Fatalf("messages = %d, want 3: %#v", len(messages), messages)
	}
	assistant := messages[1].(map[string]any)
	assistantContent := assistant["content"].([]any)
	if assistantContent[0].(map[string]any)["type"] != "thinking" ||
		assistantContent[1].(map[string]any)["type"] != "tool_use" {
		t.Fatalf("assistant content = %#v", assistantContent)
	}
	toolResults := messages[2].(map[string]any)["content"].([]any)
	if len(toolResults) != 2 {
		t.Fatalf("grouped tool results = %d", len(toolResults))
	}
	if toolResults[0].(map[string]any)["is_error"] != true {
		t.Fatalf("first tool result is not marked error: %#v", toolResults[0])
	}
	tools := request["tools"].([]any)
	if _, ok := tools[0].(map[string]any)["input_schema"]; !ok {
		t.Fatalf("Anthropic tool lacks input_schema: %#v", tools[0])
	}
}

func TestAnthropicHeadersAndThinkingConfig(t *testing.T) {
	std := &AnthropicStandard{}
	headers := std.Headers("sk-ant")
	if headers["x-api-key"] != "sk-ant" || headers["anthropic-version"] != anthropicAPIVersion {
		t.Fatalf("headers = %#v", headers)
	}
	extra := std.ExtraBody("claude-sonnet-4-6", "high")
	if extra["thinking"].(map[string]any)["type"] != "adaptive" {
		t.Fatalf("thinking config = %#v", extra)
	}
	if extra["output_config"].(map[string]any)["effort"] != "high" {
		t.Fatalf("output config = %#v", extra)
	}
	if got := std.ExtraBody("claude-sonnet-4-5", "high"); got != nil {
		t.Fatalf("older model received adaptive thinking: %#v", got)
	}
	disabled := std.ExtraBody("claude-sonnet-4-5", "none")
	if disabled["thinking"].(map[string]any)["type"] != "disabled" {
		t.Fatalf("disabled thinking config = %#v", disabled)
	}
}

func TestAnthropicEncodesMultipartToolResults(t *testing.T) {
	body, err := (&AnthropicStandard{}).EncodeRequest(&ChatRequest{
		Model: "claude-test",
		Messages: []ChatMessage{{
			Role: "tool", ToolCallID: "toolu_1",
			Content: PartsContent([]ContentPart{
				{Type: "text", Text: "screenshot"},
				{Type: "image_url", ImageURL: &ImageURL{URL: "data:image/png;base64,YWJj"}},
			}),
		}},
	})
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}
	var request map[string]any
	if err := json.Unmarshal(body, &request); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	message := request["messages"].([]any)[0].(map[string]any)
	toolResult := message["content"].([]any)[0].(map[string]any)
	content := toolResult["content"].([]any)
	if len(content) != 2 || content[1].(map[string]any)["type"] != "image" {
		t.Fatalf("tool result content = %#v", content)
	}
}

func TestAnthropicBoundsPromptCacheBreakpoints(t *testing.T) {
	cache := &CacheControl{Type: "ephemeral"}
	request := &ChatRequest{
		Model: "claude-test",
		Messages: []ChatMessage{
			{Role: "system", Content: TextContent("first"), CacheControl: cache},
			{Role: "system", Content: TextContent("second"), CacheControl: cache},
			{Role: "tool", ToolCallID: "toolu_1", Content: TextContent("one"), CacheControl: cache},
			{Role: "tool", ToolCallID: "toolu_2", Content: TextContent("two"), CacheControl: cache},
		},
	}
	for _, name := range []string{"one", "two", "three"} {
		request.Tools = append(request.Tools, ToolDef{
			Type: "function",
			Function: FunctionDef{
				Name: name, Parameters: json.RawMessage(`{"type":"object"}`),
			},
			CacheControl: cache,
		})
	}
	body, err := (&AnthropicStandard{}).EncodeRequest(request)
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}
	var wire map[string]any
	if err := json.Unmarshal(body, &wire); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	system := wire["system"].([]any)
	if _, exists := system[0].(map[string]any)["cache_control"]; exists {
		t.Fatalf("old system cache breakpoint retained: %#v", system)
	}
	if _, exists := system[1].(map[string]any)["cache_control"]; !exists {
		t.Fatalf("latest system cache breakpoint missing: %#v", system)
	}
	messageBlocks := wire["messages"].([]any)[0].(map[string]any)["content"].([]any)
	if _, exists := messageBlocks[0].(map[string]any)["cache_control"]; exists {
		t.Fatalf("old message cache breakpoint retained: %#v", messageBlocks)
	}
	if _, exists := messageBlocks[1].(map[string]any)["cache_control"]; !exists {
		t.Fatalf("latest message cache breakpoint missing: %#v", messageBlocks)
	}
	tools := wire["tools"].([]any)
	for i := 0; i < len(tools)-1; i++ {
		if _, exists := tools[i].(map[string]any)["cache_control"]; exists {
			t.Fatalf("old tool cache breakpoint retained: %#v", tools)
		}
	}
	if _, exists := tools[len(tools)-1].(map[string]any)["cache_control"]; !exists {
		t.Fatalf("latest tool cache breakpoint missing: %#v", tools)
	}
}

func TestAnthropicStreamDecoderUsageThinkingAndTool(t *testing.T) {
	decoder := (&AnthropicStandard{}).NewStreamDecoder()
	events := []string{
		`{"type":"message_start","message":{"usage":{"input_tokens":10,"output_tokens":1,"cache_creation_input_tokens":2,"cache_read_input_tokens":3}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"reason"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_1","name":"read_file","input":{}}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"path\":"}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"\"a.go\"}"}}`,
		`{"type":"content_block_stop","index":1}`,
		`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":9}}`,
		`{"type":"message_stop"}`,
	}

	var usage TokenUsage
	var reasoning string
	var providerItems []ProviderItem
	var toolDeltas []ToolCallDelta
	var finish string
	var done bool
	for _, data := range events {
		update, err := decoder.Decode(StreamEvent{Data: []byte(data)})
		if err != nil {
			t.Fatalf("Decode(%s): %v", data, err)
		}
		if update.Usage != nil {
			usage = *update.Usage
		}
		reasoning += update.ReasoningDelta
		providerItems = append(providerItems, update.ProviderItems...)
		toolDeltas = append(toolDeltas, update.ToolCalls...)
		if update.FinishReason != "" {
			finish = update.FinishReason
		}
		done = done || update.Done
	}

	if usage.PromptTokens != 15 || usage.PromptTokensCached != 3 ||
		usage.PromptTokensWritten != 2 || usage.CompletionTokens != 9 {
		t.Fatalf("usage = %+v", usage)
	}
	if reasoning != "reason" {
		t.Fatalf("reasoning = %q", reasoning)
	}
	if len(providerItems) != 2 {
		t.Fatalf("provider items = %d", len(providerItems))
	}
	var block map[string]any
	if err := json.Unmarshal(providerItems[0].Data, &block); err != nil {
		t.Fatalf("unmarshal thinking item: %v", err)
	}
	if block["thinking"] != "reason" || block["signature"] != "sig" {
		t.Fatalf("thinking item = %#v", block)
	}
	if len(toolDeltas) != 4 || toolDeltas[0].ID != "toolu_1" ||
		toolDeltas[0].Name != "read_file" {
		t.Fatalf("tool deltas = %+v", toolDeltas)
	}
	if got := toolDeltas[len(toolDeltas)-1]; !got.ReplaceArguments || got.Arguments != `{"path":"a.go"}` {
		t.Fatalf("final tool delta = %+v", got)
	}
	if finish != "tool_use" || !done {
		t.Fatalf("finish=%q done=%v", finish, done)
	}
}

func TestAnthropicDecodeNonStreaming(t *testing.T) {
	response, err := (&AnthropicStandard{}).DecodeResponse([]byte(`{
		"id":"msg_1",
		"type":"message",
		"role":"assistant",
		"model":"claude-test",
		"content":[
			{"type":"thinking","thinking":"reason","signature":"sig"},
			{"type":"text","text":"answer"},
			{"type":"tool_use","id":"toolu_1","name":"read_file","input":{"path":"a.go"}}
		],
		"stop_reason":"tool_use",
		"usage":{"input_tokens":10,"output_tokens":5,"cache_creation_input_tokens":2,"cache_read_input_tokens":4}
	}`))
	if err != nil {
		t.Fatalf("DecodeResponse: %v", err)
	}
	message := response.Choices[0].Message
	if message.ContentString() != "answer" || message.ReasoningContent != "reason" {
		t.Fatalf("message = %+v", message)
	}
	if len(message.ProviderItems) != 3 || len(message.ToolCalls) != 1 {
		t.Fatalf("message metadata = %+v", message)
	}
	if response.Usage.PromptTokens != 16 || response.Usage.PromptTokensDetails.CachedTokens != 4 ||
		response.Usage.PromptTokensDetails.CacheWriteTokens != 2 {
		t.Fatalf("usage = %+v", response.Usage)
	}
}

func TestAnthropicStreamZeroArgumentToolFinalizesObject(t *testing.T) {
	decoder := (&AnthropicStandard{}).NewStreamDecoder()
	if _, err := decoder.Decode(StreamEvent{Data: []byte(
		`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"list_files","input":{}}}`,
	)}); err != nil {
		t.Fatalf("start: %v", err)
	}
	update, err := decoder.Decode(StreamEvent{Data: []byte(
		`{"type":"content_block_stop","index":0}`,
	)})
	if err != nil {
		t.Fatalf("stop: %v", err)
	}
	if len(update.ToolCalls) != 1 || update.ToolCalls[0].Arguments != "{}" ||
		!update.ToolCalls[0].ReplaceArguments {
		t.Fatalf("tool update = %+v", update.ToolCalls)
	}
	if len(update.ProviderItems) != 1 {
		t.Fatalf("provider items = %+v", update.ProviderItems)
	}
}

func TestAnthropicPreservesAssistantBlockOrder(t *testing.T) {
	decoded, err := (&AnthropicStandard{}).DecodeResponse([]byte(`{
		"type":"message",
		"role":"assistant",
		"content":[
			{"type":"thinking","thinking":"r1","signature":"s1"},
			{"type":"text","text":"before"},
			{"type":"thinking","thinking":"r2","signature":"s2"},
			{"type":"tool_use","id":"toolu_1","name":"read_file","input":{"path":"a.go"}}
		],
		"stop_reason":"tool_use",
		"usage":{}
	}`))
	if err != nil {
		t.Fatalf("DecodeResponse: %v", err)
	}
	body, err := (&AnthropicStandard{}).EncodeRequest(&ChatRequest{
		Model:    "claude-sonnet-4-6",
		Messages: []ChatMessage{decoded.Choices[0].Message},
	})
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}
	var request map[string]any
	if err := json.Unmarshal(body, &request); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	blocks := request["messages"].([]any)[0].(map[string]any)["content"].([]any)
	var got []string
	for _, raw := range blocks {
		got = append(got, raw.(map[string]any)["type"].(string))
	}
	want := []string{"thinking", "text", "thinking", "tool_use"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("block order = %v, want %v", got, want)
	}
}

func TestAnthropicReplaysMalformedToolArgumentsAsEmptyInput(t *testing.T) {
	body, err := (&AnthropicStandard{}).EncodeRequest(&ChatRequest{
		Model: "claude-sonnet-4-6",
		Messages: []ChatMessage{{
			Role: "assistant",
			ToolCalls: []ToolCall{{
				ID:       "toolu_1",
				Function: FunctionCall{Name: "read_file", Arguments: `{"path":`},
			}},
		}},
	})
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}
	var request map[string]any
	if err := json.Unmarshal(body, &request); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	block := request["messages"].([]any)[0].(map[string]any)["content"].([]any)[0].(map[string]any)
	if input := block["input"].(map[string]any); len(input) != 0 {
		t.Fatalf("fallback input = %#v", input)
	}
}

func TestAnthropicRejectsMalformedImage(t *testing.T) {
	_, err := (&AnthropicStandard{}).EncodeRequest(&ChatRequest{
		Model: "claude-sonnet-4-6",
		Messages: []ChatMessage{{
			Role: "user",
			Content: PartsContent([]ContentPart{{
				Type: "image_url", ImageURL: &ImageURL{URL: "data:image/svg+xml;base64,not-base64"},
			}}),
		}},
	})
	if err == nil {
		t.Fatal("expected malformed image error")
	}
}
