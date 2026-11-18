package provider

import (
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

const gatewayHTTPDisconnect = `{"error":{"type":"invalid_request_error","message":"stream error: stream disconnected before completion: stream closed before response.completed"}}`

func TestHTTPRecoveryUsesStatusInsteadOfGenericErrorEnvelope(t *testing.T) {
	for _, status := range []int{408, 409, 425, 429, 500, 502, 503, 504, 529} {
		if !Retryable(&APIError{Type: "invalid_request_error", Code: fmt.Sprint(status)}) {
			t.Errorf("streamed explicit status %d became a permanent fault", status)
		}
		for _, body := range []string{
			gatewayHTTPDisconnect,
			`{"error":{"type":"invalid_request_error","message":"upstream unavailable"}}`,
			`{"error":{"code":"invalid_request","message":"upstream unavailable"}}`,
		} {
			err := readStatusError(&http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, 1<<20)
			if !err.Retryable() || !Retryable(fmt.Errorf("request: %w", err)) {
				t.Errorf("HTTP %d generic envelope became a permanent fault: %v", status, err)
			}
			if status == 429 && ErrorSummary(err) != "Provider rate limit reached" {
				t.Errorf("temporary limit described as billing/configuration: %s", ErrorSummary(err))
			}
		}
	}
}

func TestHTTPRecoveryKeepsSpecificPermanentFailuresAndRetryVeto(t *testing.T) {
	for _, test := range []struct {
		name, body, hint string
		status           int
	}{
		{"request schema", gatewayHTTPDisconnect, "true", 400},
		{"authentication status", gatewayHTTPDisconnect, "true", 401},
		{"authorization status", gatewayHTTPDisconnect, "true", 403},
		{"missing endpoint", gatewayHTTPDisconnect, "true", 404},
		{"oversized input", gatewayHTTPDisconnect, "true", 413},
		{"validation status", gatewayHTTPDisconnect, "true", 422},
		{"quota", `{"error":{"type":"invalid_request_error","code":"insufficient_quota"}}`, "true", 429},
		{"billing", `{"error":{"code":"billing_hard_limit_reached"}}`, "true", 429},
		{"explicit authentication", `{"error":{"type":"authentication_error"}}`, "true", 503},
		{"explicit permission", `{"error":{"code":"permission_denied"}}`, "true", 503},
		{"explicit context limit", `{"error":{"code":"context_length_exceeded"}}`, "true", 503},
		{"explicit model fault", `{"error":{"code":"model_not_found"}}`, "true", 503},
		{"specific request fault", `{"error":{"code":"invalid_request_body"}}`, "true", 503},
		{"identified invalid parameter", `{"error":{"type":"invalid_request_error","param":"tools[0]","message":"invalid tool schema"}}`, "true", 503},
		{"explicit no retry", gatewayHTTPDisconnect, "false", 408},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := readStatusError(&http.Response{StatusCode: test.status, Header: http.Header{"X-Should-Retry": []string{test.hint}}, Body: io.NopCloser(strings.NewReader(test.body))}, 1<<20)
			if err.Retryable() || Retryable(err) {
				t.Fatalf("permanent fault retried: %v", err)
			}
		})
	}
	// A generic streamed error has no transient HTTP status as evidence.
	if Retryable(&APIError{Type: "invalid_request_error", Message: "upstream unavailable"}) {
		t.Fatal("untyped streamed request rejection became retryable")
	}
	for _, err := range []*APIError{
		{Type: "invalid_request_error", Code: "503", Param: "tools[0]"},
		{Type: "authentication_error", Code: "503"},
		{Type: "insufficient_quota", Code: "429"},
	} {
		if Retryable(err) {
			t.Fatalf("explicit permanent streamed evidence ignored: %v", err)
		}
	}
}

func TestHTTPGatewayDisconnectRetriesBothRequestModes(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", streaming), func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if requests.Add(1) == 1 {
					w.WriteHeader(http.StatusRequestTimeout)
					_, _ = fmt.Fprint(w, gatewayHTTPDisconnect)
					return
				}
				if streaming {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"finished\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
				} else {
					_, _ = fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"finished"},"finish_reason":"stop"}]}`)
				}
			}))
			defer server.Close()
			p := FromPreset("openai-chat", "key", server.URL)
			client, err := p.Client(WithMaxAttempts(2))
			if err != nil {
				t.Fatal(err)
			}
			client.dial.BaseBackoff, client.dial.MaxBackoff = time.Millisecond, time.Millisecond
			req := &ChatRequest{Model: "model", Messages: []ChatMessage{NewTextMessage("user", "task")}}
			var text string
			if streaming {
				stream, err := client.Stream(t.Context(), req)
				if err != nil {
					t.Fatal(err)
				}
				defer stream.Close()
				for {
					update, err := stream.Next()
					if errors.Is(err, io.EOF) {
						break
					}
					if err != nil {
						t.Fatal(err)
					}
					text += update.ContentDelta
				}
			} else {
				response, err := client.Complete(t.Context(), req)
				if err != nil {
					t.Fatal(err)
				}
				text = response.Choices[0].Message.ContentString()
			}
			if text != "finished" || requests.Load() != 2 {
				t.Fatalf("text=%q requests=%d", text, requests.Load())
			}
		})
	}
}

func TestStreamedStatusPreservesInvalidParameterEvidence(t *testing.T) {
	protocol := &OpenAIStandard{}
	for _, decode := range []func([]byte) error{
		func(raw []byte) error { _, err := protocol.DecodeResponse(raw); return err },
		func(raw []byte) error {
			_, err := protocol.NewStreamDecoder().Decode(StreamEvent{Data: raw})
			return err
		},
	} {
		for _, param := range []string{"", "tools[0]"} {
			err := decode([]byte(fmt.Sprintf(`{"error":{"type":"invalid_request_error","code":503,"param":%q,"message":"gateway failure"}}`, param)))
			var api *APIError
			if !errors.As(err, &api) || api.Param != param || Retryable(err) != (param == "") {
				t.Fatalf("adapter lost status/parameter evidence: %T %+v retryable=%t", err, err, Retryable(err))
			}
		}
	}
}
