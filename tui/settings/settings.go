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
type SaveMsg struct{ AppSettings types.AppSettings }

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
}

const (
	fieldProvider       = "provider"
	fieldModel          = "model"
	fieldAPIKey         = "api-key"
	fieldBaseURL        = "base-url"
	fieldThinkingEffort = "thinking-effort"
)

// New creates a workspace settings model from current AppSettings.
func New(th *theme.Theme, current types.AppSettings, presets []types.ProviderPreset) *Model {
	appearance := current.Theme
	if appearance.Preset == "" {
		appearance.Preset = "dark"
	}
	editor := current.Editor
	if editor.TabSize == 0 {
		editor.TabSize = 4
	}
	if editor.SyntaxTheme == "" {
		editor.SyntaxTheme = "catppuccin"
	}
	if editor.RenderWhitespace == "" {
		editor.RenderWhitespace = "none"
	}
	lsp := current.LSP
	if !lsp.Enabled && !lsp.AutoDetect {
		lsp.Enabled, lsp.AutoDetect, lsp.Diagnostics = true, true, true
	}
	uiSettings := current.UI
	if uiSettings.SidebarWidth == 0 {
		uiSettings.SidebarWidth = 30
	}
	font := current.Font
	if font.Size == 0 {
		font.Size = 12
	}
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
	m := &Model{th: th, presets: presets, providers: providers, providerID: active, models: make(map[string][]types.ModelEntry)}
	m.categories = []Category{
		{Name: "Providers", Group: "AI", Description: "Connections, credentials, and provider defaults.", Form: m.buildProvidersForm()},
		{Name: "Main Agent", Group: "AI", Description: "Workspace defaults for planning and delegation.", Form: m.buildAgentForm(current.Agents.Categories["default"])},
		{Name: "Researcher", Group: "AI", Description: "Workspace defaults for research and context gathering.", Form: m.buildAgentForm(current.Agents.Categories["researcher"])},
		{Name: "Crafter", Group: "AI", Description: "Workspace defaults for implementation work.", Form: m.buildAgentForm(current.Agents.Categories["crafter"])},
		{Name: "Aeron", Group: "AI", Description: "Workspace defaults for mechanical tasks.", Form: m.buildAgentForm(current.Agents.Categories["aeron"])},
		{Name: "Appearance", Group: "IDE", Description: "Theme palette and surface colors.", Form: formModel(
			append([]*ui.FieldState{
				cycleField("preset", "preset", append([]string(nil), theme.AllPresetNames...), appearance.Preset),
			}, appearanceColorFields(appearance.Preset, appearance)...)...,
		)},
		{Name: "Editor", Group: "IDE", Description: "Editing, wrapping, syntax, and save behavior.", Form: formModel(
			textField("tab-size", "tab size", fmt.Sprintf("%d", editor.TabSize), false), cycleField("word-wrap", "word wrap", []string{"on", "off"}, boolStr(editor.WordWrap)),
			cycleField("line-numbers", "line numbers", []string{"on", "off"}, boolStr(editor.LineNumbers)), cycleField("syntax-theme", "syntax theme", []string{"catppuccin", "dracula", "github", "monokai", "solarized"}, editor.SyntaxTheme),
			cycleField("render-whitespace", "render whitespace", []string{"none", "boundary", "all"}, editor.RenderWhitespace), textField("auto-save-delay", "auto-save (ms)", fmt.Sprintf("%d", editor.AutoSaveDelay), false),
		)},
		{Name: "Language Servers", Group: "IDE", Description: "Language intelligence and diagnostics.", Form: formModel(
			cycleField("enabled", "enabled", []string{"on", "off"}, boolStr(lsp.Enabled)), cycleField("auto-detect", "auto-detect", []string{"on", "off"}, boolStr(lsp.AutoDetect)), cycleField("diagnostics", "diagnostics", []string{"on", "off"}, boolStr(lsp.Diagnostics)),
		)},
		{Name: "Interface", Group: "IDE", Description: "File tree, sidebar, icons, and diagnostics.", Form: formModel(
			textField("sidebar-width", "sidebar width", fmt.Sprintf("%d", uiSettings.SidebarWidth), false), cycleField("show-hidden", "show hidden", []string{"on", "off"}, boolStr(uiSettings.ShowHiddenFiles)),
			cycleField("show-icons", "show icons", []string{"on", "off"}, boolStr(uiSettings.ShowIcons)), cycleField("nerd-font", "nerd font", []string{"on", "off"}, boolStr(uiSettings.UseNerdFont)),
			cycleField("git-status", "git status", []string{"on", "off"}, boolStr(uiSettings.ShowGitStatus)), cycleField("diagnostics", "diagnostics", []string{"on", "off"}, boolStr(uiSettings.ShowDiagnostics)),
		)},
		{Name: "Font", Group: "IDE", Description: "Terminal font hints and ligatures.", Form: formModel(
			textField("family", "family", font.Family, false), textField("size", "size", fmt.Sprintf("%d", font.Size), false), cycleField("ligatures", "ligatures", []string{"on", "off"}, boolStr(font.Ligatures)),
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

func textField(id, label, value string, password bool) *ui.FieldState {
	kind := ui.FieldText
	if password {
		kind = ui.FieldPassword
	}
	v := ui.StringValue(value)
	return &ui.FieldState{ID: id, Label: label, Type: kind, Value: v, Initial: v}
}

func cycleField(id, label string, values []string, current string) *ui.FieldState {
	options := make([]ui.FormOption, len(values))
	for i, value := range values {
		options[i] = ui.FormOption{ID: value, Label: value}
	}
	v := ui.StringValue(current)
	return &ui.FieldState{ID: id, Label: label, Type: ui.FieldCycle, Value: v, Initial: v, Options: options}
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
		readOnlyColorField("background", "background", resolve(overrides.Bg, c.Bg)),
		readOnlyColorField("foreground", "foreground", resolve(overrides.Fg, c.Fg)),
		readOnlyColorField("accent", "accent", resolve(overrides.Accent, c.Accent)),
		readOnlyColorField("accent2", "accent2", resolve(overrides.Accent2, c.Accent2)),
		readOnlyColorField("subpane-bg", "subpane bg", resolve(overrides.SubPaneBg, c.SubPaneBg)),
		readOnlyColorField("border", "border", resolve(overrides.Border, c.Border)),
		readOnlyColorField("selection", "selection", resolve(overrides.Selection, c.Selection)),
	}
}

func readOnlyColorField(id, label, hex string) *ui.FieldState {
	v := ui.StringValue(hex)
	return &ui.FieldState{
		ID: id, Label: label, Type: ui.FieldText,
		Value: v, Initial: v, ReadOnly: true,
	}
}

func selectField(id, label, value string, options []ui.FormOption) *ui.FieldState {
	v := ui.StringValue(value)
	return &ui.FieldState{ID: id, Label: label, Type: ui.FieldSelect, Value: v, Initial: v, Options: options}
}

func (m *Model) buildProvidersForm() *ui.FormModel {
	cfg := m.providers[m.providerID]
	baseURL := cfg.ProviderURL
	if baseURL == "" {
		if preset := provider.GetPreset(m.providerID); preset != nil {
			baseURL = preset.BaseURL
		}
	}
	return formModel(selectField(fieldProvider, "provider", m.providerID, m.providerOptions()), selectField(fieldModel, "default model", cfg.Model, modelOptions(m.models[m.providerID])), textField(fieldAPIKey, "api key", cfg.APIKey, true), textField(fieldBaseURL, "base url", baseURL, false), cycleField(fieldThinkingEffort, "thinking level", []string{"none", "low", "medium", "high", "xhigh"}, cfg.ThinkingEffort))
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
	return formModel(selectField(fieldProvider, "provider", providerID, m.providerOptions()), selectField(fieldModel, "model", model, modelOptions(m.models[providerID])), cycleField(fieldThinkingEffort, "thinking level", []string{"none", "low", "medium", "high", "xhigh"}, thinking))
}

func (m *Model) providerOptions() []ui.FormOption {
	options := make([]ui.FormOption, 0, len(m.presets))
	for _, preset := range m.presets {
		options = append(options, ui.FormOption{ID: preset.ID, Label: preset.Name, Detail: preset.HelpText})
	}
	return options
}

func modelOptions(models []types.ModelEntry) []ui.FormOption {
	options := make([]ui.FormOption, 0, len(models))
	for _, model := range models {
		label := model.Name
		if label == "" {
			label = model.ID
		}
		detail := ""
		if model.ContextWindow > 0 {
			detail = rendering.FormatTokens(model.ContextWindow)
		}
		options = append(options, ui.FormOption{ID: model.ID, Label: label, Detail: detail})
	}
	return options
}

func boolStr(value bool) string {
	if value {
		return "on"
	}
	return "off"
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
			field.Options = modelOptions(m.models[providerID])
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
			field.Options = modelOptions(models)
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

func (m *Model) isAgentCategory(index int) bool { return index >= catMain && index <= catAeron }

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
		field.Options = modelOptions(m.models[providerID])
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
	*cfg = types.EditorSettings{TabSize: parseOpt(m.value(catEditor, "tab-size"), 4), WordWrap: m.value(catEditor, "word-wrap") == "on", LineNumbers: m.value(catEditor, "line-numbers") == "on", SyntaxTheme: m.value(catEditor, "syntax-theme"), RenderWhitespace: m.value(catEditor, "render-whitespace"), AutoSaveDelay: parseOpt(m.value(catEditor, "auto-save-delay"), 0)}
}

func (m *Model) collectLSP(cfg *types.LSPSettings) {
	*cfg = types.LSPSettings{Enabled: m.value(catLSP, "enabled") == "on", AutoDetect: m.value(catLSP, "auto-detect") == "on", Diagnostics: m.value(catLSP, "diagnostics") == "on"}
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
