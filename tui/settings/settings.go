package settings

import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"image/color"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"

	tea "charm.land/bubbletea/v2"

	"crude/provider"
	"crude/tui/rendering"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
	"crude/types/capability"
)

// SaveMsg is returned as a tea.Msg on save.
type SaveMsg struct {
	Target      *Model
	Scope       types.SettingsScope
	AppSettings types.AppSettings
	Overrides   types.WorkspaceOverrides
}

// CancelMsg is returned as a tea.Msg on cancel.
type CancelMsg struct{}

// PromptKind identifies a settings decision handled by the floating overlay
// host. The settings window and its editor remain mounted underneath.
type PromptKind uint8

const (
	PromptRemoveAccount PromptKind = iota + 1
	PromptDiscardEditor
	PromptDiscardSettings
	PromptRemoveCapability
)

type PromptMsg struct {
	Target           *Model
	Kind             PromptKind
	ID               string
	Label            string
	RelatedCount     int
	beforeAccount    *provider.Account
	accountEditor    *accountEdit
	accountDraft     *provider.Account
	preferences      []byte
	agentEditor      *ui.FormModel
	agentDraft       []byte
	capabilityEditor *capabilityCatalog
	capabilityForm   *ui.FormModel
	capabilityDraft  []byte
	CapabilityAction *capability.Action
}

type AccountChangeMsg struct {
	Target *Model
	Change types.AccountChange
}

type AccountChangeResultMsg struct {
	Target              *Model
	Change              types.AccountChange
	Settings            types.AppSettings
	Err                 error
	WorkspaceGeneration uint64
}

// ThemePreviewMsg applies a theme preset immediately while Settings stays open.
type ThemePreviewMsg struct {
	Preset string
}

// FramePreviewMsg applies edited shell frame paddings immediately while
// Settings stays open, so the inset preview follows each keystroke. Nil
// fields are left unchanged.
type FramePreviewMsg struct {
	Top, Right, Bottom, Left *int
}

// FetchModelsMsg asks the workspace layer to load models for a provider.
type FetchModelsMsg struct {
	ProviderID string
	AccountID  string
	Generation uint64
	BaseURL    string
	APIKey     string
	StandardID string
	Account    provider.Account // detached draft; never persisted by model discovery
}

// ModelsMsg and ModelsErrorMsg deliver an asynchronous model-list result.
type ModelsMsg struct {
	ProviderID string
	AccountID  string
	Generation uint64
	Models     []types.ModelEntry
}

type ModelsErrorMsg struct {
	ProviderID string
	AccountID  string
	Generation uint64
	Error      string
}

// ResetSectionMsg clears the workspace override for one settings page.
type ResetSectionMsg struct{ Key string }

// ScopeMsg switches the settings dialog between application and workspace.
type ScopeMsg struct{ Scope types.SettingsScope }

// ChatGPTSignInResultMsg delivers the outcome of a "Sign in with ChatGPT" run.
// On success Creds holds the tokens to persist; on failure Err is set (and may
// wrap a ChatGPTSignInError whose Fatal flag means the stored session is dead).
type ChatGPTSignInResultMsg struct {
	AccountID  string
	Generation uint64
	Creds      *provider.OAuthCredentials
	Err        error
}

// ChatGPTSignOutMsg clears the stored ChatGPT tokens.
type ChatGPTSignOutMsg struct {
	AccountID  string
	Generation uint64
}

// Category is a presentation-neutral settings section backed by the shared
// form model. It deliberately has no legacy tui/form state.
type Category struct {
	Name        string
	Group       string
	Description string
	Form        *ui.FormModel
}

type categorySnapshot struct{ values map[string]ui.FieldValue }

type modelSnapshot struct {
	categories []categorySnapshot
	selected   int
	pane       int
	accountID  string
	accounts   map[string]provider.Account
	languages  map[string]types.LSPLanguageSettings
}

// Model is the workspace settings screen state.
type Model struct {
	// terminalShells are detected on the core's machine; Auto resolves to the first.
	terminalShells      []types.TerminalShell
	th                  *theme.Theme
	categories          []Category
	selected            int
	pane                int // 0: category rail, 1: form
	snapshot            *modelSnapshot
	presets             []types.ProviderPreset
	accounts            map[string]provider.Account
	accountID           string // account being browsed, never an active connection
	saving              bool
	saveError           string
	saveDisabled        map[*ui.FieldState]bool
	authGeneration      map[string]uint64
	authErrors          map[string]string
	authWaitingID       string
	fetchGeneration     map[string]uint64
	accountEditor       *accountEdit
	accountBusy         bool
	accountError        string
	nextCategory        int
	nextScope           types.SettingsScope
	preferencesBase     []byte
	models              map[string][]types.ModelEntry
	searchEd            *ui.TextEdit
	searchActive        bool
	accountQuery        string
	scope               types.SettingsScope
	appDefaults         types.AppSettings
	applicationRevision string
	workspaceRevision   string
	// agents holds the working AgentsSettings so the main agent form's
	// Multi-Agent toggle reads and writes the real workspace flag.
	agents                   *types.AgentsSettings
	memoryAgent              types.CategorySettings
	autoReviewer             types.AutoPermissionSettings
	capabilities             capability.Settings
	capabilitySnapshot       capability.Snapshot
	capabilityDiscovery      map[capability.Kind][]capability.Entry
	capabilityDiscoveryQuery map[capability.Kind]capability.Action
	capabilitySequence       uint64
	capabilityRequests       map[uint64]capability.Action
	capabilityPending        *capability.Action
	capabilityResult         *CapabilityResultMsg
	capabilityRevision       uint64
	capabilityDraft          *ui.FormModel
	// lspLanguages is the working server inventory edited by the Language
	// Servers page; lspLanguagesBase is the saved baseline for dirty state.
	lspLanguages     map[string]types.LSPLanguageSettings
	lspLanguagesBase map[string]types.LSPLanguageSettings
	// onChange lets the retained surface reconcile Save/rail state after any
	// field edit without the form routing an action through it.
	onChange func()
}

type accountEdit struct {
	original *provider.Account
	draft    provider.Account
	seedID   string
	isNew    bool
}

const (
	fieldProvider             = "provider"
	fieldModel                = "model"
	fieldAPIKey               = "api-key"
	fieldAccount              = "account"
	fieldAccountLabel         = "account-label"
	fieldConnection           = "connection"
	fieldAutoPermAccount      = "auto-permission-account"
	fieldBaseURL              = "base-url"
	fieldAPIStandard          = "api-standard"
	fieldThinkingEffort       = "thinking-effort"
	fieldMultiAgent           = "multi-agent"
	fieldContextCompaction    = "context-compaction-percent"
	fieldContextRetention     = "context-retention-tokens"
	fieldSafetyConfirmAt      = "safety-confirm-at"
	fieldSafetyUnknown        = "safety-unknown-risk"
	fieldSafetyScript         = "safety-script-risk"
	fieldSafetyRules          = "safety-command-rules"
	fieldSafetyNotify         = "safety-notify-dangerous"
	fieldAutoPermission       = "auto-permission-enabled"
	fieldAutoPermModel        = "auto-permission-model"
	fieldAutoPermDenyMode     = "auto-permission-deny-mode"
	fieldAutoPermUnresolved   = "auto-permission-unresolved-mode"
	fieldAutoPermThinking     = "auto-permission-thinking-effort"
	fieldAutoPermFormat       = "auto-permission-response-format"
	fieldAutoPermTimeout      = "auto-permission-timeout"
	fieldAutoPermOutputTokens = "auto-permission-output-tokens"
	fieldAutoPermRetries      = "auto-permission-retries"
	fieldAutoPermGrowth       = "auto-permission-retry-growth"
	fieldAutoPermRetryCeiling = "auto-permission-retry-ceiling"
	fieldAutoPermInputBytes   = "auto-permission-input-bytes"
	fieldAutoPermInstructions = "auto-permission-instructions"
	fieldHistoryEnabled       = "history-enabled"
	fieldHistoryGitIgnore     = "history-gitignore"
	fieldHistoryExternal      = "history-external"
	fieldHistorySafety        = "history-safety"
	fieldHistoryMaxFile       = "history-max-file"
	fieldHistoryDebounce      = "history-debounce"
	fieldHistoryReconcile     = "history-reconcile"
	fieldHistoryKeepHours     = "history-keep-hours"
	fieldHistoryKeepDaily     = "history-keep-daily"
	fieldHistoryMaxSnaps      = "history-max-snapshots"
	fieldHistoryBudget        = "history-budget"
	fieldHistoryIgnores       = "history-ignores"
)

// New creates a settings model from current AppSettings (application scope).
func New(th *theme.Theme, current types.AppSettings, presets []types.ProviderPreset) *Model {
	return NewScoped(th, current, presets, types.SettingsScopeApplication, current, types.WorkspaceOverrides{})
}

// NewScoped creates a settings model for application or workspace scope.
func NewScoped(th *theme.Theme, current types.AppSettings, presets []types.ProviderPreset, scope types.SettingsScope, appDefaults types.AppSettings, _ types.WorkspaceOverrides) *Model {
	types.ApplyAppDefaults(&current)
	current.Agents.AgentProfiles = types.CloneAgentProfiles(current.Agents.AgentProfiles)
	categories := make(map[string]types.CategorySettings, len(current.Agents.Categories))
	for id, value := range current.Agents.Categories {
		categories[id] = value
	}
	current.Agents.Categories = categories
	appearance := current.Theme
	editor := current.Editor
	lsp := current.LSP
	uiSettings := current.UI
	if len(presets) == 0 {
		for _, preset := range provider.PresetList() {
			presets = append(presets, types.ProviderPreset{
				ID: preset.ID, Name: preset.Name, HelpText: preset.HelpText,
				BaseURL: preset.BaseURL, StandardID: preset.StandardID,
			})
		}
	}
	m := &Model{
		th: th, presets: presets,
		accounts: cloneAccounts(current.Accounts), authGeneration: map[string]uint64{}, authErrors: map[string]string{}, fetchGeneration: map[string]uint64{},
		models: make(map[string][]types.ModelEntry), scope: scope, appDefaults: appDefaults,
		applicationRevision: current.ApplicationRevision,
		workspaceRevision:   current.WorkspaceRevision,
		agents:              &current.Agents,
		memoryAgent:         current.Memory.MemoryCategory,
		autoReviewer:        current.Safety.AutoPermission.Normalized(),
		capabilities:        capability.Normalize(current.Capabilities),
		capabilityDiscovery: make(map[capability.Kind][]capability.Entry),
		terminalShells:      slices.Clone(current.TerminalShells),
	}
	accountIDs := make([]string, 0, len(m.accounts))
	for id := range m.accounts {
		accountIDs = append(accountIDs, id)
	}
	slices.Sort(accountIDs)
	if len(accountIDs) > 0 {
		m.accountID = accountIDs[0]
	}
	appearanceFields := append([]*ui.FieldState{
		ui.SeparatorFieldState("theme", "Theme"),
		ui.CycleFieldState("preset", "Theme", append([]string(nil), theme.AllPresetNames...), appearance.Preset, "A complete palette for the whole window. ‹ and › preview themes live; Close without saving to go back."),
		ui.SeparatorFieldState("palette", "Palette"),
	}, appearanceColorFields(appearance.Preset, appearance)...)
	appearanceFields = append(appearanceFields,
		// The form shows the effective value on each row; when the stored
		// value is unset (nil) the accessor resolves to the default. Blank
		// input means "inherit the default". Fields are Live so the frame
		// previews around the live shell while Settings stays open.
		ui.SeparatorFieldState("frame", "Window margins"),
		liveFramePadField("frame-top", "Top", "rows", appearance.PadTopOrDefault(), "Blank rows above the header. Changes preview immediately."),
		liveFramePadField("frame-bottom", "Bottom", "rows", appearance.PadBottomOrDefault(), "Blank rows below the status bar. Changes preview immediately."),
		liveFramePadField("frame-left", "Left", "columns", appearance.PadLeftOrDefault(), "Blank columns left of the activity rail. Changes preview immediately."),
		liveFramePadField("frame-right", "Right", "columns", appearance.PadRightOrDefault(), "Blank columns right of the content. Changes preview immediately."),
	)
	m.categories = []Category{
		{Name: "Accounts", Group: "AI", Description: "Named account credentials and complete connection settings.", Form: m.buildAccountsForm()},
		{Name: "Main Agent", Group: "AI", Description: "Workspace defaults for planning and delegation.", Form: m.buildAgentForm(current.Agents.Categories["default"], true)},
		{Name: "Researcher", Group: "AI", Description: "Workspace defaults for research and context gathering.", Form: m.buildAgentForm(current.Agents.Categories["researcher"], false)},
		{Name: "Crafter", Group: "AI", Description: "Workspace defaults for implementation work.", Form: m.buildAgentForm(current.Agents.Categories["crafter"], false)},
		{Name: "Aeron", Group: "AI", Description: "Workspace defaults for mechanical tasks.", Form: m.buildAgentForm(current.Agents.Categories["aeron"], false)},
		{Name: "Memory", Group: "AI", Description: "Durable workspace memory: recall, extraction, consolidation.", Form: m.buildMemoryForm(current.Memory)},
		{Name: "Connection", Group: "Network", Description: "Application-wide connection timeouts, pooling, keep-alive, and HTTP protocol behavior.", Form: m.buildNetworkConnectionForm(current.Network)},
		{Name: "Proxy", Group: "Network", Description: "Environment, direct, or custom HTTP and SOCKS proxy routing with explicit DNS behavior.", Form: m.buildNetworkProxyForm(current.Network)},
		{Name: "Certificates", Group: "Network", Description: "System trust, custom certificate authorities, exact-host certificate exceptions, and mTLS identity.", Form: m.buildNetworkCertificatesForm(current.Network)},
		{Name: "Command Safety", Group: "Security", Description: "Command severity, confirmation, and dangerous-action attention.", Form: m.buildSafetyForm(current.Safety)},
		{Name: "Auto Review Policy", Group: "Security", Description: "Synchronized view of Auto Review Agent's bounded runtime policy.", Form: m.buildAutoReviewPolicyForm(current.Safety.AutoPermission)},
		{Name: "Appearance", Group: "IDE", Description: "Theme palette and surface colors.", Form: formModel(appearanceFields...)},
		{Name: "Editor", Group: "IDE", Description: "Editing, wrapping, syntax, and save behavior.", Form: formModel(
			ui.SeparatorFieldState("editing", "Editing"),
			ui.NumberFieldState("tab-size", "Tab size", editor.TabSize, "spaces", "How wide a tab is, and how many spaces Tab inserts."),
			ui.BoolFieldState("word-wrap", "Word wrap", editor.WordWrap, "Wrap long lines inside the editor instead of scrolling sideways."),
			ui.BoolFieldState("line-numbers", "Line numbers", editor.LineNumbers, "Show the line-number gutter."),
			ui.SegmentedFieldState("syntax-theme", "Syntax colors", []ui.FormOption{{ID: "catppuccin", Label: "Catppuccin"}, {ID: "dracula", Label: "Dracula"}, {ID: "github", Label: "GitHub"}, {ID: "monokai", Label: "Monokai"}, {ID: "solarized", Label: "Solarized"}}, editor.SyntaxTheme, "The color grammar used for source code."),
			ui.SegmentedFieldState("render-whitespace", "Show whitespace", []ui.FormOption{{ID: "none", Label: "Never"}, {ID: "boundary", Label: "At edges"}, {ID: "all", Label: "Always"}}, editor.RenderWhitespace, "Draw spaces and tabs. At edges shows only leading and trailing whitespace."),
			ui.SeparatorFieldState("saving", "Saving"),
			autoSaveModeField(editor.AutoSave),
			autoSaveDelayField(editor.AutoSaveDelay),
			ui.BoolFieldState("format-on-save", "Format on manual save", editor.FormatOnSave, "Format explicit saves only. Automatic saves always preserve the exact text you typed."),
		)},
		{Name: "Code Intelligence", Group: "IDE", Description: "Completions, navigation and diagnostics from language servers, and how much they may use.", Form: m.buildLSPForm(lsp)},
		{Name: "Interface", Group: "IDE", Description: "File tree, sidebar, icons, and diagnostics.", Form: formModel(
			ui.SeparatorFieldState("workspace-chrome", "Layout"),
			ui.SegmentedFieldState("context-help", "Shortcut hints", []ui.FormOption{{ID: "compact", Label: "Compact"}, {ID: "expanded", Label: "Expanded"}, {ID: "off", Label: "Off"}}, defaultString(uiSettings.ContextHelp, "compact"), "Shortcuts for the focused area, shown at the top of the window. Full Help (F1) is always available."),
			ui.NumberFieldState("sidebar-width", "Sidebar width", uiSettings.SidebarWidth, "columns", "Width of the file and workspace navigation sidebar."),
			ui.SeparatorFieldState("workspace-tree", "File tree"),
			ui.BoolFieldState("show-hidden", "Hidden files", uiSettings.ShowHiddenFiles, "Include dotfiles in the workspace tree."),
			ui.BoolFieldState("show-icons", "File icons", uiSettings.ShowIcons, "Show file-type icons beside names."),
			ui.BoolFieldState("nerd-font", "Nerd Font icons", uiSettings.UseNerdFont, "Use extended icon glyphs. Turn on only if your terminal font is a Nerd Font."),
			ui.BoolFieldState("git-status", "Git status", uiSettings.ShowGitStatus, "Mark changed files in the tree."),
			ui.BoolFieldState("diagnostics", "Problems", uiSettings.ShowDiagnostics, "Mark files that have language diagnostics."),
		)},
		{Name: "Local History", Group: "IDE", Description: "Automatic project snapshots, retention, and safe restore policy.", Form: m.buildHistoryForm(current.LocalHistory)},
	}
	m.categories = append(m.categories, Category{Name: "Agents", Group: "AI", Description: "Built-in roles and reusable named agents.", Form: formModel()})
	m.lspLanguages = cloneLSPLanguages(types.NormalizeLSP(lsp).Languages)
	m.lspLanguagesBase = cloneLSPLanguages(m.lspLanguages)
	m.categories = append(m.categories,
		Category{Name: "Skills", Group: "AI", Description: "Reusable expertise your agents can load. Search every source and install in one step.", Form: formModel()},
		Category{Name: "MCP", Group: "AI", Description: "Tools and services your agents can call. Search registries or add your own server.", Form: formModel()},
		Category{Name: "Sources", Group: "AI", Description: "Where Skills and MCP search: built-in registries plus your own repositories and folders.", Form: formModel()},
		Category{Name: "Acquisition", Group: "AI", Description: "When agents may pick or install capabilities on their own, and the limits they work within.", Form: m.buildAcquisitionForm()},
		Category{Name: "Language Servers", Group: "IDE", Description: "The server behind each language. Open one to change its command, arguments or environment.", Form: formModel()},
		Category{Name: "Terminal", Group: "IDE", Description: "The shell integrated terminals start, its environment, and where new terminals open.", Form: m.buildTerminalForm(current.Terminal)},
	)
	for index := range m.categories {
		category := index
		m.categories[index].Form.OnChanged(func(fieldID string) {
			m.handleFieldChanged(category, fieldID)
			if m.onChange != nil {
				m.onChange()
			}
		})
	}
	m.ensureVisibleSelection()
	m.preferencesBase = m.preferenceBytes()
	m.nextCategory = -1
	return m
}

func (m *Model) preferenceBytes() []byte {
	s := m.AppSettings()
	s.Accounts = nil
	s.Capabilities.Skills, s.Capabilities.Connections, s.Capabilities.Sources = nil, nil, nil
	encoded, _ := json.Marshal(s)
	return encoded
}

func (m *Model) hasUnsavedPreferences() bool {
	return !bytes.Equal(m.preferencesBase, m.preferenceBytes())
}

// BeginSave claims the retained settings draft for one asynchronous
// persistence attempt. It freezes every ordinary preference field so a result
// can be correlated with the exact values carried by SaveMsg.
func (m *Model) BeginSave() bool {
	if m == nil || m.saving || m.accountBusy {
		return false
	}
	if form := m.categories[catLSP].Form; form != nil {
		form.ValidateAll()
		if form.AnyErrors() {
			m.saveError = "Correct the highlighted Code Intelligence fields before saving."
			m.SelectCategory(catLSP)
			m.EnterForm()
			return false
		}
	}
	if err := (types.LSPSettings{Languages: m.lspLanguages}).Validate(); err != nil {
		m.saveError = "Language Servers: " + err.Error()
		m.SelectCategory(catLanguageServers)
		m.EnterForm()
		return false
	}
	m.saving = true
	m.saveError = ""
	m.saveDisabled = make(map[*ui.FieldState]bool)
	for _, category := range m.categories {
		if category.Form == nil {
			continue
		}
		for _, field := range category.Form.Fields() {
			m.saveDisabled[field] = field.Disabled
			field.Disabled = true
		}
	}
	return true
}

// FinishSave releases an exact pending save. A failed save leaves all values
// and dirty state intact for retry; a successful save advances only the
// preference baseline.
func (m *Model) FinishSave(err error) {
	if m == nil || !m.saving {
		return
	}
	for field, disabled := range m.saveDisabled {
		field.Disabled = disabled
	}
	m.saveDisabled = nil
	m.saving = false
	if err != nil {
		m.saveError = err.Error()
		return
	}
	m.saveError = ""
	for _, category := range m.categories {
		if category.Form != nil {
			category.Form.Commit()
		}
	}
	m.preferencesBase = m.preferenceBytes()
	m.lspLanguagesBase = cloneLSPLanguages(m.lspLanguages)
}

func (m *Model) Saving() bool {
	return m != nil && m.saving
}

func (m *Model) SaveError() string {
	if m == nil {
		return ""
	}
	return m.saveError
}

func (m *Model) accountEditorDirty() bool {
	if m.accountEditor == nil {
		return false
	}
	m.storeAccountForm()
	edit := m.accountEditor
	if edit.isNew {
		return m.categories[catAccounts].Form.AnyDirty() || edit.draft.Label != "" || edit.draft.APIKey != "" || edit.draft.OAuth != nil
	}
	return edit.original != nil && !reflect.DeepEqual(*edit.original, edit.draft)
}

func (m *Model) buildHistoryForm(settings types.LocalHistorySettings) *ui.FormModel {
	extra := ui.TextAreaFieldState(fieldHistoryIgnores, "Also ignore", strings.Join(settings.ExtraIgnorePatterns, "\n"), "One Git-style pattern per line. Stored history never enters the project tree.")
	extra.Rows = 4
	return formModel(
		ui.SeparatorFieldState("history-capture", "Capture"),
		ui.BoolFieldState(fieldHistoryEnabled, "Local history", settings.EnabledValue(), "Keep restorable snapshots of the workspace and of each message boundary."),
		ui.BoolFieldState(fieldHistoryGitIgnore, "Honor .gitignore", settings.UseGitIgnoreValue(), "Skip files your Git ignore rules exclude, plus build output Crude knows about."),
		ui.BoolFieldState(fieldHistoryExternal, "Capture outside edits", settings.CaptureExternalValue(), "Also snapshot changes made by shell commands, generators and other editors."),
		ui.NumberFieldState(fieldHistoryMaxFile, "Skip files over", settings.MaxFileSizeMB, "MB", "Larger files are never read or stored."),
		ui.NumberFieldState(fieldHistoryDebounce, "Snapshot after", settings.DebounceMS, "ms", "Quiet period after file changes before an automatic snapshot."),
		ui.NumberFieldState(fieldHistoryReconcile, "Rescan every", settings.ReconcileIntervalSeconds, "seconds", "A bounded rescan that recovers changes the file watcher missed."),
		extra,
		ui.SeparatorFieldState("history-retention", "Retention"),
		ui.NumberFieldState(fieldHistoryKeepHours, "Keep everything for", settings.RetentionKeepHours, "hours", "Every snapshot from this recent window is kept."),
		ui.NumberFieldState(fieldHistoryKeepDaily, "Then keep daily for", settings.RetentionKeepDaily, "days", "Older history keeps one snapshot per day."),
		ui.NumberFieldState(fieldHistoryMaxSnaps, "Keep at most", settings.RetentionMaxSnapshots, "snapshots", "Upper bound after protected message and restore snapshots."),
		ui.NumberFieldState(fieldHistoryBudget, "Storage budget", settings.StorageBudgetMB, "MB", "Unprotected history is pruned once storage passes this size."),
		ui.SeparatorFieldState("history-restore", "Restore safety"),
		&ui.FieldState{ID: fieldHistorySafety, Label: "Safety snapshot", Type: ui.FieldText, Value: ui.StringValue("Always on"), ReadOnly: true, Description: "Every restore captures current state first; this safety invariant cannot be disabled."},
	)
}

func formModel(fields ...*ui.FieldState) *ui.FormModel {
	m := ui.NewFormModel()
	for _, field := range fields {
		liftUnit(field)
		m.AddField(field)
	}
	return m
}

// settingsUnits are the measurement suffixes a settings label may carry in
// parentheses. They move into the field's Unit so labels read as names
// ("Debounce") and the value reads as a quantity ("750 ms").
var settingsUnits = []string{"ms", "seconds", "minutes", "hours", "bytes", "KB", "MB", "MiB", "tokens"}

// liftUnit moves a trailing "(unit)" from a single-line field's label into
// its Unit slot.
func liftUnit(field *ui.FieldState) {
	if field == nil || field.Unit != "" || (field.Type != ui.FieldText && field.Type != ui.FieldPassword) {
		return
	}
	for _, unit := range settingsUnits {
		if suffix := " (" + unit + ")"; strings.HasSuffix(field.Label, suffix) {
			field.Label, field.Unit = strings.TrimSuffix(field.Label, suffix), unit
			return
		}
	}
}

func autoSaveModeField(mode types.AutoSaveMode) *ui.FieldState {
	return ui.SegmentedFieldState("auto-save", "Auto save", []ui.FormOption{
		{ID: string(types.AutoSaveOff), Label: "Off"},
		{ID: string(types.AutoSaveAfterDelay), Label: "After delay"},
		{ID: string(types.AutoSaveEditorFocusChange), Label: "Editor switch"},
		{ID: string(types.AutoSaveTerminalFocusLoss), Label: "Window blur"},
	}, string(mode), "Save text exactly as typed, never formatted. Editor switch saves when focus leaves the editor; Window blur needs focus reports from your terminal.")
}

func autoSaveDelayField(delay int) *ui.FieldState {
	field := ui.NumberFieldState("auto-save-delay", "Save after", delay, "ms", "Quiet period before an After delay save, from 250 to 60000 ms.")
	field.Validator = func(value ui.FieldValue) []string {
		delay, err := strconv.Atoi(strings.TrimSpace(value.String()))
		if err != nil || delay < 250 || delay > 60000 {
			return []string{"Enter a delay from 250 to 60000 milliseconds"}
		}
		return nil
	}
	return field
}

// appearanceColorFields builds read-only hex+swatch rows reflecting the active
// preset (or an explicit override string when set). They are not editable —
// palette changes happen by cycling the preset.
func appearanceColorFields(preset string, overrides types.ThemeSettings) []*ui.FieldState {
	th := theme.FromPreset(preset)
	resolve := func(override string, col color.Color) string {
		if override != "" {
			return override
		}
		return rendering.ThemeColorToHex(col)
	}
	c := th.Colors
	return []*ui.FieldState{
		readOnlyColorField("background", "Background", resolve(overrides.Bg, c.Bg)),
		readOnlyColorField("foreground", "Foreground", resolve(overrides.Fg, c.Fg)),
		readOnlyColorField("accent", "Accent", resolve(overrides.Accent, c.Accent)),
		readOnlyColorField("accent2", "Secondary accent", resolve(overrides.Accent2, c.Accent2)),
		readOnlyColorField("subpane-bg", "Panel background", resolve(overrides.SubPaneBg, c.SubPaneBg)),
		readOnlyColorField("border", "Border", resolve(overrides.Border, c.Border)),
		readOnlyColorField("selection", "Selection", resolve(overrides.Selection, c.Selection)),
	}
}

func readOnlyColorField(id, label, hex string) *ui.FieldState {
	v := ui.StringValue(hex)
	return &ui.FieldState{
		ID: id, Label: label, Type: ui.FieldText,
		Value: v, Initial: v, ReadOnly: true,
	}
}

func (m *Model) buildAccountsForm() *ui.FormModel {
	return m.buildAccountEditorForm()
}

func cloneAccounts(src map[string]provider.Account) map[string]provider.Account {
	out := make(map[string]provider.Account, len(src))
	for id, a := range src {
		out[id] = provider.CloneAccount(a)
	}
	return out
}

func (m *Model) accountRows() []ui.TableRow {
	ids := make([]string, 0, len(m.accounts))
	for id := range m.accounts {
		a := m.accounts[id]
		query := strings.ToLower(strings.TrimSpace(m.accountQuery))
		if query != "" && !strings.Contains(strings.ToLower(accountConnectionLabel(a)+" "+a.Label+" "+a.Connection.ProviderURL+" "+a.Connection.APIStandard), query) {
			continue
		}
		ids = append(ids, id)
	}
	slices.SortFunc(ids, func(a, b string) int {
		left, right := m.accounts[a], m.accounts[b]
		if c := strings.Compare(accountConnectionLabel(left), accountConnectionLabel(right)); c != 0 {
			return c
		}
		if c := strings.Compare(left.Label, right.Label); c != 0 {
			return c
		}
		return strings.Compare(a, b)
	})
	rows := make([]ui.TableRow, 0, len(ids))
	for _, id := range ids {
		a := m.accounts[id]
		status := "No credentials"
		switch {
		case a.Connection.AuthType == provider.AuthNone:
			status = "Not required"
		case a.OAuth != nil && a.OAuth.AccessToken != "":
			status = "Sign-in stored"
		case a.APIKey != "":
			status = "API key configured"
		}
		rows = append(rows, ui.TableRow{ID: id, Cells: []ui.TableCell{
			{Text: accountConnectionLabel(a)}, {Text: a.Label}, {Text: status},
		}})
	}
	return rows
}

func (m *Model) selectAccount(id string) {
	if id == m.accountID || m.accounts[id].ID != id {
		return
	}
	m.accountID = id
}
func (m *Model) connectionOptions(includeBlank bool) []ui.FormOption {
	out := []ui.FormOption{}
	if includeBlank {
		out = append(out, ui.FormOption{ID: "", Label: "Unconfigured"})
	}
	ids := make([]string, 0, len(m.accounts))
	for id := range m.accounts {
		ids = append(ids, id)
	}
	slices.SortFunc(ids, func(a, b string) int {
		aa, bb := m.accounts[a], m.accounts[b]
		if c := strings.Compare(aa.ProviderID, bb.ProviderID); c != 0 {
			return c
		}
		return strings.Compare(a, b)
	})
	for _, id := range ids {
		a := m.accounts[id]
		label := a.Label
		if label == "" {
			label = id
		}
		out = append(out, ui.FormOption{ID: id, Label: label})
	}
	return out
}
func accountConnectionLabel(account provider.Account) string {
	if account.Connection.Name != "" {
		return account.Connection.Name
	}
	return account.ProviderID
}
func (m *Model) beginAccountEdit(create bool, providerID string) error {
	if m.accountBusy {
		return nil
	}
	edit := &accountEdit{isNew: create}
	if create {
		id, err := provider.NewAccountID()
		if err != nil {
			return err
		}
		if providerID == "" && len(provider.PresetIDs()) > 0 {
			providerID = provider.PresetIDs()[0]
		}
		seed := provider.DefaultTemplates()[providerID]
		edit.draft = provider.Account{ID: id, ProviderID: providerID, Connection: provider.CloneTemplate(seed)}
		edit.seedID = providerID
	} else {
		a, ok := m.accounts[m.accountID]
		if !ok {
			return fmt.Errorf("select an account to edit")
		}
		copy := provider.CloneAccount(a)
		edit.original = &copy
		edit.draft = provider.CloneAccount(a)
	}
	m.accountEditor = edit
	m.accountError = ""
	m.rebuildAccountsForm()
	return nil
}

func (m *Model) beginAccountClone() error {
	if m.accountBusy {
		return nil
	}
	source, ok := m.accounts[m.accountID]
	if !ok {
		return fmt.Errorf("select an account to clone")
	}
	id, err := provider.NewAccountID()
	if err != nil {
		return err
	}
	m.accountEditor = &accountEdit{
		isNew:  true,
		seedID: source.ProviderID,
		draft: provider.Account{
			ID:         id,
			ProviderID: source.ProviderID,
			Connection: provider.CloneTemplate(source.Connection),
		},
	}
	m.accountError = ""
	m.rebuildAccountsForm()
	return nil
}

func (m *Model) cancelAccountEdit() {
	if m.accountBusy {
		return
	}
	if m.accountEditor != nil {
		m.authGeneration[m.accountEditor.draft.ID] = nextAccountRequest.Add(1)
		delete(m.authErrors, m.accountEditor.draft.ID)
		if m.authWaitingID == m.accountEditor.draft.ID {
			m.authWaitingID = ""
		}
	}
	m.accountEditor = nil
	m.accountError = ""
	m.rebuildAccountsForm()
}

func (m *Model) accountEditChange() (types.AccountChange, error) {
	if m.accountEditor == nil {
		return types.AccountChange{}, fmt.Errorf("no account editor is open")
	}
	m.storeAccountForm()
	form := m.categories[catAccounts].Form
	form.ValidateAll()
	if form.AnyErrors() {
		return types.AccountChange{}, fmt.Errorf("correct the highlighted fields")
	}
	edit := m.accountEditor
	a := edit.draft
	if a.ProviderID == "" || a.ID == "" {
		return types.AccountChange{}, fmt.Errorf("choose a starting connection")
	}
	if err := a.Connection.Validate(); err != nil {
		return types.AccountChange{}, err
	}
	for id, existing := range m.accounts {
		if id != a.ID && strings.EqualFold(strings.TrimSpace(existing.Label), a.Label) {
			if name := form.Field(fieldAccountLabel); name != nil {
				name.Errors = []string{"Account names must be unique."}
			}
			return types.AccountChange{}, fmt.Errorf("choose a distinct account name")
		}
	}
	if a.Connection.AuthType == provider.AuthChatGPT {
		if a.OAuth == nil || a.OAuth.AccessToken == "" && a.OAuth.RefreshToken == "" {
			return types.AccountChange{}, fmt.Errorf("sign in with ChatGPT before adding this account")
		}
	} else if a.Connection.AuthType != provider.AuthNone && a.APIKey == "" {
		if field := form.Field(fieldAPIKey); field != nil {
			field.Errors = []string{"Enter an API key for this account."}
		}
		return types.AccountChange{}, fmt.Errorf("configure this account's credentials")
	}
	a = provider.CloneAccount(a)
	change := types.AccountChange{ID: a.ID, ProviderID: a.ProviderID, Before: edit.original, After: &a}
	return change, nil
}

func (m *Model) accountSelectionChange(action string) (types.AccountChange, error) {
	a, ok := m.accounts[m.accountID]
	if !ok {
		return types.AccountChange{}, fmt.Errorf("select an account")
	}
	previous := provider.CloneAccount(a)
	change := types.AccountChange{ID: a.ID, ProviderID: a.ProviderID, Before: &previous}
	switch action {
	case "remove":
		return change, nil
	default:
		return types.AccountChange{}, fmt.Errorf("unknown account action")
	}
}

func (m *Model) applyAccountChange(result AccountChangeResultMsg) {
	m.accountBusy = false
	if result.Err != nil {
		m.accountError = result.Err.Error()
		m.rebuildAccountsForm()
		return
	}
	m.accountError = ""
	if m.accountEditor != nil {
		m.authGeneration[m.accountEditor.draft.ID] = nextAccountRequest.Add(1)
	}
	m.accountEditor = nil
	if result.Change.Before != nil {
		key := accountModelKey(result.Change.ProviderID, result.Change.ID)
		delete(m.models, key)
		m.fetchGeneration[key] = nextAccountRequest.Add(1)
	}
	m.accounts = cloneAccounts(result.Settings.Accounts)
	m.accountQuery = ""
	if result.Change.After == nil {
		m.authGeneration[result.Change.ID] = nextAccountRequest.Add(1)
		m.fetchGeneration[accountModelKey(result.Change.ProviderID, result.Change.ID)]++
		m.accountID = ""
		for _, row := range m.accountRows() {
			m.accountID = row.ID
			break
		}
	} else {
		m.accountID = result.Change.ID
	}
	m.rebuildAccountsForm()
	m.refreshAgentConnectionFields()
}

func (m *Model) refreshAgentConnectionFields() {
	for _, index := range []int{catMain, catResearcher, catCrafter, catAeron, catAgents} {
		form := m.categories[index].Form
		if form == nil {
			continue
		}
		account := form.Field(fieldConnection)
		if account == nil {
			continue
		}
		account.Options = m.connectionOptions(false)
		selected := account.Value.String()
		if selected != "" {
			if _, exists := m.accounts[selected]; !exists {
				account.Errors = []string{"This account is missing. Choose another before saving."}
			} else {
				account.Errors = nil
			}
		}
		if model := form.Field(fieldModel); model != nil {
			model.Options = ui.ModelOptions(m.models[accountModelKey(m.accounts[selected].ProviderID, selected)])
		}
	}
}

var nextAccountRequest atomic.Uint64

func (m *Model) SignInRequest() (string, uint64) {
	id := m.accountID
	if m.accountEditor != nil {
		id = m.accountEditor.draft.ID
	}
	m.authGeneration[id] = nextAccountRequest.Add(1)
	return id, m.authGeneration[id]
}

// Field IDs for the protocol option, capability, and sampling rows.
const (
	fieldProtocolStore       = "protocol-store"
	fieldProtocolMediaReplay = "protocol-media-replay-recovery"
	fieldProtocolZDR         = "protocol-zdr"
	fieldProtocolSummaries   = "protocol-summaries"
	fieldProtocolTruncate    = "protocol-truncation"
	fieldProtocolTier        = "protocol-tier"
	fieldExtraBody           = "extra-body"
	fieldCtxWindow           = "model-context-window"
	fieldMaxOutput           = "model-max-output"
	fieldModalityPrefix      = "modality-"
	fieldTemperature         = "model-temperature"
	fieldTopP                = "model-top-p"
)

// serviceTierOptions turns the protocol's declared tiers into select options.
// The first tier is the wire default and carries the empty ID (inherit).
func serviceTierOptions(tiers []string) []ui.FormOption {
	options := make([]ui.FormOption, 0, len(tiers))
	for i, tier := range tiers {
		id := tier
		if i == 0 {
			id = ""
		}
		label := tierLabel(tier)
		if i == 0 {
			label += " (default)"
		}
		options = append(options, ui.FormOption{ID: id, Label: label})
	}
	return options
}

// tierLabel humanizes a tier identifier ("flex" → "Flex", "standard_only" →
// "Standard only").
func tierLabel(tier string) string {
	words := strings.Split(strings.ReplaceAll(tier, "-", "_"), "_")
	for i, word := range words {
		if word != "" {
			words[i] = strings.ToUpper(word[:1]) + word[1:]
		}
	}
	return strings.Join(words, " ")
}

func protocolStoreValue(settings *provider.ProtocolSettings) *bool {
	if settings == nil {
		return nil
	}
	return settings.Store
}

func protocolMediaReplayValue(settings *provider.ProtocolSettings) *bool {
	if settings == nil {
		return nil
	}
	return settings.MediaReplayRecovery
}

func protocolSummariesValue(settings *provider.ProtocolSettings) *bool {
	if settings == nil {
		return nil
	}
	return settings.ReasoningSummaries
}

// extraBodyField builds the raw-JSON power-user field: a multi-line editor
// (JSON blobs do not fit one row) validating that the input is a JSON object.
func (m *Model) extraBodyField(cfg provider.Template) *ui.FieldState {
	f := ui.TextAreaFieldState(fieldExtraBody, "Extra JSON body", formatExtraBody(cfg.ExtraBody),
		"Raw JSON object merged into every request body sent.")
	f.Validator = func(value ui.FieldValue) []string {
		trimmed := strings.TrimSpace(value.String())
		if trimmed == "" {
			return nil
		}
		var decoded map[string]any
		if err := json.Unmarshal([]byte(trimmed), &decoded); err != nil {
			return []string{"Must be a JSON object, e.g. {\"key\": \"value\"}"}
		}
		return nil
	}
	return f
}

// formatExtraBody renders stored extras as compact JSON for editing.
func formatExtraBody(extra map[string]any) string {
	if len(extra) == 0 {
		return ""
	}
	raw, err := json.Marshal(extra)
	if err != nil {
		return ""
	}
	return string(raw)
}

// Dialer field IDs. Each row offers a ladder of concrete values; the built-in
// default leads the ladder and is labeled "(default)" so the inherited
// behavior is always visible in the control itself.
const (
	fieldDialTimeout         = "dial-request-timeout"
	fieldDialConnect         = "dial-connect-timeout"
	fieldDialAttempts        = "dial-max-attempts"
	fieldDialBackoff         = "dial-max-backoff"
	fieldDialProgressStartup = "dial-progress-startup-timeout"
	fieldDialProgressFloor   = "dial-progress-timeout-floor"
	fieldDialProgressCeiling = "dial-progress-timeout-ceiling"
)

var (
	dialTimeoutLadder  = []int{30, 60, 120, 300, 600} // default: unlimited (0)
	dialConnectLadder  = []int{5, 10, 30, 60}         // default: inherit Network
	dialAttemptsLadder = []int{1, 2, 3, 5, 10}        // default: 6
	dialBackoffLadder  = []int{15, 30, 60, 300}       // default: 30s
	dialProgressLadder = []int{15, 30, 45, 60, 120, 300, 600, 900, 1800}
)

// dialerFields builds the transport-tuning rows for one provider. A nil dialer
// selects the built-in defaults; nothing is persisted unless a row moves off
// its default.
func dialerFields(dialer *provider.DialSettings) []*ui.FieldState {
	d := dialerValue(dialer)
	startup := dialSecondsField(fieldDialProgressStartup, "Initial progress timeout", d.ProgressStartupTimeoutSeconds,
		int(provider.DefaultProgressStartupTimeout.Seconds()), dialProgressLadder,
		"Silence before the first byte in each upload, response-header, or download phase.")
	floor := dialSecondsField(fieldDialProgressFloor, "Min progress timeout", d.ProgressTimeoutFloorSeconds,
		int(provider.DefaultProgressTimeoutFloor.Seconds()), dialProgressLadder,
		"Minimum allowed silence after bytes start moving; must not exceed the initial timeout.")
	ceiling := dialSecondsField(fieldDialProgressCeiling, "Max progress timeout", d.ProgressTimeoutCeilingSeconds,
		int(provider.DefaultProgressTimeoutCeiling.Seconds()), dialProgressLadder,
		"Maximum adaptive silence budget; must be at least the initial timeout.")
	backoff := dialSecondsField(fieldDialBackoff, "Max backoff", d.MaxBackoffSeconds,
		int(provider.DefaultMaxBackoff.Seconds()), dialBackoffLadder,
		"Local exponential delay cap (1-300 seconds). Retry-After may extend it; every cooldown is at most 300 seconds.")
	backoff.Validator = func(value ui.FieldValue) []string {
		if backoff.ReadOnly {
			return nil
		}
		seconds, err := strconv.Atoi(strings.TrimSpace(value.String()))
		if err != nil || seconds < 1 || seconds > 300 {
			return []string{"Enter a retry delay from 1 to 300 seconds."}
		}
		return nil
	}
	validateProgress := func(_ ui.FieldValue) []string {
		if startup.ReadOnly {
			return nil
		}
		policy := provider.DialSettings{
			ProgressStartupTimeoutSeconds: dialSecondsFromChoice(startup.Value.String(), int(provider.DefaultProgressStartupTimeout.Seconds())),
			ProgressTimeoutFloorSeconds:   dialSecondsFromChoice(floor.Value.String(), int(provider.DefaultProgressTimeoutFloor.Seconds())),
			ProgressTimeoutCeilingSeconds: dialSecondsFromChoice(ceiling.Value.String(), int(provider.DefaultProgressTimeoutCeiling.Seconds())),
		}
		if err := policy.Validate(); err != nil {
			return []string{err.Error()}
		}
		return nil
	}
	startup.Validator, floor.Validator, ceiling.Validator = validateProgress, validateProgress, validateProgress
	return []*ui.FieldState{
		ui.SeparatorFieldState("dialer", "Connection tuning"),
		dialSecondsField(fieldDialTimeout, "Request timeout", d.RequestTimeoutSeconds, 0, dialTimeoutLadder,
			"Overall cap including upload, streaming, retries and retry waits. Unlimited does not disable progress timeouts."),
		dialConnectField(d.ConnectTimeoutSeconds),
		startup, floor, ceiling,
		dialCountField(fieldDialAttempts, "Max attempts", d.MaxAttempts, provider.DefaultMaxAttempts, dialAttemptsLadder,
			"Tries per request; failures retry with exponential backoff."),
		backoff,
	}
}

func dialConnectField(seconds int) *ui.FieldState {
	field := ui.CycleFieldState(fieldDialConnect, "Connect timeout", nil, strconv.Itoa(seconds),
		"Network defaults inherit separate TCP/TLS budgets. An explicit value overrides both, not model response waiting.")
	field.Options = []ui.FormOption{{ID: "0", Label: "Network defaults"}}
	for _, option := range dialFieldOptions(seconds, 0, dialConnectLadder, "s") {
		if option.ID != "0" {
			field.Options = append(field.Options, option)
		}
	}
	field.Validator = func(value ui.FieldValue) []string {
		if field.ReadOnly {
			return nil
		}
		seconds, err := strconv.Atoi(value.String())
		if err != nil || seconds < 0 || seconds > 300 {
			return []string{"Use Network defaults or 1-300 seconds."}
		}
		return nil
	}
	return field
}

// dialSecondsField renders one timeout row.
func dialSecondsField(id, label string, value, builtinDefault int, ladder []int, description string) *ui.FieldState {
	f := ui.CycleFieldState(id, label, nil, dialChoiceID(value, builtinDefault), description)
	f.Options = dialFieldOptions(value, builtinDefault, ladder, "s")
	return f
}

// dialCountField renders one unit-less count row.
func dialCountField(id, label string, value, builtinDefault int, ladder []int, description string) *ui.FieldState {
	f := ui.CycleFieldState(id, label, nil, dialChoiceID(value, builtinDefault), description)
	f.Options = dialFieldOptions(value, builtinDefault, ladder, "")
	return f
}

// dialChoiceID maps a stored value to its option ID; unset values select the
// built-in default.
func dialChoiceID(value, builtinDefault int) string {
	if value <= 0 {
		return strconv.Itoa(builtinDefault)
	}
	return strconv.Itoa(value)
}

// dialFieldOptions builds a ladder of concrete choices in natural order: the
// built-in default sits at its own value (marked "(default)") and Unlimited
// (0) comes last, so the stepper's ‹ always means less and › always more. A
// stored value outside the ladder is inserted so it stays visible and editable.
func dialFieldOptions(current, builtinDefault int, ladder []int, unit string) []ui.FormOption {
	values := append([]int{builtinDefault}, ladder...)
	if current > 0 {
		values = append(values, current)
	}
	// 0 means Unlimited: it ranks after every finite value.
	rank := func(value int) int {
		if value == 0 {
			return math.MaxInt
		}
		return value
	}
	slices.SortFunc(values, func(a, b int) int { return cmp.Compare(rank(a), rank(b)) })
	values = slices.Compact(values)
	options := make([]ui.FormOption, 0, len(values))
	for _, value := range values {
		options = append(options, ui.FormOption{
			ID:    strconv.Itoa(value),
			Label: dialChoiceLabel(value, builtinDefault, unit),
		})
	}
	return options
}

// dialChoiceLabel renders one concrete value; the built-in default is marked.
func dialChoiceLabel(value, builtinDefault int, unit string) string {
	label := strconv.Itoa(value) + unit
	if value == 0 {
		label = "Unlimited"
	}
	if value == builtinDefault {
		label += " (default)"
	}
	return label
}

// dialerValue returns a non-nil view of the stored dialer so field rendering
// never needs nil checks.
func dialerValue(dialer *provider.DialSettings) *provider.DialSettings {
	if dialer == nil {
		return &provider.DialSettings{}
	}
	return dialer
}

// dialSecondsFromChoice parses an option ID into seconds. Values equal to the
// built-in default persist as 0 (unset); 0 for the request timeout row means
// unlimited, which is itself the built-in default.
func dialSecondsFromChoice(choice string, builtinDefault int) int {
	seconds, err := strconv.Atoi(choice)
	if err != nil || seconds == builtinDefault {
		return 0
	}
	return seconds
}

// dialAttemptsFromChoice parses the attempts option ID; the built-in default
// persists as 0 (unset).
func dialAttemptsFromChoice(choice string) int {
	return dialSecondsFromChoice(choice, provider.DefaultMaxAttempts)
}

// dialerFromForm folds the connection-tuning rows back into persisted settings.
// Rows left on the built-in default persist as unset, and an all-default form
// stores nil — defaults, not a frozen copy of them.
func dialerFromForm(timeout, connect, attempts, backoff, startup, floor, ceiling string) *provider.DialSettings {
	dialer := provider.DialSettings{
		RequestTimeoutSeconds:         dialSecondsFromChoice(timeout, 0),
		ConnectTimeoutSeconds:         dialSecondsFromChoice(connect, 0),
		MaxAttempts:                   dialAttemptsFromChoice(attempts),
		MaxBackoffSeconds:             dialSecondsFromChoice(backoff, int(provider.DefaultMaxBackoff.Seconds())),
		ProgressStartupTimeoutSeconds: dialSecondsFromChoice(startup, int(provider.DefaultProgressStartupTimeout.Seconds())),
		ProgressTimeoutFloorSeconds:   dialSecondsFromChoice(floor, int(provider.DefaultProgressTimeoutFloor.Seconds())),
		ProgressTimeoutCeilingSeconds: dialSecondsFromChoice(ceiling, int(provider.DefaultProgressTimeoutCeiling.Seconds())),
	}
	if dialer == (provider.DialSettings{}) {
		return nil
	}
	return &dialer
}

// chatgptAuthFields builds the OAuth status + sign-in/sign-out rows shown in
// place of the API-key field for the ChatGPT subscription provider.
func (m *Model) chatgptAuthFields(account provider.Account) []*ui.FieldState {
	signedIn := account.OAuth != nil && account.OAuth.AccessToken != ""
	status := "Not signed in"
	if signedIn {
		status = "Sign-in stored"
	}
	if m.authWaitingID == account.ID {
		status = "Waiting for browser sign-in"
	}
	statusField := ui.TextFieldState("chatgpt-status", "Status", status, "OAuth credentials belong only to this account.")
	statusField.ReadOnly = true
	if message := m.authErrors[account.ID]; message != "" {
		statusField.Errors = []string{message}
	}
	fields := []*ui.FieldState{statusField}
	return fields
}

func (m *Model) rebuildAccountsForm() {
	form := m.buildAccountsForm()
	m.categories[catAccounts].Form = form
	form.OnChanged(func(fieldID string) { m.handleFieldChanged(catAccounts, fieldID) })
}

// buildAgentForm builds an agent's behavior form. When isMain is true it also
// carries the Multi-Agent enable/disable toggle so the main settings surface
// mirrors the /settings deck. Communication protocol is deliberately not an
// agent property — it is resolved from the provider at use time.
func (m *Model) buildAgentForm(cfg types.CategorySettings, isMain bool) *ui.FormModel {
	providerID := m.accounts[cfg.AccountID].ProviderID
	fields := []*ui.FieldState{
		ui.SeparatorFieldState("agent", "Agent behavior"),
		ui.SelectFieldState(fieldConnection, "Account", cfg.AccountID, m.connectionOptions(false), "The account this agent connects through."),
		ui.ModelFieldState(fieldModel, "Model", cfg.Model, ui.ModelOptions(m.models[accountModelKey(providerID, cfg.AccountID)]), "Models offered by this account. Enter fetches the list."),
		thinkingSegments(cfg.ThinkingEffort),
	}
	fields = append(fields, m.capabilityFields(cfg)...)
	compaction := types.DefaultContextCompactionPercent
	if cfg.ContextCompactionPercent != nil {
		compaction = *cfg.ContextCompactionPercent
	}
	compactionField := ui.NumberFieldState(fieldContextCompaction, "Compact context at", compaction, "% full",
		"Summarize older context once the window is this full. 0 never compacts automatically.")
	compactionField.Validator = intRangeValidator(0, 100)
	retention := types.DefaultContextRetentionTokens
	if cfg.ContextRetentionTokens != nil {
		retention = *cfg.ContextRetentionTokens
	}
	retentionField := ui.NumberFieldState(fieldContextRetention, "Keep recent input", retention, "tokens",
		"Keep up to this many estimated tokens of recent human input verbatim after compaction. The checkpoint, runtime handoff, and context capacity still take priority.")
	retentionField.Validator = intRangeValidator(types.MinContextRetentionTokens, types.MaxContextRetentionTokens)
	fields = append(fields,
		ui.SeparatorFieldState("context-management", "Context management"),
		compactionField,
		retentionField,
	)
	fields = append(fields, m.samplingFields(cfg)...)
	prompt := ui.TextAreaFieldState("agent-instructions", "System instructions", cfg.Instructions, "Agent-specific instructions.")
	prompt.Rows = 10
	fields = append(fields, ui.TextFieldState("agent-description", "Purpose", cfg.Description, "When to use this agent."), prompt)
	if isMain {
		fields = append(fields,
			ui.SeparatorFieldState("delegation", "Delegation"),
			ui.BoolFieldState(fieldMultiAgent, "Multi Agent",
				m.agentsEnabled(), "Allow delegation to other agents."),
		)
	}
	return formModel(fields...)
}

// thinkingSegments shows every reasoning level at once; Default leaves the
// choice to the provider.
func thinkingSegments(current string) *ui.FieldState {
	options := []ui.FormOption{{ID: "", Label: "Default"}}
	for _, level := range types.ThinkingLevels {
		label := strings.ToUpper(level[:1]) + level[1:]
		if level == "xhigh" {
			label = "X-High"
		}
		options = append(options, ui.FormOption{ID: level, Label: label})
	}
	return ui.SegmentedFieldState(fieldThinkingEffort, "Thinking", options, current,
		"How much the model reasons before answering. Default leaves it to the provider; higher levels are slower and cost more.")
}

// capabilityFields builds the Model capabilities section: window limits and
// modality toggles for the agent's model. Values left blank inherit whatever
// the model registry reports at use time, so they never freeze stale numbers.
func (m *Model) capabilityFields(cfg types.CategorySettings) []*ui.FieldState {
	fields := []*ui.FieldState{
		ui.SeparatorFieldState("capabilities", "Model capabilities"),
		numericField(fieldCtxWindow, "Context window", cfg.ModelContextWindow,
			"Input tokens the model accepts. Leave blank to use what the model reports."),
		numericField(fieldMaxOutput, "Max output", cfg.ModelMaxOutput,
			"Output tokens per response. Leave blank to use what the model reports."),
	}
	for _, field := range fields[1:] {
		field.Unit, field.Placeholder = "tokens", "auto"
	}
	fields[1].Inherited = cfg.ModelContextWindow == 0
	fields[2].Inherited = cfg.ModelMaxOutput == 0
	for _, modality := range provider.KnownModalities() {
		field := ui.ToggleFieldState(
			fieldModalityPrefix+modality,
			modalityLabel(modality),
			slices.Contains(cfg.ModelModalities, modality),
			modalityDescription(modality),
		)
		field.Inherited = cfg.ModelModalities == nil
		fields = append(fields, field)
	}
	return fields
}

// samplingFields builds the Sampling section: temperature and top-p overrides
// in their standard 0–2 / 0–1 ranges. Blank inherits the provider default.
func (m *Model) samplingFields(cfg types.CategorySettings) []*ui.FieldState {
	temperature, topP := "", ""
	if cfg.ModelTemperature != nil {
		temperature = strconv.FormatFloat(*cfg.ModelTemperature, 'f', -1, 64)
	}
	if cfg.ModelTopP != nil {
		topP = strconv.FormatFloat(*cfg.ModelTopP, 'f', -1, 64)
	}
	temperatureField := ui.TextFieldState(fieldTemperature, "Temperature", temperature,
		"0-2; blank = provider default.")
	temperatureField.Validator = floatRangeValidator(0, 2)
	topPField := ui.TextFieldState(fieldTopP, "Top P", topP,
		"0-1; blank = provider default.")
	topPField.Validator = floatRangeValidator(0, 1)
	for _, field := range []*ui.FieldState{temperatureField, topPField} {
		field.Numeric, field.Placeholder = true, "default"
	}
	return []*ui.FieldState{
		ui.SeparatorFieldState("sampling", "Sampling"),
		temperatureField,
		topPField,
	}
}

// floatRangeValidator builds a validator accepting blank input or a float in
// [min, max].
func floatRangeValidator(min, max float64) func(ui.FieldValue) []string {
	return func(value ui.FieldValue) []string {
		trimmed := strings.TrimSpace(value.String())
		if trimmed == "" {
			return nil
		}
		parsed, err := strconv.ParseFloat(trimmed, 64)
		if err != nil || parsed < min || parsed > max {
			return []string{fmt.Sprintf("Must be a number between %s and %s.",
				strconv.FormatFloat(min, 'f', -1, 64), strconv.FormatFloat(max, 'f', -1, 64))}
		}
		return nil
	}
}

func intRangeValidator(min, max int) func(ui.FieldValue) []string {
	return func(value ui.FieldValue) []string {
		parsed, err := strconv.Atoi(strings.TrimSpace(value.String()))
		if err != nil || parsed < min || parsed > max {
			return []string{fmt.Sprintf("Must be an integer between %d and %d.", min, max)}
		}
		return nil
	}
}

// numericField renders an optional integer field; 0 and blank mean inherit.
func numericField(id, label string, value int, description string) *ui.FieldState {
	current := ""
	if value > 0 {
		current = strconv.Itoa(value)
	}
	f := ui.TextFieldState(id, label, current, description)
	f.Numeric = true
	f.Validator = func(v ui.FieldValue) []string {
		trimmed := strings.TrimSpace(v.String())
		if trimmed == "" {
			return nil
		}
		if n, err := strconv.Atoi(trimmed); err != nil || n <= 0 {
			return []string{"Must be a positive whole number."}
		}
		return nil
	}
	return f
}

// modalityLabel humanizes a modality constant for the toggle label.
func modalityLabel(modality string) string {
	switch modality {
	case provider.ModalityImage:
		return "Images"
	case provider.ModalityPDF:
		return "PDF documents"
	case provider.ModalityVideo:
		return "Video"
	case provider.ModalityAudio:
		return "Audio"
	}
	return strings.ToUpper(modality[:1]) + modality[1:]
}

// modalityDescription explains what each input modality toggle enables.
func modalityDescription(modality string) string {
	switch modality {
	case provider.ModalityImage:
		return "Accept image attachments."
	case provider.ModalityPDF:
		return "Accept PDF attachments."
	case provider.ModalityVideo:
		return "Accept video attachments."
	case provider.ModalityAudio:
		return "Accept audio attachments."
	}
	return "The model accepts " + modality + " attachments."
}

// agentsEnabled is a placeholder that is replaced by NewScoped wiring below;
// it reads the controller-level multi-agent flag from the model.
func (m *Model) agentsEnabled() bool {
	if m.agents != nil {
		return m.agents.Enabled
	}
	return true
}

// Memory's job cadence lives here; its agent identity and model live in Agents.
func (m *Model) buildMemoryForm(mem types.MemorySettings) *ui.FormModel {
	return formModel(
		ui.SeparatorFieldState("memory-recall", "Recall"),
		ui.BoolFieldState("auto-recall", "Recall memories", mem.AutoRecallEnabled(), "Add relevant workspace memories to the agent's context on every turn."),
		ui.NumberFieldState("recall-budget", "Recall budget", mem.RecallBudgetOrDefault(types.DefaultMemoryRecallBudget), "tokens", "The most context the recalled memory block may use."),
		ui.NumberFieldState("recall-limit", "Memories per turn", mem.RecallLimitOrDefault(types.DefaultMemoryRecallLimit), "max", "The most memories added to a single turn."),
		ui.SeparatorFieldState("memory-background", "Learning"),
		ui.BoolFieldState("auto-extract", "Learn in background", mem.AutoExtractEnabled(), "Extract durable memories and summaries from finished turns in a background job."),
		ui.NumberFieldState("extract-every", "Learn every", mem.ExtractCadenceOrDefault(types.DefaultMemoryExtractCadence), "turns", "How often background learning runs."),
		ui.BoolFieldState("consolidation", "Tidy memories", mem.ConsolidationEnabled(), "Merge duplicates, resolve contradictions and let stale memories fade. Its model is set in Agents › Memory Agent."),
	)
}

func (m *Model) buildSafetyForm(settings types.SafetySettings) *ui.FormModel {
	severity := func(ids ...string) []ui.FormOption {
		labels := map[string]string{"unknown": "Unknown", "safe": "Safe", "project-write": "Writes", "network": "Network", "destructive": "Destructive", "blocked": "Blocked"}
		options := make([]ui.FormOption, len(ids))
		for index, id := range ids {
			options[index] = ui.FormOption{ID: id, Label: labels[id]}
		}
		return options
	}
	riskOptions := severity("unknown", "safe", "project-write", "network", "destructive", "blocked")
	rules := ui.KeyValuesFieldState(fieldSafetyRules, "Command rules", formatCommandRules(settings.CommandRules),
		"Pattern on the left, severity on the right, e.g. scripts/*.sh → safe or deploy* → destructive. Severities: unknown, safe, project-write, network, destructive, blocked.")
	rules.Placeholder = "Add rule"
	rules.Columns = []string{"Pattern", "Severity"}
	rules.Validator = commandRulesValidator
	return formModel(
		ui.SeparatorFieldState("command-policy", "Command policy"),
		ui.SegmentedFieldState(fieldSafetyConfirmAt, "Ask before", severity("unknown", "project-write", "network", "destructive"), settings.ConfirmAt,
			"Ask before running commands at or above this severity. Unknown asks for everything the parser cannot prove safe."),
		ui.SegmentedFieldState(fieldSafetyUnknown, "Unclassified commands", riskOptions, settings.UnknownCommandRisk,
			"The severity given to commands the structural parser cannot classify."),
		ui.SegmentedFieldState(fieldSafetyScript, "Local scripts", riskOptions, settings.ScriptExecutionRisk,
			"The severity given to running .sh files directly and to shell/source wrappers."),
		rules,
		ui.SeparatorFieldState("safety-attention", "Attention"),
		ui.BoolFieldState(fieldSafetyNotify, "Danger notifications", settings.NotifyDangerous != "off",
			"Keep dangerous and blocked actions visible. Policy hard blocks always apply before auto review."),
	)
}

func (m *Model) buildAutoReviewPolicyForm(settings types.AutoPermissionSettings) *ui.FormModel {
	return formModel(m.autoReviewPolicyFields(settings)...)
}

func (m *Model) buildAutoReviewerForm() *ui.FormModel {
	auto := m.autoReviewer.Normalized()
	purpose := ui.TextFieldState("agent-description", "Purpose", "Reviews eligible root and child permission requests", "Fixed safety role; every agent owns an independent review lifecycle and this agent executes no tools.")
	purpose.ReadOnly = true
	instructions := ui.TextAreaFieldState("agent-instructions", "Extra restrictions", auto.Instructions,
		"May only narrow approvals. Core reviewer safety instructions cannot be changed.")
	instructions.Rows = 8
	instructions.Validator = func(value ui.FieldValue) []string {
		if len(strings.TrimSpace(value.String())) > 1024 {
			return []string{"Use at most 1024 bytes of additional restrictions."}
		}
		return nil
	}
	fields := []*ui.FieldState{
		ui.SelectFieldState(fieldConnection, "Account", auto.AccountID, m.connectionOptions(false), "Explicit reviewer account; never inherits Main Agent."),
		ui.ModelFieldState(fieldModel, "Model", auto.Model, ui.ModelOptions(m.models[accountModelKey(auto.Provider, auto.AccountID)]),
			"Explicit reviewer model; never inherits Main Agent."),
		purpose, instructions,
	}
	fields = append(fields, m.autoReviewPolicyFields(auto)...)
	return formModel(fields...)
}

func (m *Model) autoReviewPolicyFields(settings types.AutoPermissionSettings) []*ui.FieldState {
	auto := settings.Normalized()
	askDeny := []ui.FormOption{{ID: "ask", Label: "Ask me"}, {ID: "deny", Label: "Deny"}}
	thinking := ui.SegmentedFieldState(fieldAutoPermThinking, "Thinking effort", []ui.FormOption{
		{ID: "low", Label: "Low"}, {ID: "medium", Label: "Medium"}, {ID: "high", Label: "High"}, {ID: "max", Label: "Maximum"},
	}, auto.ThinkingEffort, "Low is the reliable default. Higher effort spends more of the output budget; values a model does not support fail closed.")
	format := ui.SegmentedFieldState(fieldAutoPermFormat, "Response format", []ui.FormOption{
		{ID: types.AutoPermissionFormatAuto, Label: "Auto"},
		{ID: types.AutoPermissionFormatJSONSchema, Label: "JSON schema"},
		{ID: types.AutoPermissionFormatJSONObject, Label: "JSON object"},
		{ID: types.AutoPermissionFormatPrompt, Label: "Prompt only"},
	}, auto.ResponseFormat, "Auto (recommended) tries a strict schema on OpenAI Chat endpoints, then falls back when a retry is available. Explicit formats fail closed when unsupported.")
	retries := autoPermissionIntField(fieldAutoPermRetries, "Retries", auto.MaxAttempts-1, 0, 5,
		"Extra requests after a timeout, truncation, transport failure or invalid output. Valid allow, deny and ask decisions are never retried.")
	retries.Unit = "max"
	growth := ui.SegmentedFieldState(fieldAutoPermGrowth, "Grow budget on retry", []ui.FormOption{
		{ID: types.AutoPermissionGrowthFixed, Label: "Fixed"},
		{ID: types.AutoPermissionGrowth2x, Label: "2×"},
		{ID: types.AutoPermissionGrowth4x, Label: "4×"},
	}, auto.RetryTokenGrowth, "How much the output budget grows after each technical failure (4× recommended). The retry ceiling always wins.")
	ceiling := autoPermissionIntField(fieldAutoPermRetryCeiling, "Retry ceiling", auto.MaxRetryOutputTokens, 16, 8192,
		"Hard per-attempt output limit. Too little headroom can exhaust reasoning before the verdict.")
	ceiling.Unit = "tokens"
	unresolved := ui.SegmentedFieldState(fieldAutoPermUnresolved, "When unsure", askDeny, auto.UnresolvedMode,
		"Ask me pauses for a person. Deny rejects structural uncertainty, a model 'ask' or exhausted recovery so unattended work continues safely.")
	timeout := autoPermissionIntField(fieldAutoPermTimeout, "Timeout per attempt", auto.TimeoutSeconds, 1, 120,
		"Each attempt has this deadline. A fully exhausted review never approves.")
	timeout.Unit = "seconds"
	initial := autoPermissionIntField(fieldAutoPermOutputTokens, "First attempt budget", auto.MaxOutputTokens, 16, 4096,
		"First-attempt response cap, including hidden reasoning on providers that report it as output.")
	initial.Unit = "tokens"
	input := autoPermissionIntField(fieldAutoPermInputBytes, "Review input limit", auto.MaxInputBytes, 64<<10, 1<<20,
		"Caps the complete serialized review payload for the exact tool arguments and operation metadata; conversation history and the ambient environment are excluded. The safe floor is 64 KiB (65536), the ceiling 1 MiB (1048576), and nothing is truncated.")
	input.Unit = "bytes"
	return []*ui.FieldState{
		ui.SeparatorFieldState("auto-permission-section-outcomes", "Review outcomes"),
		ui.SegmentedFieldState(fieldAutoPermDenyMode, "When it denies", askDeny, auto.DenyMode,
			"Ask you, or reject the call, when the reviewer returns deny. Review never creates a permanent grant."),
		unresolved,
		thinking,
		format,
		ui.SeparatorFieldState("auto-permission-section-recovery", "Technical recovery"),
		timeout,
		initial,
		retries,
		growth,
		ceiling,
		ui.SeparatorFieldState("auto-permission-section-bounds", "Review bounds"),
		input,
	}
}

func autoPermissionIntField(id, label string, value, minValue, maxValue int, description string) *ui.FieldState {
	field := ui.TextFieldState(id, label, strconv.Itoa(value), description)
	field.Validator = intRangeValidator(minValue, maxValue)
	return field
}

func autoPermissionFromForm(current types.AutoPermissionSettings, form *ui.FormModel) types.AutoPermissionSettings {
	auto := current.Normalized()
	if form == nil {
		return auto
	}
	value := func(id, fallback string) string {
		if field := form.Field(id); field != nil {
			return field.Value.String()
		}
		return fallback
	}
	auto.DenyMode = value(fieldAutoPermDenyMode, auto.DenyMode)
	auto.UnresolvedMode = value(fieldAutoPermUnresolved, auto.UnresolvedMode)
	auto.ThinkingEffort = value(fieldAutoPermThinking, auto.ThinkingEffort)
	auto.ResponseFormat = value(fieldAutoPermFormat, auto.ResponseFormat)
	auto.TimeoutSeconds = parseOpt(value(fieldAutoPermTimeout, strconv.Itoa(auto.TimeoutSeconds)), auto.TimeoutSeconds)
	auto.MaxOutputTokens = parseOpt(value(fieldAutoPermOutputTokens, strconv.Itoa(auto.MaxOutputTokens)), auto.MaxOutputTokens)
	auto.MaxAttempts = parseOpt(value(fieldAutoPermRetries, strconv.Itoa(auto.MaxAttempts-1)), auto.MaxAttempts-1) + 1
	auto.RetryTokenGrowth = value(fieldAutoPermGrowth, auto.RetryTokenGrowth)
	auto.MaxRetryOutputTokens = parseOpt(value(fieldAutoPermRetryCeiling, strconv.Itoa(auto.MaxRetryOutputTokens)), auto.MaxRetryOutputTokens)
	auto.MaxInputBytes = parseOpt(value(fieldAutoPermInputBytes, strconv.Itoa(auto.MaxInputBytes)), auto.MaxInputBytes)
	return auto.Normalized()
}

func validateAutoReviewPolicyForm(form *ui.FormModel) {
	if form == nil {
		return
	}
	for _, id := range []string{fieldAutoPermFormat, fieldAutoPermTimeout, fieldAutoPermOutputTokens, fieldAutoPermRetries, fieldAutoPermRetryCeiling, fieldAutoPermInputBytes} {
		if field := form.Field(id); field != nil {
			field.Validate()
		}
	}
	initial := parseOpt(formFieldValue(form, fieldAutoPermOutputTokens), 256)
	retries := parseOpt(formFieldValue(form, fieldAutoPermRetries), 2)
	growth := formFieldValue(form, fieldAutoPermGrowth)
	ceiling := parseOpt(formFieldValue(form, fieldAutoPermRetryCeiling), 1024)
	field := form.Field(fieldAutoPermRetryCeiling)
	if field == nil || field.HasErrors() {
		return
	}
	if ceiling < initial {
		field.Errors = append(field.Errors, "The retry ceiling must be at least the first attempt budget.")
	} else if retries > 0 && growth != types.AutoPermissionGrowthFixed && ceiling == initial {
		field.Errors = append(field.Errors, "Growing retries need a ceiling above the first attempt budget.")
	}
}

func (m *Model) validateAutoReviewFormat(form *ui.FormModel, accountID string) {
	if form == nil || accountID == "" {
		return
	}
	format := formFieldValue(form, fieldAutoPermFormat)
	if format != types.AutoPermissionFormatJSONSchema && format != types.AutoPermissionFormatJSONObject {
		return
	}
	account, ok := m.accounts[accountID]
	standard := ""
	if ok {
		standard = account.Connection.APIStandard
	}
	if standard == "" && ok {
		standard = provider.DefaultStandardID(account.ProviderID)
	}
	if standard != "openai" {
		if field := form.Field(fieldAutoPermFormat); field != nil {
			field.Errors = append(field.Errors, "Explicit JSON response formats require an OpenAI Chat-compatible account; use Auto or Prompt only.")
		}
	}
}

func formFieldValue(form *ui.FormModel, id string) string {
	if form == nil {
		return ""
	}
	if field := form.Field(id); field != nil {
		return field.Value.String()
	}
	return ""
}

// Command rules edit as a two-column table, one "pattern: severity" line per
// rule. The severity is the text after the last colon, so patterns may
// themselves contain colons.
func formatCommandRules(rules []types.CommandSeverityRule) string {
	lines := make([]string, 0, len(rules))
	for _, rule := range rules {
		if strings.TrimSpace(rule.Pattern) != "" && strings.TrimSpace(rule.Risk) != "" {
			lines = append(lines, strings.TrimSpace(rule.Pattern)+": "+strings.TrimSpace(rule.Risk))
		}
	}
	return strings.Join(lines, "\n")
}

func splitCommandRule(line string) (string, string, bool) {
	index := strings.LastIndex(line, ":")
	if index < 0 {
		return "", "", false
	}
	pattern, risk := strings.TrimSpace(line[:index]), strings.TrimSpace(line[index+1:])
	return pattern, risk, pattern != "" && risk != ""
}

func parseCommandRules(value string) []types.CommandSeverityRule {
	var rules []types.CommandSeverityRule
	for _, line := range strings.Split(value, "\n") {
		if pattern, risk, ok := splitCommandRule(line); ok {
			rules = append(rules, types.CommandSeverityRule{Pattern: pattern, Risk: risk})
		}
	}
	return rules
}

func commandRulesValidator(value ui.FieldValue) []string {
	validRisk := map[string]bool{"unknown": true, "safe": true, "project-write": true, "network": true, "destructive": true, "blocked": true}
	for _, line := range strings.Split(value.String(), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if _, risk, ok := splitCommandRule(line); !ok || !validRisk[risk] {
			return []string{"Each rule needs a pattern and one of: unknown, safe, project-write, network, destructive, blocked."}
		}
	}
	return nil
}

type standardDefault struct {
	ID     string
	Label  string
	Detail string
}

func standardOptions(def *standardDefault) []ui.FormOption {
	options := make([]ui.FormOption, 0, len(provider.StandardList())+1)
	if def != nil {
		options = append(options, ui.FormOption{ID: def.ID, Label: def.Label, Detail: def.Detail})
	}
	for _, standard := range provider.StandardList() {
		options = append(options, ui.FormOption{
			ID: standard.ID, Label: standard.Name, Detail: standard.Description,
		})
	}
	return options
}

func providerStandard(providerID string, cfg provider.Template) string {
	if cfg.APIStandard != "" {
		return cfg.APIStandard
	}
	return provider.DefaultStandardID(providerID)
}

func parseOpt(value string, defaultVal int) int {
	if value == "" {
		return defaultVal
	}
	if _, err := fmt.Sscanf(value, "%d", &defaultVal); err != nil {
		return defaultVal
	}
	return defaultVal
}

func (m *Model) currentForm() *ui.FormModel {
	if m == nil || m.selected < 0 || m.selected >= len(m.categories) {
		return nil
	}
	return m.categories[m.selected].Form
}

func (m *Model) value(category int, fieldID string) string {
	if category < 0 || category >= len(m.categories) || m.categories[category].Form == nil {
		return ""
	}
	field := m.categories[category].Form.Field(fieldID)
	if field == nil {
		return ""
	}
	return field.Value.String()
}

// ValueForTest exposes a form field's string value for tests.
func (m *Model) ValueForTest(category int, fieldID string) string {
	return m.value(category, fieldID)
}

// SetValueForTest sets a form field value, firing the same onChanged path a
// user edit would.
func (m *Model) SetValueForTest(category int, fieldID, value string) {
	if m.Saving() || category < 0 || category >= len(m.categories) || m.categories[category].Form == nil {
		return
	}
	m.categories[category].Form.SetValue(fieldID, ui.StringValue(value))
}

func (m *Model) SelectAccountForTest(id string) { m.selectAccount(id) }

func (m *Model) isAgentCategory(index int) bool {
	return index == catMain || index == catResearcher || index == catCrafter || index == catAeron || index == catAgents
}

func (m *Model) handleFieldChanged(category int, fieldID string) {
	if category == catTerminal && fieldID == fieldTerminalShell {
		m.syncTerminalCommand(m.categories[catTerminal].Form)
		return
	}
	if category == catAppearance && fieldID == "preset" {
		m.syncAppearanceColors()
		return
	}
	if category == catAutoReviewPolicy {
		validateAutoReviewPolicyForm(m.categories[catAutoReviewPolicy].Form)
		m.autoReviewer = autoPermissionFromForm(m.autoReviewer, m.categories[catAutoReviewPolicy].Form)
		m.validateAutoReviewFormat(m.categories[catAutoReviewPolicy].Form, m.autoReviewer.AccountID)
		return
	}
	if category == catAccounts && m.accountEditor != nil {
		if fieldID == accountFieldSeed && m.accountEditor.isNew {
			id := m.value(catAccounts, accountFieldSeed)
			m.accountEditor.seedID = id
			m.accountEditor.draft.ProviderID = id
			m.accountEditor.draft.Connection = provider.CloneTemplate(provider.DefaultTemplates()[id])
			m.accountEditor.draft.APIKey, m.accountEditor.draft.OAuth = "", nil
			m.rebuildAccountsForm()
			return
		}
		m.storeAccountForm()
		if fieldID == accountFieldAuthType || fieldID == accountFieldProtocol {
			m.rebuildAccountsForm()
		}
		return
	}
	if (m.isAgentCategory(category) || category == catMemory) && fieldID == fieldConnection {
		accountID := m.value(category, fieldConnection)
		account, ok := m.accounts[accountID]
		if !ok {
			return
		}
		form := m.categories[category].Form
		if field := form.Field(fieldModel); field != nil {
			form.SetValue(fieldModel, ui.StringValue(""))
			field.Options = ui.ModelOptions(m.models[accountModelKey(account.ProviderID, accountID)])
			field.Errors, field.Loading, field.LoadMessage = nil, false, ""
		}
		return
	}
	if (m.isAgentCategory(category) || category == catMemory) && fieldID == fieldModel {
		m.autofillCapabilities(category)
		return
	}
	if m.isAgentCategory(category) && strings.HasPrefix(fieldID, fieldModalityPrefix) {
		for _, modality := range provider.KnownModalities() {
			if field := m.categories[category].Form.Field(fieldModalityPrefix + modality); field != nil {
				field.Inherited = false
			}
		}
		m.autofillCapabilities(category)
		return
	}
	if m.isAgentCategory(category) && (fieldID == fieldCtxWindow || fieldID == fieldMaxOutput) {
		field := m.categories[category].Form.Field(fieldID)
		field.Inherited = field.Value.IsEmpty()
		return
	}
}

// autofillCapabilities pre-fills an agent form's capability and sampling rows
// from the selected model's registry entry. Fields the registry doesn't know
// are left untouched so previously saved overrides survive a model switch.
func (m *Model) autofillCapabilities(category int) {
	form := m.categories[category].Form
	if form == nil {
		return
	}
	modelID := m.value(category, fieldModel)
	accountID := m.value(category, fieldConnection)
	providerID := m.accounts[accountID].ProviderID
	var entry types.ModelEntry
	var found bool
	for _, model := range m.models[accountModelKey(providerID, accountID)] {
		if model.ID == modelID {
			entry, found = model, true
			break
		}
	}
	if !found {
		entry = types.ModelEntriesFromInfos([]provider.ModelInfo{{ID: modelID}})[0]
	}
	if field := form.Field(fieldCtxWindow); field != nil && field.Inherited {
		value := ""
		if entry.ContextWindow > 0 {
			value = strconv.Itoa(entry.ContextWindow)
		}
		field.Value = ui.StringValue(value)
		field.Initial, field.Dirty = field.Value, false
	}
	if field := form.Field(fieldMaxOutput); field != nil && field.Inherited {
		value := ""
		if entry.MaxOutput > 0 {
			value = strconv.Itoa(entry.MaxOutput)
		}
		field.Value = ui.StringValue(value)
		field.Initial, field.Dirty = field.Value, false
	}
	for _, modality := range provider.KnownModalities() {
		field := form.Field(fieldModalityPrefix + modality)
		if field == nil {
			continue
		}
		supported, known := entry.ModalitySupport[modality]
		if entry.ModalitySupport == nil && entry.Modalities != nil {
			supported, known = slices.Contains(entry.Modalities, modality), true
		}
		if field.Inherited {
			field.Value = ui.StringValue(modalityChoice(supported))
			field.Initial = field.Value
			field.Dirty = false
		}
		field.DescriptionImportant = false
		field.Description = "Model card: unknown."
		if known {
			field.Description = "Model card: " + modalityChoice(supported) + "."
			if !field.Inherited && (field.Value.String() == "yes") != supported {
				field.Description = "Inconsistent: override " + field.Value.String() + "; card " + modalityChoice(supported) + "."
				field.DescriptionImportant = true
			}
		}
		if slices.Contains(entry.ConflictingModalities, modality) {
			field.Description += " Catalog entries disagree."
			field.DescriptionImportant = true
		}
	}
}

// modalityChoice renders a modality toggle's cycle value.
func modalityChoice(enabled bool) string {
	if enabled {
		return "yes"
	}
	return "no"
}

// syncAppearanceColors refreshes read-only colour rows after a preset cycle.
func (m *Model) syncAppearanceColors() {
	if m == nil || catAppearance >= len(m.categories) || m.categories[catAppearance].Form == nil {
		return
	}
	preset := m.value(catAppearance, "preset")
	form := m.categories[catAppearance].Form
	for _, field := range appearanceColorFields(preset, types.ThemeSettings{}) {
		if f := form.Field(field.ID); f != nil {
			f.Value = field.Value
			f.Initial = field.Value
			f.Dirty = false
		}
	}
}

// Update accepts asynchronous workspace results. User interaction is owned by
// the mounted component tree rather than a parallel legacy form event loop.
func (m *Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case CapabilityResultMsg:
		m.applyCapabilityResult(msg)
	case types.ModelCatalogUpdatedMsg:
		// The catalog lives in the core; this shell cannot re-derive model
		// cards locally. Refetch every loaded account list through the core.
		var cmds []tea.Cmd
		for accountID, account := range m.accounts {
			if _, loaded := m.models[accountModelKey(account.ProviderID, accountID)]; loaded {
				cmds = append(cmds, m.fetchModelsForAccountCmd(account.ProviderID, accountID))
			}
		}
		return *m, tea.Batch(cmds...)
	case AccountChangeResultMsg:
		if msg.Target == m {
			m.applyAccountChange(msg)
		}
	case ModelsMsg:
		if account, ok := m.accounts[msg.AccountID]; ok && account.ProviderID == msg.ProviderID && msg.Generation > 0 && m.fetchGeneration[accountModelKey(msg.ProviderID, msg.AccountID)] == msg.Generation {
			m.setModelsForAccount(msg.ProviderID, msg.AccountID, msg.Models)
		}
	case ModelsErrorMsg:
		if account, ok := m.accounts[msg.AccountID]; ok && account.ProviderID == msg.ProviderID && msg.Generation > 0 && m.fetchGeneration[accountModelKey(msg.ProviderID, msg.AccountID)] == msg.Generation {
			m.setModelsErrorForAccount(msg.ProviderID, msg.AccountID, msg.Error)
		}
	case ChatGPTSignInResultMsg:
		if m.accountEditor != nil && m.accountEditor.draft.ID == msg.AccountID && m.accountEditor.draft.Connection.AuthType == provider.AuthChatGPT && msg.Generation > 0 && m.authGeneration[msg.AccountID] == msg.Generation {
			m.authWaitingID = ""
			if msg.Err == nil && msg.Creds != nil {
				m.setChatGPTAuth(msg.AccountID, msg.Creds, "")
			} else {
				text := "Sign-in failed"
				if msg.Err != nil {
					text = msg.Err.Error()
				}
				m.authErrors[msg.AccountID] = text
				m.rebuildAccountsForm()
			}
		}
	case ChatGPTSignOutMsg:
		if m.accountEditor != nil && m.accountEditor.draft.ID == msg.AccountID && m.accountEditor.draft.Connection.AuthType == provider.AuthChatGPT && msg.Generation > 0 && m.authGeneration[msg.AccountID] == msg.Generation {
			m.authWaitingID = ""
			m.authGeneration[msg.AccountID] = nextAccountRequest.Add(1)
			m.setChatGPTAuth(msg.AccountID, nil, "")
		}
	}
	return *m, nil
}
func (m *Model) setChatGPTAuth(id string, creds *provider.OAuthCredentials, errMsg string) {
	if m.accountEditor == nil || m.accountEditor.draft.ID != id {
		return
	}
	a := m.accountEditor.draft
	if creds != nil {
		copy := *creds
		a.OAuth = &copy
	} else {
		a.OAuth = nil
	}
	m.accountEditor.draft = a
	delete(m.authErrors, id)
	m.fetchGeneration[accountModelKey(a.ProviderID, id)] = nextAccountRequest.Add(1)
	delete(m.models, accountModelKey(a.ProviderID, id))
	m.rebuildAccountsForm()
	if errMsg != "" {
		if f := m.categories[catAccounts].Form.Field("chatgpt-status"); f != nil {
			f.Errors = []string{errMsg}
		}
	}
}

func accountModelKey(providerID, accountID string) string { return providerID + "/" + accountID }
func (m *Model) resolvedAccount(category int) (string, string) {
	switch category {
	case catAccounts:
		if m.accountEditor != nil {
			return m.accountEditor.draft.ProviderID, m.accountEditor.draft.ID
		}
		return m.accounts[m.accountID].ProviderID, m.accountID
	default:
		id := m.value(category, fieldConnection)
		return m.accounts[id].ProviderID, id
	}
}
func (m *Model) OpenSelect(fieldID string) tea.Cmd {
	if fieldID != fieldModel || m.currentForm() == nil {
		return nil
	}
	return m.prefetchModels(m.selected)
}

// prefetchModels asks the core for the selected account's model list when a
// form showing that account has none yet. The core owns the model catalog, so
// its entries are the only source of a model's capabilities: an agent editor
// prefetches on open and on every account change, and capability rows fill as
// soon as the list arrives instead of waiting for the model dropdown.
func (m *Model) prefetchModels(category int) tea.Cmd {
	if m == nil || category < 0 || category >= len(m.categories) || m.categories[category].Form == nil {
		return nil
	}
	providerID, accountID := m.resolvedAccount(category)
	if providerID == "" || accountID == "" {
		return nil
	}
	field := m.categories[category].Form.Field(fieldModel)
	if field == nil || len(field.Options) > 0 || field.Loading {
		return nil
	}
	if models, loaded := m.models[accountModelKey(providerID, accountID)]; loaded && len(models) > 0 {
		field.Options = ui.ModelOptions(models)
		m.autofillCapabilities(category)
		return nil
	}
	field.Loading, field.LoadMessage = true, "Fetching models…"
	return m.fetchModelsForAccountCmd(providerID, accountID)
}
func (m *Model) fetchModelsCmd() tea.Cmd {
	account := m.accounts[m.accountID]
	return m.fetchModelsForAccountCmd(account.ProviderID, m.accountID)
}
func (m *Model) fetchModelsForAccountCmd(providerID, accountID string) tea.Cmd {
	account, ok := cloneAccounts(m.accounts)[accountID]
	if m.accountEditor != nil && m.accountEditor.draft.ID == accountID {
		m.storeAccountForm()
		account = cloneAccounts(map[string]provider.Account{accountID: m.accountEditor.draft})[accountID]
		ok = true
	}
	if !ok || account.ProviderID != providerID {
		return nil
	}
	effective := account.Connection
	key := accountModelKey(providerID, accountID)
	m.fetchGeneration[key] = nextAccountRequest.Add(1)
	generation := m.fetchGeneration[key]
	return func() tea.Msg {
		return FetchModelsMsg{ProviderID: providerID, AccountID: accountID, Generation: generation, BaseURL: effective.ProviderURL, APIKey: account.APIKey, StandardID: providerStandard(providerID, effective), Account: account}
	}
}
func (m *Model) setModelsForAccount(providerID, accountID string, models []types.ModelEntry) {
	m.models[accountModelKey(providerID, accountID)] = models
	for index := range m.categories {
		if !m.isAgentCategory(index) && index != catMemory {
			continue
		}
		pid, aid := m.resolvedAccount(index)
		if pid != providerID || aid != accountID {
			continue
		}
		if field := m.categories[index].Form.Field(fieldModel); field != nil {
			field.Options = ui.ModelOptions(models)
			field.Errors, field.Loading, field.LoadMessage = nil, false, ""
		}
		if m.isAgentCategory(index) {
			m.autofillCapabilities(index)
		}
	}
}
func (m *Model) setModelsErrorForAccount(providerID, accountID, message string) {
	for index := range m.categories {
		if !m.isAgentCategory(index) && index != catMemory {
			continue
		}
		pid, aid := m.resolvedAccount(index)
		if pid != providerID || aid != accountID {
			continue
		}
		if field := m.categories[index].Form.Field(fieldModel); field != nil {
			field.Errors, field.Loading, field.LoadMessage = []string{message}, false, ""
		}
	}
}

func (m *Model) searchText() string {
	if m == nil || m.searchEd == nil {
		return ""
	}
	return m.searchEd.Value()
}

func (m *Model) SetSearchQuery(query string) {
	if m.searchEd == nil {
		m.searchEd = ui.NewTextEdit()
	}
	m.searchEd.SetValue(query)
	if !m.editorOpen() {
		m.ensureVisibleSelection()
	}
}

func (m *Model) SetSearchActive(active bool) { m.searchActive = active }
func (m *Model) SelectedCategory() int       { return m.selected }
func (m *Model) Pane() int                   { return m.pane }

// Categories exposes the settings pages for tests that assert painted rail
// content after navigation.
func (m *Model) Categories() []Category { return m.categories }
func (m *Model) Scope() types.SettingsScope {
	if m == nil || m.scope == "" {
		return types.SettingsScopeApplication
	}
	return m.scope
}
func (m *Model) SetScope(scope types.SettingsScope) {
	if m.scope == scope {
		return
	}
	m.storeAccountForm()
	m.scope = scope
	if scope == types.SettingsScopeWorkspace && m.selected == catAccounts {
		m.SelectCategory(catMain)
	}
	if len(m.categories) > catAccounts {
		m.rebuildAccountsForm()
	}
	if len(m.categories) > catNetworkCertificates {
		m.rebuildNetworkForms()
	}
}
func (m *Model) Overrides() types.WorkspaceOverrides {
	if m.Scope() != types.SettingsScopeWorkspace {
		return types.WorkspaceOverrides{}
	}
	return types.WorkspaceOverridesFromDiff(m.appDefaults, m.AppSettings())
}

func (m *Model) SelectCategory(index int) {
	if index >= 0 && index < len(m.categories) && (index != catAccounts || m.Scope() == types.SettingsScopeApplication) {
		m.selected, m.pane = index, 0
	}
}

// SelectAccounts routes connection recovery to the application-wide roster.
func (m *Model) SelectAccounts() {
	m.SetScope(types.SettingsScopeApplication)
	m.SelectCategory(catAccounts)
}

// MoveCategory shifts the selected category by delta within the currently
// visible (search-filtered) list. Returns false when nothing changed.
func (m *Model) MoveCategory(delta int) bool {
	visible := m.visibleCategoryIndices()
	if len(visible) == 0 {
		return false
	}
	pos := 0
	for i, index := range visible {
		if index == m.selected {
			pos = i
			break
		}
	}
	next := pos + delta
	if next < 0 {
		next = 0
	}
	if next >= len(visible) {
		next = len(visible) - 1
	}
	if visible[next] == m.selected {
		return false
	}
	m.SelectCategory(visible[next])
	return true
}

func (m *Model) EnterForm() {
	m.searchActive, m.pane = false, 1
}

func (m *Model) CategoryItems() []ui.ListItem {
	visible := m.visibleCategoryIndices()
	items := make([]ui.ListItem, 0, len(visible)*2)
	lastGroup := ""
	for _, index := range visible {
		if index == catAccounts && m.Scope() != types.SettingsScopeApplication {
			continue
		}
		category := m.categories[index]
		if category.Group != lastGroup {
			if lastGroup != "" {
				items = append(items, ui.ListItem{ID: "settings/gap/" + strings.ToLower(category.Group), Spacer: true})
			}
			items = append(items, ui.ListItem{ID: "settings/group/" + strings.ToLower(category.Group), Label: category.Group, Header: true})
			lastGroup = category.Group
		}
		item := ui.ListItem{ID: fmt.Sprintf("settings/category/%d", index), Label: category.Name, Payload: index}
		if m.categoryDirty(index) {
			item.Detail = railModifiedMarker
		}
		items = append(items, item)
	}
	return items
}

// railModifiedMarker tags a rail row whose page holds unsaved edits.
const railModifiedMarker = "●"

// categoryDirty reports unsaved edits on one preference page.
func (m *Model) categoryDirty(index int) bool {
	if index < 0 || index >= len(m.categories) || index == catAccounts {
		return false
	}
	if index == catLanguageServers {
		return m.lspLanguagesDirty()
	}
	if index == catAgents {
		for _, agent := range []int{catMain, catResearcher, catCrafter, catAeron} {
			if form := m.categories[agent].Form; form != nil && form.AnyDirty() {
				return true
			}
		}
	}
	form := m.categories[index].Form
	return form != nil && form.AnyDirty()
}

func (m *Model) visibleCategoryIndices() []int {
	query := strings.ToLower(strings.TrimSpace(m.searchText()))
	exactCategory := -1
	if query != "" {
		for index, category := range m.categories {
			if strings.EqualFold(category.Name, query) {
				exactCategory = index
				break
			}
		}
	}
	indices := make([]int, 0, len(m.categories))
	for index, category := range m.categories {
		// Agent roles live inside the Agents catalog.
		if index == catAgents || index == catResearcher || index == catCrafter || index == catAeron {
			continue
		}
		if index == catMain {
			index, category = catAgents, m.categories[catAgents]
		}
		if index == catAccounts && m.Scope() != types.SettingsScopeApplication {
			continue
		}
		if exactCategory >= 0 && index != exactCategory {
			continue
		}
		if query == "" || m.categoryMatches(index, category, query) {
			indices = append(indices, index)
		}
	}
	groupOrder := make(map[string]int)
	for _, index := range indices {
		group := m.categories[index].Group
		if _, exists := groupOrder[group]; !exists {
			groupOrder[group] = len(groupOrder)
		}
	}
	// Network is application-owned transport policy: it stays last in the
	// rail, after the everyday IDE/Security/AI sections, instead of being
	// interleaved between AI and Security.
	slices.SortStableFunc(indices, func(left, right int) int {
		leftGroup, rightGroup := m.categories[left].Group, m.categories[right].Group
		if leftGroup == rightGroup {
			return railPosition(left) - railPosition(right)
		}
		if leftGroup == networkGroupName {
			return 1
		}
		if rightGroup == networkGroupName {
			return -1
		}
		return groupOrder[leftGroup] - groupOrder[rightGroup]
	})
	return indices
}

// searchTarget is the page a search lands on: the selected page while it
// still matches, otherwise the first match.
func (m *Model) searchTarget() int {
	visible := m.visibleCategoryIndices()
	for _, index := range visible {
		if index == m.selected {
			return index
		}
	}
	if len(visible) > 0 {
		return visible[0]
	}
	return m.selected
}

func (m *Model) ensureVisibleSelection() {
	visible := m.visibleCategoryIndices()
	if len(visible) == 0 {
		return
	}
	for _, index := range visible {
		if index == m.selected {
			return
		}
	}
	m.selected = visible[0]
}

// railPosition orders pages inside a rail group. Pages appear in definition
// order, except that Language Servers sits directly after Code Intelligence,
// the page whose behavior it configures, and Agents stands where the Main
// Agent role is defined.
func railPosition(index int) int {
	switch index {
	case catLanguageServers:
		return catLSP*2 + 1
	case catTerminal:
		// Terminal follows Interface, the other window-chrome page.
		return catUI*2 + 1
	case catAgents:
		// The Agents catalog takes the Main Agent role's place.
		return catMain * 2
	}
	return index * 2
}

// networkGroupName is the rail group for application-owned transport policy.
// It is always ordered last; see visibleCategoryIndices.
const networkGroupName = "Network"

const (
	catAccounts = iota
	catMain
	catResearcher
	catCrafter
	catAeron
	catMemory
	catNetworkConnection
	catNetworkProxy
	catNetworkCertificates
	catSafety
	catAutoReviewPolicy
	catAppearance
	catEditor
	catLSP
	catUI
	catHistory
	catAgents
	catSkills
	catMCP
	catSources
	catAcquisition
	catLanguageServers
	catTerminal
)

func (m *Model) currentOverrideKey() string {
	switch m.selected {
	case catAppearance:
		return "appearance"
	case catEditor:
		return "editor"
	case catLSP, catLanguageServers:
		return "lsp"
	case catUI:
		return "interface"
	case catMemory:
		return "memory"
	case catNetworkConnection, catNetworkProxy, catNetworkCertificates:
		return ""
	case catSafety, catAutoReviewPolicy:
		return "safety"
	case catHistory:
		return "local_history"
	case catTerminal:
		return "terminal"
	case catSkills, catMCP, catSources, catAcquisition:
		return "capabilities"
	default:
		return "agents"
	}
}

// AppSettings returns the values held by the shared form models.
func (m *Model) AppSettings() types.AppSettings {
	var settings types.AppSettings
	settings.ApplicationRevision, settings.WorkspaceRevision = m.applicationRevision, m.workspaceRevision
	m.collectAccounts(&settings)
	m.collectAgents(&settings.Agents)
	m.collectMemory(&settings.Memory)
	m.collectNetwork(&settings.Network)
	m.collectSafety(&settings.Safety)
	m.collectHistory(&settings.LocalHistory)
	m.collectAppearance(&settings.Theme)
	m.collectEditor(&settings.Editor)
	m.collectLSP(&settings.LSP)
	m.collectUI(&settings.UI)
	m.collectTerminal(&settings.Terminal)
	settings.Capabilities = capability.Clone(m.capabilities)
	settings.Capabilities.Acquisition = m.acquisitionFromForm()
	return settings
}

func (m *Model) collectHistory(settings *types.LocalHistorySettings) {
	patterns := strings.FieldsFunc(m.value(catHistory, fieldHistoryIgnores), func(r rune) bool { return r == '\n' || r == '\r' })
	for index := range patterns {
		patterns[index] = strings.TrimSpace(patterns[index])
	}
	*settings = types.LocalHistorySettings{
		Enabled:                     onOff(m.value(catHistory, fieldHistoryEnabled) == "on"),
		UseGitIgnore:                onOff(m.value(catHistory, fieldHistoryGitIgnore) == "on"),
		CaptureExternalChanges:      onOff(m.value(catHistory, fieldHistoryExternal) == "on"),
		SafetySnapshotBeforeRestore: "on",
		MaxFileSizeMB:               parseOpt(m.value(catHistory, fieldHistoryMaxFile), 100),
		DebounceMS:                  parseOpt(m.value(catHistory, fieldHistoryDebounce), 750),
		ReconcileIntervalSeconds:    parseOpt(m.value(catHistory, fieldHistoryReconcile), 60),
		RetentionKeepHours:          parseOpt(m.value(catHistory, fieldHistoryKeepHours), 24),
		RetentionKeepDaily:          parseOpt(m.value(catHistory, fieldHistoryKeepDaily), 30),
		RetentionMaxSnapshots:       parseOpt(m.value(catHistory, fieldHistoryMaxSnaps), 500),
		StorageBudgetMB:             parseOpt(m.value(catHistory, fieldHistoryBudget), 1024),
		ExtraIgnorePatterns:         patterns,
	}
}

func (m *Model) collectMemory(cfg *types.MemorySettings) {
	*cfg = types.MemorySettings{
		AutoRecall:         onOff(m.value(catMemory, "auto-recall") == "on"),
		AutoExtract:        onOff(m.value(catMemory, "auto-extract") == "on"),
		Consolidation:      onOff(m.value(catMemory, "consolidation") == "on"),
		RecallBudgetTokens: parseOpt(m.value(catMemory, "recall-budget"), types.DefaultMemoryRecallBudget),
		RecallLimit:        parseOpt(m.value(catMemory, "recall-limit"), types.DefaultMemoryRecallLimit),
		ExtractEveryTurns:  parseOpt(m.value(catMemory, "extract-every"), types.DefaultMemoryExtractCadence),
		MemoryCategory:     m.memoryAgent,
	}
}

func (m *Model) collectSafety(settings *types.SafetySettings) {
	*settings = types.SafetySettings{
		ConfirmAt:           m.value(catSafety, fieldSafetyConfirmAt),
		UnknownCommandRisk:  m.value(catSafety, fieldSafetyUnknown),
		ScriptExecutionRisk: m.value(catSafety, fieldSafetyScript),
		CommandRules:        parseCommandRules(m.value(catSafety, fieldSafetyRules)),
		NotifyDangerous:     onOff(m.value(catSafety, fieldSafetyNotify) == "on"),
	}
	auto := autoPermissionFromForm(m.autoReviewer, m.categories[catAutoReviewPolicy].Form)
	m.autoReviewer = auto
	settings.AutoPermission = auto
}

func onOff(enabled bool) string {
	if enabled {
		return "on"
	}
	return "off"
}

func (m *Model) collectAccounts(settings *types.AppSettings) {
	m.storeAccountForm()
	settings.Accounts = cloneAccounts(m.accounts)
}

func (m *Model) storeAccountForm() { m.storeAccountEditorForm() }

// protocolSettingsFromForm folds the protocol option rows back into persisted
// settings. A form left entirely on defaults stores nil — protocol defaults,
// not a frozen copy of them. Fields the protocol does not expose never render,
// so their form lookups read empty and stay unset.
func (m *Model) protocolSettingsFromForm(category int) *provider.ProtocolSettings {
	settings := provider.ProtocolSettings{
		Store:               m.triStateAt(category, fieldProtocolStore),
		MediaReplayRecovery: m.triStateAt(category, fieldProtocolMediaReplay),
		ZeroDataRetention:   m.toggleAt(category, fieldProtocolZDR),
		ReasoningSummaries:  m.triStateAt(category, fieldProtocolSummaries),
		Truncation:          m.truncationAt(category, fieldProtocolTruncate),
		ServiceTier:         m.value(category, fieldProtocolTier),
	}
	if settings == (provider.ProtocolSettings{}) {
		return nil
	}
	return &settings
}

// triStateAt reads an optional-boolean row; nil means the protocol default.
func (m *Model) triStateAt(category int, fieldID string) *bool {
	return ui.TriStateFieldValue(m.fieldAt(category, fieldID))
}

// toggleAt reads a yes/no row as a plain bool.
func (m *Model) toggleAt(category int, fieldID string) bool {
	return ui.ToggleFieldValue(m.fieldAt(category, fieldID))
}

// truncationAt reads the context-overflow row; "truncate" persists true.
func (m *Model) truncationAt(category int, fieldID string) *bool {
	if m.value(category, fieldID) == "truncate" {
		on := true
		return &on
	}
	return nil
}

// fieldAt returns a form field, tolerating a missing form.
func (m *Model) fieldAt(category int, fieldID string) *ui.FieldState {
	if category < 0 || category >= len(m.categories) || m.categories[category].Form == nil {
		return nil
	}
	return m.categories[category].Form.Field(fieldID)
}

// extraBodyFromForm parses the extra-body JSON field; blank is nil and
// invalid JSON is nil (the field validator blocks saving invalid input).
func extraBodyFromForm(value string) map[string]any {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(trimmed), &decoded); err != nil {
		return nil
	}
	return decoded
}

func (m *Model) collectAgents(agents *types.AgentsSettings) {
	*agents = *m.agents
	agents.AgentProfiles = types.CloneAgentProfiles(m.agents.AgentProfiles)
	agents.Categories = make(map[string]types.CategorySettings)
	for _, entry := range []struct {
		index int
		key   string
	}{{catMain, "default"}, {catResearcher, "researcher"}, {catCrafter, "crafter"}, {catAeron, "aeron"}} {
		category := m.agentCategoryFromForm(m.categories[entry.index].Form, m.agents.Categories[entry.key])
		agents.Categories[entry.key] = category
	}
	if field := m.categories[catMain].Form.Field(fieldMultiAgent); field != nil {
		agents.Enabled = ui.BoolFieldValue(field)
	}
}

func (m *Model) agentCategoryFromForm(form *ui.FormModel, category types.CategorySettings) types.CategorySettings {
	value := func(id string) string {
		if field := form.Field(id); field != nil {
			return field.Value.String()
		}
		return ""
	}
	previousModel := category.Model
	category.AccountID, category.Model, category.ThinkingEffort = value(fieldConnection), value(fieldModel), value(fieldThinkingEffort)
	category.Provider = m.accounts[category.AccountID].ProviderID
	category.Description, category.Instructions = value("agent-description"), value("agent-instructions")
	// Public catalog facts are not edits to the user's settings. Keep existing
	// explicit values, but never carry old-model facts across a model change.
	if previousModel != category.Model {
		category.ModelInputPrice, category.ModelOutputPrice, category.ModelCachePrice = 0, 0, 0
		category.ModelSupportsTools = false
	}
	category.ModelContextWindow = parsePositiveInt(value(fieldCtxWindow))
	category.ModelMaxOutput = parsePositiveInt(value(fieldMaxOutput))
	if field := form.Field(fieldCtxWindow); field != nil && field.Inherited {
		category.ModelContextWindow = 0
	}
	if field := form.Field(fieldMaxOutput); field != nil && field.Inherited {
		category.ModelMaxOutput = 0
	}
	category.ModelModalities = nil
	explicitModalities := false
	for _, modality := range provider.KnownModalities() {
		if field := form.Field(fieldModalityPrefix + modality); field != nil && !field.Inherited {
			explicitModalities = true
		}
	}
	if explicitModalities {
		category.ModelModalities = []string{}
		for _, modality := range provider.KnownModalities() {
			if value(fieldModalityPrefix+modality) == "yes" {
				category.ModelModalities = append(category.ModelModalities, modality)
			}
		}
	}
	category.ModelTemperature = parseFloatPtr(value(fieldTemperature))
	category.ModelTopP = parseFloatPtr(value(fieldTopP))
	category.ContextCompactionPercent = parsePercentPtr(value(fieldContextCompaction))
	category.ContextRetentionTokens = parseIntPtr(value(fieldContextRetention))
	return category
}

// parsePositiveInt parses an optional integer field; blank or invalid is 0.
func parsePositiveInt(value string) int {
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

// parseFloatPtr parses an optional float field; blank or invalid is nil.
func parseFloatPtr(value string) *float64 {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	parsed, err := strconv.ParseFloat(trimmed, 64)
	if err != nil {
		return nil
	}
	return &parsed
}

func parsePercentPtr(value string) *int {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return nil
	}
	parsed = min(100, max(0, parsed))
	return &parsed
}

func parseIntPtr(value string) *int {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return nil
	}
	return &parsed
}

// framePadFieldState renders one shell-frame padding input. Values are whole
// numbers of cells including zero; blank means "inherit the default" (the
// initial value shows the effective pad so the row reads as a plain number).
func framePadFieldState(id, label string, value int, description string) *ui.FieldState {
	f := ui.TextFieldState(id, label, strconv.Itoa(value), description)
	f.Validator = func(v ui.FieldValue) []string {
		trimmed := strings.TrimSpace(v.String())
		if trimmed == "" {
			return nil
		}
		if n, err := strconv.Atoi(trimmed); err != nil || n < 0 {
			return []string{"Must be a whole number of cells (0 or more)."}
		}
		return nil
	}
	return f
}

// liveFramePadField is a shell-frame padding row wired for live preview:
// every keystroke emits form.changed so the shell re-insets immediately.
func liveFramePadField(id, label, unit string, value int, description string) *ui.FieldState {
	f := framePadFieldState(id, label, value, description)
	f.Live, f.Unit = true, unit
	return f
}

// framePadField parses one shell-frame padding row. A blank or invalid field
// returns nil so the leaf stays unset and inherits the theme default.
func framePadField(value string) *int {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	n, err := strconv.Atoi(trimmed)
	if err != nil || n < 0 {
		return nil
	}
	return &n
}

// framePreview reports whether a changed field is one of the live shell-frame
// padding rows and returns the complete frame for the preview message. The
// message is SAVE-FAITHFUL: it mirrors exactly what collectAppearance would
// persist, so what the user previews is what a save produces. The settings
// model resolves a blank field the same way collectAppearance does (a zero
// pointer), which applyFramePreview maps to the configured default.
func (m *Model) framePreview(fieldID string) (FramePreviewMsg, bool) {
	if m == nil || m.SelectedCategory() != catAppearance {
		return FramePreviewMsg{}, false
	}
	switch fieldID {
	case "frame-top", "frame-bottom", "frame-left", "frame-right":
		return FramePreviewMsg{
			Top:    framePadField(m.value(catAppearance, "frame-top")),
			Bottom: framePadField(m.value(catAppearance, "frame-bottom")),
			Left:   framePadField(m.value(catAppearance, "frame-left")),
			Right:  framePadField(m.value(catAppearance, "frame-right")),
		}, true
	}
	return FramePreviewMsg{}, false
}

func (m *Model) collectAppearance(cfg *types.ThemeSettings) {
	// Colour rows are read-only preset reflections; the preset name and the
	// shell frame paddings persist.
	*cfg = types.ThemeSettings{
		Preset:    m.value(catAppearance, "preset"),
		PadTop:    framePadField(m.value(catAppearance, "frame-top")),
		PadBottom: framePadField(m.value(catAppearance, "frame-bottom")),
		PadLeft:   framePadField(m.value(catAppearance, "frame-left")),
		PadRight:  framePadField(m.value(catAppearance, "frame-right")),
	}
}

func (m *Model) collectEditor(cfg *types.EditorSettings) {
	*cfg = types.EditorSettings{TabSize: parseOpt(m.value(catEditor, "tab-size"), 4), WordWrap: m.value(catEditor, "word-wrap") == "on", LineNumbers: m.value(catEditor, "line-numbers") == "on", SyntaxTheme: m.value(catEditor, "syntax-theme"), RenderWhitespace: m.value(catEditor, "render-whitespace"), AutoSave: types.AutoSaveMode(m.value(catEditor, "auto-save")), AutoSaveDelay: parseOpt(m.value(catEditor, "auto-save-delay"), 1000), FormatOnSave: m.value(catEditor, "format-on-save") == "on"}
}

func (m *Model) collectUI(cfg *types.UISettings) {
	*cfg = types.UISettings{SidebarWidth: parseOpt(m.value(catUI, "sidebar-width"), 30), ShowHiddenFiles: m.value(catUI, "show-hidden") == "on", ShowIcons: m.value(catUI, "show-icons") == "on", UseNerdFont: m.value(catUI, "nerd-font") == "on", ShowGitStatus: m.value(catUI, "git-status") == "on", ShowDiagnostics: m.value(catUI, "diagnostics") == "on", ContextHelp: defaultString(m.value(catUI, "context-help"), "compact")}
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

// Snapshot records values without retaining component interaction state.
func (m *Model) Snapshot() {
	snapshot := &modelSnapshot{
		categories: make([]categorySnapshot, len(m.categories)),
		selected:   m.selected, pane: m.pane,
		accountID: m.accountID, accounts: cloneAccounts(m.accounts),
		languages: cloneLSPLanguages(m.lspLanguages),
	}
	for index, category := range m.categories {
		values := make(map[string]ui.FieldValue, category.Form.FieldCount())
		for _, field := range category.Form.Fields() {
			values[field.ID] = field.Value
		}
		snapshot.categories[index] = categorySnapshot{values: values}
	}
	m.snapshot = snapshot
}

func (m *Model) Restore() {
	if m.snapshot == nil {
		return
	}
	for index, snapshot := range m.snapshot.categories {
		if index >= len(m.categories) {
			break
		}
		for fieldID, value := range snapshot.values {
			field := m.categories[index].Form.Field(fieldID)
			if field != nil {
				field.Value, field.Dirty = value, false
				field.Validate()
			}
		}
	}
	m.selected, m.pane = m.snapshot.selected, m.snapshot.pane
	m.accountID, m.accounts = m.snapshot.accountID, cloneAccounts(m.snapshot.accounts)
	m.lspLanguages = cloneLSPLanguages(m.snapshot.languages)
	m.syncLSPSummary()
	m.accountEditor = nil
	m.accountError = ""
	m.rebuildAccountsForm()
}
