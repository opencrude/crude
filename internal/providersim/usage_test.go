package providersim

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestUsageOmissions(t *testing.T) {
	for _, tc := range []struct {
		wire               Wire
		input, read, usage string
	}{
		{Chat, "prompt_tokens", "cached_tokens", "usage"},
		{Responses, "input_tokens", "cached_tokens", "usage"},
		{Codex, "input_tokens", "cached_tokens", "usage"},
		{Messages, "input_tokens", "cache_read_input_tokens", "usage"},
		{Gemini, "promptTokenCount", "cachedContentTokenCount", "usageMetadata"},
		{Interactions, "total_input_tokens", "total_cached_tokens", "usage"},
		{Ollama, "prompt_eval_count", "prompt_eval_cached_count", "eval_count"},
	} {
		for _, stream := range []bool{false, true} {
			if tc.wire == Codex && !stream {
				continue // Codex validates stream:true before it accepts a turn.
			}
			t.Run(fmt.Sprintf("%s/stream=%v", tc.wire, stream), func(t *testing.T) {
				reports := make(chan string, 1)
				s := newServer(t, func(msg string, args ...any) { reports <- fmt.Sprintf(msg, args...) }, nil)
				for _, mode := range []string{"reported_zero", "omit_cache", "omit_all"} {
					u := Usage{Input: 100, Output: 7, OmitCacheRead: mode == "omit_cache"}
					turn := Text("answer").WithUsage(u)
					if mode == "omit_all" {
						turn = turn.WithoutUsage()
					}
					status := http.StatusOK
					if mode == "omit_all" && tc.wire == Messages ||
						mode == "omit_cache" && (tc.wire == Responses || tc.wire == Codex) {
						status = http.StatusInternalServerError
					}
					s.Script(turn)
					rows := checkedTurn(t, tc.wire, do(t, turnRequest(t, t.Context(), s, tc.wire, stream)), stream, status)
					if status != http.StatusOK {
						select {
						case msg := <-reports:
							if !strings.Contains(msg, "requires") {
								t.Fatal("missing usage requirement:", msg)
							}
						default:
							t.Fatal("invalid omission did not report misuse")
						}
						continue
					}
					raw := string(jsonBytes(rows))
					hasKey := func(k string) bool { return strings.Contains(raw, `"`+k+`":`) }
					if !strings.Contains(raw, `"answer"`) ||
						hasKey(tc.usage) != (mode != "omit_all") || hasKey(tc.input) != (mode != "omit_all") ||
						hasKey(tc.read) != (mode == "reported_zero") {
						t.Fatalf("%s: output or counter presence wrong: %s", mode, raw)
					}
					if mode == "reported_zero" && !strings.Contains(raw, `"`+tc.read+`":0`) {
						t.Fatalf("missing explicit zero: %s", raw)
					}
					if tc.wire == Chat && stream && mode == "omit_all" && (len(rows) != 3 || choice(rows[2])["finish_reason"] != "stop") {
						t.Fatal("expected the finish, without a usage-only chunk:", rows)
					}
				}
			})
		}
	}
}

func TestCompactionRequiresUsageFields(t *testing.T) {
	reports := make(chan string, 1)
	s := newServer(t, func(msg string, args ...any) { reports <- fmt.Sprintf(msg, args...) }, nil)
	s.Script(Turn{}.WithoutUsage(), Turn{}.WithUsage(Usage{OmitCacheRead: true}))
	responseJSON(t, postResponses(t, s, Responses, "/responses/compact", testInput("summarize")), 500, "ErrorResponse")
	if msg := <-reports; !strings.Contains(msg, "compaction response requires usage") {
		t.Fatal(msg)
	}
	b := messageBody(messageInput("user", "summarize"))
	b["compaction"] = messageObject{"type": "summarize"}
	messageErrorType(t, sendMessages(t, s, b, demandBeta), 500, "api_error", "BetaErrorResponse")
	if msg := <-reports; !strings.Contains(msg, "iteration usage requires cache_read_input_tokens") {
		t.Fatal(msg)
	}
}
