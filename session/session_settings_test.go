package session

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"crude/agent"
	"crude/provider"
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

func TestCategoryModelKeepsVendorPrefix(t *testing.T) {
	// Gateway catalogs (Command Code, OpenRouter) require the full vendored
	// model id on the wire — "z-ai/glm-5.3-flash". Neither the settings
	// round-trip nor category resolution may reduce it to the bare name.
	const vendored = "z-ai/glm-5.3-flash"

	stored := catSettingsToConfig(ttypes.CategorySettings{
		Provider: "commandcode",
		Model:    vendored,
	})
	if stored.Model != vendored {
		t.Fatalf("catSettingsToConfig stripped the vendor prefix: %q", stored.Model)
	}

	round := catConfigToSettings(stored)
	if round.Model != vendored {
		t.Fatalf("catConfigToSettings altered the model id: %q", round.Model)
	}

	base := &Config{
		ActiveProvider: "commandcode",
		Providers: map[string]ProviderConfig{
			"commandcode": {APIKey: "key", ProviderURL: "https://api.commandcode.ai/provider/v1"},
		},
		Agents: AgentsConfig{
			ActiveProfile: "default",
			Profiles: map[string]ProfileConfig{
				"default": {Categories: map[string]CategoryConfig{
					CategoryDefault: {Provider: "commandcode", Model: vendored},
				}},
			},
		},
	}
	if got := base.GetActiveDefaultCategory().Model; got != vendored {
		t.Fatalf("GetActiveDefaultCategory model = %q, want %q", got, vendored)
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
	childDir := filepath.Join(root, "parent", "subagents")
	if err := os.MkdirAll(childDir, 0o700); err != nil {
		t.Fatal(err)
	}
	childDB, err := sessiondb.OpenAndMigrate(filepath.Join(childDir, "child.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := sessiondb.Create(childDB, sessiondb.SessionMeta{
		ID: "child", ProjectPath: root, ProjectName: "project", Model: "child-model",
		CreatedAt: time.Now().UnixNano(), ParentSessionID: sql.NullString{
			String: "parent", Valid: true,
		},
	}); err != nil {
		t.Fatalf("Create child: %v", err)
	}
	childMessages := []provider.ChatMessage{
		provider.NewTextMessage("user", "# Assignment\n- Description: inspect lifecycle"),
		provider.NewTextMessage("assistant", "Report complete."),
	}
	if _, err := sessiondb.ReplaceMessagesWithMeta(childDB, "child", childMessages, []int{1, 1}, []sessiondb.DisplayMeta{{DisplayRole: "assignment"}, {DisplayRole: "assistant"}}); err != nil {
		t.Fatal(err)
	}
	if err := childDB.Close(); err != nil {
		t.Fatal(err)
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
	snapshots := resumed.CompletedSubAgentSnapshots()
	if len(snapshots) != 1 || snapshots[0].Model != "child-model" || len(snapshots[0].Messages) != 2 ||
		snapshots[0].Messages[0].Meta.DisplayRole != "assignment" {
		t.Fatalf("completed child snapshot = %+v", snapshots)
	}
}

func TestResumeRecoversLegacyChildWhenParentResultWasNeverPersisted(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	root := t.TempDir()
	parentPath := filepath.Join(root, "parent.db")
	parentDB, err := sessiondb.OpenAndMigrate(parentPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := sessiondb.Create(parentDB, sessiondb.SessionMeta{
		ID: "legacy-parent", ProjectPath: root, ProjectName: "project", CreatedAt: time.Now().UnixNano(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := parentDB.Close(); err != nil {
		t.Fatal(err)
	}
	legacyRoot, err := SessionsRoot()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(legacyRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	childPath := filepath.Join(legacyRoot, "legacy-child.db")
	childDB, err := sessiondb.OpenAndMigrate(childPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := sessiondb.Create(childDB, sessiondb.SessionMeta{
		ID: "legacy-child", ProjectPath: root, ProjectName: "project", Model: "child-model",
		CreatedAt:       time.Now().Add(-time.Second).UnixNano(),
		ParentSessionID: sql.NullString{String: "legacy-parent", Valid: true},
		AgentCategory:   sql.NullString{String: "aeron", Valid: true},
		AgentName:       sql.NullString{String: "legacy full child name", Valid: true},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := sessiondb.ReplaceMessages(childDB, "legacy-child", []provider.ChatMessage{
		provider.NewTextMessage("user", "# Assignment\nlegacy"),
		provider.NewTextMessage("assistant", "Legacy child completed successfully."),
	}, []int{1, 1}); err != nil {
		t.Fatal(err)
	}
	if err := sessiondb.CloseSession(childDB, "legacy-child"); err != nil {
		t.Fatal(err)
	}
	if err := childDB.Close(); err != nil {
		t.Fatal(err)
	}

	resumed, err := ResumeSessionWithDir(parentPath, testConfig(), root, filepath.Join(root, "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(resumed.Close)
	result, ok := resumed.SubAgentRunner().Manager().GetCompleted("legacy-child")
	if !ok || result.Name != "legacy full child name" || result.Status != agent.SubAgentCompleted ||
		!strings.Contains(result.SummaryGist, "completed successfully") {
		t.Fatalf("recovered legacy result = %+v ok=%v", result, ok)
	}
	rows, err := sessiondb.ListSubAgentResults(resumed.DB(), "legacy-parent")
	if err != nil || len(rows) != 1 || rows[0].ChildSessionID != "legacy-child" {
		t.Fatalf("recovered parent result rows = %+v err=%v", rows, err)
	}
}

func TestResumeRestoresActiveContextIndependentlyFromFullArchive(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "wire-parent.db")
	db, err := sessiondb.OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := sessiondb.Create(db, sessiondb.SessionMeta{
		ID: "wire-parent", ProjectPath: root, ProjectName: "project", CreatedAt: time.Now().UnixNano(),
	}); err != nil {
		t.Fatal(err)
	}
	archive := []provider.ChatMessage{
		provider.NewTextMessage("user", "old request"),
		provider.NewTextMessage("assistant", "old answer"),
		provider.NewTextMessage("user", "current request"),
		provider.NewTextMessage("assistant", "current answer"),
	}
	if _, err := sessiondb.ReplaceMessages(db, "wire-parent", archive, []int{1, 1, 2, 2}); err != nil {
		t.Fatal(err)
	}
	active := []provider.ChatMessage{
		provider.NewTextMessage("system", "<context_checkpoint>old archive</context_checkpoint>"),
		provider.NewTextMessage("user", "current request"),
		provider.NewTextMessage("assistant", "current answer"),
	}
	if err := sessiondb.SaveContextState(db, "wire-parent", sessiondb.ContextState{
		Generation: 7, Reason: "manual_compaction", Messages: active,
	}); err != nil {
		t.Fatal(err)
	}
	if err := sessiondb.InsertCostSnapshot(db, 120, 30, 20, 0.42, nil); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	resumed, err := ResumeSessionWithDir(dbPath, testConfig(), root, filepath.Join(root, "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(resumed.Close)
	if len(resumed.msgs) != 4 || len(resumed.activeContext) != 3 || resumed.activeContext[0].Role != "system" ||
		resumed.contextGeneration != 7 {
		t.Fatalf("archive=%d active=%#v generation=%d", len(resumed.msgs), resumed.activeContext, resumed.contextGeneration)
	}
	cost := resumed.CostSnapshot()
	if cost.TotalPromptTokens != 120 || cost.TotalCompletionTokens != 30 || cost.TotalCachedTokens != 20 || cost.TotalCost != 0.42 {
		t.Fatalf("restored cumulative cost snapshot = %#v", cost)
	}
}

func TestContextCompactionDefaultsAndLiveInheritance(t *testing.T) {
	cfg := testConfig()
	main := cfg.GetActiveDefaultCategory()
	if main.ContextCompactionPercent == nil || *main.ContextCompactionPercent != 85 {
		t.Fatalf("default compaction = %#v", main.ContextCompactionPercent)
	}
	disabled := 0
	profile := cfg.Agents.Profiles[cfg.Agents.ActiveProfile]
	profile.Categories[CategoryDefault] = CategoryConfig{
		Provider: cfg.ActiveProvider, Model: main.Model, ContextCompactionPercent: &disabled,
	}
	profile.Categories[CategoryResearcher] = CategoryConfig{Inherit: InheritMain}
	cfg.Agents.Profiles[cfg.Agents.ActiveProfile] = profile
	researcher := cfg.GetCategoryConfig(CategoryResearcher)
	if researcher.ContextCompactionPercent == nil || *researcher.ContextCompactionPercent != 0 {
		t.Fatalf("inherited disabled compaction = %#v", researcher.ContextCompactionPercent)
	}
}
