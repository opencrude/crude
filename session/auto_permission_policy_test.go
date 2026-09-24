package session

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"crude/agent"
	"crude/provider"
	"crude/safety"
)

func TestReviewerOptInScopeAndExistingGrantSpendNothing(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		t.Error("ineligible review reached provider")
	}))
	defer server.Close()
	for _, mode := range []string{"off", "unconfigured", "child", "granted"} {
		t.Run(mode, func(t *testing.T) {
			s, a := newReviewerTestSession(t, server.URL)
			switch mode {
			case "off":
				s.cfg.Safety.AutoPermission.Enabled = "off"
			case "unconfigured":
				s.cfg.Safety.AutoPermission.Model = ""
			case "child":
				a.Depth = 1
			case "granted":
				s.permStore.GrantAlways(safety.OpUpdate, "edit_file", "main.go")
			}
			got := s.permissionReviewer(a)(t.Context(), provider.ToolCall{Function: provider.FunctionCall{Name: "edit_file"}}, map[string]any{"path": "main.go", "old_string": "old", "new_string": "new"}, safety.RiskProjectWrite, func(agent.AgentEvent) {})
			if got.Decision != "ask" {
				t.Fatalf("ineligible decision=%+v", got)
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatalf("unexpected review calls=%d", calls.Load())
	}
}

func TestReviewerDenialModeAndConcurrentBudget(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"{\"decision\":\"deny\",\"reason\":\"suspicious edit\"}"},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()
	s, a := newReviewerTestSession(t, server.URL)
	call := provider.ToolCall{Function: provider.FunctionCall{Name: "edit_file"}}
	params := map[string]any{"path": "main.go", "old_string": "old", "new_string": "new"}
	s.cfg.Safety.AutoPermission.DenyMode = "ask"
	if got := s.permissionReviewer(a)(t.Context(), call, params, safety.RiskProjectWrite, func(agent.AgentEvent) {}); got.Decision != "ask" {
		t.Fatal("default denied review did not ask human")
	}
	s.cfg.Safety.AutoPermission.DenyMode = "deny"
	s.cfg.Safety.AutoPermission.MaxReviewsPerTurn = 2
	calls.Store(0)
	review := s.permissionReviewer(a)
	var denials atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Go(func() {
			if got := review(t.Context(), call, params, safety.RiskProjectWrite, func(agent.AgentEvent) {}); got.Decision == "deny" {
				denials.Add(1)
			}
		})
	}
	wg.Wait()
	if calls.Load() != 2 || denials.Load() != 2 {
		t.Fatalf("concurrent cap: calls=%d denials=%d", calls.Load(), denials.Load())
	}
}
