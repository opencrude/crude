package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"crude/provider"
	"crude/sessiondb"
	"crude/types"
)

func accountRoutingConfig() *Config {
	cfg := modelAttributionConfig("https://example.invalid/v1", "m")
	cfg.Accounts = map[string]provider.Account{
		"work":     {ID: "work", ProviderID: "openrouter", APIKey: "secret-work"},
		"personal": {ID: "personal", ProviderID: "openrouter", APIKey: "secret-personal"},
	}
	profile := cfg.Agents.Profiles["default"]
	main := profile.Categories[CategoryDefault]
	main.AccountID = "work"
	profile.Categories[CategoryDefault] = main
	cfg.Agents.Profiles["default"] = profile
	return cfg
}

func TestAccountPinnedAcrossDefaultChangeAndResume(t *testing.T) {
	cfg := accountRoutingConfig()
	home, dir := t.TempDir(), filepath.Join(t.TempDir(), "sessions")
	s, err := NewWithSessionDir("pinned", cfg, home, dir)
	if err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(dir, s.SessionID()+".db")
	oldID := s.SessionID()
	profile := cfg.Agents.Profiles["default"]
	main := profile.Categories[CategoryDefault]
	main.AccountID = "personal"
	profile.Categories[CategoryDefault] = main
	cfg.Agents.Profiles["default"] = profile
	s.UpdateWorkspaceDefaults(types.AppSettings{Accounts: cfg.Accounts, Providers: cfg.Providers, Agents: types.AgentsSettings{Categories: map[string]types.CategorySettings{CategoryDefault: {AccountID: "personal", Model: "m"}}}})
	if got := s.cfg.GetActiveDefaultCategory().AccountID; got != "work" {
		t.Fatalf("live session redirected to %q", got)
	}
	if err := s.ResetSessionSettings(); err != nil {
		t.Fatal(err)
	}
	if got := s.cfg.GetActiveDefaultCategory().AccountID; got != "work" {
		t.Fatalf("reset redirected session to %q", got)
	}
	if err := s.AppendWorkspaceHistoryEvent("account-pin", "account pin"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	resumed, err := ResumeSessionWithDir(dbPath, cfg, home, dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := resumed.cfg.GetActiveDefaultCategory().AccountID; got != "work" {
		t.Fatalf("resume redirected to %q", got)
	}
	meta, err := sessiondb.GetMeta(resumed.DB())
	if err != nil || meta.AccountID != "work" || meta.ID != oldID {
		t.Fatalf("durable pin %+v err=%v", meta, err)
	}
	resumed.Close()
	fresh, err := NewWithSessionDir("new", cfg, home, dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := fresh.cfg.GetActiveDefaultCategory().AccountID; got != "personal" {
		t.Fatalf("new session ignored Main Agent selection: %q", got)
	}
	fresh.Close()
	contents, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(contents), "secret-work") || strings.Contains(string(contents), "secret-personal") {
		t.Fatal("session database contains credentials")
	}
}

func TestAccountRemovedResumesWithoutBindingOrFallback(t *testing.T) {
	cfg := accountRoutingConfig()
	manager := provider.NewAccountManager(cfg.Accounts, nil)
	cfg.AccountManager = manager
	home, dir := t.TempDir(), filepath.Join(t.TempDir(), "sessions")
	s, err := NewWithSessionDir("pinned", cfg, home, dir)
	if err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(dir, s.SessionID()+".db")
	if err := s.AppendWorkspaceHistoryEvent("account-pin", "account pin"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	manager.Replace(map[string]provider.Account{"personal": cfg.Accounts["personal"]})
	resumed, err := ResumeSessionWithDir(dbPath, cfg, home, dir)
	if err != nil {
		t.Fatalf("removed account blocked history: %v", err)
	}
	defer resumed.Close()
	if resumed.provider != nil {
		t.Fatal("resumed missing account unexpectedly bound a provider")
	}
	main := resumed.cfg.GetActiveDefaultCategory()
	if main.AccountID != "work" {
		t.Fatalf("lost pinned connection: %+v", main)
	}
	if _, err := resumed.cfg.providerForCategory(main); err == nil || !strings.Contains(err.Error(), "work") {
		t.Fatalf("removed account rebound: %v", err)
	}
}

func TestAccountProfileDoesNotSerializeSecrets(t *testing.T) {
	cfg := accountRoutingConfig()
	if err := cfg.pinAccounts(); err != nil {
		t.Fatal(err)
	}
	profile, _ := activeProfileCfg(cfg)
	profile.Categories[CategoryCrafter] = CategoryConfig{AccountID: "personal", Model: "m"}
	cfg.Agents.Profiles["default"] = profile
	data, err := json.Marshal(cfg.Agents)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "secret-") || !strings.Contains(string(data), "personal") {
		t.Fatalf("serialized roles: %s", data)
	}
	for _, tc := range []struct{ role, key string }{{CategoryDefault, "secret-work"}, {CategoryCrafter, "secret-personal"}} {
		cat := cfg.GetCategoryConfig(tc.role)
		p, err := cfg.providerForCategory(cat)
		if err != nil {
			t.Fatal(err)
		}
		if got := bearerForTest(t, p); got != "Bearer "+tc.key {
			t.Fatalf("%s auth = %q", tc.role, got)
		}
	}
}

func TestSaveRejectsRoleAccountMismatchWithoutPersistence(t *testing.T) {
	cfg := accountRoutingConfig()
	home, dir := t.TempDir(), filepath.Join(t.TempDir(), "sessions")
	sess, err := NewWithSessionDir("save", cfg, home, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	before := sess.SettingsSnapshot()
	changes := before
	changes.SessionOverrides = map[string]types.CategorySettings{
		CategoryDefault: {AccountID: "work", Model: "m"},
		CategoryCrafter: {AccountID: "missing", Model: "m"},
	}
	if err := sess.HandleSettingsSave(changes); err == nil || !strings.Contains(err.Error(), "crafter") {
		t.Fatalf("invalid account saved: %v", err)
	}
	if got := sess.SettingsSnapshot().AccountID; got != before.AccountID {
		t.Fatalf("failed save altered account %q", got)
	}
	overrides, err := sessiondb.LoadAgentSettingOverrides(sess.DB(), sess.SessionID())
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := overrides[CategoryCrafter]; exists {
		t.Fatal("failed save wrote role override")
	}
}

func TestTransportSettingsFollowTemplateAndAccountEditsAndResume(t *testing.T) {
	cfg := accountRoutingConfig()
	home, dir := t.TempDir(), filepath.Join(t.TempDir(), "sessions")
	s, err := NewWithSessionDir("transport-settings", cfg, home, dir)
	if err != nil {
		t.Fatal(err)
	}
	current := s
	t.Cleanup(func() {
		if current != nil {
			current.Close()
		}
	})
	previous := s.provider
	previousDial := previous.DialSettings()
	template := cfg.Providers["openrouter"]
	template.Dialer = &provider.DialSettings{
		RequestTimeoutSeconds: 300, ConnectTimeoutSeconds: 60, MaxAttempts: 3, MaxBackoffSeconds: 60,
		ProgressStartupTimeoutSeconds: 120, ProgressTimeoutFloorSeconds: 60, ProgressTimeoutCeilingSeconds: 600,
	}
	cfg.Providers["openrouter"] = template
	override := provider.DialSettings{
		RequestTimeoutSeconds: 600, ConnectTimeoutSeconds: 10, MaxAttempts: 2, MaxBackoffSeconds: 15,
		ProgressStartupTimeoutSeconds: 300, ProgressTimeoutFloorSeconds: 120, ProgressTimeoutCeilingSeconds: 900,
	}
	for _, useOverride := range []bool{false, true, false} {
		account := cfg.Accounts["work"]
		account.Overrides.Dialer = nil
		want := *template.Dialer
		if useOverride {
			account.Overrides.Dialer = &override
			want = override
		}
		cfg.Accounts["work"] = account
		s.UpdateWorkspaceDefaults(types.AppSettings{Accounts: cfg.Accounts, Providers: cfg.Providers, Agents: ConfigToSettings(cfg).Agents})
		if s.provider == nil || s.provider.DialSettings() != want {
			t.Fatalf("live account override=%t was not applied to requests: %+v", useOverride, s.provider)
		}
		other, err := s.cfg.ProviderForAccount("personal", "m")
		if err != nil || other.DialSettings() != *template.Dialer {
			t.Fatalf("account override leaked to another account: provider=%+v err=%v", other, err)
		}
	}
	if previous.DialSettings() != previousDial {
		t.Fatal("settings edit mutated an already constructed provider")
	}
	dbPath := filepath.Join(dir, s.SessionID()+".db")
	if err := s.AppendWorkspaceHistoryEvent("transport-settings", "transport settings changed"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	current = nil
	resumed, err := ResumeSessionWithDir(dbPath, cfg, home, dir)
	if err != nil {
		t.Fatal(err)
	}
	current = resumed
	if resumed.provider == nil || resumed.provider.DialSettings() != *template.Dialer || resumed.cfg.GetActiveDefaultCategory().AccountID != "work" {
		t.Fatalf("resume lost pinned account or live transport settings: %+v", resumed.provider)
	}
}
