package provider

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"crude/types/reasoning"
)

// Events follow an Interactions stream captured from
// generativelanguage.googleapis.com (gemini-3.1-flash-lite, 2026-11-20).
func TestGeminiInteractionsStreamAssemblesSteps(t *testing.T) {
	decoder := (&GeminiInteractionsStandard{}).NewStreamDecoder()
	events := []StreamEvent{
		{Event: "interaction.created", Data: []byte(`{"interaction":{"id":"v1_x","object":"interaction","model":"gemini-3.1-flash-lite"},"event_type":"interaction.created"}`)},
		{Event: "interaction.status_update", Data: []byte(`{"interaction_id":"v1_x","status":"in_progress","event_type":"interaction.status_update"}`)},
		{Event: "step.start", Data: []byte(`{"index":0,"step":{"type":"thought"},"event_type":"step.start"}`)},
		{Event: "step.delta", Data: []byte(`{"index":0,"delta":{"content":{"type":"text","text":"Planning the calls."},"type":"thought_summary"},"event_type":"step.delta"}`)},
		{Event: "step.delta", Data: []byte(`{"index":0,"delta":{"signature":"c2lnLXRob3VnaHQ=","type":"thought_signature"},"event_type":"step.delta"}`)},
		{Event: "step.stop", Data: []byte(`{"index":0,"event_type":"step.stop"}`)},
		{Event: "step.start", Data: []byte(`{"index":1,"step":{"id":"call_a","type":"function_call","name":"read_file","arguments":{}},"event_type":"step.start"}`)},
		{Event: "step.delta", Data: []byte(`{"index":1,"delta":{"arguments":"{\"path\":","type":"arguments_delta"},"event_type":"step.delta"}`)},
		{Event: "step.delta", Data: []byte(`{"index":1,"delta":{"arguments":"\"/etc/hostname\"}","type":"arguments_delta"},"event_type":"step.delta"}`)},
		{Event: "step.stop", Data: []byte(`{"index":1,"event_type":"step.stop"}`)},
		{Event: "step.start", Data: []byte(`{"index":2,"step":{"id":"call_b","type":"function_call","name":"get_weather","arguments":{}},"event_type":"step.start"}`)},
		{Event: "step.delta", Data: []byte(`{"index":2,"delta":{"arguments":"{\"city\":\"Paris\"}","type":"arguments_delta"},"event_type":"step.delta"}`)},
		{Event: "step.stop", Data: []byte(`{"index":2,"event_type":"step.stop"}`)},
		{Event: "interaction.completed", Data: []byte(`{"interaction":{"id":"v1_x","status":"requires_action","usage":{"total_tokens":263,"total_input_tokens":114,"total_cached_tokens":10,"total_output_tokens":35,"total_tool_use_tokens":0,"total_thought_tokens":114},"service_tier":"standard","object":"interaction"},"event_type":"interaction.completed"}`)},
	}
	var reasoningText strings.Builder
	arguments := map[int]string{}
	ids := map[int]string{}
	var items []ProviderItem
	var last StreamUpdate
	for _, event := range events {
		update, err := decoder.Decode(event)
		if err != nil {
			t.Fatalf("Decode(%s): %v", event.Data, err)
		}
		for _, delta := range update.Reasoning {
			if delta.Kind != reasoning.Summary {
				t.Fatalf("reasoning kind = %q", delta.Kind)
			}
			reasoningText.WriteString(delta.Text)
		}
		for _, call := range update.ToolCalls {
			if call.ID != "" {
				ids[call.Index] = call.ID
			}
			if call.ReplaceArguments {
				arguments[call.Index] = call.Arguments
			} else {
				arguments[call.Index] += call.Arguments
			}
		}
		items = append(items, update.ProviderItems...)
		last = update
	}
	if reasoningText.String() != "Planning the calls." {
		t.Fatalf("reasoning = %q", reasoningText.String())
	}
	if !reflect.DeepEqual(arguments, map[int]string{0: `{"path":"/etc/hostname"}`, 1: `{"city":"Paris"}`}) ||
		!reflect.DeepEqual(ids, map[int]string{0: "call_a", 1: "call_b"}) {
		t.Fatalf("calls: ids=%v arguments=%v", ids, arguments)
	}
	if !last.Done || last.FinishReason != "tool_calls" {
		t.Fatalf("terminal = %+v", last)
	}
	want := TokenUsage{PromptTokens: 114, CompletionTokens: 149, PromptTokensCached: 10, CacheReadKnown: true,
		ReasoningTokens: 114, ReasoningKnown: true, ReasoningInOutput: true, ServiceTier: "standard"}
	if last.Usage == nil || *last.Usage != want {
		t.Fatalf("usage = %+v", last.Usage)
	}
	var steps []string
	for _, item := range items {
		steps = append(steps, string(item.Data))
	}
	wantSteps := []string{
		`{"signature":"c2lnLXRob3VnaHQ=","summary":[{"text":"Planning the calls.","type":"text"}],"type":"thought"}`,
		`{"arguments":{"path":"/etc/hostname"},"id":"call_a","name":"read_file","type":"function_call"}`,
		`{"arguments":{"city":"Paris"},"id":"call_b","name":"get_weather","type":"function_call"}`,
	}
	if !reflect.DeepEqual(steps, wantSteps) {
		t.Fatalf("stored steps = %v", steps)
	}
}

func TestGeminiInteractionsEncodeReplaysStepsStatelessly(t *testing.T) {
	history := geminiTestHistory(false)
	history[2].ProviderItems = []ProviderItem{
		{Standard: "gemini-interactions", Type: "thought", Data: json.RawMessage(`{"signature":"c2ln","type":"thought"}`)},
		{Standard: "gemini-interactions", Type: "model_output", Data: json.RawMessage(`{"content":[{"text":"Checking.","type":"text"}],"type":"model_output"}`)},
		{Standard: "gemini-interactions", Type: "function_call", Data: json.RawMessage(`{"arguments":{"path":"a.go"},"id":"call_1","name":"read_file","type":"function_call"}`)},
		{Standard: "gemini-interactions", Type: "function_call", Data: json.RawMessage(`{"arguments":{"tool_uses":[]},"id":"calltool-local","name":"multi_tool_use_parallel","type":"function_call"}`)},
	}
	body, err := (&GeminiInteractionsStandard{}).EncodeRequest(&ChatRequest{Model: "models/gemini-3.5-flash", Messages: history, Stream: true, MaxOutputTokens: 512, ToolChoice: "none",
		Tools: []ToolDef{{Type: "function", Function: FunctionDef{Name: "read_file", Description: "read", Parameters: json.RawMessage(`{"type":"object","properties":{"mode":{"const":"text"}}}`)}}}})
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(body, &wire); err != nil {
		t.Fatal(err)
	}
	if wire["model"] != "gemini-3.5-flash" || wire["store"] != false || wire["stream"] != true || wire["system_instruction"] != "be precise" {
		t.Fatalf("request = %#v", wire)
	}
	steps := wire["input"].([]any)
	var types []string
	for _, step := range steps {
		types = append(types, step.(map[string]any)["type"].(string))
	}
	if !reflect.DeepEqual(types, []string{"user_input", "thought", "model_output", "function_call", "function_call", "function_result", "function_result", "user_input"}) {
		t.Fatalf("step types = %v", types)
	}
	image := steps[0].(map[string]any)["content"].([]any)[1]
	if !reflect.DeepEqual(image, map[string]any{"type": "image", "mime_type": "image/png", "data": "YWJj"}) {
		t.Fatalf("image content = %#v", image)
	}
	failed := steps[5].(map[string]any)
	if !reflect.DeepEqual(failed, map[string]any{"type": "function_result", "call_id": "call_1", "name": "read_file", "result": "missing", "is_error": true}) {
		t.Fatalf("failed result = %#v", failed)
	}
	media := steps[6].(map[string]any)["result"].([]any)
	if len(media) != 2 || !reflect.DeepEqual(media[1], map[string]any{"type": "image", "mime_type": "image/png", "data": "cG5n"}) {
		t.Fatalf("media result = %#v", media)
	}
	generation := wire["generation_config"].(map[string]any)
	if generation["tool_choice"] != "none" || generation["max_output_tokens"] != float64(512) {
		t.Fatalf("generation_config = %#v", generation)
	}
	tool := wire["tools"].([]any)[0].(map[string]any)
	if !reflect.DeepEqual(tool["parameters"], map[string]any{"type": "object", "properties": map[string]any{"mode": map[string]any{"enum": []any{"text"}}}}) {
		t.Fatalf("tool = %#v", tool)
	}
}

func TestGeminiInteractionsRebuildsUnreplayableTurns(t *testing.T) {
	body, err := (&GeminiInteractionsStandard{}).EncodeRequest(&ChatRequest{Model: "gemini-3.5-flash", Messages: geminiTestHistory(false)})
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Input []map[string]any `json:"input"`
	}
	if err := json.Unmarshal(body, &wire); err != nil {
		t.Fatal(err)
	}
	if wire.Input[1]["type"] != "model_output" || wire.Input[2]["signature"] != geminiSkipSignature || wire.Input[3]["signature"] != nil {
		t.Fatalf("rebuilt steps = %v", wire.Input)
	}
}

func TestGeminiInteractionsExtrasAndLearning(t *testing.T) {
	std := &GeminiInteractionsStandard{}
	extras := std.RequestExtras("gemini-3.5-flash", "max", ProtocolSettings{ServiceTier: "flex"})
	want := map[string]any{"generation_config": map[string]any{"thinking_level": "xhigh", "thinking_summaries": "auto"}, "service_tier": "flex"}
	if !reflect.DeepEqual(extras, want) {
		t.Fatalf("extras = %#v", extras)
	}
	scope := "https://interactions.test/v1beta"
	req := &ChatRequest{Model: "gemini-3.5-flash"}
	rejected := &StatusError{StatusCode: 400, Message: "invalid_request: Thinking level XHIGH is not supported for this model. Please retry with other thinking level."}
	if !std.LearnRejection(scope, req, []byte(`{"generation_config":{"thinking_level":"xhigh"}}`), rejected) {
		t.Fatal("rejection was not learned")
	}
	body := map[string]any{"generation_config": map[string]any{"thinking_level": "xhigh", "thinking_summaries": "auto"}}
	std.FinalizeRequest(scope, req, body)
	if got := body["generation_config"].(map[string]any)["thinking_level"]; got != "high" {
		t.Fatalf("finalized level = %v", got)
	}
}

func TestGeminiInteractionsDecodeResponse(t *testing.T) {
	response, err := (&GeminiInteractionsStandard{}).DecodeResponse([]byte(`{"id":"v1_y","status":"completed","usage":{"total_tokens":9,"total_input_tokens":8,"total_cached_tokens":0,"total_output_tokens":1,"total_thought_tokens":0},"service_tier":"standard","steps":[{"signature":"c2ln","type":"thought"},{"content":[{"text":"ok","type":"text"}],"type":"model_output"}],"object":"interaction","model":"gemini-3.1-flash-lite"}`))
	if err != nil {
		t.Fatal(err)
	}
	choice := response.Choices[0]
	if choice.FinishReason != "stop" || choice.Message.ContentString() != "ok" || len(choice.Message.ProviderItems) != 2 || response.Usage.PromptTokens != 8 {
		t.Fatalf("response = %+v", response)
	}
}

func TestGeminiInteractionsPrivacyRevisionAndReplayShape(t *testing.T) {
	p := FromPreset("google", "k", "")
	if err := p.SetProtocolID("gemini-interactions"); err != nil {
		t.Fatal(err)
	}
	p.Model = "gemini-3.5-flash"
	off := false
	p.SetProtocolSettings(&ProtocolSettings{ReasoningSummaries: &off})
	p.SetExtraBody(map[string]any{"store": true, "previous_interaction_id": "v1_old"})
	headers, err := p.RequestHeaders(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if headers.Get("Api-Revision") != geminiInteractionsRevision || headers.Get("x-goog-api-key") != "k" {
		t.Fatalf("headers = %v", headers)
	}
	client, err := p.Client()
	if err != nil {
		t.Fatal(err)
	}
	history := []ChatMessage{NewTextMessage("user", "hi"), {Role: "assistant", Content: TextContent("hello"), ProviderItems: []ProviderItem{
		{Standard: "gemini-interactions", Type: "thought", Data: json.RawMessage(`{"type":"thought","summary":[{"type":"text","text":"unsigned"}]}`)},
		{Standard: "gemini-interactions", Type: "model_output", Data: json.RawMessage(`{"type":"model_output","id":"step_9","content":[{"type":"text","text":"hello","id":"c_1"}]}`)},
	}}, NewTextMessage("user", "again")}
	body, err := client.encodeChatRequest(&ChatRequest{Model: p.Model, Messages: history, Stream: true})
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(body, &wire); err != nil {
		t.Fatal(err)
	}
	if wire["store"] != false || wire["previous_interaction_id"] != nil {
		t.Fatalf("Google must not keep this interaction: %v", wire)
	}
	if got := wire["generation_config"].(map[string]any)["thinking_summaries"]; got != "none" {
		t.Fatalf("thinking_summaries = %v", got)
	}
	steps := wire["input"].([]any)
	want := map[string]any{"type": "model_output", "content": []any{map[string]any{"type": "text", "text": "hello"}}}
	if len(steps) != 3 || !reflect.DeepEqual(steps[1], want) {
		t.Fatalf("replayed steps = %#v", steps)
	}
}
