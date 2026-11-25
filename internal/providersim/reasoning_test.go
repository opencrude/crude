package providersim

import (
	"net/http"
	"strings"
	"testing"
)

func TestJournalReasoningAsSent(t *testing.T) {
	for _, tc := range []struct {
		wire                       Wire
		field, setting, path, base string
	}{
		{Chat, "reasoning_effort", `"h\u0069gh"`, "/chat/completions", plainChat},
		{Responses, "reasoning", `{ "summary": "auto", "effort": "high" }`, "/responses", `{"model":"test","input":"hello"}`},
		{Codex, "reasoning", `{ "summary": "auto", "effort": "high" }`, "/responses", `{"model":"test","input":[],"stream":true,"store":false}`},
	} {
		t.Run(string(tc.wire), func(t *testing.T) {
			s := New(t)
			for _, want := range []string{"", tc.setting, "null"} {
				body := tc.base
				if want != "" {
					body = strings.TrimSuffix(body, "}") + `,"` + tc.field + `":` + want + `}`
				}
				r, err := http.NewRequestWithContext(t.Context(), http.MethodPost, s.BaseURL(tc.wire)+tc.path, strings.NewReader(body))
				if err != nil {
					t.Fatal(err)
				}
				s.Script(Text("accepted"))
				checkedTurn(t, tc.wire, do(t, r), tc.wire == Codex, http.StatusOK)
				req, _ := s.Last()
				if string(req.Reasoning) != want || (req.Reasoning == nil) != (want == "") {
					t.Fatalf("%s: got %q, want %q", tc.field, req.Reasoning, want)
				}
			}
		})
	}
}
