package session

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"crude/sessiondb"
	ttypes "crude/tui/types"
)

func TestConfigWithSessionOverridesUsesWorkspaceThenSession(t *testing.T) {
	base := &Config{
		ActiveProvider: "openrouter",
		Providers: map[string]ProviderConfig{
			"openrouter": {APIKey: "workspace-key", Model: "workspace-model", ThinkingEffort: "low"},
			"openai": {
				APIKey: "openai-key", ProviderURL: "https://api.openai.com/v1",
				APIStandard:    "openai-responses",
				ModelStandards: map[string]string{"gpt-5": "openai"},
			},
		},
		Agents: AgentsConfig{
			ActiveProfile: "default",
			Profiles: map[string]ProfileConfig{
				"default": {Categories: map[string]CategoryConfig{
					CategoryDefault: {Provider: "openrouter", Model: "workspace-main", ThinkingEffort: "medium"},
					"crafter":       {Provider: "openrouter", Model: "workspace-crafter"},
				}},
			},
		},
	}
	overrides := map[string]sessiondb.AgentSettingOverride{
		"crafter": {Provider: "openai", Model: "gpt-5", ThinkingEffort: "high"},
	}

	effective := configWithSessionOverrides(base, overrides)
	crafter := effective.GetCategoryConfig("crafter")
	if crafter.Provider != "openai" || crafter.Model != "gpt-5" ||
		crafter.APIStandard != "openai" || crafter.ThinkingEffort != "high" {
		t.Fatalf("crafter override not applied: %#v", crafter)
	}
	if crafter.APIKey != "openai-key" {
		t.Fatalf("provider credentials were not inherited: %#v", crafter)
	}
	main := effective.GetActiveDefaultCategory()
	if main.Model != "workspace-main" {
		t.Fatalf("unoverridden main model = %q, want workspace-main", main.Model)
	}
	if base.GetCategoryConfig("crafter").Model != "workspace-crafter" {
		t.Fatal("base workspace config was mutated")
	}
}

func TestCategoryAPIKeyAlwaysComesFromProvider(t *testing.T) {
	cfg := &Config{
		ActiveProvider: "opencode-go",
		Providers: map[string]ProviderConfig{
			"opencode-go": {APIKey: "provider-key", ProviderURL: "https://opencode.ai/zen/go/v1"},
		},
		Agents: AgentsConfig{
			ActiveProfile: "test",
			Profiles: map[string]ProfileConfig{
				"test": {Categories: map[string]CategoryConfig{
					CategoryDefault: {
						Provider: "opencode-go",
						APIKey:   "stale-profile-key",
						Model:    "deepseek-v4-flash",
					},
				}},
			},
		},
	}
	got := cfg.GetActiveDefaultCategory()
	if got.APIKey != "provider-key" {
		t.Fatalf("resolved API key = %q, want provider-key", got.APIKey)
	}
	settings := ConfigToSettings(cfg)
	if settings.Agents.Categories[CategoryDefault].APIKey != "" {
		t.Fatalf("agent category settings leaked API key: %#v", settings.Agents.Categories[CategoryDefault])
	}
	if settings.APIKey != "provider-key" {
		t.Fatalf("session snapshot API key = %q, want provider-key", settings.APIKey)
	}
}

func TestApplyWorkspaceAgentDefaultsDoesNotStoreAPIKey(t *testing.T) {
	cfg := &Config{
		Providers: map[string]ProviderConfig{
			"opencode-go": {APIKey: "provider-key"},
		},
		Agents: AgentsConfig{
			ActiveProfile: "default",
			Profiles: map[string]ProfileConfig{
				"default": {Categories: map[string]CategoryConfig{
					CategoryDefault: {Provider: "opencode-go", APIKey: "old-key"},
				}},
			},
		},
	}
	ApplyWorkspaceAgentDefaults(cfg, ttypes.AgentsSettings{Categories: map[string]ttypes.CategorySettings{
		CategoryDefault: {Provider: "opencode-go", APIKey: "from-form", Model: "m"},
	}})
	stored := cfg.Agents.Profiles["default"].Categories[CategoryDefault]
	if stored.APIKey != "" {
		t.Fatalf("stored category API key = %q, want empty", stored.APIKey)
	}
	if stored.Model != "m" {
		t.Fatalf("stored model = %q, want m", stored.Model)
	}
	if got := cfg.GetActiveDefaultCategory().APIKey; got != "provider-key" {
		t.Fatalf("resolved API key = %q, want provider-key", got)
	}
}

func TestEnsureAgentsDefaultsRestoresOmittedMultiAgent(t *testing.T) {
	agents := AgentsConfig{ActiveProfile: "default", Profiles: map[string]ProfileConfig{
		"default": {Categories: map[string]CategoryConfig{
			CategoryDefault: {Provider: "openrouter", Model: "m"},
		}},
	}}
	EnsureAgentsDefaults(&agents)
	if !agents.Enabled || agents.Profiles["default"].MultiAgent == nil || !*agents.Profiles["default"].MultiAgent {
		t.Fatalf("omitted multi-agent stayed off: %#v", agents)
	}
	cfg := &Config{Agents: agents}
	if !cfg.IsMultiAgentEnabled() {
		t.Fatal("restored defaults still report multi-agent disabled")
	}
}

func TestHandleSettingsSaveAppliesMultiAgent(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig()
	off := false
	prof := cfg.Agents.Profiles[cfg.Agents.ActiveProfile]
	prof.MultiAgent = &off
	cfg.Agents.Enabled = false
	cfg.Agents.Profiles[cfg.Agents.ActiveProfile] = prof

	sess, err := NewWithSessionDir("", cfg, root, root)
	if err != nil {
		t.Fatalf("NewWithSessionDir: %v", err)
	}
	t.Cleanup(sess.Close)
	if sess.SettingsSnapshot().Agents.Enabled || sess.SubAgentRunner() != nil {
		t.Fatal("precondition: session must start with multi-agent off")
	}

	settings := sess.SettingsSnapshot()
	settings.Agents.Enabled = true
	on := true
	settings.SessionMultiAgent = &on
	if err := sess.HandleSettingsSave(settings); err != nil {
		t.Fatalf("HandleSettingsSave: %v", err)
	}
	if !sess.SettingsSnapshot().Agents.Enabled {
		t.Fatal("saved multi-agent on did not enable the session")
	}
	if sess.SubAgentRunner() == nil {
		t.Fatal("enabling multi-agent did not create the sub-agent runner")
	}
	snap := sess.SettingsSnapshot()
	if snap.SessionMultiAgent == nil || !*snap.SessionMultiAgent {
		t.Fatalf("snapshot missing session multi-agent override: %#v", snap)
	}
}

func TestApplyWorkspaceAgentDefaultsUpdatesMultiAgentFlag(t *testing.T) {
	on := true
	cfg := &Config{Agents: AgentsConfig{
		Enabled:       true,
		ActiveProfile: "default",
		Profiles: map[string]ProfileConfig{
			"default": {
				MultiAgent: &on,
				Categories: map[string]CategoryConfig{
					CategoryDefault: {Provider: "openrouter", Model: "model"},
				},
			},
		},
	}}
	ApplyWorkspaceAgentDefaults(cfg, ttypes.AgentsSettings{
		Enabled:       false,
		ActiveProfile: "default",
		Categories: map[string]ttypes.CategorySettings{
			CategoryDefault: {Provider: "openrouter", Model: "model"},
		},
	})
	if cfg.IsMultiAgentEnabled() || cfg.Agents.Enabled {
		t.Fatalf("workspace multi-agent off was not applied: %#v", cfg.Agents)
	}
}

func TestWorkspaceRefreshPreservesSessionMultiAgentOverride(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig()
	sess, err := NewWithSessionDir("", cfg, root, root)
	if err != nil {
		t.Fatalf("NewWithSessionDir: %v", err)
	}
	t.Cleanup(sess.Close)

	settings := sess.SettingsSnapshot()
	off := false
	settings.Agents.Enabled = false
	settings.SessionMultiAgent = &off
	if err := sess.HandleSettingsSave(settings); err != nil {
		t.Fatalf("HandleSettingsSave: %v", err)
	}

	workspaceSettings := ConfigToSettings(cfg)
	workspaceSettings.Agents.Enabled = true
	sess.UpdateWorkspaceDefaults(ttypes.AppSettings{Agents: workspaceSettings.Agents})

	snapshot := sess.SettingsSnapshot()
	if snapshot.SessionMultiAgent == nil || *snapshot.SessionMultiAgent || snapshot.Agents.Enabled {
		t.Fatalf("session UI lost explicit multi-agent off: %#v", snapshot)
	}
	if effective := sess.RuntimeProfile().GetEffectiveConfig("main"); effective.IsMultiAgentEnabled() {
		t.Fatalf("runtime ignored displayed multi-agent off: %#v", effective.Agents)
	}
}

func TestHandleSettingsSavePreservesMultiAgentInheritance(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig()
	off := false
	prof := cfg.Agents.Profiles[cfg.Agents.ActiveProfile]
	prof.MultiAgent = &off
	cfg.Agents.Enabled = false
	cfg.Agents.Profiles[cfg.Agents.ActiveProfile] = prof
	sess, err := NewWithSessionDir("", cfg, root, root)
	if err != nil {
		t.Fatalf("NewWithSessionDir: %v", err)
	}
	t.Cleanup(sess.Close)

	settings := sess.SettingsSnapshot()
	settings.SessionMultiAgent = nil
	if err := sess.HandleSettingsSave(settings); err != nil {
		t.Fatalf("HandleSettingsSave: %v", err)
	}
	if got := sess.SettingsSnapshot().SessionMultiAgent; got != nil {
		t.Fatalf("saving inherited settings created override %v", *got)
	}

	workspaceSettings := ConfigToSettings(cfg)
	workspaceSettings.Agents.Enabled = true
	sess.UpdateWorkspaceDefaults(ttypes.AppSettings{Agents: workspaceSettings.Agents})
	if !sess.RuntimeProfile().GetEffectiveConfig("main").IsMultiAgentEnabled() {
		t.Fatal("inherited session did not follow workspace multi-agent change")
	}
}

func TestSettingsToSessionOverridesHonorsExplicitMap(t *testing.T) {
	settings := ttypes.Settings{
		Agents: ttypes.AgentsSettings{Categories: map[string]ttypes.CategorySettings{
			"default": {Provider: "ignored", Model: "ignored"},
		}},
		SessionOverrides: map[string]ttypes.CategorySettings{
			"researcher": {
				Provider: "anthropic", Model: "claude",
				APIStandard: "anthropic", ThinkingEffort: "low",
			},
		},
	}
	overrides := settingsToSessionOverrides(settings)
	if len(overrides) != 1 || overrides["researcher"].Model != "claude" ||
		overrides["researcher"].APIStandard != "anthropic" {
		t.Fatalf("explicit override map not honored: %#v", overrides)
	}
}

func TestResumeHydratesCompletedSubAgents(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "parent.db")
	db, err := sessiondb.OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("OpenAndMigrate: %v", err)
	}
	if err := sessiondb.Create(db, sessiondb.SessionMeta{
		ID: "parent", ProjectPath: root, ProjectName: "project",
		CreatedAt: time.Now().UnixNano(),
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := sessiondb.Create(db, sessiondb.SessionMeta{
		ID: "child", ProjectPath: root, ProjectName: "project",
		CreatedAt: time.Now().UnixNano(), ParentSessionID: sql.NullString{
			String: "parent", Valid: true,
		},
	}); err != nil {
		t.Fatalf("Create child: %v", err)
	}
	if err := sessiondb.InsertSubAgentResult(db, sessiondb.SubAgentResultRow{
		ParentSessionID: "parent", ChildSessionID: "child",
		AgentID: "research-1", AgentName: "Research",
		AgentCategory: "researcher", Status: "completed",
		SummaryGist: "Found the lifecycle bug.", CreatedAt: time.Now().UnixNano(),
	}); err != nil {
		t.Fatalf("InsertSubAgentResult: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close seed DB: %v", err)
	}

	resumed, err := ResumeSessionWithDir(dbPath, testConfig(), root, root)
	if err != nil {
		t.Fatalf("ResumeSessionWithDir: %v", err)
	}
	t.Cleanup(resumed.Close)
	if resumed.SubAgentRunner() == nil {
		t.Fatal("sub-agent runner was not restored")
	}
	result, ok := resumed.SubAgentRunner().Manager().GetCompleted("research-1")
	if !ok || result.SummaryGist != "Found the lifecycle bug." ||
		result.SessionID != "child" {
		t.Fatalf("hydrated result = %+v, ok=%v", result, ok)
	}
}
