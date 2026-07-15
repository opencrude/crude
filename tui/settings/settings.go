package settings

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/provider"
	"crude/tui/components"
	"crude/tui/form"
	"crude/tui/rendering"
	"crude/tui/theme"
	"crude/tui/types"
)

// SaveMsg is returned as a tea.Msg on save.
type SaveMsg struct {
	AppSettings types.AppSettings
}

// CancelMsg is returned as a tea.Msg on cancel.
type CancelMsg struct{}

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

// Category defines one settings section in the left panel.
type Category struct {
	Name string
	Icon string
	Form *form.Form
}

// Model is the workspace settings window state.
type Model struct {
	th         *theme.Theme
	categories []Category
	selected   int
	pane       int // 0: category rail, 1: form
	snapshot   *modelSnapshot
	lastLayout settingsLayout
	presets    []types.ProviderPreset
	providers  map[string]types.ProviderConfig
	providerID string
	models     map[string][]types.ModelEntry
}

type modelSnapshot struct {
	categories []Category
	selected   int
	pane       int
}

type settingsLayout struct {
	width, height      int
	leftW              int
	categoryY          int
	formX, formY       int
	saveX0, saveX1     int
	cancelX0, cancelX1 int
}

// New creates a workspace settings model from current AppSettings.
// presets is the known provider list (from provider.Presets); may be nil.
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
		lsp.Enabled = true
		lsp.AutoDetect = true
		lsp.Diagnostics = true
	}

	ui := current.UI
	if ui.SidebarWidth == 0 {
		ui.SidebarWidth = 30
	}

	font := current.Font
	if font.Size == 0 {
		font.Size = 12
	}

	if len(presets) == 0 {
		for _, p := range provider.Presets {
			presets = append(presets, types.ProviderPreset{
				ID: p.ID, Name: p.Name, HelpText: p.HelpText, BaseURL: p.BaseURL,
			})
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

	m := &Model{
		th: th, presets: presets, providers: providers, providerID: active,
		models: make(map[string][]types.ModelEntry),
	}
	m.categories = []Category{
		{Name: "Providers", Form: m.buildProvidersForm()},
		{Name: "Main Agent", Form: m.buildAgentForm("Main Agent", current.Agents.Categories["default"])},
		{Name: "Researcher", Form: m.buildAgentForm("Researcher", current.Agents.Categories["researcher"])},
		{Name: "Crafter", Form: m.buildAgentForm("Crafter", current.Agents.Categories["crafter"])},
		{Name: "Aeron", Form: m.buildAgentForm("Aeron", current.Agents.Categories["aeron"])},
		{Name: "Appearance", Form: m.buildAppearanceForm(appearance)},
		{Name: "Editor", Form: m.buildEditorForm(editor)},
		{Name: "Language Servers", Form: m.buildLSPForm(lsp)},
		{Name: "Interface", Form: m.buildUIForm(ui)},
		{Name: "Font", Form: m.buildFontForm(font)},
	}
	return m
}

func (m *Model) buildAgentForm(title string, cfg types.CategorySettings) *form.Form {
	providerID := cfg.Provider
	if providerID == "" {
		providerID = m.providerID
	}
	providerItem := form.NewSelectItem(m.th, "provider", providerID, 10)
	options := make([]components.ListItem, 0, len(m.presets))
	for _, preset := range m.presets {
		options = append(options, components.ListItem{ID: preset.ID, Label: preset.Name, Detail: preset.HelpText})
	}
	providerItem.SetOptions(options)

	providerCfg := m.providers[providerID]
	model := cfg.Model
	if model == "" {
		model = providerCfg.Model
	}
	thinking := cfg.ThinkingEffort
	if thinking == "" {
		thinking = providerCfg.ThinkingEffort
	}
	modelItem := form.NewSelectItem(m.th, "model", model, 12)
	modelItem.SetOptions(modelListItems(m.models[providerID]))

	f := form.NewForm(m.th, title,
		"workspace default \u00B7 tab next \u00B7 enter choose",
		providerItem,
		modelItem,
		form.NewCycleItem(m.th, "thinking level", []string{"none", "low", "medium", "high", "xhigh"}, thinking),
	)
	return &f
}

func boolStr(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

// formatCategoryLabel places any icon then a single space then the name.
// Icon cell width comes from rendering.PlainWidth (same tables as UV).
// Keep icons; do not special-case glyphs here — width correctness lives in
// the shared ansi/rendering width path.
func formatCategoryLabel(icon, name string) string {
	if icon == "" {
		return name
	}
	return icon + " " + name
}

func parseOpt(s string, defaultVal int) int {
	if s == "" {
		return defaultVal
	}
	var n int
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil {
		return defaultVal
	}
	return n
}

func (m *Model) buildProvidersForm() *form.Form {
	providerItem := form.NewSelectItem(m.th, "provider", m.providerID, 10)
	providerOptions := make([]components.ListItem, 0, len(m.presets))
	for _, p := range m.presets {
		providerOptions = append(providerOptions, components.ListItem{
			ID: p.ID, Label: p.Name, Detail: p.HelpText,
		})
	}
	providerItem.SetOptions(providerOptions)

	cfg := m.providers[m.providerID]
	baseURL := cfg.ProviderURL
	if baseURL == "" {
		if p := provider.GetPreset(m.providerID); p != nil {
			baseURL = p.BaseURL
		}
	}
	modelItem := form.NewSelectItem(m.th, "default model", cfg.Model, 12)
	modelItem.SetOptions(modelListItems(m.models[m.providerID]))

	f := form.NewForm(m.th, "Providers",
		"tab next \u00B7 enter choose/edit \u00B7 esc categories",
		providerItem,
		modelItem,
		form.NewTextItem(m.th, "api key", cfg.APIKey, true),
		form.NewTextItem(m.th, "base url", baseURL, false),
		form.NewCycleItem(m.th, "thinking level", []string{"none", "low", "medium", "high", "xhigh"}, cfg.ThinkingEffort),
	)
	return &f
}

func modelListItems(models []types.ModelEntry) []components.ListItem {
	items := make([]components.ListItem, 0, len(models))
	for _, model := range models {
		name := model.Name
		if name == "" {
			name = model.ID
		}
		detail := ""
		if model.ContextWindow > 0 {
			detail = rendering.FormatTokens(model.ContextWindow)
		}
		items = append(items, components.ListItem{ID: model.ID, Label: name, Detail: detail})
	}
	return items
}

func (m *Model) buildAppearanceForm(cfg types.ThemeSettings) *form.Form {
	f := form.NewForm(m.th, "Appearance",
		"tab next \u00B7 enter edit \u00B7 \u2190\u2192 change",
		form.NewCycleItem(m.th, "preset", []string{"dark", "light", "custom"}, cfg.Preset),
		form.NewTextItem(m.th, "background", cfg.Bg, false),
		form.NewTextItem(m.th, "foreground", cfg.Fg, false),
		form.NewTextItem(m.th, "accent", cfg.Accent, false),
		form.NewTextItem(m.th, "accent2", cfg.Accent2, false),
		form.NewTextItem(m.th, "subpane bg", cfg.SubPaneBg, false),
		form.NewTextItem(m.th, "border", cfg.Border, false),
		form.NewTextItem(m.th, "selection", cfg.Selection, false),
	)
	return &f
}

func (m *Model) buildEditorForm(cfg types.EditorSettings) *form.Form {
	f := form.NewForm(m.th, "Editor",
		"tab next \u00B7 enter edit \u00B7 \u2190\u2192 change",
		form.NewTextItem(m.th, "tab size", fmt.Sprintf("%d", cfg.TabSize), false),
		form.NewCycleItem(m.th, "word wrap", []string{"on", "off"}, boolStr(cfg.WordWrap)),
		form.NewCycleItem(m.th, "line numbers", []string{"on", "off"}, boolStr(cfg.LineNumbers)),
		form.NewCycleItem(m.th, "syntax theme", []string{"catppuccin", "dracula", "github", "monokai", "solarized"}, cfg.SyntaxTheme),
		form.NewCycleItem(m.th, "render whitespace", []string{"none", "boundary", "all"}, cfg.RenderWhitespace),
		form.NewTextItem(m.th, "auto-save (ms)", fmt.Sprintf("%d", cfg.AutoSaveDelay), false),
	)
	return &f
}

func (m *Model) buildLSPForm(cfg types.LSPSettings) *form.Form {
	f := form.NewForm(m.th, "Language Servers",
		"tab next \u00B7 enter edit \u00B7 \u2190\u2192 change",
		form.NewCycleItem(m.th, "enabled", []string{"on", "off"}, boolStr(cfg.Enabled)),
		form.NewCycleItem(m.th, "auto-detect", []string{"on", "off"}, boolStr(cfg.AutoDetect)),
		form.NewCycleItem(m.th, "diagnostics", []string{"on", "off"}, boolStr(cfg.Diagnostics)),
	)
	return &f
}

func (m *Model) buildUIForm(cfg types.UISettings) *form.Form {
	f := form.NewForm(m.th, "Interface",
		"tab next \u00B7 enter edit \u00B7 \u2190\u2192 change",
		form.NewTextItem(m.th, "sidebar width", fmt.Sprintf("%d", cfg.SidebarWidth), false),
		form.NewCycleItem(m.th, "show hidden", []string{"on", "off"}, boolStr(cfg.ShowHiddenFiles)),
		form.NewCycleItem(m.th, "show icons", []string{"on", "off"}, boolStr(cfg.ShowIcons)),
		form.NewCycleItem(m.th, "nerd font", []string{"on", "off"}, boolStr(cfg.UseNerdFont)),
		form.NewCycleItem(m.th, "git status", []string{"on", "off"}, boolStr(cfg.ShowGitStatus)),
		form.NewCycleItem(m.th, "diagnostics", []string{"on", "off"}, boolStr(cfg.ShowDiagnostics)),
	)
	return &f
}

func (m *Model) buildFontForm(cfg types.FontSettings) *form.Form {
	f := form.NewForm(m.th, "Font",
		"tab next \u00B7 enter edit \u00B7 \u2190\u2192 change",
		form.NewTextItem(m.th, "family", cfg.Family, false),
		form.NewTextItem(m.th, "size", fmt.Sprintf("%d", cfg.Size), false),
		form.NewCycleItem(m.th, "ligatures", []string{"on", "off"}, boolStr(cfg.Ligatures)),
	)
	return &f
}

func (m *Model) currentForm() *form.Form {
	return m.categories[m.selected].Form
}

func (m *Model) setForm(f form.Form) {
	m.categories[m.selected].Form = &f
}

// Update handles keyboard and mouse input. Returns updated model and optional command.
func (m *Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	case tea.MouseClickMsg:
		return m.handleMouseClick(msg)
	case tea.MouseMotionMsg:
		m.handleMouseMotion(msg)
		return *m, nil
	case tea.MouseWheelMsg:
		f := m.currentForm()
		updated, cmd, action := f.Update(msg)
		return *m, m.processFormUpdate(m.formProviderBefore(), updated, cmd, action)
	case ModelsMsg:
		m.setModels(msg.ProviderID, msg.Models)
		return *m, nil
	case ModelsErrorMsg:
		if (m.selected == catProviders && m.providerID == msg.ProviderID) ||
			(m.isAgentCategory() && m.currentForm().ItemValue(0) == msg.ProviderID) {
			m.currentForm().SetItemLoading(1, false, "")
			m.currentForm().SetItemFetchError(1, msg.Error)
		}
		return *m, nil
	}
	// Route non-key/mouse events to the form (e.g. PasteMsg)
	f := m.currentForm()
	oldProvider := m.formProviderBefore()
	updated, cmd, action := f.Update(msg)
	return *m, m.processFormUpdate(oldProvider, updated, cmd, action)
}

func (m *Model) handleKey(keyMsg tea.KeyPressMsg) (Model, tea.Cmd) {
	f := m.currentForm()
	if f.IsEditing() || f.IsExpanded() {
		oldProvider := m.formProviderBefore()
		updated, cmd, action := f.Update(keyMsg)
		return *m, m.processFormUpdate(oldProvider, updated, cmd, action)
	}

	s := keyMsg.String()
	if s == "ctrl+s" || s == "s" {
		return *m, m.saveCmd()
	}
	if s == "c" && m.pane == 0 {
		return *m, m.cancelCmd()
	}

	if m.pane == 0 {
		if s == "j" {
			keyMsg = tea.KeyPressMsg{Code: tea.KeyDown}
		} else if s == "k" {
			keyMsg = tea.KeyPressMsg{Code: tea.KeyUp}
		}
		switch keyMsg.Code {
		case tea.KeyEsc:
			return *m, m.cancelCmd()
		case tea.KeyUp:
			if m.selected > 0 {
				m.selected--
			}
		case tea.KeyDown:
			if m.selected < len(m.categories)-1 {
				m.selected++
			}
		case tea.KeyRight, tea.KeyEnter, tea.KeyTab:
			m.pane = 1
		}
		return *m, nil
	}

	if keyMsg.Code == tea.KeyEsc {
		m.pane = 0
		return *m, nil
	}
	oldProvider := m.formProviderBefore()
	updated, cmd, action := f.Update(keyMsg)
	return *m, m.processFormUpdate(oldProvider, updated, cmd, action)
}

func (m *Model) processFormUpdate(oldProvider string, updated form.Form, cmd tea.Cmd, action form.FormAction) tea.Cmd {
	m.setForm(updated)
	if m.selected == catProviders {
		newProvider := updated.ItemValue(0)
		if newProvider != "" && newProvider != oldProvider {
			m.storeProviderForm(oldProvider)
			m.loadProviderForm(newProvider)
		}
		if action == form.FormActionExpand && updated.ExpandedIndex() == 1 && m.currentForm().ItemOptionCount(1) == 0 {
			m.currentForm().SetItemLoading(1, true, "fetching models...")
			request := m.fetchModelsCmd()
			if cmd != nil {
				return tea.Batch(cmd, request)
			}
			return request
		}
	} else if m.isAgentCategory() {
		newProvider := updated.ItemValue(0)
		if newProvider != "" && newProvider != oldProvider {
			providerCfg := m.providers[newProvider]
			m.currentForm().SetItemValue(1, providerCfg.Model)
			m.currentForm().SetItemValue(2, providerCfg.ThinkingEffort)
			m.currentForm().SetItemSelectOptions(1, modelListItems(m.models[newProvider]))
		}
		if action == form.FormActionExpand && updated.ExpandedIndex() == 1 && m.currentForm().ItemOptionCount(1) == 0 {
			m.currentForm().SetItemLoading(1, true, "fetching models...")
			request := m.fetchModelsForProviderCmd(m.currentForm().ItemValue(0))
			if cmd != nil {
				return tea.Batch(cmd, request)
			}
			return request
		}
	}
	if action == form.FormActionBack {
		m.pane = 0
		return nil
	}
	return cmd
}

func (m *Model) formProviderBefore() string {
	if m.selected >= catProviders && m.selected <= catAeron {
		return m.currentForm().ItemValue(0)
	}
	return ""
}

func (m *Model) isAgentCategory() bool {
	return m.selected >= catMain && m.selected <= catAeron
}

func (m *Model) saveCmd() tea.Cmd {
	m.storeProviderForm(m.providerID)
	return func() tea.Msg {
		return &SaveMsg{AppSettings: m.AppSettings()}
	}
}

func (m *Model) cancelCmd() tea.Cmd {
	return func() tea.Msg {
		return &CancelMsg{}
	}
}

func (m *Model) handleMouseClick(msg tea.MouseClickMsg) (Model, tea.Cmd) {
	if msg.Button != tea.MouseLeft {
		return *m, nil
	}
	x, y := msg.Mouse().X, msg.Mouse().Y

	layout := m.lastLayout
	if y == layout.height-1 {
		if x >= layout.saveX0 && x <= layout.saveX1 {
			return *m, m.saveCmd()
		}
		if x >= layout.cancelX0 && x <= layout.cancelX1 {
			return *m, m.cancelCmd()
		}
	}

	if x >= 0 && x < layout.leftW {
		idx := y - layout.categoryY
		if idx >= 0 && idx < len(m.categories) {
			m.selected = idx
			m.pane = 0
			return *m, nil
		}
	}

	if x >= layout.formX && y >= layout.formY && y < layout.height-3 {
		m.pane = 1
		f := m.currentForm()
		oldProvider := m.formProviderBefore()
		local := tea.MouseClickMsg{X: x - layout.formX, Y: y - layout.formY, Button: msg.Button}
		updated, cmd, action := f.Update(local)
		return *m, m.processFormUpdate(oldProvider, updated, cmd, action)
	}

	return *m, nil
}

func (m *Model) handleMouseMotion(msg tea.MouseMotionMsg) {
}

// View renders the two-column settings window. Returns lines.
func (m *Model) View(width, height int) []string {
	if width < 42 {
		width = 42
	}
	if height < 12 {
		height = 12
	}
	leftW := 18
	for _, cat := range m.categories {
		lw := 5 + rendering.VisibleWidth(cat.Name)
		if lw > leftW {
			leftW = lw
		}
	}
	if leftW > width/3 {
		leftW = width / 3
	}
	rightW := width - leftW - 1
	formX := leftW + 3
	formW := rightW - 4
	f := m.currentForm()
	formView := f.View(formW, 0)
	formLines := strings.Split(formView, "\n")
	railStyle := lipgloss.NewStyle().Background(m.th.Colors.SubPaneBg).Foreground(m.th.Colors.Muted)
	bodyStyle := lipgloss.NewStyle().Background(m.th.Colors.PopupBg).Foreground(m.th.Colors.Fg)
	selectedStyle := lipgloss.NewStyle().Background(m.th.Colors.PopupSubtleBg).Foreground(m.th.Colors.Fg).Bold(true)
	accentStyle := lipgloss.NewStyle().Background(m.th.Colors.PopupSubtleBg).Foreground(m.th.Colors.PopupTitleFocused).Bold(true)
	sep := lipgloss.NewStyle().Background(m.th.Colors.PopupBg).Foreground(m.th.Colors.PopupDivider).Render("│")
	fit := func(s string, w int, style lipgloss.Style) string {
		if w < 1 {
			return ""
		}
		s = ansi.Truncate(s, w, "")
		if pad := w - rendering.VisibleWidth(s); pad > 0 {
			s += strings.Repeat(" ", pad)
		}
		return style.Render(s)
	}

	categoryY := 1
	footerDividerY := height - 3
	hintY := height - 2
	buttonY := height - 1
	result := make([]string, height)
	for y := 0; y < height; y++ {
		leftText := ""
		idx := y - categoryY
		leftStyle := railStyle
		if idx >= 0 && idx < len(m.categories) {
			leftText = fmt.Sprintf(" %02d  %s", idx+1, m.categories[idx].Name)
			if idx == m.selected {
				leftText = " ▌" + leftText[2:]
				if m.pane == 0 {
					leftStyle = accentStyle
				} else {
					leftStyle = selectedStyle
				}
			}
		}
		rightText := ""
		if y < footerDividerY && y < len(formLines) {
			rightText = "  " + formLines[y]
		}
		if y == footerDividerY {
			rightText = strings.Repeat("─", rightW)
		}
		if y == hintY {
			hint := "Tab next  ·  Enter choose/edit  ·  Esc categories"
			if m.pane == 0 {
				hint = "↑↓ category  ·  Enter open  ·  Esc close"
			}
			rightText = "  " + hint
		}
		if y == buttonY {
			save := " Save changes "
			cancel := " Cancel "
			rightText = "  " + lipgloss.NewStyle().Background(m.th.Colors.PopupButtonBg).Foreground(m.th.Colors.PopupButtonFg).Bold(true).Render(save) + "  " + lipgloss.NewStyle().Foreground(m.th.Colors.Muted).Background(m.th.Colors.PopupBg).Render(cancel)
		}
		result[y] = fit(leftText, leftW, leftStyle) + sep + fit(rightText, rightW, bodyStyle)
	}

	saveX0 := leftW + 1 + 2
	saveX1 := saveX0 + rendering.VisibleWidth(" Save changes ") - 1
	cancelX0 := saveX1 + 3
	m.lastLayout = settingsLayout{
		width: width, height: height, leftW: leftW, categoryY: categoryY,
		formX: formX, formY: 0,
		saveX0: saveX0, saveX1: saveX1,
		cancelX0: cancelX0, cancelX1: cancelX0 + rendering.VisibleWidth(" Cancel ") - 1,
	}
	return result
}

// category indices after Providers was added as index 0.
const (
	catProviders  = 0
	catMain       = 1
	catResearcher = 2
	catCrafter    = 3
	catAeron      = 4
	catAppearance = 5
	catEditor     = 6
	catLSP        = 7
	catUI         = 8
	catFont       = 9
)

// AppSettings returns the current values as an AppSettings struct.
func (m *Model) AppSettings() types.AppSettings {
	var s types.AppSettings
	m.collectProviders(&s)
	m.collectAgents(&s.Agents)
	m.collectAppearance(&s.Theme)
	m.collectEditor(&s.Editor)
	m.collectLSP(&s.LSP)
	m.collectUI(&s.UI)
	m.collectFont(&s.Font)
	return s
}

func (m *Model) collectAgents(agents *types.AgentsSettings) {
	if agents.Categories == nil {
		agents.Categories = make(map[string]types.CategorySettings)
	}
	for _, entry := range []struct {
		index int
		key   string
	}{
		{catMain, "default"},
		{catResearcher, "researcher"},
		{catCrafter, "crafter"},
		{catAeron, "aeron"},
	} {
		f := m.categories[entry.index].Form
		category := types.CategorySettings{
			Provider: f.ItemValue(0), Model: f.ItemValue(1), ThinkingEffort: f.ItemValue(2),
		}
		for _, model := range m.models[category.Provider] {
			if model.ID == category.Model {
				category.ModelContextWindow = model.ContextWindow
				category.ModelInputPrice = model.InputPrice
				category.ModelOutputPrice = model.OutputPrice
				category.ModelCachePrice = model.CacheReadPrice
				category.ModelSupportsVision = model.SupportsVision
				category.ModelSupportsTools = model.SupportsTools
				category.ModelMaxOutput = model.MaxOutput
				category.ModelAttachment = model.Attachment
				break
			}
		}
		agents.Categories[entry.key] = category
	}
}

func (m *Model) collectProviders(s *types.AppSettings) {
	m.storeProviderForm(m.providerID)
	s.ActiveProvider = m.providerID
	s.Providers = make(map[string]types.ProviderConfig, len(m.providers))
	for id, cfg := range m.providers {
		s.Providers[id] = cfg
	}
}

func (m *Model) storeProviderForm(providerID string) {
	if providerID == "" || len(m.categories) == 0 {
		return
	}
	f := m.categories[catProviders].Form
	m.providers[providerID] = types.ProviderConfig{
		Model:          f.ItemValue(1),
		APIKey:         f.ItemValue(2),
		ProviderURL:    f.ItemValue(3),
		ThinkingEffort: f.ItemValue(4),
	}
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
	f := m.categories[catProviders].Form
	f.SetItemValue(0, providerID)
	f.SetItemValue(1, cfg.Model)
	f.SetItemValue(2, cfg.APIKey)
	f.SetItemValue(3, baseURL)
	f.SetItemValue(4, cfg.ThinkingEffort)
	f.SetItemSelectOptions(1, modelListItems(m.models[providerID]))
}

func (m *Model) fetchModelsCmd() tea.Cmd {
	f := m.categories[catProviders].Form
	providerID := m.providerID
	baseURL := f.ItemValue(3)
	apiKey := f.ItemValue(2)
	return func() tea.Msg {
		return FetchModelsMsg{ProviderID: providerID, BaseURL: baseURL, APIKey: apiKey}
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
	return func() tea.Msg {
		return FetchModelsMsg{ProviderID: providerID, BaseURL: baseURL, APIKey: cfg.APIKey}
	}
}

func (m *Model) setModels(providerID string, models []types.ModelEntry) {
	m.models[providerID] = models
	if m.providerID == providerID {
		f := m.categories[catProviders].Form
		f.SetItemSelectOptions(1, modelListItems(models))
		f.SetItemLoading(1, false, "")
	}
	for _, idx := range []int{catMain, catResearcher, catCrafter, catAeron} {
		f := m.categories[idx].Form
		if f.ItemValue(0) == providerID {
			f.SetItemSelectOptions(1, modelListItems(models))
			f.SetItemLoading(1, false, "")
		}
	}
}

func (m *Model) collectAppearance(cfg *types.ThemeSettings) {
	*cfg = types.ThemeSettings{
		Preset:    m.categories[catAppearance].Form.ItemValue(0),
		Bg:        m.categories[catAppearance].Form.ItemValue(1),
		Fg:        m.categories[catAppearance].Form.ItemValue(2),
		Accent:    m.categories[catAppearance].Form.ItemValue(3),
		Accent2:   m.categories[catAppearance].Form.ItemValue(4),
		SubPaneBg: m.categories[catAppearance].Form.ItemValue(5),
		Border:    m.categories[catAppearance].Form.ItemValue(6),
		Selection: m.categories[catAppearance].Form.ItemValue(7),
	}
}

func (m *Model) collectEditor(cfg *types.EditorSettings) {
	*cfg = types.EditorSettings{
		TabSize:          parseOpt(m.categories[catEditor].Form.ItemValue(0), 4),
		WordWrap:         m.categories[catEditor].Form.ItemValue(1) == "on",
		LineNumbers:      m.categories[catEditor].Form.ItemValue(2) == "on",
		SyntaxTheme:      m.categories[catEditor].Form.ItemValue(3),
		RenderWhitespace: m.categories[catEditor].Form.ItemValue(4),
		AutoSaveDelay:    parseOpt(m.categories[catEditor].Form.ItemValue(5), 0),
	}
}

func (m *Model) collectLSP(cfg *types.LSPSettings) {
	*cfg = types.LSPSettings{
		Enabled:     m.categories[catLSP].Form.ItemValue(0) == "on",
		AutoDetect:  m.categories[catLSP].Form.ItemValue(1) == "on",
		Diagnostics: m.categories[catLSP].Form.ItemValue(2) == "on",
	}
}

func (m *Model) collectUI(cfg *types.UISettings) {
	*cfg = types.UISettings{
		SidebarWidth:    parseOpt(m.categories[catUI].Form.ItemValue(0), 30),
		ShowHiddenFiles: m.categories[catUI].Form.ItemValue(1) == "on",
		ShowIcons:       m.categories[catUI].Form.ItemValue(2) == "on",
		UseNerdFont:     m.categories[catUI].Form.ItemValue(3) == "on",
		ShowGitStatus:   m.categories[catUI].Form.ItemValue(4) == "on",
		ShowDiagnostics: m.categories[catUI].Form.ItemValue(5) == "on",
	}
}

func (m *Model) collectFont(cfg *types.FontSettings) {
	*cfg = types.FontSettings{
		Family:    m.categories[catFont].Form.ItemValue(0),
		Size:      parseOpt(m.categories[catFont].Form.ItemValue(1), 12),
		Ligatures: m.categories[catFont].Form.ItemValue(2) == "on",
	}
}

func (m *Model) saveCurrentForm() {
}

// Snapshot saves the current state for cancel/restore.
func (m *Model) Snapshot() {
	catSnaps := make([]Category, len(m.categories))
	for i, cat := range m.categories {
		snap := *cat.Form
		snap.SaveSnapshot()
		catSnaps[i] = Category{Name: cat.Name, Icon: cat.Icon, Form: &snap}
	}
	m.snapshot = &modelSnapshot{
		categories: catSnaps,
		selected:   m.selected,
		pane:       m.pane,
	}
}

// Restore restores from snapshot.
func (m *Model) Restore() {
	if m.snapshot == nil {
		return
	}
	for i, snap := range m.snapshot.categories {
		if i < len(m.categories) {
			snap.Form.RestoreSnapshot()
			m.categories[i] = snap
		}
	}
	m.selected = m.snapshot.selected
	m.pane = m.snapshot.pane
}
