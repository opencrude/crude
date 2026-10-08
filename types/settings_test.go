package types

import (
	"reflect"
	"strings"

	"crude/provider"
	"testing"
)

func TestMergeAppWorkspaceInheritsNilOverrides(t *testing.T) {
	app := AppSettings{
		Theme:    ThemeSettings{Preset: "dark"},
		Editor:   EditorSettings{TabSize: 4, WordWrap: true},
		Accounts: map[string]provider.Account{"work": {ID: "work", Connection: provider.DefaultTemplates()["openrouter"]}},
	}
	got := MergeAppWorkspace(app, WorkspaceOverrides{})
	if got.Theme.Preset != "dark" || got.Editor.TabSize != 4 || !got.Editor.WordWrap {
		t.Fatalf("inherited app values lost: %#v", got)
	}
	if got.Accounts["work"].Connection.ProviderURL != "https://openrouter.ai/api/v1" {
		t.Fatalf("account connections must stay application-level: %#v", got.Accounts)
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
		Theme:  ThemeSettings{Preset: "dark"},
		Editor: EditorSettings{TabSize: 4},
	}
	ApplyAppDefaults(&app)
	working := cloneAppSettings(app)
	working.Editor.TabSize = 2
	got := OverridesFromDiff(app, working)
	if len(got.Theme) != 0 {
		t.Fatalf("unchanged theme must inherit, got %s", got.Theme)
	}
	if string(got.Editor) != `{"tab_size":2}` {
		t.Fatalf("editor override should carry only the changed leaf, got %s", got.Editor)
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

func TestAutoPermissionSettingsNormalizedDefaultsAndBounds(t *testing.T) {
	got := (AutoPermissionSettings{}).Normalized()
	if got.Enabled != "off" || got.DenyMode != "ask" || got.UnresolvedMode != "ask" || got.ThinkingEffort != "low" ||
		got.ResponseFormat != AutoPermissionFormatAuto || got.TimeoutSeconds != 15 ||
		got.MaxOutputTokens != 256 || got.MaxAttempts != 3 || got.RetryTokenGrowth != AutoPermissionGrowth4x ||
		got.MaxRetryOutputTokens != 1024 || got.MaxInputBytes != 64<<10 {
		t.Fatalf("normalized defaults = %+v", got)
	}

	got = (AutoPermissionSettings{
		Enabled: "yes", DenyMode: "reject", UnresolvedMode: "allow", ThinkingEffort: "unlimited", ResponseFormat: "xml", TimeoutSeconds: -4,
		MaxOutputTokens: 99999, MaxAttempts: 99, RetryTokenGrowth: "random", MaxRetryOutputTokens: 10,
		MaxInputBytes: 10,
	}).Normalized()
	if got.Enabled != "off" || got.DenyMode != "ask" || got.UnresolvedMode != "ask" || got.TimeoutSeconds != 1 ||
		got.ThinkingEffort != "low" || got.ResponseFormat != AutoPermissionFormatAuto ||
		got.MaxOutputTokens != 4096 || got.MaxAttempts != 6 || got.RetryTokenGrowth != AutoPermissionGrowth4x ||
		got.MaxRetryOutputTokens != 4096 || got.MaxInputBytes != 64<<10 {
		t.Fatalf("normalized bounds = %+v", got)
	}

	got = (AutoPermissionSettings{Enabled: "on", DenyMode: "deny", UnresolvedMode: "deny", ThinkingEffort: "high",
		ResponseFormat: AutoPermissionFormatPrompt, TimeoutSeconds: 120, MaxOutputTokens: 16,
		MaxAttempts: 1, RetryTokenGrowth: AutoPermissionGrowthFixed, MaxRetryOutputTokens: 16,
		MaxInputBytes: 1 << 20}).Normalized()
	if got.Enabled != "on" || got.DenyMode != "deny" || got.UnresolvedMode != "deny" || got.ThinkingEffort != "high" ||
		got.ResponseFormat != AutoPermissionFormatPrompt || got.MaxAttempts != 1 {
		t.Fatalf("valid choices changed = %+v", got)
	}
}

func TestAutoPermissionReviewOutputTokensFollowPersistedStrategy(t *testing.T) {
	settings := AutoPermissionSettings{MaxOutputTokens: 96, MaxAttempts: 3,
		RetryTokenGrowth: AutoPermissionGrowth4x, MaxRetryOutputTokens: 1024}
	if got := []int{settings.ReviewOutputTokens(0), settings.ReviewOutputTokens(1), settings.ReviewOutputTokens(2)}; !reflect.DeepEqual(got, []int{96, 384, 1024}) {
		t.Fatalf("4x budgets = %v", got)
	}
	settings.RetryTokenGrowth = AutoPermissionGrowth2x
	settings.MaxRetryOutputTokens = 300
	if got := []int{settings.ReviewOutputTokens(0), settings.ReviewOutputTokens(1), settings.ReviewOutputTokens(2)}; !reflect.DeepEqual(got, []int{96, 192, 300}) {
		t.Fatalf("2x budgets = %v", got)
	}
	settings.RetryTokenGrowth = AutoPermissionGrowthFixed
	if got := settings.ReviewOutputTokens(5); got != 96 {
		t.Fatalf("fixed budget = %d", got)
	}
}

func TestWorkspaceAutoPermissionExplicitOffStaysSparse(t *testing.T) {
	app := AppSettings{Safety: SafetySettings{AutoPermission: AutoPermissionSettings{
		Enabled: "on", Provider: "openai", Model: "gpt-review", DenyMode: "ask",
		TimeoutSeconds: 8, MaxOutputTokens: 96, MaxInputBytes: 64 << 10,
	}}}
	ApplyAppDefaults(&app)
	working := cloneAppSettings(app)
	working.Safety.AutoPermission.Enabled = "off"
	overrides := OverridesFromDiff(app, working)
	if string(overrides.Safety) != `{"auto_permission":{"enabled":"off"}}` {
		t.Fatalf("auto-permission override = %s", overrides.Safety)
	}

	app.Safety.AutoPermission.Model = "gpt-review-v2"
	got := MergeAppWorkspace(app, overrides).Safety.AutoPermission
	if got.Enabled != "off" || got.Model != "gpt-review-v2" || got.Provider != "openai" {
		t.Fatalf("merged auto-permission = %+v", got)
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

func TestNormalizeLSPPreservesExplicitAllFeaturesOff(t *testing.T) {
	s := DefaultLSPSettings()
	s.StartOnOpen, s.Completions, s.Hover, s.Definition, s.References, s.Outline, s.Rename, s.AutoComplete = false, false, false, false, false, false, false, false
	s.DiagInEditor, s.DiagInTree, s.DiagInStatus = false, false, false
	got := NormalizeLSP(s)
	if got.StartOnOpen || got.Completions || got.Hover || got.Definition || got.References || got.Outline || got.Rename || got.AutoComplete || got.DiagInEditor || got.DiagInTree || got.DiagInStatus {
		t.Fatal("normalization re-enabled explicitly disabled feature")
	}
}

func TestLSPSeverityAtLeast(t *testing.T) {
	s := LSPSettings{MinSeverity: "warning"}
	if !s.SeverityAtLeast(1) || !s.SeverityAtLeast(2) || s.SeverityAtLeast(3) {
		t.Fatalf("warning threshold failed")
	}
}

func TestLSPRuntimeContract(t *testing.T) {
	s := DefaultLSPSettings()
	for _, tc := range []struct{ path, command, language string }{
		{"main.go", "gopls", "go"},
		{"main.py", "pyright-langserver", "python"},
		{"main.c", "clangd", "c"},
		{"main.cpp", "clangd", "cpp"},
		{"main.js", "typescript-language-server", "javascript"},
		{"main.tsx", "typescript-language-server", "typescriptreact"},
	} {
		spec, ok := s.ResolveServer(tc.path)
		if !ok || spec.Command != tc.command || spec.LanguageID != tc.language {
			t.Fatalf("resolve %s: %+v, ok=%v", tc.path, spec, ok)
		}
	}
	if _, ok := s.ResolveServer("main.unsupported"); ok {
		t.Fatal("unsupported language must not launch a server")
	}
	s.Servers = map[string]string{".go": "custom-gopls"}
	s.ServerArgs = map[string][]string{".go": {"serve"}}
	spec, ok := s.ResolveServer("main.go")
	if !ok || spec.Command != "custom-gopls" || !reflect.DeepEqual(spec.Args, []string{"serve"}) {
		t.Fatalf("command override lost: %+v", spec)
	}
	clone := CloneLSPSettings(s)
	clone.ServerArgs[".go"][0] = "changed"
	lang := clone.Languages["go"]
	lang.Enabled = false
	clone.Languages["go"] = lang
	if s.ServerArgs[".go"][0] != "serve" || !s.Languages["go"].Enabled {
		t.Fatal("LSP settings clone aliases its source")
	}
	if _, ok := clone.ResolveServer("main.go"); ok {
		t.Fatal("disabled language must not launch its override")
	}
	s.Enabled = false
	s.MaxServers, s.IdleTimeoutSeconds, s.MaxDocumentBytes = 0, 0, 0
	s = NormalizeLSP(s)
	if _, ok := s.ResolveServer("main.go"); ok || s.Enabled {
		t.Fatal("normalization must preserve disabled LSP")
	}
	if s.MaxServers <= 0 || s.IdleTimeoutSeconds <= 0 || s.MaxDocumentBytes <= 0 {
		t.Fatalf("missing bounded runtime defaults: %+v", s)
	}
	limits := NormalizeLSP(LSPSettings{MaxServers: 1, IdleTimeoutSeconds: 30, MaxDocumentBytes: 1024})
	if limits.MaxServers != 1 || limits.IdleTimeoutSeconds != 30 || limits.MaxDocumentBytes != 1024 {
		t.Fatal("explicit resource limits were replaced")
	}
}

func TestWorkspaceOverridesFromDiffNeverIncludesAccounts(t *testing.T) {
	app := AppSettings{Accounts: map[string]provider.Account{"account": {ID: "account", ProviderID: "openrouter", APIKey: "app-key", Connection: provider.DefaultTemplates()["openrouter"]}}}
	ApplyAppDefaults(&app)
	working := cloneAppSettings(app)
	working.Accounts["account"] = provider.Account{ID: "account", ProviderID: "openrouter", APIKey: "workspace-key"}
	got := WorkspaceOverridesFromDiff(app, working)
	if !got.IsEmpty() {
		t.Fatalf("credential edit became workspace override: %+v", got)
	}
	cfg := working.Accounts["account"]
	cfg.Connection.ProviderURL = "https://workspace.invalid/v1"
	working.Accounts["account"] = cfg
	got = WorkspaceOverridesFromDiff(app, working)
	if !got.IsEmpty() {
		t.Fatalf("workspace must not own templates or accounts: %+v", got)
	}
	merged := MergeAppWorkspace(app, got)
	if merged.Accounts["account"].APIKey != "app-key" || merged.Accounts["account"].Connection.ProviderURL != "https://openrouter.ai/api/v1" {
		t.Fatal("workspace changed application template or account credentials")
	}
}

func TestWorkspaceOverridesFromDiffOmitsAgentAPIKeys(t *testing.T) {
	app := AppSettings{
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

func TestAccountConnectionsCannotBeWorkspaceOverrides(t *testing.T) {
	app := AppSettings{
		Accounts: map[string]provider.Account{"work": {ID: "work", ProviderID: "openai", APIKey: "app-key", Connection: provider.Template{Name: "OpenAI", ProviderURL: "https://api.openai.com/v1", APIStandard: "openai-responses", Headers: map[string]string{"X-Trace": "app"}}}},
	}
	ApplyAppDefaults(&app)
	working := cloneAppSettings(app)
	account := working.Accounts["work"]
	account.Connection.APIStandard = "openai"
	account.Connection.Headers["X-Trace"] = "workspace"
	account.APIKey = "workspace-key"
	working.Accounts["work"] = account
	overlay := OverridesFromDiff(app, working)
	if !overlay.IsEmpty() {
		t.Fatalf("workspace persisted template/account data: %+v", overlay)
	}
	got := MergeAppWorkspace(app, overlay)
	if got.Accounts["work"].Connection.APIStandard != "openai-responses" || got.Accounts["work"].Connection.Headers["X-Trace"] != "app" || got.Accounts["work"].APIKey != "app-key" {
		t.Fatal("workspace changed an app-owned template or account")
	}
	if app.Accounts["work"].Connection.Headers["X-Trace"] != "app" {
		t.Fatal("cloned template headers alias application settings")
	}
}
