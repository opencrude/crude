package providersim

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"
)

const plainChat = `{"model":"gpt-4o-mini","messages":[{"role":"user","content":"Hello"}]}`
const streamingChat = `{"model":"gpt-4o-mini","messages":[{"role":"user","content":"Hello"}],"stream":true}`
const richChat = `{
  "model":"gpt-4o-mini",
  "messages":[
    {"role":"developer","content":[{"type":"text","text":"Be concise.","prompt_cache_breakpoint":{"mode":"explicit","ttl":"30m"}}]},
    {"role":"user","content":[{"type":"text","text":"Look."},{"type":"image_url","image_url":{"url":"https://example.test/x.png"}},{"type":"file","file":{"file_id":"file-1"}}]},
    {"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"read","arguments":"{\"id\":1}"}}]},
    {"role":"tool","tool_call_id":"call_1","content":[{"type":"text","text":"fo"},{"type":"text","text":"und"}]}
  ],
  "tools":[{"type":"function","function":{"name":"read","description":"Read a record","parameters":{"type":"object","properties":{"id":{"type":"integer"}},"additionalProperties":false}}}],
  "tool_choice":{"type":"function","function":{"name":"read"}},
  "max_completion_tokens":80,
  "prompt_cache_key":"scenario",
  "store":false
}`

func TestChatCompletionsAndModelList(t *testing.T) {
	s := New(t, WithModels(Chat, "gpt-4o-mini", "other-model"))
	if _, ok := s.Last(); ok {
		t.Fatal("new server has a journal entry")
	}
	s.Script(Text("line 1\nline 2 😺").WithTools(ToolCall{ID: "call_x", Name: "read", Arguments: `{"id":7}`}, ToolCall{Name: "status", Arguments: "{}"}).
		WithReasoning("private", "secret-sig", "secret-payload").WithUsage(Usage{Input: 1500, Output: 15, Cached: 1152, CacheWrite: 128, Reasoning: 4}),
		Refusal("I cannot do that.").WithFinish(FinishFiltered))
	models := get(t, s.BaseURL(Chat)+"/models")
	body := jsonResponse(t, models, http.StatusOK, "ListModelsResponse")
	ids := make([]string, 0, 2)
	for _, rawModel := range body["data"].([]any) {
		ids = append(ids, rawModel.(map[string]any)["id"].(string))
	}
	if !reflect.DeepEqual(ids, []string{"gpt-4o-mini", "other-model"}) {
		t.Fatalf("models = %v", ids)
	}

	stream := post(t, s, strings.TrimSuffix(streamingChat, "}")+`,"stream_options":{"include_usage":true}}`)
	chunks, done, readErr := chatEvents(t, stream)
	if readErr != nil || !done || len(chunks) != 8 {
		t.Fatalf("stream: %d chunks, done=%v, err=%v", len(chunks), done, readErr)
	}
	for _, chunk := range chunks[:len(chunks)-1] {
		if usage, ok := chunk["usage"]; !ok || usage != nil {
			t.Errorf("working chunk usage = %#v, present = %v", usage, ok)
		}
		if chunk["id"] != chunks[0]["id"] || chunk["model"] != "gpt-4o-mini" {
			t.Errorf("unstable chunk envelope: %#v", chunk)
		}
	}
	if delta(chunks[0])["role"] != "assistant" || delta(chunks[1])["content"] != "line 1\nline 2 😺" {
		t.Fatal("missing role or unescaped text")
	}
	for i, want := range []ToolCall{{ID: "call_x", Name: "read", Arguments: `{"id":7}`}, {Name: "status", Arguments: `{}`}} {
		start := firstTool(delta(chunks[2+2*i]))
		more := firstTool(delta(chunks[3+2*i]))
		if start["index"] != json.Number(fmt.Sprint(i)) || more["index"] != start["index"] || start["type"] != "function" || start["id"] == "" ||
			(want.ID != "" && start["id"] != want.ID) || start["function"].(map[string]any)["name"] != want.Name ||
			more["function"].(map[string]any)["arguments"] != want.Arguments {
			t.Fatalf("tool %d start = %#v, continuation = %#v", i, start, more)
		}
	}
	if choice(chunks[6])["finish_reason"] != "tool_calls" || len(chunks[7]["choices"].([]any)) != 0 {
		t.Fatal("expected tool_calls finish followed by usage-only chunk")
	}
	usage := chunks[7]["usage"].(map[string]any)
	if usage["total_tokens"] != json.Number("1515") || usage["prompt_tokens"] != json.Number("1500") ||
		usage["prompt_tokens_details"].(map[string]any)["cached_tokens"] != json.Number("1152") ||
		usage["prompt_tokens_details"].(map[string]any)["cache_write_tokens"] != json.Number("128") ||
		usage["completion_tokens_details"].(map[string]any)["reasoning_tokens"] != json.Number("4") {
		t.Fatalf("usage override = %#v", usage)
	}
	if raw := fmt.Sprint(chunks); strings.Contains(raw, "reasoning_content") || strings.Contains(raw, "secret-") {
		t.Fatal("public Chat emitted unsupported reasoning content")
	}

	body = jsonResponse(t, post(t, s, plainChat), http.StatusOK, "CreateChatCompletionResponse")
	one := choice(body)
	message := one["message"].(map[string]any)
	if message["refusal"] != "I cannot do that." || message["content"] != nil || one["finish_reason"] != "content_filter" {
		t.Fatalf("refusal response = %#v", one)
	}
	if entries := s.Requests(); len(entries) != 3 || entries[0].Method != http.MethodGet || !entries[1].Stream || entries[2].Stream {
		t.Fatalf("journal did not preserve three HTTP operations: %#v", entries)
	}
}

func TestChatNeutralJournalAndResponder(t *testing.T) {
	s := New(t)
	s.Script(Text("first"))
	s.Respond(func(r Request) Turn {
		if r.Index != 2 || r.Model != "gpt-4o-mini" || r.Raw == nil || r.Effective.Messages[2].Parts[0].Text != "found" {
			t.Errorf("unexpected responder entry: %#v", r)
		}
		// A responder can inspect the already-journaled request and mutate its
		// private copy without deadlocking or corrupting journal history.
		last, ok := s.Last()
		if !ok || last.Index != r.Index {
			t.Errorf("responder request is not visible in the journal")
		}
		r.Raw[0] = '!'
		r.Header.Set("X-Sim-Test", "changed")
		r.Effective.Tools[0].Schema[0] = '!'
		return CallTools(ToolCall{ID: "reply_call", Name: "read", Arguments: `{"id":9}`})
	})
	jsonResponse(t, post(t, s, plainChat), http.StatusOK, "CreateChatCompletionResponse")
	body := jsonResponse(t, post(t, s, richChat), http.StatusOK, "CreateChatCompletionResponse")
	message := choice(body)["message"].(map[string]any)
	call := firstTool(message)
	if call["id"] != "reply_call" || call["function"].(map[string]any)["arguments"] != `{"id":9}` || choice(body)["finish_reason"] != "tool_calls" {
		t.Fatalf("non-stream tool call = %#v", body)
	}
	last, _ := s.Last()
	if string(last.Raw) != richChat || !reflect.DeepEqual(last.Header.Values("X-Sim-Test"), []string{"one", "two"}) ||
		last.Wire != Chat || last.Method != http.MethodPost || last.Path != "/v1/chat/completions" || last.OutputLimit != "80" || last.Store == nil || *last.Store || last.PreviousResponseID != "" {
		t.Fatalf("journal request metadata: %#v", last)
	}
	if len(last.System) != 1 || last.System[0].Text != "Be concise." || last.System[0].Role != "developer" || last.System[0].Path != "/messages/0/content/0" {
		t.Fatalf("system projection: %#v", last.System)
	}
	parts := last.Messages[0].Parts
	if len(parts) != 3 || parts[0].Text != "Look." || parts[1].Kind != PartImage || parts[1].ImageURL != "https://example.test/x.png" ||
		parts[2].Kind != PartOpaque || string(parts[2].Raw) != `{"file":{"file_id":"file-1"},"type":"file"}` {
		t.Fatalf("multi-modal projection: %#v", parts)
	}
	out, result := last.Messages[1].Parts[0], last.Messages[2].Parts[0]
	if string(result.Raw) != `[{"text":"fo","type":"text"},{"text":"und","type":"text"}]` ||
		out.Kind != PartToolCall || out.Call != (ToolCall{ID: "call_1", Name: "read", Arguments: `{"id":1}`}) ||
		result.Kind != PartToolResult || result.CallID != "call_1" || result.Text != "found" {
		t.Fatalf("call/result = %#v / %#v", out, result)
	}
	if tool := last.Tools[0]; tool.Name != "read" || tool.Description != "Read a record" || !json.Valid(tool.Schema) ||
		string(last.ToolChoice) != `{"function":{"name":"read"},"type":"function"}` ||
		len(last.CacheMarkers) != 2 || last.CacheMarkers[0].Path != "/prompt_cache_key" || string(last.CacheMarkers[0].Value) != `"scenario"` ||
		last.CacheMarkers[1].Path != "/messages/0/content/0/prompt_cache_breakpoint" || string(last.CacheMarkers[1].Value) != `{"mode":"explicit","ttl":"30m"}` ||
		!reflect.DeepEqual(last.Conversation, last.Effective) {
		t.Fatalf("tools, cache, or effective projection: %#v", last)
	}
	last.Messages[0].Parts[0].Text = "edited"
	last.Raw[0] = '!'
	last.Header.Set("X-Sim-Test", "edited")
	if after := s.Requests()[1]; after.Messages[0].Parts[0].Text != "Look." || after.Header.Get("X-Sim-Test") != "one" || string(after.Raw) != richChat {
		t.Fatal("journal mutated through its snapshot")
	}
}

func TestChatViolationsReportAndReturnProviderError(t *testing.T) {
	issues := make(chan string, 1)
	s := newServer(t, func(msg string, args ...any) { issues <- fmt.Sprintf(msg, args...) }, nil)
	s.Script(Text("still first"))
	for _, tc := range []struct{ body, path, param string }{
		{`{"model":"gpt-4o-mini"}`, "/messages", "messages"},
		{`{"model":"gpt-4o-mini","messages":[{"role":"user","content":42}]}`, "/properties/messages/items", ""},
		{strings.TrimSuffix(plainChat, "}") + `,"extra/key~unknown":true}`, "/extra~1key~0unknown", "extra/key~unknown"},
		{strings.TrimSuffix(plainChat, "}") + `,"":true}`, "unknown top-level field", ""},
		{plainChat + ` {}`, "more than one JSON value", ""},
	} {
		body := jsonResponse(t, post(t, s, tc.body), http.StatusBadRequest, "ErrorResponse")
		payload := body["error"].(map[string]any)
		if payload["type"] != "invalid_request_error" || (tc.param != "" && payload["param"] != tc.param) {
			t.Errorf("bad request payload = %#v", payload)
		}
		select {
		case issue := <-issues:
			if !strings.Contains(issue, tc.path) {
				t.Fatalf("report %q omits %q", issue, tc.path)
			}
		default:
			t.Fatal("request violation did not fail the test")
		}
		last, _ := s.Last()
		if !strings.Contains(last.Violation, tc.path) || string(last.Raw) != tc.body {
			t.Fatal("rejected request absent from journal")
		}
	}
	// The wire's top-level policy must not close open nested schema objects.
	valid := strings.Replace(plainChat, `"content":"Hello"`, `"content":"Hello","nested_extension":true`, 1)
	body := jsonResponse(t, post(t, s, valid), http.StatusOK, "CreateChatCompletionResponse")
	if choice(body)["message"].(map[string]any)["content"] != "still first" {
		t.Fatal("an invalid request consumed the scripted turn")
	}
}

func TestChatHTTPFaults(t *testing.T) {
	s := New(t)
	date := time.Date(2030, time.January, 2, 3, 4, 5, 0, time.UTC)
	for _, tc := range []struct {
		name, code, header, value string
		status                    int
		turn                      Turn
	}{
		{"rate_seconds", "rate_limit_exceeded", "Retry-After", "4", 429, HTTPError(RateLimit).WithRetryHint(RetryAfterSeconds(4))},
		{"rate_milliseconds", "rate_limit_exceeded", "retry-after-ms", "125", 429, HTTPError(RateLimit).WithRetryHint(RetryAfterMilliseconds(125))},
		{"rate_date", "rate_limit_exceeded", "Retry-After", "Wed, 02 Jan 2030 03:04:05 GMT", 429, HTTPError(RateLimit).WithRetryHint(RetryAfterDate(date))},
		{"rate_none", "rate_limit_exceeded", "", "", 429, HTTPError(RateLimit)},
		{"context", "context_length_exceeded", "", "", 400, HTTPError(ContextOverflow)},
		{"overload", "server_is_overloaded", "", "", 503, HTTPError(Overload)},
		{"auth", "invalid_api_key", "", "", 401, HTTPError(Auth)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s.Script(tc.turn)
			res := post(t, s, streamingChat)
			body := jsonResponse(t, res, tc.status, "ErrorResponse")
			if body["error"].(map[string]any)["code"] != tc.code {
				t.Fatalf("fault payload = %#v", body)
			}
			for _, header := range []string{"Retry-After", "retry-after-ms"} {
				want := ""
				if header == tc.header {
					want = tc.value
				}
				if got := res.Header.Get(header); got != want {
					t.Errorf("header %s = %q, want %q", header, got, want)
				}
			}
		})
	}
}

func TestChatStreamFaultsAndGate(t *testing.T) {
	s := New(t)
	gate := NewGate()
	s.Script(Text("visible").WithStreamError(2, Overload), Text("partial").WithCut(2), Text("released").WithGate(gate), Text("concurrent"))
	events, done, err := chatEvents(t, post(t, s, streamingChat))
	if done || err != nil || len(events) != 3 || delta(events[1])["content"] != "visible" || events[2]["error"].(map[string]any)["code"] != "server_is_overloaded" {
		t.Fatalf("in-band fault: events=%#v done=%v err=%v", events, done, err)
	}
	events, done, err = chatEvents(t, post(t, s, streamingChat))
	if done || err != io.ErrUnexpectedEOF || len(events) != 2 || delta(events[1])["content"] != "partial" {
		t.Fatalf("connection cut: events=%#v done=%v err=%v", events, done, err)
	}

	res := post(t, s, streamingChat)
	if err := gate.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	if req, ok := s.Last(); !ok || req.Index != 3 || !req.Stream {
		t.Fatalf("gate did not announce the journaled request: %#v", req)
	}
	type streamRead struct {
		raw []byte
		err error
	}
	started := make(chan struct{})
	result := make(chan streamRead, 1)
	go func() {
		close(started)
		raw, err := io.ReadAll(res.Body)
		result <- streamRead{raw, err}
	}()
	<-started
	other := jsonResponse(t, post(t, s, plainChat), http.StatusOK, "CreateChatCompletionResponse")
	if choice(other)["message"].(map[string]any)["content"] != "concurrent" {
		t.Fatal("the gated request blocked another turn")
	}
	select {
	case got := <-result:
		t.Fatalf("gated stream ended before Release: %q, %v", got.raw, got.err)
	default:
	}
	gate.Release()
	gate.Release()
	got := <-result
	events, done = parseChatEvents(t, got.raw)
	if got.err != nil || !done || len(events) != 3 || delta(events[1])["content"] != "released" {
		t.Fatalf("released stream: %q, %v", got.raw, got.err)
	}
	for _, event := range events {
		if _, exists := event["usage"]; exists {
			t.Error("usage was emitted without stream_options.include_usage")
		}
	}
}

func TestChatDeepSeekIsAnExplicitGatewayProfile(t *testing.T) {
	s := New(t, WithChatGateway("deepseek-flash", DeepSeek))
	s.Script(Text("answer").WithReasoning("thought", "not-on-chat", "not-on-chat"), Reasoning("nonstream thought", "opaque", "opaque"), Text("no replay"))
	stream := `{"model":"deepseek-flash","messages":[{"role":"user","content":"Hello"}],"stream":true,"thinking":{"type":"enabled"}}`
	events, done, err := chatEvents(t, post(t, s, stream))
	if !done || err != nil || len(events) != 4 || delta(events[1])["reasoning_content"] != "thought" || delta(events[2])["content"] != "answer" || strings.Contains(fmt.Sprint(events), "not-on-chat") {
		t.Fatalf("DeepSeek stream: %#v done=%v err=%v", events, done, err)
	}
	prior := `{"model":"deepseek-flash","messages":[{"role":"assistant","content":"answer","reasoning_content":"thought"}]}`
	withTools := strings.TrimSuffix(prior, "}") + `,"tools":[{"type":"function","function":{"name":"read"}}]}`
	body := jsonResponse(t, post(t, s, withTools), http.StatusOK, "CreateChatCompletionResponse")
	if msg := choice(body)["message"].(map[string]any); msg["reasoning_content"] != "nonstream thought" || strings.Contains(fmt.Sprint(msg), "opaque") {
		t.Fatalf("nonstream gateway reasoning = %#v", msg)
	}
	jsonResponse(t, post(t, s, prior), http.StatusOK, "CreateChatCompletionResponse")
	requests := s.Requests()
	if requests[1].Effective.Messages[0].Parts[0].Kind != PartReasoning || requests[1].Effective.Messages[0].Parts[0].Text != "thought" ||
		requests[2].Messages[0].Parts[0].Kind != PartReasoning || requests[2].Effective.Messages[0].Parts[0].Kind != PartText {
		t.Fatalf("DeepSeek replay input = %#v / %#v", requests[1].Effective, requests[2].Effective)
	}
}

func post(t *testing.T, s *Server, body string) *http.Response {
	t.Helper()
	r, err := http.NewRequestWithContext(t.Context(), http.MethodPost, s.BaseURL(Chat)+"/chat/completions", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Add("X-Sim-Test", "one")
	r.Header.Add("X-Sim-Test", "two")
	return do(t, r)
}

func get(t *testing.T, endpoint string) *http.Response {
	t.Helper()
	r, err := http.NewRequestWithContext(t.Context(), http.MethodGet, endpoint, nil)
	if err != nil {
		t.Fatal(err)
	}
	return do(t, r)
}

func do(t *testing.T, r *http.Request) *http.Response {
	t.Helper()
	res, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = res.Body.Close() })
	return res
}

func readResponse(t *testing.T, res *http.Response) []byte {
	t.Helper()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func jsonResponse(t *testing.T, res *http.Response, status int, component string) map[string]any {
	t.Helper()
	raw := readResponse(t, res)
	if res.StatusCode != status || res.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("HTTP %d %s: %s", res.StatusCode, res.Header.Get("Content-Type"), raw)
	}
	return validateChatOutput(t, component, raw)
}

func chatEvents(t *testing.T, response *http.Response) ([]map[string]any, bool, error) {
	t.Helper()
	if response.StatusCode != 200 || response.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("expected 200 SSE, got %s: %s", response.Status, readResponse(t, response))
	}
	raw, err := io.ReadAll(response.Body)
	events, done := parseChatEvents(t, raw)
	return events, done, err
}

func parseChatEvents(t *testing.T, raw []byte) ([]map[string]any, bool) {
	t.Helper()
	var events []map[string]any
	done := false
	for event := range strings.SplitSeq(strings.TrimSuffix(string(raw), "\n\n"), "\n\n") {
		if event == "" && len(raw) == 0 {
			break
		}
		data, ok := strings.CutPrefix(event, "data: ")
		if !ok || strings.Contains(data, "\n") || done || !strings.HasSuffix(string(raw), "\n\n") {
			t.Fatalf("invalid Chat SSE framing: %q", raw)
		}
		if data == "[DONE]" {
			done = true
			continue
		}
		component := "CreateChatCompletionStreamResponse"
		if strings.HasPrefix(data, `{"error":`) {
			component = "ErrorResponse"
		}
		events = append(events, validateChatOutput(t, component, []byte(data)))
	}
	return events, done
}

func validateChatOutput(t *testing.T, component string, raw []byte) map[string]any {
	t.Helper()
	b, err := wireBundle(Chat)
	if err != nil {
		t.Fatal(err)
	}
	value, err := decodeJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.validate(component, value); err != nil {
		t.Fatalf("%s rejected emitted body %s: %v", component, raw, err)
	}
	return value.(map[string]any)
}

func choice(event map[string]any) map[string]any { return event["choices"].([]any)[0].(map[string]any) }
func delta(event map[string]any) map[string]any  { return choice(event)["delta"].(map[string]any) }
func firstTool(message map[string]any) map[string]any {
	return message["tool_calls"].([]any)[0].(map[string]any)
}
