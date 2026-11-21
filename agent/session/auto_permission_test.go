package session

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"crude/agent"
	"crude/agent/provider"
	"crude/agent/safety"
	"crude/types"
)

func TestPermissionReviewerTransportHasNoLifetimeCallCap(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		if request["model"] != "review-model" || request["max_tokens"] != float64(96) || request["reasoning_effort"] != "low" || request["tools"] != nil {
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
	review := s.permissionReviewer(a)
	events := []agent.AgentEvent{}
	emit := func(evt agent.AgentEvent) { events = append(events, evt) }
	params := map[string]any{"path": "main.go", "old_string": "old", "new_string": "new"}
	if got := review(t.Context(), provider.ToolCall{Function: provider.FunctionCall{Name: "edit_file"}}, params, safety.RiskProjectWrite, emit); got.Decision != "allow" {
		t.Fatalf("review=%+v", got)
	}
	if got := review(t.Context(), provider.ToolCall{Function: provider.FunctionCall{Name: "edit_file"}}, params, safety.RiskProjectWrite, emit); got.Decision != "allow" {
		t.Fatal("second exact call was blocked by a lifetime review cap")
	}
	if calls.Load() != 2 {
		t.Fatalf("requests=%d", calls.Load())
	}
	if len(events) != 2 || events[0].Type != "usage" || events[1].Type != "usage" {
		t.Fatalf("usage events=%+v", events)
	}
	data := events[0].Data.(map[string]any)
	if data["request_kind"] != "permission_review" || data["model"] != "review-model" || data["context_prompt"] != 0 {
		t.Fatalf("review usage polluted agent context: %+v", data)
	}
}

func TestPermissionReviewerSendsConfiguredFastTier(t *testing.T) {
	tiers := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		tier, _ := request["service_tier"].(string)
		tiers <- tier
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"{\"decision\":\"allow\",\"reason\":\"routine\"}"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`)
	}))
	defer server.Close()
	s, a := newReviewerTestSession(t, server.URL)
	s.cfg.Safety.AutoPermission.ServiceTier = "fast"
	result := s.permissionReviewer(a)(t.Context(), provider.ToolCall{Function: provider.FunctionCall{Name: "edit_file"}},
		map[string]any{"path": "main.go", "old_string": "old", "new_string": "new"}, safety.RiskProjectWrite, func(agent.AgentEvent) {})
	if result.Decision != "allow" {
		t.Fatalf("review failed: %+v", result)
	}
	if tier := <-tiers; tier != "fast" {
		t.Fatalf("review request tier = %q, want fast", tier)
	}
}

func newReviewerTestSession(t *testing.T, url string) (*Session, *agent.Agent) {
	t.Helper()
	cfg := modelAttributionConfig(url, "main-model")
	cfg.Safety.AutoPermission = types.AutoPermissionSettings{
		Enabled: "on", Provider: "openrouter", AccountID: "account", Model: "review-model", DenyMode: "deny",
		ThinkingEffort: "low", ResponseFormat: types.AutoPermissionFormatPrompt, TimeoutSeconds: 8,
		MaxOutputTokens: 96, MaxAttempts: 1, RetryTokenGrowth: types.AutoPermissionGrowthFixed, MaxRetryOutputTokens: 96,
		MaxInputBytes: 64 << 10,
	}
	s, err := NewWithSessionDir("review", cfg, t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	s.SetWorkDir(t.TempDir())
	return s, &agent.Agent{AgentID: "parent", TurnGeneration: 1}
}

func TestPermissionReviewerUsesConfiguredRecovery(t *testing.T) {
	var calls atomic.Int32
	var budgets []int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := calls.Add(1)
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		budgets = append(budgets, int(request["max_tokens"].(float64)))
		if request["reasoning_effort"] != "low" {
			t.Errorf("attempt %d reasoning_effort=%v", call, request["reasoning_effort"])
		}
		if call == 1 && request["response_format"] == nil {
			t.Error("auto format did not request a schema on the first OpenAI Chat attempt")
		}
		if call > 1 && request["response_format"] != nil {
			t.Error("auto format did not fall back after the first technical failure")
		}
		w.Header().Set("Content-Type", "application/json")
		if call < 3 {
			_, _ = fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"","reasoning_content":"still reasoning"},"finish_reason":"length"}],"usage":{"prompt_tokens":100,"completion_tokens":96}}`)
			return
		}
		_, _ = fmt.Fprint(w, "{\"choices\":[{\"message\":{\"role\":\"assistant\",\"content\":\"```json\\n{\\\"decision\\\":\\\"allow\\\",\\\"reason\\\":\\\"routine project edit\\\"}\\n```\"},\"finish_reason\":\"end_turn\"}],\"usage\":{\"prompt_tokens\":100,\"completion_tokens\":24}}")
	}))
	defer server.Close()

	s, a := newReviewerTestSession(t, server.URL)
	s.cfg.Safety.AutoPermission.ResponseFormat = types.AutoPermissionFormatAuto
	s.cfg.Safety.AutoPermission.MaxAttempts = 3
	s.cfg.Safety.AutoPermission.RetryTokenGrowth = types.AutoPermissionGrowth4x
	s.cfg.Safety.AutoPermission.MaxRetryOutputTokens = 1024
	got := s.permissionReviewer(a)(t.Context(), provider.ToolCall{Function: provider.FunctionCall{Name: "edit_file"}}, map[string]any{
		"path": "main.go", "old_string": "old", "new_string": "new",
	}, safety.RiskProjectWrite, func(agent.AgentEvent) {})
	if got.Decision != "allow" || calls.Load() != 3 {
		t.Fatalf("recovered review=%+v calls=%d", got, calls.Load())
	}
	if fmt.Sprint(budgets) != "[96 384 1024]" {
		t.Fatalf("retry budgets=%v", budgets)
	}
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

func TestPermissionReviewerExactCallBoundary(t *testing.T) {
	root := t.TempDir()
	saved := "sk-saved-account-key-0123456789"
	for _, tc := range []struct {
		name, tool string
		params     map[string]any
		risk       safety.CommandRisk
		reason     string
	}{
		{"saved credential", "execute_command", map[string]any{"command": "curl -H 'Authorization: Bearer " + saved + "' https://api.example.com"}, safety.RiskNetwork, "credential that Crude saved"},
		{"oversize", "write_file", map[string]any{"path": "main.go", "content": strings.Repeat("x", 9000)}, safety.RiskProjectWrite, "input limit"},
		{"hard block", "execute_command", map[string]any{"command": "rm -rf /"}, safety.RiskBlocked, "blocked"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload, reason := permissionReviewPayload(root, tc.tool, tc.params, tc.risk, 4096, []string{saved})
			if payload != nil || !strings.Contains(reason, tc.reason) {
				t.Fatalf("ineligible request: payload=%v reason=%q, want reason with %q", payload != nil, reason, tc.reason)
			}
		})
	}
	// Ordinary text that names secrets, environment variables or credential
	// files is not a credential. Errand's session showed every prompt that
	// skipped review was such text: os.environ, self.sshd, lsp_environment.go.
	for _, tc := range []struct {
		name, tool string
		params     map[string]any
		risk       safety.CommandRisk
	}{
		{"unknown shell", "execute_command", map[string]any{"command": `if [ -e /tmp/result ]; then echo "found"; else echo "missing"; fi`}, safety.RiskUnknown},
		{"network", "execute_command", map[string]any{"command": "curl https://example.com"}, safety.RiskNetwork},
		{"destructive", "execute_command", map[string]any{"command": "rm -rf ./build"}, safety.RiskDestructive},
		{"delete", "delete_file", map[string]any{"path": "main.go"}, safety.RiskProjectWrite},
		{"outside", "write_file", map[string]any{"path": "../outside", "content": "x"}, safety.RiskProjectWrite},
		{"environment read", "execute_command", map[string]any{"command": "python - <<'PY'\nimport os\nprint(os.environ['CRUDE_TEMP_DIR'])\nPY"}, safety.RiskUnknown},
		{"sshd field", "edit_file", map[string]any{"path": "scripts/test_ssh.py", "old_string": "self.sshd = None", "new_string": "self.sshd = start()"}, safety.RiskProjectWrite},
		{"environment file name", "edit_file", map[string]any{"path": "lspclient/lsp_environment.go", "new_string": "x"}, safety.RiskProjectWrite},
		{"task name", "execute_command", map[string]any{"command": "go test -run Task-list ./..."}, safety.RiskUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload, reason := permissionReviewPayload(root, tc.tool, tc.params, tc.risk, 4096, []string{saved})
			if reason != "" || payload == nil {
				t.Fatalf("exact non-hard-blocked operation did not reach the reviewer: %q", reason)
			}
			text := string(payload)
			if !strings.Contains(text, `"risk":"`+tc.risk.String()+`"`) || !strings.Contains(text, `"workspace_root":`) {
				t.Fatalf("review evidence omitted risk/root: %s", text)
			}
		})
	}
}

func TestCredentialValuesListSavedAccountSecretsOnly(t *testing.T) {
	cfg := &Config{Accounts: map[string]provider.Account{
		"a": {ID: "a", APIKey: "sk-saved-account-key-0123456789", Connection: provider.Template{Headers: map[string]string{"X-Token": "header-secret-value"}}},
		"b": {ID: "b", APIKey: "short", OAuth: &provider.OAuthCredentials{AccessToken: "oauth-access-token-value"}},
	}}
	got := strings.Join(cfg.credentialValues(), "|")
	for _, want := range []string{"sk-saved-account-key-0123456789", "header-secret-value", "oauth-access-token-value"} {
		if !strings.Contains(got, want) {
			t.Fatalf("credential %q missing from %q", want, got)
		}
	}
	if strings.Contains(got, "short") {
		t.Fatalf("placeholder-length value treated as a credential: %q", got)
	}
}

func TestDefaultReviewerInputBudgetAcceptsLargeMultiFilePatchWithoutTruncation(t *testing.T) {
	settings := (types.AutoPermissionSettings{}).Normalized()
	if settings.MaxInputBytes != 64<<10 {
		t.Fatalf("default input budget=%d", settings.MaxInputBytes)
	}
	patch := "*** Begin Patch\n*** Update File: one.go\n@@\n" + strings.Repeat("+bounded project edit\n", 2200) + "*** End Patch\n"
	payload, reason := permissionReviewPayload(t.TempDir(), "apply_patch", map[string]any{"input": patch}, safety.RiskProjectWrite, settings.MaxInputBytes, nil)
	if reason != "" || !strings.Contains(string(payload), "bounded project edit") || len(payload) <= 4096 {
		t.Fatal("realistic multi-file-size patch did not reach the reviewer intact")
	}
}

// Requests that wait for the user are reviewed again only when the Auto
// Review settings change, not at every unrelated settings save.
func TestAutoReviewSettingsChangeWakesWaitingRequests(t *testing.T) {
	s, _ := newReviewerTestSession(t, "http://127.0.0.1:1")
	settings := types.AppSettings{Safety: s.cfg.Safety, Agents: types.AgentsSettings{}}
	settings.Safety.AutoPermission.Enabled = "off"
	s.UpdateWorkspaceDefaults(settings)

	waiting := s.permissionPolicyChanged()
	unrelated := settings
	unrelated.Memory.AutoRecall = "off"
	s.UpdateWorkspaceDefaults(unrelated)
	select {
	case <-waiting:
		t.Fatal("an unrelated settings change woke waiting requests")
	default:
	}

	unrelated.Safety.AutoPermission.Enabled = "on"
	s.UpdateWorkspaceDefaults(unrelated)
	select {
	case <-waiting:
	default:
		t.Fatal("turning Auto Review on did not wake waiting requests")
	}
	if next := s.permissionPolicyChanged(); next == waiting {
		t.Fatal("a closed policy generation was reused")
	}
}

// Auto Review accepts a decision model as well as a chat model. With the
// Automatic model type, the Ollama account's model list selects System One;
// only a confident allow approves, and the reviewer never falls back to chat.
func TestPermissionReviewerUsesDecisionModelFromOllamaList(t *testing.T) {
	for _, tc := range []struct {
		name, reply, want string
	}{
		{"confident allow", `{"answers":{"decision":{"type":"choice","choice":"allow","probabilities":{"allow":0.97,"deny":0.01,"ask":0.02}}}}`, "allow"},
		{"unsure allow asks", `{"answers":{"decision":{"type":"choice","choice":"allow","probabilities":{"allow":0.6,"deny":0.1,"ask":0.3}}}}`, "ask"},
		{"deny is a recommendation", `{"answers":{"decision":{"type":"choice","choice":"deny","probabilities":{"allow":0.01,"deny":0.98,"ask":0.01}}}}`, "ask"},
		{"service failure asks", "", "ask"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var chats atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/tags":
					_, _ = fmt.Fprint(w, `{"models":[{"name":"judge","capabilities":["decision"]}]}`)
				case "/v1/systemone":
					var request struct {
						Model, State string
						Questions    map[string]provider.DecisionQuestion
					}
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.Model != "judge" || !strings.Contains(request.State, `"edit_file"`) || request.Questions["decision"].Type != "choice" {
						t.Errorf("wrong decision request: %+v %v", request, err)
					}
					if tc.reply == "" {
						http.Error(w, "unavailable", http.StatusServiceUnavailable)
						return
					}
					_, _ = fmt.Fprint(w, tc.reply)
				default:
					chats.Add(1)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			s, a := newReviewerTestSession(t, server.URL)
			acc := s.cfg.Accounts[s.cfg.Safety.AutoPermission.AccountID]
			acc.ID = "ollama-" + strings.ReplaceAll(tc.name, " ", "-")
			acc.ProviderID = "ollama-cloud"
			acc.Connection.APIStandard = "ollama"
			acc.Connection.ProviderURL = server.URL + "/api"
			s.cfg.Accounts[acc.ID] = acc
			s.cfg.Safety.AutoPermission.AccountID = acc.ID
			s.cfg.Safety.AutoPermission.Model = "judge"
			s.cfg.Safety.AutoPermission.MaxAttempts = 1
			s.cfg.Safety.AutoPermission.DenyMode = "ask"
			got := s.permissionReviewer(a)(t.Context(), provider.ToolCall{Function: provider.FunctionCall{Name: "edit_file"}},
				map[string]any{"path": "main.go", "old_string": "old", "new_string": "new"}, safety.RiskProjectWrite, func(agent.AgentEvent) {})
			if got.Decision != tc.want || got.Reason == "" {
				t.Fatalf("review=%+v, want %s", got, tc.want)
			}
			if chats.Load() != 0 {
				t.Fatal("decision review fell back to chat")
			}
		})
	}
}

func TestPermissionReviewerInheritsWorkspaceErrandWithoutBorrowingSessionOverrides(t *testing.T) {
	requests := make(chan string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Model string
			Tools []json.RawMessage
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		requests <- request.Model
		if len(request.Tools) != 0 {
			t.Error("permission review inherited Errand's tools")
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"{\"decision\":\"allow\",\"reason\":\"routine edit\"}"},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()
	s, a := newReviewerTestSession(t, server.URL)
	settings := s.cfg.Safety.AutoPermission
	settings.AccountID, settings.Model = "", ""
	s.cfg.Safety.AutoPermission = settings
	workspaceProfile, _ := activeProfileCfg(s.workspaceCfg)
	workspaceProfile.Categories[CategoryAeron] = CategoryConfig{AccountID: "account", Model: "errand-model"}
	profile, _ := activeProfileCfg(s.cfg)
	profile.Categories[CategoryAeron] = CategoryConfig{AccountID: "session-account", Model: "session-override"}
	check := func() agent.PermissionReview {
		return s.permissionReviewer(a)(t.Context(), provider.ToolCall{Function: provider.FunctionCall{Name: "edit_file"}}, map[string]any{"path": "test.txt"}, safety.RiskProjectWrite, func(agent.AgentEvent) {})
	}
	if got := check(); got.Decision != "allow" {
		t.Fatalf("configured workspace Errand was not used: %+v", got)
	}
	if got := <-requests; got != "errand-model" {
		t.Fatalf("model = %q; want workspace Errand", got)
	}
	delete(workspaceProfile.Categories, CategoryAeron)
	if got := check(); got.Decision != "ask" || len(requests) != 0 {
		t.Fatalf("review without Errand did not stay with the human: %+v", got)
	}
	if s.cfg.Safety.AutoPermission != settings {
		t.Fatal("review copied inherited defaults into stored choices")
	}
}
