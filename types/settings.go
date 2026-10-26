package types

import (
	"encoding/json"
	"maps"
	"reflect"
	"slices"

	"crude/network"
	"crude/provider"
	captypes "crude/types/capability"
)

const (
	// Explicit verbatim-retention bounds; unset retention is adaptive.
	MinContextRetentionTokens = 1_000
	MaxContextRetentionTokens = 200_000
)

// AccountChange is one identity-scoped application account mutation.
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
// the workspace explicitly set; an absent key inherits from the application
// layer forever, including later application-level edits. Whole-category
// snapshots are what made a workspace behave like a hard copy: changing
// one editor field used to freeze every other editor field at whatever the
// application happened to hold that day, so a later app-level change to an
// untouched field silently did nothing.
type WorkspaceOverrides struct {
	Theme        json.RawMessage `json:"theme,omitempty"`
	Editor       json.RawMessage `json:"editor,omitempty"`
	LSP          json.RawMessage `json:"lsp,omitempty"`
	UI           json.RawMessage `json:"ui,omitempty"`
	Agents       json.RawMessage `json:"agents,omitempty"`
	Memory       json.RawMessage `json:"memory,omitempty"`
	Safety       json.RawMessage `json:"safety,omitempty"`
	LocalHistory json.RawMessage `json:"local_history,omitempty"`
	Capabilities json.RawMessage `json:"capabilities,omitempty"`
	Terminal     json.RawMessage `json:"terminal,omitempty"`
}

// IsEmpty reports that the workspace inherits everything.
func (o WorkspaceOverrides) IsEmpty() bool {
	return len(o.Theme) == 0 && len(o.Editor) == 0 && len(o.LSP) == 0 &&
		len(o.UI) == 0 && len(o.Agents) == 0 && len(o.Memory) == 0 && len(o.Safety) == 0 && len(o.LocalHistory) == 0 && len(o.Capabilities) == 0 && len(o.Terminal) == 0
}

// DefaultLSPSettings is the application-level language-intelligence baseline.
func DefaultLSPSettings() LSPSettings {
	languages := make(map[string]LSPLanguageSettings, 16)
	for _, lang := range DefaultLSPLanguages() {
		languages[lang.ID] = LSPLanguageSettings{Enabled: true, Command: lang.DefaultCmd, Args: append([]string(nil), lang.DefaultArgs...)}
	}
	return LSPSettings{
		Enabled:            true,
		AutoDetect:         true,
		StartOnOpen:        true,
		Diagnostics:        true,
		Completions:        true,
		Hover:              true,
		Definition:         true,
		References:         true,
		Outline:            true,
		Rename:             true,
		DiagInEditor:       true,
		DiagInTree:         true,
		DiagInStatus:       true,
		AutoComplete:       true,
		MinSeverity:        "hint",
		UpdateDebounceMS:   300,
		MaxServers:         2,
		ServerThreads:      2,
		ServerMemoryMB:     1024,
		IdleTimeoutSeconds: 300,
		MaxDocumentBytes:   2 * 1024 * 1024,
		Languages:          languages,
	}
}

// ApplyAppDefaults fills zero-value IDE preferences so an empty config.json
// still produces a usable application layer.
func ApplyAppDefaults(s *AppSettings) {
	if s == nil {
		return
	}
	s.Capabilities = captypes.WithDefaults(s.Capabilities)
	s.Network = network.Normalize(s.Network)
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
	s.Terminal = NormalizeTerminalSettings(s.Terminal)
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
	s.UI.AttachmentOpen = s.UI.AttachmentOpenOrDefault()
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
		!s.Hover && !s.Definition && s.MinSeverity == "" && len(s.Languages) == 0
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

// NormalizeLSP fills missing language-intelligence fields without turning an
// explicit all-off configuration back on.
func NormalizeLSP(s LSPSettings) LSPSettings {
	s = CloneLSPSettings(s)
	def := DefaultLSPSettings()
	if s.ServerThreads == 0 {
		s.ServerThreads = def.ServerThreads
	}
	if s.ServerMemoryMB == 0 {
		s.ServerMemoryMB = def.ServerMemoryMB
	}
	if s.MinSeverity == "" {
		s.MinSeverity = def.MinSeverity
	}
	if s.UpdateDebounceMS == 0 {
		s.UpdateDebounceMS = def.UpdateDebounceMS
	}
	if s.MaxServers == 0 {
		s.MaxServers = def.MaxServers
	}
	if s.IdleTimeoutSeconds == 0 {
		s.IdleTimeoutSeconds = def.IdleTimeoutSeconds
	}
	if s.MaxDocumentBytes == 0 {
		s.MaxDocumentBytes = def.MaxDocumentBytes
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
	app = cloneAppSettings(app)
	ApplyAppDefaults(&app)
	out := cloneAppSettings(app)
	applySparse(&out.Theme, ws.Theme)
	applySparse(&out.Editor, ws.Editor)
	applySparse(&out.LSP, ws.LSP)
	applySparse(&out.UI, ws.UI)
	applySparse(&out.Agents, ws.Agents)
	applySparse(&out.Memory, ws.Memory)
	applySparse(&out.Safety, ws.Safety)
	applySparse(&out.LocalHistory, ws.LocalHistory)
	applySparse(&out.Terminal, ws.Terminal)
	out.Terminal = NormalizeTerminalSettings(out.Terminal)
	out.Capabilities = mergeCapabilities(out.Capabilities, ws.Capabilities)
	return out
}

// OverridesFromDiff records the individual leaf values in working that
// differ from app. Everything else inherits.
func OverridesFromDiff(app, working AppSettings) WorkspaceOverrides {
	var out WorkspaceOverrides
	out.Theme = sparseDiff(app.Theme, working.Theme)
	out.Editor = sparseDiff(app.Editor, working.Editor)
	out.LSP = sparseDiff(app.LSP, working.LSP)
	out.UI = sparseDiff(app.UI, working.UI)
	out.Agents = sparseDiff(app.Agents, working.Agents)
	out.Memory = sparseDiff(app.Memory, working.Memory)
	out.Safety = sparseDiff(app.Safety, working.Safety)
	out.LocalHistory = sparseDiff(app.LocalHistory, working.LocalHistory)
	out.Terminal = sparseDiff(app.Terminal, working.Terminal)
	out.Capabilities = capabilityDiff(app.Capabilities, working.Capabilities)
	return out
}

func capabilityDiff(base, working captypes.Settings) json.RawMessage {
	delta := make(map[string]any)
	if raw := sparseDiff(base, working); len(raw) > 0 {
		_ = json.Unmarshal(raw, &delta)
	}
	baseMap, _ := jsonObject(base)
	workingMap, _ := jsonObject(working)
	for _, collection := range []string{"skills", "connections", "sources"} {
		before, _ := baseMap[collection].(map[string]any)
		after, _ := workingMap[collection].(map[string]any)
		changes, _ := delta[collection].(map[string]any)
		for id := range before {
			if _, exists := after[id]; !exists {
				if changes == nil {
					changes = make(map[string]any)
				}
				changes[id] = nil
			}
		}
		if len(changes) > 0 {
			delta[collection] = changes
		}
	}
	if len(delta) == 0 {
		return nil
	}
	encoded, _ := json.Marshal(delta)
	return encoded
}

func mergeCapabilities(base captypes.Settings, overlay json.RawMessage) captypes.Settings {
	if len(overlay) == 0 {
		return base
	}
	var changes map[string]any
	if json.Unmarshal(overlay, &changes) != nil {
		return base
	}
	values, _ := jsonObject(base)
	mergeCapabilityObjects(values, changes)
	encoded, err := json.Marshal(values)
	if err != nil {
		return base
	}
	var merged captypes.Settings
	if json.Unmarshal(encoded, &merged) != nil {
		return base
	}
	return captypes.Normalize(merged)
}

func mergeCapabilityObjects(values, changes map[string]any) {
	for key, value := range changes {
		if value == nil {
			delete(values, key)
			continue
		}
		before, beforeObject := values[key].(map[string]any)
		after, afterObject := value.(map[string]any)
		if beforeObject && afterObject {
			mergeCapabilityObjects(before, after)
		} else {
			values[key] = value
		}
	}
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
	if len(overlay) != 0 {
		_ = DecodeStoredSettings(overlay, dst)
	}
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
	case "agents":
		return len(o.Agents) > 0
	case "memory":
		return len(o.Memory) > 0
	case "capabilities", "skills", "mcp", "sources", "acquisition":
		return len(o.Capabilities) > 0
	case "safety":
		return len(o.Safety) > 0
	case "local_history", "local history", "history":
		return len(o.LocalHistory) > 0
	case "terminal":
		return len(o.Terminal) > 0
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
	case "agents":
		o.Agents = nil
	case "memory":
		o.Memory = nil
	case "capabilities", "skills", "mcp", "sources", "acquisition":
		o.Capabilities = nil
	case "safety":
		o.Safety = nil
	case "local_history", "local history", "history":
		o.LocalHistory = nil
	case "terminal":
		o.Terminal = nil
	}
}

func cloneAppSettings(s AppSettings) AppSettings {
	out := s
	out.Capabilities = captypes.Clone(s.Capabilities)
	out.Accounts = provider.CloneAccounts(s.Accounts)
	out.Agents = cloneAgents(s.Agents)
	out.Memory.MemoryCategory = CloneCategorySettings(s.Memory.MemoryCategory)
	out.Theme.PadTop = cloneIntSetting(s.Theme.PadTop)
	out.Theme.PadBottom = cloneIntSetting(s.Theme.PadBottom)
	out.Theme.PadLeft = cloneIntSetting(s.Theme.PadLeft)
	out.Theme.PadRight = cloneIntSetting(s.Theme.PadRight)
	out.Safety.CommandRules = append([]CommandSeverityRule(nil), s.Safety.CommandRules...)
	out.LocalHistory.ExtraIgnorePatterns = append([]string(nil), s.LocalHistory.ExtraIgnorePatterns...)
	out.LSP = CloneLSPSettings(s.LSP)
	out.Terminal = CloneTerminalSettings(s.Terminal)
	out.TerminalShells = slices.Clone(s.TerminalShells)
	return out
}

// CloneAppSettings detaches a read projection; no mutable child can modify its parent.
func CloneAppSettings(s AppSettings) AppSettings { return cloneAppSettings(s) }

func cloneAgents(src AgentsSettings) AgentsSettings {
	out := src
	out.AgentProfiles = CloneAgentProfiles(src.AgentProfiles)
	if src.Categories != nil {
		out.Categories = make(map[string]CategorySettings, len(src.Categories))
		for k, v := range src.Categories {
			out.Categories[k] = CloneCategorySettings(v)
		}
	}
	return out
}

func CloneCategorySettings(v CategorySettings) CategorySettings {
	v.ContextCompactionPercent = cloneIntSetting(v.ContextCompactionPercent)
	v.ContextRetentionTokens = cloneIntSetting(v.ContextRetentionTokens)
	v.AllowedTools = slices.Clone(v.AllowedTools)
	v.DeniedTools = slices.Clone(v.DeniedTools)
	v.SkillModes = maps.Clone(v.SkillModes)
	v.ModelModalities = slices.Clone(v.ModelModalities)
	if v.Enabled != nil {
		value := *v.Enabled
		v.Enabled = &value
	}
	if v.ModelTemperature != nil {
		value := *v.ModelTemperature
		v.ModelTemperature = &value
	}
	if v.ModelTopP != nil {
		value := *v.ModelTopP
		v.ModelTopP = &value
	}
	return v
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
		v.Extensions = append([]string(nil), v.Extensions...)
		v.RootMarkers = append([]string(nil), v.RootMarkers...)
		v.Environment = cloneStringMap(v.Environment)
		v.Settings = cloneJSONObject(v.Settings)
		v.InitializationOptions = cloneJSONObject(v.InitializationOptions)
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

// LanguageEnabled reports whether the language server for ext should start.
func (s LSPSettings) LanguageEnabled(ext string) bool {
	_, ok := s.ResolveServer("document" + ext)
	return ok
}

// ServerOverride returns an explicit command/argument definition without defaults.
func (s LSPSettings) ServerOverride(ext string) (cmd string, args []string, ok bool) {
	id, _ := s.languageForExtension(ext)
	if lang, exists := s.Languages[id]; exists && !lang.Enabled {
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
