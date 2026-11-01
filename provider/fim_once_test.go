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

	"crude/network"
)

func fimRequest() FIMOnceRequest {
	return FIMOnceRequest{Dialect: FIMOpenAI, EndpointPath: "completions",
		Model: "literal/model", Prefix: " \tα\r\n", Suffix: "\n終  ",
		MaxOutputTokens: 32, MaxOutputBytes: 1024}
}

const fimPartial = `{"choices":[{"index":0,"text":" \tλ\n","finish_reason":null}],"usage":{"prompt_tokens":7}}`
const fimFinish = `{"choices":[{"index":0,"text":"","finish_reason":"stop"}]}`
const fimDone = "data: [DONE]\n\n"

func fimSSE(events ...string) string {
	var out strings.Builder
	for _, event := range events {
		out.WriteString("data: ")
		out.WriteString(event)
		out.WriteString("\n\n")
	}
	return out.String()
}

func fimServer(t *testing.T, body string) (*Provider, *atomic.Int32) {
	t.Helper()
	calls := &atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(server.Close)
	return &Provider{BaseURL: server.URL, StandardName: "openai"}, calls
}

func TestFIMDialectLiteralWire(t *testing.T) {
	for _, test := range []struct {
		dialect                      FIMDialect
		mount, path, wantPath, event string
	}{
		{FIMOpenAI, "", "completions", "/v1/completions",
			`{"choices":[{"index":0,"text":"\t é😀\\uD800 \r\n","finish_reason":"stop"}]}`},
		{FIMDeepSeek, "/beta", "/completions", "/beta/completions",
			`{"choices":[{"index":0,"text":"\t é\uD83D\uDE00\\uD800 \r\n","finish_reason":"length"}]}`},
		{FIMCodestral, "/gateway/v1/", "fim/completions", "/gateway/v1/fim/completions",
			`{"choices":[{"index":0,"delta":{"role":"assistant","content":"\t é😀\\uD800 \r\n","tool_calls":null},"finish_reason":"stop"}]}`},
	} {
		t.Run(string(test.dialect), func(t *testing.T) {
			var calls atomic.Int32
			prefix, suffix := " \tα\r\n", "\n終  "
			if test.dialect == FIMDeepSeek {
				prefix, suffix = strings.Repeat("α", 128<<10), ""
			}
			if test.dialect == FIMCodestral {
				prefix = ""
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != "POST" || r.URL.RequestURI() != test.wantPath {
					t.Errorf("request: %s %s", r.Method, r.URL)
				}
				if r.Header.Get("X-Credential") != "secret" || r.Header.Get("X-Recipe") != "literal" ||
					r.Header.Get("Authorization") != "" || r.Header.Get("Content-Type") != "application/json" {
					t.Errorf("headers: %v", r.Header)
				}
				var got map[string]any
				if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
					t.Error(err)
				}
				want := map[string]any{"model": "literal/model", "prompt": prefix,
					"suffix": suffix, "max_tokens": float64(32), "stream": true}
				if test.dialect != FIMCodestral {
					want["stream_options"] = map[string]any{"include_usage": true}
				}
				if !reflect.DeepEqual(got, want) {
					t.Errorf("wire = %#v; want %#v", got, want)
				}
				w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
				_, _ = io.WriteString(w, fimSSE(test.event,
					`{"choices":[],"usage":{"prompt_tokens":0,"completion_tokens":0}}`)+fimDone)
			}))
			defer server.Close()
			p := &Provider{BaseURL: server.URL + test.mount, StandardName: "openai", APIKey: "secret",
				authType: AuthCustomHeader, authHeader: "X-Credential", headers: map[string]string{"X-Recipe": "literal"}}
			req := fimRequest()
			req.Dialect = test.dialect
			req.Prefix, req.Suffix = prefix, suffix
			req.EndpointPath = test.path
			got, err := p.CompleteFIMOnce(context.Background(), req)
			if err != nil || got.Text != "\t é😀\\uD800 \r\n" || !got.Accepted ||
				!got.UsageComplete || got.Usage == nil || *got.Usage != (TokenUsage{}) || calls.Load() != 1 {
				t.Fatalf("result=%+v usage=%+v error=%v calls=%d", got, got.Usage, err, calls.Load())
			}
		})
	}
}

func TestFIMUsageEvidence(t *testing.T) {
	for _, test := range []struct {
		name, usage       string
		present, complete bool
		input, output     int
	}{
		{"absent", "", false, false, 0, 0},
		{"null", `"usage":null,`, false, false, 0, 0},
		{"empty", `"usage":{},`, false, false, 0, 0},
		{"partial input", `"usage":{"prompt_tokens":4},`, true, false, 4, 0},
		{"partial output", `"usage":{"completion_tokens":6},`, true, false, 0, 6},
		{"zero", `"usage":{"prompt_tokens":0,"completion_tokens":0},`, true, true, 0, 0},
		{"both", `"usage":{"prompt_tokens":4,"completion_tokens":6},`, true, true, 4, 6},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := `{` + test.usage + `"choices":[{"index":0,"text":"x","finish_reason":"stop"}]}`
			p, _ := fimServer(t, fimSSE(body)+fimDone)
			got, err := p.CompleteFIMOnce(context.Background(), fimRequest())
			if err != nil || got.UsageComplete != test.complete || (got.Usage != nil) != test.present {
				t.Fatalf("result=%+v error=%v", got, err)
			}
			if got.Usage != nil && (got.Usage.PromptTokens != test.input || got.Usage.CompletionTokens != test.output) {
				t.Fatalf("usage=%+v", got.Usage)
			}
		})
	}
	p, _ := fimServer(t, fimSSE(fimPartial, fimFinish,
		`{"choices":[],"usage":{"completion_tokens":2}}`)+fimDone)
	got, err := p.CompleteFIMOnce(context.Background(), fimRequest())
	if err != nil || !got.UsageComplete || got.Usage.PromptTokens != 7 || got.Usage.CompletionTokens != 2 {
		t.Fatalf("trailing partial usage: %+v, %v", got, err)
	}
}

func TestFIMRefusesBeforeNetwork(t *testing.T) {
	p, calls := fimServer(t, fimSSE(fimFinish)+fimDone)
	refuse := func(req FIMOnceRequest) {
		t.Helper()
		if got, err := p.CompleteFIMOnce(context.Background(), req); err == nil || got.Accepted {
			t.Fatalf("accepted invalid request: %+v %v", got, err)
		}
	}
	for _, path := range []string{"", "/", "//evil/x", "https://evil/x", "x:y", "a@b", "x?y", "x#y",
		".", "../x", "a/../x", "a/./x", "%2e%2e/x", "%252e%252e/x", "x%2fy", "a\\b", "x//y", "x/", "x\n", " x", "x\x00"} {
		req := fimRequest()
		req.EndpointPath = path
		refuse(req)
	}
	for _, mutate := range []func(*FIMOnceRequest){
		func(r *FIMOnceRequest) { r.Dialect = "guess" },
		func(r *FIMOnceRequest) { r.Model = "" },
		func(r *FIMOnceRequest) { r.Prefix, r.Suffix = "", "" },
		func(r *FIMOnceRequest) { r.Prefix = "\x00multipart:[]" },
		func(r *FIMOnceRequest) { r.Prefix = "\xff" },
		func(r *FIMOnceRequest) { r.Suffix = "\a" },
		func(r *FIMOnceRequest) { r.Prefix = strings.Repeat("α", 128<<10) },
		func(r *FIMOnceRequest) { r.MaxOutputTokens = 0 },
		func(r *FIMOnceRequest) { r.MaxOutputTokens = 4097 },
		func(r *FIMOnceRequest) { r.MaxOutputBytes = 0 },
		func(r *FIMOnceRequest) { r.MaxOutputBytes = (64 << 10) + 1 },
	} {
		req := fimRequest()
		mutate(&req)
		refuse(req)
	}
	for _, mutate := range []func(*Provider){
		func(p *Provider) { p.StandardName = "anthropic" },
		func(p *Provider) { p.StandardName = "unknown" },
		func(p *Provider) { p.authType = AuthChatGPT },
		func(p *Provider) { p.SetOAuth(&OAuthCredentials{AccessToken: "no"}) },
		func(p *Provider) { p.extraBody = map[string]any{"model": "override"} },
		func(p *Provider) { p.extraBody = map[string]any{"unknown": true} },
		func(p *Provider) { p.protocolSettings = &ProtocolSettings{} },
		func(p *Provider) { p.thinkingEffort = "high" },
		func(p *Provider) { v := 0.1; p.SetSampling(&v, nil) },
		func(p *Provider) { p.headers = map[string]string{"Idempotency-Key": "x"} },
		func(p *Provider) { p.BaseURL += "?query=x" },
		func(p *Provider) { p.BaseURL += "#fragment" },
		func(p *Provider) { p.BaseURL += "/%2e%2e" },
	} {
		invalid := &Provider{BaseURL: p.BaseURL, StandardName: "openai"}
		mutate(invalid)
		if got, err := invalid.CompleteFIMOnce(context.Background(), fimRequest()); err == nil || got.Accepted {
			t.Errorf("invalid provider: %+v, %v", got, err)
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("invalid forms made %d requests", calls.Load())
	}
}

func TestFIMMalformedAndFailedStreamsPreserveObservations(t *testing.T) {
	choice := func(fields string) string { return fimSSE(`{"choices":[{` + fields + `}]}`) }
	text := func(raw string) string { return choice(`"index":0,"text":` + raw + `,"finish_reason":"stop"`) }
	usage := func(raw string) string { return fimSSE(`{"choices":[],"usage":{"completion_tokens":` + raw + `}}`) }
	for _, test := range []struct{ name, tail string }{
		{"EOF", ""}, {"finish without done", fimSSE(fimFinish)}, {"done without finish", fimDone},
		{"malformed", "data: {broken}\n\n"}, {"truncated", `data: {"choices":[`},
		{"error envelope", fimSSE(`{"error":{},"usage":{"completion_tokens":2}}`)},
		{"named error", "event: error\ndata: {}\n\n"},
		{"failed finish", choice(`"index":0,"text":"","finish_reason":"aborted"`) + fimDone},
		{"duplicate keys", fimSSE(`{"choices":[],"cho\u0069ces":[]}`)},
		{"multiple choices", fimSSE(`{"choices":[{"index":0},{"index":0}]}`)},
		{"nonzero index", choice(`"index":1,"text":"x","finish_reason":"stop"`)},
		{"missing finish", choice(`"index":0,"text":""`)},
		{"bad usage", usage("-1")}, {"null usage", usage("null")},
		{"fractional usage", usage("1.5")}, {"overflow usage", usage("999999999999999999999")},
		{"string usage", usage(`"2"`)},
		{"tools", choice(`"index":0,"text":"","tool_calls":[],"finish_reason":"stop"`)},
		{"reasoning", choice(`"index":0,"text":"","reasoning_content":"x","finish_reason":"stop"`)},
		{"message", choice(`"index":0,"message":{},"finish_reason":"stop"`)},
		{"multipart", text(`["x"]`)}, {"NUL", text(`"\u0000"`)}, {"control", text(`"\u001b"`)},
		{"UTF8", text("\"\xff\"")}, {"high surrogate", text(`"\ud800"`)}, {"low surrogate", text(`"\udfff"`)},
		{"wrong surrogate pair", text(`"\ud800\u0061"`)},
		{"nested", fimSSE(`{"choices":[],"usage":{"x":` + strings.Repeat("[", 20) + "0" + strings.Repeat("]", 20) + `}}`)},
		{"choice after finish", fimSSE(fimFinish, fimFinish) + fimDone},
	} {
		t.Run(test.name, func(t *testing.T) {
			p, calls := fimServer(t, fimSSE(fimPartial)+test.tail)
			got, err := p.CompleteFIMOnce(context.Background(), fimRequest())
			if err == nil || !got.Accepted || got.Text != " \tλ\n" || got.Usage == nil ||
				got.Usage.PromptTokens != 7 || got.UsageComplete || calls.Load() != 1 {
				t.Fatalf("result=%+v usage=%+v error=%v calls=%d", got, got.Usage, err, calls.Load())
			}
			if test.name == "error envelope" && got.Usage.CompletionTokens != 2 {
				t.Fatalf("error-envelope usage lost: %+v", got.Usage)
			}
		})
	}
}

func TestFIMCodestralRefusesNonTextForms(t *testing.T) {
	for _, delta := range []string{
		`{"content":[{"type":"text","text":"x"}]}`, `{"content":"x","tool_calls":[]}`,
		`{"content":"x","reasoning_content":"hidden"}`, `{"content":"x","role":"tool"}`,
	} {
		p, _ := fimServer(t, fimSSE(`{"choices":[{"index":0,"delta":`+delta+`,"finish_reason":"stop"}]}`)+fimDone)
		req := fimRequest()
		req.Dialect = FIMCodestral
		got, err := p.CompleteFIMOnce(context.Background(), req)
		if err == nil || got.Text != "" || !got.Accepted {
			t.Fatalf("delta=%s result=%+v error=%v", delta, got, err)
		}
	}
}

func TestFIMResponseBounds(t *testing.T) {
	for _, test := range []struct{ name, tail string }{
		{"output", fimSSE(`{"choices":[{"index":0,"text":"` + strings.Repeat("x", 2000) + `","finish_reason":null}]}`)},
		{"event", "data: " + strings.Repeat("x", (32<<10)+1) + "\n\n"},
		{"multiline event", "data: " + strings.Repeat("x", 17000) + "\ndata: " + strings.Repeat("y", 17000) + "\n\n"},
		{"count", strings.Repeat(fimSSE(`{"choices":[],"usage":{}}`), 2048)},
		{"total", strings.Repeat(":"+strings.Repeat("x", 8190)+"\n\n", 130)},
	} {
		t.Run(test.name, func(t *testing.T) {
			p, _ := fimServer(t, fimSSE(fimPartial)+test.tail)
			got, err := p.CompleteFIMOnce(context.Background(), fimRequest())
			if !errors.Is(err, ErrResponseTooLarge) || !got.Accepted || got.Usage == nil ||
				got.UsageComplete || !strings.HasPrefix(got.Text, " \tλ\n") || len(got.Text) > 1024 {
				t.Fatalf("text bytes=%d result=%+v error=%v", len(got.Text), got, err)
			}
		})
	}
	chunk := `{"choices":[{"index":0,"text":"` + strings.Repeat("x", 16384) + `","finish_reason":null}]}`
	p, _ := fimServer(t, strings.Repeat(fimSSE(chunk), 4)+fimSSE(fimFinish)+fimDone)
	req := fimRequest()
	req.MaxOutputBytes = 64 << 10
	got, err := p.CompleteFIMOnce(context.Background(), req)
	if err != nil || len(got.Text) != 64<<10 {
		t.Fatalf("exact text bound: bytes=%d error=%v", len(got.Text), err)
	}
}

func TestFIMSingleAttemptHTTPAndRedirect(t *testing.T) {
	for _, status := range []int{503, 429, 404, 301, 302, 303, 307, 308} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.URL.Path != "/mount/completions" || r.Method != "POST" {
					t.Errorf("alternate request: %s %s", r.Method, r.URL)
				}
				w.Header().Set("Location", "/alternate")
				w.Header().Set("Retry-After", "0")
				w.WriteHeader(status)
				_, _ = io.WriteString(w, `{"error":{"message":"refused"}}`)
			}))
			defer server.Close()
			p := &Provider{BaseURL: server.URL + "/mount", StandardName: "openai", dial: DialSettings{MaxAttempts: 6}}
			got, err := p.CompleteFIMOnce(context.Background(), fimRequest())
			var statusErr *StatusError
			if !errors.As(err, &statusErr) || got.Accepted || calls.Load() != 1 {
				t.Fatalf("result=%+v error=%v calls=%d", got, err, calls.Load())
			}
		})
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[]}`)
	}))
	defer server.Close()
	p := &Provider{BaseURL: server.URL, StandardName: "openai"}
	if got, err := p.CompleteFIMOnce(context.Background(), fimRequest()); err == nil || !got.Accepted {
		t.Fatalf("wrong framing: %+v, %v", got, err)
	}
}

func TestFIMCancelClosesOriginalResponse(t *testing.T) {
	ready, serverJoined := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(serverJoined)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, fimSSE(fimPartial))
		w.(http.Flusher).Flush()
		close(ready)
		<-r.Context().Done()
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type outcome struct {
		result FIMOnceResult
		err    error
	}
	returned := make(chan outcome, 1)
	p := &Provider{BaseURL: server.URL, StandardName: "openai"}
	go func() {
		result, err := p.CompleteFIMOnce(ctx, fimRequest())
		returned <- outcome{result, err}
	}()
	<-ready
	// Pace cancellation after flushed content, then assert actual retention.
	timer := time.AfterFunc(100*time.Millisecond, cancel)
	defer timer.Stop()
	select {
	case got := <-returned:
		if !errors.Is(got.err, context.Canceled) || !got.result.Accepted || got.result.UsageComplete ||
			got.result.Text != " \tλ\n" || got.result.Usage == nil || got.result.Usage.PromptTokens != 7 {
			t.Fatalf("cancelled result=%+v error=%v", got.result, got.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("local synchronous response reader did not join")
	}
	select {
	case <-serverJoined:
	case <-time.After(3 * time.Second):
		t.Fatal("original HTTP response context did not close")
	}
	// This proves only the local HTTP handler joined, not remote model compute.
}

func TestFIMSharedNetworkPolicy(t *testing.T) {
	preserveNetworkDefaults(t)
	var calls atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Host != "fim.invalid" || r.URL.Path != "/mounted/completions" ||
			r.Header.Get("Authorization") != "Bearer current-key" {
			t.Errorf("shared proxy/header path: %s %v", r.URL, r.Header)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, fimSSE(fimFinish)+fimDone)
	}))
	defer proxy.Close()
	settings := network.DefaultSettings()
	settings.ProxyMode, settings.ProxyURL = network.ProxyModeCustom, proxy.URL
	if err := network.ConfigureDefault(settings); err != nil {
		t.Fatal(err)
	}
	p := &Provider{BaseURL: "http://fim.invalid/mounted", StandardName: "openai", APIKey: "current-key"}
	if _, err := p.CompleteFIMOnce(context.Background(), fimRequest()); err != nil || calls.Load() != 1 {
		t.Fatalf("proxy calls=%d error=%v", calls.Load(), err)
	}
	settings.ProxyURL = "://invalid"
	if err := network.ConfigureStartupDefault(settings); err == nil {
		t.Fatal("invalid shared policy was accepted")
	}
	if got, err := p.CompleteFIMOnce(context.Background(), fimRequest()); err == nil || got.Accepted || calls.Load() != 1 {
		t.Fatalf("invalid policy bypassed: %+v, %v calls=%d", got, err, calls.Load())
	}
}
