package providersim

import (
	"fmt"
	"testing"
)

func TestFaultKindsOnEveryWire(t *testing.T) {
	for _, tc := range []struct {
		kind                               FaultKind
		status                             int
		openAI                             any
		messages, gemini, interact, ollama string
	}{
		{ModelNotFound, 404, "model_not_found", "not_found_error", "NOT_FOUND", "model_not_found", "model not found"},
		{Quota, 429, "insufficient_quota", "rate_limit_error", "RESOURCE_EXHAUSTED", "quota_exceeded", ""},
		{BadGateway, 502, "server_error", "api_error", "UNAVAILABLE", "bad_gateway", "cloud model unavailable"},
		{InvalidRequest, 400, nil, "invalid_request_error", "INVALID_ARGUMENT", "invalid_request", "invalid request"},
		{RequestTimeout, 408, "request_timeout", "timeout_error", "DEADLINE_EXCEEDED", "request_timeout", "Request timed out."},
	} {
		for _, wire := range testWires {
			t.Run(string(wire)+"/"+string(tc.kind), func(t *testing.T) {
				var issues chan string
				report := t.Errorf
				if wire == Ollama && tc.kind == Quota {
					issues = make(chan string, 1)
					report = func(f string, args ...any) { issues <- fmt.Sprintf(f, args...) }
				}
				s := newServer(t, report, nil)
				for _, stream := range []bool{false, true} {
					turn, status := HTTPError(tc.kind), tc.status
					if stream {
						turn, status = Text("accepted").WithStreamError(1, tc.kind), 200
					}
					s.Script(turn.WithRetryHint(RetryAfterSeconds(3)))
					res := do(t, turnRequest(t, t.Context(), s, wire, true))
					if issues != nil {
						rows := checkedTurn(t, wire, res, false, 500)
						msg := "Ollama has no documented quota error. Use RateLimit for HTTP 429."
						if rows[0]["error"] != msg || <-issues != "simulate Ollama: "+msg {
							t.Fatal("unsupported quota fixture was not reported:", rows)
						}
						continue
					}
					rows := checkedTurn(t, wire, res, stream, status)
					if stream && len(rows) != 2 {
						t.Fatalf("expected one successful event followed by one error; got %v", rows)
					}
					if !stream && tc.kind == RequestTimeout && res.Header.Get("Retry-After") != "3" {
						t.Fatal("408 lost its Retry-After header")
					}
					row := rows[len(rows)-1]
					detail, _ := row["error"].(map[string]any)
					if stream && (wire == Responses || wire == Codex) {
						switch row["type"] {
						case "response.failed":
							detail = row["response"].(map[string]any)["error"].(map[string]any)
						case "error":
							detail = row
						default:
							t.Fatalf("expected a terminal failure: %v", row)
						}
					}
					msg := detail["message"]
					var got, want any
					switch wire {
					case Chat, Responses, Codex:
						got, want = detail["code"], tc.openAI
						if stream && wire == Codex && tc.kind == InvalidRequest {
							want = "invalid_request_error"
						}
					case Messages:
						got, want = detail["type"], tc.messages
						if tc.kind == Quota && (detail["details"].(map[string]any)["error_code"] != "enforced_spend_limit_reached" || res.Header.Get("Retry-After") != "") {
							t.Fatalf("ordinary rate limit instead of spend cap: %v", detail)
						}
					case Gemini:
						got, want = detail["status"], tc.gemini
						if fmt.Sprint(detail["code"]) != fmt.Sprint(tc.status) {
							t.Fatal(detail)
						}
						if tc.kind == Quota {
							ds := detail["details"].([]any)
							if ds[0].(map[string]any)["@type"] != "type.googleapis.com/google.rpc.QuotaFailure" ||
								!stream && (len(ds) != 2 || ds[1].(map[string]any)["retryDelay"] != "3s") {
								t.Fatalf("missing quota violation or retry hint: %v", ds)
							}
						}
					case Interactions:
						got, want = detail["code"], tc.interact
					case Ollama:
						got, want, msg = row["error"], tc.ollama, row["error"]
					}
					if got != want || msg == nil || msg == "" || tc.kind == RequestTimeout && msg != "Request timed out." {
						t.Fatalf("wrong fault: got %v, want %v: %v", got, want, row)
					}
				}
			})
		}
	}
}
