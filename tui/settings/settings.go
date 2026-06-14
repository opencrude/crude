package settings

import (
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"crude/tui/components"
	"crude/tui/form"
	"crude/tui/rendering"
	"crude/tui/theme"
	"crude/tui/types"
)

type Event interface{ event() }

type SaveEvent struct{ Settings types.Settings }

func (SaveEvent) event() {}

type CancelEvent struct{}

func (CancelEvent) event() {}

const (
	pageRoot = iota
	pageProfiles
	pageCategory
)

type Model struct {
	theme *theme.Theme

	page        int
	categoryKey string

	rootForm     form.Form
	categoryForm form.Form

	profileCursor     int
	creatingProfile   bool
	profileNameInput  []rune
	profileNameCursor int

	providerID    string
	activePresets []types.ProviderPreset

	getProviderConfigFn func(string) types.ProviderConfig
	fetchModelsFn       func(providerID, baseURL, apiKey string) tea.Cmd

	modelsData []types.ModelEntry

	snapshot *settingsSnapshot

	agentsSettings types.AgentsSettings
	turnLogs      string
}

type settingsSnapshot struct {
	providerID      string
	agentsSettings  types.AgentsSettings
	turnLogs        string
}

func NewModel(t *theme.Theme, initial types.Settings, presets []types.ProviderPreset, getProviderConfigFn func(string) types.ProviderConfig, fetchModelsFn func(string, string, string) tea.Cmd) Model {
	agents := initial.Agents
	if agents.Categories == nil {
		agents.Categories = make(map[string]types.CategorySettings)
	}
	if agents.ActiveProfile == "" {
		agents.ActiveProfile = "default"
	}

	turnLogs := initial.TurnLogsEnabled
	if turnLogs == "" {
		turnLogs = "on"
	}

	return Model{
		theme:               t,
		page:                pageRoot,
		rootForm:            buildRootForm(t, agents, turnLogs, initial.ProviderID, initial.Model),
		providerID:          initial.ProviderID,
		activePresets:       presets,
		getProviderConfigFn: getProviderConfigFn,
		fetchModelsFn:       fetchModelsFn,
		agentsSettings:      agents,
		turnLogs:            turnLogs,
	}
}

func catDetail(t *theme.Theme, cat types.CategorySettings, agentsEnabled bool) string {
	if !agentsEnabled {
		return t.Styles.DimLabel.Render("Off")
	}
	if cat.Model != "" && cat.Provider != "" {
		return t.Styles.InputText.Render(cat.Model + "/" + cat.Provider)
	}
	if cat.Model != "" {
		return t.Styles.InputText.Render(cat.Model)
	}
	return t.Styles.DimLabel.Render("inherits from main")
}

func boolStr(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

func buildRootForm(t *theme.Theme, agents types.AgentsSettings, turnLogs, defaultProvider, defaultModel string) form.Form {
	profileLabel := agents.ActiveProfile
	if profileLabel == "" {
		profileLabel = "default"
	}

	mainCat := agents.Categories["default"]
	mainModel := mainCat.Model
	if mainModel == "" {
		mainModel = defaultModel
	}
	mainProv := mainCat.Provider
	if mainProv == "" {
		mainProv = defaultProvider
	}
	mainDetail := ""
	if mainModel != "" && mainProv != "" {
		mainDetail = t.Styles.InputText.Render(mainModel + "/" + mainProv)
	} else if mainModel != "" {
		mainDetail = t.Styles.InputText.Render(mainModel)
	} else {
		mainDetail = lipgloss.NewStyle().Foreground(t.Colors.Error).Bold(true).Render("⚠ choose a model")
	}

	items := []form.FormItem{
		form.NewNavItem(t, "Profiles", profileLabel, "switch or add config presets", "profiles"),
		form.NewNavItem(t, "Main Agent", mainDetail, "plans & delegates work", "cat:main"),
		form.NewNavItem(t, "Researcher", catDetail(t, agents.Categories["researcher"], agents.Enabled), "reads files, searches web & gathers context", "cat:researcher"),
		form.NewNavItem(t, "Crafter", catDetail(t, agents.Categories["crafter"], agents.Enabled), "implements changes", "cat:crafter"),
		form.NewNavItem(t, "Aeron", catDetail(t, agents.Categories["aeron"], agents.Enabled), "dumb mechanical labor", "cat:aeron"),
		form.NewCycleItem(t, "Multi agent", []string{"on", "off"}, boolStr(agents.Enabled)),
		form.NewCycleItem(t, "Turn Logs", []string{"on", "off"}, turnLogs),
	}

	return form.NewFormWithButtons(t, "Settings",
		"arrows/tab: navigate · enter: edit · esc: return",
		[]form.Button{
			{Label: "Save", Key: "s", Color: t.Colors.Success},
			{Label: "Cancel", Key: "c", Color: t.Colors.Error, Cancel: true},
		},
		items...)
}

func buildCategoryForm(t *theme.Theme, catKey string, cat types.CategorySettings, presets []types.ProviderPreset, isMain bool) form.Form {
	providerItem := form.NewSelectItem(t, "provider", cat.Provider, 12)
	providerOptions := make([]components.ListItem, len(presets))
	for i, p := range presets {
		providerOptions[i] = components.ListItem{ID: p.ID, Label: p.Name, Detail: p.HelpText}
	}
	providerItem.SetOptions(providerOptions)

	baseURLItem := form.NewTextItem(t, "base url", cat.ProviderURL, false)
	apiKeyItem := form.NewTextItem(t, "api key", cat.APIKey, true)
	modelItem := form.NewSelectItem(t, "model", cat.Model, 12)
	effortItem := form.NewCycleItem(t, "thinking effort",
		[]string{"none", "low", "medium", "high", "xhigh"}, cat.ThinkingEffort)

	title := "Category: " + catKey
	if cat.Description != "" {
		title = cat.Description
	}
	if isMain {
		title = "Main Agent"
	}

	buttons := []form.Button{
		{Label: "Save", Key: "s", Color: t.Colors.Success},
		{Label: "Cancel", Key: "c", Color: t.Colors.Error, Cancel: true},
	}
	if !isMain {
		buttons = append(buttons, form.Button{Label: "Reset", Key: "r", Color: t.Colors.Warning, Reset: true})
	}

	return form.NewFormWithButtons(t, title,
		"enter: edit · esc: back",
		buttons,
		providerItem, baseURLItem, apiKeyItem, modelItem, effortItem)
}

func (m Model) Update(msg tea.Msg) (Model, tea.Cmd, Event) {
	if keyMsg, ok := msg.(tea.KeyPressMsg); ok && !m.isEditing() {
		switch keyMsg.String() {
		case "j":
			msg = tea.KeyPressMsg{Code: tea.KeyDown}
		case "k":
			msg = tea.KeyPressMsg{Code: tea.KeyUp}
		}
	}

	switch m.page {
	case pageRoot:
		return m.updateRoot(msg)
	case pageProfiles:
		return m.updateProfiles(msg)
	case pageCategory:
		return m.updateCategory(msg)
	}

	return m, nil, nil
}

func (m Model) isEditing() bool {
	switch m.page {
	case pageRoot:
		return m.rootForm.IsEditing()
	case pageProfiles:
		return m.creatingProfile
	case pageCategory:
		return m.categoryForm.IsEditing()
	}
	return false
}

func (m *Model) saveActiveProfileState() {
	maEnabled := m.agentsSettings.Enabled
	tl := m.turnLogs
	for i := range m.agentsSettings.Profiles {
		if m.agentsSettings.Profiles[i].Name == m.agentsSettings.ActiveProfile {
			m.agentsSettings.Profiles[i].MultiAgent = boolStr(maEnabled)
			m.agentsSettings.Profiles[i].TurnLogs = tl
			m.agentsSettings.Profiles[i].Categories = copyCatMap(m.agentsSettings.Categories)
			return
		}
	}
	m.agentsSettings.Profiles = append(m.agentsSettings.Profiles, types.ProfileSettings{
		Name:       m.agentsSettings.ActiveProfile,
		MultiAgent: boolStr(maEnabled),
		TurnLogs:   tl,
		Categories: copyCatMap(m.agentsSettings.Categories),
	})
}

func copyCatMap(src map[string]types.CategorySettings) map[string]types.CategorySettings {
	dst := make(map[string]types.CategorySettings, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func (m *Model) loadProfile(name string) bool {
	for _, p := range m.agentsSettings.Profiles {
		if p.Name == name {
			m.agentsSettings.Categories = copyCatMap(p.Categories)
			m.agentsSettings.Enabled = p.MultiAgent != "off"
			m.turnLogs = p.TurnLogs
			if m.turnLogs == "" {
				m.turnLogs = "on"
			}
			mainCat := p.Categories["default"]
			if mainCat.Provider == "" {
				m.providerID = ""
			}
			return true
		}
	}
	m.agentsSettings.Categories = make(map[string]types.CategorySettings)
	m.agentsSettings.Enabled = true
	m.turnLogs = "on"
	m.providerID = ""
	return false
}

func (m Model) updateRoot(msg tea.Msg) (Model, tea.Cmd, Event) {
	wasEnabled := m.agentsSettings.Enabled

	updated, cmd, action := m.rootForm.Update(msg)
	m.rootForm = updated

	if action == form.FormActionNavigate {
		if target, ok := m.rootForm.FocusedNavTarget(); ok {
			switch {
			case target == "profiles":
				m.saveActiveProfileState()
				m.openProfiles()
			case strings.HasPrefix(target, "cat:"):
				m.OpenCategory(strings.TrimPrefix(target, "cat:"))
			}
		}
		return m, cmd, nil
	}

	multiAgentVal := m.rootForm.ItemValue(5)
	m.agentsSettings.Enabled = multiAgentVal == "on"
	m.turnLogs = m.rootForm.ItemValue(6)

	if m.agentsSettings.Enabled && len(m.agentsSettings.Categories) == 0 {
		m.agentsSettings.MaxConcurrent = 4
		m.agentsSettings.Categories["default"] = types.CategorySettings{}
		m.agentsSettings.Categories["researcher"] = types.CategorySettings{}
		m.agentsSettings.Categories["crafter"] = types.CategorySettings{}
		m.agentsSettings.Categories["aeron"] = types.CategorySettings{}
	}

	if m.agentsSettings.Enabled != wasEnabled {
		m.rebuildRootForm()
	}

	switch action {
	case form.FormActionSave:
		m.saveActiveProfileState()
		return m, nil, SaveEvent{Settings: m.toSettings()}
	case form.FormActionBack, form.FormActionCancel:
		return m, nil, CancelEvent{}
	}

	return m, cmd, nil
}

func (m Model) updateProfiles(msg tea.Msg) (Model, tea.Cmd, Event) {
	// Handle paste for profile name creation field
	if pasteMsg, ok := msg.(tea.PasteMsg); ok {
		if m.creatingProfile {
			for _, r := range pasteMsg.Content {
				if unicode.IsPrint(r) {
					m.profileNameInput = append(m.profileNameInput[:m.profileNameCursor], append([]rune{r}, m.profileNameInput[m.profileNameCursor:]...)...)
					m.profileNameCursor++
				}
			}
		}
		return m, nil, nil
	}

	keyMsg, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil, nil
	}

	numProfiles := len(m.agentsSettings.Profiles)
	createIdx := numProfiles

	if keyMsg.Code == tea.KeyEsc {
		if m.creatingProfile {
			m.cancelCreate()
			return m, nil, nil
		}
		m.page = pageRoot
		return m, nil, nil
	}

	if m.profileCursor == createIdx {
		return m.updateCreateRow(keyMsg, numProfiles, createIdx)
	}

	return m.updateProfileList(keyMsg, numProfiles, createIdx)
}

func (m Model) updateCreateRow(keyMsg tea.KeyPressMsg, numProfiles, createIdx int) (Model, tea.Cmd, Event) {
	m.creatingProfile = true

	switch keyMsg.Code {
	case tea.KeyUp:
		if m.profileCursor > 0 {
			m.profileCursor--
		}
		m.creatingProfile = false
		return m, nil, nil
	case tea.KeyDown:
		return m, nil, nil
	case tea.KeyBackspace:
		if m.profileNameCursor > 0 {
			m.profileNameInput = append(m.profileNameInput[:m.profileNameCursor-1], m.profileNameInput[m.profileNameCursor:]...)
			m.profileNameCursor--
		}
		return m, nil, nil
	case tea.KeyDelete:
		if m.profileNameCursor < len(m.profileNameInput) {
			m.profileNameInput = append(m.profileNameInput[:m.profileNameCursor], m.profileNameInput[m.profileNameCursor+1:]...)
		}
		return m, nil, nil
	case tea.KeyLeft:
		if m.profileNameCursor > 0 {
			m.profileNameCursor--
		}
		return m, nil, nil
	case tea.KeyRight:
		if m.profileNameCursor < len(m.profileNameInput) {
			m.profileNameCursor++
		}
		return m, nil, nil
	case tea.KeyHome:
		m.profileNameCursor = 0
		return m, nil, nil
	case tea.KeyEnd:
		m.profileNameCursor = len(m.profileNameInput)
		return m, nil, nil
	case tea.KeyEnter:
		return m.saveNewProfile()
	default:
		for _, r := range keyMsg.Text {
			if unicode.IsPrint(r) {
				m.profileNameInput = append(m.profileNameInput[:m.profileNameCursor], append([]rune{r}, m.profileNameInput[m.profileNameCursor:]...)...)
				m.profileNameCursor++
			}
		}
	}
	return m, nil, nil
}

func (m Model) updateProfileList(keyMsg tea.KeyPressMsg, numProfiles, createIdx int) (Model, tea.Cmd, Event) {
	switch keyMsg.Code {
	case tea.KeyUp:
		if m.profileCursor > 0 {
			m.profileCursor--
		}
	case tea.KeyDown:
		if m.profileCursor < createIdx {
			m.profileCursor++
		}
	case tea.KeyEnter:
		if m.profileCursor < numProfiles {
			name := m.agentsSettings.Profiles[m.profileCursor].Name
			if name != m.agentsSettings.ActiveProfile {
				m.saveActiveProfileState()
				m.agentsSettings.ActiveProfile = name
				m.loadProfile(name)
				m.rebuildRootForm()
			}
			m.page = pageRoot
			return m, nil, nil
		}
	case tea.KeyDelete:
		if m.profileCursor < numProfiles {
			name := m.agentsSettings.Profiles[m.profileCursor].Name
			if name != "default" {
				m.deleteProfile(name)
				if m.profileCursor >= len(m.agentsSettings.Profiles) {
					m.profileCursor = len(m.agentsSettings.Profiles) - 1
				}
			}
		}
	}
	return m, nil, nil
}

func (m *Model) cancelCreate() {
	m.creatingProfile = false
	m.profileNameInput = nil
	m.profileNameCursor = 0
	for i, p := range m.agentsSettings.Profiles {
		if p.Name == m.agentsSettings.ActiveProfile {
			m.profileCursor = i
			break
		}
	}
}

func (m Model) saveNewProfile() (Model, tea.Cmd, Event) {
	name := strings.TrimSpace(string(m.profileNameInput))
	if name != "" && name != "default" && !m.profileExists(name) {
		m.createProfile(name)
		m.profileCursor = len(m.agentsSettings.Profiles) - 1
		m.creatingProfile = false
		m.profileNameInput = nil
		m.profileNameCursor = 0
	}
	return m, nil, nil
}

func (m Model) updateCategory(msg tea.Msg) (Model, tea.Cmd, Event) {
	oldProvider := m.categoryForm.ItemValue(0)

	updated, cmd, action := m.categoryForm.Update(msg)
	m.categoryForm = updated

	if action == form.FormActionExpand {
		expanded := m.categoryForm.ExpandedIndex()
		if expanded == 0 && m.canFetchModels() && m.categoryForm.ItemOptionCount(0) == 0 && !m.categoryForm.IsItemLoading(0) {
			m.categoryForm.SetItemLoading(0, true, "fetching models...")
		}
		if expanded == 3 && m.canFetchModels() && m.categoryForm.ItemOptionCount(3) == 0 && !m.categoryForm.IsItemLoading(3) {
			m.categoryForm.SetItemLoading(3, true, "fetching models...")
			fetchCmd := m.fetchModels()
			if fetchCmd != nil {
				return m, fetchCmd, nil
			}
		}
	}

	newProvider := m.categoryForm.ItemValue(0)
	if newProvider != oldProvider {
		m.updateProviderFields(newProvider)
	}

	switch action {
	case form.FormActionSave:
		m.saveCategoryForm()
		m.saveActiveProfileState()
		m.rebuildRootForm()
		m.page = pageRoot
		return m, nil, nil
	case form.FormActionReset:
		m.agentsSettings.Categories[m.categoryKey] = types.CategorySettings{}
		m.saveActiveProfileState()
		m.rebuildRootForm()
		m.page = pageRoot
		return m, nil, nil
	case form.FormActionBack, form.FormActionCancel:
		m.page = pageRoot
		return m, nil, nil
	}

	return m, cmd, nil
}

func (m Model) View(spinFrame int) string {
	switch m.page {
	case pageRoot:
		return m.rootForm.View(70, spinFrame)
	case pageProfiles:
		return "\n" + m.viewProfiles(70, spinFrame)
	case pageCategory:
		return m.categoryForm.View(70, spinFrame)
	}
	return ""
}

func (m Model) Hint() string {
	switch m.page {
	case pageRoot:
		return m.rootForm.Hint()
	case pageProfiles:
		return m.profileHint()
	case pageCategory:
		return m.categoryForm.Hint()
	}
	return ""
}

func (m Model) viewProfiles(width int, spinFrame int) string {
	dim := m.theme.Styles.DimLabel
	accent := m.theme.Styles.InputPrompt
	accentColor := m.theme.Colors.Accent
	fg := m.theme.Colors.Fg

	sepWidth := width
	if sepWidth < 20 {
		sepWidth = 20
	}

	var b strings.Builder

	titleStyle := lipgloss.NewStyle().Foreground(accentColor).Bold(true)
	b.WriteString(titleStyle.Render("Profiles"))
	b.WriteString("\n")
	b.WriteString(dim.Render(strings.Repeat("─", sepWidth)))
	b.WriteString("\n")

	numProfiles := len(m.agentsSettings.Profiles)
	createIdx := numProfiles

	for i, p := range m.agentsSettings.Profiles {
		if m.profileCursor == i {
			marker := accent.Render("❯")
			label := lipgloss.NewStyle().Foreground(fg).Bold(true).Render(p.Name)
			b.WriteString(marker + " " + label)
		} else {
			label := lipgloss.NewStyle().Foreground(fg).Render(p.Name)
			b.WriteString("  " + label)
		}
		b.WriteString("\n")
	}

	b.WriteString(dim.Render(strings.Repeat("─", sepWidth)))
	b.WriteString("\n")

	if m.profileCursor == createIdx {
		marker := accent.Render("❯")
		if m.creatingProfile && len(m.profileNameInput) > 0 {
			before := string(m.profileNameInput[:m.profileNameCursor])
			after := string(m.profileNameInput[m.profileNameCursor:])
			inputStyle := m.theme.Styles.InputText

			curChar := " "
			cursorBlock := ""
			if m.profileNameCursor < len(m.profileNameInput) {
				curChar = string(m.profileNameInput[m.profileNameCursor])
				after = string(m.profileNameInput[m.profileNameCursor+1:])
				if components.BlinkOn(spinFrame) {
					rendered := lipgloss.NewStyle().Background(accentColor).Foreground(m.theme.Colors.Bg).Render(curChar)
					cursorBlock = inputStyle.Render(before) + rendered + inputStyle.Render(after)
				} else {
					cursorBlock = inputStyle.Render(before) + inputStyle.Render(curChar) + inputStyle.Render(after)
				}
			} else {
				cursorBlock = inputStyle.Render(before) + components.BlockCursor(spinFrame, accentColor)
			}
			b.WriteString(marker + " " + cursorBlock)
		} else if m.creatingProfile {
			cursorBlock := components.BlockCursor(spinFrame, accentColor)
			b.WriteString(marker + " " + cursorBlock)
		} else {
			createLabel := lipgloss.NewStyle().Foreground(fg).Render("Create new profile")
			b.WriteString(marker + " " + createLabel)
		}
	} else {
		createLabel := dim.Render("Create new profile")
		b.WriteString("  " + createLabel)
	}
	b.WriteString("\n")

	b.WriteString(dim.Render(strings.Repeat("─", sepWidth)))
	b.WriteString("\n")

	return b.String()
}

func (m Model) profileHint() string {
	if m.creatingProfile {
		return "[ type name · enter: save · esc: cancel ]"
	}
	numProfiles := len(m.agentsSettings.Profiles)
	createIdx := numProfiles
	desc := ""
	if m.profileCursor < numProfiles {
		desc = m.agentsSettings.Profiles[m.profileCursor].Name
	} else if m.profileCursor == createIdx {
		desc = "add a new profile"
	}
	h := "↑↓ navigate · enter: select · del: delete · esc: back"
	if desc != "" {
		h = desc + " · " + h
	}
	return "[ " + h + " ]"
}

func (m *Model) SetPresets(presets []types.ProviderPreset) {
	m.activePresets = presets
}

func (m *Model) SetModels(models []types.ModelEntry) {
	m.modelsData = models
	items := make([]components.ListItem, 0, len(models))
	for _, mod := range models {
		detail := ""
		if mod.ContextWindow > 0 {
			detail = rendering.FormatTokens(mod.ContextWindow)
		}
		name := mod.Name
		if name == "" {
			name = mod.ID
		}
		items = append(items, components.ListItem{ID: mod.ID, Label: name, Detail: detail})
	}
	if m.page == pageCategory {
		m.categoryForm.SetItemSelectOptions(3, items)
		m.categoryForm.SetItemLoading(3, false, "")
	}
}

func (m *Model) SetModelsError(err string) {
	if m.page == pageCategory {
		m.categoryForm.SetItemLoading(3, false, "")
		m.categoryForm.SetItemFetchError(3, err)
	}
}

func (m *Model) SaveSnapshot() {
	m.rootForm.SaveSnapshot()
	m.snapshot = &settingsSnapshot{
		providerID:      m.providerID,
		agentsSettings:  m.agentsSettings,
		turnLogs:        m.turnLogs,
	}
}

func (m *Model) RestoreSnapshot() {
	if m.snapshot != nil {
		m.providerID = m.snapshot.providerID
		m.agentsSettings = m.snapshot.agentsSettings
		m.turnLogs = m.snapshot.turnLogs
	}
	m.rootForm.RestoreSnapshot()
	m.rebuildRootForm()
}

func (m *Model) FocusField(idx int) {
	m.rootForm.FocusItem(idx)
}

func (m *Model) SetFieldValue(idx int, val string) {
	m.rootForm.SetItemValue(idx, val)
}

func (m Model) ToSettings() types.Settings {
	return m.toSettings()
}

func (m Model) toSettings() types.Settings {
	mainCat := m.agentsSettings.Categories["default"]
	providerID := mainCat.Provider
	apiKey := mainCat.APIKey
	providerURL := mainCat.ProviderURL
	model := mainCat.Model
	thinkingEffort := mainCat.ThinkingEffort

	return types.Settings{
		APIKey:          apiKey,
		Model:           model,
		ProviderID:      providerID,
		ProviderURL:     providerURL,
		ProviderAPIKey:  apiKey,
		ThinkingEffort:  thinkingEffort,
		TurnLogsEnabled: m.turnLogs,
		Agents:          m.agentsSettings,
	}
}

func (m *Model) openProfiles() {
	m.profileCursor = 0
	for i, p := range m.agentsSettings.Profiles {
		if p.Name == m.agentsSettings.ActiveProfile {
			m.profileCursor = i
			break
		}
	}
	m.creatingProfile = false
	m.profileNameInput = nil
	m.profileNameCursor = 0
	m.page = pageProfiles
}

func (m *Model) OpenCategory(catKey string) {
	cat := m.agentsSettings.Categories[catKey]
	if cat.Provider == "" && catKey == "default" {
		cat.Provider = m.providerID
	}
	m.categoryKey = catKey
	m.categoryForm = buildCategoryForm(m.theme, catKey, cat, m.activePresets, catKey == "default")
	m.page = pageCategory
}

func (m *Model) OpenDefaultProfileEdit() {
	m.OpenCategory("default")
}

func (m *Model) saveCategoryForm() {
	cat := m.agentsSettings.Categories[m.categoryKey]
	cat.Provider = m.categoryForm.ItemValue(0)
	cat.ProviderURL = m.categoryForm.ItemValue(1)
	cat.APIKey = m.categoryForm.ItemValue(2)
	cat.Model = m.categoryForm.ItemValue(3)
	cat.ThinkingEffort = m.categoryForm.ItemValue(4)

	for _, mod := range m.modelsData {
		if mod.ID == cat.Model {
			cat.ModelContextWindow = mod.ContextWindow
			cat.ModelInputPrice = mod.InputPrice
			cat.ModelOutputPrice = mod.OutputPrice
			cat.ModelCachePrice = mod.CacheReadPrice
			cat.ModelSupportsVision = mod.SupportsVision
			cat.ModelSupportsTools = mod.SupportsTools
			cat.ModelMaxOutput = mod.MaxOutput
			cat.ModelAttachment = mod.Attachment
			break
		}
	}

	m.agentsSettings.Categories[m.categoryKey] = cat

	if m.categoryKey == "default" {
		m.providerID = cat.Provider
	}
}

func (m *Model) createProfile(name string) {
	m.agentsSettings.Profiles = append(m.agentsSettings.Profiles, types.ProfileSettings{
		Name:       name,
		Categories: make(map[string]types.CategorySettings),
		MultiAgent: "on",
		TurnLogs:   "on",
	})
}

func (m *Model) deleteProfile(name string) {
	if name == "default" {
		return
	}
	for i := range m.agentsSettings.Profiles {
		if m.agentsSettings.Profiles[i].Name == name {
			m.agentsSettings.Profiles = append(m.agentsSettings.Profiles[:i], m.agentsSettings.Profiles[i+1:]...)
			return
		}
	}
}

func (m Model) profileExists(name string) bool {
	for _, p := range m.agentsSettings.Profiles {
		if p.Name == name {
			return true
		}
	}
	return false
}

func (m Model) canFetchModels() bool {
	providerID := m.categoryForm.ItemValue(0)
	if providerID == "ollama" || providerID == "custom" {
		return false
	}
	apiKey := m.categoryForm.ItemValue(2)
	if apiKey == "" && providerID != "openrouter" {
		return false
	}
	return true
}

func (m Model) fetchModels() tea.Cmd {
	providerID := m.categoryForm.ItemValue(0)
	baseURL := m.categoryForm.ItemValue(1)
	apiKey := m.categoryForm.ItemValue(2)
	if m.fetchModelsFn != nil {
		return m.fetchModelsFn(providerID, baseURL, apiKey)
	}
	return nil
}

func (m *Model) autofillFromExistingProvider(providerID string) {
	var preset types.ProviderPreset
	for _, p := range m.activePresets {
		if p.ID == providerID {
			preset = p
			break
		}
	}

	if m.getProviderConfigFn != nil {
		savedCfg := m.getProviderConfigFn(providerID)
		if m.categoryForm.ItemValue(1) == "" {
			url := savedCfg.ProviderURL
			if url == "" {
				url = preset.BaseURL
			}
			if url != "" {
				m.categoryForm.SetItemValue(1, url)
			}
		}
		if m.categoryForm.ItemValue(2) == "" {
			if savedCfg.APIKey != "" {
				m.categoryForm.SetItemValue(2, savedCfg.APIKey)
			}
		}
	} else if m.categoryForm.ItemValue(1) == "" && preset.BaseURL != "" {
		m.categoryForm.SetItemValue(1, preset.BaseURL)
	}

	for _, cat := range m.agentsSettings.Categories {
		if cat.Provider == providerID && cat.ProviderURL != "" {
			if m.categoryForm.ItemValue(1) == "" {
				m.categoryForm.SetItemValue(1, cat.ProviderURL)
			}
			if m.categoryForm.ItemValue(2) == "" && cat.APIKey != "" {
				m.categoryForm.SetItemValue(2, cat.APIKey)
			}
			return
		}
	}
}

func (m *Model) updateProviderFields(newProvider string) {
	if m.categoryKey == "default" {
		m.providerID = newProvider
	}

	var preset types.ProviderPreset
	for _, p := range m.activePresets {
		if p.ID == newProvider {
			preset = p
			break
		}
	}

	m.categoryForm.SetItemSelectOptions(3, nil)
	m.categoryForm.SetItemValue(3, "")
	m.categoryForm.SetItemValue(4, "")

	cat := m.agentsSettings.Categories[m.categoryKey]
	cat.Model = ""
	cat.ThinkingEffort = ""
	cat.ModelContextWindow = 0
	cat.ModelInputPrice = 0
	cat.ModelOutputPrice = 0
	cat.ModelCachePrice = 0
	m.agentsSettings.Categories[m.categoryKey] = cat

	if m.getProviderConfigFn != nil {
		savedCfg := m.getProviderConfigFn(newProvider)
		m.categoryForm.SetItemValue(1, savedCfg.ProviderURL)
		m.categoryForm.SetItemValue(2, savedCfg.APIKey)
		if savedCfg.ProviderURL == "" {
			m.categoryForm.SetItemValue(1, preset.BaseURL)
		}
	} else {
		m.categoryForm.SetItemValue(1, preset.BaseURL)
	}
}

func (m *Model) NavigateTo(target string) {
	switch {
	case target == "profiles":
		m.openProfiles()
	case strings.HasPrefix(target, "cat:"):
		m.OpenCategory(strings.TrimPrefix(target, "cat:"))
	}
}

func (m *Model) rebuildRootForm() {
	m.rootForm = buildRootForm(m.theme, m.agentsSettings, m.turnLogs, m.providerID, "")
}
