package provider

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

const gatewayStreamDiagnostic = "stream error: stream disconnected before completion: stream closed before response.completed"

func TestGatewayStreamDisconnectClientReplayBoundary(t *testing.T) {
	for _, fragment := range []string{"", `{"content":"accepted text"}`, `{"reasoning_content":"accepted thought"}`, `{"tool_calls":[{"index":0,"id":"partial","function":{"name":"write_file","arguments":"{\"path\":"}}]}`} {
		t.Run(fragment, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				if requests.Add(1) > 1 {
					_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"recovered\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
					return
				}
				if fragment != "" {
					_, _ = fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":%s}]}\n\n", fragment)
				}
				_, _ = fmt.Fprintf(w, "data: {\"error\":{\"type\":\"invalid_request_error\",\"message\":%q}}\n\n", gatewayStreamDiagnostic)
			}))
			defer server.Close()
			p := FromPreset("openai-chat", "key", server.URL)
			p.SetDialSettings(DialSettings{MaxAttempts: 2})
			client, err := p.Client()
			if err != nil {
				t.Fatal(err)
			}
			stream, err := client.Stream(t.Context(), &ChatRequest{Model: "m", Messages: []ChatMessage{NewTextMessage("user", "hi")}})
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Close()
			update, err := stream.Next()
			if fragment == "" {
				if err != nil || update.ContentDelta != "recovered" || requests.Load() != 2 {
					t.Fatalf("pre-output recovery: update=%+v error=%v requests=%d", update, err, requests.Load())
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			_, err = stream.Next()
			var interrupted *StreamInterruptionError
			var api *APIError
			if !errors.As(err, &interrupted) || !errors.As(err, &api) || api.Message != gatewayStreamDiagnostic || !Retryable(err) || requests.Load() != 1 {
				t.Fatalf("partial output must stand: error=%T %v requests=%d", err, err, requests.Load())
			}
		})
	}
}

func TestGatewayStreamDisconnectEveryRegisteredDecoder(t *testing.T) {
	for _, protocol := range ProtocolList() {
		t.Run(protocol.Info().ID, func(t *testing.T) {
			for _, envelope := range []string{
				fmt.Sprintf(`{"type":"error","error":{"type":"invalid_request_error","message":%q}}`, gatewayStreamDiagnostic),
				fmt.Sprintf(`{"error":%q}`, gatewayStreamDiagnostic),
			} {
				_, err := protocol.NewStreamDecoder().Decode(StreamEvent{Data: []byte(envelope)})
				var interrupted *StreamInterruptionError
				if !errors.As(err, &interrupted) || !Retryable(err) {
					t.Fatalf("translated gateway envelope=%s error=%T %v", envelope, err, err)
				}
			}
			for _, fields := range []string{
				`"type":"invalid_request_error","message":"stream disconnected"`,
				fmt.Sprintf(`"type":"invalid_request_error","param":"tools[0]","message":%q`, gatewayStreamDiagnostic),
				fmt.Sprintf(`"type":"authentication_error","message":%q`, gatewayStreamDiagnostic),
				fmt.Sprintf(`"type":"invalid_request_error","code":"insufficient_quota","message":%q`, gatewayStreamDiagnostic),
			} {
				_, err := protocol.NewStreamDecoder().Decode(StreamEvent{Data: []byte(`{"type":"error","error":{` + fields + `}}`)})
				if Retryable(err) {
					t.Fatalf("permanent/unknown error made transient: %v", err)
				}
			}
		})
	}
	if Retryable(&APIError{Type: "invalid_request_error", Message: gatewayStreamDiagnostic}) {
		t.Fatal("non-stream API error must not acquire stream evidence")
	}
}

func TestGatewayStreamDisconnectActualProtocolWires(t *testing.T) {
	fixtures := map[string]struct{ contentType, partial, complete string }{
		"openai":           {"text/event-stream", "data: {\"choices\":[{\"delta\":{\"content\":\"accepted\"}}]}\n\n", "data: {\"choices\":[{\"delta\":{\"content\":\"finished\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"},
		"anthropic":        {"text/event-stream", "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"accepted\"}}\n\n", "data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"finished\"}}\n\ndata: {\"type\":\"message_stop\"}\n\n"},
		"ollama":           {"application/x-ndjson", "{\"message\":{\"role\":\"assistant\",\"content\":\"accepted\"}}\n", "{\"message\":{\"role\":\"assistant\",\"content\":\"finished\"},\"done\":true}\n"},
		"openai-responses": {"text/event-stream", "data: {\"type\":\"response.output_text.delta\",\"delta\":\"accepted\"}\n\n", "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"finished\"}]}]}}\n\n"},
	}
	fixtures["codex"] = fixtures["openai-responses"]
	for _, protocol := range ProtocolList() {
		id := protocol.Info().ID
		fixture, found := fixtures[id]
		if !found {
			t.Fatalf("no wire fixture for %s", id)
		}
		for _, accepted := range []bool{false, true} {
			for _, variant := range []string{"object", "string", "auth", "quota", "validation", "veto", "veto-eof", "unknown"} {
				t.Run(fmt.Sprintf("%s/accepted=%t/%s", id, accepted, variant), func(t *testing.T) {
					var requests atomic.Int32
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						w.Header().Set("Content-Type", fixture.contentType)
						if variant == "veto" || variant == "veto-eof" {
							w.Header().Set("x-should-retry", "false")
						}
						if requests.Add(1) > 1 {
							_, _ = fmt.Fprint(w, fixture.complete)
							return
						}
						if accepted {
							_, _ = fmt.Fprint(w, fixture.partial)
						}
						if variant == "veto-eof" {
							return
						}
						envelope := fmt.Sprintf(`{"type":"error","error":{"type":"invalid_request_error","message":%q}}`, gatewayStreamDiagnostic)
						switch variant {
						case "string":
							envelope = fmt.Sprintf(`{"error":%q}`, gatewayStreamDiagnostic)
						case "auth":
							envelope = fmt.Sprintf(`{"type":"error","error":{"type":"authentication_error","message":%q}}`, gatewayStreamDiagnostic)
						case "quota":
							envelope = fmt.Sprintf(`{"type":"error","error":{"type":"invalid_request_error","code":"insufficient_quota","message":%q}}`, gatewayStreamDiagnostic)
						case "validation":
							envelope = fmt.Sprintf(`{"type":"error","error":{"type":"invalid_request_error","param":"tools[0]","message":%q}}`, gatewayStreamDiagnostic)
						case "unknown":
							envelope = `{"type":"error","error":{"type":"invalid_request_error","message":"stream disconnected"}}`
						}
						if id == "ollama" {
							_, _ = fmt.Fprintln(w, envelope)
						} else {
							_, _ = fmt.Fprintf(w, "event: error\ndata: %s\n\n", envelope)
						}
					}))
					defer server.Close()
					p := FromPreset("openai-chat", "key", server.URL)
					if err := p.SetProtocolID(id); err != nil {
						t.Fatal(err)
					}
					client, err := p.Client(WithMaxAttempts(2))
					if err != nil {
						t.Fatal(err)
					}
					// Keep the real wire path but eliminate transport cooldown cost.
					client.dial.BaseBackoff, client.dial.MaxBackoff = 0, 0
					stream, err := client.Stream(t.Context(), &ChatRequest{Model: "m", Messages: []ChatMessage{NewTextMessage("user", "hi")}})
					if err != nil {
						t.Fatal(err)
					}
					defer stream.Close()
					if accepted {
						update, err := stream.Next()
						if err != nil || update.ContentDelta != "accepted" {
							t.Fatalf("accepted prefix=%+v error=%v", update, err)
						}
					}
					update, err := stream.Next()
					transient := variant == "object" || variant == "string"
					if transient && !accepted {
						if err != nil || update.ContentDelta != "finished" || requests.Load() != 2 {
							t.Fatalf("safe retry failed: update=%+v error=%v requests=%d", update, err, requests.Load())
						}
					} else if err == nil || requests.Load() != 1 || Retryable(err) != transient {
						t.Fatalf("replay/classification: error=%v retryable=%t requests=%d", err, Retryable(err), requests.Load())
					}
				})
			}
		}
	}
}
