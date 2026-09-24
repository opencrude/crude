package session

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"crude/agent"
	"crude/provider"
	"crude/safety"
	"crude/types"
)

func TestPermissionReviewerTransportAndBudget(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		if request["model"] != "review-model" || request["max_tokens"] != float64(96) || request["tools"] != nil {
			t.Errorf("unbounded/wrong reviewer request: %+v", request)
		}
		messages, _ := request["messages"].([]any)
		if len(messages) != 2 {
			t.Error("reviewer received conversation history")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"{\"decision\":\"allow\",\"reason\":\"routine project edit\"}"},"finish_reason":"stop"}],"usage":{"prompt_tokens":110,"completion_tokens":15}}`)
	}))
	defer server.Close()
	s, a := newReviewerTestSession(t, server.URL)
	s.cfg.Safety.AutoPermission.MaxReviewsPerTurn = 1
	review := s.permissionReviewer(a)
	events := []agent.AgentEvent{}
	emit := func(evt agent.AgentEvent) { events = append(events, evt) }
	params := map[string]any{"path": "main.go", "old_string": "old", "new_string": "new"}
	if got := review(t.Context(), provider.ToolCall{Function: provider.FunctionCall{Name: "edit_file"}}, params, safety.RiskProjectWrite, emit); got.Decision != "allow" {
		t.Fatalf("review=%+v", got)
	}
	if got := review(t.Context(), provider.ToolCall{Function: provider.FunctionCall{Name: "edit_file"}}, params, safety.RiskProjectWrite, emit); got.Decision != "ask" {
		t.Fatal("budget did not fall back to human")
	}
	if calls.Load() != 1 {
		t.Fatalf("requests=%d", calls.Load())
	}
	if len(events) != 1 || events[0].Type != "usage" {
		t.Fatalf("usage events=%+v", events)
	}
	data := events[0].Data.(map[string]any)
	if data["request_kind"] != "permission_review" || data["model"] != "review-model" || data["context_prompt"] != 0 {
		t.Fatalf("review usage polluted agent context: %+v", data)
	}
}

func newReviewerTestSession(t *testing.T, url string) (*Session, *agent.Agent) {
	t.Helper()
	cfg := modelAttributionConfig(url, "main-model")
	cfg.Safety.AutoPermission = types.AutoPermissionSettings{Enabled: "on", Provider: "openrouter", AccountID: "account", Model: "review-model", DenyMode: "deny"}
	s, err := NewWithSessionDir("review", cfg, t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	s.SetWorkDir(t.TempDir())
	return s, &agent.Agent{AgentID: "parent", TurnGeneration: 1}
}

func TestPermissionReviewerFailsToHuman(t *testing.T) {
	cases := []struct {
		name, body string
		status     int
	}{
		{"malformed", `{"decision":"allow"}`, 200},
		{"bad json", `not json`, 200},
		{"extra fields", `{"decision":"allow","reason":"ok","always":true}`, 200},
		{"unknown", `{"decision":"maybe","reason":"unsure"}`, 200},
		{"trailing", `{"decision":"allow","reason":"ok"} {}`, 200},
		{"unavailable", "", 503},
		{"huge", strings.Repeat("x", 20000), 200},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"role": "assistant", "content": tc.body}, "finish_reason": "stop"}}})
			}))
			defer server.Close()
			s, a := newReviewerTestSession(t, server.URL)
			got := s.permissionReviewer(a)(t.Context(), provider.ToolCall{Function: provider.FunctionCall{Name: "write_file"}}, map[string]any{"path": "main.go", "content": "package main"}, safety.RiskProjectWrite, func(agent.AgentEvent) {})
			if got.Decision != "ask" || calls.Load() != 1 {
				t.Fatalf("failure did not ask once: %+v calls=%d", got, calls.Load())
			}
		})
	}
}

func TestPermissionReviewerDropsStaleAndTimedOutDecisions(t *testing.T) {
	for _, mode := range []string{"disable-reenable", "timeout", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			arrived := make(chan struct{})
			release := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				close(arrived)
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"{\"decision\":\"allow\",\"reason\":\"ok\"}"},"finish_reason":"stop"}]}`)
			}))
			defer server.Close()
			defer close(release)
			s, a := newReviewerTestSession(t, server.URL)
			s.cfg.Safety.AutoPermission.TimeoutSeconds = 1
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan agent.PermissionReview, 1)
			go func() {
				done <- s.permissionReviewer(a)(ctx, provider.ToolCall{Function: provider.FunctionCall{Name: "write_file"}}, map[string]any{"path": "main.go", "content": "package main"}, safety.RiskProjectWrite, func(agent.AgentEvent) {})
			}()
			select {
			case <-arrived:
			case <-time.After(3 * time.Second):
				t.Fatal("review request not started")
			}
			switch mode {
			case "disable-reenable":
				settings := ConfigToSettings(s.cfg)
				app := types.AppSettings{Agents: settings.Agents, Safety: s.cfg.Safety}
				app.Safety.AutoPermission.Enabled = "off"
				s.UpdateWorkspaceDefaults(app)
				app.Safety.AutoPermission.Enabled = "on"
				s.UpdateWorkspaceDefaults(app)
				release <- struct{}{}
			case "cancel":
				cancel()
			}
			select {
			case got := <-done:
				if got.Decision != "ask" {
					t.Fatalf("stale approval=%+v", got)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("review did not respect cancellation/timeout")
			}
		})
	}
}

func TestPermissionReviewerStructuralCeiling(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, tool string
		params     map[string]any
		risk       safety.CommandRisk
	}{
		{"delete", "delete_file", map[string]any{"path": "main.go"}, safety.RiskProjectWrite},
		{"outside", "write_file", map[string]any{"path": "../outside", "content": "x"}, safety.RiskProjectWrite},
		{"symlink", "write_file", map[string]any{"path": "escape/new", "content": "x"}, safety.RiskProjectWrite},
		{"credential", "edit_file", map[string]any{"path": ".env", "new_string": "x"}, safety.RiskProjectWrite},
		{"token", "write_file", map[string]any{"path": "main.go", "content": "api_key=abc"}, safety.RiskProjectWrite},
		{"oversize", "write_file", map[string]any{"path": "main.go", "content": strings.Repeat("x", 9000)}, safety.RiskProjectWrite},
		{"network", "execute_command", map[string]any{"command": "curl https://example.com"}, safety.RiskNetwork},
		{"compound", "execute_command", map[string]any{"command": "go test ./...; rm -rf /"}, safety.RiskProjectWrite},
		{"unknown", "execute_command", map[string]any{"command": "./custom-script"}, safety.RiskUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, ok := permissionReviewPayload(root, tc.tool, tc.params, tc.risk, 4096); ok {
				t.Fatal("ineligible request sent to reviewer")
			}
		})
	}
	if _, ok := permissionReviewPayload(root, "edit_file", map[string]any{"path": "main.go", "old_string": "old", "new_string": "new"}, safety.RiskProjectWrite, 4096); !ok {
		t.Fatal("routine bounded project edit was ineligible")
	}
}
