package types

import (
	"encoding/json"
	"reflect"

	"crude/provider"
)

// AccountChange is one revision-checked application credential mutation.
// Other app preferences are never included in the write.
type AccountChange struct {
	ID         string
	ProviderID string
	Before     *provider.Account
	After      *provider.Account
}

// SettingsScope selects which layer a settings editor is writing.
type SettingsScope string

const (
	SettingsScopeApplication SettingsScope = "application"
	SettingsScopeWorkspace   SettingsScope = "workspace"
)

// WorkspaceOverrides is a sparse overlay on AppSettings. This is the
// JetBrains project-settings model: the workspace stores only what it
// changes, down to the individual FIELD.
//
// Each category holds a partial JSON object carrying just the leaf values
// the workspace overrides; an absent key inherits from the application
// layer forever, including later application-level edits. Whole-category
// snapshots are what made a workspace behave like a hard copy — changing
// one editor field used to freeze every other editor field at whatever the
// application happened to hold that day, so a later app-level change to an
// untouched field silently did nothing.
type WorkspaceOverrides struct {
	Theme        json.RawMessage `json:"theme,omitempty"`
	Editor       json.RawMessage `json:"editor,omitempty"`
	LSP          json.RawMessage `json:"lsp,omitempty"`
	UI           json.RawMessage `json:"ui,omitempty"`
	Font         json.RawMessage `json:"font,omitempty"`
	Agents       json.RawMessage `json:"agents,omitempty"`
	Memory       json.RawMessage `json:"memory,omitempty"`
	Safety       json.RawMessage `json:"safety,omitempty"`
	LocalHistory json.RawMessage `json:"local_history,omitempty"`
}

// IsEmpty reports that the workspace inherits everything.
func (o WorkspaceOverrides) IsEmpty() bool {
	return len(o.Theme) == 0 && len(o.Editor) == 0 && len(o.LSP) == 0 &&
		len(o.UI) == 0 && len(o.Font) == 0 && len(o.Agents) == 0 && len(o.Memory) == 0 && len(o.Safety) == 0 && len(o.LocalHistory) == 0
}

// LSPLanguage describes a built-in language server the settings UI can
// enable, disable, or retarget.
type LSPLanguage struct {
	ID          string
	Name        string
	Exts        []string
	DefaultCmd  string
	DefaultArgs []string
}

// DefaultLSPLanguages is the catalog of servers Crude knows how to start.
func DefaultLSPLanguages() []LSPLanguage {
	return []LSPLanguage{
		{ID: "go", Name: "Go (gopls)", Exts: []string{".go"}, DefaultCmd: "gopls"},
		{ID: "rust", Name: "Rust (rust-analyzer)", Exts: []string{".rs"}, DefaultCmd: "rust-analyzer"},
		{ID: "python", Name: "Python (pyright)", Exts: []string{".py"}, DefaultCmd: "pyright-langserver", DefaultArgs: []string{"--stdio"}},
		{ID: "typescript", Name: "TypeScript / JavaScript", Exts: []string{".ts", ".js", ".tsx", ".jsx"}, DefaultCmd: "typescript-language-server", DefaultArgs: []string{"--stdio"}},
		{ID: "css", Name: "CSS", Exts: []string{".css"}, DefaultCmd: "vscode-css-language-server", DefaultArgs: []string{"--stdio"}},
		{ID: "json", Name: "JSON", Exts: []string{".json"}, DefaultCmd: "vscode-json-language-server", DefaultArgs: []string{"--stdio"}},
		{ID: "cpp", Name: "C / C++ (clangd)", Exts: []string{".c", ".h", ".cpp", ".hpp", ".cc", ".hh", ".cxx", ".hxx", ".c++", ".h++"}, DefaultCmd: "clangd"},
		{ID: "csharp", Name: "C# (OmniSharp)", Exts: []string{".cs"}, DefaultCmd: "omnisharp", DefaultArgs: []string{"--languageserver"}},
		{ID: "zig", Name: "Zig (zls)", Exts: []string{".zig"}, DefaultCmd: "zls"},
		{ID: "odin", Name: "Odin (ols)", Exts: []string{".odin"}, DefaultCmd: "ols"},
		{ID: "lua", Name: "Lua", Exts: []string{".lua"}, DefaultCmd: "lua-language-server"},
	}
}

// DefaultLSPSettings is the application-level language-intelligence baseline.
func DefaultLSPSettings() LSPSettings {
	languages := make(map[string]LSPLanguageSettings, 16)
	for _, lang := range DefaultLSPLanguages() {
		languages[lang.ID] = LSPLanguageSettings{Enabled: true, Command: lang.DefaultCmd, Args: append([]string(nil), lang.DefaultArgs...)}
	}
	return LSPSettings{
		Enabled:          true,
		AutoDetect:       true,
		StartOnOpen:      true,
		Diagnostics:      true,
		Completions:      true,
		Hover:            true,
		Definition:       true,
		References:       true,
		Outline:          true,
		Rename:           true,
		DiagInEditor:     true,
		DiagInTree:       true,
		DiagInStatus:     true,
		AutoComplete:     true,
		MinSeverity:      "hint",
		UpdateDebounceMS: 300,
		Languages:        languages,
	}
}

// ApplyAppDefaults fills zero-value IDE preferences so an empty config.json
// still produces a usable application layer.
func ApplyAppDefaults(s *AppSettings) {
	if s == nil {
		return
	}
	if s.Theme.Preset == "" {
		s.Theme.Preset = "dark"
	}
	if s.Editor.TabSize == 0 {
		s.Editor.TabSize = 4
	}
	if s.Editor.SyntaxTheme == "" {
		s.Editor.SyntaxTheme = "catppuccin"
	}
	if s.Editor.RenderWhitespace == "" {
		s.Editor.RenderWhitespace = "none"
	}
	s.Editor = NormalizeEditorSettings(s.Editor)
	if lspUnset(s.LSP) {
		s.LSP = DefaultLSPSettings()
	} else {
		s.LSP = NormalizeLSP(s.LSP)
	}
	if s.UI.SidebarWidth == 0 {
		s.UI.SidebarWidth = 30
	}
	if s.UI.ContextHelp == "" {
		s.UI.ContextHelp = "compact"
	}
	if s.Font.Size == 0 {
		s.Font.Size = 12
	}
	if s.ActiveProvider == "" {
		s.ActiveProvider = "openrouter"
	}
	if s.Providers == nil {
		s.Providers = map[string]ProviderConfig{s.ActiveProvider: {}}
	}
	if s.Accounts == nil {
		s.Accounts = make(map[string]provider.Account)
	}
	if s.Memory.AutoRecall == "" {
		s.Memory.AutoRecall = "on"
	}
	if s.Memory.AutoExtract == "" {
		s.Memory.AutoExtract = "on"
	}
	if s.Memory.Consolidation == "" {
		s.Memory.Consolidation = "on"
	}
	if s.Memory.RecallBudgetTokens == 0 {
		s.Memory.RecallBudgetTokens = DefaultMemoryRecallBudget
	}
	if s.Memory.RecallLimit == 0 {
		s.Memory.RecallLimit = DefaultMemoryRecallLimit
	}
	if s.Memory.ExtractEveryTurns == 0 {
		s.Memory.ExtractEveryTurns = DefaultMemoryExtractCadence
	}
	if s.Safety.ConfirmAt == "" {
		s.Safety.ConfirmAt = "unknown"
	}
	if s.Safety.UnknownCommandRisk == "" {
		s.Safety.UnknownCommandRisk = "unknown"
	}
	if s.Safety.ScriptExecutionRisk == "" {
		s.Safety.ScriptExecutionRisk = "unknown"
	}
	if s.Safety.NotifyDangerous == "" {
		s.Safety.NotifyDangerous = "on"
	}
	if s.LocalHistory.Enabled == "" {
		s.LocalHistory.Enabled = "on"
	}
	if s.LocalHistory.UseGitIgnore == "" {
		s.LocalHistory.UseGitIgnore = "on"
	}
	if s.LocalHistory.CaptureExternalChanges == "" {
		s.LocalHistory.CaptureExternalChanges = "on"
	}
	if s.LocalHistory.SafetySnapshotBeforeRestore == "" {
		s.LocalHistory.SafetySnapshotBeforeRestore = "on"
	}
	if s.LocalHistory.MaxFileSizeMB == 0 {
		s.LocalHistory.MaxFileSizeMB = 100
	}
	if s.LocalHistory.DebounceMS == 0 {
		s.LocalHistory.DebounceMS = 750
	}
	if s.LocalHistory.ReconcileIntervalSeconds == 0 {
		s.LocalHistory.ReconcileIntervalSeconds = 60
	}
	if s.LocalHistory.RetentionKeepHours == 0 {
		s.LocalHistory.RetentionKeepHours = 24
	}
	if s.LocalHistory.RetentionKeepDaily == 0 {
		s.LocalHistory.RetentionKeepDaily = 30
	}
	if s.LocalHistory.RetentionMaxSnapshots == 0 {
		s.LocalHistory.RetentionMaxSnapshots = 500
	}
	if s.LocalHistory.StorageBudgetMB == 0 {
		s.LocalHistory.StorageBudgetMB = 1024
	}
}

func lspUnset(s LSPSettings) bool {
	return !s.Enabled && !s.AutoDetect && !s.Diagnostics && !s.Completions &&
		!s.Hover && !s.Definition && s.MinSeverity == "" && len(s.Languages) == 0 &&
		len(s.Servers) == 0
}

func NormalizeEditorSettings(s EditorSettings) EditorSettings {
	switch s.AutoSave {
	case AutoSaveOff, AutoSaveAfterDelay, AutoSaveEditorFocusChange, AutoSaveTerminalFocusLoss:
	default:
		s.AutoSave = AutoSaveOff
	}
	if s.AutoSaveDelay <= 0 {
		s.AutoSaveDelay = 1000
	}
	s.AutoSaveDelay = min(60000, max(250, s.AutoSaveDelay))
	return s
}

// lspLegacyFeaturesUnset reports a config written before per-feature flags
// existed. Those bools persist without omitempty, so a missing field loads as
// false and would otherwise disable diagnostics even when Diagnostics is on.
func lspLegacyFeaturesUnset(s LSPSettings) bool {
	return !s.StartOnOpen && !s.Completions && !s.Hover && !s.Definition &&
		!s.References && !s.Outline && !s.Rename && !s.AutoComplete &&
		!s.DiagInEditor && !s.DiagInTree && !s.DiagInStatus
}

// NormalizeLSP fills missing language-intelligence fields without turning an
// explicit all-off configuration back on.
func NormalizeLSP(s LSPSettings) LSPSettings {
	def := DefaultLSPSettings()
	if s.MinSeverity == "" {
		s.MinSeverity = def.MinSeverity
	}
	if s.UpdateDebounceMS == 0 {
		s.UpdateDebounceMS = def.UpdateDebounceMS
	}
	if lspLegacyFeaturesUnset(s) {
		s.StartOnOpen = def.StartOnOpen
		s.Completions = def.Completions
		s.Hover = def.Hover
		s.Definition = def.Definition
		s.References = def.References
		s.Outline = def.Outline
		s.Rename = def.Rename
		s.AutoComplete = def.AutoComplete
		s.DiagInEditor = def.DiagInEditor
		s.DiagInTree = def.DiagInTree
		s.DiagInStatus = def.DiagInStatus
	}
	if s.Languages == nil {
		s.Languages = def.Languages
	} else {
		for id, lang := range def.Languages {
			if _, ok := s.Languages[id]; !ok {
				s.Languages[id] = lang
			}
		}
	}
	return s
}

// MergeAppWorkspace returns application settings with workspace overrides
// applied field by field. Any leaf the workspace does not name inherits from
// the application layer, so later app-level edits keep flowing through.
func MergeAppWorkspace(app AppSettings, ws WorkspaceOverrides) AppSettings {
	ApplyAppDefaults(&app)
	out := cloneAppSettings(app)
	applySparse(&out.Theme, ws.Theme)
	applySparse(&out.Editor, ws.Editor)
	applySparse(&out.LSP, ws.LSP)
	applySparse(&out.UI, ws.UI)
	applySparse(&out.Font, ws.Font)
	applySparse(&out.Agents, ws.Agents)
	applySparse(&out.Memory, ws.Memory)
	applySparse(&out.Safety, ws.Safety)
	applySparse(&out.LocalHistory, ws.LocalHistory)
	return out
}

// OverridesFromDiff records the individual leaf values in working that
// differ from app. Everything else inherits.
//
// It is also the migration path for legacy whole-category overrides: run a
// stored overlay through MergeAppWorkspace and back through this function
// and only the genuinely different leaves survive.
func OverridesFromDiff(app, working AppSettings) WorkspaceOverrides {
	var out WorkspaceOverrides
	out.Theme = sparseDiff(app.Theme, working.Theme)
	out.Editor = sparseDiff(app.Editor, working.Editor)
	out.LSP = sparseDiff(app.LSP, working.LSP)
	out.UI = sparseDiff(app.UI, working.UI)
	out.Font = sparseDiff(app.Font, working.Font)
	out.Agents = sparseDiff(app.Agents, working.Agents)
	out.Memory = sparseDiff(app.Memory, working.Memory)
	out.Safety = sparseDiff(app.Safety, working.Safety)
	out.LocalHistory = sparseDiff(app.LocalHistory, working.LocalHistory)
	return out
}

// sparseDiff returns a partial JSON object holding only the leaves of
// working that differ from base, or nil when the two agree. Nested objects
// recurse so one changed leaf never captures its siblings.
func sparseDiff(base, working any) json.RawMessage {
	baseMap, ok1 := jsonObject(base)
	workingMap, ok2 := jsonObject(working)
	if !ok1 || !ok2 {
		return nil
	}
	delta := diffObjects(baseMap, workingMap)
	if len(delta) == 0 {
		return nil
	}
	data, err := json.Marshal(delta)
	if err != nil {
		return nil
	}
	return data
}

// applySparse deep-merges a partial JSON object onto dst, which must already
// hold the inherited value.
func applySparse(dst any, overlay json.RawMessage) {
	if len(overlay) == 0 {
		return
	}
	var overlayMap map[string]any
	if err := json.Unmarshal(overlay, &overlayMap); err != nil || len(overlayMap) == 0 {
		return
	}
	baseMap, ok := jsonObject(dst)
	if !ok {
		return
	}
	mergeObjects(baseMap, overlayMap)
	data, err := json.Marshal(baseMap)
	if err != nil {
		return
	}
	// Merging onto the marshalled form means absent keys keep the inherited
	// value; unmarshalling the whole object back is therefore lossless.
	_ = json.Unmarshal(data, dst)
}

func jsonObject(v any) (map[string]any, bool) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, false
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, false
	}
	if out == nil {
		out = map[string]any{}
	}
	return out, true
}

func diffObjects(base, working map[string]any) map[string]any {
	out := map[string]any{}
	for key, wv := range working {
		bv, present := base[key]
		if !present {
			out[key] = wv
			continue
		}
		bm, bIsObj := bv.(map[string]any)
		wm, wIsObj := wv.(map[string]any)
		if bIsObj && wIsObj {
			if sub := diffObjects(bm, wm); len(sub) > 0 {
				out[key] = sub
			}
			continue
		}
		if !reflect.DeepEqual(bv, wv) {
			out[key] = wv
		}
	}
	// A key the base carries but working dropped means working holds the
	// zero value and `omitempty` elided it. Record that zero explicitly so
	// "turned this off" is a real override rather than silent inheritance.
	for key, bv := range base {
		if _, present := working[key]; present {
			continue
		}
		if zero, ok := jsonZero(bv); ok {
			out[key] = zero
		}
	}
	return out
}

// jsonZero is the elided-by-omitempty counterpart of a JSON value. Objects
// have no sparse representation for "cleared", so they report false.
func jsonZero(v any) (any, bool) {
	switch v.(type) {
	case bool:
		return false, true
	case float64:
		return float64(0), true
	case string:
		return "", true
	case []any:
		return []any{}, true
	}
	return nil, false
}

func mergeObjects(dst, overlay map[string]any) {
	for key, ov := range overlay {
		dv, present := dst[key]
		dm, dIsObj := dv.(map[string]any)
		om, oIsObj := ov.(map[string]any)
		if present && dIsObj && oIsObj {
			mergeObjects(dm, om)
			continue
		}
		dst[key] = ov
	}
}

// WorkspaceOverridesFromDiff is OverridesFromDiff for the workspace layer:
// API keys never leave the application config, so credential-only edits do
// not create a workspace provider overlay.
func WorkspaceOverridesFromDiff(app, working AppSettings) WorkspaceOverrides {
	appSans := cloneAppSettings(app)
	workingSans := cloneAppSettings(working)
	// Accounts are app-global: never compare or persist their credentials in workspace overlays.
	appSans.Accounts = nil
	workingSans.Accounts = nil
	stripAgentAPIKeys(&appSans.Agents)
	stripAgentAPIKeys(&workingSans.Agents)
	return OverridesFromDiff(appSans, workingSans)
}

func stripAgentAPIKeys(agents *AgentsSettings) {
	if agents == nil {
		return
	}
	for id, cat := range agents.Categories {
		cat.APIKey = ""
		agents.Categories[id] = cat
	}
	if len(agents.Profiles) == 0 {
		return
	}
	profiles := make([]ProfileSettings, len(agents.Profiles))
	for i, profile := range agents.Profiles {
		profiles[i] = profile
		if profile.Categories == nil {
			continue
		}
		cats := make(map[string]CategorySettings, len(profile.Categories))
		for id, cat := range profile.Categories {
			cat.APIKey = ""
			cats[id] = cat
		}
		profiles[i].Categories = cats
	}
	agents.Profiles = profiles
}

// CategoryOverridden reports whether a settings page is stored as a workspace
// override rather than inherited from the application layer.
func (o WorkspaceOverrides) CategoryOverridden(name string) bool {
	switch name {
	case "theme", "appearance":
		return len(o.Theme) > 0
	case "editor":
		return len(o.Editor) > 0
	case "lsp", "language servers":
		return len(o.LSP) > 0
	case "ui", "interface":
		return len(o.UI) > 0
	case "font":
		return len(o.Font) > 0
	case "agents":
		return len(o.Agents) > 0
	case "memory":
		return len(o.Memory) > 0
	case "safety":
		return len(o.Safety) > 0
	}
	return false
}

// ClearCategory drops one page of workspace overrides so it inherits again.
func (o *WorkspaceOverrides) ClearCategory(name string) {
	if o == nil {
		return
	}
	switch name {
	case "theme", "appearance":
		o.Theme = nil
	case "editor":
		o.Editor = nil
	case "lsp", "language servers":
		o.LSP = nil
	case "ui", "interface":
		o.UI = nil
	case "font":
		o.Font = nil
	case "agents":
		o.Agents = nil
	case "memory":
		o.Memory = nil
	case "safety":
		o.Safety = nil
	case "local_history", "local history", "history":
		o.LocalHistory = nil
	}
}

func cloneAppSettings(s AppSettings) AppSettings {
	out := s
	out.Providers = cloneProviders(s.Providers)
	out.Accounts = make(map[string]provider.Account, len(s.Accounts))
	for id, account := range s.Accounts {
		if account.OAuth != nil {
			copy := *account.OAuth
			account.OAuth = &copy
		}
		out.Accounts[id] = account
	}
	out.Agents = cloneAgents(s.Agents)
	out.Memory.MemoryCategory.ContextCompactionPercent = cloneIntSetting(s.Memory.MemoryCategory.ContextCompactionPercent)
	out.Safety.CommandRules = append([]CommandSeverityRule(nil), s.Safety.CommandRules...)
	out.LocalHistory.ExtraIgnorePatterns = append([]string(nil), s.LocalHistory.ExtraIgnorePatterns...)
	out.LSP.Languages = cloneLanguages(s.LSP.Languages)
	out.LSP.Servers = cloneStringMap(s.LSP.Servers)
	out.LSP.ServerArgs = cloneStringSliceMap(s.LSP.ServerArgs)
	return out
}

func cloneProviders(src map[string]ProviderConfig) map[string]ProviderConfig {
	if src == nil {
		return nil
	}
	out := make(map[string]ProviderConfig, len(src))
	for k, v := range src {
		out[k] = provider.CloneTemplate(v)
	}
	return out
}

func cloneAgents(src AgentsSettings) AgentsSettings {
	out := src
	if src.Categories != nil {
		out.Categories = make(map[string]CategorySettings, len(src.Categories))
		for k, v := range src.Categories {
			v.ContextCompactionPercent = cloneIntSetting(v.ContextCompactionPercent)
			out.Categories[k] = v
		}
	}
	if src.Profiles != nil {
		out.Profiles = make([]ProfileSettings, len(src.Profiles))
		for i, profile := range src.Profiles {
			out.Profiles[i] = profile
			if profile.Categories != nil {
				out.Profiles[i].Categories = make(map[string]CategorySettings, len(profile.Categories))
				for key, value := range profile.Categories {
					value.ContextCompactionPercent = cloneIntSetting(value.ContextCompactionPercent)
					out.Profiles[i].Categories[key] = value
				}
			}
		}
	}
	return out
}

func cloneIntSetting(src *int) *int {
	if src == nil {
		return nil
	}
	value := *src
	return &value
}

func cloneLanguages(src map[string]LSPLanguageSettings) map[string]LSPLanguageSettings {
	if src == nil {
		return nil
	}
	out := make(map[string]LSPLanguageSettings, len(src))
	for k, v := range src {
		v.Args = append([]string(nil), v.Args...)
		out[k] = v
	}
	return out
}

func cloneStringMap(src map[string]string) map[string]string {
	if src == nil {
		return nil
	}
	out := make(map[string]string, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

func cloneStringSliceMap(src map[string][]string) map[string][]string {
	if src == nil {
		return nil
	}
	out := make(map[string][]string, len(src))
	for k, v := range src {
		out[k] = append([]string(nil), v...)
	}
	return out
}

// LanguageEnabled reports whether the language server for ext should start.
func (s LSPSettings) LanguageEnabled(ext string) bool {
	if !s.Enabled {
		return false
	}
	id := languageIDForExt(ext)
	if id == "" {
		return true
	}
	if lang, ok := s.Languages[id]; ok {
		return lang.Enabled
	}
	return true
}

// ServerOverride returns a command override for ext when the user retargeted
// that language. ok is false when the built-in default should be used.
func (s LSPSettings) ServerOverride(ext string) (cmd string, args []string, ok bool) {
	if cmd, exists := s.Servers[ext]; exists && cmd != "" {
		return cmd, append([]string(nil), s.ServerArgs[ext]...), true
	}
	id := languageIDForExt(ext)
	if id == "" {
		return "", nil, false
	}
	lang, exists := s.Languages[id]
	if !exists || lang.Command == "" {
		return "", nil, false
	}
	def := languageByID(id)
	if def.DefaultCmd == lang.Command && equalStrings(def.DefaultArgs, lang.Args) {
		return "", nil, false
	}
	return lang.Command, append([]string(nil), lang.Args...), true
}

func languageIDForExt(ext string) string {
	for _, lang := range DefaultLSPLanguages() {
		for _, candidate := range lang.Exts {
			if candidate == ext {
				return lang.ID
			}
		}
	}
	return ""
}

func languageByID(id string) LSPLanguage {
	for _, lang := range DefaultLSPLanguages() {
		if lang.ID == id {
			return lang
		}
	}
	return LSPLanguage{}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// SeverityAtLeast reports whether severity (1=error … 4=hint) should be shown
// given the configured minimum.
func (s LSPSettings) SeverityAtLeast(severity int) bool {
	min := 4
	switch s.MinSeverity {
	case "error":
		min = 1
	case "warning":
		min = 2
	case "info":
		min = 3
	case "hint", "":
		min = 4
	}
	if severity < 1 {
		severity = 1
	}
	return severity <= min
}
