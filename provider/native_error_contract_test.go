package provider

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// Vendor-native errors, not only a gateway's translated OpenAI envelope.
// References: platform.claude.com/docs/en/api/errors and docs.ollama.com/api/errors.
func TestNativeAnthropicAndOllamaStreamErrors(t *testing.T) {
	fixtures := []struct {
		protocol, name, envelope string
		retry                    bool
	}{
		{"anthropic", "overload", `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`, true},
		{"anthropic", "internal", `{"type":"error","error":{"type":"api_error","message":"Internal server error"}}`, true},
		{"anthropic", "timeout", `{"type":"error","error":{"type":"timeout_error","message":"Request timed out"}}`, true},
		{"anthropic", "rate limit", `{"type":"error","error":{"type":"rate_limit_error","message":"Rate limit exceeded"}}`, true},
		{"anthropic", "invalid request", `{"type":"error","error":{"type":"invalid_request_error","message":"Invalid input"}}`, false},
		{"anthropic", "authentication", `{"type":"error","error":{"type":"authentication_error","message":"Invalid API key"}}`, false},
		{"anthropic", "billing", `{"type":"error","error":{"type":"billing_error","message":"Billing limit reached"}}`, false},
		{"anthropic", "permission", `{"type":"error","error":{"type":"permission_error","message":"Access denied"}}`, false},
		{"anthropic", "not found", `{"type":"error","error":{"type":"not_found_error","message":"Resource missing"}}`, false},
		{"anthropic", "oversized", `{"type":"error","error":{"type":"request_too_large","message":"Request too large"}}`, false},
		{"ollama", "model execution", `{"error":"an error was encountered while running the model"}`, true},
		{"ollama", "generation failure", `{"error":"the model failed to generate a response"}`, true},
		{"ollama", "missing model", `{"error":"model 'missing' not found"}`, false},
		{"ollama", "invalid input", `{"error":"invalid JSON in request"}`, false},
		{"ollama", "resource requirement", `{"error":"model requires more system memory than is available"}`, false},
		{"ollama", "unknown", `{"error":"unrecognized backend failure"}`, false},
	}
	for _, tc := range fixtures {
		for _, accepted := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%s/accepted=%v", tc.protocol, tc.name, accepted), func(t *testing.T) {
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					n := requests.Add(1)
					if tc.protocol == "ollama" {
						w.Header().Set("Content-Type", "application/x-ndjson")
						if n > 1 {
							_, _ = fmt.Fprintln(w, `{"message":{"content":"finished"},"done":true}`)
							return
						}
						if accepted {
							_, _ = fmt.Fprintln(w, `{"message":{"content":"accepted"},"done":false}`)
						}
						_, _ = fmt.Fprintln(w, tc.envelope)
						return
					}
					w.Header().Set("Content-Type", "text/event-stream")
					if n > 1 {
						_, _ = fmt.Fprint(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"finished\"}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
						return
					}
					if accepted {
						_, _ = fmt.Fprint(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"accepted\"}}\n\n")
					}
					_, _ = fmt.Fprintf(w, "event: error\ndata: %s\n\n", tc.envelope)
				}))
				defer server.Close()
				p := FromPreset("openai-chat", "key", server.URL)
				if err := p.SetProtocolID(tc.protocol); err != nil {
					t.Fatal(err)
				}
				client, err := p.Client(WithMaxAttempts(2))
				if err != nil {
					t.Fatal(err)
				}
				client.dial.BaseBackoff, client.dial.MaxBackoff = 0, 0
				stream, err := client.Stream(t.Context(), &ChatRequest{Model: "m", Messages: []ChatMessage{NewTextMessage("user", "request")}})
				if err != nil {
					t.Fatal(err)
				}
				defer stream.Close()
				if accepted {
					if update, err := stream.Next(); err != nil || update.ContentDelta != "accepted" {
						t.Fatalf("partial output: %+v %v", update, err)
					}
				}
				update, err := stream.Next()
				if tc.retry && !accepted {
					if err != nil || update.ContentDelta != "finished" || requests.Load() != 2 {
						t.Fatalf("safe retry: %+v %v requests=%d", update, err, requests.Load())
					}
				} else if err == nil || Retryable(err) != tc.retry || requests.Load() != 1 {
					t.Fatalf("classification/replay: %v retry=%v requests=%d", err, Retryable(err), requests.Load())
				}
			})
		}
	}
}

func TestNativePermanentEvidenceOutranksTransientEnvelope(t *testing.T) {
	for _, kind := range []string{"permission_error", "not_found_error", "request_too_large", "billing_error", "authentication_error"} {
		err := &APIError{Type: kind, Code: "503", Message: gatewayStreamDiagnostic}
		if Retryable(err) || streamDisconnectEvidence(err) {
			t.Fatalf("permanent native %s retried", kind)
		}
	}
	for _, err := range []error{&APIError{Type: "api_error", Param: "input"}, &StatusError{StatusCode: 402, RetryHint: "true"}} {
		if Retryable(err) {
			t.Fatalf("permanent evidence retried: %v", err)
		}
	}
	var cause *APIError
	err := classifyStreamError("ollama", "", &APIError{Message: "an error was encountered while running the model"})
	if !errors.As(err, &cause) || cause.Message != "an error was encountered while running the model" {
		t.Fatal("native diagnostic lost")
	}
}
