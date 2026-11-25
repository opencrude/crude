package providersim

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Documentation snapshots (official Markdown, fetched 2026-11-25, SHA-256).
// The generator pins request/event/model types separately in schemas/manifest.json.
// https://platform.claude.com/docs/en/api/messages/create.md 83f99375973542d278731f1f76d7e35459289e744c63d5930086b11d3c802f36
// https://platform.claude.com/docs/en/api/errors.md d529d3de38d3daeb123c3386d127bf4fe06d15b8341188e8ffb883da4ae1cb90
// https://platform.claude.com/docs/en/api/models/list.md 709ddaca24018c3a9a605d8583f4fe66aa83b5ebc0ee50730558d0e586f9e321
// https://platform.claude.com/docs/en/api/beta/models/list.md f4dde711eaa808bbe4376db26e48896ed20149870364330815d02fd0b4fa5cd2
// https://platform.claude.com/docs/en/build-with-claude/prompt-caching.md 00a0361bd0ec83b56f73cae233b56a0b292e52b60e0edf923a1bd2ba1f0ba26f
// https://platform.claude.com/docs/en/build-with-claude/streaming.md 5f2e4d3559649aec8d41e0a9b85e1e218f6dcb61748ff32ebfc6954811564123
// https://platform.claude.com/docs/en/build-with-claude/extended-thinking.md f903c483f854852ff93e40673836250b297686b7c9780b862d94e90ce07fcae3
// https://platform.claude.com/docs/en/build-with-claude/thinking.md 94f8320b7612865d3a6df5a5cfe9166501fda01548f2b411959b8664e6094d82
// https://platform.claude.com/docs/en/build-with-claude/compaction.md 206adb26a90d4b26a0d1269c16dd6361db579ab1f122ed719d3714dc683cad3a
// https://platform.claude.com/docs/en/build-with-claude/compaction-on-demand.md 82e4c3894c713d68d19b349c6237cf5a401d95d5916831329140596c5a0496d2
// https://platform.claude.com/docs/en/build-with-claude/compaction-threshold.md 0e441125483f5bb94f132ee646d4031a94427aedf8f6228016c0e08f71199ca4

func messageInput(role string, content any) messageObject {
	return messageObject{"role": role, "content": content}
}
func messageBody(messages ...messageObject) messageObject {
	return messageObject{"model": "claude-sonnet-4-6", "max_tokens": 2048, "messages": messages}
}
func sendMessages(t *testing.T, s *Server, body messageObject, betas ...string) *http.Response {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	r, err := http.NewRequestWithContext(t.Context(), "POST", s.BaseURL(Messages)+"/v1/messages", strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Content-Type", "application/json")
	for _, b := range betas {
		r.Header.Add("anthropic-beta", b)
	}
	return do(t, r)
}
func validateMessageOutput(t *testing.T, root string, v any) {
	t.Helper()
	b, err := wireBundle(Messages)
	if err != nil {
		t.Fatal(err)
	}
	if err = b.validate(root, v); err != nil {
		t.Fatalf("invalid %s: %.1200v; value: %.700v", root, err, v)
	}
}
func messageJSON(t *testing.T, r *http.Response, status int, root string) messageObject {
	t.Helper()
	raw := readResponse(t, r)
	if r.StatusCode != status || r.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("HTTP %s, expected %d: %.1400s", r.Status, status, raw)
	}
	v, err := decodeJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	if root != "" {
		validateMessageOutput(t, root, v)
	}
	body := v.(messageObject)
	if status >= 400 && (body["type"] != "error" || body["request_id"] == "" || body["request_id"] != r.Header.Get("request-id")) {
		t.Fatalf("unattributed error: %v", body)
	}
	return body
}
func messageEvents(t *testing.T, r *http.Response, root string) ([]messageObject, error) {
	t.Helper()
	if r.StatusCode != 200 || r.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("not SSE: %d %.1300s", r.StatusCode, readResponse(t, r))
	}
	raw, readErr := io.ReadAll(r.Body)
	var events []messageObject
	for f := range strings.SplitSeq(strings.TrimSuffix(string(raw), "\n\n"), "\n\n") {
		if f == "" {
			continue
		}
		tag, data, ok := strings.Cut(f, "\ndata: ")
		if !ok || !strings.HasPrefix(tag, "event: ") {
			t.Fatalf("bad frame %.120s", f)
		}
		v, err := decodeJSON([]byte(data))
		if err != nil {
			t.Fatal(err)
		}
		validateMessageOutput(t, root, v)
		e := v.(messageObject)
		if e["type"] != strings.TrimPrefix(tag, "event: ") {
			t.Fatal(e)
		}
		events = append(events, e)
	}
	return events, readErr
}
func messageStream(t *testing.T, r *http.Response, root string) []messageObject {
	t.Helper()
	ev, err := messageEvents(t, r, root)
	if err != nil || len(ev) == 0 {
		t.Fatalf("incomplete stream: %v", err)
	}
	return ev
}
func numField(t *testing.T, v messageObject, key string) int {
	t.Helper()
	val, ok := v[key].(json.Number)
	if !ok {
		t.Fatalf("missing number %s: %v", key, v)
	}
	n, err := strconv.Atoi(val.String())
	if err != nil {
		t.Fatal(err)
	}
	return n
}
func messageErrorType(t *testing.T, r *http.Response, status int, kind, root string) messageObject {
	t.Helper()
	v := messageJSON(t, r, status, root)["error"].(messageObject)
	if v["type"] != kind || v["message"] == "" {
		t.Fatalf("wrong error: %v", v)
	}
	return v
}

func TestMessagesRichReplyAndReplay(t *testing.T) {
	s := newServer(t, func(string, ...any) {}, nil)
	call := ToolCall{"tool_1", "lookup", `{"key":"a"}`}
	turn := Text("visible").WithReasoning("think", "", "ciphertext").WithTools(call).WithUsage(Usage{Input: 100, Output: 20, Cached: 52, CacheWrite: 15})
	s.Script(turn, turn, Refusal("declined"), Text("limited").WithFinish(FinishLength))
	user := messageInput("user", []any{messageObject{"type": "image", "source": messageObject{"type": "url", "url": "https://example.test/a.png"}}, messageObject{"type": "text", "text": "select"}})
	body := messageBody(user)
	body["system"] = []any{messageObject{"type": "text", "text": "system", "cache_control": messageObject{"type": "ephemeral"}}}
	body["thinking"] = messageObject{"type": "enabled", "budget_tokens": 1024}
	body["tools"] = []any{messageObject{"name": "lookup", "description": "find", "input_schema": messageObject{"type": "object"}}}
	body["tool_choice"] = messageObject{"type": "auto"}
	v := messageJSON(t, sendMessages(t, s, body), 200, "Message")
	out := v["content"].([]any)
	if len(out) != 4 || v["stop_reason"] != "tool_use" || out[0].(messageObject)["thinking"] != "think" || out[0].(messageObject)["signature"] == "" || out[1].(messageObject)["data"] != "ciphertext" || out[2].(messageObject)["text"] != "visible" || out[3].(messageObject)["input"].(messageObject)["key"] != "a" {
		t.Fatal(v)
	}
	u := v["usage"].(messageObject)
	if numField(t, u, "input_tokens") != 33 || numField(t, u, "output_tokens") != 20 || numField(t, u, "cache_creation_input_tokens") != 15 || numField(t, u, "cache_read_input_tokens") != 52 {
		t.Fatal(u)
	}
	req, _ := s.Last()
	if req.Wire != Messages || req.Stream || req.OutputLimit != "2048" || req.System[0].Text != "system" || req.Messages[0].Parts[0].ImageURL != "https://example.test/a.png" || req.Messages[0].Parts[1].Text != "select" || req.Tools[0].Name != "lookup" || string(req.Tools[0].Schema) != `{"type":"object"}` || string(req.ToolChoice) != `{"type":"auto"}` || string(req.Reasoning) != `{"budget_tokens":1024,"type":"enabled"}` || len(req.CacheMarkers) != 1 || req.CacheMarkers[0].Path != "/system/0/cache_control" {
		t.Fatalf("unexpected projection: %+v", req)
	}
	body["stream"] = true
	events := messageStream(t, sendMessages(t, s, body, "interleaved-thinking-2025-05-14"), "BetaMessageStreamEvent")
	want := "message_start ping content_block_start content_block_delta content_block_delta content_block_stop content_block_start content_block_stop content_block_start content_block_delta content_block_stop content_block_start content_block_delta content_block_stop message_delta message_stop"
	if eventNames(events) != want {
		t.Fatal(eventNames(events))
	}
	for _, item := range []struct {
		at, i    int
		typ, key string
		value    any
	}{{3, 0, "thinking_delta", "thinking", "think"}, {4, 0, "signature_delta", "signature", out[0].(messageObject)["signature"]}, {9, 2, "text_delta", "text", "visible"}, {12, 3, "input_json_delta", "partial_json", call.Arguments}} {
		d := events[item.at]["delta"].(messageObject)
		if numField(t, events[item.at], "index") != item.i || d["type"] != item.typ || d[item.key] != item.value {
			t.Fatal(events[item.at])
		}
	}
	if events[14]["delta"].(messageObject)["stop_reason"] != "tool_use" || numField(t, events[14]["usage"].(messageObject), "output_tokens") != 20 {
		t.Fatal(events[14])
	}
	req, _ = s.Last()
	if req.Header.Get("anthropic-beta") != "interleaved-thinking-2025-05-14" || !req.Stream {
		t.Fatal(req.Header)
	}
	replay := messageBody(user, messageInput("assistant", out), messageInput("user", []any{messageObject{"type": "tool_result", "tool_use_id": call.ID, "content": []any{messageObject{"type": "text", "text": "found"}}}}))
	v = messageJSON(t, sendMessages(t, s, replay), 200, "Message")
	if v["stop_reason"] != "refusal" || v["content"].([]any)[0].(messageObject)["text"] != "declined" {
		t.Fatal(v)
	}
	req, _ = s.Last()
	p := req.Messages[1].Parts
	if p[0].Kind != PartReasoning || p[1].Encrypted != "ciphertext" || p[3].Call != call || req.Messages[2].Parts[0].Kind != PartToolResult || req.Messages[2].Parts[0].CallID != call.ID || req.Messages[2].Parts[0].Text != "found" {
		t.Fatal(req.Messages)
	}
	for _, mutation := range []struct {
		i    int
		k, v string
	}{{0, "thinking", "wrong"}, {0, "signature", "wrong"}, {1, "data", "wrong"}} {
		b := out[mutation.i].(messageObject)
		old := b[mutation.k]
		b[mutation.k] = mutation.v
		messageErrorType(t, sendMessages(t, s, replay), 400, "invalid_request_error", "ErrorResponse")
		b[mutation.k] = old
	}
	if v = messageJSON(t, sendMessages(t, s, replay), 200, "Message"); v["stop_reason"] != "max_tokens" {
		t.Fatal(v)
	}
}

func TestMessagesValidationAndFaults(t *testing.T) {
	var reports []string
	s := newServer(t, func(f string, args ...any) { reports = append(reports, fmt.Sprintf(f, args...)) }, nil)
	body := messageBody(messageInput("user", "hi"))
	s.Script(Text("after invalid input"))
	body["not_a_param"] = true
	errObj := messageErrorType(t, sendMessages(t, s, body), 400, "invalid_request_error", "ErrorResponse")
	if errObj["message"] != "not_a_param: Extra inputs are not permitted" || !strings.Contains(reports[0], "/not_a_param") {
		t.Fatal(errObj, reports)
	}
	delete(body, "not_a_param")
	delete(body, "max_tokens")
	messageErrorType(t, sendMessages(t, s, body), 400, "invalid_request_error", "ErrorResponse")
	body["max_tokens"] = "wrong"
	messageErrorType(t, sendMessages(t, s, body), 400, "invalid_request_error", "ErrorResponse")
	body["max_tokens"] = 8
	if v := messageJSON(t, sendMessages(t, s, body), 200, "Message"); v["content"].([]any)[0].(messageObject)["text"] != "after invalid input" || len(reports) != 3 {
		t.Fatal(v, reports)
	}
	for _, tt := range []struct {
		k      FaultKind
		status int
		typ    string
	}{{Auth, 401, "authentication_error"}, {ContextOverflow, 400, "invalid_request_error"}, {RateLimit, 429, "rate_limit_error"}, {Overload, 529, "overloaded_error"}} {
		f := HTTPError(tt.k)
		if tt.k == RateLimit {
			f = f.WithRetryHint(RetryAfterSeconds(19))
		}
		s.Script(f)
		r := sendMessages(t, s, body, "example-beta")
		e := messageErrorType(t, r, tt.status, tt.typ, "BetaErrorResponse")
		if tt.k == RateLimit && r.Header.Get("retry-after") != "19" || tt.k == ContextOverflow && !strings.Contains(e["message"].(string), "prompt is too long") {
			t.Fatalf("%s: %v, %v", tt.k, r.Header, e)
		}
	}
	// Doc-sourced edge response, not represented in SDK ErrorResponse:
	// https://platform.claude.com/docs/en/api/errors.md (SHA-256 above).
	// Expect avoids allocating/sending a 128 MiB body just to exercise the limit.
	r, err := http.NewRequestWithContext(t.Context(), "POST", s.BaseURL(Messages)+"/v1/messages", strings.NewReader("x"))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Expect", "100-continue")
	r.ContentLength = 129 << 20
	r.Close = true
	messageErrorType(t, do(t, r), 413, "request_too_large", "")
}

func TestMessagesStreamFaultsAndCacheGate(t *testing.T) {
	s := New(t, WithCacheProfile(Messages, "claude-sonnet-4-6", CacheProfile{Minimum: 1, Granularity: 1}))
	gate := NewGate()
	s.Script(Text("released").WithGate(gate), Text("concurrent"), Text("ok").WithStreamError(4, Overload), Text("cut").WithCut(3), StreamError(0, Auth))
	body := messageBody(messageInput("user", "hi"))
	body["cache_control"] = messageObject{"type": "ephemeral"}
	body["stream"] = true
	paused := sendMessages(t, s, body)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	if err := gate.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	body["stream"] = false
	conc := messageJSON(t, sendMessages(t, s, body), 200, "Message")
	if u := conc["usage"].(messageObject); numField(t, u, "cache_read_input_tokens") != 0 || numField(t, u, "cache_creation_input_tokens") == 0 {
		t.Fatal("gated stream published cache too early", u)
	}
	gate.Release()
	ev := messageStream(t, paused, "MessageStreamEvent")
	if ev[len(ev)-1]["type"] != "message_stop" || ev[3]["delta"].(messageObject)["text"] != "released" {
		t.Fatal(ev)
	}
	body["stream"] = true
	ev = messageStream(t, sendMessages(t, s, body), "MessageStreamEvent")
	if eventNames(ev) != "message_start ping content_block_start content_block_delta error" || ev[4]["error"].(messageObject)["type"] != "overloaded_error" {
		t.Fatal(ev)
	}
	ev, err := messageEvents(t, sendMessages(t, s, body), "MessageStreamEvent")
	if !errors.Is(err, io.ErrUnexpectedEOF) || eventNames(ev) != "message_start ping content_block_start" {
		t.Fatalf("cut: %s, %v", eventNames(ev), err)
	}
	ev = messageStream(t, sendMessages(t, s, body, "example-beta"), "BetaMessageStreamEvent")
	if eventNames(ev) != "error" || ev[0]["error"].(messageObject)["type"] != "authentication_error" {
		t.Fatal(ev)
	}
}

func markedText(value, ttl string) messageObject {
	b := messageObject{"type": "text", "text": value}
	if ttl != "" {
		b["cache_control"] = messageObject{"type": "ephemeral", "ttl": ttl}
	}
	return b
}
func TestMessagesCacheBreakpoints(t *testing.T) {
	s := newServer(t, func(string, ...any) {}, []Option{WithCacheProfile(Messages, "claude-sonnet-4-6", CacheProfile{Minimum: 1, Granularity: 1})})
	s.Respond(func(Request) Turn { return Text("ok") })
	body := messageBody(messageInput("user", "tail"))
	base := markedText("prefix", "1h")
	body["system"] = []any{base}
	send := func() (int, int, int, int) {
		u := messageJSON(t, sendMessages(t, s, body), 200, "Message")["usage"].(messageObject)
		read, write, plain := numField(t, u, "cache_read_input_tokens"), numField(t, u, "cache_creation_input_tokens"), numField(t, u, "input_tokens")
		h := numField(t, u["cache_creation"].(messageObject), "ephemeral_1h_input_tokens")
		req, _ := s.Last()
		if req.Prefix.CachedTokens != read || req.Prefix.CacheWriteTokens != write || req.Prefix.InputTokens != read+write+plain || req.Prefix.SharedTokens < read || numField(t, u["cache_creation"].(messageObject), "ephemeral_5m_input_tokens") != write-h {
			t.Fatal(req.Prefix, u)
		}
		return read, write, plain, h
	}
	read, write, tail, h := send()
	if read != 0 || write <= 0 || tail <= 0 || h != write {
		t.Fatal(read, write, tail, h)
	}
	if r, w, p, h := send(); r != write || w != 0 || p != tail || h != 0 {
		t.Fatal(r, w, p, h)
	}
	delete(base, "cache_control")
	msg := messageInput("user", []any{markedText("tail", ""), markedText("more", "5m")})
	body["messages"] = []any{msg}
	r, w, p, h := send()
	if r != write || w <= tail || p != 0 || h != 0 {
		t.Fatal(r, w, p, h)
	}
	delete(body, "system")
	body["messages"] = []any{messageInput("user", []any{markedText("shared but unwritten", ""), markedText("varies 1", "5m")})}
	send()
	body["messages"] = []any{messageInput("user", []any{markedText("shared but unwritten", ""), markedText("varies 2", "5m")})}
	if r, _, _, _ := send(); r != 0 {
		t.Fatal("read from unmarked shared prefix", r)
	}
	var four []any
	for i := range 4 {
		four = append(four, markedText(strconv.Itoa(i), "5m"))
	}
	body["messages"] = []any{messageInput("user", append(slices.Clone(four), markedText("fifth", "5m")))}
	messageErrorType(t, sendMessages(t, s, body), 400, "invalid_request_error", "ErrorResponse")
	body["messages"] = []any{messageInput("user", []any{markedText("short", "5m"), markedText("long", "1h")})}
	messageErrorType(t, sendMessages(t, s, body), 400, "invalid_request_error", "ErrorResponse")
	body["cache_control"] = messageObject{"type": "ephemeral", "ttl": "1h"}
	body["messages"] = []any{messageInput("user", []any{markedText("conflict", "5m")})}
	messageErrorType(t, sendMessages(t, s, body), 400, "invalid_request_error", "ErrorResponse")
}
func TestMessagesCacheLimitsAndLookback(t *testing.T) {
	s := New(t)
	s.Respond(func(Request) Turn { return Text("ok") })
	for _, tc := range []struct {
		id    string
		write bool
	}{{"claude-opus-4-6", false}, {"claude-sonnet-4-6", true}} {
		b := messageBody(messageInput("user", []any{markedText(strings.Repeat("x", 4800), "5m")}))
		b["model"] = tc.id
		u := messageJSON(t, sendMessages(t, s, b), 200, "Message")["usage"].(messageObject)
		if (numField(t, u, "cache_creation_input_tokens") > 0) != tc.write {
			t.Fatal(tc, u)
		}
	}
	for _, added := range []int{19, 20} {
		s := New(t, WithCacheProfile(Messages, "claude-sonnet-4-6", CacheProfile{Minimum: 1, Granularity: 1}))
		s.Respond(func(Request) Turn { return Text("ok") })
		parts := []any{markedText("start", "5m")}
		body := messageBody(messageInput("user", parts))
		u := messageJSON(t, sendMessages(t, s, body), 200, "Message")["usage"].(messageObject)
		first := numField(t, u, "cache_creation_input_tokens")
		delete(parts[0].(messageObject), "cache_control")
		for i := range added {
			parts = append(parts, markedText(strconv.Itoa(i), ""))
		}
		body["messages"] = []any{messageInput("user", parts)}
		body["cache_control"] = messageObject{"type": "ephemeral"}
		u = messageJSON(t, sendMessages(t, s, body), 200, "Message")["usage"].(messageObject)
		r := numField(t, u, "cache_read_input_tokens")
		if added == 19 && r != first || added == 20 && r != 0 {
			t.Fatalf("%d appended: read %d, first write %d", added, r, first)
		}
	}
}
func TestMessagesCacheExpiry(t *testing.T) { // Deterministic time boundary for lookup; HTTP tests cover actual publication.
	key := [32]byte{1}
	start := time.Unix(100, 0)
	for _, ttl := range []time.Duration{5 * time.Minute, time.Hour} {
		cache := map[[32]byte]messageCacheEntry{key: {ttl, start.Add(ttl)}}
		plan := messageCachePlan{points: []messageCachePoint{{key: key, tokens: 2000, ttl: ttl}}}
		p := Prefix{InputTokens: 2000, SharedBlocks: 1, SharedTokens: 2000}
		plan.account(&p, CacheProfile{Minimum: 1024, Granularity: 1}, cache, start.Add(ttl-time.Nanosecond))
		if p.CachedTokens != 2000 || p.CacheWriteTokens != 0 {
			t.Fatal(p)
		}
		plan.account(&p, CacheProfile{Minimum: 1024, Granularity: 1}, cache, start.Add(ttl))
		if p.CachedTokens != 0 || p.CacheWriteTokens != 2000 {
			t.Fatal(p)
		}
	}
}

func TestMessagesOnDemandCompaction(t *testing.T) {
	s := newServer(t, func(string, ...any) {}, nil)
	s.Respond(func(Request) Turn { return Text("recipe summary") })
	old := messageInput("user", "recipe ingredients")
	body := messageBody(old)
	body["compaction"] = messageObject{"type": "summarize"}
	messageErrorType(t, sendMessages(t, s, body), 400, "invalid_request_error", "ErrorResponse")
	v := messageJSON(t, sendMessages(t, s, body, "example-beta, compact-2026-09-04"), 200, "BetaMessage")
	parts := v["content"].([]any)
	u := v["usage"].(messageObject)
	it := u["iterations"].([]any)
	if v["stop_reason"] != "compaction" || len(parts) != 1 || len(it) != 1 || it[0].(messageObject)["type"] != "compaction" || numField(t, it[0].(messageObject), "input_tokens") <= 0 || numField(t, u, "input_tokens") != 0 || numField(t, u, "output_tokens") != 0 {
		t.Fatal(v)
	}
	sealed := parts[0].(messageObject)
	if sealed["type"] != "compaction" || sealed["signature"] == "" || sealed["content"] != "recipe summary" {
		t.Fatal(sealed)
	}
	body["stream"] = true
	ev := messageStream(t, sendMessages(t, s, body, demandBeta), "BetaMessageStreamEvent")
	if eventNames(ev) != "message_start ping content_block_start content_block_stop message_delta message_stop" || !reflect.DeepEqual(ev[2]["content_block"], sealed) {
		t.Fatal(ev)
	}
	rem := messageInput("user", "next")
	prior := messageInput("assistant", parts)
	cont := messageBody(prior, rem)
	messageJSON(t, sendMessages(t, s, cont, demandBeta), 200, "BetaMessage")
	req, _ := s.Last()
	if len(req.Effective.Messages) != 2 || req.Messages[0].Parts[0].Kind != PartOpaque || req.Effective.Messages[0].Parts[0].Text != "recipe summary" || req.Effective.Messages[1].Parts[0].Text != "next" {
		t.Fatal(req.Effective)
	}
	messageErrorType(t, sendMessages(t, s, cont), 400, "invalid_request_error", "ErrorResponse")
	for _, k := range []string{"signature", "content", "type", "extra"} {
		mut := maps.Clone(sealed)
		mut[k] = "wrong"
		bad := messageBody(messageInput("assistant", []any{mut}), rem)
		msg := messageErrorType(t, sendMessages(t, s, bad, demandBeta), 400, "invalid_request_error", "BetaErrorResponse")
		if (k == "signature" || k == "content") && msg["details"].(messageObject)["error_code"] != "compaction_signature_invalid" {
			t.Fatal(msg)
		}
	}
	misplaced := messageBody(old, prior, rem)
	e := messageErrorType(t, sendMessages(t, s, misplaced, demandBeta), 400, "invalid_request_error", "BetaErrorResponse")
	if e["details"].(messageObject)["error_code"] != "compaction_block_misplaced" {
		t.Fatal(e)
	}
	body["context_management"] = messageObject{"edits": []any{messageObject{"type": "compact_20260112"}}}
	messageErrorType(t, sendMessages(t, s, body, demandBeta, thresholdBeta), 400, "invalid_request_error", "BetaErrorResponse")
	cont["context_management"] = body["context_management"]
	messageErrorType(t, sendMessages(t, s, cont, demandBeta, thresholdBeta), 400, "invalid_request_error", "BetaErrorResponse")
}
func TestMessagesThresholdCompaction(t *testing.T) {
	s := newServer(t, func(string, ...any) {}, nil)
	s.Respond(func(Request) Turn { return Text("summary or answer") })
	edit := messageObject{"type": "compact_20260112", "trigger": messageObject{"type": "input_tokens", "value": 50000}, "pause_after_compaction": true}
	cfg := messageObject{"edits": []any{edit}}
	body := messageBody(messageInput("user", "small"))
	body["context_management"] = cfg
	messageErrorType(t, sendMessages(t, s, body, "other"), 400, "invalid_request_error", "BetaErrorResponse")
	v := messageJSON(t, sendMessages(t, s, body, thresholdBeta), 200, "BetaMessage")
	if v["stop_reason"] != "end_turn" || len(v["usage"].(messageObject)["iterations"].([]any)) != 1 {
		t.Fatal(v)
	}
	old := messageInput("user", strings.Repeat("old ", 50000))
	body["messages"] = []any{old}
	body["stream"] = true
	ev := messageStream(t, sendMessages(t, s, body, thresholdBeta), "BetaMessageStreamEvent")
	if eventNames(ev) != "message_start ping content_block_start content_block_delta content_block_stop message_delta message_stop" || ev[2]["content_block"].(messageObject)["content"] != nil || ev[3]["delta"].(messageObject)["type"] != "compaction_delta" || ev[3]["delta"].(messageObject)["content"] != "summary or answer" || ev[5]["delta"].(messageObject)["stop_reason"] != "compaction" {
		t.Fatal(ev)
	}
	body["stream"] = false
	v = messageJSON(t, sendMessages(t, s, body, thresholdBeta), 200, "BetaMessage")
	out := v["content"].([]any)
	if len(out) != 1 || v["stop_reason"] != "compaction" || out[0].(messageObject)["content"] != "summary or answer" || out[0].(messageObject)["signature"] != nil {
		t.Fatal(v)
	}
	sealed := out[0].(messageObject)
	replay := messageBody(old, messageInput("assistant", []any{sealed, markedText("kept", "")}), messageInput("user", "continue"))
	messageErrorType(t, sendMessages(t, s, replay, thresholdBeta), 400, "invalid_request_error", "BetaErrorResponse")
	replay["context_management"] = cfg
	v = messageJSON(t, sendMessages(t, s, replay, thresholdBeta), 200, "BetaMessage")
	req, _ := s.Last()
	if v["stop_reason"] != "end_turn" || len(req.Messages) != 3 || len(req.Effective.Messages) != 2 || len(req.Effective.Messages[0].Parts) != 2 || req.Effective.Messages[0].Parts[0].Text != "summary or answer" || req.Effective.Messages[0].Parts[1].Text != "kept" || req.Effective.Messages[1].Parts[0].Text != "continue" || req.Prefix.InputTokens >= 50000 {
		t.Fatal("old context was replayed", req.Prefix)
	}
	sealed["content"] = "changed"
	messageErrorType(t, sendMessages(t, s, replay, thresholdBeta), 400, "invalid_request_error", "BetaErrorResponse")
	edit["pause_after_compaction"] = false
	v = messageJSON(t, sendMessages(t, s, body, thresholdBeta), 200, "BetaMessage")
	out = v["content"].([]any)
	it := v["usage"].(messageObject)["iterations"].([]any)
	if v["stop_reason"] != "end_turn" || len(out) != 2 || out[0].(messageObject)["type"] != "compaction" || out[1].(messageObject)["text"] != "summary or answer" || len(it) != 2 || it[1].(messageObject)["type"] != "message" || numField(t, it[1].(messageObject), "input_tokens") >= numField(t, it[0].(messageObject), "input_tokens") {
		t.Fatal(v)
	}
}
func TestMessagesModelInventory(t *testing.T) {
	s := New(t, WithModels(Messages, "claude-opus-4-6", "claude-haiku-4-5"))
	for _, beta := range []bool{false, true} {
		r, err := http.NewRequestWithContext(t.Context(), "GET", s.BaseURL(Messages)+"/v1/models", nil)
		if err != nil {
			t.Fatal(err)
		}
		root := "ModelInfo"
		if beta {
			r.Header.Set("anthropic-beta", demandBeta)
			root = "BetaModelInfo"
		}
		v := messageJSON(t, do(t, r), 200, "")
		models := v["data"].([]any)
		if len(models) != 2 || v["has_more"] != false || v["first_id"] != "claude-opus-4-6" || v["last_id"] != "claude-haiku-4-5" {
			t.Fatal(v)
		}
		for i, item := range models {
			validateMessageOutput(t, root, item)
			caps := item.(messageObject)["capabilities"].(messageObject)
			if beta {
				if caps["compaction"].(messageObject)["summarize"].(messageObject)["supported"] != (i == 0) {
					t.Fatal(caps)
				}
			} else if _, ok := caps["compaction"]; ok {
				t.Fatal("beta capability leaked into stable listing")
			}
		}
	}
}

func TestMessagesToolJSONDeltaPreservesScriptedBytes(t *testing.T) {
	s := New(t)
	const args = `{ "z": 1e2, "a" : "<" }`
	s.Script(CallTools(ToolCall{Name: "lookup", Arguments: args}))
	body := messageBody(messageInput("user", "find"))
	body["stream"] = true
	ev := messageStream(t, sendMessages(t, s, body), "MessageStreamEvent")
	if ev[3]["delta"].(messageObject)["partial_json"] != args {
		t.Fatalf("tool delta normalized the input: %v", ev[3])
	}
}
