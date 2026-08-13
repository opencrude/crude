package types

import "reflect"

// SettingsScope selects which layer a settings editor is writing.
type SettingsScope string

const (
	SettingsScopeApplication SettingsScope = "application"
	SettingsScopeWorkspace   SettingsScope = "workspace"
)

// WorkspaceOverrides is a sparse overlay on AppSettings. A nil pointer means
// inherit from the application layer. This is the JetBrains project-settings
// model: the workspace stores only what it changes.
type WorkspaceOverrides struct {
	Theme          *ThemeSettings            `json:"theme,omitempty"`
	Editor         *EditorSettings           `json:"editor,omitempty"`
	LSP            *LSPSettings              `json:"lsp,omitempty"`
	UI             *UISettings               `json:"ui,omitempty"`
	Font           *FontSettings             `json:"font,omitempty"`
	ActiveProvider *string                   `json:"active_provider,omitempty"`
	Providers      map[string]ProviderConfig `json:"providers,omitempty"`
	Agents         *AgentsSettings           `json:"agents,omitempty"`
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
	if lspUnset(s.LSP) {
		s.LSP = DefaultLSPSettings()
	} else {
		s.LSP = NormalizeLSP(s.LSP)
	}
	if s.UI.SidebarWidth == 0 {
		s.UI.SidebarWidth = 30
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
}

func lspUnset(s LSPSettings) bool {
	return !s.Enabled && !s.AutoDetect && !s.Diagnostics && !s.Completions &&
		!s.Hover && !s.Definition && s.MinSeverity == "" && len(s.Languages) == 0 &&
		len(s.Servers) == 0
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
// applied. Nil override fields inherit from app.
func MergeAppWorkspace(app AppSettings, ws WorkspaceOverrides) AppSettings {
	ApplyAppDefaults(&app)
	out := cloneAppSettings(app)
	if ws.Theme != nil {
		out.Theme = *ws.Theme
	}
	if ws.Editor != nil {
		out.Editor = *ws.Editor
	}
	if ws.LSP != nil {
		out.LSP = NormalizeLSP(*ws.LSP)
	}
	if ws.UI != nil {
		out.UI = *ws.UI
	}
	if ws.Font != nil {
		out.Font = *ws.Font
	}
	if ws.ActiveProvider != nil && *ws.ActiveProvider != "" {
		out.ActiveProvider = *ws.ActiveProvider
	}
	if len(ws.Providers) > 0 {
		if out.Providers == nil {
			out.Providers = make(map[string]ProviderConfig, len(ws.Providers))
		}
		for id, overlay := range ws.Providers {
			out.Providers[id] = mergeProvider(out.Providers[id], overlay)
		}
	}
	if ws.Agents != nil {
		out.Agents = mergeAgents(out.Agents, *ws.Agents)
		out.Agents.Enabled = ws.Agents.Enabled
	}
	return out
}

// OverridesFromDiff records every category in working that differs from app
// as a workspace override. Identical categories inherit.
func OverridesFromDiff(app, working AppSettings) WorkspaceOverrides {
	var out WorkspaceOverrides
	if !reflect.DeepEqual(app.Theme, working.Theme) {
		theme := working.Theme
		out.Theme = &theme
	}
	if !reflect.DeepEqual(app.Editor, working.Editor) {
		editor := working.Editor
		out.Editor = &editor
	}
	if !reflect.DeepEqual(app.LSP, working.LSP) {
		lsp := working.LSP
		out.LSP = &lsp
	}
	if !reflect.DeepEqual(app.UI, working.UI) {
		ui := working.UI
		out.UI = &ui
	}
	if !reflect.DeepEqual(app.Font, working.Font) {
		font := working.Font
		out.Font = &font
	}
	if app.ActiveProvider != working.ActiveProvider {
		p := working.ActiveProvider
		out.ActiveProvider = &p
	}
	if !reflect.DeepEqual(app.Providers, working.Providers) {
		out.Providers = cloneProviders(working.Providers)
	}
	if !reflect.DeepEqual(app.Agents, working.Agents) {
		agents := cloneAgents(working.Agents)
		out.Agents = &agents
	}
	return out
}

// WorkspaceOverridesFromDiff is OverridesFromDiff for the workspace layer:
// API keys never leave the application config, so credential-only edits do
// not create a workspace provider overlay.
func WorkspaceOverridesFromDiff(app, working AppSettings) WorkspaceOverrides {
	appSans := cloneAppSettings(app)
	workingSans := cloneAppSettings(working)
	for id, cfg := range appSans.Providers {
		cfg.APIKey = ""
		appSans.Providers[id] = cfg
	}
	for id, cfg := range workingSans.Providers {
		cfg.APIKey = ""
		workingSans.Providers[id] = cfg
	}
	return OverridesFromDiff(appSans, workingSans)
}

// CategoryOverridden reports whether a settings page is stored as a workspace
// override rather than inherited from the application layer.
func (o WorkspaceOverrides) CategoryOverridden(name string) bool {
	switch name {
	case "theme", "appearance":
		return o.Theme != nil
	case "editor":
		return o.Editor != nil
	case "lsp", "language servers":
		return o.LSP != nil
	case "ui", "interface":
		return o.UI != nil
	case "font":
		return o.Font != nil
	case "providers":
		return o.ActiveProvider != nil || len(o.Providers) > 0
	case "agents":
		return o.Agents != nil
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
	case "providers":
		o.ActiveProvider = nil
		o.Providers = nil
	case "agents":
		o.Agents = nil
	}
}

func mergeProvider(base, overlay ProviderConfig) ProviderConfig {
	out := base
	if overlay.APIKey != "" {
		out.APIKey = overlay.APIKey
	}
	if overlay.ProviderURL != "" {
		out.ProviderURL = overlay.ProviderURL
	}
	if overlay.APIStandard != "" {
		out.APIStandard = overlay.APIStandard
	}
	if overlay.Model != "" {
		out.Model = overlay.Model
	}
	if len(overlay.ModelStandards) > 0 {
		if out.ModelStandards == nil {
			out.ModelStandards = make(map[string]string, len(overlay.ModelStandards))
		}
		for model, standard := range overlay.ModelStandards {
			out.ModelStandards[model] = standard
		}
	}
	if overlay.ThinkingEffort != "" {
		out.ThinkingEffort = overlay.ThinkingEffort
	}
	return out
}

func mergeAgents(base, overlay AgentsSettings) AgentsSettings {
	out := cloneAgents(base)
	if overlay.Enabled {
		out.Enabled = true
	}
	if overlay.ActiveProfile != "" {
		out.ActiveProfile = overlay.ActiveProfile
	}
	if overlay.MaxConcurrent != 0 {
		out.MaxConcurrent = overlay.MaxConcurrent
	}
	if len(overlay.Categories) > 0 {
		if out.Categories == nil {
			out.Categories = make(map[string]CategorySettings, len(overlay.Categories))
		}
		for key, cat := range overlay.Categories {
			out.Categories[key] = mergeCategory(out.Categories[key], cat)
		}
	}
	if len(overlay.Profiles) > 0 {
		out.Profiles = append([]ProfileSettings(nil), overlay.Profiles...)
	}
	return out
}

func mergeCategory(base, overlay CategorySettings) CategorySettings {
	out := base
	if overlay.Provider != "" {
		out.Provider = overlay.Provider
	}
	if overlay.ProviderURL != "" {
		out.ProviderURL = overlay.ProviderURL
	}
	if overlay.APIKey != "" {
		out.APIKey = overlay.APIKey
	}
	if overlay.APIStandard != "" {
		out.APIStandard = overlay.APIStandard
	}
	if overlay.Model != "" {
		out.Model = overlay.Model
	}
	if overlay.ThinkingEffort != "" {
		out.ThinkingEffort = overlay.ThinkingEffort
	}
	if overlay.Policy != "" {
		out.Policy = overlay.Policy
	}
	if overlay.MaxSteps != 0 {
		out.MaxSteps = overlay.MaxSteps
	}
	if overlay.Description != "" {
		out.Description = overlay.Description
	}
	return out
}

func cloneAppSettings(s AppSettings) AppSettings {
	out := s
	out.Providers = cloneProviders(s.Providers)
	out.Agents = cloneAgents(s.Agents)
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
		v.ModelStandards = cloneStringMap(v.ModelStandards)
		out[k] = v
	}
	return out
}

func cloneAgents(src AgentsSettings) AgentsSettings {
	out := src
	if src.Categories != nil {
		out.Categories = make(map[string]CategorySettings, len(src.Categories))
		for k, v := range src.Categories {
			out.Categories[k] = v
		}
	}
	if src.Profiles != nil {
		out.Profiles = append([]ProfileSettings(nil), src.Profiles...)
	}
	return out
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
