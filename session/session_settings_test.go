package session

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"crude/agent"
	"crude/provider"
	"crude/sessiondb"
	ttypes "crude/tui/types"
	recoverytypes "crude/types/recovery"
)

func TestConfigWithSessionOverridesUsesWorkspaceThenSession(t *testing.T) {
	compact := 2
	base := &Config{
		ActiveProvider: "openrouter",
		Accounts: map[string]provider.Account{
			"router-account": {ID: "router-account", ProviderID: "openrouter", APIKey: "router-secret"},
			"openai-account": {ID: "openai-account", ProviderID: "openai", APIKey: "openai-secret"},
		},
		Providers: map[string]ProviderConfig{
			"openrouter": provider.DefaultTemplates()["openrouter"],
			"openai": {
				Name: "OpenAI", ProviderURL: "https://api.openai.com/v1", APIStandard: "openai-responses", AuthType: provider.AuthProtocol,
			},
		},
		Agents: AgentsConfig{
			ActiveProfile: "default",
			Profiles: map[string]ProfileConfig{
				"default": {Categories: map[string]CategoryConfig{
					CategoryDefault: {AccountID: "router-account", Model: "workspace-main", ThinkingEffort: "medium"},
					"crafter":       {AccountID: "router-account", Model: "workspace-crafter"},
				}},
			},
		},
	}
	overrides := map[string]sessiondb.AgentSettingOverride{
		"crafter": {AccountID: "openai-account", Model: "gpt-5", ThinkingEffort: "high", ContextCompactionPercent: &compact},
	}

	effective := configWithSessionOverrides(base, overrides)
	crafter := effective.GetCategoryConfig("crafter")
	if crafter.AccountID != "openai-account" || crafter.Model != "gpt-5" || crafter.ThinkingEffort != "high" {
		t.Fatalf("crafter override not applied: %#v", crafter)
	}
	if effective.templateIDForAccount(crafter.AccountID) != "openai" {
		t.Fatalf("crafter account did not resolve its template: %#v", crafter)
	}
	if crafter.ContextCompactionPercent == nil || *crafter.ContextCompactionPercent != 2 {
		t.Fatalf("crafter compaction threshold = %v, want 2", crafter.ContextCompactionPercent)
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
		AccountID: "command-account",
		Model:     vendored,
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
			"commandcode": provider.DefaultTemplates()["commandcode"],
		},
		Agents: AgentsConfig{
			ActiveProfile: "default",
			Profiles: map[string]ProfileConfig{
				"default": {Categories: map[string]CategoryConfig{
					CategoryDefault: {AccountID: "command-account", Model: vendored},
				}},
			},
		},
	}
	if got := base.GetActiveDefaultCategory().Model; got != vendored {
		t.Fatalf("GetActiveDefaultCategory model = %q, want %q", got, vendored)
	}
}

func TestCategoryAccountRequiresMainAgentSelection(t *testing.T) {
	cfg := &Config{ActiveProvider: "opencode-go", Providers: map[string]ProviderConfig{"opencode-go": provider.DefaultTemplates()["opencode-go"]},
		Agents: DefaultAgentsConfig()}
	got := cfg.GetActiveDefaultCategory()
	if got.AccountID != "" {
		t.Fatalf("category selected an account implicitly: %q", got.AccountID)
	}
	settings := ConfigToSettings(cfg)
	if settings.AccountID != "" || settings.APIKey != "" || settings.ProviderAPIKey != "" {
		t.Fatalf("session settings leaked credentials or lost account: %+v", settings)
	}
}

func TestWorkspaceAgentDefaultStoresAccountNotSecret(t *testing.T) {
	cfg := &Config{ActiveProvider: "opencode-go", Providers: map[string]ProviderConfig{"opencode-go": provider.DefaultTemplates()["opencode-go"]}, Agents: DefaultAgentsConfig()}
	ApplyWorkspaceAgentDefaults(cfg, ttypes.AgentsSettings{Categories: map[string]ttypes.CategorySettings{
		CategoryDefault: {Provider: "opencode-go", AccountID: "account", APIKey: "discarded", Model: "m"},
	}})
	stored := cfg.Agents.Profiles["default"].Categories[CategoryDefault]
	if stored.AccountID != "account" || stored.Model != "m" {
		t.Fatalf("stored category: %+v", stored)
	}
}

func TestEnsureAgentsDefaultsRestoresOmittedMultiAgent(t *testing.T) {
	agents := AgentsConfig{ActiveProfile: "default", Profiles: map[string]ProfileConfig{
		"default": {Categories: map[string]CategoryConfig{
			CategoryDefault: {AccountID: "router-account", Model: "m"},
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
	if sess.SubAgentRunner() != nil {
		t.Fatal("enabling multi-agent eagerly created a runner before any launch")
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
					CategoryDefault: {AccountID: "account", Model: "model"},
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
	compact := 2
	settings := ttypes.Settings{
		Agents: ttypes.AgentsSettings{Categories: map[string]ttypes.CategorySettings{
			"default": {Provider: "ignored", Model: "ignored"},
		}},
		SessionOverrides: map[string]ttypes.CategorySettings{
			"researcher": {
				AccountID: "anthropic-account", Model: "claude", ThinkingEffort: "low",
				ContextCompactionPercent: &compact,
			},
		},
	}
	overrides := settingsToSessionOverrides(settings)
	if len(overrides) != 1 || overrides["researcher"].Model != "claude" ||
		overrides["researcher"].AccountID != "anthropic-account" || overrides["researcher"].ContextCompactionPercent == nil ||
		*overrides["researcher"].ContextCompactionPercent != 2 {
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
		ID: "parent", AccountID: "account", ProjectPath: root, ProjectName: "project",
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
	if err := sessiondb.InsertSubAgentResult(db, sessiondb.SubAgentResultRow{
		ParentSessionID: "parent", ChildSessionID: "running-1",
		AgentID: "running-1", AgentName: "Interrupted", AgentCategory: "crafter",
		Status: "running", CreatedAt: time.Now().UnixNano(),
	}); err != nil {
		t.Fatalf("Insert running SubAgentResult: %v", err)
	}
	owner, err := json.Marshal(childRecoveryOwner{Spec: agent.SubAgentSpec{
		ID: "running-1", Name: "Interrupted", Category: agent.CatCrafter,
		Description: "finish pending child work", Model: "test-model",
		AccountID: "account", ParentID: "parent-orchestrator", ParentTurn: 1, Depth: 1,
	}})
	if err != nil {
		t.Fatal(err)
	}
	assignment := provider.NewTextMessage("user", "finish pending child work")
	assignment.Origin = provider.OriginAssignment
	parentRecovery := sessiondb.RecoveryCheckpoint{
		Status: recoverytypes.Status{
			ID: "recovery-running-1", AgentID: "running-1", Generation: 1,
			State: recoverytypes.Recovering, Attempt: 1,
			StartedAt: time.Now(), UpdatedAt: time.Now(),
			Summary: "Provider connection is recovering", Action: "Stop to cancel",
		},
		PendingInput: provider.UserInput{
			InternalID: "assignment-running-1", Text: "finish pending child work",
			Origin: provider.OriginAssignment,
		},
		InputAdmitted: true, Archive: []provider.ChatMessage{assignment},
		ActiveContext: []provider.ChatMessage{assignment}, ContextGeneration: 1,
		ContextReason: "child_assignment", OwnerData: owner,
	}
	if err := sessiondb.SaveRecoveryCheckpoint(db, "parent", parentRecovery); err != nil {
		t.Fatal(err)
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
	runningDB, err := sessiondb.OpenAndMigrate(filepath.Join(childDir, "running-1.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := sessiondb.Create(runningDB, sessiondb.SessionMeta{
		ID: "running-1", ProjectPath: root, ProjectName: "project", Model: "test-model",
		AccountID: "account", CreatedAt: time.Now().UnixNano(),
		ParentSessionID: sql.NullString{String: "parent", Valid: true},
		AgentCategory:   sql.NullString{String: agent.CatCrafter, Valid: true},
		AgentName:       sql.NullString{String: "Interrupted", Valid: true}, Depth: 1,
	}); err != nil {
		t.Fatal(err)
	}
	childRecovery := parentRecovery
	childRecovery.ContextOwnerID = "running-1"
	if err := sessiondb.SaveRecoveryCheckpoint(runningDB, "running-1", childRecovery); err != nil {
		t.Fatal(err)
	}
	if err := runningDB.Close(); err != nil {
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
	if interrupted, ok := resumed.SubAgentRunner().Manager().GetCompleted("running-1"); ok {
		t.Fatalf("unfinished child was terminalized: %+v", interrupted)
	}
	recoveries, err := sessiondb.LoadRecoveryRecords(resumed.DB(), resumed.SessionID())
	if err != nil || len(recoveries) != 1 || recoveries[0].State != recoverytypes.Interrupted {
		t.Fatalf("passive child recovery = %+v, err=%v", recoveries, err)
	}
	snapshots := resumed.CompletedSubAgentSnapshots()
	if len(snapshots) != 1 || snapshots[0].Model != "child-model" || len(snapshots[0].Messages) != 2 ||
		snapshots[0].Messages[0].Meta.DisplayRole != "assignment" {
		t.Fatalf("completed child snapshot = %+v", snapshots)
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
		ID: "wire-parent", AccountID: "account", ProjectPath: root, ProjectName: "project", CreatedAt: time.Now().UnixNano(),
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
		{
			Role: "user", Origin: provider.OriginCheckpoint, Visibility: provider.VisibilityContextOnly,
			Content: provider.TextContent(`<context_checkpoint role="root" version="2">old archive</context_checkpoint>`),
		},
		provider.NewTextMessage("user", "current request"),
		provider.NewTextMessage("assistant", "current answer"),
	}
	if err := sessiondb.SaveContextState(db, "wire-parent", "wire-parent", sessiondb.ContextState{
		Generation: 7, Reason: "manual_compaction", Messages: active,
	}); err != nil {
		t.Fatal(err)
	}
	if err := sessiondb.UpsertUsageRecord(db, sessiondb.UsageRecord{
		RequestID: "wire-parent/7/1", SessionID: "wire-parent", AgentID: "root",
		TurnGeneration: 7, PromptTokens: 120, CompletionTokens: 30,
		CachedTokens: 20, CacheReadKnown: true, CostUSD: 0.42, CostKnown: true,
		ModelID: "test-model", CreatedAt: 1, UpdatedAt: 1,
	}); err != nil {
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
	if len(resumed.msgs) != 4 || len(resumed.activeContext) != 3 || resumed.activeContext[0].EffectiveOrigin() != provider.OriginCheckpoint ||
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
		AccountID: main.AccountID, Model: main.Model, ContextCompactionPercent: &disabled,
	}
	profile.Categories[CategoryResearcher] = CategoryConfig{Inherit: InheritMain}
	cfg.Agents.Profiles[cfg.Agents.ActiveProfile] = profile
	researcher := cfg.GetCategoryConfig(CategoryResearcher)
	if researcher.ContextCompactionPercent == nil || *researcher.ContextCompactionPercent != 0 {
		t.Fatalf("inherited disabled compaction = %#v", researcher.ContextCompactionPercent)
	}
}
