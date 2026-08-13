package provider

import (
	"encoding/json"
	"testing"
)

func TestOpenAIStandardEncodesCompatibleTokenLimit(t *testing.T) {
	std := &OpenAIStandard{}
	body, err := std.EncodeRequest(&ChatRequest{
		Model:               "model",
		Messages:            []ChatMessage{NewTextMessage("user", "hello")},
		Stream:              true,
		MaxOutputTokens:     4096,
		MaxTokens:           2048,
		MaxCompletionTokens: 1024,
	})
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}
	var request map[string]any
	if err := json.Unmarshal(body, &request); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	if got := int(request["max_completion_tokens"].(float64)); got != 1024 {
		t.Fatalf("max_completion_tokens = %d", got)
	}
	if _, exists := request["max_tokens"]; exists {
		t.Fatal("request contains both max_tokens and max_completion_tokens")
	}
}

func TestOpenAIStandardEncodesOnlyWireMessageFields(t *testing.T) {
	detail := json.RawMessage(`{"type":"reasoning.encrypted","data":"opaque"}`)
	body, err := (&OpenAIStandard{}).EncodeRequest(&ChatRequest{
		Model: "model",
		Messages: []ChatMessage{
			{
				Role:         "assistant",
				Content:      TextContent(""),
				CacheControl: &CacheControl{Type: "ephemeral"},
				ToolCalls: []ToolCall{{
					Index: 7, ID: "call_1", Type: "function",
					Function: FunctionCall{Name: "read_file", Arguments: `{"path":"a.go"}`},
				}},
				ProviderItems: []ProviderItem{
					{Standard: "openai", Type: "reasoning.encrypted", Data: detail},
					{Standard: "anthropic", Type: "thinking", Data: json.RawMessage(`{"type":"thinking"}`)},
				},
			},
			{Role: "tool", ToolCallID: "call_1", Content: TextContent("failed"), ToolError: true},
		},
		Tools: []ToolDef{{
			Type: "function",
			Function: FunctionDef{
				Name: "read_file", Parameters: json.RawMessage(`{"type":"object"}`),
			},
			CacheControl: &CacheControl{Type: "ephemeral"},
		}},
	})
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}
	var request map[string]any
	if err := json.Unmarshal(body, &request); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	messages := request["messages"].([]any)
	assistant := messages[0].(map[string]any)
	if _, exists := assistant["provider_items"]; exists {
		t.Fatalf("internal provider_items leaked onto wire: %#v", assistant)
	}
	if _, exists := assistant["cache_control"]; exists {
		t.Fatalf("non-standard cache_control leaked onto message: %#v", assistant)
	}
	call := assistant["tool_calls"].([]any)[0].(map[string]any)
	if _, exists := call["index"]; exists {
		t.Fatalf("stream-only tool index leaked onto request: %#v", call)
	}
	details := assistant["reasoning_details"].([]any)
	if len(details) != 1 || details[0].(map[string]any)["data"] != "opaque" {
		t.Fatalf("reasoning_details = %#v", details)
	}
	toolResult := messages[1].(map[string]any)
	if _, exists := toolResult["tool_error"]; exists {
		t.Fatalf("internal tool_error leaked onto wire: %#v", toolResult)
	}
	toolDef := request["tools"].([]any)[0].(map[string]any)
	if _, exists := toolDef["cache_control"]; exists {
		t.Fatalf("non-standard cache_control leaked onto tool: %#v", toolDef)
	}
}

func TestOpenAIStandardPreservesReasoningDetails(t *testing.T) {
	response, err := (&OpenAIStandard{}).DecodeResponse([]byte(`{
		"choices":[{
			"message":{
				"role":"assistant",
				"content":null,
				"reasoning_details":[{"type":"reasoning.encrypted","data":"opaque"}]
			},
			"finish_reason":"tool_calls"
		}]
	}`))
	if err != nil {
		t.Fatalf("DecodeResponse: %v", err)
	}
	message := response.Choices[0].Message
	if len(message.ProviderItems) != 1 || message.ProviderItems[0].Standard != "openai" {
		t.Fatalf("provider items = %+v", message.ProviderItems)
	}
	if len(message.ReasoningDetails) != 0 {
		t.Fatalf("wire reasoning details were not normalized: %+v", message.ReasoningDetails)
	}
}

func TestOpenAIChatDecoderReportsUsageOnlyChunk(t *testing.T) {
	decoder := (&OpenAIStandard{}).NewStreamDecoder()
	update, err := decoder.Decode(StreamEvent{Data: []byte(`{
		"choices": [],
		"usage": {
			"prompt_tokens": 11,
			"completion_tokens": 7,
			"prompt_tokens_details": {"cached_tokens": 3, "cache_write_tokens": 2}
		}
	}`)})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if update.Usage == nil {
		t.Fatal("usage-only chunk was discarded")
	}
	if update.Usage.PromptTokens != 11 || update.Usage.CompletionTokens != 7 ||
		update.Usage.PromptTokensCached != 3 || update.Usage.PromptTokensWritten != 2 {
		t.Fatalf("usage = %+v", *update.Usage)
	}
}

func TestOpenAIChatDecoderToolDelta(t *testing.T) {
	decoder := (&OpenAIStandard{}).NewStreamDecoder()
	update, err := decoder.Decode(StreamEvent{Data: []byte(`{
		"choices": [{
			"delta": {"tool_calls": [{
				"index": 2,
				"id": "call_1",
				"type": "function",
				"function": {"name": "read_file", "arguments": "{\"path\":"}
			}]},
			"finish_reason": null
		}]
	}`)})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(update.ToolCalls) != 1 {
		t.Fatalf("tool deltas = %d", len(update.ToolCalls))
	}
	call := update.ToolCalls[0]
	if call.Index != 2 || call.ID != "call_1" || call.Name != "read_file" ||
		call.Arguments != `{"path":` {
		t.Fatalf("tool delta = %+v", call)
	}
}

func TestOpenAIChatDecoderSurfacesErrorEnvelope(t *testing.T) {
	decoder := (&OpenAIStandard{}).NewStreamDecoder()
	_, err := decoder.Decode(StreamEvent{Data: []byte(`{
		"error": {"type": "rate_limit_error", "code": "rate_limit", "message": "slow down"}
	}`)})
	if err == nil {
		t.Fatal("Decode succeeded for error envelope")
	}
}

func TestOpenAIStandardUsesCompletionLimitForReasoningModels(t *testing.T) {
	body, err := (&OpenAIStandard{}).EncodeRequest(&ChatRequest{
		Model: "openai/gpt-5.6", Messages: []ChatMessage{NewTextMessage("user", "hi")},
		MaxOutputTokens: 2048,
	})
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}
	var request map[string]any
	if err := json.Unmarshal(body, &request); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	if request["max_completion_tokens"] != float64(2048) {
		t.Fatalf("request = %#v", request)
	}
	if _, exists := request["max_tokens"]; exists {
		t.Fatalf("legacy max_tokens sent to reasoning model: %#v", request)
	}
}

func TestOpenAIChatPreservesRepeatedReasoningDetails(t *testing.T) {
	decoder := (&OpenAIStandard{}).NewStreamDecoder()
	event := StreamEvent{Data: []byte(`{
		"choices":[{"delta":{"reasoning_details":[{"type":"reasoning.text","text":"same"}]}}]
	}`)}
	for index := 0; index < 2; index++ {
		update, err := decoder.Decode(event)
		if err != nil {
			t.Fatalf("Decode %d: %v", index, err)
		}
		if len(update.ProviderItems) != 1 {
			t.Fatalf("update %d provider items = %+v", index, update.ProviderItems)
		}
	}
}

func TestOpenAIChatDecodesRefusals(t *testing.T) {
	response, err := (&OpenAIStandard{}).DecodeResponse([]byte(`{
		"choices":[{"message":{"role":"assistant","content":null,"refusal":"cannot comply"},"finish_reason":"stop"}]
	}`))
	if err != nil {
		t.Fatalf("DecodeResponse: %v", err)
	}
	if got := response.Choices[0].Message.ContentString(); got != "cannot comply" {
		t.Fatalf("content = %q", got)
	}
	update, err := (&OpenAIStandard{}).NewStreamDecoder().Decode(StreamEvent{Data: []byte(`{
		"choices":[{"delta":{"refusal":"cannot "}}]
	}`)})
	if err != nil {
		t.Fatalf("Decode stream: %v", err)
	}
	if update.ContentDelta != "cannot " {
		t.Fatalf("refusal delta = %q", update.ContentDelta)
	}
}

func TestOpenAIThinkingNoneIsExplicit(t *testing.T) {
	extra := (&OpenAIStandard{}).ExtraBody("gpt-5.6", "none")
	if extra["reasoning_effort"] != "none" {
		t.Fatalf("extra = %#v", extra)
	}
}

func TestOpenAIStandardRejectsMissingChoices(t *testing.T) {
	if _, err := (&OpenAIStandard{}).DecodeResponse([]byte(`{}`)); err == nil {
		t.Fatal("expected missing choices error")
	}
}
