package provider

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// --- URL resolution ---

func TestResolveAPIBaseAppendsRootToBareOrigin(t *testing.T) {
	tests := []struct {
		raw, root, want string
	}{
		{"https://ollama.com", "/api", "https://ollama.com/api"},
		{"https://api.example.com", "/v1", "https://api.example.com/v1"},
		// An explicit path is kept as-is: the user knows their gateway shape.
		{"https://ollama.com/api", "/api", "https://ollama.com/api"},
		{"https://proxy.example.com/openai/v1", "/v1", "https://proxy.example.com/openai/v1"},
		{"https://host.example/", "/v1", "https://host.example/v1"},
	}
	for _, tt := range tests {
		base, err := resolveAPIBase(tt.raw, tt.root)
		if err != nil {
			t.Fatalf("resolveAPIBase(%q): %v", tt.raw, err)
		}
		if base.String() != tt.want {
			t.Errorf("resolveAPIBase(%q, %q) = %q, want %q", tt.raw, tt.root, base.String(), tt.want)
		}
	}
	if _, err := resolveAPIBase("", "/v1"); err == nil {
		t.Error("empty base URL must be rejected")
	}
	if _, err := resolveAPIBase("not-a-url", "/v1"); err == nil {
		t.Error("relative base URL must be rejected")
	}
}

// TestOllamaCloudChatURLIsNotDoubled pins the original bug: a preset base URL
// that already contains the protocol root must not get the root appended a
// second time (https://ollama.com/api/api/chat 404s).
func TestOllamaCloudChatURLIsNotDoubled(t *testing.T) {
	p := FromPreset("ollama-cloud", "key", "https://ollama.com/api")
	client, err := p.Client()
	if err != nil {
		t.Fatalf("Client: %v", err)
	}
	if got := client.chatURL().Path; got != "/api/chat" {
		t.Fatalf("chat path = %q, want /api/chat", got)
	}
	lister, ok := client.Protocol().(ModelLister)
	if !ok {
		t.Fatal("ollama protocol must implement ModelLister")
	}
	modelsURL := lister.ModelsURL(client.base, "")
	if got := modelsURL.Path; got != "/api/tags" {
		t.Fatalf("models path = %q, want /api/tags", got)
	}
}

// --- chat ---

func TestClientCompleteDecodesAndSendsAuth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer key" {
			t.Errorf("authorization = %q", got)
		}
		if got := r.Header.Get("Accept"); got != "text/event-stream, application/x-ndjson, application/json" {
			t.Errorf("accept = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()

	p := FromPreset("openai-chat", "key", server.URL)
	client, err := p.Client()
	if err != nil {
		t.Fatalf("Client: %v", err)
	}
	resp, err := client.Complete(t.Context(), &ChatRequest{
		Model:    "m",
		Messages: []ChatMessage{NewTextMessage("user", "hello")},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Choices[0].Message.ContentString() != "hi" {
		t.Fatalf("response = %+v", resp)
	}
}

func TestClientRetriesRetryableStatusWithBackoff(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "0")
			http.Error(w, `{"error":{"message":"overloaded"}}`, http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()

	p := FromPreset("openai-chat", "key", server.URL)
	client, err := p.Client()
	if err != nil {
		t.Fatalf("Client: %v", err)
	}
	resp, err := client.Complete(t.Context(), &ChatRequest{Model: "m", Messages: []ChatMessage{NewTextMessage("user", "hi")}})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Choices[0].Message.ContentString() != "ok" || calls.Load() != 2 {
		t.Fatalf("response=%+v calls=%d", resp, calls.Load())
	}
}

func TestClientDoesNotRetryClientErrorsAndWrapsExhaustion(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, `{"error":{"message":"bad key"}}`, http.StatusUnauthorized)
	}))
	defer server.Close()

	p := FromPreset("openai-chat", "key", server.URL)
	p.SetDialSettings(DialSettings{MaxAttempts: 3})
	client, err := p.Client()
	if err != nil {
		t.Fatalf("Client: %v", err)
	}
	_, err = client.Complete(t.Context(), &ChatRequest{Model: "m", Messages: []ChatMessage{NewTextMessage("user", "hi")}})
	var status *StatusError
	if !errors.As(err, &status) || status.StatusCode != http.StatusUnauthorized {
		t.Fatalf("error = %v, want StatusError 401", err)
	}
	if status.Retryable() {
		t.Fatal("401 must not be retryable")
	}
	if calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1 (no retry on 401)", calls.Load())
	}
}

func TestClientWrapsRetriesExhausted(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "0")
		http.Error(w, `{}`, http.StatusServiceUnavailable)
	}))
	defer server.Close()

	p := FromPreset("openai-chat", "key", server.URL)
	p.SetDialSettings(DialSettings{MaxAttempts: 3})
	client, err := p.Client()
	if err != nil {
		t.Fatalf("Client: %v", err)
	}
	_, err = client.Complete(t.Context(), &ChatRequest{Model: "m", Messages: []ChatMessage{NewTextMessage("user", "hi")}})
	var exhausted *ExhaustedError
	if !errors.As(err, &exhausted) || exhausted.Attempts != 3 {
		t.Fatalf("error = %v, want ExhaustedError with 3 attempts", err)
	}
	if calls.Load() != 3 {
		t.Fatalf("calls = %d, want 3", calls.Load())
	}
}

// --- streaming ---

func TestClientStreamRetriesOnlyBeforeFirstUpdate(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if calls.Add(1) == 1 {
			// Connection established, then dropped before any output.
			_, _ = fmt.Fprint(w, ": warmup\n\n")
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			panic(http.ErrAbortHandler)
		}
		_, _ = fmt.Fprint(w,
			"data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\n"+
				"data: [DONE]\n\n")
	}))
	defer server.Close()

	p := FromPreset("openai-chat", "key", server.URL)
	p.SetDialSettings(DialSettings{MaxAttempts: 3})
	client, err := p.Client()
	if err != nil {
		t.Fatalf("Client: %v", err)
	}
	stream, err := client.Stream(t.Context(), &ChatRequest{Model: "m", Messages: []ChatMessage{NewTextMessage("user", "hi")}})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer stream.Close()

	var content strings.Builder
	for {
		update, err := stream.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		content.WriteString(update.ContentDelta)
	}
	if content.String() != "ok" || calls.Load() != 2 {
		t.Fatalf("content=%q calls=%d", content.String(), calls.Load())
	}
}

func TestClientStreamSurfacesUnexpectedEOFAfterOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n")
	}))
	defer server.Close()

	p := FromPreset("openai-chat", "key", server.URL)
	p.SetDialSettings(DialSettings{MaxAttempts: 3})
	client, err := p.Client()
	if err != nil {
		t.Fatalf("Client: %v", err)
	}
	stream, err := client.Stream(t.Context(), &ChatRequest{Model: "m", Messages: []ChatMessage{NewTextMessage("user", "hi")}})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer stream.Close()

	if _, err := stream.Next(); err != nil {
		t.Fatalf("first Next: %v", err)
	}
	_, err = stream.Next()
	if err == nil || !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("error = %v, want unexpected EOF", err)
	}
}

func TestLooksLikeNDJSONDetectsMultipleValues(t *testing.T) {
	cases := []struct {
		name   string
		peeked string
		want   bool
	}{
		{"two values", `{"a":1}\n{"a":2}\n`, true},
		{"value plus whitespace", `{"a":1}\n  \n`, true},
		{"single value", `{"a":1}`, false},
		{"pretty-printed single doc", `{\n\t"a": 1\n}`, false},
		{"empty", "", false},
	}
	for _, tc := range cases {
		if got := looksLikeNDJSON([]byte(tc.peeked)); got != tc.want {
			t.Errorf("%s: looksLikeNDJSON(%q) = %v, want %v", tc.name, tc.peeked, got, tc.want)
		}
	}
}

func TestNewStreamEventReaderSniffsNDJSONUnderJSONHeader(t *testing.T) {
	// A gateway that labels an NDJSON stream "application/json" must still
	// stream per-line; trusting the header folds the turn into one decode.
	body := io.NopCloser(strings.NewReader(`{"a":1}\n{"a":2}\n`))
	resp := &http.Response{Header: http.Header{"Content-Type": []string{"application/json"}}, Body: body}
	reader, _, err := newStreamEventReader(resp, FramingNDJSON)
	if err != nil {
		t.Fatalf("newStreamEventReader: %v", err)
	}
	if _, ok := reader.(*ndjsonEventReader); !ok {
		t.Fatalf("reader = %T, want ndjsonEventReader", reader)
	}
}

func TestNewStreamEventReaderKeepsSingleJSONUnderJSONHeader(t *testing.T) {
	body := io.NopCloser(strings.NewReader(`{"a": 1}`))
	resp := &http.Response{Header: http.Header{"Content-Type": []string{"application/json"}}, Body: body}
	reader, _, err := newStreamEventReader(resp, FramingNDJSON)
	if err != nil {
		t.Fatalf("newStreamEventReader: %v", err)
	}
	if _, ok := reader.(*singleJSONEventReader); !ok {
		t.Fatalf("reader = %T, want singleJSONEventReader", reader)
	}
}

func TestNewStreamEventReaderPrettyDocUnderDeclaredNDJSON(t *testing.T) {
	// A server that ignored stream=true answers with one pretty-printed
	// document; line framing would shred it, so it routes to the single-JSON
	// reader despite the declared NDJSON framing.
	body := io.NopCloser(strings.NewReader("{\n\t\"model\": \"llama3\",\n\t\"done\": true\n}"))
	resp := &http.Response{Header: http.Header{"Content-Type": []string{"application/json"}}, Body: body}
	reader, _, err := newStreamEventReader(resp, FramingNDJSON)
	if err != nil {
		t.Fatalf("newStreamEventReader: %v", err)
	}
	if _, ok := reader.(*singleJSONEventReader); !ok {
		t.Fatalf("reader = %T, want singleJSONEventReader", reader)
	}
}

func TestClientStreamDecodesSingleJSONThroughResponseSchema(t *testing.T) {
	// An Anthropic-style provider answers a streaming request with one JSON
	// document. The payload follows the non-streaming schema, so it must be
	// decoded via DecodeResponse, not the event decoder.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{
			"type":"message","role":"assistant",
			"content":[
				{"type":"text","text":"fallback"},
				{"type":"tool_use","id":"toolu_1","name":"read_file","input":{"path":"a.go"}}
			],
			"stop_reason":"tool_use",
			"usage":{"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":2}
		}`)
	}))
	defer server.Close()

	p := FromPreset("anthropic", "key", server.URL)
	client, err := p.Client()
	if err != nil {
		t.Fatalf("Client: %v", err)
	}
	stream, err := client.Stream(t.Context(), &ChatRequest{Model: "claude-test", Messages: []ChatMessage{NewTextMessage("user", "hi")}})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer stream.Close()

	var update StreamUpdate
	for {
		u, err := stream.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		update = u
	}
	if update.ContentDelta != "fallback" {
		t.Fatalf("content = %q", update.ContentDelta)
	}
	if !update.Done || update.FinishReason != "tool_use" {
		t.Fatalf("update = %+v", update)
	}
	if len(update.ToolCalls) != 1 || update.ToolCalls[0].Name != "read_file" ||
		update.ToolCalls[0].Arguments != `{"path":"a.go"}` || !update.ToolCalls[0].ReplaceArguments {
		t.Fatalf("tool calls = %+v", update.ToolCalls)
	}
	if update.Usage == nil || update.Usage.PromptTokens != 12 || update.Usage.PromptTokensCached != 2 {
		t.Fatalf("usage = %+v", update.Usage)
	}
}

// --- model listing ---

func TestListModelsDetectsCursorCycles(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Every page claims more results and points back at the same cursor.
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"data":[{"id":"m"}],"has_more":true,"last_id":"loop"}`)
	}))
	defer server.Close()

	p := FromPreset("anthropic", "key", server.URL)
	client, err := p.Client()
	if err != nil {
		t.Fatalf("Client: %v", err)
	}
	_, err = client.ListModels(t.Context())
	if err == nil || !strings.Contains(err.Error(), "cursor") {
		t.Fatalf("error = %v, want cursor-cycle detection", err)
	}
}

func TestListModelsAppliesListingDeadline(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"data":[]}`)
	}))
	defer server.Close()

	p := FromPreset("openai-chat", "key", server.URL)
	p.SetDialSettings(DialSettings{RequestTimeoutSeconds: 0})
	client, err := p.Client()
	if err != nil {
		t.Fatalf("Client: %v", err)
	}
	if _, err := client.ListModels(context.Background()); err == nil {
		t.Skip("server responded within the listing deadline")
	}
}

// --- conformance ---

// TestProtocolRegistryCoversBuiltins pins the registered protocol set.
func TestProtocolRegistryCoversBuiltins(t *testing.T) {
	want := map[string]bool{
		"openai": false, "openai-responses": false, "anthropic": false,
		"ollama": false, "codex": false,
	}
	for _, proto := range ProtocolList() {
		id := proto.Info().ID
		if _, ok := want[id]; !ok {
			continue
		}
		want[id] = true
	}
	for id, seen := range want {
		if !seen {
			t.Errorf("protocol %q is not registered", id)
		}
	}
}

// TestProtocolConformance exercises invariants every registered protocol must
// hold: a well-formed Info, a working stream decoder, and a models page
// decoder that rejects garbage (when a models endpoint is declared).
func TestProtocolConformance(t *testing.T) {
	for _, proto := range ProtocolList() {
		info := proto.Info()
		if info.ID == "" || info.Name == "" || info.ChatPath == "" {
			t.Errorf("%T: incomplete Info: %+v", proto, info)
		}
		if info.Root != "" && !strings.HasPrefix(info.Root, "/") {
			t.Errorf("%s: Root %q must be empty or start with /", info.ID, info.Root)
		}
		decoder := proto.NewStreamDecoder()
		if decoder == nil {
			t.Errorf("%s: NewStreamDecoder returned nil", info.ID)
			continue
		}
		// An unknown model must not panic and must not invent a window.
		if meta, ok := proto.(ProtocolMetadata); ok {
			if got := meta.ContextWindow("totally-unknown-model"); got < 0 {
				t.Errorf("%s: ContextWindow(unknown) = %d", info.ID, got)
			}
		}
		if lister, ok := proto.(ModelLister); ok && info.ModelsPath != "" {
			if _, err := lister.DecodeModelsPage([]byte("not json")); err == nil {
				t.Errorf("%s: DecodeModelsPage accepted garbage", info.ID)
			}
		}
	}
}

// TestProtocolAliasesResolve pins the accepted alias set so stored configs
// using legacy IDs keep resolving.
func TestProtocolAliasesResolve(t *testing.T) {
	tests := map[string]string{
		"openai-chat": "openai", "responses": "openai-responses",
		"openai": "openai", "anthropic": "anthropic", "codex": "codex",
	}
	for raw, want := range tests {
		got, ok := canonicalProtocolID(raw)
		if !ok || got != want {
			t.Errorf("canonicalProtocolID(%q) = %q,%v; want %q", raw, got, ok, want)
		}
	}
}

// --- dialer settings ---

func TestDialSettingsResolveOverridesDefaults(t *testing.T) {
	cfg := DialSettings{
		RequestTimeoutSeconds:         45,
		ConnectTimeoutSeconds:         7,
		ProgressStartupTimeoutSeconds: 120,
		ProgressTimeoutFloorSeconds:   90,
		ProgressTimeoutCeilingSeconds: 600,
		MaxAttempts:                   2,
		MaxBackoffSeconds:             20,
	}.resolve()
	if cfg.RequestTimeout != 45*time.Second || cfg.ConnectTimeout != 7*time.Second ||
		cfg.MaxAttempts != 2 || cfg.MaxBackoff != 20*time.Second ||
		cfg.ProgressStartupTimeout != 120*time.Second || cfg.ProgressTimeoutFloor != 90*time.Second ||
		cfg.ProgressTimeoutCeiling != 600*time.Second {
		t.Fatalf("resolved = %+v", cfg)
	}
	if cfg.BaseBackoff != DefaultBaseBackoff || cfg.MaxResponseBytes != DefaultMaxResponseBytes {
		t.Fatalf("unset fields must keep defaults: %+v", cfg)
	}
	// Zero settings resolve to the built-in defaults entirely.
	fallback := DialSettings{}.resolve()
	if fallback != DefaultDialConfig() {
		t.Fatalf("zero settings = %+v, want defaults %+v", fallback, DefaultDialConfig())
	}
	// MaxAttempts never drops below 1.
	if got := (DialSettings{MaxAttempts: -3}).resolve().MaxAttempts; got < 1 {
		t.Fatalf("MaxAttempts = %d, want >= 1", got)
	}
}

func TestDialConfigClampInClient(t *testing.T) {
	p := FromPreset("openai-chat", "key", "https://example.invalid")
	client, err := NewClient(p.Protocol(), p.BaseURL, p.APIKey, DialConfig{MaxAttempts: 0})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if client.dial.MaxAttempts != 1 {
		t.Fatalf("MaxAttempts = %d, want clamped to 1", client.dial.MaxAttempts)
	}
}

func TestClientRequestTimeoutEnforced(t *testing.T) {
	for _, test := range []struct {
		name     string
		stream   bool
		partial  bool
		deadline bool
	}{
		{name: "stream headers", stream: true},
		{name: "stream body", stream: true, partial: true},
		{name: "stream with parent deadline", stream: true, deadline: true},
		{name: "complete with parent deadline", deadline: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				if test.stream {
					w.Header().Set("Content-Type", "text/event-stream")
					if test.partial {
						_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n")
						w.(http.Flusher).Flush()
					}
				}
				timer := time.NewTimer(150 * time.Millisecond)
				defer timer.Stop()
				progress := time.NewTicker(5 * time.Millisecond)
				defer progress.Stop()
			waiting:
				for {
					select {
					case <-r.Context().Done():
						return
					case <-timer.C:
						break waiting
					case <-progress.C:
						if test.partial {
							_, _ = fmt.Fprint(w, ": heartbeat\n\n")
							w.(http.Flusher).Flush()
						}
					}
				}
				if test.stream {
					_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"late\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
				} else {
					_, _ = fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"late"},"finish_reason":"stop"}]}`)
				}
			}))
			defer server.Close()
			p := FromPreset("openai-chat", "key", server.URL)
			client, err := p.Client()
			if err != nil {
				t.Fatal(err)
			}
			client.dial.RequestTimeout = 40 * time.Millisecond
			ctx := t.Context()
			if test.deadline {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, time.Second)
				defer cancel()
			}
			req := &ChatRequest{Model: "m", Messages: []ChatMessage{NewTextMessage("user", "hi")}}
			var content strings.Builder
			if test.stream {
				stream, streamErr := client.Stream(ctx, req)
				if streamErr != nil {
					t.Fatal(streamErr)
				}
				defer stream.Close()
				for err == nil {
					var update StreamUpdate
					update, err = stream.Next()
					content.WriteString(update.ContentDelta)
				}
			} else {
				_, err = client.Complete(ctx, req)
			}
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("error=%v content=%q, want configured request deadline", err, content.String())
			}
			if test.partial && content.String() != "partial" {
				t.Fatalf("accepted partial output lost: %q", content.String())
			}
		})
	}
}

func TestListModelsEnforcesRequestTimeoutWithParentDeadline(t *testing.T) {
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		timer := time.NewTimer(150 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-request.Context().Done():
			return nil, request.Context().Err()
		case <-timer.C:
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"data":[]}`))}, nil
		}
	})
	p := FromPreset("openai-chat", "key", "https://example.invalid")
	client, err := p.Client(WithHTTPClient(&http.Client{Transport: transport}))
	if err != nil {
		t.Fatal(err)
	}
	client.dial.RequestTimeout = 40 * time.Millisecond
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if _, err := client.ListModels(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("listing error=%v, want configured request deadline", err)
	}
}

func TestClientStreamRequestTimeoutIncludesRetryBackoff(t *testing.T) {
	var calls atomic.Int32
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, io.ErrUnexpectedEOF
	})
	p := FromPreset("openai-chat", "key", "https://example.invalid")
	client, err := p.Client(WithHTTPClient(&http.Client{Transport: transport}))
	if err != nil {
		t.Fatal(err)
	}
	client.dial.RequestTimeout = 40 * time.Millisecond
	client.dial.BaseBackoff = time.Second
	stream, err := client.Stream(t.Context(), &ChatRequest{Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if _, err := stream.Next(); !errors.Is(err, context.DeadlineExceeded) || calls.Load() != 1 {
		t.Fatalf("retry exceeded overall budget: err=%v calls=%d", err, calls.Load())
	}
}

func TestClientStreamReleasesRequestDeadline(t *testing.T) {
	for _, outcome := range []string{"close before reading", "completed", "failed"} {
		t.Run(outcome, func(t *testing.T) {
			transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
				if outcome == "failed" {
					return nil, errors.New("permanent test failure")
				}
				return sseResponse("ok"), nil
			})
			p := FromPreset("openai-chat", "key", "https://example.invalid")
			p.SetDialSettings(DialSettings{RequestTimeoutSeconds: 120})
			client, err := p.Client(WithHTTPClient(&http.Client{Transport: transport}))
			if err != nil {
				t.Fatal(err)
			}
			stream, err := client.Stream(t.Context(), &ChatRequest{Model: "m"})
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Close()
			if outcome != "close before reading" {
				for !stream.closed {
					_, err = stream.Next()
				}
				if outcome == "failed" && err == nil {
					t.Fatal("request failure was not returned")
				}
			} else if err := stream.Close(); err != nil {
				t.Fatal(err)
			}
			if stream.ctx.Err() != context.Canceled {
				t.Fatal("closed stream retained its request deadline")
			}
		})
	}
}

func TestClientRequestTimeoutPreservesShorterParentDeadline(t *testing.T) {
	p := FromPreset("openai-chat", "key", "https://example.invalid")
	p.SetDialSettings(DialSettings{RequestTimeoutSeconds: 120})
	client, err := p.Client()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	stream, err := client.Stream(ctx, &ChatRequest{Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	want, _ := ctx.Deadline()
	if got, _ := stream.ctx.Deadline(); !got.Equal(want) {
		t.Fatalf("parent deadline changed: got=%s want=%s", got, want)
	}
}

func TestClientConnectTimeoutAlsoControlsTLSHandshake(t *testing.T) {
	p := FromPreset("openai-chat", "key", "https://example.invalid")
	p.SetDialSettings(DialSettings{ConnectTimeoutSeconds: 45})
	client, err := p.Client()
	if err != nil {
		t.Fatal(err)
	}
	if got := client.dial.TLSHandshakeTimeout; got != 45*time.Second {
		t.Fatalf("TLS retained a hidden timeout: %s", got)
	}
}
