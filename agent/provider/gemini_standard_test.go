package provider

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"crude/internal/providersim"
	"crude/types/reasoning"
)

// Chunks below follow streams captured from generativelanguage.googleapis.com
// (gemini-3.5-flash, 2026-11-20); signatures are shortened.

func decodeGeminiEvents(t *testing.T, decoder StreamDecoder, events ...string) []StreamUpdate {
	t.Helper()
	var updates []StreamUpdate
	for _, data := range events {
		update, err := decoder.Decode(StreamEvent{Data: []byte(data)})
		if err != nil {
			t.Fatalf("Decode(%s): %v", data, err)
		}
		updates = append(updates, update)
	}
	return updates
}

func TestGeminiStreamKeepsSignedPartsAndMergesUnsignedText(t *testing.T) {
	decoder := (&GeminiStandard{}).NewStreamDecoder()
	updates := decodeGeminiEvents(t, decoder,
		`{"candidates":[{"content":{"parts":[{"text":"**Assessing**\n\nChecking factors.","thought":true}],"role":"model"},"index":0}],"usageMetadata":{"promptTokenCount":11,"totalTokenCount":210,"thoughtsTokenCount":199,"serviceTier":"standard"}}`,
		`{"candidates":[{"content":{"parts":[{"text":"No. "}],"role":"model"},"index":0}],"usageMetadata":{"promptTokenCount":11,"candidatesTokenCount":3,"thoughtsTokenCount":424}}`,
		`{"candidates":[{"content":{"parts":[{"text":"391 = 17 × 23."}],"role":"model"},"index":0}],"usageMetadata":{"promptTokenCount":11,"candidatesTokenCount":27,"thoughtsTokenCount":424}}`,
		`{"candidates":[{"content":{"parts":[{"text":"","thoughtSignature":"c2lnLWZpbmFs"}],"role":"model"},"finishReason":"STOP","index":0}],"usageMetadata":{"promptTokenCount":11,"candidatesTokenCount":35,"cachedContentTokenCount":4,"thoughtsTokenCount":424,"toolUsePromptTokenCount":2,"serviceTier":"standard"},"modelVersion":"gemini-3.6-flash","responseId":"r1"}`,
	)
	var content strings.Builder
	for _, update := range updates {
		content.WriteString(update.ContentDelta)
	}
	if content.String() != "No. 391 = 17 × 23." {
		t.Fatalf("content = %q", content.String())
	}
	if got := updates[0].Reasoning; len(got) != 1 || got[0].Kind != reasoning.Summary || !strings.HasPrefix(got[0].Text, "**Assessing**") {
		t.Fatalf("reasoning = %+v", got)
	}
	last := updates[len(updates)-1]
	if last.FinishReason != "stop" || !decoder.(eofTerminalDecoder).CompleteAtEOF() {
		t.Fatalf("terminal update = %+v", last)
	}
	want := TokenUsage{PromptTokens: 13, CompletionTokens: 459, PromptTokensCached: 4, CacheReadKnown: true,
		ReasoningTokens: 424, ReasoningKnown: true, ReasoningInOutput: true, ServiceTier: "standard"}
	if last.Usage == nil || *last.Usage != want {
		t.Fatalf("usage = %+v, want %+v", last.Usage, want)
	}
	if len(last.ProviderItems) != 1 || last.ProviderItems[0].Standard != "gemini" {
		t.Fatalf("provider items = %+v", last.ProviderItems)
	}
	var stored struct {
		Parts []map[string]any `json:"parts"`
	}
	if err := json.Unmarshal(last.ProviderItems[0].Data, &stored); err != nil {
		t.Fatal(err)
	}
	wantParts := []map[string]any{
		{"text": "**Assessing**\n\nChecking factors.", "thought": true},
		{"text": "No. 391 = 17 × 23."},
		{"text": "", "thoughtSignature": "c2lnLWZpbmFs"},
	}
	if !reflect.DeepEqual(stored.Parts, wantParts) {
		t.Fatalf("stored parts = %#v\nwant %#v", stored.Parts, wantParts)
	}
}

func TestGeminiStreamParallelCallsKeepWirePartsAndIDs(t *testing.T) {
	first := `{"functionCall":{"name":"read_file","args":{"path":"/etc/hostname"},"id":"call_1"},"thoughtSignature":"c2lnLWNhbGw="}`
	second := `{"functionCall":{"name":"get_weather","args":{"city":"Paris"},"id":"call_2"}}`
	decoder := (&GeminiStandard{}).NewStreamDecoder()
	updates := decodeGeminiEvents(t, decoder,
		`{"candidates":[{"content":{"parts":[`+first+`],"role":"model"},"index":0}]}`,
		`{"candidates":[{"content":{"parts":[`+second+`],"role":"model"},"index":0}]}`,
		`{"candidates":[{"content":{"parts":[{"text":""}],"role":"model"},"finishReason":"STOP","index":0}],"usageMetadata":{"promptTokenCount":140,"candidatesTokenCount":51,"thoughtsTokenCount":145}}`,
	)
	calls := append(updates[0].ToolCalls, updates[1].ToolCalls...)
	if len(calls) != 2 || calls[0].Index != 0 || calls[1].Index != 1 || calls[0].ID != "call_1" || calls[1].Name != "get_weather" ||
		calls[0].Arguments != `{"path":"/etc/hostname"}` || !calls[1].ReplaceArguments {
		t.Fatalf("calls = %+v", calls)
	}
	last := updates[2]
	if last.FinishReason != "tool_calls" || !decoder.(eofTerminalDecoder).CompleteAtEOF() {
		t.Fatalf("terminal = %+v", last)
	}
	var stored struct {
		Parts []json.RawMessage `json:"parts"`
	}
	if err := json.Unmarshal(last.ProviderItems[0].Data, &stored); err != nil {
		t.Fatal(err)
	}
	// The unsigned empty text part closes as an empty part; the calls stay
	// byte-identical, with the signature on the first call only.
	if len(stored.Parts) != 3 || string(stored.Parts[0]) != first || string(stored.Parts[1]) != second || string(stored.Parts[2]) != `{"text":""}` {
		t.Fatalf("stored parts = %s", stored.Parts)
	}
}

func TestGeminiStreamFailuresAndFilters(t *testing.T) {
	_, err := (&GeminiStandard{}).NewStreamDecoder().Decode(StreamEvent{Data: []byte(`{"promptFeedback":{"blockReason":"SAFETY"},"usageMetadata":{"promptTokenCount":9}}`)})
	var api *APIError
	if !errors.As(err, &api) || api.Type != "content_filter" || !Permanent(err) {
		t.Fatalf("blocked prompt err = %#v", err)
	}
	_, err = (&GeminiStandard{}).NewStreamDecoder().Decode(StreamEvent{Data: []byte(`{"candidates":[{"content":{"parts":[]},"finishReason":"MALFORMED_FUNCTION_CALL","finishMessage":"Malformed function call: read_file{"}]}`)})
	if !errors.As(err, &api) || !Retryable(err) || !strings.Contains(err.Error(), "Malformed function call") {
		t.Fatalf("malformed call err = %#v retryable=%v", err, Retryable(err))
	}
	update, err := (&GeminiStandard{}).NewStreamDecoder().Decode(StreamEvent{Data: []byte(`{"candidates":[{"content":{"parts":[{"text":"partial"}]},"finishReason":"MAX_TOKENS"}]}`)})
	if err != nil || update.FinishReason != "length" {
		t.Fatalf("max tokens = %+v, %v", update, err)
	}
	update, err = (&GeminiStandard{}).NewStreamDecoder().Decode(StreamEvent{Data: []byte(`{"candidates":[{"content":{"parts":[{"text":"x"}]},"finishReason":"FINISH_REASON_UNSPECIFIED"}]}`)})
	if err != nil || update.FinishReason != "" || update.ContentDelta != "x" {
		t.Fatalf("unspecified finish = %+v, %v", update, err)
	}
	update, err = (&GeminiStandard{}).NewStreamDecoder().Decode(StreamEvent{Data: []byte(`{"candidates":[{"content":{"parts":[{"text":"x"}]},"finishReason":"RECITATION"}]}`)})
	if err != nil || update.FinishReason != "content_filter" {
		t.Fatalf("recitation = %+v, %v", update, err)
	}
	_, err = (&GeminiStandard{}).NewStreamDecoder().Decode(StreamEvent{Data: []byte(`{"error":{"code":503,"message":"This model is currently experiencing high demand.","status":"UNAVAILABLE"}}`)})
	if err == nil || !Retryable(err) {
		t.Fatalf("in-stream overload err = %v retryable=%v", err, Retryable(err))
	}
}

// geminiSSEServer serves one fixed SSE body for every request.
func geminiSSEServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(server.Close)
	return server
}

func TestGeminiStreamReadsLateUsageAndEndsAtEOF(t *testing.T) {
	server := geminiSSEServer(t, "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"hi\"}]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":5}}\n\n"+
		"data: {\"candidates\":[{\"content\":{\"parts\":[]}}],\"usageMetadata\":{\"promptTokenCount\":5,\"candidatesTokenCount\":1,\"thoughtsTokenCount\":7}}\n\n")
	p := FromPreset("google", "k", server.URL+"/v1beta")
	p.Model = "gemini-3.5-flash"
	client, err := p.Client()
	if err != nil {
		t.Fatal(err)
	}
	stream, err := client.Stream(context.Background(), &ChatRequest{Model: p.Model, Messages: []ChatMessage{NewTextMessage("user", "hi")}})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	var usage *TokenUsage
	var finish string
	for {
		update, err := stream.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		if update.Usage != nil {
			usage = update.Usage
		}
		if update.FinishReason != "" {
			finish = update.FinishReason
		}
	}
	if finish != "stop" || usage == nil || usage.CompletionTokens != 8 {
		t.Fatalf("finish=%q usage=%+v, want the late usage frame", finish, usage)
	}
}

func TestGeminiStreamWithoutFinishIsInterrupted(t *testing.T) {
	server := geminiSSEServer(t, "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"hi\"}]}}]}\n\n")
	p := FromPreset("google", "k", server.URL+"/v1beta")
	p.Model = "gemini-3.5-flash"
	p.SetDialSettings(DialSettings{MaxAttempts: 1})
	client, err := p.Client()
	if err != nil {
		t.Fatal(err)
	}
	stream, err := client.Stream(context.Background(), &ChatRequest{Model: p.Model, Messages: []ChatMessage{NewTextMessage("user", "hi")}})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if _, err := stream.Next(); err != nil {
		t.Fatalf("first update: %v", err)
	}
	_, err = stream.Next()
	var interrupted *StreamInterruptionError
	if !errors.As(err, &interrupted) || interrupted.Kind != StreamInterruptionMissingTerminal {
		t.Fatalf("err = %v, want a missing-terminal interruption", err)
	}
}

func geminiTestHistory(withItems bool) []ChatMessage {
	assistant := ChatMessage{
		Role: "assistant", Content: TextContent("Checking."),
		ToolCalls: []ToolCall{
			{ID: "call_1", Type: "function", Function: FunctionCall{Name: "read_file", Arguments: `{"path":"a.go"}`}},
			{ID: "calltool-local", Type: "function", Function: FunctionCall{Name: "multi_tool_use.parallel", Arguments: `{"tool_uses":[]}`}},
		},
	}
	if withItems {
		assistant.ProviderItems = []Item{{Standard: "gemini", Type: geminiContentItem, Data: json.RawMessage(
			`{"parts":[{"text":"Checking."},{"functionCall":{"id":"call_1","name":"read_file","args":{"path":"a.go"}},"thoughtSignature":"c2ln"},{"functionCall":{"name":"multi_tool_use_parallel","args":{"tool_uses":[]}}}]}`)}}
	}
	return []ChatMessage{
		NewTextMessage("system", "be precise"),
		{Role: "user", Content: PartsContent([]ContentPart{{Type: "text", Text: "inspect"}, {Type: "image_url", ImageURL: &ImageURL{URL: "data:image/png;base64,YWJj"}}})},
		assistant,
		{Role: "tool", ToolCallID: "call_1", Content: TextContent("missing"), ToolError: true},
		{Role: "tool", ToolCallID: "calltool-local", Content: PartsContent([]ContentPart{{Type: "text", Text: "shot"}, {Type: "image_url", MimeType: "image/png", Data: []byte("png")}})},
		NewTextMessage("user", "continue"),
	}
}

func encodeGeminiForTest(t *testing.T, req *ChatRequest) map[string]any {
	t.Helper()
	body, err := (&GeminiStandard{}).EncodeRequest(req)
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}
	var wire map[string]any
	if err := json.Unmarshal(body, &wire); err != nil {
		t.Fatal(err)
	}
	return wire
}

func TestGeminiEncodeReplaysExactPartsAndGroupsToolResults(t *testing.T) {
	wire := encodeGeminiForTest(t, &ChatRequest{Model: "gemini-3.5-flash", Messages: geminiTestHistory(true), MaxOutputTokens: 4096, ToolChoice: "required",
		Tools: []ToolDef{{Type: "function", Function: FunctionDef{Name: "read_file", Description: "read", Parameters: json.RawMessage(`{"type":"object"}`)}}}})
	if got := wire["systemInstruction"]; !reflect.DeepEqual(got, map[string]any{"parts": []any{map[string]any{"text": "be precise"}}}) {
		t.Fatalf("systemInstruction = %#v", got)
	}
	contents := wire["contents"].([]any)
	if len(contents) != 4 {
		t.Fatalf("contents = %#v", contents)
	}
	user := contents[0].(map[string]any)
	if user["role"] != "user" || !reflect.DeepEqual(user["parts"].([]any)[1], map[string]any{"inlineData": map[string]any{"mimeType": "image/png", "data": "YWJj"}}) {
		t.Fatalf("user content = %#v", user)
	}
	model := contents[1].(map[string]any)
	parts := model["parts"].([]any)
	if model["role"] != "model" || len(parts) != 3 || parts[1].(map[string]any)["thoughtSignature"] != "c2ln" {
		t.Fatalf("model content = %#v", model)
	}
	results := contents[2].(map[string]any)
	responses := results["parts"].([]any)
	first := responses[0].(map[string]any)["functionResponse"].(map[string]any)
	second := responses[1].(map[string]any)["functionResponse"].(map[string]any)
	if results["role"] != "user" || len(responses) != 2 ||
		!reflect.DeepEqual(first, map[string]any{"id": "call_1", "name": "read_file", "response": map[string]any{"error": "missing"}}) {
		t.Fatalf("tool results = %#v", results)
	}
	// Gemini sent the second call without an ID, so the answer has none;
	// its image travels as a response part.
	if _, hasID := second["id"]; hasID || second["name"] != "multi_tool_use_parallel" ||
		!reflect.DeepEqual(second["parts"], []any{map[string]any{"inlineData": map[string]any{"mimeType": "image/png", "data": "cG5n"}}}) {
		t.Fatalf("second result = %#v", second)
	}
	if contents[3].(map[string]any)["role"] != "user" {
		t.Fatalf("follow-up user turn = %#v", contents[3])
	}
	if got := wire["toolConfig"]; !reflect.DeepEqual(got, map[string]any{"functionCallingConfig": map[string]any{"mode": "ANY"}}) {
		t.Fatalf("toolConfig = %#v", got)
	}
	if got := wire["generationConfig"]; !reflect.DeepEqual(got, map[string]any{"maxOutputTokens": float64(4096)}) {
		t.Fatalf("generationConfig = %#v", got)
	}
}

func TestGeminiEncodeRebuildsHistoryItCannotReplay(t *testing.T) {
	wire := encodeGeminiForTest(t, &ChatRequest{Model: "gemini-3.5-flash", Messages: geminiTestHistory(false)})
	parts := wire["contents"].([]any)[1].(map[string]any)["parts"].([]any)
	wantFirst := map[string]any{"functionCall": map[string]any{"id": "call_1", "name": "read_file", "args": map[string]any{"path": "a.go"}}, "thoughtSignature": geminiSkipSignature}
	if len(parts) != 3 || !reflect.DeepEqual(parts[0], map[string]any{"text": "Checking."}) || !reflect.DeepEqual(parts[1], wantFirst) {
		t.Fatalf("rebuilt parts = %#v", parts)
	}
	if _, signed := parts[2].(map[string]any)["thoughtSignature"]; signed {
		t.Fatalf("only the first call carries the signature: %#v", parts[2])
	}
	second := wire["contents"].([]any)[2].(map[string]any)["parts"].([]any)[1].(map[string]any)["functionResponse"].(map[string]any)
	if second["id"] != "calltool-local" {
		t.Fatalf("rebuilt calls are answered with their IDs: %#v", second)
	}
}

func TestGeminiFunctionSchemaKeepsJSONSchemaAndFixesWhatGeminiRejects(t *testing.T) {
	schema, err := geminiFunctionSchema(json.RawMessage(`{"$schema":"x","type":"object","properties":{"mode":{"const":"text"},"n":{"type":["integer","null"],"maximum":12345678901234567890},"items":{"type":"array","uniqueItems":true,"items":{"$ref":"#/$defs/item"}}},"required":["mode","ghost"],"additionalProperties":false,"$defs":{"item":{"type":"string"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(schema)
	want := `{"$defs":{"item":{"type":"string"}},"additionalProperties":false,"properties":{"items":{"items":{"$ref":"#/$defs/item"},"type":"array","uniqueItems":true},"mode":{"enum":["text"]},"n":{"maximum":12345678901234567890,"type":["integer","null"]}},"required":["mode"],"type":"object"}`
	if string(encoded) != want {
		t.Fatalf("schema = %s\nwant %s", encoded, want)
	}
	// Property names and literal values are data, not schema keywords.
	schema, err = geminiFunctionSchema(json.RawMessage(`{"type":"object","properties":{"const":{"type":"string","const":"x","enum":["x","y"]},"$schema":{"type":"string","default":{"const":1,"$schema":2}}},"required":["const","$schema"]}`))
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ = json.Marshal(schema)
	want = `{"properties":{"$schema":{"default":{"$schema":2,"const":1},"type":"string"},"const":{"enum":["x"],"type":"string"}},"required":["const","$schema"],"type":"object"}`
	if string(encoded) != want {
		t.Fatalf("schema = %s\nwant %s", encoded, want)
	}
}

func TestGeminiChatURLAndAuth(t *testing.T) {
	p := FromPreset("google", "AQ.test", "")
	if p.ProtocolID() != "gemini" || p.BaseURL != "https://generativelanguage.googleapis.com/v1beta" {
		t.Fatalf("google preset = %s %s", p.ProtocolID(), p.BaseURL)
	}
	client, err := p.Client()
	if err != nil {
		t.Fatal(err)
	}
	if got := client.chatURL(&ChatRequest{Model: "gemini-3.5-flash", Stream: true}).String(); got != "https://generativelanguage.googleapis.com/v1beta/models/gemini-3.5-flash:streamGenerateContent?alt=sse" {
		t.Fatalf("stream URL = %s", got)
	}
	if got := client.chatURL(&ChatRequest{Model: "tunedModels/mine"}).String(); got != "https://generativelanguage.googleapis.com/v1beta/tunedModels/mine:generateContent" {
		t.Fatalf("unary URL = %s", got)
	}
	headers, err := p.RequestHeaders(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if headers.Get("x-goog-api-key") != "AQ.test" || headers.Get("Authorization") != "" {
		t.Fatalf("headers = %v", headers)
	}
}

func TestGeminiRequestExtrasMergeIntoGenerationConfig(t *testing.T) {
	p := FromPreset("google", "k", "")
	p.Model = "gemini-3.5-flash"
	p.SetThinkingEffort("high")
	p.SetServiceTier("fast")
	p.SetExtraBody(map[string]any{"generationConfig": map[string]any{"seed": 7}})
	client, err := p.Client()
	if err != nil {
		t.Fatal(err)
	}
	body, err := client.encodeChatRequest(&ChatRequest{Model: p.Model, Messages: []ChatMessage{NewTextMessage("user", "hi")}, MaxOutputTokens: 100})
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(body, &wire); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"maxOutputTokens": float64(100), "seed": float64(7),
		"thinkingConfig": map[string]any{"thinkingLevel": "HIGH", "includeThoughts": true}}
	if !reflect.DeepEqual(wire["generationConfig"], want) || wire["serviceTier"] != "priority" {
		t.Fatalf("generationConfig = %#v serviceTier = %v", wire["generationConfig"], wire["serviceTier"])
	}
}

// geminiRejectingServer uses the native INVALID_ARGUMENT envelope when a model
// rejects a thinking setting or another request option.
func geminiRejectingServer(t *testing.T, reject func(providersim.Request) string) *providersim.Server {
	t.Helper()
	server := providersim.New(t)
	server.Respond(func(request providersim.Request) providersim.Turn {
		if message := reject(request); message != "" {
			return providersim.HTTPError(providersim.InvalidRequest).WithFaultMessage(message)
		}
		return providersim.Text("ok")
	})
	return server
}

func streamGeminiText(t *testing.T, p *Provider) string {
	t.Helper()
	client, err := p.Client()
	if err != nil {
		t.Fatal(err)
	}
	stream, err := client.Stream(context.Background(), &ChatRequest{Model: p.Model, Messages: []ChatMessage{NewTextMessage("user", "hi")}})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	var text strings.Builder
	for {
		update, err := stream.Next()
		if errors.Is(err, io.EOF) {
			return text.String()
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		text.WriteString(update.ContentDelta)
	}
}

func thinkingLevelOf(request providersim.Request) string {
	var thinking struct {
		ThinkingLevel string `json:"thinkingLevel"`
	}
	_ = json.Unmarshal(request.Reasoning, &thinking)
	return thinking.ThinkingLevel
}

func TestGeminiLearnsRejectedThinkingLevelsAndKeepsThem(t *testing.T) {
	// Gemma accepts MINIMAL and HIGH only, and names no level when it
	// rejects one.
	server := geminiRejectingServer(t, func(request providersim.Request) string {
		switch thinkingLevelOf(request) {
		case "MINIMAL", "HIGH":
			return ""
		}
		return "Thinking level is not supported for this model."
	})
	p := FromPreset("google", "k", server.BaseURL(providersim.Gemini))
	p.Model = "gemma-4-31b-it"
	p.SetThinkingEffort("medium")
	if got := streamGeminiText(t, p); got != "ok" {
		t.Fatalf("text = %q", got)
	}
	var levels []string
	for _, request := range server.Requests() {
		levels = append(levels, thinkingLevelOf(request))
	}
	// MEDIUM is rejected, the nearer lower LOW is rejected, then HIGH works.
	if !reflect.DeepEqual(levels, []string{"MEDIUM", "LOW", "HIGH"}) {
		t.Fatalf("levels sent = %v", levels)
	}
	before := len(server.Requests())
	got := streamGeminiText(t, p)
	requests := server.Requests()[before:]
	if got != "ok" || len(requests) != 1 || thinkingLevelOf(requests[0]) != "HIGH" {
		t.Fatalf("learned request: text=%q requests=%v", got, requests)
	}
}

func TestGeminiLearnsModelWithoutThinking(t *testing.T) {
	server := geminiRejectingServer(t, func(request providersim.Request) string {
		if len(request.Reasoning) != 0 {
			return "Thinking is not enabled for models/antigravity-preview-latest"
		}
		return ""
	})
	p := FromPreset("google", "k", server.BaseURL(providersim.Gemini))
	p.Model = "antigravity-preview-latest"
	got := streamGeminiText(t, p)
	requests := server.Requests()
	if got != "ok" || len(requests) != 2 {
		t.Fatalf("text=%q requests=%v", got, requests)
	}
	var body map[string]json.RawMessage
	_ = json.Unmarshal(requests[1].Raw, &body)
	if _, has := body["generationConfig"]; has {
		t.Fatalf("retry still sends thinking: %s", requests[1].Raw)
	}
}

func TestGeminiUnrelatedRejectionIsNotRetried(t *testing.T) {
	server := geminiRejectingServer(t, func(providersim.Request) string {
		return "Function call is missing a thought_signature in functionCall parts."
	})
	p := FromPreset("google", "k", server.BaseURL(providersim.Gemini))
	p.Model = "gemini-3.5-flash"
	client, err := p.Client()
	if err != nil {
		t.Fatal(err)
	}
	stream, err := client.Stream(context.Background(), &ChatRequest{Model: p.Model, Messages: []ChatMessage{NewTextMessage("user", "hi")}})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	_, err = stream.Next()
	var status *StatusError
	if !errors.As(err, &status) || status.StatusCode != 400 || !strings.Contains(status.Error(), "INVALID_ARGUMENT: Function call is missing") || len(server.Requests()) != 1 {
		t.Fatalf("err = %v bodies = %d", err, len(server.Requests()))
	}
}

func googleErrorResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}

func TestGoogleErrorEnvelopeClassification(t *testing.T) {
	daily := readStatusError(googleErrorResponse(429, `{"error":{"code":429,"message":"You exceeded your current quota.","status":"RESOURCE_EXHAUSTED","details":[
		{"@type":"type.googleapis.com/google.rpc.QuotaFailure","violations":[{"quotaId":"GenerateRequestsPerDayPerProjectPerModel-FreeTier"},{"quotaId":"GenerateRequestsPerMinutePerProjectPerModel-FreeTier"}]},
		{"@type":"type.googleapis.com/google.rpc.RetryInfo","retryDelay":"48486.2s"}]}}`), 1<<20)
	if daily.Retryable() || !Permanent(daily) || ErrorSummary(daily) != "Provider quota or billing limit reached" {
		t.Fatalf("daily quota: retryable=%v summary=%q code=%q", daily.Retryable(), ErrorSummary(daily), daily.Code)
	}
	if daily.Error() != "API 429: RESOURCE_EXHAUSTED: You exceeded your current quota." {
		t.Fatalf("message = %q", daily.Error())
	}
	minute := readStatusError(googleErrorResponse(429, `{"error":{"code":429,"message":"Rate limited.","status":"RESOURCE_EXHAUSTED","details":[
		{"@type":"type.googleapis.com/google.rpc.QuotaFailure","violations":[{"quotaId":"GenerateRequestsPerMinutePerProjectPerModel"}]},
		{"@type":"type.googleapis.com/google.rpc.RetryInfo","retryDelay":"17.5s"}]}}`), 1<<20)
	if !minute.Retryable() || minute.RetryAfter != 17500*time.Millisecond || ErrorSummary(minute) != "Provider rate limit reached" {
		t.Fatalf("per-minute: retryable=%v after=%v summary=%q", minute.Retryable(), minute.RetryAfter, ErrorSummary(minute))
	}
	badKey := readStatusError(googleErrorResponse(400, `{"error":{"code":400,"message":"API key not valid.","status":"INVALID_ARGUMENT","details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"API_KEY_INVALID","domain":"googleapis.com"}]}}`), 1<<20)
	if badKey.Retryable() || ErrorSummary(badKey) != "Provider authentication failed" {
		t.Fatalf("bad key summary = %q", ErrorSummary(badKey))
	}
	overload := readStatusError(googleErrorResponse(503, `{"error":{"code":503,"message":"This model is currently experiencing high demand.","status":"UNAVAILABLE"}}`), 1<<20)
	if !overload.Retryable() {
		t.Fatal("503 UNAVAILABLE must be retryable")
	}
	// Interactions frames errors as SSE and gives the daily window only as
	// Retry-After.
	daily = readStatusError(&http.Response{StatusCode: 429, Header: http.Header{"Content-Type": {"text/event-stream"}, "Retry-After": {"46570"}},
		Body: io.NopCloser(strings.NewReader("{\"error\":{\"message\":\"Rate limit exceeded for model gemini-3.6-flash (limit: 20 requests per day on Free Tier).\",\"code\":\"too_many_requests\"}}\n\n"))}, 1<<20)
	if daily.Retryable() || ErrorSummary(daily) != "Provider quota or billing limit reached" {
		t.Fatalf("interactions daily quota: retryable=%v summary=%q", daily.Retryable(), ErrorSummary(daily))
	}
	throttled := readStatusError(&http.Response{StatusCode: 429, Header: http.Header{"Retry-After": {"30"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"slow down","code":"too_many_requests"}}`))}, 1<<20)
	if !throttled.Retryable() || throttled.RetryAfter != 30*time.Second {
		t.Fatalf("short Retry-After: retryable=%v after=%v", throttled.Retryable(), throttled.RetryAfter)
	}
	framed := readStatusError(&http.Response{StatusCode: 503, Header: http.Header{"Content-Type": {"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader("event: error\ndata: {\"error\":{\"message\":\"gemma-4-31b-it is currently experiencing high demand.\",\"code\":\"service_unavailable\"},\"event_type\":\"error\"}\n\n"))}, 1<<20)
	if !framed.Retryable() || framed.Error() != "API 503: service_unavailable: gemma-4-31b-it is currently experiencing high demand." {
		t.Fatalf("SSE-framed error = %q retryable=%v", framed.Error(), framed.Retryable())
	}
	overflow := readStatusError(googleErrorResponse(400, `{"error":{"code":400,"message":"The input token count (1200000) exceeds the maximum number of tokens allowed (1048576).","status":"INVALID_ARGUMENT"}}`), 1<<20)
	if !IsContextWindowExceeded(overflow) {
		t.Fatal("Gemini context overflow is not recognized")
	}
}

func TestGeminiModelListing(t *testing.T) {
	page, err := (&GeminiStandard{}).DecodeModelsPage([]byte(`{"models":[
		{"name":"models/gemini-3.5-flash","displayName":"Gemini 3.5 Flash","inputTokenLimit":1048576,"outputTokenLimit":65536,"supportedGenerationMethods":["generateContent","countTokens"],"thinking":true},
		{"name":"models/gemini-embedding-2","supportedGenerationMethods":["embedContent"]},
		{"name":"models/gemini-decision-1","displayName":"Gemini Decision 1","inputTokenLimit":262144,"outputTokenLimit":1,"supportedGenerationMethods":["createDecision"]}],
		"nextPageToken":"next"}`))
	if err != nil {
		t.Fatal(err)
	}
	want := []ModelInfo{
		{ID: "gemini-3.5-flash", Name: "Gemini 3.5 Flash", ContextWindow: 1048576, MaxOutput: 65536},
		{ID: "gemini-decision-1", Name: "Gemini Decision 1", ContextWindow: 262144, MaxOutput: 1, Decision: true},
	}
	if !reflect.DeepEqual(page.Models, want) || page.Next != "next" {
		t.Fatalf("page = %+v", page)
	}
	if got := (&GeminiStandard{}).ModelsURL(mustURL(t, "https://g.example/v1beta"), "next").String(); got != "https://g.example/v1beta/models?pageSize=1000&pageToken=next" {
		t.Fatalf("models URL = %s", got)
	}
}

func TestGeminiDecisionCodec(t *testing.T) {
	codec := &GeminiStandard{}
	questions := map[string]DecisionQuestion{
		"q1": {Type: "noul", Instructions: "Is this code?"},
		"q2": {Type: "choice", Instructions: "Which area?", Criteria: map[string]string{"backend": "Server", "ui": "Interface"}},
	}
	wire := codec.EncodeDecision("gemini-decision-1", "state", questions)
	if !reflect.DeepEqual(wire["input"], []any{map[string]any{"type": "text", "text": "state"}}) {
		t.Fatalf("decision input = %#v", wire["input"])
	}
	reply, err := onceJSON([]byte(`{"answers":{"q2":{"confidence":0.99,"choice":"backend","probabilities":{"backend":0.99,"ui":0.01},"type":"choice"},"q1":{"confidence":0.98,"noul":0.98,"type":"noul"}},"usage":{"total_tokens":147}}`))
	if err != nil {
		t.Fatal(err)
	}
	answers, err := codec.DecodeDecision(reply, questions)
	if err != nil || answers["q1"].Noul != 0.98 || answers["q2"].Choice != "backend" {
		t.Fatalf("answers = %+v, %v", answers, err)
	}
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
