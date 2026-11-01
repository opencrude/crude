package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"
)

const onceStop = "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"
const onceDone = "data: [DONE]\n\n"

func onceFixture(url string) (*Provider, TextOnceRequest) {
	return &Provider{ID: "custom", BaseURL: url, StandardName: "openai", Model: "literal-model", APIKey: "fixture-key"},
		TextOnceRequest{Model: "literal-model", System: " system \r\n", Input: " input\t\n", MaxOutputTokens: 97, MaxOutputBytes: 128}
}

func onceRun(t *testing.T, protocol, wire string, capBytes int) (TextOnceResult, error) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, wire)
	}))
	defer srv.Close()
	p, req := onceFixture(srv.URL)
	p.StandardName, req.MaxOutputBytes = protocol, capBytes
	result, err := p.CompleteTextOnce(t.Context(), req)
	if !result.Accepted || (err != nil && result.UsageComplete) {
		t.Fatalf("invalid accounting: %+v err=%v", result, err)
	}
	return result, err
}

func onceTerminal(usage string) string {
	return `data: {"type":"response.completed","response":{"status":"completed","output":[{"id":"m","type":"message","role":"assistant","content":[{"type":"output_text","text":"literal"}]}]` + usage + "}}\n\n"
}

func TestTextOnceIndependentOriginalWireNegatives(t *testing.T) {
	for _, tc := range []struct{ name, event string }{
		{"unpaired-surrogate", `{"choices":[{"index":0,"delta":{"content":"\ud800"},"finish_reason":"stop"}]}`},
		{"multiple-choices", `{"choices":[{"index":0,"delta":{"content":"first"},"finish_reason":"stop"},{"index":1,"delta":{"content":"second"},"finish_reason":"stop"}]}`},
		{"oversized-wire-event", `{"choices":[{"index":0,"delta":{"content":"literal"},"finish_reason":"stop"}],"irrelevant":"` + strings.Repeat("a", 512<<10) + `"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := onceRun(t, "openai", "data: "+tc.event+"\n\n"+onceDone, 128)
			if err == nil || result.Text != "" {
				t.Fatalf("ambiguous or oversized original provider wire became usable code: case=%s text=%q", tc.name, result.Text)
			}
		})
	}
}

func TestTextOnceUsagePresenceAndFailure(t *testing.T) {
	// Literal independent oracle: empty usage must differ from explicit full zero.
	for _, protocol := range []string{"openai", "openai-responses"} {
		for _, tc := range []struct {
			name, usage            string
			present, complete, bad bool
		}{
			{"absent", "", false, false, false}, {"null", "null", false, false, false},
			{"empty", "{}", true, false, false},
			{"partial-input", `{"prompt_tokens":7}`, true, false, false},
			{"zero", `{"prompt_tokens":0,"completion_tokens":0}`, true, true, false},
			{"full", `{"prompt_tokens":7,"completion_tokens":2}`, true, true, false},
			{"null-scalar", `{"prompt_tokens":null,"completion_tokens":0}`, true, false, true},
			{"negative", `{"prompt_tokens":-1,"completion_tokens":0}`, true, false, true},
			{"fraction", `{"prompt_tokens":1.5,"completion_tokens":0}`, true, false, true},
			{"string", `{"prompt_tokens":"1","completion_tokens":0}`, true, false, true},
		} {
			t.Run(protocol+"/"+tc.name, func(t *testing.T) {
				usage := tc.usage
				if usage != "" {
					usage = `,"usage":` + usage
				}
				wire := onceStop
				if usage != "" {
					wire += `data: {"choices":[]` + usage + "}\n\n"
				}
				wire += onceDone
				if protocol == "openai-responses" {
					wire = onceTerminal(strings.ReplaceAll(strings.ReplaceAll(usage, "prompt_tokens", "input_tokens"), "completion_tokens", "output_tokens"))
				}
				got, err := onceRun(t, protocol, wire, 128)
				if (err != nil) != tc.bad || (got.Usage != nil) != tc.present || got.UsageComplete != tc.complete {
					t.Fatalf("unreported token scalars became indistinguishable from complete explicit zero usage: %+v %v", got, err)
				}
				if tc.name == "full" && (got.Usage.PromptTokens != 7 || got.Usage.CompletionTokens != 2) {
					t.Fatalf("observations changed: %+v", got)
				}
			})
		}
	}
	const prefix = "data: {\"choices\":[{\"delta\":{\"content\":\"abc\"}}],\"usage\":{\"prompt_tokens\":7,\"completion_tokens\":2}}\n\n"
	for _, suffix := range []string{"", "data: {bad\n\n", `data: {"error":{"message":"late failure"}}` + "\n\n", onceStop} {
		got, err := onceRun(t, "openai", prefix+suffix, 128)
		if err == nil || got.Text != "abc" || got.Usage == nil || got.Usage.PromptTokens != 7 || got.UsageComplete {
			t.Fatalf("failed stream lost observations: %+v %v", got, err)
		}
	}
	got, err := onceRun(t, "openai", prefix+onceStop+`data: {"choices":[],"usage":{}}`+"\n\n"+onceDone, 128)
	if err != nil || got.Usage == nil || got.UsageComplete {
		t.Fatalf("trailing incomplete usage: %+v %v", got, err)
	}
	got, err = onceRun(t, "openai-responses", strings.Replace(onceTerminal(`,"usage":{"input_tokens":7,"output_tokens":2}`), "response.completed", "response.incomplete", 1), 128)
	if err == nil || got.Usage == nil || got.Usage.PromptTokens != 7 || got.UsageComplete {
		t.Fatalf("failed response: %+v %v", got, err)
	}
}

func onceChat(delta string) string {
	return `data: {"choices":[{"delta":` + delta + `,"finish_reason":"stop"}]}` + "\n\n" + onceDone
}

func TestTextOnceStrictWire(t *testing.T) {
	for _, delta := range []string{
		`{"content":"\udc00"}`, `{"content":"\ud800x"}`, "{\"content\":\"\xff\"}",
		`{"content":"a","content":"b"}`, `{"content":"a","c\u006fntent":"b"}`,
		`{"content":["x"]}`, `{"tool_calls":[]}`, `{"reasoning_content":"private"}`, `{"refusal":"no"}`,
	} {
		if got, err := onceRun(t, "openai", onceChat(delta), 128); err == nil {
			t.Fatalf("unsafe delta: %s %+v", delta, got)
		}
	}
	for _, wire := range []string{
		onceDone, onceStop, onceStop + "data: [DONE]", onceStop + onceStop + onceDone,
		strings.Replace(onceStop, `"stop"`, `"length"`, 1) + onceDone,
		`data: {"choices":[],"usage":{"prompt_tokens":0,"prompt_tokens":1,"completion_tokens":0}}` + "\n\n" + onceDone,
		`data: {"choices":[{"delta":{},"finish_reason":"stop"}],"x":` + strings.Repeat("[", 30) + "0" + strings.Repeat("]", 30) + "}\n\n" + onceDone,
		strings.Replace(onceStop, "}\n\n", "} {}\n\n", 1) + onceDone,
	} {
		if got, err := onceRun(t, "openai", wire, 128); err == nil {
			t.Fatalf("truncated/ambiguous success: %+v", got)
		}
	}
}

func TestTextOnceBoundsAndLiteralUnicode(t *testing.T) {
	for _, tc := range []struct {
		wire string
		cap  int
		text string
		bad  bool
	}{
		{`data: {"choices":[{"delta":{"content":" \r\nλ\ud83d\ude00\\ud800�"},"finish_reason":"stop"}]}` + "\n\n" + onceDone, 128, " \r\nλ😀\\ud800�", false},
		{`data: {"choices":[{"delta":{"content":"λ😀"},"finish_reason":"stop"}]}` + "\n\n" + onceDone, 6, "λ😀", false},
		{`data: {"choices":[{"delta":{"content":"λ😀"},"finish_reason":"stop"}]}` + "\n\n" + onceDone, 4, "λ", true},
		{strings.Repeat(": "+strings.Repeat("a", 999)+"\n", 34) + "\n" + onceStop + onceDone, 128, "", true},
		{strings.Repeat(":"+strings.Repeat("a", 1020)+"\n\n", 1026) + onceStop + onceDone, 128, "", true},
		{strings.Repeat("data: {\"choices\":[{\"delta\":{}}]}\n\n", 2048) + onceStop + onceDone, 128, "", true},
		{": \xff\n\n" + onceStop + onceDone, 128, "", true},
		{onceStop + onceDone, 128, "", false},
	} {
		got, err := onceRun(t, "openai", tc.wire, tc.cap)
		if (err != nil) != tc.bad || got.Text != tc.text || !utf8.ValidString(got.Text) {
			t.Fatalf("bounded literal: %+v err=%v", got, err)
		}
	}
	// Raw CRLF size differs from SSEReader's normalized count.
	over := strings.Repeat(":x\r\n", 8192) + "\r\n" + onceStop + onceDone
	if _, err := onceRun(t, "openai", over, 128); !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("raw CRLF bound: %v", err)
	}
}

func TestTextOnceResponsesStreamAndRefusals(t *testing.T) {
	wire := `data: {"type":"response.output_item.added","output_index":0,"item":{"id":"m","type":"message","role":"assistant","content":[]}}` + "\n\n" +
		`data: {"type":"response.output_text.delta","item_id":"m","content_index":0,"delta":"literal"}` + "\n\n" +
		`data: {"type":"response.output_text.done","text":"literal"}` + "\n\n" + onceTerminal(`,"usage":{"input_tokens":0,"output_tokens":0}`)
	got, err := onceRun(t, "openai-responses", wire, 128)
	if err != nil || got.Text != "literal" || !got.UsageComplete {
		t.Fatalf("responses stream: %+v %v", got, err)
	}
	for _, unsafe := range []string{
		strings.Replace(onceTerminal(""), `"message"`, `"reasoning"`, 1),
		strings.Replace(onceTerminal(""), `"message"`, `"web_search_call"`, 1),
		strings.Replace(onceTerminal(""), `"content":[`, `"content":[{"type":"output_text","text":"extra"},`, 1),
		`data: {"type":"response.reasoning_text.delta","delta":"private"}` + "\n\n" + onceTerminal(""),
		`data: {"type":"response.output_text.delta","output_index":1,"delta":"other"}` + "\n\n" + onceTerminal(""),
		`data: {"type":"response.output_text.delta","delta":"different"}` + "\n\n" + onceTerminal(""),
	} {
		if got, err := onceRun(t, "openai-responses", unsafe, 128); err == nil {
			t.Fatalf("unsupported response usable: %+v", got)
		}
	}
	got, err = onceRun(t, "openai-responses", `data: {"type":"response.completed","response":{"status":"completed","output":[]}}`+"\n\n", 128)
	if err != nil || got.Text != "" || got.UsageComplete {
		t.Fatalf("explicit empty: %+v %v", got, err)
	}
}

func TestTextOnceRequestAndSingleAttempt(t *testing.T) {
	for _, protocol := range []string{"openai", "openai-responses"} {
		for _, status := range []int{200, 503, 429, 404, 301, 302, 303, 307, 308} {
			t.Run(fmt.Sprintf("%s/%d", protocol, status), func(t *testing.T) {
				var calls, redirected atomic.Int32
				target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { redirected.Add(1) }))
				defer target.Close()
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					path := "/mount/chat/completions"
					if protocol == "openai-responses" {
						path = "/mount/responses"
					}
					if r.Method != "POST" || r.URL.Path != path || r.Header.Get("Authorization") != "Bearer fixture-key" || r.Header.Get("X-Fixture") != "kept" || r.Header.Get("Idempotency-Key") != "" {
						t.Errorf("request %s %s %v", r.Method, r.URL, r.Header)
					}
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					want := `{"model":"literal-model","stream":true,"max_tokens":97,"tool_choice":"none","stream_options":{"include_usage":true},"messages":[{"role":"system","content":" system \r\n"},{"role":"user","content":" input\t\n"}]}`
					if protocol == "openai-responses" {
						want = `{"model":"literal-model","stream":true,"max_output_tokens":97,"tool_choice":"none","store":true,"input":[{"role":"system","content":[{"type":"input_text","text":" system \r\n"}]},{"role":"user","content":[{"type":"input_text","text":" input\t\n"}]}]}`
					}
					var expected map[string]any
					if err := json.Unmarshal([]byte(want), &expected); err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(body, expected) {
						t.Errorf("body got=%#v want=%#v", body, expected)
					}
					w.Header().Set("Content-Type", "text/event-stream")
					w.Header().Set("Location", target.URL)
					w.Header().Set("Retry-After", "300")
					w.WriteHeader(status)
					wire := onceStop + onceDone
					if protocol == "openai-responses" {
						wire = onceTerminal("")
					}
					_, _ = io.WriteString(w, wire)
				}))
				defer srv.Close()
				p, req := onceFixture(srv.URL + "/mount")
				p.StandardName, p.headers = protocol, map[string]string{"X-Fixture": "kept"}
				p.SetDialSettings(DialSettings{MaxAttempts: 6})
				got, err := p.CompleteTextOnce(t.Context(), req)
				if calls.Load() != 1 || redirected.Load() != 0 || (err != nil) != (status != 200) || got.Accepted != (status == 200) {
					t.Fatalf("one attempt %+v err=%v count=%d redir=%d", got, err, calls.Load(), redirected.Load())
				}
				if status != 200 {
					var se *StatusError
					if !errors.As(err, &se) || se.StatusCode != status {
						t.Fatalf("status error: %v", err)
					}
				}
			})
		}
	}
}

func TestTextOnceAdmission(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer srv.Close()
	for _, key := range []string{"model", "messages", "tools", "stream", "max_tokens", "n", "previous_response_id", "unknown"} {
		p, r := onceFixture(srv.URL)
		p.SetExtraBody(map[string]any{key: nil})
		if _, err := p.CompleteTextOnce(t.Context(), r); err == nil {
			t.Fatalf("unsafe extra %s", key)
		}
	}
	p, r := onceFixture(srv.URL)
	p.headers = map[string]string{"Idempotency-Key": ""}
	if _, err := p.CompleteTextOnce(t.Context(), r); err == nil {
		t.Fatal("replay header")
	}
	p.headers = nil
	p.SetOAuth(&OAuthCredentials{RefreshToken: "never-refresh", ExpiresAt: 1})
	if _, err := p.CompleteTextOnce(t.Context(), r); err == nil {
		t.Fatal("OAuth")
	}
	if calls.Load() != 0 {
		t.Fatalf("invalid config reached HTTP: %d", calls.Load())
	}
}

func TestTextOncePacedCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var calls atomic.Int32
	closed := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}],\"usage\":{\"prompt_tokens\":11,\"completion_tokens\":2}}\n\n")
		w.(http.Flusher).Flush()
		time.AfterFunc(30*time.Millisecond, cancel)
		<-r.Context().Done()
		close(closed)
	}))
	defer srv.Close()
	p, r := onceFixture(srv.URL)
	got, err := p.CompleteTextOnce(ctx, r)
	if !errors.Is(err, context.Canceled) || got.Text != "partial" || got.Usage == nil || got.Usage.PromptTokens != 11 || got.UsageComplete {
		t.Fatalf("cancel observations: %+v %v", got, err)
	}
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("local body still open")
	}
	if calls.Load() != 1 {
		t.Fatalf("cancellation replayed %d", calls.Load())
	}
	// Local HTTP closure is not proof that remote model inference terminated.
}
