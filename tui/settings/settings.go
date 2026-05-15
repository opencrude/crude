package settings

import (
	tea "github.com/charmbracelet/bubbletea"

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

type settingsSnapshot struct {
	providerID                                           string
	modelCtx                                  int
	modelInPrice, modelOutPrice, modelCachePrice         float64
}

type Model struct {
	theme *theme.Theme
	form  form.Form

	providerID    string
	activePresets []types.ProviderPreset

	modelCtx                                  int
	modelInPrice, modelOutPrice, modelCachePrice float64

	getProviderConfigFn func(string) types.ProviderConfig
	fetchModelsFn       func(providerID, baseURL, apiKey string) tea.Cmd

	modelsData []types.ModelEntry

	snapshot *settingsSnapshot
}

func NewModel(t *theme.Theme, initial types.Settings, presets []types.ProviderPreset, getProviderConfigFn func(string) types.ProviderConfig, fetchModelsFn func(string, string, string) tea.Cmd) Model {
	providerOptions := make([]components.ListItem, len(presets))
	for i, p := range presets {
		providerOptions[i] = components.ListItem{ID: p.ID, Label: p.Name, Detail: p.HelpText}
	}

	providerItem := form.NewSelectItem(t, "provider", initial.ProviderID, 12)
	providerItem.SetOptions(providerOptions)

	baseURLItem := form.NewTextItem(t, "base url", initial.ProviderURL, false)
	apiKeyItem := form.NewTextItem(t, "api key", initial.ProviderAPIKey, true)
	modelItem := form.NewSelectItem(t, "model", initial.Model, 12)
	effortItem := form.NewCycleItem(t, "thinking effort",
		[]string{"none", "low", "medium", "high", "xhigh"}, initial.ThinkingEffort)
	turnLogsItem := form.NewCycleItem(t, "turn logs",
		[]string{"on", "off"}, initial.TurnLogsEnabled)

	f := form.NewFormWithButtons(t, "Settings",
		"[ interactive settings · arrows/tab: navigate · enter: edit · esc: return ]",
		[]form.Button{{Label: "Save", Key: "s", Color: t.Colors.Success}, {Label: "Cancel", Key: "c", Color: t.Colors.Error, Cancel: true}},
		providerItem, baseURLItem, apiKeyItem, modelItem, effortItem, turnLogsItem)

	return Model{
		theme:               t,
		form:                f,
		providerID:          initial.ProviderID,
		activePresets:       presets,
		modelCtx:            initial.ModelContextWindow,
		modelInPrice:        initial.MainInputPrice,
		modelOutPrice:       initial.MainOutputPrice,
		modelCachePrice:     initial.MainCacheReadPrice,
		getProviderConfigFn: getProviderConfigFn,
		fetchModelsFn:       fetchModelsFn,
	}
}

func (m Model) Update(msg tea.Msg) (Model, tea.Cmd, Event) {
	if keyMsg, ok := msg.(tea.KeyMsg); ok && !m.form.IsEditing() {
		switch keyMsg.String() {
		case "j":
			msg = tea.KeyMsg{Type: tea.KeyDown}
		case "k":
			msg = tea.KeyMsg{Type: tea.KeyUp}
		case "e":
			msg = tea.KeyMsg{Type: tea.KeyEnter}
		}
	}

	oldProvider := m.form.ItemValue(0)
	oldModel := m.form.ItemValue(3)

	updated, cmd, action := m.form.Update(msg)
	m.form = updated

	if action == form.FormActionExpand {
		expanded := m.form.ExpandedIndex()
		if expanded == 3 && m.canFetchModels() && m.form.ItemOptionCount(expanded) == 0 && !m.form.IsItemLoading(expanded) {
			m.form.SetItemLoading(expanded, true, "fetching models...")
			fetchCmd := m.fetchModels()
			if fetchCmd != nil {
				return m, fetchCmd, nil
			}
		}
	}

	newProvider := m.form.ItemValue(0)
	if newProvider != oldProvider {
		m.updateProviderFields(newProvider)
	}

	newModel := m.form.ItemValue(3)
	if newModel != oldModel {
		m.updateModelMetadata(newModel)
	}

	switch action {
	case form.FormActionSave:
		return m, nil, SaveEvent{Settings: m.toSettings()}
	case form.FormActionBack, form.FormActionCancel:
		return m, nil, CancelEvent{}
	}

	return m, cmd, nil
}

func (m Model) View(spinFrame int) string {
	return m.form.View(60, spinFrame)
}

func (m Model) Hint() string {
	return m.form.Hint()
}

func (m *Model) SetPresets(presets []types.ProviderPreset) {
	m.activePresets = presets
	providerOptions := make([]components.ListItem, len(presets))
	for i, p := range presets {
		providerOptions[i] = components.ListItem{ID: p.ID, Label: p.Name, Detail: p.HelpText}
	}
	m.form.SetItemSelectOptions(0, providerOptions)
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

	m.form.SetItemSelectOptions(3, items)
	m.form.SetItemLoading(3, false, "")
}

func (m *Model) SetModelsError(err string) {
	m.form.SetItemLoading(3, false, "")
	m.form.SetItemFetchError(3, err)
}

func (m *Model) SaveSnapshot() {
	m.form.SaveSnapshot()
	m.snapshot = &settingsSnapshot{
		providerID:      m.providerID,
		modelCtx:        m.modelCtx,
		modelInPrice:    m.modelInPrice,
		modelOutPrice:   m.modelOutPrice,
		modelCachePrice: m.modelCachePrice,
	}
}

func (m *Model) RestoreSnapshot() {
	if m.snapshot != nil {
		m.providerID = m.snapshot.providerID
		m.modelCtx = m.snapshot.modelCtx
		m.modelInPrice = m.snapshot.modelInPrice
		m.modelOutPrice = m.snapshot.modelOutPrice
		m.modelCachePrice = m.snapshot.modelCachePrice
	}
	m.form.RestoreSnapshot()
}

func (m *Model) FocusField(idx int) {
	m.form.FocusItem(idx)
}

func (m *Model) SetFieldValue(idx int, val string) {
	m.form.SetItemValue(idx, val)
}

func (m Model) ToSettings() types.Settings {
	return m.toSettings()
}

func (m Model) toSettings() types.Settings {
	apiKey := m.form.ItemValue(2)
	return types.Settings{
		ProviderID:              m.form.ItemValue(0),
		ProviderURL:             m.form.ItemValue(1),
		ProviderAPIKey:          apiKey,
		APIKey:                  apiKey,
		Model:                   m.form.ItemValue(3),
		ThinkingEffort:          m.form.ItemValue(4),
		TurnLogsEnabled:         m.form.ItemValue(5),
		ModelContextWindow:       m.modelCtx,
		MainInputPrice:           m.modelInPrice,
		MainOutputPrice:          m.modelOutPrice,
		MainCacheReadPrice:       m.modelCachePrice,
	}
}

func (m *Model) canFetchModels() bool {
	providerID := m.form.ItemValue(0)
	if providerID == "ollama" || providerID == "custom" {
		return false
	}
	apiKey := m.form.ItemValue(2)
	if apiKey == "" && providerID != "openrouter" {
		return false
	}
	return true
}

func (m Model) fetchModels() tea.Cmd {
	providerID := m.form.ItemValue(0)
	baseURL := m.form.ItemValue(1)
	apiKey := m.form.ItemValue(2)
	if m.fetchModelsFn != nil {
		return m.fetchModelsFn(providerID, baseURL, apiKey)
	}
	return nil
}

func (m *Model) updateProviderFields(newProvider string) {
	oldID := m.providerID
	m.providerID = newProvider

	if newProvider != oldID {
		m.form.SetItemSelectOptions(3, nil)
		m.modelCtx = 0
		m.modelInPrice = 0
		m.modelOutPrice = 0
		m.modelCachePrice = 0

		var preset types.ProviderPreset
		for _, p := range m.activePresets {
			if p.ID == newProvider {
				preset = p
				break
			}
		}
		if m.getProviderConfigFn != nil {
			savedCfg := m.getProviderConfigFn(newProvider)
			m.form.SetItemValue(1, savedCfg.ProviderURL)
			m.form.SetItemValue(2, savedCfg.APIKey)
			m.form.SetItemValue(3, savedCfg.Model)
			m.form.SetItemValue(4, savedCfg.ThinkingEffort)
			if savedCfg.ProviderURL == "" {
				m.form.SetItemValue(1, preset.BaseURL)
			}
		} else {
			m.form.SetItemValue(1, preset.BaseURL)
		}
	}
}

func (m *Model) updateModelMetadata(modelID string) {
	ctx := 0
	inPrice := 0.0
	outPrice := 0.0
	cachePrice := 0.0
	for _, entry := range m.modelsData {
		if entry.ID == modelID {
			ctx = entry.ContextWindow
			inPrice = entry.InputPrice
			outPrice = entry.OutputPrice
			cachePrice = entry.CacheReadPrice
			break
		}
	}

	m.modelCtx = ctx
	m.modelInPrice = inPrice
	m.modelOutPrice = outPrice
	m.modelCachePrice = cachePrice
}


