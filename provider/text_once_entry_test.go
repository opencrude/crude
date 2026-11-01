package provider

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

// Only the real wire is varied. Missing scalar counters and explicit zero
// counters must not yield indistinguishable accounting observations.
func TestPredictionTextOnceDistinguishesIncompleteUsageFromExplicitZero(t *testing.T) {
	for _, protocol := range []string{"openai", "openai-responses"} {
		t.Run(protocol, func(t *testing.T) {
			outcomes := make([]TextOnceResult, 0, 2)
			for _, usage := range []string{`{}`, `{"prompt_tokens":0,"completion_tokens":0}`} {
				if protocol == "openai-responses" {
					usage = strings.ReplaceAll(strings.ReplaceAll(usage, "prompt_tokens", "input_tokens"), "completion_tokens", "output_tokens")
				}
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					_, _ = io.Copy(io.Discard, r.Body)
					w.Header().Set("Content-Type", "text/event-stream")
					if protocol == "openai" {
						_, _ = io.WriteString(w, `data: {"choices":[{"index":0,"delta":{"content":"literal"},"finish_reason":"stop"}],"usage":`+usage+"}\n\ndata: [DONE]\n\n")
					} else {
						_, _ = io.WriteString(w, `data: {"type":"response.completed","response":{"status":"completed","output":[{"id":"m","type":"message","role":"assistant","content":[{"type":"output_text","text":"literal"}]}],"usage":`+usage+"}}\n\n")
					}
				}))
				p := &Provider{ID: "custom", BaseURL: srv.URL, StandardName: protocol, Model: "literal-model", APIKey: "fixture"}
				result, err := p.CompleteTextOnce(t.Context(), TextOnceRequest{Model: "literal-model", Input: "literal source", MaxOutputTokens: 32, MaxOutputBytes: 128})
				srv.Close()
				if err != nil || !result.Accepted || result.Text != "literal" || result.Usage == nil {
					t.Fatalf("literal usage observation was lost: result=%+v err=%v", result, err)
				}
				outcomes = append(outcomes, result)
			}
			if reflect.DeepEqual(outcomes[0], outcomes[1]) {
				t.Fatal("unreported token scalars became indistinguishable from complete explicit zero usage")
			}
		})
	}
}

func TestPredictionTextOnceOriginalWireCannotInventOrChooseCode(t *testing.T) {
	for _, tc := range []struct{ name, event string }{
		{"unpaired-surrogate", `{"choices":[{"index":0,"delta":{"content":"\ud800"},"finish_reason":"stop"}]}`},
		{"multiple-choices", `{"choices":[{"index":0,"delta":{"content":"first"},"finish_reason":"stop"},{"index":1,"delta":{"content":"second"},"finish_reason":"stop"}]}`},
		{"oversized-wire-event", `{"choices":[{"index":0,"delta":{"content":"literal"},"finish_reason":"stop"}],"irrelevant":"` + strings.Repeat("a", 512<<10) + `"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, "data: "+tc.event+"\n\ndata: [DONE]\n\n")
			}))
			defer srv.Close()
			p := &Provider{ID: "custom", BaseURL: srv.URL, StandardName: "openai", Model: "literal-model", APIKey: "fixture"}
			result, err := p.CompleteTextOnce(context.Background(), TextOnceRequest{Model: "literal-model", Input: "literal source", MaxOutputTokens: 32, MaxOutputBytes: 128})
			if err == nil {
				t.Fatalf("ambiguous or oversized original provider wire became usable code: case=%s text=%q", tc.name, result.Text)
			}
		})
	}
}
