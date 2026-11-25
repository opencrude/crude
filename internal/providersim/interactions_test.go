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
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const (
	testInteractionsPath = "/v1beta/interactions"
	testInteractModel    = "gemini-3.8-flash"
	testInteractInput    = `{"model":"` + testInteractModel + `","input":"Hello"}`
	testInteractStream   = `{"model":"` + testInteractModel + `","input":"Hello","stream":true}`
)

func interactJSON(t *testing.T, s *Server, body string) googleObject {
	t.Helper()
	return googleJSON(t, Interactions, "Interaction", googlePOST(t, s, Interactions, testInteractionsPath, body), 200)
}

func interactEvents(t *testing.T, resBody string, s *Server) ([]googleEvent, bool, error) {
	t.Helper()
	return googleEvents(t, Interactions, googlePOST(t, s, Interactions, testInteractionsPath, resBody))
}

func interactNames(events []googleEvent) string {
	var names []string
	for _, event := range events {
		names = append(names, event.name)
	}
	return strings.Join(names, " ")
}

func TestInteractionsStreamStoreAndContinue(t *testing.T) {
	s := New(t)
	call := ToolCall{ID: "call_1", Name: "lookup", Arguments: `{"key":"a"}`}
	s.Script(Text("Visible").WithReasoning("Plan", "c2ln", "").WithTools(call).WithUsage(Usage{Input: 120, Output: 24, Cached: 17, Reasoning: 5}), Text("found").WithFinish(FinishLength), Text("third").WithFinish(FinishFiltered))
	request := `{
		"model":"gemini-3.8-flash", "stream":true, "system_instruction":"First only",
		"input":[{"type":"text","text":"Find it"},{"type":"image","uri":"https://example.test/id","mime_type":"image/png"}],
		"tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}}],
		"generation_config":{"thinking_level":"high","thinking_summaries":"auto","max_output_tokens":32,"tool_choice":"auto"}
	}`
	events, done, err := interactEvents(t, request, s)
	want := "interaction.created step.start step.delta step.delta step.stop step.start step.delta step.stop step.start step.delta step.stop interaction.completed"
	if err != nil || !done || interactNames(events) != want {
		t.Fatal(interactNames(events), done, err)
	}
	created := googleObjectAt(events[0].body, "interaction")
	completed := googleObjectAt(events[11].body, "interaction")
	id := created["id"].(string)
	if created["status"] != "in_progress" || completed["status"] != "requires_action" || completed["id"] != id || completed["steps"] != nil || googleObjectAt(events[2].body, "delta")["type"] != "thought_summary" || googleObjectAt(events[3].body, "delta")["signature"] != "c2ln" || googleObjectAt(events[6].body, "delta")["text"] != "Visible" || googleObjectAt(events[8].body, "step")["id"] != call.ID || googleObjectAt(events[9].body, "delta")["arguments"] != call.Arguments {
		t.Fatal(events)
	}
	u := googleObjectAt(completed, "usage")
	if u["total_input_tokens"] != json.Number("120") || u["total_cached_tokens"] != json.Number("17") || u["total_output_tokens"] != json.Number("19") || u["total_thought_tokens"] != json.Number("5") || u["total_tokens"] != json.Number("144") {
		t.Fatal(u)
	}
	req, _ := s.Last()
	if req.Model != testInteractModel || !req.Stream || req.Store != nil || req.OutputLimit != "32" || len(req.System) != 1 || req.System[0].Text != "First only" || req.Tools[0].Name != call.Name || req.Messages[0].Parts[1].ImageURL != "https://example.test/id" || string(req.ToolChoice) != `"auto"` || string(req.Reasoning) != `{"thinking_level":"high","thinking_summaries":"auto"}` {
		t.Fatalf("wrong journal: %#v", req)
	}
	path := s.BaseURL(Interactions) + testInteractionsPath + "/" + id
	row := googleJSON(t, Interactions, "Interaction", get(t, path), 200)
	steps := googleArray(row["steps"])
	if len(steps) != 4 || steps[0].(googleObject)["type"] != "user_input" || steps[1].(googleObject)["signature"] != "c2ln" || steps[3].(googleObject)["id"] != call.ID || row["status"] != "requires_action" {
		t.Fatal(row)
	}
	resumed, replayDone, err := googleEvents(t, Interactions, get(t, path+"?stream=true&last_event_id="+events[7].body["event_id"].(string)))
	if err != nil || !replayDone || !reflect.DeepEqual(resumed, events[8:]) {
		t.Fatal(resumed, replayDone, err)
	}
	second := interactJSON(t, s, fmt.Sprintf(`{"model":%q,"previous_interaction_id":%q,"input":[{"type":"function_result","call_id":"call_1","result":"result"}]}`, testInteractModel, id))
	if second["previous_interaction_id"] != id || second["status"] != "incomplete" || len(googleArray(second["steps"])) != 1 {
		t.Fatal(second)
	}
	req, _ = s.Last()
	m := req.Effective.Messages
	if req.PreviousResponseID != id || len(req.Messages) != 1 || len(m) != 5 || len(req.Effective.System) != 0 || len(req.Effective.Tools) != 0 || m[0].Parts[0].Text != "Find it" || m[1].Parts[0].Signature != "c2ln" || m[3].Parts[0].Call != call || m[4].Parts[0].CallID != call.ID || m[4].Parts[0].Text != "result" {
		t.Fatalf("wrong effective conversation: %#v", req)
	}
	third := interactJSON(t, s, fmt.Sprintf(`{"model":%q,"previous_interaction_id":%q,"input":"next","store":false}`, testInteractModel, second["id"]))
	if third["status"] != "failed" || googleArray(third["errors"])[0].(googleObject)["code"] != "safety" {
		t.Fatal(third)
	}
	req, _ = s.Last()
	if len(req.Effective.Messages) != 7 || req.Effective.Messages[5].Parts[0].Text != "found" || req.Effective.Messages[6].Parts[0].Text != "next" {
		t.Fatal(req.Effective)
	}
	missing := googleJSON(t, Interactions, "ErrorResponse", get(t, s.BaseURL(Interactions)+testInteractionsPath+"/"+third["id"].(string)), 404)
	if googleObjectAt(missing, "error")["code"] != "not_found" {
		t.Fatal(missing)
	}
}

func TestInteractionsStatelessReplayAndCache(t *testing.T) {
	var reports atomic.Int32
	s := newServer(t, func(string, ...any) { reports.Add(1) }, []Option{WithCacheProfile(Interactions, testInteractModel, CacheProfile{Minimum: 1, Granularity: 1})})
	s.Script(Text("visible").WithReasoning("Think", "opaque", ""), Text("again"), Text("appended"))
	firstInput := `{"model":"` + testInteractModel + `","input":"Hello","store":false}`
	first := interactJSON(t, s, firstInput)
	initial, _ := s.Last()
	if initial.Prefix.CachedTokens != 0 || initial.Prefix.CacheWriteTokens != initial.Prefix.InputTokens || initial.Prefix.InputTokens <= 0 || googleObjectAt(first, "usage")["total_cached_tokens"] != json.Number("0") {
		t.Fatal(first, initial.Prefix)
	}
	second := interactJSON(t, s, firstInput)
	again, _ := s.Last()
	if googleObjectAt(second, "usage")["total_cached_tokens"] != json.Number(fmt.Sprint(initial.Prefix.InputTokens)) || again.Prefix.CachedTokens != initial.Prefix.InputTokens || again.Prefix.CacheWriteTokens != 0 {
		t.Fatal(second, again.Prefix)
	}
	for _, unknown := range []string{first["id"].(string), "v1_missing"} {
		body := fmt.Sprintf(`{"model":%q,"previous_interaction_id":%q,"input":"x"}`, testInteractModel, unknown)
		e := googleJSON(t, Interactions, "ErrorResponse", googlePOST(t, s, Interactions, testInteractionsPath, body), 404)
		if detail := googleObjectAt(e, "error"); detail["code"] != "not_found" || !strings.Contains(googleString(detail, "message"), "/previous_interaction_id") {
			t.Fatal(e)
		}
	}
	user := googleObject{"type": "user_input", "content": []any{googleObject{"type": "text", "text": "Hello"}}}
	steps := googleArray(first["steps"])
	thought := steps[0].(googleObject)
	if thought["type"] != "thought" || thought["signature"] != "opaque" || steps[1].(googleObject)["type"] != "model_output" {
		t.Fatal(steps)
	}
	altered := maps.Clone(thought)
	altered["signature"] = "changed"
	next := googleObject{"type": "user_input", "content": []any{googleObject{"type": "text", "text": "More"}}}
	input := []any{user, altered, steps[1], next}
	body := googleObject{"model": testInteractModel, "input": input, "store": false}
	e := googleJSON(t, Interactions, "ErrorResponse", googlePOST(t, s, Interactions, testInteractionsPath, string(jsonBytes(body))), 400)
	if detail := googleObjectAt(e, "error"); detail["code"] != "invalid_request" || !strings.Contains(googleString(detail, "message"), "/input/1") {
		t.Fatal(e)
	}
	input[1] = thought
	appended := interactJSON(t, s, string(jsonBytes(body)))
	req, _ := s.Last()
	if reports.Load() != 3 || !strings.Contains(string(jsonBytes(appended)), `"text":"appended"`) || len(req.Effective.Messages) != 4 || req.Effective.Messages[1].Parts[0].Signature != "opaque" || req.Prefix.CachedTokens != initial.Prefix.InputTokens || req.Prefix.CacheWriteTokens != req.Prefix.InputTokens-initial.Prefix.InputTokens || req.Prefix.FirstDifference != "/input/1" || googleObjectAt(appended, "usage")["total_cached_tokens"] != googleObjectAt(second, "usage")["total_cached_tokens"] {
		t.Fatal(appended, req, reports.Load())
	}
}

func TestInteractionsStrictRequestsAndExpiration(t *testing.T) {
	var reports atomic.Int32
	s := newServer(t, func(string, ...any) { reports.Add(1) }, nil)
	state := newInteractionWire(s)
	state.stored["v1_expired"] = storedInteraction{expires: time.Unix(1, 0)}
	state.stored["v1_expired_parent"] = storedInteraction{expires: time.Unix(1, 0)}
	hs := httptest.NewServer(state.routes())
	t.Cleanup(hs.Close)
	for _, tc := range []struct{ payload, code, path string }{
		{`{"model":"gemini-3.8-flash","input":"x","extra":true}`, "parameter_unknown", "/extra"},
		{`{"agent":"test-agent","input":"x","generation_config":{}}`, "parameter_unknown", "/generation_config"},
		{`{"model":"gemini-3.8-flash","input":"x","generation_config":{"thinkingLevel":"HIGH"}}`, "parameter_unknown", "/generation_config/thinkingLevel"},
		{`{"model":"gemini-3.8-flash","input":"x","generation_config":{"thinking_level":"HIGH"}}`, "invalid_request", "thinking_level"},
		{`{"model":"gemini-3.8-flash","input":true}`, "invalid_request", "input"},
		{`{"model":"gemini-3.8-flash","agent":"test-agent","input":"x"}`, "invalid_request", "/model"},
		{`{"model":"gemini-3.8-flash","input":"x","background":true,"store":false}`, "invalid_request", "/store"},
	} {
		e := googleJSON(t, Interactions, "ErrorResponse", googlePOST(t, s, Interactions, testInteractionsPath, tc.payload), 400)
		detail := googleObjectAt(e, "error")
		last, _ := s.Last()
		if detail["code"] != tc.code || !strings.Contains(googleString(detail, "message"), tc.path) || !strings.Contains(last.Violation, tc.path) {
			t.Fatal(e, last.Violation)
		}
	}
	e := googleJSON(t, Interactions, "ErrorResponse", get(t, hs.URL+testInteractionsPath+"/v1_expired"), 404)
	if googleObjectAt(e, "error")["code"] != "not_found" || reports.Load() != 7 {
		t.Fatal(e, reports.Load())
	}
	replay := strings.NewReader(`{"model":"gemini-3.8-flash","input":"next","previous_interaction_id":"v1_expired_parent"}`)
	r, err := http.NewRequestWithContext(t.Context(), http.MethodPost, hs.URL+testInteractionsPath, replay)
	if err != nil {
		t.Fatal(err)
	}
	e = googleJSON(t, Interactions, "ErrorResponse", do(t, r), 404)
	if detail := googleObjectAt(e, "error"); detail["code"] != "not_found" || !strings.Contains(googleString(detail, "message"), "/previous_interaction_id") || reports.Load() != 8 {
		t.Fatal(e, reports.Load())
	}
	s.Script(Text("from agent"))
	body := interactJSON(t, s, `{"agent":"test-agent","input":"hello"}`)
	last, _ := s.Last()
	if body["agent"] != "test-agent" || body["model"] != nil || last.Model != "test-agent" || !strings.Contains(string(jsonBytes(body)), `"text":"from agent"`) {
		t.Fatal(body, last)
	}
}

func TestInteractionsFaultsAndGate(t *testing.T) {
	s := New(t)
	for _, tc := range []struct {
		turn            Turn
		status          int
		code, key, hint string
	}{
		{HTTPError(RateLimit), 429, "rate_limit_exceeded", "Retry-After", ""},
		{HTTPError(RateLimit).WithRetryHint(RetryAfterSeconds(2)), 429, "rate_limit_exceeded", "Retry-After", "2"},
		{HTTPError(RateLimit).WithRetryHint(RetryAfterMilliseconds(90)), 429, "rate_limit_exceeded", "retry-after-ms", "90"},
		{HTTPError(RateLimit).WithRetryHint(RetryAfterDate(time.Unix(0, 0))), 429, "rate_limit_exceeded", "Retry-After", "Thu, 01 Jan 1970 00:00:00 GMT"},
		{HTTPError(ContextOverflow), 400, "invalid_request", "Retry-After", ""},
		{HTTPError(Auth), 401, "authentication", "Retry-After", ""},
		{HTTPError(Overload), 503, "service_unavailable", "Retry-After", ""},
	} {
		s.Script(tc.turn)
		res := googlePOST(t, s, Interactions, testInteractionsPath, testInteractInput)
		e := googleJSON(t, Interactions, "ErrorResponse", res, tc.status)
		if googleObjectAt(e, "error")["code"] != tc.code || res.Header.Get(tc.key) != tc.hint {
			t.Fatal(e, res.Header)
		}
	}
	s.Script(Text("partial").WithStreamError(3, RateLimit), Text("cut").WithCut(3))
	events, done, err := interactEvents(t, testInteractStream, s)
	if err != nil || done || interactNames(events) != "interaction.created step.start step.delta error" || googleObjectAt(events[2].body, "delta")["text"] != "partial" || googleObjectAt(events[3].body, "error")["code"] != "rate_limit_exceeded" {
		t.Fatal(events, done, err)
	}
	id := googleObjectAt(events[0].body, "interaction")["id"].(string)
	googleJSON(t, Interactions, "ErrorResponse", get(t, s.BaseURL(Interactions)+testInteractionsPath+"/"+id), 404)
	events, done, err = interactEvents(t, testInteractStream, s)
	if !errors.Is(err, io.ErrUnexpectedEOF) || done || interactNames(events) != "interaction.created step.start step.delta" || googleObjectAt(events[2].body, "delta")["text"] != "cut" {
		t.Fatal(events, done, err)
	}
	id = googleObjectAt(events[0].body, "interaction")["id"].(string)
	googleJSON(t, Interactions, "ErrorResponse", get(t, s.BaseURL(Interactions)+testInteractionsPath+"/"+id), 404)
	gate := NewGate()
	defer gate.Release()
	s.Script(Text("released").WithGate(gate), Text("concurrent"))
	res := googlePOST(t, s, Interactions, testInteractionsPath, testInteractStream)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := gate.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	if body := interactJSON(t, s, testInteractInput); !strings.Contains(string(jsonBytes(body)), `"text":"concurrent"`) {
		t.Fatal(body)
	}
	gate.Release()
	events, done, err = googleEvents(t, Interactions, res)
	if err != nil || !done || interactNames(events) != "interaction.created step.start step.delta step.stop interaction.completed" || googleObjectAt(events[2].body, "delta")["text"] != "released" {
		t.Fatal(events, done, err)
	}
}
