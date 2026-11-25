package providersim

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func postResponses(t *testing.T, s *Server, wire Wire, path string, body any) *http.Response {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	r, err := http.NewRequestWithContext(t.Context(), "POST", s.BaseURL(wire)+path, strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Content-Type", "application/json")
	return do(t, r)
}

func checkedResponse(t *testing.T, component string, raw []byte) responseObject {
	t.Helper()
	b, err := wireBundle(Responses)
	if err != nil {
		t.Fatal(err)
	}
	v, err := decodeJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err = b.validate(component, v); err != nil {
		t.Fatalf("invalid %s: %v; body %.1300s", component, err, raw)
	}
	return v.(responseObject)
}

func responseJSON(t *testing.T, res *http.Response, status int, schema string) responseObject {
	t.Helper()
	raw := readResponse(t, res)
	if res.StatusCode != status || res.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("HTTP %s: %.1300s", res.Status, raw)
	}
	return checkedResponse(t, schema, raw)
}

func responseEvents(t *testing.T, res *http.Response, codex bool) ([]responseObject, error) {
	t.Helper()
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("not an SSE response: %s: %.1300s", res.Status, readResponse(t, res))
	}
	raw, err := io.ReadAll(res.Body)
	var events []responseObject
	for _, f := range strings.Split(strings.TrimSuffix(string(raw), "\n\n"), "\n\n") {
		if f == "" {
			continue
		}
		tag, p, ok := strings.Cut(f, "\ndata: ")
		if !ok || !strings.HasPrefix(tag, "event: ") {
			t.Fatalf("bad SSE frame %.180s", f)
		}
		name := strings.TrimPrefix(tag, "event: ")
		var e responseObject
		if codex && name == "response.failed" { // Private error codes are not members of public ResponseErrorCode.
			v, parseErr := decodeJSON([]byte(p))
			if parseErr != nil {
				t.Fatal(parseErr)
			}
			e = v.(responseObject)
			resp := e["response"].(responseObject)
			detail := resp["error"].(responseObject)
			if resp["status"] != "failed" || detail["code"] == nil || detail["message"] == nil {
				t.Fatalf("invalid codex failure: %v", resp)
			}
		} else {
			e = checkedResponse(t, "ResponseStreamEvent", []byte(p))
		}
		if codex && (name == "response.output_item.done" || name == "response.output_item.added") {
			b, err := wireBundle(Codex)
			if err != nil {
				t.Fatal(err)
			}
			if err = b.validate("ResponseItem", e["item"]); err != nil {
				t.Fatalf("invalid Codex item in %s: %v", name, err)
			}
		}
		if e["type"] != name || e["sequence_number"] != json.Number(strconv.Itoa(len(events))) {
			t.Fatalf("invalid event type or sequence: %v", e)
		}
		events = append(events, e)
	}
	return events, err
}

func completeEvents(t *testing.T, res *http.Response, codex bool) []responseObject {
	t.Helper()
	events, err := responseEvents(t, res, codex)
	if err != nil || len(events) == 0 {
		t.Fatalf("no complete stream: %v", err)
	}
	return events
}

func lastResponse(events []responseObject) responseObject {
	return events[len(events)-1]["response"].(responseObject)
}

func eventNames(events []responseObject) string {
	var names []string
	for _, e := range events {
		names = append(names, strings.TrimPrefix(e["type"].(string), "response."))
	}
	return strings.Join(names, " ")
}

func testInput(input any) responseObject { return responseObject{"model": "gpt-4.1", "input": input} }

func TestResponsesRichStreamAndReplay(t *testing.T) {
	s := New(t)
	call := ToolCall{ID: "call_1", Name: "lookup", Arguments: `{"key":"a"}`}
	s.Script(Text("hello").WithReasoning("decide", "", "opaque").WithRefusal("no").WithTools(call).WithUsage(Usage{Input: 5000, Cached: 3000, CacheWrite: 1234, Output: 20, Reasoning: 10}), Text("end").WithFinish(FinishLength))
	body := testInput([]any{responseObject{"type": "message", "role": "developer", "content": []any{responseObject{"type": "input_text", "text": "Plan.", "prompt_cache_breakpoint": responseObject{"mode": "explicit"}}}}, responseObject{"role": "user", "content": []any{responseObject{"type": "input_image", "image_url": "https://example.test/i", "detail": "auto"}, responseObject{"type": "input_text", "text": "Find it"}}}})
	body["stream"] = true
	body["reasoning"] = responseObject{"summary": "auto"}
	body["include"] = []string{"reasoning.encrypted_content"}
	body["prompt_cache_key"] = "cache1"
	body["tools"] = []any{responseObject{"type": "function", "name": "lookup", "description": "Look up", "parameters": responseObject{"type": "object"}, "strict": false}}
	body["tool_choice"] = "auto"
	body["max_output_tokens"] = 32
	events := completeEvents(t, postResponses(t, s, Responses, "/responses", body), false)
	want := "created in_progress output_item.added reasoning_summary_part.added reasoning_summary_text.delta reasoning_summary_text.done reasoning_summary_part.done output_item.done output_item.added content_part.added output_text.delta output_text.done content_part.done content_part.added refusal.delta refusal.done content_part.done output_item.done output_item.added function_call_arguments.delta function_call_arguments.done output_item.done completed"
	if got := eventNames(events); got != want {
		t.Fatalf("events: %s", got)
	}
	final := lastResponse(events)
	out := final["output"].([]any)
	if !reflect.DeepEqual(events[7]["item"], out[0]) || !reflect.DeepEqual(events[17]["item"], out[1]) || !reflect.DeepEqual(events[21]["item"], out[2]) || events[19]["delta"] != call.Arguments || out[0].(responseObject)["encrypted_content"] != "opaque" {
		t.Fatal("final output did not match streamed parts")
	}
	usage := final["usage"].(responseObject)
	if fmt.Sprint(usage["total_tokens"]) != "5020" || fmt.Sprint(usage["input_tokens_details"].(responseObject)["cache_write_tokens"]) != "1234" {
		t.Fatal(usage)
	}
	req, _ := s.Last()
	if req.OutputLimit != "32" || string(req.ToolChoice) != `"auto"` || req.Tools[0].Name != "lookup" || req.System[0].Text != "Plan." || req.Messages[0].Parts[0].ImageURL != "https://example.test/i" || len(req.CacheMarkers) != 2 || req.Store != nil {
		t.Fatalf("wrong journal projection: %#v", req)
	}
	replay := testInput(append(out, responseObject{"type": "function_call_output", "call_id": "call_1", "output": "found"}))
	replay["stream"] = true
	events = completeEvents(t, postResponses(t, s, Responses, "/responses", replay), false)
	if f := lastResponse(events); f["status"] != "incomplete" || f["incomplete_details"].(responseObject)["reason"] != "max_output_tokens" {
		t.Fatal(f)
	}
	req, _ = s.Last()
	if p := req.Messages[0].Parts[0]; p.Encrypted != "opaque" || p.Kind != PartReasoning || p.Text != "decide" {
		t.Fatal(p)
	}
	if req.Messages[2].Parts[0].Call != call || req.Messages[3].Parts[0].Text != "found" {
		t.Fatal(req.Messages)
	}
}

func TestResponsesStoredChainAndInventory(t *testing.T) {
	s := newServer(t, func(string, ...any) {}, []Option{WithModels(Responses, "custom"), WithCacheProfile(Responses, "gpt-4.1", CacheProfile{Minimum: 1, Granularity: 1})})
	models := responseJSON(t, get(t, s.BaseURL(Responses)+"/models"), 200, "ListModelsResponse")
	if models["data"].([]any)[0].(responseObject)["id"] != "custom" {
		t.Fatal(models)
	}
	s.Script(Text("remembered"), Reasoning("private reason", "", "cipher"), Text("ok"))
	first := testInput("remember me")
	first["instructions"] = "old"
	saved := responseJSON(t, postResponses(t, s, Responses, "/responses", first), 200, "Response")
	firstUsage := saved["usage"].(responseObject)
	if d := firstUsage["input_tokens_details"].(responseObject); d["cache_write_tokens"] != firstUsage["input_tokens"] || d["cached_tokens"] != json.Number("0") {
		t.Fatalf("first write: %v", d)
	}
	id := saved["id"].(string)
	if got := responseJSON(t, get(t, s.BaseURL(Responses)+"/responses/"+id), 200, "Response"); !reflect.DeepEqual(got, saved) {
		t.Fatal("retrieval mismatch")
	}
	next := testInput("recall")
	next["previous_response_id"] = id
	next["store"] = false
	next["stream"] = true
	ev := completeEvents(t, postResponses(t, s, Responses, "/responses", next), false)
	if !strings.Contains(eventNames(ev), "reasoning_text.delta reasoning_text.done") {
		t.Fatal(eventNames(ev))
	}
	req, _ := s.Last()
	if len(req.Effective.System) != 0 || len(req.Effective.Messages) != 3 || req.Effective.Messages[0].Parts[0].Text != "remember me" || req.Effective.Messages[1].Parts[0].Text != "remembered" || len(req.Messages) != 1 || req.PreviousResponseID != id || req.Store == nil || *req.Store {
		t.Fatal(req.Effective)
	}
	unsaved := lastResponse(ev)["id"].(string)
	responseJSON(t, get(t, s.BaseURL(Responses)+"/responses/"+unsaved), 404, "ErrorResponse")
	for _, unknown := range []string{unsaved, "resp_unknown"} {
		next["previous_response_id"] = unknown
		e := responseJSON(t, postResponses(t, s, Responses, "/responses", next), 400, "ErrorResponse")["error"].(responseObject)
		if e["code"] != "previous_response_not_found" || e["param"] != "previous_response_id" {
			t.Fatal(e)
		}
	}
	cached := responseJSON(t, postResponses(t, s, Responses, "/responses", first), 200, "Response")["usage"].(responseObject)
	if data := cached["input_tokens_details"].(responseObject); data["cached_tokens"] != cached["input_tokens"] || data["cache_write_tokens"] != json.Number("0") {
		t.Fatal(cached)
	}
}

func TestResponsesStandaloneCompaction(t *testing.T) {
	s := newServer(t, func(string, ...any) {}, nil)
	s.Script(Turn{}, Text("ok"))
	body := testInput([]any{responseObject{"role": "user", "content": "blue key"}, responseObject{"role": "assistant", "content": "seven bells"}, responseObject{"role": "user", "content": "remain outside"}})
	compacted := responseJSON(t, postResponses(t, s, Responses, "/responses/compact", body), 200, "CompactResource")
	output := compacted["output"].([]any)
	if len(output) != 2 {
		t.Fatal(output)
	}
	item := output[0].(responseObject)
	if item["type"] != "compaction" || strings.Contains(item["encrypted_content"].(string), "blue key") {
		t.Fatal(item)
	}
	replay := testInput(output)
	responseJSON(t, postResponses(t, s, Responses, "/responses", replay), 200, "Response")
	req, _ := s.Last()
	summary := req.Effective.Messages[0].Parts[0].Text
	if len(req.Effective.Messages) != 2 || !strings.Contains(summary, "blue key") || !strings.Contains(summary, "seven bells") || strings.Contains(summary, "remain outside") || req.Effective.Messages[1].Parts[0].Text != "remain outside" {
		t.Fatal(req.Effective)
	}
	other := newServer(t, func(string, ...any) {}, nil)
	foreign := responseJSON(t, postResponses(t, other, Responses, "/responses", replay), 400, "ErrorResponse")
	if foreign["error"].(responseObject)["code"] != "invalid_encrypted_content" {
		t.Fatal(foreign)
	}
	for _, field := range []string{"id", "encrypted_content", "arbitrary", "type"} {
		altered := maps.Clone(item)
		altered[field] = "different"
		if field == "type" {
			altered[field] = "item_reference"
		}
		bad := responseJSON(t, postResponses(t, s, Responses, "/responses", testInput([]any{altered})), 400, "ErrorResponse")
		if bad["error"].(responseObject)["code"] != "invalid_encrypted_content" {
			t.Fatal(bad)
		}
	}
}

func TestResponsesThresholdAndPrefix(t *testing.T) {
	s := New(t)
	s.Script(Text("answer"), Text("ok"), Text("stored"), Text("no compaction"))
	input := strings.Repeat("once only ", 550)
	body := testInput(input)
	body["stream"] = true
	body["context_management"] = []any{responseObject{"type": "compaction", "compact_threshold": 1000}}
	ev := completeEvents(t, postResponses(t, s, Responses, "/responses", body), false)
	if !strings.HasPrefix(eventNames(ev), "created in_progress output_item.added compaction.compacting output_item.done output_item.added content_part.added output_text.delta") {
		t.Fatal(eventNames(ev))
	}
	final := lastResponse(ev)
	output := final["output"].([]any)
	if len(output) != 2 || output[0].(responseObject)["type"] != "compaction" {
		t.Fatal(output)
	}
	replay := append([]any{responseObject{"role": "user", "content": input}}, output...)
	replay = append(replay, responseObject{"role": "user", "content": "next"})
	responseJSON(t, postResponses(t, s, Responses, "/responses", testInput(replay)), 200, "Response")
	req, _ := s.Last()
	if len(req.Effective.Messages) != 3 || strings.Count(req.Effective.Messages[0].Parts[0].Text, input) != 1 || req.Effective.Messages[1].Parts[0].Text != "answer" || req.Prefix.InputTokens >= 1000 {
		t.Fatal("replay duplicated or lost compacted history")
	}
	chain := testInput("next")
	chain["previous_response_id"] = final["id"]
	responseJSON(t, postResponses(t, s, Responses, "/responses", chain), 200, "Response")
	stored, _ := s.Last()
	if !reflect.DeepEqual(req.Effective, stored.Effective) {
		t.Fatal("stored and stateless windows differ")
	}
	body["input"] = "small"
	ev = completeEvents(t, postResponses(t, s, Responses, "/responses", body), false)
	if strings.Contains(eventNames(ev), "compacting") {
		t.Fatal("compacted below threshold")
	}
}

func TestResponsesStrictRequests(t *testing.T) {
	var reports []string
	s := newServer(t, func(msg string, args ...any) { reports = append(reports, fmt.Sprintf(msg, args...)) }, nil)
	s.Script(Text("still next"))
	for _, tc := range []struct {
		route string
		body  responseObject
		path  string
	}{{"/responses", responseObject{"model": "gpt-4.1", "input": 12}, "/input"}, {"/responses", responseObject{"model": "gpt-4.1", "unknown": 1}, "/unknown"}, {"/responses", responseObject{"input": "hi"}, "/model"}, {"/responses/compact", responseObject{"model": "gpt-4.1", "store": true}, "/store"}, {"/responses/compact", responseObject{"input": "hi"}, "model"}} {
		responseJSON(t, postResponses(t, s, Responses, tc.route, tc.body), 400, "ErrorResponse")
		last, _ := s.Last()
		if !strings.Contains(last.Violation, tc.path) {
			t.Fatalf("missing %s: %s", tc.path, last.Violation)
		}
	}
	if len(reports) != 5 {
		t.Fatal(reports)
	}
	got := responseJSON(t, postResponses(t, s, Responses, "/responses", testInput("ok")), 200, "Response")
	if !strings.Contains(string(jsonBytes(got["output"])), "still next") {
		t.Fatal(got)
	}
}

func TestResponsesHTTPFaults(t *testing.T) {
	s := New(t)
	for _, tc := range []struct {
		turn                Turn
		status              int
		code, header, value string
	}{{HTTPError(RateLimit), 429, "rate_limit_exceeded", "", ""}, {HTTPError(RateLimit).WithRetryHint(RetryAfterSeconds(2)), 429, "rate_limit_exceeded", "Retry-After", "2"}, {HTTPError(RateLimit).WithRetryHint(RetryAfterMilliseconds(90)), 429, "rate_limit_exceeded", "retry-after-ms", "90"}, {HTTPError(RateLimit).WithRetryHint(RetryAfterDate(time.Unix(0, 0))), 429, "rate_limit_exceeded", "Retry-After", "Thu, 01 Jan 1970 00:00:00 GMT"}, {HTTPError(ContextOverflow), 400, "context_length_exceeded", "", ""}, {HTTPError(Overload), 503, "server_is_overloaded", "", ""}, {HTTPError(Auth), 401, "invalid_api_key", "", ""}} {
		for _, wire := range []Wire{Responses, Codex} {
			body := testInput("ok")
			if wire == Codex {
				body = codexInput(responseObject{"type": "message", "role": "user", "content": []any{responseObject{"type": "input_text", "text": "ok"}}})
			}
			s.Script(tc.turn)
			res := postResponses(t, s, wire, "/responses", body)
			e := responseJSON(t, res, tc.status, "ErrorResponse")["error"].(responseObject)
			if e["code"] != tc.code || res.Header.Get(tc.header) != tc.value {
				t.Fatal(e, res.Header)
			}
		}
	}
}

func TestResponsesStreamFaultsAndGate(t *testing.T) {
	s := New(t)
	body := testInput("hi")
	body["stream"] = true
	for _, tc := range []struct {
		kind        FaultKind
		event, code string
	}{{ContextOverflow, "error", "context_length_exceeded"}, {Auth, "error", "invalid_api_key"}, {Overload, "response.failed", "server_error"}, {RateLimit, "response.failed", "rate_limit_exceeded"}} {
		s.Script(Text("partial").WithStreamError(5, tc.kind))
		ev := completeEvents(t, postResponses(t, s, Responses, "/responses", body), false)
		if len(ev) != 6 || ev[4]["delta"] != "partial" || ev[5]["type"] != tc.event {
			t.Fatal(eventNames(ev))
		}
		detail := ev[5]
		if tc.event == "response.failed" {
			detail = lastResponse(ev)["error"].(responseObject)
		}
		if detail["code"] != tc.code {
			t.Fatal(detail)
		}
		id := ev[0]["response"].(responseObject)["id"].(string)
		responseJSON(t, get(t, s.BaseURL(Responses)+"/responses/"+id), 404, "ErrorResponse")
	}
	s.Script(Text("partial").WithCut(5))
	ev, err := responseEvents(t, postResponses(t, s, Responses, "/responses", body), false)
	if !errors.Is(err, io.ErrUnexpectedEOF) || len(ev) != 5 || ev[4]["delta"] != "partial" {
		t.Fatal(err, eventNames(ev))
	}
	gate := NewGate()
	s.Script(Text("released").WithGate(gate), Text("concurrent"))
	res := postResponses(t, s, Responses, "/responses", body)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := gate.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	defer gate.Release()
	concurrent := completeEvents(t, postResponses(t, s, Responses, "/responses", body), false)
	if concurrent[4]["delta"] != "concurrent" {
		t.Fatal(concurrent)
	}
	gate.Release()
	ev = completeEvents(t, res, false)
	if ev[4]["delta"] != "released" || !slices.Contains(strings.Fields(eventNames(ev)), "completed") {
		t.Fatal(ev)
	}
}

func TestResponsesExpiredIDCannotContinue(t *testing.T) {
	// Use an already-expired store record, not a sleep or a public clock knob.
	s := newServer(t, func(string, ...any) {}, nil)
	state := newResponseState(s, Responses)
	state.stored["resp_expired"] = storedResponse{body: responseObject{"id": "resp_expired"}, expires: time.Unix(1, 0)}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/responses", state.create)
	mux.HandleFunc("GET /v1/responses/{id}", state.retrieve)
	httpServer := httptest.NewServer(mux)
	t.Cleanup(httpServer.Close)
	request, err := http.NewRequestWithContext(t.Context(), "POST", httpServer.URL+"/v1/responses", strings.NewReader(`{"model":"gpt-4o","input":"new turn","previous_response_id":"resp_expired"}`))
	if err != nil {
		t.Fatal(err)
	}
	e := responseJSON(t, do(t, request), 400, "ErrorResponse")["error"].(responseObject)
	if e["code"] != "previous_response_not_found" {
		t.Fatal(e)
	}
	responseJSON(t, get(t, httpServer.URL+"/v1/responses/resp_expired"), 404, "ErrorResponse")
}
