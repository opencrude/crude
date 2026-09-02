package types

import (
	"strings"
	"testing"
)

func TestMergeAppWorkspaceInheritsNilOverrides(t *testing.T) {
	app := AppSettings{
		Theme:          ThemeSettings{Preset: "dark"},
		Editor:         EditorSettings{TabSize: 4, WordWrap: true},
		ActiveProvider: "openrouter",
		Providers:      map[string]ProviderConfig{"openrouter": {APIKey: "app-key", Model: "app-model"}},
	}
	got := MergeAppWorkspace(app, WorkspaceOverrides{})
	if got.Theme.Preset != "dark" || got.Editor.TabSize != 4 || !got.Editor.WordWrap {
		t.Fatalf("inherited app values lost: %#v", got)
	}
	if got.Providers["openrouter"].APIKey != "app-key" {
		t.Fatalf("provider credentials must stay app-level unless overridden: %#v", got.Providers)
	}
}

func TestMergeAppWorkspaceOverridesSelectedCategories(t *testing.T) {
	app := AppSettings{
		Theme:  ThemeSettings{Preset: "dark"},
		Editor: EditorSettings{TabSize: 4},
		LSP:    DefaultLSPSettings(),
	}
	ApplyAppDefaults(&app)
	working := cloneAppSettings(app)
	working.Editor.TabSize = 8
	working.Editor.WordWrap = true

	got := MergeAppWorkspace(app, OverridesFromDiff(app, working))
	if got.Theme.Preset != "dark" {
		t.Fatalf("theme should inherit, got %q", got.Theme.Preset)
	}
	if got.Editor.TabSize != 8 || !got.Editor.WordWrap {
		t.Fatalf("editor override not applied: %#v", got.Editor)
	}
}

func TestOverridesFromDiffRecordsOnlyChangedCategories(t *testing.T) {
	app := AppSettings{
		Theme:          ThemeSettings{Preset: "dark"},
		Editor:         EditorSettings{TabSize: 4},
		ActiveProvider: "openrouter",
	}
	ApplyAppDefaults(&app)
	working := cloneAppSettings(app)
	working.Editor.TabSize = 2
	working.ActiveProvider = "openai"
	got := OverridesFromDiff(app, working)
	if len(got.Theme) != 0 {
		t.Fatalf("unchanged theme must inherit, got %s", got.Theme)
	}
	if string(got.Editor) != `{"tab_size":2}` {
		t.Fatalf("editor override should carry only the changed leaf, got %s", got.Editor)
	}
	if got.ActiveProvider == nil || *got.ActiveProvider != "openai" {
		t.Fatalf("provider override missing: %#v", got.ActiveProvider)
	}
}

// A workspace that overrides ONE field must keep inheriting every sibling
// field, including application changes made after the override was stored.
// Whole-category snapshots made a workspace behave like a hard copy: turning
// something off at the application level had no effect on a workspace that
// had once touched anything in that category.
func TestWorkspaceInheritsFieldsItNeverTouched(t *testing.T) {
	app := AppSettings{Editor: EditorSettings{TabSize: 4, LineNumbers: true, Minimap: true}}
	ApplyAppDefaults(&app)

	working := cloneAppSettings(app)
	working.Editor.TabSize = 2
	overrides := OverridesFromDiff(app, working)

	// The application later turns two untouched features off.
	app.Editor.Minimap = false
	app.Editor.LineNumbers = false

	got := MergeAppWorkspace(app, overrides)
	if got.Editor.TabSize != 2 {
		t.Fatalf("explicit workspace override lost: %#v", got.Editor)
	}
	if got.Editor.Minimap || got.Editor.LineNumbers {
		t.Fatalf("untouched fields must follow the application layer: %#v", got.Editor)
	}
}

// Language-intelligence toggles are the worst case: LSPSettings is one big
// category, so a per-category overlay froze every feature flag at once.
func TestWorkspaceInheritsLSPFlagsItNeverTouched(t *testing.T) {
	app := AppSettings{LSP: DefaultLSPSettings()}
	ApplyAppDefaults(&app)

	working := cloneAppSettings(app)
	working.LSP.MinSeverity = "error"
	overrides := OverridesFromDiff(app, working)

	app.LSP.Completions = false
	app.LSP.Hover = false

	got := MergeAppWorkspace(app, overrides)
	if got.LSP.MinSeverity != "error" {
		t.Fatalf("explicit override lost: %#v", got.LSP)
	}
	if got.LSP.Completions || got.LSP.Hover {
		t.Fatal("app-level LSP feature switches must reach workspaces that never touched them")
	}
	if !got.LSP.Definition || !got.LSP.Diagnostics {
		t.Fatal("inheriting must not disable unrelated features")
	}
}

// Turning a boolean off is an override, not an absence. `omitempty` elides
// false, so a naive diff would treat "disabled here" as "inherit".
func TestWorkspaceCanOverrideBoolToFalse(t *testing.T) {
	app := AppSettings{Editor: EditorSettings{TabSize: 4, WordWrap: true}}
	ApplyAppDefaults(&app)

	working := cloneAppSettings(app)
	working.Editor.WordWrap = false
	overrides := OverridesFromDiff(app, working)
	if len(overrides.Editor) == 0 {
		t.Fatal("disabling a field must record an override")
	}
	if got := MergeAppWorkspace(app, overrides); got.Editor.WordWrap {
		t.Fatalf("explicit false must win over an inherited true: %#v", got.Editor)
	}
}

func TestWorkspaceLocalHistoryOverridesRemainFieldLevel(t *testing.T) {
	app := AppSettings{LocalHistory: LocalHistorySettings{Enabled: "on", UseGitIgnore: "on", StorageBudgetMB: 1024, RetentionKeepDaily: 30}}
	ApplyAppDefaults(&app)
	working := cloneAppSettings(app)
	working.LocalHistory.StorageBudgetMB = 256
	overrides := OverridesFromDiff(app, working)
	app.LocalHistory.RetentionKeepDaily = 60
	got := MergeAppWorkspace(app, overrides)
	if got.LocalHistory.StorageBudgetMB != 256 || got.LocalHistory.RetentionKeepDaily != 60 || !got.LocalHistory.EnabledValue() {
		t.Fatalf("merged local history=%+v", got.LocalHistory)
	}
}

// Loading re-diffs stored overlays, which is how legacy whole-category rows
// heal into sparse ones. That round trip has to be stable.
func TestOverridesRoundTripIsStable(t *testing.T) {
	app := AppSettings{
		Theme:  ThemeSettings{Preset: "dark"},
		Editor: EditorSettings{TabSize: 4},
		LSP:    DefaultLSPSettings(),
		Agents: AgentsSettings{Enabled: true, MaxConcurrent: 3},
	}
	ApplyAppDefaults(&app)
	working := cloneAppSettings(app)
	working.Editor.TabSize = 2
	working.LSP.Rename = false
	working.Agents.MaxConcurrent = 8

	first := OverridesFromDiff(app, working)
	second := OverridesFromDiff(app, MergeAppWorkspace(app, first))
	if string(first.Editor) != string(second.Editor) ||
		string(first.LSP) != string(second.LSP) ||
		string(first.Agents) != string(second.Agents) {
		t.Fatalf("round trip drifted:\nfirst =%s %s %s\nsecond=%s %s %s",
			first.Editor, first.LSP, first.Agents, second.Editor, second.LSP, second.Agents)
	}
	merged := MergeAppWorkspace(app, second)
	if merged.Editor.TabSize != 2 || merged.LSP.Rename || merged.Agents.MaxConcurrent != 8 {
		t.Fatalf("round trip lost overrides: %#v %#v %#v", merged.Editor, merged.LSP, merged.Agents)
	}
}

func TestLSPLanguageEnabledAndServerOverride(t *testing.T) {
	s := DefaultLSPSettings()
	if !s.LanguageEnabled(".go") {
		t.Fatal("go should be enabled by default")
	}
	lang := s.Languages["go"]
	lang.Enabled = false
	s.Languages["go"] = lang
	if s.LanguageEnabled(".go") {
		t.Fatal("disabled go must not start")
	}
	s.Enabled = false
	if s.LanguageEnabled(".rs") {
		t.Fatal("master switch must disable every language")
	}

	s = DefaultLSPSettings()
	lang = s.Languages["python"]
	lang.Command = "pylsp"
	lang.Args = nil
	s.Languages["python"] = lang
	cmd, args, ok := s.ServerOverride(".py")
	if !ok || cmd != "pylsp" || len(args) != 0 {
		t.Fatalf("python override: cmd=%q args=%v ok=%v", cmd, args, ok)
	}
}

func TestNormalizeLSPMigratesLegacyFeatureFlags(t *testing.T) {
	// Old config.json only had enabled/auto_detect/diagnostics. New display
	// and feature bools load as false and must not hide diagnostics.
	got := NormalizeLSP(LSPSettings{Enabled: true, AutoDetect: true, Diagnostics: true})
	if !got.DiagInEditor || !got.DiagInTree || !got.DiagInStatus {
		t.Fatalf("legacy diagnostics must show in editor/tree/status: %#v", got)
	}
	if !got.Completions || !got.Hover || !got.Definition || !got.StartOnOpen {
		t.Fatalf("legacy configs must inherit feature defaults: %#v", got)
	}
	if !got.Enabled || !got.Diagnostics {
		t.Fatal("migration must not flip Enabled/Diagnostics")
	}

	explicit := NormalizeLSP(LSPSettings{
		Enabled: true, Diagnostics: true, Completions: true,
		DiagInEditor: false, DiagInTree: true, DiagInStatus: true,
	})
	if explicit.DiagInEditor {
		t.Fatal("explicit editor-off must survive when other new flags are set")
	}
}

func TestLSPSeverityAtLeast(t *testing.T) {
	s := LSPSettings{MinSeverity: "warning"}
	if !s.SeverityAtLeast(1) || !s.SeverityAtLeast(2) || s.SeverityAtLeast(3) {
		t.Fatalf("warning threshold failed")
	}
}

func TestWorkspaceOverridesFromDiffOmitsAPIKeys(t *testing.T) {
	app := AppSettings{
		ActiveProvider: "openrouter",
		Providers:      map[string]ProviderConfig{"openrouter": {APIKey: "app-key", Model: "app-model"}},
	}
	ApplyAppDefaults(&app)
	working := cloneAppSettings(app)
	working.Providers["openrouter"] = ProviderConfig{APIKey: "workspace-key", Model: "app-model"}
	got := WorkspaceOverridesFromDiff(app, working)
	if len(got.Providers) != 0 {
		t.Fatalf("credential-only edit must not create a workspace overlay: %#v", got.Providers)
	}

	working.Providers["openrouter"] = ProviderConfig{APIKey: "workspace-key", Model: "ws-model"}
	got = WorkspaceOverridesFromDiff(app, working)
	if strings.Contains(string(got.Providers), "workspace-key") {
		t.Fatalf("workspace overlay leaked API key: %s", got.Providers)
	}
	if !strings.Contains(string(got.Providers), "ws-model") {
		t.Fatalf("model override missing: %s", got.Providers)
	}
	merged := MergeAppWorkspace(app, got)
	if merged.Providers["openrouter"].APIKey != "app-key" {
		t.Fatalf("effective API key should stay app-level, got %#v", merged.Providers)
	}
	if merged.Providers["openrouter"].Model != "ws-model" {
		t.Fatalf("effective model should use workspace overlay, got %#v", merged.Providers)
	}
}

func TestWorkspaceOverridesFromDiffOmitsAgentAPIKeys(t *testing.T) {
	app := AppSettings{
		ActiveProvider: "opencode-go",
		Providers:      map[string]ProviderConfig{"opencode-go": {APIKey: "app-key", Model: "app-model"}},
		Agents: AgentsSettings{Categories: map[string]CategorySettings{
			"default": {Provider: "opencode-go", APIKey: "app-key", Model: "app-model"},
		}},
	}
	ApplyAppDefaults(&app)
	working := cloneAppSettings(app)
	working.Agents.Categories["default"] = CategorySettings{
		Provider: "opencode-go", APIKey: "ws-key", Model: "ws-model",
	}
	got := WorkspaceOverridesFromDiff(app, working)
	if strings.Contains(string(got.Agents), "ws-key") || strings.Contains(string(got.Agents), "app-key") {
		t.Fatalf("workspace agent overlay leaked API key: %s", got.Agents)
	}
	if !strings.Contains(string(got.Agents), "ws-model") {
		t.Fatalf("agent model overlay missing: %s", got.Agents)
	}
}

func TestProviderProtocolOverridesMergeAndClone(t *testing.T) {
	app := AppSettings{
		ActiveProvider: "openai",
		Providers: map[string]ProviderConfig{
			"openai": {
				APIStandard:    "openai-responses",
				ModelStandards: map[string]string{"gpt-legacy": "openai"},
			},
		},
	}
	ApplyAppDefaults(&app)
	working := cloneAppSettings(app)
	working.Providers["openai"] = ProviderConfig{
		APIStandard:    "openai",
		ModelStandards: map[string]string{"gpt-legacy": "openai", "gpt-5.6": "openai-responses"},
	}
	overlay := OverridesFromDiff(app, working)
	got := MergeAppWorkspace(app, overlay)
	cfg := got.Providers["openai"]
	if cfg.APIStandard != "openai" ||
		cfg.ModelStandards["gpt-legacy"] != "openai" ||
		cfg.ModelStandards["gpt-5.6"] != "openai-responses" {
		t.Fatalf("merged provider protocols = %#v", cfg)
	}
	cfg.ModelStandards["gpt-legacy"] = "anthropic"
	if app.Providers["openai"].ModelStandards["gpt-legacy"] != "openai" {
		t.Fatal("merged model-standard map aliases application settings")
	}
}
