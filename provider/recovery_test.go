package provider

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestStreamRetriesTransientGatewayEventBeforeOutput(t *testing.T) {
	var calls atomic.Int32
	s := testStallStream(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("data: {\"error\":{\"type\":\"gateway_stream_terminated\",\"message\":\"Upstream stream ended before terminal chunk\"}}\n\n"))}, nil
		}
		return sseResponse("recovered"), nil
	}))
	defer s.Close()
	u, err := s.Next()
	if err != nil || u.ContentDelta != "recovered" || calls.Load() != 2 {
		t.Fatalf("transient stream error killed request: %+v %v requests=%d", u, err, calls.Load())
	}
}

func TestStreamEOFWithoutTerminalIsNotSuccess(t *testing.T) {
	var calls atomic.Int32
	s := testStallStream(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("data: {\"choices\":[{\"delta\":{\"content\":\"accepted prefix\"}}]}\n\n"))}, nil
	}))
	defer s.Close()
	if _, err := s.Next(); err != nil {
		t.Fatal(err)
	}
	_, err := s.Next()
	if !errors.Is(err, io.ErrUnexpectedEOF) || calls.Load() != 1 {
		t.Fatalf("truncated stream was accepted or replayed: %v requests=%d", err, calls.Load())
	}
	if _, again := s.Next(); !errors.Is(again, io.EOF) || calls.Load() != 1 {
		t.Fatalf("reading after terminal failure reissued request: %v requests=%d", again, calls.Load())
	}
}

func TestExplicitRetryHeadersAndPermanentQuota(t *testing.T) {
	for _, tc := range []struct {
		status  int
		headers http.Header
		body    string
		want    bool
	}{
		{503, http.Header{"X-Should-Retry": []string{"false"}}, `{"error":{"type":"server_error"}}`, false},
		{429, http.Header{}, `{"error":{"code":"insufficient_quota","message":"billing limit"}}`, false},
		{529, http.Header{}, `{"error":{"type":"overloaded_error"}}`, true},
	} {
		e := readStatusError(&http.Response{StatusCode: tc.status, Header: tc.headers, Body: io.NopCloser(strings.NewReader(tc.body))}, 1<<20)
		if got := e.Retryable(); got != tc.want {
			t.Errorf("status %d retryable=%v want=%v", tc.status, got, tc.want)
		}
	}
	cfg := DefaultDialConfig()
	cfg.MaxBackoff = time.Second
	if got := serverHintedDelay(&http.Response{Header: http.Header{"Retry-After-Ms": []string{"1250"}}}, 1, cfg); got != 1250*time.Millisecond {
		t.Fatalf("server minimum retry delay shortened: %s", got)
	}
}

func TestRecoveryClassificationIsSpecific(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want bool
	}{
		{&net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET}, true},
		{&net.DNSError{Err: "server misbehaving", IsTemporary: true}, true},
		{&net.DNSError{Err: "no such host", IsNotFound: true}, false},
		{x509.UnknownAuthorityError{}, false},
		{context.Canceled, false},
		{context.DeadlineExceeded, true},
		{&APIError{Type: "invalid_request_error", Message: "invalid tool schema"}, false},
		{&APIError{Code: "insufficient_quota"}, false},
		{&APIError{Type: "overloaded_error"}, true},
		{&APIError{Code: "502"}, true},
		{&APIError{Code: "401"}, false},
	} {
		if got := Retryable(tc.err); got != tc.want {
			t.Errorf("%T: retryable=%v want=%v", tc.err, got, tc.want)
		}
	}
	cfg := DefaultDialConfig()
	if got := serverHintedDelay(&http.Response{Header: http.Header{"Retry-After": []string{"86400"}}}, 1, cfg); got != 5*time.Minute {
		t.Fatalf("long server cooldown=%s, want absolute five-minute ceiling", got)
	}
	if got := backoffDelay(1, DialConfig{BaseBackoff: time.Millisecond, MaxBackoff: time.Millisecond}); got > time.Millisecond {
		t.Fatalf("backoff ignored its configured ceiling: %s", got)
	}
}

func TestResponsesDisconnectAfterAcceptedToolFragmentIsTypedAndNotReplayed(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"accepted prefix\"}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"response.function_call_arguments.delta\",\"output_index\":0,\"delta\":\"{\\\"path\\\":\"}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"error\",\"error\":{\"type\":\"invalid_request_error\",\"message\":\"stream error: stream disconnected before completion: stream closed before response.completed\"}}\n\n")
	}))
	defer server.Close()

	p := FromPreset("openai-chat", "key", server.URL)
	if err := p.SetProtocolID("openai-responses"); err != nil {
		t.Fatal(err)
	}
	client, err := p.Client(WithMaxAttempts(3))
	if err != nil {
		t.Fatal(err)
	}
	stream, err := client.Stream(t.Context(), &ChatRequest{Model: "m", Messages: []ChatMessage{NewTextMessage("user", "hi")}})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()

	text, err := stream.Next()
	if err != nil || text.ContentDelta != "accepted prefix" || text.ContinuationID != "" {
		t.Fatalf("text update=%+v err=%v", text, err)
	}
	tool, err := stream.Next()
	if err != nil || len(tool.ToolCalls) != 1 || tool.ToolCalls[0].Arguments != `{"path":` || tool.ContinuationID != "" {
		t.Fatalf("tool fragment=%+v err=%v", tool, err)
	}
	_, err = stream.Next()
	var interrupted *StreamInterruptionError
	if !errors.As(err, &interrupted) || interrupted.Kind != StreamInterruptionProtocolError || !Retryable(err) {
		t.Fatalf("error=%T %v, want retryable typed protocol interruption", err, err)
	}
	var api *APIError
	if !errors.As(err, &api) || api.Type != "invalid_request_error" {
		t.Fatalf("underlying structured error lost: %T %v", err, err)
	}
	if requests.Load() != 1 {
		t.Fatalf("accepted stochastic request replayed: requests=%d", requests.Load())
	}
}

func TestResponsesDisconnectClassificationRequiresNarrowProtocolEvidence(t *testing.T) {
	decoder := (&OpenAIResponsesStandard{}).NewStreamDecoder()
	for _, tc := range []struct {
		name      string
		payload   string
		retryable bool
	}{
		{
			name:      "official disconnect diagnostic",
			payload:   `{"type":"error","error":{"type":"invalid_request_error","message":"stream error: stream disconnected before completion: stream closed before response.completed"}}`,
			retryable: true,
		},
		{
			name:      "deterministic schema rejection",
			payload:   `{"type":"error","error":{"type":"invalid_request_error","param":"tools[0]","message":"invalid tool schema"}}`,
			retryable: false,
		},
		{
			name:      "unstructured similar prose",
			payload:   `{"type":"error","error":{"type":"invalid_request_error","message":"stream disconnected"}}`,
			retryable: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := decoder.Decode(StreamEvent{Data: []byte(tc.payload)})
			if err == nil || Retryable(err) != tc.retryable {
				t.Fatalf("error=%T %v retryable=%v, want %v", err, err, Retryable(err), tc.retryable)
			}
		})
	}
	if Retryable(&StreamInterruptionError{Cause: context.Canceled}) {
		t.Fatal("explicit cancellation was reclassified as retryable interruption")
	}
}

func TestRegisteredProtocolsTypeMissingTerminalAfterAcceptedSemanticOutput(t *testing.T) {
	fixtures := map[string]struct {
		contentType string
		body        string
		assert      func(t *testing.T, update StreamUpdate)
	}{
		"anthropic": {
			contentType: "text/event-stream",
			body:        "data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"reason\"}}\n\n",
			assert: func(t *testing.T, update StreamUpdate) {
				if update.ReasoningText() != "reason" {
					t.Fatalf("reasoning update=%+v", update)
				}
			},
		},
		"codex": {
			contentType: "text/event-stream",
			body:        "data: {\"type\":\"response.function_call_arguments.delta\",\"output_index\":0,\"delta\":\"{\\\"path\\\":\"}\n\n",
			assert: func(t *testing.T, update StreamUpdate) {
				if len(update.ToolCalls) != 1 || update.ToolCalls[0].Arguments != `{"path":` {
					t.Fatalf("tool update=%+v", update)
				}
			},
		},
		"ollama": {
			contentType: "application/x-ndjson",
			body:        "{\"model\":\"m\",\"message\":{\"role\":\"assistant\",\"content\":\"partial\"}}\n",
			assert: func(t *testing.T, update StreamUpdate) {
				if update.ContentDelta != "partial" {
					t.Fatalf("content update=%+v", update)
				}
			},
		},
		"openai": {
			contentType: "text/event-stream",
			body:        "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"},\"finish_reason\":null}]}\n\n",
			assert: func(t *testing.T, update StreamUpdate) {
				if update.ContentDelta != "partial" {
					t.Fatalf("content update=%+v", update)
				}
			},
		},
		"openai-responses": {
			contentType: "text/event-stream",
			body:        "data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n",
			assert: func(t *testing.T, update StreamUpdate) {
				if update.ContentDelta != "partial" {
					t.Fatalf("content update=%+v", update)
				}
			},
		},
	}
	protocols := ProtocolList()
	if len(protocols) != len(fixtures) {
		ids := make([]string, 0, len(protocols))
		for _, protocol := range protocols {
			ids = append(ids, protocol.Info().ID)
		}
		t.Fatalf("fixture matrix does not cover registered protocols: %v", ids)
	}
	for _, protocol := range protocols {
		protocol := protocol
		fixture, ok := fixtures[protocol.Info().ID]
		if !ok {
			t.Fatalf("missing fixture for registered protocol %q", protocol.Info().ID)
		}
		t.Run(protocol.Info().ID, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				w.Header().Set("Content-Type", fixture.contentType)
				_, _ = fmt.Fprint(w, fixture.body)
			}))
			defer server.Close()
			client, err := NewClient(protocol, server.URL, "key", DialConfig{MaxAttempts: 3}, WithHTTPClient(server.Client()))
			if err != nil {
				t.Fatal(err)
			}
			stream, err := client.Stream(t.Context(), &ChatRequest{Model: "m", Messages: []ChatMessage{NewTextMessage("user", "hi")}})
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Close()
			update, err := stream.Next()
			if err != nil {
				t.Fatalf("semantic update: %v", err)
			}
			fixture.assert(t, update)
			_, err = stream.Next()
			var interrupted *StreamInterruptionError
			if !errors.As(err, &interrupted) || interrupted.Kind != StreamInterruptionMissingTerminal || interrupted.Protocol != protocol.Info().ID {
				t.Fatalf("terminal error=%T %v", err, err)
			}
			if requests.Load() != 1 {
				t.Fatalf("accepted request replayed: %d", requests.Load())
			}
		})
	}
}
