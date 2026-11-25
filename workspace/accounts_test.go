package workspace

import (
	"path/filepath"
	"testing"
	"time"

	"crude/agent/provider"
	"crude/agent/session"
	"crude/internal/providersim"
	"crude/types/setting"
)

// This journey uses real workspace/session persistence rather than testing the
// resolver's chosen implementation: Main Agent settings, explicit switching, and resume.
func TestAccountSelectionSurvivesMainAgentSwitchAndResume(t *testing.T) {
	setTestHome(t)
	home, root := t.TempDir(), t.TempDir()
	w := NewWorkspace(root, "test-model", "tester", setting.Settings{}, &testLogger{})
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
	w.SetAppDefaults(setting.App{Accounts: accounts, LocalHistory: setting.LocalHistory{Enabled: "off"}})
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
	settings.SessionOverrides = map[string]setting.Category{session.CategoryDefault: {AccountID: "work", Model: "test-model"}}
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
	gate := providersim.NewGate()
	server := providersim.New(t)
	server.Respond(func(request providersim.Request) providersim.Turn {
		arrived <- request.Header.Get("Authorization")
		return providersim.Text("done").WithUsage(providersim.Usage{Input: 10, Output: 1}).WithGate(gate)
	})
	defer gate.Release()
	accounts := map[string]provider.Account{
		"personal": {ID: "personal", ProviderID: "openai", Label: "Personal", APIKey: "personal-key", Connection: provider.DefaultTemplates()["openai"]},
		"work":     {ID: "work", ProviderID: "openai", Label: "Work", APIKey: "work-key", Connection: provider.DefaultTemplates()["openai"]},
	}
	// The built-in OpenAI account uses Responses, not Chat Completions.
	baseURL := server.BaseURL(providersim.Responses)
	cfg := historyE2EConfig(baseURL)
	for id, account := range accounts {
		account.Connection.ProviderURL = baseURL
		accounts[id] = account
	}
	cfg.Accounts = accounts
	cfg.AccountManager = provider.NewAccountManager(accounts, nil)
	w := NewWorkspace(root, "history-model", "tester", setting.Settings{}, &testLogger{})
	t.Cleanup(w.Close)
	w.SetAppDefaults(setting.App{LocalHistory: setting.LocalHistory{Enabled: "off"}})
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
