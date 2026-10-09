package workspace

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"crude/provider"
	"crude/session"
	"crude/types"
)

// This journey uses real workspace/session persistence rather than testing the
// resolver's chosen implementation: Main Agent settings, explicit switching, and resume.
func TestAccountSelectionSurvivesMainAgentSwitchAndResume(t *testing.T) {
	setTestHome(t)
	home, root := t.TempDir(), t.TempDir()
	w := NewWorkspace(root, "test-model", "tester", types.Settings{}, &testLogger{})
	t.Cleanup(w.Close)
	accounts := map[string]provider.Account{
		"personal": {ID: "personal", ProviderID: "openai", Label: "Personal", APIKey: "personal-key", Connection: provider.DefaultTemplates()["openai"]},
		"work":     {ID: "work", ProviderID: "openai", Label: "Work", APIKey: "work-key", Connection: provider.DefaultTemplates()["openai"]},
	}
	cfg := &session.Config{Accounts: accounts,
		AccountManager: provider.NewAccountManager(accounts, nil),

		Agents: session.DefaultAgentsConfig(),
	}
	profile := cfg.Agents.Profiles[cfg.Agents.ActiveProfile]
	main := profile.Categories[session.CategoryDefault]
	main.AccountID, main.Model = "personal", "test-model"
	profile.Categories[session.CategoryDefault] = main
	cfg.Agents.Profiles[cfg.Agents.ActiveProfile] = profile
	w.SetBackendConfig(cfg, home)
	w.SetAppDefaults(types.AppSettings{Accounts: accounts, LocalHistory: types.LocalHistorySettings{Enabled: "off"}})
	first, err := w.CreateSession(cfg, home)
	if err != nil {
		t.Fatal(err)
	}
	if got := first.backend.SettingsSnapshot().AccountID; got != "personal" {
		t.Fatalf("first account=%q", got)
	}
	profile.Categories[session.CategoryDefault] = session.CategoryConfig{AccountID: "work", Model: "test-model"}
	cfg.Agents.Profiles[cfg.Agents.ActiveProfile] = profile
	if got := first.backend.SettingsSnapshot().AccountID; got != "personal" {
		t.Fatalf("Main Agent edit redirected existing session to %q", got)
	}
	second, err := w.CreateSession(cfg, home)
	if err != nil {
		t.Fatal(err)
	}
	if got := second.backend.SettingsSnapshot().AccountID; got != "work" {
		t.Fatalf("new session ignored Main Agent account: %q", got)
	}
	settings := first.backend.SettingsSnapshot()
	settings.AccountID = "work"
	settings.SessionOverrides = map[string]types.CategorySettings{session.CategoryDefault: {AccountID: "work", Model: "test-model"}}
	if err := w.HandleSettingsSaveForSession(first.SessionID, settings); err != nil {
		t.Fatal(err)
	}
	id := first.SessionID
	dbPath, err := SessionDBPath(root, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.backend.AppendWorkspaceHistoryEvent("Saved workspace state", "Workspace state saved"); err != nil {
		t.Fatal(err)
	}
	first.backend.Close()
	profile.Categories[session.CategoryDefault] = session.CategoryConfig{AccountID: "personal", Model: "other-model"}
	cfg.Agents.Profiles[cfg.Agents.ActiveProfile] = profile
	resumed, err := session.ResumeSessionWithDir(dbPath, cfg, home, filepath.Dir(dbPath))
	if err != nil {
		t.Fatalf("resume %s: %v", id, err)
	}
	defer resumed.Close()
	if got := resumed.SettingsSnapshot().AccountID; got != "work" {
		t.Fatalf("resume lost explicit choice: %q", got)
	}
}

func TestTwoConcurrentSessionsUseIndependentAccounts(t *testing.T) {
	setTestHome(t)
	home, root := t.TempDir(), t.TempDir()
	arrived := make(chan string, 2)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		arrived <- r.Header.Get("Authorization")
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":1}}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	defer close(release)
	accounts := map[string]provider.Account{
		"personal": {ID: "personal", ProviderID: "openai", Label: "Personal", APIKey: "personal-key", Connection: provider.DefaultTemplates()["openai"]},
		"work":     {ID: "work", ProviderID: "openai", Label: "Work", APIKey: "work-key", Connection: provider.DefaultTemplates()["openai"]},
	}
	cfg := historyE2EConfig(server.URL)
	for id, account := range accounts {
		account.Connection.ProviderURL = server.URL
		accounts[id] = account
	}
	cfg.Accounts = accounts
	cfg.AccountManager = provider.NewAccountManager(accounts, nil)
	w := NewWorkspace(root, "history-model", "tester", types.Settings{}, &testLogger{})
	t.Cleanup(w.Close)
	w.SetAppDefaults(types.AppSettings{LocalHistory: types.LocalHistorySettings{Enabled: "off"}})
	for _, id := range []string{"personal", "work"} {
		profile := cfg.Agents.Profiles[cfg.Agents.ActiveProfile]
		main := profile.Categories[session.CategoryDefault]
		main.AccountID = id
		profile.Categories[session.CategoryDefault] = main
		cfg.Agents.Profiles[cfg.Agents.ActiveProfile] = profile
		frontend, err := w.CreateSession(cfg, home)
		if err != nil {
			t.Fatal(err)
		}
		sender := &historyE2ESender{events: make(chan historyE2EEvent, 128)}
		orch, err := w.StartOrchestrator(frontend.SessionID, sender)
		if err != nil {
			t.Fatal(err)
		}
		if !frontend.backend.SubmitInput(orch.ID, provider.PlainInput("hello")) {
			t.Fatal("turn not admitted")
		}
	}
	seen := map[string]bool{}
	for range 2 {
		select {
		case key := <-arrived:
			seen[key] = true
		case <-time.After(10 * time.Second):
			t.Fatal("sessions did not reach the backend concurrently")
		}
	}
	if !seen["Bearer personal-key"] || !seen["Bearer work-key"] {
		t.Fatalf("accounts mixed on wire: %v", seen)
	}
}
