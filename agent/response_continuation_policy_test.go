package agent

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"crude/provider"
)

func TestResponsesContinuationDiagnosticsRespectRequestPolicy(t *testing.T) {
	off, on := false, true
	policies := []struct {
		name     string
		settings *provider.ProtocolSettings
		extras   map[string]any
		enabled  bool
		store    bool
	}{
		{name: "default on", enabled: true, store: true},
		{name: "explicit on", settings: &provider.ProtocolSettings{Store: &on}, enabled: true, store: true},
		{name: "storage off", settings: &provider.ProtocolSettings{Store: &off}},
		{name: "off overrides extras", settings: &provider.ProtocolSettings{Store: &off}, extras: map[string]any{"store": true}},
		{name: "zero data retention", settings: &provider.ProtocolSettings{Store: &on, ZeroDataRetention: true}},
		{name: "extra storage off", extras: map[string]any{"store": false}},
		{name: "automatic truncation", settings: &provider.ProtocolSettings{Truncation: &on}, store: true},
	}
	outcomes := []struct {
		name   string
		fields string
		reason string
	}{
		{name: "not stored", fields: `"id":"response-2","store":false`, reason: "response_not_stored"},
		{name: "missing id", fields: `"store":true`, reason: "response_id_missing"},
		{name: "stored", fields: `"id":"response-2","store":true`, reason: "response_id_retained"},
	}
	for _, policy := range policies {
		for _, outcome := range outcomes {
			for _, sse := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/sse=%v", policy.name, outcome.name, sse), func(t *testing.T) {
					requests := 0
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						requests++
						var wire struct {
							Store    *bool             `json:"store"`
							Previous string            `json:"previous_response_id"`
							Input    []json.RawMessage `json:"input"`
						}
						if err := json.NewDecoder(r.Body).Decode(&wire); err != nil {
							t.Error(err)
							return
						}
						if wire.Store == nil || *wire.Store != policy.store {
							t.Errorf("request store = %v, want %v", wire.Store, policy.store)
						}
						if policy.enabled {
							if wire.Previous != "response-1" || len(wire.Input) != 1 {
								t.Errorf("enabled request did not use the retained prefix: previous=%q items=%d", wire.Previous, len(wire.Input))
							}
						} else if wire.Previous != "" || len(wire.Input) != 4 {
							t.Errorf("disabled request reused the retained prefix: previous=%q items=%d", wire.Previous, len(wire.Input))
						}
						response := fmt.Sprintf(`{%s,"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"answer"}]}],"usage":{"input_tokens":8,"output_tokens":2}}`, outcome.fields)
						if sse {
							w.Header().Set("Content-Type", "text/event-stream")
							_, _ = fmt.Fprintf(w, "data: {\"type\":\"response.completed\",\"response\":%s}\n\n", response)
						} else {
							w.Header().Set("Content-Type", "application/json")
							_, _ = fmt.Fprint(w, response)
						}
					}))
					defer server.Close()

					p := provider.FromPreset("openai", "key", server.URL)
					p.Model = "test-model"
					messages := []provider.ChatMessage{
						provider.NewTextMessage("system", "stable policy"),
						provider.NewTextMessage("user", "first prompt"),
					}
					previous := provider.NewTextMessage("assistant", "earlier answer")
					provider.BindResponseReference(&previous, &provider.ResponseReference{
						ID: "response-1", Scope: p.ResponseContinuationScope(p.Model),
						InputHash: provider.ResponseInputHash(messages, nil),
					})
					messages = append(messages, previous, provider.NewTextMessage("user", "next prompt"))
					p.SetProtocolSettings(policy.settings)
					p.SetExtraBody(policy.extras)
					a := newTestAgent(t, p, p.Model)
					result, err := a.completeStream(t.Context(), messages, nil, nil, nil, nil, nil, nil, nil, nil)
					if err != nil {
						t.Fatal(err)
					}
					wantReason := "disabled"
					if policy.enabled {
						wantReason = outcome.reason
					}
					if result.ResponseContinuationReason != wantReason {
						t.Errorf("continuation diagnostic = %q, want %q", result.ResponseContinuationReason, wantReason)
					}
					if (result.ResponseReference != nil) != (policy.enabled && outcome.name == "stored") {
						t.Errorf("retained response reference = %+v", result.ResponseReference)
					}
					if requests != 1 || result.Content != "answer" || result.Usage.PromptTokens != 8 || result.Usage.CompletionTokens != 2 {
						t.Errorf("normal completion changed: requests=%d result=%+v", requests, result)
					}
					if messages[2].ResponseReference == nil || messages[2].ResponseReference.ID != "response-1" {
						t.Fatal("request policy changed the historical response reference")
					}
				})
			}
		}
	}
}

func TestRunResponsesContinuationWarningRequiresEnabledStorage(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		for _, missingID := range []bool{false, true} {
			t.Run(fmt.Sprintf("enabled=%v/missing_id=%v", enabled, missingID), func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					fields := `"id":"response","store":false`
					if missingID {
						fields = `"store":true`
					}
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = fmt.Fprintf(w, "data: {\"type\":\"response.completed\",\"response\":{%s,\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"answer\"}]}]}}\n\n", fields)
				}))
				defer server.Close()
				p := provider.FromPreset("openai", "key", server.URL)
				p.Model = "test-model"
				p.SetProtocolSettings(&provider.ProtocolSettings{Store: &enabled})
				a := newTestAgent(t, p, p.Model)
				var events []AgentEvent
				a.Run(t.Context(), []provider.ChatMessage{provider.NewTextMessage("user", "hello")}, "system",
					func(event AgentEvent) { events = append(events, event) }, nil, "", nil)
				if !hasEvent(events, "done") || hasEvent(events, "error") {
					t.Fatalf("normal turn failed: %+v", events)
				}
				warnings := 0
				for _, event := range events {
					if event.Type == "response_continuation_unavailable" {
						warnings++
					}
				}
				wantWarnings := 0
				if enabled {
					wantWarnings = 1
				}
				if warnings != wantWarnings {
					t.Fatalf("delta-unavailable UI events = %d, want %d", warnings, wantWarnings)
				}
			})
		}
	}
}
