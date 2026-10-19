package session

import (
	"path/filepath"
	"strings"
	"testing"

	"crude/provider"
	"crude/sessiondb"
	"crude/types"
)

func TestResumeUnpinnedHistoryNeedsExplicitConnectionEvenAfterWorkspaceUpdate(t *testing.T) {
	cfg := accountRoutingConfig()
	home, dir := t.TempDir(), filepath.Join(t.TempDir(), "sessions")
	original, err := NewWithSessionDir("", cfg, home, dir)
	if err != nil {
		t.Fatal(err)
	}
	id := original.SessionID()
	if err := original.AppendWorkspaceHistoryEvent("keep prior work", "Prior work"); err != nil {
		t.Fatal(err)
	}
	if err := sessiondb.SaveAgentSettingOverrides(original.DB(), id, map[string]sessiondb.AgentSettingOverride{CategoryDefault: {AccountID: "removed-account"}}); err != nil {
		t.Fatal(err)
	}
	original.Close()
	resumed, err := ResumeSessionWithDir(filepath.Join(dir, id+".db"), cfg, home, dir)
	if err != nil {
		t.Fatalf("old transcript should open for recovery: %v", err)
	}
	defer resumed.Close()
	if len(resumed.MessagesWithMeta()) != 1 || resumed.provider != nil || resumed.ConnectionIssue() == nil {
		t.Fatalf("history or disconnected state lost: provider=%v issue=%v", resumed.provider, resumed.ConnectionIssue())
	}
	if err := resumed.HandleSettingsSave(resumed.SettingsSnapshot()); err != nil {
		t.Fatal("unchanged preferences must remain saveable while connection needs repair", err)
	}
	if resumed.ConnectionIssue() == nil {
		t.Fatal("ordinary save silently selected another connection")
	}
	settings := types.AppSettings{Accounts: cfg.Accounts, Agents: types.AgentsSettings{Categories: map[string]types.CategorySettings{CategoryDefault: {AccountID: "personal", Model: "m"}}}}
	resumed.UpdateWorkspaceDefaults(settings)
	if resumed.provider != nil || resumed.ConnectionIssue() == nil || resumed.SettingsSnapshot().AccountID != "removed-account" {
		t.Fatalf("workspace account update rebound old session: provider=%v issue=%v settings=%+v", resumed.provider, resumed.ConnectionIssue(), resumed.SettingsSnapshot())
	}
	chosen := resumed.SettingsSnapshot()
	chosen.SessionOverrides = map[string]types.CategorySettings{CategoryDefault: {AccountID: "personal", Model: "m"}}
	if err := resumed.HandleSettingsSave(chosen); err != nil {
		t.Fatalf("explicit recovery: %v", err)
	}
	meta, err := sessiondb.GetMeta(resumed.DB())
	if err != nil || meta.AccountID != "personal" || resumed.provider == nil || resumed.ConnectionIssue() != nil {
		t.Fatalf("recovered connection not durable: meta=%+v provider=%v issue=%v err=%v", meta, resumed.provider, resumed.ConnectionIssue(), err)
	}
}

func TestResumeRemovedAccountRetainsHistoryAndRecoversByExplicitSelection(t *testing.T) {
	cfg := accountRoutingConfig()
	cfg.AccountManager = provider.NewAccountManager(cfg.Accounts, nil)
	home, dir := t.TempDir(), filepath.Join(t.TempDir(), "sessions")
	original, err := NewWithSessionDir("recover", cfg, home, dir)
	if err != nil {
		t.Fatal(err)
	}
	id := original.SessionID()
	if err := sessiondb.SaveAgentSettingOverrides(original.DB(), id, map[string]sessiondb.AgentSettingOverride{CategoryDefault: {AccountID: "work"}}); err != nil {
		t.Fatal(err)
	}
	original.msgs = []provider.ChatMessage{provider.NewTextMessage("user", "keep this conversation")}
	if _, err := sessiondb.ReplaceMessagesWithMeta(original.DB(), id, original.msgs, []int{1}, []sessiondb.DisplayMeta{{DisplayRole: "user", DisplayContent: "keep this conversation"}}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, id+".db")
	original.Close()

	// Changing the workspace Main Agent account never rebinds a resumed agent.
	cfg.Accounts["other"] = provider.Account{ID: "other", ProviderID: "anthropic", APIKey: "other-key", Connection: provider.DefaultTemplates()["anthropic"]}
	cfg.AccountManager.Replace(map[string]provider.Account{"personal": cfg.Accounts["personal"], "other": cfg.Accounts["other"]})
	a := cfg.Accounts["other"]
	a.Connection = provider.CloneTemplate(provider.DefaultTemplates()["anthropic"])
	cfg.Accounts["other"] = a
	profile := cfg.Agents.Profiles["default"]
	profile.Categories[CategoryDefault] = CategoryConfig{AccountID: "other", Model: "m"}
	cfg.Agents.Profiles["default"] = profile

	resumed, err := ResumeSessionWithDir(path, cfg, home, dir)
	if err != nil {
		t.Fatalf("opening history with missing account: %v", err)
	}
	if resumed.SessionID() != id || len(resumed.MessagesWithMeta()) != 1 || resumed.MessagesWithMeta()[0].Meta.DisplayContent != "keep this conversation" {
		t.Fatalf("history/identity lost: id=%q messages=%+v", resumed.SessionID(), resumed.MessagesWithMeta())
	}
	before := resumed.SettingsSnapshot()
	if before.AccountID != "work" || resumed.provider != nil {
		t.Fatalf("session silently rebound: provider=%q account=%q bound=%v", before.ProviderID, before.AccountID, resumed.provider != nil)
	}
	if _, err := resumed.cfg.providerForCategory(resumed.cfg.GetActiveDefaultCategory()); err == nil || !strings.Contains(err.Error(), "work") {
		t.Fatalf("missing account constructed a provider: %v", err)
	}
	meta, err := sessiondb.GetMeta(resumed.DB())
	if err != nil || meta.Provider != "openrouter" || meta.AccountID != "work" {
		t.Fatalf("resume changed durable connection: %+v err=%v", meta, err)
	}
	// A deliberate settings save, unlike changed workspace defaults, is allowed
	// to select a replacement and restore ability to construct the provider.
	before.SessionOverrides = map[string]types.CategorySettings{CategoryDefault: {AccountID: "personal", Model: "m"}}
	if err := resumed.HandleSettingsSave(before); err != nil {
		t.Fatalf("explicit replacement: %v", err)
	}
	if resumed.provider == nil || resumed.provider.AccountID != "personal" {
		t.Fatalf("replacement not bound: %+v", resumed.provider)
	}
	meta, err = sessiondb.GetMeta(resumed.DB())
	if err != nil || meta.AccountID != "personal" || meta.Provider != "openrouter" || meta.ID != id {
		t.Fatalf("replacement metadata: %+v err=%v", meta, err)
	}
	resumed.Close()
	again, err := ResumeSessionWithDir(path, cfg, home, dir)
	if err != nil {
		t.Fatalf("resuming recovered session: %v", err)
	}
	defer again.Close()
	if got := again.SettingsSnapshot(); got.AccountID != "personal" || got.ProviderID != "openrouter" || len(again.MessagesWithMeta()) != 1 {
		t.Fatalf("recovered session not retained: settings=%+v history=%d", got, len(again.MessagesWithMeta()))
	}
	// A further explicit cross-provider switch must update both pieces of
	// authoritative session metadata or a future resume reverts the provider.
	switchSettings := again.SettingsSnapshot()
	switchSettings.SessionOverrides = map[string]types.CategorySettings{CategoryDefault: {AccountID: "other", Model: "m"}}
	if err := again.HandleSettingsSave(switchSettings); err != nil {
		t.Fatalf("explicit provider switch: %v", err)
	}
	meta, err = sessiondb.GetMeta(again.DB())
	if err != nil || meta.Provider != "anthropic" || meta.AccountID != "other" {
		t.Fatalf("cross-provider switch metadata: %+v err=%v", meta, err)
	}
}

func TestResumeIgnoresWorkspaceMainAgentAccountChange(t *testing.T) {
	cfg := accountRoutingConfig()
	home, dir := t.TempDir(), filepath.Join(t.TempDir(), "sessions")
	original, err := NewWithSessionDir("provider-pin", cfg, home, dir)
	if err != nil {
		t.Fatal(err)
	}
	id := original.SessionID()
	if err := sessiondb.SaveAgentSettingOverrides(original.DB(), id, map[string]sessiondb.AgentSettingOverride{CategoryDefault: {AccountID: "work"}}); err != nil {
		t.Fatal(err)
	}
	original.msgs = []provider.ChatMessage{provider.NewTextMessage("user", "keep pin")}
	if _, err := sessiondb.ReplaceMessagesWithMeta(original.DB(), id, original.msgs, []int{1}, nil); err != nil {
		t.Fatal(err)
	}
	original.Close()
	cfg.Accounts["other"] = provider.Account{ID: "other", ProviderID: "anthropic", APIKey: "other-key", Connection: provider.DefaultTemplates()["anthropic"]}
	a := cfg.Accounts["other"]
	a.Connection = provider.CloneTemplate(provider.DefaultTemplates()["anthropic"])
	cfg.Accounts["other"] = a
	profile := cfg.Agents.Profiles["default"]
	profile.Categories[CategoryDefault] = CategoryConfig{AccountID: "other", Model: "m"}
	cfg.Agents.Profiles["default"] = profile
	resumed, err := ResumeSessionWithDir(filepath.Join(dir, id+".db"), cfg, home, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer resumed.Close()
	main := resumed.cfg.GetActiveDefaultCategory()
	if main.AccountID != "work" || resumed.provider == nil || resumed.provider.AccountID != "work" {
		t.Fatalf("default switch redirected connection: %+v bound=%+v", main, resumed.provider)
	}
}
