package providersim

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"
)

const ollamaPrompt = `{"model":"fixture:latest","messages":[{"role":"user","content":"hello"}]}`

func TestOllamaStreamAndToolReplay(t *testing.T) {
	s := New(t)
	s.Script(
		Text("line 1\nline 2 😺").WithReasoning("first\nthen", "private-signature", "private-payload").
			WithTools(ToolCall{ID: "call_provided", Name: "get_weather", Arguments: `{"city":"Paris","days":2}`}, ToolCall{Name: "status"}).
			WithUsage(Usage{Input: 50, Cached: 30, CacheWrite: 9, Output: 14, Reasoning: 6}),
		Text("Paris: dry.").WithReasoning("hidden", "", "").WithFinish(FinishLength),
	)
	body := strings.TrimSuffix(ollamaPrompt, "}") + `,"think":"high","unknown_option":1e9999,
		"tools":[{"type":"function","function":{"name":"get_weather","description":"Read the forecast","parameters":{"type":"object"}}}]}`
	stream, err := ollamaLines(t, postOllama(t, s, "/api/chat", body))
	if err != nil || len(stream) != 4 {
		t.Fatalf("stream has %d lines, read error: %v", len(stream), err)
	}
	for i, event := range stream {
		msg := event["message"].(map[string]any)
		if event["model"] != "fixture:latest" || msg["role"] != "assistant" || event["done"] != (i == 3) {
			t.Fatalf("stream line %d: %#v", i, event)
		}
		if _, err := time.Parse(time.RFC3339Nano, event["created_at"].(string)); err != nil {
			t.Fatalf("line %d created_at: %v", i, err)
		}
		if i < 3 && (event["prompt_eval_count"] != nil || event["eval_count"] != nil || event["done_reason"] != nil) {
			t.Fatalf("line %d has terminal fields: %#v", i, event)
		}
	}
	thinking := stream[0]["message"].(map[string]any)
	answer := stream[1]["message"].(map[string]any)
	if thinking["thinking"] != "first\nthen" || thinking["content"] != "" || answer["content"] != "line 1\nline 2 😺" || answer["thinking"] != nil {
		t.Fatalf("thinking = %#v, answer = %#v", thinking, answer)
	}
	calls := stream[2]["message"].(map[string]any)["tool_calls"].([]any)
	if len(calls) != 2 {
		t.Fatalf("tool calls = %#v", calls)
	}
	first := calls[0].(map[string]any)
	function := first["function"].(map[string]any)
	if first["id"] != "call_provided" || function["index"] != json.Number("0") || function["name"] != "get_weather" ||
		!reflect.DeepEqual(function["arguments"], map[string]any{"city": "Paris", "days": json.Number("2")}) {
		t.Fatalf("first tool call = %#v", first)
	}
	second := calls[1].(map[string]any)
	function = second["function"].(map[string]any)
	if !strings.HasPrefix(second["id"].(string), "call_") || second["id"] == first["id"] || function["index"] != json.Number("1") ||
		function["name"] != "status" || !reflect.DeepEqual(function["arguments"], map[string]any{}) {
		t.Fatalf("second tool call = %#v", second)
	}
	final := stream[3]
	if final["done_reason"] != "stop" || final["prompt_eval_count"] != json.Number("50") ||
		final["prompt_eval_cached_count"] != json.Number("30") || final["eval_count"] != json.Number("14") ||
		final["message"].(map[string]any)["content"] != "" {
		t.Fatalf("terminal line = %#v", final)
	}
	for _, unwanted := range []string{"private-signature", "private-payload", "cache_write", "reasoning_tokens"} {
		if strings.Contains(fmt.Sprint(stream), unwanted) {
			t.Fatalf("unsupported field or value %q on the wire", unwanted)
		}
	}

	// Replay the call the HTTP client received, not the original fixture.
	replay := fmt.Sprintf(`{"model":"fixture:latest","stream":false,"think":false,
		"options":{"num_predict":41},"messages":[
		{"role":"system","content":"Use concise answers."},
		{"role":"user","content":"Look.","images":["aGVsbG8="]},
		{"role":"assistant","content":"","thinking":"first\nthen","tool_calls":%s},
		{"role":"tool","content":"dry","tool_name":"get_weather","tool_call_id":"call_provided"}]}`, jsonBytes(calls))
	one := ollamaJSON(t, postOllama(t, s, "/api/chat", replay), http.StatusOK, "ChatResponse")
	msg := one["message"].(map[string]any)
	if one["done"] != true || one["done_reason"] != "length" || one["eval_count"] != json.Number("3") ||
		one["prompt_eval_cached_count"] != nil || msg["content"] != "Paris: dry." || msg["thinking"] != nil {
		t.Fatalf("nonstream reply = %#v", one)
	}
	journal := s.Requests()
	if len(journal) != 2 || !journal[0].Stream || journal[0].Violation != "" ||
		string(journal[0].Reasoning) != `"high"` || journal[0].Tools[0].Name != "get_weather" ||
		string(journal[0].Tools[0].Schema) != `{"type":"object"}` {
		t.Fatalf("initial request journal = %#v", journal)
	}
	req := journal[1]
	if req.Wire != Ollama || req.Path != "/api/chat" || req.Method != http.MethodPost || req.Stream || req.OutputLimit != "41" ||
		string(req.Reasoning) != "false" || string(req.Raw) != replay || !reflect.DeepEqual(req.Conversation, req.Effective) {
		t.Fatalf("replay request journal = %#v", req)
	}
	if len(req.System) != 1 || req.System[0].Text != "Use concise answers." || req.System[0].Role != "system" || len(req.Messages) != 3 {
		t.Fatalf("conversation = %#v", req.Conversation)
	}
	image := req.Messages[0].Parts[1]
	got := req.Messages[1].Parts
	result := req.Messages[2].Parts[0]
	if image.Kind != PartImage || image.Path != "/messages/1/images/0" || string(image.Raw) != `"aGVsbG8="` ||
		len(got) != 3 || got[0].Kind != PartReasoning || got[0].Text != "first\nthen" || got[0].Path != "/messages/2/thinking" ||
		got[1].Kind != PartToolCall || got[1].Call != (ToolCall{ID: "call_provided", Name: "get_weather", Arguments: `{"city":"Paris","days":2}`}) ||
		got[2].Call.ID != second["id"] || result.Kind != PartToolResult || result.CallID != "call_provided" ||
		result.Text != "dry" || req.Messages[2].Name != "get_weather" {
		t.Fatalf("replay projection = %#v", req.Messages)
	}
}

func TestOllamaCacheCounts(t *testing.T) {
	s := New(t, WithCacheProfile(Ollama, "fixture:latest", CacheProfile{Minimum: 1, Granularity: 1}))
	s.Respond(func(Request) Turn { return Text("done") })
	first, err := ollamaLines(t, postOllama(t, s, "/api/chat", ollamaPrompt))
	if err != nil || len(first) != 2 || first[1]["prompt_eval_count"] != json.Number("9") ||
		first[1]["prompt_eval_cached_count"] != json.Number("0") {
		t.Fatalf("first prompt = %#v, %v", first, err)
	}
	body := `{"model":"fixture:latest","think":null,"stream":false,"messages":[{"role":"user","content":"hello"},{"role":"user","content":"next"}]}`
	second := ollamaJSON(t, postOllama(t, s, "/api/chat", body), http.StatusOK, "ChatResponse")
	if second["prompt_eval_count"] != json.Number("17") || second["prompt_eval_cached_count"] != json.Number("9") ||
		second["eval_count"] != json.Number("1") {
		t.Fatalf("continued prompt = %#v", second)
	}
	req, _ := s.Last()
	if string(req.Reasoning) != "null" || req.Prefix.PreviousIndex != 1 || req.Prefix.FirstDifference != "/messages/1" ||
		req.Prefix.CachedTokens != 9 || req.Prefix.CacheWriteTokens != 8 {
		t.Fatalf("continued prefix = %#v, think = %s", req.Prefix, req.Reasoning)
	}
}

func TestOllamaNonStreamingToolCall(t *testing.T) {
	s := New(t)
	s.Script(Text("Check.").WithReasoning("Need the forecast.", "", "").
		WithTools(ToolCall{ID: "call_one", Name: "get_weather", Arguments: `{"city":"Paris"}`}).
		WithUsage(Usage{Input: 15, Output: 7}))
	body := strings.TrimSuffix(ollamaPrompt, "}") + `,"stream":false,"think":true,
		"tools":[{"type":"function","function":{"name":"get_weather","parameters":{"type":"object"}}}]}`
	res := ollamaJSON(t, postOllama(t, s, "/api/chat", body), http.StatusOK, "ChatResponse")
	msg := res["message"].(map[string]any)
	if res["done"] != true || res["done_reason"] != "stop" || res["prompt_eval_count"] != json.Number("15") ||
		res["prompt_eval_cached_count"] != json.Number("0") || res["eval_count"] != json.Number("7") ||
		msg["content"] != "Check." || msg["thinking"] != "Need the forecast." {
		t.Fatalf("nonstream reply = %#v", res)
	}
	want, err := decodeJSON([]byte(`[{"id":"call_one","function":{"index":0,"name":"get_weather","arguments":{"city":"Paris"}}}]`))
	if err != nil || !reflect.DeepEqual(msg["tool_calls"], want) {
		t.Fatalf("nonstream tool calls = %#v; want %#v, %v", msg["tool_calls"], want, err)
	}
}

func TestOllamaValidatesRequests(t *testing.T) {
	issues := make(chan string, 1)
	s := newServer(t, func(msg string, args ...any) { issues <- fmt.Sprintf(msg, args...) }, nil)
	s.Script(Text("after invalid requests"))
	for _, tc := range []struct{ route, body, path string }{
		{"/api/chat", `{"messages":[]}`, "/model"},
		{"/api/chat", `{"model":"fixture"}`, "/messages"},
		{"/api/chat", `{"model":"fixture","messages":[{"role":"user","content":42}]}`, "content"},
		{"/api/chat", `{"model":"fixture","messages":[],"stream":"false"}`, "stream"},
		{"/api/chat", `{"model":"fixture","messages":[],"think":42}`, "think"},
		{"/api/chat", `{"model":"fixture","messages":[{"role":"assistant","content":"","thinking":[]}]}`, "/messages/0/thinking"},
		{"/api/chat", `{"model":"fixture","messages":[{"role":"assistant","content":"","tool_calls":[{"function":{"name":"weather","arguments":"{}"}}]}]}`, "arguments"},
		{"/api/chat", ollamaPrompt + ` {}`, "more than one JSON value"},
		{"/api/show", `{}`, "/model"},
		{"/api/show", `{"model":"fixture","verbose":"true"}`, "verbose"},
	} {
		res := ollamaJSON(t, postOllama(t, s, tc.route, tc.body), http.StatusBadRequest, "ErrorResponse")
		if len(res) != 1 || !strings.Contains(res["error"].(string), tc.path) {
			t.Fatalf("error response for %s %s = %#v", tc.route, tc.body, res)
		}
		select {
		case issue := <-issues:
			if !strings.Contains(issue, tc.path) {
				t.Fatalf("report %q does not identify %q", issue, tc.path)
			}
		default:
			t.Fatal("invalid request did not report a test failure")
		}
		req, _ := s.Last()
		if !strings.Contains(req.Violation, tc.path) || string(req.Raw) != tc.body || req.Prefix.PreviousIndex != 0 {
			t.Fatalf("rejected journal entry = %#v", req)
		}
	}
	stream, err := ollamaLines(t, postOllama(t, s, "/api/chat", ollamaPrompt))
	if err != nil || len(stream) != 2 || stream[0]["message"].(map[string]any)["content"] != "after invalid requests" {
		t.Fatalf("invalid requests consumed the script: %#v, %v", stream, err)
	}
}

func TestOllamaInventory(t *testing.T) {
	s := New(t, WithModels(Ollama, "fixture:latest", "other:1"))
	res, err := http.Get(s.BaseURL(Ollama) + "/api/tags")
	if err != nil {
		t.Fatal(err)
	}
	rows := ollamaJSON(t, res, http.StatusOK, "ListResponse")["models"].([]any)
	if len(rows) != 2 {
		t.Fatalf("inventory = %#v", rows)
	}
	for i, id := range []string{"fixture:latest", "other:1"} {
		row := rows[i].(map[string]any)
		if row["name"] != id || row["model"] != id || len(row["digest"].(string)) != 64 {
			t.Fatalf("inventory row %d = %#v", i, row)
		}
		show := ollamaJSON(t, postOllama(t, s, "/api/show", fmt.Sprintf(`{"model":%q,"verbose":true,"extra":1}`, id)), http.StatusOK, "ShowResponse")
		info := show["model_info"].(map[string]any)
		if info["qwen3.context_length"] != json.Number("32768") ||
			!reflect.DeepEqual(show["capabilities"], []any{"completion", "tools", "thinking"}) ||
			!reflect.DeepEqual(row["details"], show["details"]) {
			t.Fatalf("model details = %#v", show)
		}
	}
	missing := ollamaJSON(t, postOllama(t, s, "/api/show", `{"model":"missing"}`), http.StatusNotFound, "ErrorResponse")
	if len(missing) != 1 || missing["error"] != "model 'missing' not found" {
		t.Fatalf("missing model response = %#v", missing)
	}
	requests := s.Requests()
	if len(requests) != 4 || requests[0].Path != "/api/tags" || requests[1].Path != "/api/show" ||
		requests[1].Model != "fixture:latest" || requests[2].Model != "other:1" || requests[3].Violation != "" {
		t.Fatalf("inventory journal = %#v", requests)
	}
}

func TestOllamaFaults(t *testing.T) {
	s := New(t)
	for _, tc := range []struct {
		kind    FaultKind
		status  int
		message string
		hint    RetryHint
		retry   string
	}{
		{RateLimit, http.StatusTooManyRequests, "rate limit exceeded", RetryAfterSeconds(7), "7"},
		{ContextOverflow, http.StatusRequestEntityTooLarge, "the input length exceeds the context length", RetryHint{}, ""},
		{Overload, http.StatusServiceUnavailable, "server busy, please try again.  maximum pending requests exceeded", RetryHint{}, ""},
		{Auth, http.StatusUnauthorized, "unauthorized", RetryHint{}, ""},
	} {
		s.Script(HTTPError(tc.kind).WithRetryHint(tc.hint))
		res := postOllama(t, s, "/api/chat", ollamaPrompt)
		if got := res.Header.Get("Retry-After"); got != tc.retry {
			t.Errorf("fault %s Retry-After = %q; want %q", tc.kind, got, tc.retry)
		}
		payload := ollamaJSON(t, res, tc.status, "ErrorResponse")
		if len(payload) != 1 || payload["error"] != tc.message {
			t.Fatalf("HTTP fault %s = %#v", tc.kind, payload)
		}
	}
	s.Script(StreamError(0, Auth), Text("first").WithReasoning("thinking", "", "").WithStreamError(2, Overload), Text("before cut").WithCut(1), CutStream(0))
	earlyError := ollamaJSON(t, postOllama(t, s, "/api/chat", ollamaPrompt), http.StatusUnauthorized, "ErrorResponse")
	if earlyError["error"] != "unauthorized" {
		t.Fatalf("failure before first line = %#v", earlyError)
	}
	lines, err := ollamaLines(t, postOllama(t, s, "/api/chat", ollamaPrompt))
	if err != nil || len(lines) != 3 || lines[0]["done"] != false || lines[1]["done"] != false ||
		lines[1]["message"].(map[string]any)["content"] != "first" || len(lines[2]) != 1 ||
		lines[2]["error"] != "server busy, please try again.  maximum pending requests exceeded" {
		t.Fatalf("in-band fault = %#v, %v", lines, err)
	}
	lines, err = ollamaLines(t, postOllama(t, s, "/api/chat", ollamaPrompt))
	if !errors.Is(err, io.ErrUnexpectedEOF) || len(lines) != 1 || lines[0]["done"] != false ||
		lines[0]["message"].(map[string]any)["content"] != "before cut" {
		t.Fatalf("cut stream = %#v, %v", lines, err)
	}
	lines, err = ollamaLines(t, postOllama(t, s, "/api/chat", ollamaPrompt))
	if !errors.Is(err, io.ErrUnexpectedEOF) || len(lines) != 0 {
		t.Fatalf("cut before first line = %#v, %v", lines, err)
	}
}

func TestOllamaGate(t *testing.T) {
	s := New(t)
	for _, mode := range []string{"release", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			gate := NewGate()
			s.Script(Text("after release").WithGate(gate))
			defer gate.Release()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			body := strings.TrimSuffix(ollamaPrompt, "}") + `,"stream":true}`
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.BaseURL(Ollama)+"/api/chat", strings.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = res.Body.Close() }()
			if err := gate.Wait(ctx); err != nil {
				t.Fatal(err)
			}
			if last, ok := s.Last(); !ok || last.Path != "/api/chat" || !last.Stream {
				t.Fatalf("request at gate is missing from journal: %#v", last)
			}
			if mode == "cancel" {
				cancel()
				events, err := ollamaLines(t, res)
				if !errors.Is(err, context.Canceled) || len(events) != 0 {
					t.Fatalf("canceled gated stream delivered %#v, %v", events, err)
				}
				return
			}
			gate.Release()
			events, err := ollamaLines(t, res)
			if err != nil || len(events) != 2 || events[0]["message"].(map[string]any)["content"] != "after release" || events[1]["done"] != true {
				t.Fatalf("released reply = %#v, %v", events, err)
			}
		})
	}
}

func postOllama(t *testing.T, s *Server, path, body string) *http.Response {
	t.Helper()
	res, err := http.Post(s.BaseURL(Ollama)+path, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func ollamaJSON(t *testing.T, res *http.Response, status int, schema string) map[string]any {
	t.Helper()
	raw, err := io.ReadAll(res.Body)
	_ = res.Body.Close()
	media, _, _ := mime.ParseMediaType(res.Header.Get("Content-Type"))
	if err != nil || res.StatusCode != status || media != "application/json" {
		t.Fatalf("HTTP %d (%s), want %d JSON: %s; read error: %v", res.StatusCode, media, status, raw, err)
	}
	return checkOllamaJSON(t, schema, raw)
}

func ollamaLines(t *testing.T, res *http.Response) ([]map[string]any, error) {
	t.Helper()
	raw, err := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK || res.Header.Get("Content-Type") != "application/x-ndjson" {
		t.Fatalf("HTTP %d (%s), want 200 NDJSON: %s", res.StatusCode, res.Header.Get("Content-Type"), raw)
	}
	if len(raw) == 0 {
		return nil, err
	}
	raw, ok := bytes.CutSuffix(raw, []byte("\n"))
	if !ok || bytes.Contains(raw, []byte("\r")) {
		t.Fatalf("stream does not use LF-delimited JSON lines: %q", raw)
	}
	var events []map[string]any
	for _, line := range bytes.Split(raw, []byte("\n")) {
		// Error lines have a different native wire schema from chat frames.
		value, parseErr := decodeJSON(line)
		if parseErr != nil {
			t.Fatalf("decode line %q: %v", line, parseErr)
		}
		obj := value.(map[string]any)
		if _, fault := obj["error"]; fault {
			checkOllamaJSON(t, "ErrorResponse", line)
		} else {
			checkOllamaJSON(t, "ChatStreamEvent", line)
			if obj["done"] == true {
				// The native server uses ChatResponse for terminal metrics,
				// which the pinned stream schema leaves as extra properties.
				checkOllamaJSON(t, "ChatResponse", line)
			}
		}
		events = append(events, obj)
	}
	return events, err
}

func checkOllamaJSON(t *testing.T, schema string, raw []byte) map[string]any {
	t.Helper()
	b, err := wireBundle(Ollama)
	if err != nil {
		t.Fatal(err)
	}
	value, err := decodeJSON(raw)
	if err != nil {
		t.Fatalf("invalid JSON %q: %v", raw, err)
	}
	if err := b.validate(schema, value); err != nil {
		t.Fatalf("%s: %v: %s", schema, err, raw)
	}
	return value.(map[string]any)
}
