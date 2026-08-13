package provider

import (
	"encoding/json"
	"testing"
)

func TestOpenAIResponsesRequestEncodesConversationItems(t *testing.T) {
	std := &OpenAIResponsesStandard{}
	reasoning := json.RawMessage(`{"id":"rs_1","type":"reasoning","summary":[]}`)
	body, err := std.EncodeRequest(&ChatRequest{
		Model: "gpt-test",
		Messages: []ChatMessage{
			{Role: "system", Content: TextContent("be precise")},
			{Role: "user", Content: PartsContent([]ContentPart{
				{Type: "text", Text: "inspect"},
				{Type: "image_url", ImageURL: &ImageURL{URL: "data:image/png;base64,YWJj", Detail: "high"}},
			})},
			{
				Role:    "assistant",
				Content: TextContent(""),
				ProviderItems: []ProviderItem{{
					Standard: "openai-responses", Type: "reasoning", Data: reasoning,
				}},
				ToolCalls: []ToolCall{{
					ID: "call_1", Type: "function",
					Function: FunctionCall{Name: "read_file", Arguments: `{"path":"a.go"}`},
				}},
			},
			{Role: "tool", ToolCallID: "call_1", Content: TextContent("contents")},
		},
		Tools: []ToolDef{{
			Type: "function",
			Function: FunctionDef{
				Name: "read_file", Description: "read",
				Parameters: json.RawMessage(`{"type":"object"}`),
			},
		}},
		Stream:          true,
		MaxOutputTokens: 1234,
	})
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}

	var request map[string]any
	if err := json.Unmarshal(body, &request); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	if request["max_output_tokens"].(float64) != 1234 {
		t.Fatalf("max_output_tokens = %v", request["max_output_tokens"])
	}
	input := request["input"].([]any)
	if len(input) != 5 {
		t.Fatalf("input items = %d, want 5: %#v", len(input), input)
	}
	if item := input[2].(map[string]any); item["type"] != "reasoning" {
		t.Fatalf("preserved item = %#v", item)
	}
	if item := input[3].(map[string]any); item["type"] != "function_call" ||
		item["call_id"] != "call_1" {
		t.Fatalf("function call item = %#v", item)
	}
	if item := input[4].(map[string]any); item["type"] != "function_call_output" ||
		item["call_id"] != "call_1" {
		t.Fatalf("function output item = %#v", item)
	}
	tools := request["tools"].([]any)
	tool := tools[0].(map[string]any)
	if tool["name"] != "read_file" {
		t.Fatalf("flat tool = %#v", tool)
	}
	if _, nested := tool["function"]; nested {
		t.Fatalf("Responses tool unexpectedly uses Chat Completions nesting: %#v", tool)
	}
}

func TestOpenAIResponsesReconstructedAssistantUsesInputContent(t *testing.T) {
	body, err := (&OpenAIResponsesStandard{}).EncodeRequest(&ChatRequest{
		Model: "gpt-test",
		Messages: []ChatMessage{
			{Role: "assistant", Content: TextContent("previous answer")},
		},
	})
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}
	var request map[string]any
	if err := json.Unmarshal(body, &request); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	item := request["input"].([]any)[0].(map[string]any)
	content := item["content"].([]any)[0].(map[string]any)
	if content["type"] != "input_text" {
		t.Fatalf("assistant content type = %v, want input_text", content["type"])
	}
}

func TestOpenAIResponsesStreamDecoderFunctionAndReasoning(t *testing.T) {
	decoder := (&OpenAIResponsesStandard{}).NewStreamDecoder()

	added, err := decoder.Decode(StreamEvent{Data: []byte(`{
		"type":"response.output_item.added",
		"output_index":1,
		"item":{"id":"fc_1","type":"function_call","call_id":"call_1","name":"read_file","arguments":""}
	}`)})
	if err != nil {
		t.Fatalf("added: %v", err)
	}
	if len(added.ToolCalls) != 1 || added.ToolCalls[0].ID != "call_1" ||
		added.ToolCalls[0].Name != "read_file" {
		t.Fatalf("added tool = %+v", added.ToolCalls)
	}

	delta, err := decoder.Decode(StreamEvent{Data: []byte(`{
		"type":"response.function_call_arguments.delta",
		"output_index":1,
		"delta":"{\"path\":\"a"
	}`)})
	if err != nil {
		t.Fatalf("delta: %v", err)
	}
	if len(delta.ToolCalls) != 1 || delta.ToolCalls[0].Arguments != `{"path":"a` {
		t.Fatalf("arguments delta = %+v", delta.ToolCalls)
	}

	done, err := decoder.Decode(StreamEvent{Data: []byte(`{
		"type":"response.function_call_arguments.done",
		"output_index":1,
		"name":"read_file",
		"arguments":"{\"path\":\"a.go\"}"
	}`)})
	if err != nil {
		t.Fatalf("arguments done: %v", err)
	}
	if !done.ToolCalls[0].ReplaceArguments || done.ToolCalls[0].Arguments != `{"path":"a.go"}` {
		t.Fatalf("arguments done = %+v", done.ToolCalls[0])
	}

	reasoning, err := decoder.Decode(StreamEvent{Data: []byte(`{
		"type":"response.output_item.done",
		"output_index":0,
		"item":{"id":"rs_1","type":"reasoning","summary":[]}
	}`)})
	if err != nil {
		t.Fatalf("reasoning done: %v", err)
	}
	if len(reasoning.ProviderItems) != 1 {
		t.Fatalf("provider items = %d", len(reasoning.ProviderItems))
	}

	completed, err := decoder.Decode(StreamEvent{Data: []byte(`{
		"type":"response.completed",
		"response":{
			"id":"resp_1",
			"status":"completed",
			"output":[
				{"id":"rs_1","type":"reasoning","summary":[]},
				{"id":"fc_1","type":"function_call","call_id":"call_1","name":"read_file","arguments":"{\"path\":\"a.go\"}"}
			],
			"usage":{"input_tokens":20,"output_tokens":8,"input_tokens_details":{"cached_tokens":5,"cache_write_tokens":2}}
		}
	}`)})
	if err != nil {
		t.Fatalf("completed: %v", err)
	}
	if !completed.Done || completed.FinishReason != "tool_calls" {
		t.Fatalf("terminal update = %+v", completed)
	}
	if len(completed.ProviderItems) != 1 || completed.ProviderItems[0].Type != "function_call" {
		t.Fatalf("terminal provider items: %+v", completed.ProviderItems)
	}
	if completed.Usage == nil || completed.Usage.PromptTokens != 20 ||
		completed.Usage.CompletionTokens != 8 || completed.Usage.PromptTokensCached != 5 ||
		completed.Usage.PromptTokensWritten != 2 {
		t.Fatalf("usage = %+v", completed.Usage)
	}
}

func TestOpenAIResponsesStreamZeroArgumentToolFinalizesObject(t *testing.T) {
	update, err := (&OpenAIResponsesStandard{}).NewStreamDecoder().Decode(StreamEvent{Data: []byte(`{
		"type":"response.function_call_arguments.done",
		"output_index":0,
		"name":"list_files",
		"arguments":""
	}`)})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(update.ToolCalls) != 1 || update.ToolCalls[0].Arguments != "{}" ||
		!update.ToolCalls[0].ReplaceArguments {
		t.Fatalf("tool update = %+v", update.ToolCalls)
	}
}

func TestOpenAIResponsesDecodeNonStreaming(t *testing.T) {
	response, err := (&OpenAIResponsesStandard{}).DecodeResponse([]byte(`{
		"id":"resp_1",
		"model":"gpt-test",
		"status":"completed",
		"output":[
			{"id":"rs_1","type":"reasoning","summary":[]},
			{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}
		],
		"usage":{"input_tokens":4,"output_tokens":2}
	}`))
	if err != nil {
		t.Fatalf("DecodeResponse: %v", err)
	}
	if len(response.Choices) != 1 || response.Choices[0].Message.ContentString() != "done" {
		t.Fatalf("response = %+v", response)
	}
	if len(response.Choices[0].Message.ProviderItems) != 2 {
		t.Fatalf("output items = %+v", response.Choices[0].Message.ProviderItems)
	}
}

func TestOpenAIResponsesTerminalEventFillsMissingDeltas(t *testing.T) {
	decoder := (&OpenAIResponsesStandard{}).NewStreamDecoder()
	if _, err := decoder.Decode(StreamEvent{Data: []byte(
		`{"type":"response.output_text.delta","delta":"hel"}`,
	)}); err != nil {
		t.Fatalf("text delta: %v", err)
	}
	if _, err := decoder.Decode(StreamEvent{Data: []byte(
		`{"type":"response.reasoning_summary_text.delta","delta":"rea"}`,
	)}); err != nil {
		t.Fatalf("reasoning delta: %v", err)
	}
	update, err := decoder.Decode(StreamEvent{Data: []byte(`{
		"type":"response.completed",
		"response":{
			"status":"completed",
			"output":[
				{"id":"rs_1","type":"reasoning","summary":[{"type":"summary_text","text":"reason"}]},
				{"id":"msg_1","type":"message","content":[{"type":"output_text","text":"hello"}]}
			]
		}
	}`)})
	if err != nil {
		t.Fatalf("completed: %v", err)
	}
	if update.ContentDelta != "lo" || update.ReasoningDelta != "son" {
		t.Fatalf("terminal deltas = content %q reasoning %q", update.ContentDelta, update.ReasoningDelta)
	}
}

func TestOpenAIResponsesDecodesRefusalText(t *testing.T) {
	response, err := (&OpenAIResponsesStandard{}).DecodeResponse([]byte(`{
		"status":"completed",
		"output":[{
			"type":"message",
			"content":[{"type":"refusal","refusal":"cannot comply"}]
		}]
	}`))
	if err != nil {
		t.Fatalf("DecodeResponse: %v", err)
	}
	if got := response.Choices[0].Message.ContentString(); got != "cannot comply" {
		t.Fatalf("content = %q", got)
	}
}

func TestOpenAIResponsesPreservesOutputItemsAndPhase(t *testing.T) {
	decoded, err := (&OpenAIResponsesStandard{}).DecodeResponse([]byte(`{
		"status":"completed",
		"output":[
			{"id":"rs_1","type":"reasoning","summary":[]},
			{"id":"msg_1","type":"message","role":"assistant","status":"completed","phase":"final_answer","content":[{"type":"output_text","text":"done"}]},
			{"id":"fc_1","type":"function_call","status":"completed","call_id":"call_1","name":"read_file","arguments":"{\"path\":\"a.go\"}"}
		]
	}`))
	if err != nil {
		t.Fatalf("DecodeResponse: %v", err)
	}
	message := decoded.Choices[0].Message
	if len(message.ProviderItems) != 3 || len(message.ToolCalls) != 1 {
		t.Fatalf("decoded message = %+v", message)
	}
	body, err := (&OpenAIResponsesStandard{}).EncodeRequest(&ChatRequest{
		Model: "gpt-5.6",
		Messages: []ChatMessage{
			message,
			{Role: "tool", ToolCallID: "call_1", Content: TextContent("contents")},
		},
	})
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}
	var request map[string]any
	if err := json.Unmarshal(body, &request); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	input := request["input"].([]any)
	if len(input) != 4 {
		t.Fatalf("input = %#v", input)
	}
	if got := input[1].(map[string]any)["phase"]; got != "final_answer" {
		t.Fatalf("message phase = %#v", got)
	}
	wantTypes := []string{"reasoning", "message", "function_call", "function_call_output"}
	for index, want := range wantTypes {
		if got := input[index].(map[string]any)["type"]; got != want {
			t.Fatalf("input[%d].type = %v, want %s", index, got, want)
		}
	}
}

func TestOpenAIResponsesIncompleteToolCallStaysTruncated(t *testing.T) {
	response, err := (&OpenAIResponsesStandard{}).DecodeResponse([]byte(`{
		"status":"incomplete",
		"incomplete_details":{"reason":"max_output_tokens"},
		"output":[{
			"id":"fc_1","type":"function_call","status":"in_progress",
			"call_id":"call_1","name":"read_file","arguments":"{\"path\":"
		}]
	}`))
	if err != nil {
		t.Fatalf("DecodeResponse: %v", err)
	}
	choice := response.Choices[0]
	if choice.FinishReason != "max_output_tokens" || len(choice.Message.ToolCalls) != 0 {
		t.Fatalf("choice = %+v", choice)
	}
}

func TestOpenAIResponsesIncompleteStreamDoesNotBecomeToolCalls(t *testing.T) {
	update, err := (&OpenAIResponsesStandard{}).NewStreamDecoder().Decode(StreamEvent{Data: []byte(`{
		"type":"response.incomplete",
		"response":{
			"status":"incomplete",
			"incomplete_details":{"reason":"max_output_tokens"},
			"output":[{
				"id":"fc_1","type":"function_call","status":"completed",
				"call_id":"call_1","name":"read_file","arguments":"{\"path\":\"a.go\"}"
			}]
		}
	}`)})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if update.FinishReason != "max_output_tokens" || !update.Done {
		t.Fatalf("update = %+v", update)
	}
	if len(update.ToolCalls) != 1 {
		t.Fatalf("tool calls = %+v", update.ToolCalls)
	}
}

func TestOpenAIResponsesDecodesReasoningContent(t *testing.T) {
	response, err := (&OpenAIResponsesStandard{}).DecodeResponse([]byte(`{
		"status":"completed",
		"output":[{
			"id":"rs_1","type":"reasoning","summary":[],
			"content":[{"type":"reasoning_text","text":"private chain"}]
		}]
	}`))
	if err != nil {
		t.Fatalf("DecodeResponse: %v", err)
	}
	if got := response.Choices[0].Message.ReasoningContent; got != "private chain" {
		t.Fatalf("reasoning = %q", got)
	}
}

func TestOpenAIResponsesThinkingNoneIsExplicit(t *testing.T) {
	extra := (&OpenAIResponsesStandard{}).ExtraBody("gpt-5.6", "none")
	reasoning := extra["reasoning"].(map[string]any)
	if reasoning["effort"] != "none" {
		t.Fatalf("reasoning = %#v", reasoning)
	}
	if _, exists := reasoning["summary"]; exists {
		t.Fatalf("none reasoning requested a summary: %#v", reasoning)
	}
}

func TestOpenAIResponsesRejectsMissingStatus(t *testing.T) {
	if _, err := (&OpenAIResponsesStandard{}).DecodeResponse([]byte(`{}`)); err == nil {
		t.Fatal("expected missing status error")
	}
}

func TestOpenAIResponsesRejectsUnsupportedImage(t *testing.T) {
	_, err := (&OpenAIResponsesStandard{}).EncodeRequest(&ChatRequest{
		Model: "gpt-5.6",
		Messages: []ChatMessage{{
			Role: "user",
			Content: PartsContent([]ContentPart{{
				Type: "image_url", ImageURL: &ImageURL{URL: "data:image/svg+xml;base64,PHN2Zy8+"},
			}}),
		}},
	})
	if err == nil {
		t.Fatal("expected unsupported image error")
	}
}
