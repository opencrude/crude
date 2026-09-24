package app

import (
	"os"
	"strings"
	"testing"

	"crude/provider"
	"crude/session"
	ttypes "crude/types"
	"crude/workspace"
)

func TestAccountsRoundTripAndDetachedConversions(t *testing.T) {
	defer setTestHome(t, t.TempDir())()
	fc := DefaultFileConfig()
	fc.Providers["openrouter"] = provider.DefaultTemplates()["openrouter"]
	fc.Accounts = map[string]provider.Account{
		"work": {ID: "work", ProviderID: "openrouter", Label: "Work", APIKey: "work-key"},
		"home": {ID: "home", ProviderID: "openrouter", Label: "Home", OAuth: &provider.OAuthCredentials{AccessToken: "home-token", RefreshToken: "refresh"}},
	}
	if err := SaveFileConfig(fc); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("config permissions: %v", info.Mode())
	}
	raw, err := os.ReadFile(ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"api_key": "work-key"`) && !strings.Contains(string(raw), `"accounts"`) {
		t.Fatal("missing account storage")
	}
	loaded := LoadFileConfig()
	if loaded.Accounts["work"].APIKey != "work-key" || loaded.Accounts["home"].OAuth.AccessToken != "home-token" {
		t.Fatalf("lost credentials: %#v", loaded.Accounts)
	}
	appSettings := FileConfigToAppSettings(loaded)
	home := appSettings.Accounts["home"]
	home.OAuth.AccessToken = "changed"
	appSettings.Accounts["home"] = home
	if LoadFileConfig().Accounts["home"].OAuth.AccessToken != "home-token" {
		t.Fatal("mutated stored credential")
	}
	converted := FileConfigToSession(loaded)
	home = converted.Accounts["home"]
	home.OAuth.AccessToken = "mutated"
	converted.Accounts["home"] = home
	if loaded.Accounts["home"].OAuth.AccessToken != "home-token" {
		t.Fatal("session conversion aliases credential")
	}
	appSettings.Accounts["home"] = loaded.Accounts["home"]
	ApplyAppSettingsToFileConfig(&loaded, appSettings)
	if loaded.Accounts["home"].OAuth.AccessToken != "home-token" {
		t.Fatal("settings conversion aliases credential")
	}
}

func TestRotatedAccountPersistenceAndRemoval(t *testing.T) {
	defer setTestHome(t, t.TempDir())()
	fc := DefaultFileConfig()
	fc.Accounts = map[string]provider.Account{
		"one": {ID: "one", ProviderID: "chatgpt", OAuth: &provider.OAuthCredentials{AccessToken: "old", RefreshToken: "refresh-old"}},
		"two": {ID: "two", ProviderID: "chatgpt", APIKey: "other"},
	}
	fc.Theme = nil
	if err := SaveFileConfig(fc); err != nil {
		t.Fatal(err)
	}
	expected := cloneAccount(fc.Accounts["one"])
	rotated := cloneAccount(expected)
	rotated.OAuth.AccessToken = "new"
	rotated.OAuth.RefreshToken = "refresh-new"
	if err := persistRotatedAccount(rotated, expected); err != nil {
		t.Fatal(err)
	}
	loaded := LoadFileConfig()
	if loaded.Accounts["one"].OAuth.RefreshToken != "refresh-new" || loaded.Accounts["two"].APIKey != "other" {
		t.Fatalf("refresh lost account: %#v", loaded.Accounts)
	}
	if err := persistRotatedAccount(rotated, expected); err == nil {
		t.Fatal("stale refresh accepted")
	}
	delete(loaded.Accounts, "one")
	if err := SaveFileConfig(loaded); err != nil {
		t.Fatal(err)
	}
	if err := persistRotatedAccount(rotated, expected); err == nil {
		t.Fatal("removed account resurrected")
	}
	if _, ok := LoadFileConfig().Accounts["one"]; ok {
		t.Fatal("removed account persisted")
	}
}

// Exercise the same callback installed by Run through the workspace save entry
// point, without launching an interactive terminal.
func TestAppSettingsSaveAfterRotationAndReconnect(t *testing.T) {
	defer setTestHome(t, t.TempDir())()
	fc := DefaultFileConfig()
	fc.Accounts = map[string]provider.Account{
		"one": {ID: "one", ProviderID: "chatgpt", Label: "Work", OAuth: &provider.OAuthCredentials{AccessToken: "old", RefreshToken: "refresh-old"}},
		"two": {ID: "two", ProviderID: "chatgpt", APIKey: "other"},
	}
	if err := SaveFileConfig(fc); err != nil {
		t.Fatal(err)
	}
	a, err := New(Opts{FC: fc})
	if err != nil {
		t.Fatal(err)
	}
	ws := &workspace.Workspace{Logger: &appLogger{}}
	a.configureWorkspace(ws)
	opened := ws.AppDefaults()
	previous := fc.Accounts["one"]
	rotated := cloneAccount(previous)
	rotated.OAuth.AccessToken = "new"
	rotated.OAuth.RefreshToken = "refresh-new"
	if err := a.accounts.Commit(func(_ map[string]provider.Account) (map[string]provider.Account, error) {
		if err := a.persistAccountRotation(rotated); err != nil {
			return nil, err
		}
		return LoadFileConfig().Accounts, nil
	}); err != nil {
		t.Fatal(err)
	}
	// A preferences form opened before rotation cannot save any account edits.
	account := opened.Accounts["two"]
	account.Label = "Renamed"
	opened.Accounts["two"] = account
	if err := ws.SaveAppSettings(opened); err != nil {
		t.Fatal(err)
	}
	if got := LoadFileConfig(); got.Accounts["one"].OAuth.RefreshToken != "refresh-new" || got.Accounts["two"].Label != "" {
		t.Fatalf("preferences save replayed account data: %+v", got.Accounts)
	}
	oldTwo := fc.Accounts["two"]
	if err := ws.SaveAccountChange(ttypes.AccountChange{ID: "two", ProviderID: "chatgpt", Before: &oldTwo, After: &account}); err != nil {
		t.Fatalf("explicit account rename: %v", err)
	}
	if got := LoadFileConfig().Accounts["two"].Label; got != "Renamed" {
		t.Fatalf("account-only rename was not persisted: %q", got)
	}
	if got := a.accounts.Snapshot()["one"]; got.OAuth.RefreshToken != "refresh-new" {
		t.Fatalf("manager reverted rotation: %+v", got)
	}
	if err := ws.SaveAppSettings(ws.AppDefaults()); err != nil {
		t.Fatalf("form opened after rotation rejected: %v", err)
	}
	// Explicit reconnect replaces only this account's credential.
	reconnect := ws.AppDefaults()
	before := reconnect.Accounts["one"]
	one := reconnect.Accounts["one"]
	one.APIKey, one.OAuth = "intentional-key", nil
	if err := ws.SaveAccountChange(ttypes.AccountChange{ID: "one", ProviderID: "chatgpt", Before: &before, After: &one}); err != nil {
		t.Fatalf("user reconnect rejected: %v", err)
	}
	if got := a.accounts.Snapshot()["one"]; got.APIKey != "intentional-key" || got.OAuth != nil {
		t.Fatalf("reconnect was not installed: %+v", got)
	}
	// An external reconnect cannot be rolled back by an old settings form.
	stale := ws.AppDefaults()
	external := LoadFileConfig()
	reconnected := external.Accounts["one"]
	reconnected.APIKey, reconnected.OAuth = "new-key", nil
	external.Accounts["one"] = reconnected
	if err := SaveFileConfig(external); err != nil {
		t.Fatal(err)
	}
	old := stale.Accounts["one"]
	changed := old
	changed.Label = "Trying to overwrite"
	if err := ws.SaveAccountChange(ttypes.AccountChange{ID: "one", ProviderID: "chatgpt", Before: &old, After: &changed}); err == nil {
		t.Fatal("external reconnect was silently overwritten by stale account edit")
	}
	if err := ws.SaveAppSettings(stale); err != nil {
		t.Fatalf("preferences save unexpectedly refused external account change: %v", err)
	}
	if got := LoadFileConfig().Accounts["one"]; got.APIKey != "new-key" || got.OAuth != nil {
		t.Fatalf("external reconnect lost: %+v", got)
	}
}

func TestConfirmedAccountWritesDoNotSaveStalePreferenceDrafts(t *testing.T) {
	defer setTestHome(t, t.TempDir())()
	fc := DefaultFileConfig()
	fc.Providers["openrouter"] = provider.DefaultTemplates()["openrouter"]
	fc.Accounts = map[string]provider.Account{"personal": {ID: "personal", ProviderID: "openrouter", Label: "Personal", APIKey: "personal-secret"}}
	if err := SaveFileConfig(fc); err != nil {
		t.Fatal(err)
	}
	a, err := New(Opts{FC: fc})
	if err != nil {
		t.Fatal(err)
	}
	ws := &workspace.Workspace{Logger: &appLogger{}}
	a.configureWorkspace(ws)
	stale := ws.AppDefaults()
	team := provider.Account{ID: "team", ProviderID: "openrouter", Label: "Team", APIKey: "team-secret"}
	if err := ws.SaveAccountChange(ttypes.AccountChange{ID: "team", ProviderID: "openrouter", After: &team}); err != nil {
		t.Fatalf("add second provider account: %v", err)
	}
	if saved := LoadFileConfig(); len(saved.Accounts) != 2 {
		t.Fatal("second account replaced first account")
	}
	stale.Editor.TabSize = 3
	stale.Providers["openrouter"] = ttypes.ProviderConfig{Name: "Stale template", ProviderURL: "https://stale.invalid/v1"}
	stale.Accounts = map[string]provider.Account{"personal": fc.Accounts["personal"]}
	if err := ws.SaveAppSettings(stale); err != nil {
		t.Fatal(err)
	}
	if saved := LoadFileConfig(); len(saved.Accounts) != 2 || saved.Accounts["team"].APIKey != "team-secret" || saved.Providers["openrouter"].Name == "Stale template" || saved.Editor.TabSize != 3 {
		t.Fatal("preferences save replayed stale account/template data or lost editor edit")
	}
	before, err := os.ReadFile(ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	duplicate := provider.Account{ID: "duplicate", ProviderID: "openrouter", Label: "team", APIKey: "different"}
	if err := ws.SaveAccountChange(ttypes.AccountChange{ID: duplicate.ID, ProviderID: duplicate.ProviderID, After: &duplicate}); err == nil {
		t.Fatal("duplicate account name was accepted")
	}
	after, err := os.ReadFile(ConfigPath())
	if err != nil || string(after) != string(before) {
		t.Fatal("failed duplicate write changed config bytes", err)
	}
	if err := ws.SaveAccountChange(ttypes.AccountChange{ID: "team", ProviderID: "openrouter", Before: &team}); err != nil {
		t.Fatal(err)
	}
	if saved := LoadFileConfig(); len(saved.Accounts) != 1 || saved.Accounts["personal"].APIKey != "personal-secret" {
		t.Fatal("removing one account affected another account")
	}
}

func TestExplicitCLIAccountAndProviderScope(t *testing.T) {
	defer setTestHome(t, t.TempDir())()
	fc := DefaultFileConfig()
	fc.Providers["openrouter"] = provider.DefaultTemplates()["openrouter"]
	fc.Accounts = map[string]provider.Account{"work": {ID: "work", ProviderID: "openrouter", APIKey: "original"}}
	agents := session.DefaultAgentsConfig()
	fc.Agents = &agents
	profile := fc.Agents.Profiles[fc.Agents.ActiveProfile]
	main := profile.Categories[session.CategoryDefault]
	main.AccountID = "work"
	profile.Categories[session.CategoryDefault] = main
	fc.Agents.Profiles[fc.Agents.ActiveProfile] = profile
	if err := SaveFileConfig(fc); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CRUDE_API_KEY", "env-key")
	t.Setenv("OPENROUTER_API_KEY", "leak")
	if _, err := ResolveConfig(CLIFlags{Provider: "openai"}); err == nil {
		t.Fatal("template flag silently switched the Main Agent account")
	}
	for _, flags := range []CLIFlags{{Provider: "openai", Account: "work"}, {Provider: "openai", Account: "work", APIKey: "bad"}, {Provider: "openai", APIKey: "no-id"}, {Provider: "openrouter", Account: "missing"}} {
		if _, err := ResolveConfig(flags); err == nil {
			t.Fatalf("accepted invalid selection %#v", flags)
		}
	}
	got, err := ResolveConfig(CLIFlags{Provider: "openrouter", Account: "second", APIKey: "second-key"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Accounts["work"].APIKey != "original" || got.Accounts["second"].APIKey != "second-key" || got.Agents.Profiles["default"].Categories[session.CategoryDefault].AccountID != "second" {
		t.Fatalf("incorrect explicit selection: %#v", got)
	}
	if LoadFileConfig().Accounts["second"].ID != "" {
		t.Fatal("CLI flag unexpectedly saved to disk")
	}
}

func TestLauncherStartupWithoutMainAgentAccountDoesNotExit(t *testing.T) {
	defer setTestHome(t, t.TempDir())()
	fc := DefaultFileConfig()
	fc.Providers["openrouter"] = provider.DefaultTemplates()["openrouter"]
	if err := SaveFileConfig(fc); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveConfig(CLIFlags{})
	if err != nil {
		t.Fatalf("launcher startup refused account setup: %v", err)
	}
	if got.Agents != nil && got.Agents.Profiles[got.Agents.ActiveProfile].Categories[session.CategoryDefault].AccountID != "" {
		t.Fatal("startup selected an account implicitly")
	}
}
