package providersim

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func googlePOST(t *testing.T, s *Server, wire Wire, path, body string) *http.Response {
	t.Helper()
	r, err := http.NewRequestWithContext(t.Context(), http.MethodPost, mountURL(s, wire)+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Content-Type", "application/json")
	return do(t, r)
}

func googleChecked(t *testing.T, wire Wire, schema string, raw []byte) googleObject {
	t.Helper()
	b, err := wireBundle(wire)
	if err != nil {
		t.Fatal(err)
	}
	obj, err := googleRequestObject(raw)
	if err != nil {
		t.Fatalf("invalid JSON response: %v: %s", err, raw)
	}
	if err := b.validate(schema, obj); err != nil {
		t.Fatalf("invalid %s %s: %v: %s", wire, schema, err, raw)
	}
	return obj
}

func googleJSON(t *testing.T, wire Wire, schema string, res *http.Response, status int) googleObject {
	t.Helper()
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil || res.StatusCode != status || res.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("expected JSON HTTP %d; got %d (%v): %s", status, res.StatusCode, err, body)
	}
	return googleChecked(t, wire, schema, body)
}

type googleEvent struct {
	name string
	body googleObject
}

func googleEvents(t *testing.T, wire Wire, res *http.Response) (events []googleEvent, done bool, err error) {
	t.Helper()
	defer res.Body.Close()
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "text/event-stream" {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("expected an SSE stream; got %d: %s", res.StatusCode, body)
	}
	read := bufio.NewScanner(res.Body)
	var name string
	var hadData bool
	for read.Scan() {
		line := read.Text()
		if line == "" && hadData {
			name, hadData = "", false
			continue
		}
		if done || hadData {
			t.Fatalf("unexpected SSE data after frame: %q", line)
		}
		if event, ok := strings.CutPrefix(line, "event: "); ok && name == "" && wire == Interactions {
			name = event
			continue
		}
		raw, ok := strings.CutPrefix(line, "data: ")
		if !ok {
			t.Fatalf("unexpected SSE line: %q", line)
		}
		hadData = true
		if raw == "[DONE]" && wire == Interactions && name == "done" {
			done = true
			continue
		}
		if wire == Gemini {
			root := "GenerateContentResponse"
			obj, _ := googleRequestObject([]byte(raw))
			if obj["error"] != nil {
				root = "ErrorResponse"
			}
			events = append(events, googleEvent{body: googleChecked(t, wire, root, []byte(raw))})
		} else {
			obj := googleChecked(t, wire, "InteractionSSEEvent", []byte(raw))
			if obj["event_type"] != name {
				t.Fatalf("event name %q differs from body %v", name, obj)
			}
			events = append(events, googleEvent{name, obj})
		}
	}
	return events, done, read.Err()
}

const (
	testGeminiModel = "gemini-3.1-pro-preview"
	testGeminiBase  = "/v1beta/models/" + testGeminiModel
	testGeminiJSON  = testGeminiBase + ":generateContent"
	testGeminiSSE   = testGeminiBase + ":streamGenerateContent?alt=sse"
	testGeminiInput = `{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`
)

func TestGeminiStreamAndSignatureReplay(t *testing.T) {
	var rejected atomic.Int32
	s := newServer(t, func(string, ...any) { rejected.Add(1) }, nil)
	call := ToolCall{ID: "call_1", Name: "lookup", Arguments: `{"key":"a"}`}
	s.Script(Text("visible").WithTools(call, ToolCall{Name: "next"}).WithReasoning("decide", "c2ln", "").WithUsage(Usage{Input: 80, Output: 20, Cached: 40, Reasoning: 5}), Text("continue").WithFinish(FinishLength), Text("new turn").WithFinish(FinishFiltered))
	request := `{
		"contents":[{"role":"user","parts":[{"text":"Find it"},{"fileData":{"mimeType":"image/png","fileUri":"https://example.test/i"}}]}],
		"systemInstruction":{"parts":[{"text":"Plan."}]},
		"tools":[{"functionDeclarations":[{"name":"lookup","parametersJsonSchema":{"type":"object"}},{"name":"next"}]}],
		"toolConfig":{"functionCallingConfig":{"mode":"AUTO"}},
		"cachedContent":"cachedContents/local", "store":false,
		"generationConfig":{"maxOutputTokens":32,"thinkingConfig":{"thinkingLevel":"HIGH","includeThoughts":true}}
	}`
	events, done, err := googleEvents(t, Gemini, googlePOST(t, s, Gemini, testGeminiSSE, request))
	if err != nil || done || len(events) != 5 {
		t.Fatalf("expected four part chunks and final metadata: %v %v %v", events, done, err)
	}
	var parts []any
	for _, event := range events[:4] {
		candidate := googleArray(event.body["candidates"])[0].(googleObject)
		if candidate["finishReason"] != nil {
			t.Fatal(candidate)
		}
		parts = append(parts, googleArray(googleObjectAt(candidate, "content")["parts"])...)
	}
	thought, text, fc1, fc2 := parts[0].(googleObject), parts[1].(googleObject), parts[2].(googleObject), parts[3].(googleObject)
	if thought["thought"] != true || thought["text"] != "decide" || thought["thoughtSignature"] != nil || text["text"] != "visible" || fc1["thoughtSignature"] != "c2ln" || fc2["thoughtSignature"] != nil || googleObjectAt(fc1, "functionCall")["id"] != "call_1" || googleObjectAt(googleObjectAt(fc1, "functionCall"), "args")["key"] != "a" {
		t.Fatal(parts)
	}
	last := events[4].body
	usage := googleObjectAt(last, "usageMetadata")
	if googleArray(last["candidates"])[0].(googleObject)["finishReason"] != "STOP" || usage["promptTokenCount"] != json.Number("80") || usage["cachedContentTokenCount"] != json.Number("40") || usage["candidatesTokenCount"] != json.Number("15") || usage["thoughtsTokenCount"] != json.Number("5") || usage["totalTokenCount"] != json.Number("100") {
		t.Fatal(last)
	}
	req, _ := s.Last()
	if req.Model != testGeminiModel || !req.Stream || req.OutputLimit != "32" || req.Store == nil || *req.Store || len(req.CacheMarkers) != 1 || req.System[0].Text != "Plan." || len(req.Tools) != 2 || req.Tools[0].Name != "lookup" || req.Messages[0].Parts[1].ImageURL != "https://example.test/i" || string(req.ToolChoice) != `{"mode":"AUTO"}` || string(req.Reasoning) != `{"includeThoughts":true,"thinkingLevel":"HIGH"}` {
		t.Fatalf("wrong journal: %#v", req)
	}
	obj, err := googleRequestObject([]byte(request))
	if err != nil {
		t.Fatal(err)
	}
	result := googleObject{"role": "user", "parts": []any{googleObject{"functionResponse": googleObject{"id": "call_1", "name": "lookup", "response": googleObject{"result": "found"}}}}}
	model := googleObject{"role": "model", "parts": parts}
	window := []any{googleArray(obj["contents"])[0], model, result}
	obj["contents"] = window
	for _, signature := range []string{"", "dGFtcGVyZWQ="} {
		altered := maps.Clone(fc1)
		if signature == "" {
			delete(altered, "thoughtSignature")
		} else {
			altered["thoughtSignature"] = signature
		}
		model["parts"] = []any{thought, text, altered, fc2}
		failure := googleJSON(t, Gemini, "ErrorResponse", googlePOST(t, s, Gemini, testGeminiJSON, string(jsonBytes(obj))), 400)
		detail := googleObjectAt(failure, "error")
		if detail["status"] != "INVALID_ARGUMENT" || !strings.Contains(googleString(detail, "message"), "/contents/1/parts/2/thoughtSignature") {
			t.Fatal(failure)
		}
	}
	model["parts"] = parts
	body := googleJSON(t, Gemini, "GenerateContentResponse", googlePOST(t, s, Gemini, testGeminiJSON, string(jsonBytes(obj))), 200)
	candidate := googleArray(body["candidates"])[0].(googleObject)
	if candidate["finishReason"] != "MAX_TOKENS" || googleArray(googleObjectAt(candidate, "content")["parts"])[0].(googleObject)["text"] != "continue" {
		t.Fatal(body)
	}
	req, _ = s.Last()
	if req.Effective.Messages[1].Parts[0].Kind != PartReasoning || req.Effective.Messages[1].Parts[2].Call != call || req.Effective.Messages[1].Parts[2].Signature != "c2ln" || req.Effective.Messages[2].Parts[0].CallID != call.ID {
		t.Fatal(req.Effective)
	}
	altered := maps.Clone(fc1)
	delete(altered, "thoughtSignature")
	model["parts"] = []any{thought, text, altered, fc2}
	obj["contents"] = append(window, googleObject{"role": "user", "parts": []any{googleObject{"text": "a new turn"}}})
	body = googleJSON(t, Gemini, "GenerateContentResponse", googlePOST(t, s, Gemini, testGeminiJSON, string(jsonBytes(obj))), 200)
	if googleArray(body["candidates"])[0].(googleObject)["finishReason"] != "SAFETY" || rejected.Load() != 2 {
		t.Fatal(body, rejected.Load())
	}
}

func TestGeminiInventoryAndStrictRequests(t *testing.T) {
	var reports atomic.Int32
	s := newServer(t, func(string, ...any) { reports.Add(1) }, []Option{WithModels(Gemini, "custom")})
	root := mountURL(s, Gemini) + "/v1beta/models"
	listed := googleJSON(t, Gemini, "ListModelsResponse", get(t, root), 200)
	entry := googleArray(listed["models"])[0].(googleObject)
	model := googleJSON(t, Gemini, "Model", get(t, root+"/custom"), 200)
	for _, data := range []googleObject{entry, model} {
		if data["name"] != "models/custom" || data["inputTokenLimit"] != json.Number("1048576") || data["outputTokenLimit"] != json.Number("65536") {
			t.Fatal(data)
		}
	}
	if e := googleJSON(t, Gemini, "ErrorResponse", get(t, root+"/unknown"), 404); googleObjectAt(e, "error")["status"] != "NOT_FOUND" {
		t.Fatal(e)
	}
	s.Script(Text("still next"))
	for _, tc := range []struct{ payload, path string }{
		{`{"contents":[],"unknown":true}`, "/unknown"},
		{`{"contents":[]}`, "/contents"},
		{`{"contents":[{"parts":[{"text":"x"}]}],"generationConfig":{"thinking_config":{}}}`, "/generationConfig/thinking_config"},
		{`{"contents":[{"parts":[{"text":"x"}]}],"generationConfig":{"thinkingConfig":{"include_thoughts":true}}}`, "/generationConfig/thinkingConfig/include_thoughts"},
		{`{"contents":[{"parts":[{"text":"x"}]}],"generationConfig":{"thinkingConfig":{"thinkingLevel":"high"}}}`, "thinkingLevel"},
	} {
		body := googleJSON(t, Gemini, "ErrorResponse", googlePOST(t, s, Gemini, testGeminiJSON, tc.payload), 400)
		detail := googleObjectAt(body, "error")
		journal, _ := s.Last()
		if detail["status"] != "INVALID_ARGUMENT" || detail["code"] != json.Number("400") || !strings.Contains(googleString(detail, "message"), tc.path) || !strings.Contains(journal.Violation, tc.path) {
			t.Fatal(body, journal.Violation)
		}
	}
	body := googleJSON(t, Gemini, "GenerateContentResponse", googlePOST(t, s, Gemini, testGeminiJSON, testGeminiInput), 200)
	if reports.Load() != 5 || !strings.Contains(string(jsonBytes(body)), `"text":"still next"`) {
		t.Fatal(body, reports.Load())
	}
}

func TestGeminiSignatureRulesByModel(t *testing.T) {
	s := New(t)
	s.Script(Text("visible").WithReasoning("thinking", "", ""), CallTools(ToolCall{Name: "lookup"}), Text("result"))
	thinking := `{"contents":[{"parts":[{"text":"hello"}]}],"generationConfig":{"thinkingConfig":{"includeThoughts":true}}}`
	reply := googleJSON(t, Gemini, "GenerateContentResponse", googlePOST(t, s, Gemini, testGeminiJSON, thinking), 200)
	candidate := googleArray(reply["candidates"])[0].(googleObject)
	parts := googleArray(googleObjectAt(candidate, "content")["parts"])
	if len(parts) != 2 || parts[0].(googleObject)["thoughtSignature"] != nil || googleString(parts[1].(googleObject), "thoughtSignature") == "" || parts[1].(googleObject)["text"] != "visible" {
		t.Fatal(parts)
	}
	const flash = "/v1beta/models/gemini-2.5-flash:generateContent"
	reply = googleJSON(t, Gemini, "GenerateContentResponse", googlePOST(t, s, Gemini, flash, testGeminiInput), 200)
	candidate = googleArray(reply["candidates"])[0].(googleObject)
	model := googleObjectAt(candidate, "content")
	part := googleArray(model["parts"])[0].(googleObject)
	if part["thoughtSignature"] != nil || googleObjectAt(part, "functionCall")["name"] != "lookup" {
		t.Fatal(part)
	}
	input := `{"contents":[{"role":"user","parts":[{"text":"hello"}]},` + string(jsonBytes(model)) + `,{"role":"user","parts":[{"functionResponse":{"name":"lookup","response":{"result":"found"}}}]}]}`
	reply = googleJSON(t, Gemini, "GenerateContentResponse", googlePOST(t, s, Gemini, flash, input), 200)
	if !strings.Contains(string(jsonBytes(reply)), `"text":"result"`) {
		t.Fatal(reply)
	}
}

func TestGeminiImplicitCache(t *testing.T) {
	s := New(t, WithCacheProfile(Gemini, testGeminiModel, CacheProfile{Minimum: 1, Granularity: 1}))
	s.Respond(func(Request) Turn { return Text("ok") })
	appended := `{"contents":[{"role":"user","parts":[{"text":"hello"}]},{"role":"model","parts":[{"text":"ok"}]},{"role":"user","parts":[{"text":"continue"}]}]}`
	var first int
	for i, input := range []string{testGeminiInput, testGeminiInput, appended} {
		body := googleJSON(t, Gemini, "GenerateContentResponse", googlePOST(t, s, Gemini, testGeminiJSON, input), 200)
		u := googleObjectAt(body, "usageMetadata")
		req, _ := s.Last()
		want := first
		if i == 0 {
			first = req.Prefix.InputTokens
		}
		if req.Prefix.CachedTokens != want || req.Prefix.CacheWriteTokens != req.Prefix.InputTokens-want || u["cachedContentTokenCount"] != json.Number(fmt.Sprint(want)) || u["promptTokenCount"] != json.Number(fmt.Sprint(req.Prefix.InputTokens)) || first == 0 {
			t.Fatal(u, req.Prefix)
		}
		if i == 2 && (req.Prefix.FirstDifference != "/contents/1" || req.Prefix.PreviousIndex != 1 || req.Prefix.InputTokens <= first) {
			t.Fatal(req.Prefix)
		}
	}
	// Same 3,000-byte-equivalent prefix falls above the 2.5 threshold but
	// below the 3.1 threshold for a separate, default-profile simulator.
	other := New(t)
	other.Respond(func(Request) Turn { return Text("ok") })
	input := fmt.Sprintf(`{"contents":[{"parts":[{"text":%q}]}]}`, strings.Repeat("x", 12000))
	for _, tc := range []struct {
		model  string
		cached bool
	}{{"gemini-2.5-flash", true}, {testGeminiModel, false}} {
		path := "/v1beta/models/" + tc.model + ":generateContent"
		googleJSON(t, Gemini, "GenerateContentResponse", googlePOST(t, other, Gemini, path, input), 200)
		body := googleJSON(t, Gemini, "GenerateContentResponse", googlePOST(t, other, Gemini, path, input), 200)
		if got := googleObjectAt(body, "usageMetadata")["cachedContentTokenCount"] != json.Number("0"); got != tc.cached {
			t.Fatal(tc.model, body)
		}
	}
}

func TestGeminiFaultsAndGate(t *testing.T) {
	s := New(t)
	for _, tc := range []struct {
		turn              Turn
		status            int
		code, delay, date string
	}{
		{HTTPError(RateLimit), 429, "RESOURCE_EXHAUSTED", "", ""},
		{HTTPError(RateLimit).WithRetryHint(RetryAfterSeconds(2)), 429, "RESOURCE_EXHAUSTED", "2s", ""},
		{HTTPError(RateLimit).WithRetryHint(RetryAfterMilliseconds(1250)), 429, "RESOURCE_EXHAUSTED", "1.250s", ""},
		{HTTPError(RateLimit).WithRetryHint(RetryAfterDate(time.Unix(0, 0))), 429, "RESOURCE_EXHAUSTED", "", "Thu, 01 Jan 1970 00:00:00 GMT"},
		{HTTPError(Auth), 403, "PERMISSION_DENIED", "", ""},
		{HTTPError(ContextOverflow), 400, "INVALID_ARGUMENT", "", ""},
		{HTTPError(Overload), 503, "UNAVAILABLE", "", ""},
	} {
		s.Script(tc.turn)
		res := googlePOST(t, s, Gemini, testGeminiJSON, testGeminiInput)
		body := googleJSON(t, Gemini, "ErrorResponse", res, tc.status)
		detail := googleObjectAt(body, "error")
		if detail["status"] != tc.code || detail["code"] != json.Number(fmt.Sprint(tc.status)) || res.Header.Get("Retry-After") != tc.date || res.Header.Get("retry-after-ms") != "" {
			t.Fatal(body, res.Header)
		}
		if tc.delay != "" {
			retry := googleArray(detail["details"])[0].(googleObject)
			if retry["@type"] != "type.googleapis.com/google.rpc.RetryInfo" || retry["retryDelay"] != tc.delay {
				t.Fatal(retry)
			}
		} else if detail["details"] != nil {
			t.Fatal(detail)
		}
	}
	s.Script(Text("partial").WithStreamError(1, Overload), Text("cut").WithCut(1))
	events, done, err := googleEvents(t, Gemini, googlePOST(t, s, Gemini, testGeminiSSE, testGeminiInput))
	if err != nil || done || len(events) != 2 || !strings.Contains(string(jsonBytes(events[0].body)), `"text":"partial"`) || googleObjectAt(events[1].body, "error")["status"] != "UNAVAILABLE" {
		t.Fatal(events, done, err)
	}
	events, done, err = googleEvents(t, Gemini, googlePOST(t, s, Gemini, testGeminiSSE, testGeminiInput))
	if !errors.Is(err, io.ErrUnexpectedEOF) || done || len(events) != 1 || !strings.Contains(string(jsonBytes(events[0].body)), `"text":"cut"`) {
		t.Fatal(events, done, err)
	}
	gate := NewGate()
	defer gate.Release()
	s.Script(Text("released").WithGate(gate), Text("concurrent"))
	res := googlePOST(t, s, Gemini, testGeminiSSE, testGeminiInput)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := gate.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	body := googleJSON(t, Gemini, "GenerateContentResponse", googlePOST(t, s, Gemini, testGeminiJSON, testGeminiInput), 200)
	if !strings.Contains(string(jsonBytes(body)), `"text":"concurrent"`) {
		t.Fatal(body)
	}
	gate.Release()
	events, done, err = googleEvents(t, Gemini, res)
	if err != nil || done || len(events) != 2 || !strings.Contains(string(jsonBytes(events[0].body)), `"text":"released"`) {
		t.Fatal(events, done, err)
	}
}
