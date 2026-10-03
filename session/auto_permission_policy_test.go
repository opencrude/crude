package session

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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
	for _, mode := range []string{"off", "unconfigured", "granted"} {
		t.Run(mode, func(t *testing.T) {
			s, a := newReviewerTestSession(t, server.URL)
			switch mode {
			case "off":
				s.cfg.Safety.AutoPermission.Enabled = "off"
			case "unconfigured":
				s.cfg.Safety.AutoPermission.Model = ""
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

func TestReviewerUnresolvedDenyModeNeverRequiresHumanInput(t *testing.T) {
	var calls atomic.Int32
	mode := "ask"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if mode == "malformed" {
			_, _ = fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"not json"},"finish_reason":"stop"}]}`)
			return
		}
		_, _ = fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"{\"decision\":\"ask\",\"reason\":\"intent is unclear\"}"},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()

	s, a := newReviewerTestSession(t, server.URL)
	s.cfg.Safety.AutoPermission.UnresolvedMode = "deny"
	call := provider.ToolCall{Function: provider.FunctionCall{Name: "edit_file"}}
	params := map[string]any{"path": "main.go", "old_string": "old", "new_string": "new"}
	if got := s.permissionReviewer(a)(t.Context(), call, params, safety.RiskProjectWrite, func(agent.AgentEvent) {}); got.Decision != "deny" || !strings.Contains(got.Reason, "unclear") {
		t.Fatalf("model ask did not fail closed without a human: %+v", got)
	}

	mode = "malformed"
	if got := s.permissionReviewer(a)(t.Context(), call, params, safety.RiskProjectWrite, func(agent.AgentEvent) {}); got.Decision != "deny" || !strings.Contains(got.Reason, "technical recovery") {
		t.Fatalf("exhausted review did not fail closed without a human: %+v", got)
	}

	before := calls.Load()
	command := provider.ToolCall{Function: provider.FunctionCall{Name: "execute_command"}}
	if got := s.permissionReviewer(a)(t.Context(), command, map[string]any{"command": "curl https://example.com"}, safety.RiskNetwork, func(agent.AgentEvent) {}); got.Decision != "deny" || !strings.Contains(got.Reason, "technical recovery") {
		t.Fatalf("failed unknown/network review did not fail closed: %+v", got)
	}
	if calls.Load() != before+1 {
		t.Fatal("unknown/network operation did not reach the remote reviewer")
	}
}

func TestReviewerDenialModeHasNoConcurrentLifetimeCap(t *testing.T) {
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
	if calls.Load() != 16 || denials.Load() != 16 {
		t.Fatalf("concurrent reviews were dropped: calls=%d denials=%d", calls.Load(), denials.Load())
	}
}

func TestRootAndChildReviewersRemainIndependentWithoutLifetimeCaps(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"{\"decision\":\"allow\",\"reason\":\"routine edit\"}"},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()

	s, root := newReviewerTestSession(t, server.URL)
	child := &agent.Agent{AgentID: "child", Depth: 1, TurnGeneration: 1}
	rootReview := s.permissionReviewer(root)
	childReview := s.permissionReviewer(child)
	call := provider.ToolCall{Function: provider.FunctionCall{Name: "edit_file"}}
	params := map[string]any{"path": "main.go", "old_string": "old", "new_string": "new"}
	if got := rootReview(t.Context(), call, params, safety.RiskProjectWrite, func(agent.AgentEvent) {}); got.Decision != "allow" {
		t.Fatalf("root review=%+v", got)
	}
	if got := childReview(t.Context(), call, params, safety.RiskProjectWrite, func(agent.AgentEvent) {}); got.Decision != "allow" {
		t.Fatalf("child review=%+v", got)
	}
	if got := rootReview(t.Context(), call, params, safety.RiskProjectWrite, func(agent.AgentEvent) {}); got.Decision != "allow" {
		t.Fatalf("second root review=%+v", got)
	}
	if got := childReview(t.Context(), call, params, safety.RiskProjectWrite, func(agent.AgentEvent) {}); got.Decision != "allow" {
		t.Fatalf("second child review=%+v", got)
	}
	child.TurnGeneration++
	if got := childReview(t.Context(), call, params, safety.RiskProjectWrite, func(agent.AgentEvent) {}); got.Decision != "allow" {
		t.Fatalf("new child turn did not receive a fresh budget: %+v", got)
	}
	if calls.Load() != 5 {
		t.Fatalf("remote review calls=%d, want 5", calls.Load())
	}
}

func TestYoloPolicyIsInheritedByOneSessionTreeOnly(t *testing.T) {
	first := &Session{}
	second := &Session{}
	first.SetYolo(true)
	second.SetYolo(false)
	firstRoot, firstChild := &agent.Agent{}, &agent.Agent{Depth: 1}
	secondRoot, secondChild := &agent.Agent{}, &agent.Agent{Depth: 1}
	first.bindAgentPermissionPolicy(firstRoot)
	first.bindAgentPermissionPolicy(firstChild)
	second.bindAgentPermissionPolicy(secondRoot)
	second.bindAgentPermissionPolicy(secondChild)
	for name, live := range map[string]*agent.Agent{"first root": firstRoot, "first child": firstChild} {
		if live.YoloMode == nil || !live.YoloMode() || live.PermissionReviewer == nil {
			t.Fatalf("%s did not inherit first session policy", name)
		}
	}
	for name, live := range map[string]*agent.Agent{"second root": secondRoot, "second child": secondChild} {
		if live.YoloMode == nil || live.YoloMode() || live.PermissionReviewer == nil {
			t.Fatalf("%s did not inherit second session policy", name)
		}
	}
	first.SetYolo(false)
	second.SetYolo(true)
	if firstRoot.YoloMode() || firstChild.YoloMode() || !secondRoot.YoloMode() || !secondChild.YoloMode() {
		t.Fatal("live session-scoped YOLO toggle leaked across independent trees")
	}
}
