package settings

import (
	"fmt"
	"image/color"
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/provider"
	"crude/tui/rendering"
	"crude/tui/theme"
	"crude/tui/types"
	"crude/tui/ui"
)

// SaveMsg is returned as a tea.Msg on save.
type SaveMsg struct {
	Scope       types.SettingsScope
	AppSettings types.AppSettings
	Overrides   types.WorkspaceOverrides
}

// CancelMsg is returned as a tea.Msg on cancel.
type CancelMsg struct{}

// ThemePreviewMsg applies a theme preset immediately while Settings stays open.
type ThemePreviewMsg struct {
	Preset string
}

// FetchModelsMsg asks the workspace layer to load models for a provider.
type FetchModelsMsg struct {
	ProviderID string
	BaseURL    string
	APIKey     string
}

// ModelsMsg and ModelsErrorMsg deliver an asynchronous model-list result.
type ModelsMsg struct {
	ProviderID string
	Models     []types.ModelEntry
}

type ModelsErrorMsg struct {
	ProviderID string
	Error      string
}

// ResetSectionMsg clears the workspace override for one settings page.
type ResetSectionMsg struct{ Key string }

// ScopeMsg switches the settings dialog between application and workspace.
type ScopeMsg struct{ Scope types.SettingsScope }

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
	providerID string
}

// Model is the workspace settings screen state.
type Model struct {
	th           *theme.Theme
	categories   []Category
	selected     int
	pane         int // 0: category rail, 1: form
	snapshot     *modelSnapshot
	presets      []types.ProviderPreset
	providers    map[string]types.ProviderConfig
	providerID   string
	models       map[string][]types.ModelEntry
	searchEd     *ui.TextEdit
	searchActive bool
	scope        types.SettingsScope
	appDefaults  types.AppSettings
}

const (
	fieldProvider       = "provider"
	fieldModel          = "model"
	fieldAPIKey         = "api-key"
	fieldBaseURL        = "base-url"
	fieldThinkingEffort = "thinking-effort"
)

// New creates a settings model from current AppSettings (application scope).
func New(th *theme.Theme, current types.AppSettings, presets []types.ProviderPreset) *Model {
	return NewScoped(th, current, presets, types.SettingsScopeApplication, current, types.WorkspaceOverrides{})
}

// NewScoped creates a settings model for application or workspace scope.
func NewScoped(th *theme.Theme, current types.AppSettings, presets []types.ProviderPreset, scope types.SettingsScope, appDefaults types.AppSettings, _ types.WorkspaceOverrides) *Model {
	types.ApplyAppDefaults(&current)
	appearance := current.Theme
	editor := current.Editor
	lsp := current.LSP
	uiSettings := current.UI
	font := current.Font
	if len(presets) == 0 {
		for _, preset := range provider.PresetList() {
			presets = append(presets, types.ProviderPreset{ID: preset.ID, Name: preset.Name, HelpText: preset.HelpText, BaseURL: preset.BaseURL})
		}
	}
	providers := make(map[string]types.ProviderConfig, len(current.Providers))
	for id, cfg := range current.Providers {
		providers[id] = cfg
	}
	active := current.ActiveProvider
	if active == "" {
		active = "openrouter"
	}
	if _, ok := providers[active]; !ok {
		providers[active] = types.ProviderConfig{}
	}
	m := &Model{th: th, presets: presets, providers: providers, providerID: active, models: make(map[string][]types.ModelEntry), scope: scope, appDefaults: appDefaults}
	appearanceFields := append([]*ui.FieldState{
		ui.SeparatorFieldState("theme", "Theme"),
		ui.CycleFieldState("preset", "Preset", append([]string(nil), theme.AllPresetNames...), appearance.Preset, "Choose a complete visual palette for the workspace."),
		ui.SeparatorFieldState("palette", "Palette preview"),
	}, appearanceColorFields(appearance.Preset, appearance)...)
	m.categories = []Category{
		{Name: "Providers", Group: "AI", Description: "Connections, credentials, and provider defaults.", Form: m.buildProvidersForm()},
		{Name: "Main Agent", Group: "AI", Description: "Workspace defaults for planning and delegation.", Form: m.buildAgentForm(current.Agents.Categories["default"])},
		{Name: "Researcher", Group: "AI", Description: "Workspace defaults for research and context gathering.", Form: m.buildAgentForm(current.Agents.Categories["researcher"])},
		{Name: "Crafter", Group: "AI", Description: "Workspace defaults for implementation work.", Form: m.buildAgentForm(current.Agents.Categories["crafter"])},
		{Name: "Aeron", Group: "AI", Description: "Workspace defaults for mechanical tasks.", Form: m.buildAgentForm(current.Agents.Categories["aeron"])},
		{Name: "Appearance", Group: "IDE", Description: "Theme palette and surface colors.", Form: formModel(appearanceFields...)},
		{Name: "Editor", Group: "IDE", Description: "Editing, wrapping, syntax, and save behavior.", Form: formModel(
			ui.SeparatorFieldState("editing", "Editing"),
			ui.TextFieldState("tab-size", "Tab size", fmt.Sprintf("%d", editor.TabSize), "Number of spaces inserted when a tab is used."),
			ui.BoolFieldState("word-wrap", "Word wrap", editor.WordWrap, "Wrap long lines inside the editor viewport."),
			ui.BoolFieldState("line-numbers", "Line numbers", editor.LineNumbers, "Show the document line-number gutter."),
			ui.CycleFieldState("syntax-theme", "Syntax theme", []string{"catppuccin", "dracula", "github", "monokai", "solarized"}, editor.SyntaxTheme, "Color grammar used for source code."),
			ui.CycleFieldState("render-whitespace", "Render whitespace", []string{"none", "boundary", "all"}, editor.RenderWhitespace, "Show spaces and tabs when inspecting layout."),
			ui.TextFieldState("auto-save-delay", "Auto-save delay", fmt.Sprintf("%d", editor.AutoSaveDelay), "Delay in milliseconds; zero disables automatic saves."),
			ui.BoolFieldState("format-on-save", "Format on save", editor.FormatOnSave, "Run the language server formatter when a file is saved."),
		)},
		{Name: "Language Servers", Group: "IDE", Description: "Language intelligence and diagnostics.", Form: m.buildLSPForm(lsp)},
		{Name: "Interface", Group: "IDE", Description: "File tree, sidebar, icons, and diagnostics.", Form: formModel(
			ui.SeparatorFieldState("workspace-chrome", "Workspace chrome"),
			ui.TextFieldState("sidebar-width", "Sidebar width", fmt.Sprintf("%d", uiSettings.SidebarWidth), "Width of the file and workspace navigation rail."),
			ui.BoolFieldState("show-hidden", "Show hidden files", uiSettings.ShowHiddenFiles, "Include dotfiles in the workspace tree."),
			ui.BoolFieldState("show-icons", "Show file icons", uiSettings.ShowIcons, "Display file-type icons in navigation."),
			ui.BoolFieldState("nerd-font", "Nerd font", uiSettings.UseNerdFont, "Use extended icon glyphs when the font supports them."),
			ui.BoolFieldState("git-status", "Git status", uiSettings.ShowGitStatus, "Show changed-file markers in the workspace tree."),
			ui.BoolFieldState("diagnostics", "Diagnostics", uiSettings.ShowDiagnostics, "Show language diagnostics in navigation."),
		)},
		{Name: "Font", Group: "IDE", Description: "Terminal font hints and ligatures.", Form: formModel(
			ui.SeparatorFieldState("typography", "Typography"),
			ui.TextFieldState("family", "Family", font.Family, "Font family is controlled by the host terminal."),
			ui.TextFieldState("size", "Size", fmt.Sprintf("%d", font.Size), "Preferred terminal font size in points."),
			ui.BoolFieldState("ligatures", "Ligatures", font.Ligatures, "Allow programming ligatures when supported by the terminal."),
		)},
	}
	for index := range m.categories {
		category := index
		m.categories[index].Form.OnChanged(func(fieldID string) { m.handleFieldChanged(category, fieldID) })
	}
	return m
}

func formModel(fields ...*ui.FieldState) *ui.FormModel {
	m := ui.NewFormModel()
	for _, field := range fields {
		m.AddField(field)
	}
	return m
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

func (m *Model) buildProvidersForm() *ui.FormModel {
	cfg := m.providers[m.providerID]
	baseURL := cfg.ProviderURL
	if baseURL == "" {
		if preset := provider.GetPreset(m.providerID); preset != nil {
			baseURL = preset.BaseURL
		}
	}
	return formModel(
		ui.SeparatorFieldState("connection", "Connection"),
		ui.SelectFieldState(fieldProvider, "Provider", m.providerID, ui.ProviderOptions(m.presets), "The service used for model requests."),
		ui.PasswordFieldState(fieldAPIKey, "API key", cfg.APIKey, "Stored locally and masked while editing."),
		ui.TextFieldState(fieldBaseURL, "Base URL", baseURL, "Override the provider endpoint when using a proxy or local gateway."),
		ui.SeparatorFieldState("defaults", "Model defaults"),
		ui.ModelFieldState(fieldModel, "Default model", cfg.Model, ui.ModelOptions(m.models[m.providerID]), "Used when an agent does not define its own model."),
		ui.ThinkingFieldState(fieldThinkingEffort, cfg.ThinkingEffort, "Controls how much internal reasoning the model may use."),
	)
}

func (m *Model) buildAgentForm(cfg types.CategorySettings) *ui.FormModel {
	providerID := cfg.Provider
	if providerID == "" {
		providerID = m.providerID
	}
	providerCfg := m.providers[providerID]
	model := cfg.Model
	if model == "" {
		model = providerCfg.Model
	}
	thinking := cfg.ThinkingEffort
	if thinking == "" {
		thinking = providerCfg.ThinkingEffort
	}
	return formModel(
		ui.SeparatorFieldState("agent", "Agent behavior"),
		ui.SelectFieldState(fieldProvider, "Provider", providerID, ui.ProviderOptions(m.presets), "Override the workspace provider for this agent."),
		ui.ModelFieldState(fieldModel, "Model", model, ui.ModelOptions(m.models[providerID]), "Override the workspace default model."),
		ui.ThinkingFieldState(fieldThinkingEffort, thinking, "Override the workspace reasoning budget."),
	)
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

func (m *Model) isAgentCategory(index int) bool {
	return index == catMain || index == catResearcher || index == catCrafter || index == catAeron
}

func (m *Model) handleFieldChanged(category int, fieldID string) {
	if category == catAppearance && fieldID == "preset" {
		m.syncAppearanceColors()
		return
	}
	if category == catProviders && fieldID == fieldProvider {
		providerID := m.value(catProviders, fieldProvider)
		if providerID != "" && providerID != m.providerID {
			m.storeProviderForm(m.providerID)
			m.loadProviderForm(providerID)
		}
		return
	}
	if m.isAgentCategory(category) && fieldID == fieldProvider {
		providerID := m.value(category, fieldProvider)
		cfg := m.providers[providerID]
		form := m.categories[category].Form
		form.SetValue(fieldModel, ui.StringValue(cfg.Model))
		form.SetValue(fieldThinkingEffort, ui.StringValue(cfg.ThinkingEffort))
		if field := form.Field(fieldModel); field != nil {
			field.Options = ui.ModelOptions(m.models[providerID])
			field.Errors, field.Loading, field.LoadMessage = nil, false, ""
		}
	}
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
	case ModelsMsg:
		m.setModels(msg.ProviderID, msg.Models)
	case ModelsErrorMsg:
		m.setModelsError(msg.ProviderID, msg.Error)
	}
	return *m, nil
}

func (m *Model) OpenSelect(fieldID string) tea.Cmd {
	if fieldID != fieldModel || m.currentForm() == nil {
		return nil
	}
	field := m.currentForm().Field(fieldModel)
	if field == nil || len(field.Options) > 0 {
		return nil
	}
	field.Loading, field.LoadMessage = true, "Fetching models..."
	if m.selected == catProviders {
		return m.fetchModelsCmd()
	}
	if m.isAgentCategory(m.selected) {
		return m.fetchModelsForProviderCmd(m.value(m.selected, fieldProvider))
	}
	return nil
}

func (m *Model) fetchModelsCmd() tea.Cmd {
	return func() tea.Msg {
		return FetchModelsMsg{ProviderID: m.providerID, BaseURL: m.value(catProviders, fieldBaseURL), APIKey: m.value(catProviders, fieldAPIKey)}
	}
}

func (m *Model) fetchModelsForProviderCmd(providerID string) tea.Cmd {
	cfg := m.providers[providerID]
	baseURL := cfg.ProviderURL
	if baseURL == "" {
		if preset := provider.GetPreset(providerID); preset != nil {
			baseURL = preset.BaseURL
		}
	}
	return func() tea.Msg { return FetchModelsMsg{ProviderID: providerID, BaseURL: baseURL, APIKey: cfg.APIKey} }
}

func (m *Model) setModels(providerID string, models []types.ModelEntry) {
	m.models[providerID] = models
	for index := range m.categories {
		if index == catProviders && m.providerID != providerID {
			continue
		}
		if m.isAgentCategory(index) && m.value(index, fieldProvider) != providerID {
			continue
		}
		if field := m.categories[index].Form.Field(fieldModel); field != nil {
			field.Options = ui.ModelOptions(models)
			field.Errors, field.Loading, field.LoadMessage = nil, false, ""
		}
	}
}

func (m *Model) setModelsError(providerID, message string) {
	for index := range m.categories {
		if (index == catProviders && m.providerID == providerID) || (m.isAgentCategory(index) && m.value(index, fieldProvider) == providerID) {
			if field := m.categories[index].Form.Field(fieldModel); field != nil {
				field.Errors, field.Loading, field.LoadMessage = []string{message}, false, ""
			}
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
	m.ensureVisibleSelection()
}

func (m *Model) SetSearchActive(active bool) { m.searchActive = active }
func (m *Model) SelectedCategory() int       { return m.selected }
func (m *Model) Pane() int                   { return m.pane }
func (m *Model) Scope() types.SettingsScope {
	if m == nil || m.scope == "" {
		return types.SettingsScopeApplication
	}
	return m.scope
}
func (m *Model) SetScope(scope types.SettingsScope) { m.scope = scope }
func (m *Model) Overrides() types.WorkspaceOverrides {
	if m.Scope() != types.SettingsScopeWorkspace {
		return types.WorkspaceOverrides{}
	}
	return types.WorkspaceOverridesFromDiff(m.appDefaults, m.AppSettings())
}

func (m *Model) SelectCategory(index int) {
	if index >= 0 && index < len(m.categories) {
		m.selected, m.pane = index, 0
	}
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
		category := m.categories[index]
		if category.Group != lastGroup {
			if lastGroup != "" {
				items = append(items, ui.ListItem{ID: "settings/gap/" + strings.ToLower(category.Group), Spacer: true})
			}
			items = append(items, ui.ListItem{ID: "settings/group/" + strings.ToLower(category.Group), Label: category.Group, Header: true})
			lastGroup = category.Group
		}
		items = append(items, ui.ListItem{ID: fmt.Sprintf("settings/category/%d", index), Label: category.Name, Payload: index})
	}
	return items
}

func (m *Model) visibleCategoryIndices() []int {
	query := strings.ToLower(strings.TrimSpace(m.searchText()))
	indices := make([]int, 0, len(m.categories))
	for index, category := range m.categories {
		if query == "" || strings.Contains(strings.ToLower(category.Name), query) || strings.Contains(strings.ToLower(category.Group), query) {
			indices = append(indices, index)
		}
	}
	return indices
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

const (
	catProviders = iota
	catMain
	catResearcher
	catCrafter
	catAeron
	catAppearance
	catEditor
	catLSP
	catUI
	catFont
)

func (m *Model) currentOverrideKey() string {
	switch m.selected {
	case catAppearance:
		return "appearance"
	case catEditor:
		return "editor"
	case catLSP:
		return "lsp"
	case catUI:
		return "interface"
	case catFont:
		return "font"
	case catProviders:
		return "providers"
	default:
		return "agents"
	}
}

// AppSettings returns the values held by the shared form models.
func (m *Model) AppSettings() types.AppSettings {
	var settings types.AppSettings
	m.collectProviders(&settings)
	m.collectAgents(&settings.Agents)
	m.collectAppearance(&settings.Theme)
	m.collectEditor(&settings.Editor)
	m.collectLSP(&settings.LSP)
	m.collectUI(&settings.UI)
	m.collectFont(&settings.Font)
	return settings
}

func (m *Model) collectProviders(settings *types.AppSettings) {
	m.storeProviderForm(m.providerID)
	settings.ActiveProvider = m.providerID
	settings.Providers = make(map[string]types.ProviderConfig, len(m.providers))
	for id, cfg := range m.providers {
		settings.Providers[id] = cfg
	}
}

func (m *Model) storeProviderForm(providerID string) {
	if providerID == "" || len(m.categories) == 0 {
		return
	}
	m.providers[providerID] = types.ProviderConfig{Model: m.value(catProviders, fieldModel), APIKey: m.value(catProviders, fieldAPIKey), ProviderURL: m.value(catProviders, fieldBaseURL), ThinkingEffort: m.value(catProviders, fieldThinkingEffort)}
}

func (m *Model) loadProviderForm(providerID string) {
	if providerID == "" {
		return
	}
	m.providerID = providerID
	cfg := m.providers[providerID]
	baseURL := cfg.ProviderURL
	if baseURL == "" {
		if preset := provider.GetPreset(providerID); preset != nil {
			baseURL = preset.BaseURL
		}
	}
	form := m.categories[catProviders].Form
	form.SetValue(fieldProvider, ui.StringValue(providerID))
	form.SetValue(fieldModel, ui.StringValue(cfg.Model))
	form.SetValue(fieldAPIKey, ui.StringValue(cfg.APIKey))
	form.SetValue(fieldBaseURL, ui.StringValue(baseURL))
	form.SetValue(fieldThinkingEffort, ui.StringValue(cfg.ThinkingEffort))
	if field := form.Field(fieldModel); field != nil {
		field.Options = ui.ModelOptions(m.models[providerID])
		field.Errors, field.Loading, field.LoadMessage = nil, false, ""
	}
}

func (m *Model) collectAgents(agents *types.AgentsSettings) {
	if agents.Categories == nil {
		agents.Categories = make(map[string]types.CategorySettings)
	}
	for _, entry := range []struct {
		index int
		key   string
	}{{catMain, "default"}, {catResearcher, "researcher"}, {catCrafter, "crafter"}, {catAeron, "aeron"}} {
		category := types.CategorySettings{Provider: m.value(entry.index, fieldProvider), Model: m.value(entry.index, fieldModel), ThinkingEffort: m.value(entry.index, fieldThinkingEffort)}
		for _, model := range m.models[category.Provider] {
			if model.ID == category.Model {
				category.ModelContextWindow, category.ModelInputPrice, category.ModelOutputPrice, category.ModelCachePrice = model.ContextWindow, model.InputPrice, model.OutputPrice, model.CacheReadPrice
				category.ModelSupportsVision, category.ModelSupportsTools, category.ModelMaxOutput, category.ModelAttachment = model.SupportsVision, model.SupportsTools, model.MaxOutput, model.Attachment
				break
			}
		}
		agents.Categories[entry.key] = category
	}
}

func (m *Model) collectAppearance(cfg *types.ThemeSettings) {
	// Colour rows are read-only preset reflections; only the preset name persists.
	*cfg = types.ThemeSettings{Preset: m.value(catAppearance, "preset")}
}

func (m *Model) collectEditor(cfg *types.EditorSettings) {
	*cfg = types.EditorSettings{TabSize: parseOpt(m.value(catEditor, "tab-size"), 4), WordWrap: m.value(catEditor, "word-wrap") == "on", LineNumbers: m.value(catEditor, "line-numbers") == "on", SyntaxTheme: m.value(catEditor, "syntax-theme"), RenderWhitespace: m.value(catEditor, "render-whitespace"), AutoSaveDelay: parseOpt(m.value(catEditor, "auto-save-delay"), 0), FormatOnSave: m.value(catEditor, "format-on-save") == "on"}
}

func (m *Model) collectLSP(cfg *types.LSPSettings) {
	lsp := types.NormalizeLSP(types.LSPSettings{
		Enabled:          m.value(catLSP, "enabled") == "on",
		AutoDetect:       m.value(catLSP, "auto-detect") == "on",
		StartOnOpen:      m.value(catLSP, "start-on-open") == "on",
		Diagnostics:      m.value(catLSP, "diagnostics") == "on",
		Completions:      m.value(catLSP, "completions") == "on",
		Hover:            m.value(catLSP, "hover") == "on",
		Definition:       m.value(catLSP, "goto-definition") == "on",
		References:       m.value(catLSP, "find-references") == "on",
		Outline:          m.value(catLSP, "outline") == "on",
		Rename:           m.value(catLSP, "rename") == "on",
		DiagInEditor:     m.value(catLSP, "diag-editor") == "on",
		DiagInTree:       m.value(catLSP, "diag-tree") == "on",
		DiagInStatus:     m.value(catLSP, "diag-status") == "on",
		AutoComplete:     m.value(catLSP, "auto-complete") == "on",
		MinSeverity:      m.value(catLSP, "min-severity"),
		UpdateDebounceMS: parseOpt(m.value(catLSP, "debounce"), 300),
		Languages:        make(map[string]types.LSPLanguageSettings),
	})
	for _, lang := range types.DefaultLSPLanguages() {
		enabled := m.value(catLSP, "lang-"+lang.ID+"-enabled") == "on"
		cmd := m.value(catLSP, "lang-"+lang.ID+"-cmd")
		args := splitArgs(m.value(catLSP, "lang-"+lang.ID+"-args"))
		if cmd == "" {
			cmd = lang.DefaultCmd
		}
		if len(args) == 0 {
			args = append([]string(nil), lang.DefaultArgs...)
		}
		lsp.Languages[lang.ID] = types.LSPLanguageSettings{Enabled: enabled, Command: cmd, Args: args}
	}
	*cfg = lsp
}

func splitArgs(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return strings.Fields(s)
}

func (m *Model) buildLSPForm(lsp types.LSPSettings) *ui.FormModel {
	lsp = types.NormalizeLSP(lsp)
	fields := []*ui.FieldState{
		ui.SeparatorFieldState("language-intelligence", "Language intelligence"),
		ui.BoolFieldState("enabled", "Enabled", lsp.Enabled, "Master switch for language servers in this scope."),
		ui.BoolFieldState("auto-detect", "Auto-detect", lsp.AutoDetect, "Start a server from files present in the project."),
		ui.BoolFieldState("start-on-open", "Start on open", lsp.StartOnOpen, "Launch detected servers when the workspace opens."),
		ui.TextFieldState("debounce", "Update delay", fmt.Sprintf("%d", lsp.UpdateDebounceMS), "Milliseconds to wait after an edit before notifying the server."),
		ui.SeparatorFieldState("features", "Features"),
		ui.BoolFieldState("diagnostics", "Diagnostics", lsp.Diagnostics, "Publish errors, warnings, and hints from the server."),
		ui.BoolFieldState("completions", "Completions", lsp.Completions, "Offer completion items at the cursor."),
		ui.BoolFieldState("auto-complete", "Auto-complete", lsp.AutoComplete, "Trigger completions while typing."),
		ui.BoolFieldState("hover", "Hover", lsp.Hover, "Show symbol documentation under the cursor."),
		ui.BoolFieldState("goto-definition", "Go to definition", lsp.Definition, "Jump to the symbol definition."),
		ui.BoolFieldState("find-references", "Find references", lsp.References, "List every reference to the symbol."),
		ui.BoolFieldState("outline", "Document outline", lsp.Outline, "Build the file symbol outline."),
		ui.BoolFieldState("rename", "Rename", lsp.Rename, "Rename the symbol across the project."),
		ui.SeparatorFieldState("diagnostic-display", "Diagnostic display"),
		ui.BoolFieldState("diag-editor", "In editor", lsp.DiagInEditor, "Underline issues in the open buffer."),
		ui.BoolFieldState("diag-tree", "In file tree", lsp.DiagInTree, "Show issue counts on files in the sidebar."),
		ui.BoolFieldState("diag-status", "In status bar", lsp.DiagInStatus, "Show project-wide error and warning totals."),
		ui.CycleFieldState("min-severity", "Minimum severity", []string{"error", "warning", "info", "hint"}, lsp.MinSeverity, "Hide diagnostics weaker than this level."),
		ui.SeparatorFieldState("servers", "Language servers"),
	}
	for _, lang := range types.DefaultLSPLanguages() {
		cfg := lsp.Languages[lang.ID]
		if cfg.Command == "" {
			cfg.Command = lang.DefaultCmd
			cfg.Args = append([]string(nil), lang.DefaultArgs...)
			cfg.Enabled = true
		}
		fields = append(fields,
			ui.BoolFieldState("lang-"+lang.ID+"-enabled", lang.Name, cfg.Enabled, "Start this server when matching files are present."),
			ui.TextFieldState("lang-"+lang.ID+"-cmd", lang.ID+" command", cfg.Command, "Executable used for "+lang.Name+"."),
			ui.TextFieldState("lang-"+lang.ID+"-args", lang.ID+" args", strings.Join(cfg.Args, " "), "Command arguments, space-separated."),
		)
	}
	return formModel(fields...)
}

func (m *Model) collectUI(cfg *types.UISettings) {
	*cfg = types.UISettings{SidebarWidth: parseOpt(m.value(catUI, "sidebar-width"), 30), ShowHiddenFiles: m.value(catUI, "show-hidden") == "on", ShowIcons: m.value(catUI, "show-icons") == "on", UseNerdFont: m.value(catUI, "nerd-font") == "on", ShowGitStatus: m.value(catUI, "git-status") == "on", ShowDiagnostics: m.value(catUI, "diagnostics") == "on"}
}

func (m *Model) collectFont(cfg *types.FontSettings) {
	*cfg = types.FontSettings{Family: m.value(catFont, "family"), Size: parseOpt(m.value(catFont, "size"), 12), Ligatures: m.value(catFont, "ligatures") == "on"}
}

// Snapshot records values without retaining component interaction state.
func (m *Model) Snapshot() {
	snapshot := &modelSnapshot{categories: make([]categorySnapshot, len(m.categories)), selected: m.selected, pane: m.pane, providerID: m.providerID}
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
	m.selected, m.pane, m.providerID = m.snapshot.selected, m.snapshot.pane, m.snapshot.providerID
}
