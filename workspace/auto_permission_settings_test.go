package workspace

import (
	"testing"

	"crude/provider"
	"crude/session"
	"crude/types"
)

func TestAutoPermissionSettingsReachExistingAndFutureSessions(t *testing.T) {
	setTestHome(t)
	root := t.TempDir()
	w := NewWorkspace(root, "model", "tester", types.Settings{}, &testLogger{})
	t.Cleanup(w.Close)
	w.SetAppDefaults(types.AppSettings{LocalHistory: types.LocalHistorySettings{Enabled: "off"}})
	cfg := &session.Config{Accounts: map[string]provider.Account{"test-account": {ID: "test-account", ProviderID: "openai", Label: "Test", APIKey: "test", Connection: provider.DefaultTemplates()["openai"]}}, Agents: session.DefaultAgentsConfig()}
	profile := cfg.Agents.Profiles[cfg.Agents.ActiveProfile]
	main := profile.Categories[session.CategoryDefault]
	main.AccountID, main.Model = "test-account", "model"
	profile.Categories[session.CategoryDefault] = main
	cfg.Agents.Profiles[cfg.Agents.ActiveProfile] = profile
	w.sessionCfg = cfg
	frontend, err := w.CreateSession(cfg, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	app := w.AppDefaults()
	app.Safety.AutoPermission = types.AutoPermissionSettings{Enabled: "on", Provider: "openai", Model: "cheap-model"}
	w.SetAppDefaults(app)
	if got := frontend.backend.SettingsSnapshot().Safety.AutoPermission; got.Enabled != "on" || got.Model != "cheap-model" {
		t.Fatalf("existing session ignored enabled reviewer: %+v", got)
	}
	if cfg.Safety.AutoPermission.Enabled != "on" {
		t.Fatal("future session defaults lost reviewer settings")
	}
	working := w.EffectiveAppSettings()
	working.Safety.AutoPermission.Enabled = "off"
	if err := w.SaveWorkspaceSettings(types.OverridesFromDiff(app, working)); err != nil {
		t.Fatal(err)
	}
	if got := frontend.backend.SettingsSnapshot().Safety.AutoPermission; got.Enabled != "off" || got.Model != "cheap-model" {
		t.Fatalf("workspace disable did not retain inherited model: %+v", got)
	}
	app.Safety.AutoPermission.Model = "new-cheap-model"
	w.SetAppDefaults(app)
	if got := frontend.backend.SettingsSnapshot().Safety.AutoPermission; got.Enabled != "off" || got.Model != "new-cheap-model" {
		t.Fatalf("live sparse reviewer inheritance failed: %+v", got)
	}
}
