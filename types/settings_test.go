package types

import "testing"

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
	tab := 8
	editor := EditorSettings{TabSize: tab, WordWrap: true}
	got := MergeAppWorkspace(app, WorkspaceOverrides{Editor: &editor})
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
	if got.Theme != nil {
		t.Fatal("unchanged theme must inherit")
	}
	if got.Editor == nil || got.Editor.TabSize != 2 {
		t.Fatalf("editor override missing: %#v", got.Editor)
	}
	if got.ActiveProvider == nil || *got.ActiveProvider != "openai" {
		t.Fatalf("provider override missing: %#v", got.ActiveProvider)
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
	if got.Providers["openrouter"].APIKey != "" {
		t.Fatalf("workspace overlay leaked API key: %#v", got.Providers)
	}
	if got.Providers["openrouter"].Model != "ws-model" {
		t.Fatalf("model override missing: %#v", got.Providers)
	}
	merged := MergeAppWorkspace(app, got)
	if merged.Providers["openrouter"].APIKey != "app-key" {
		t.Fatalf("effective API key should stay app-level, got %#v", merged.Providers)
	}
	if merged.Providers["openrouter"].Model != "ws-model" {
		t.Fatalf("effective model should use workspace overlay, got %#v", merged.Providers)
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
	overlay := WorkspaceOverrides{Providers: map[string]ProviderConfig{
		"openai": {
			APIStandard:    "openai",
			ModelStandards: map[string]string{"gpt-5.6": "openai-responses"},
		},
	}}
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
