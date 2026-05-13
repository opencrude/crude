package settings

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"crude/tui/components"
	"crude/tui/form"
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
	modelCtx, oracleCtx                                  int
	modelInPrice, modelOutPrice, modelCachePrice         float64
	oracleInPrice, oracleOutPrice, oracleCachePrice      float64
}

type Model struct {
	theme *theme.Theme
	form  form.Form

	providerID    string
	activePresets []types.ProviderPreset

	modelCtx, oracleCtx                        int
	modelInPrice, modelOutPrice, modelCachePrice float64
	oracleInPrice, oracleOutPrice, oracleCachePrice float64

	getProviderConfigFn func(string) types.ProviderConfig
	fetchModelsFn       func(providerID, baseURL, apiKey string, isOracle bool) tea.Cmd

	modelsData []types.ModelEntry

	snapshot *settingsSnapshot
}

func NewModel(t *theme.Theme, initial types.Settings, presets []types.ProviderPreset, getProviderConfigFn func(string) types.ProviderConfig, fetchModelsFn func(string, string, string, bool) tea.Cmd) Model {
	providerOptions := make([]components.ListItem, len(presets))
	for i, p := range presets {
		providerOptions[i] = components.ListItem{ID: p.ID, Label: p.Name, Detail: p.HelpText}
	}

	providerItem := form.NewSelectItem(t, "provider", initial.ProviderID, 12)
	providerItem.SetOptions(providerOptions)

	baseURLItem := form.NewTextItem(t, "base url", initial.ProviderURL, false)
	apiKeyItem := form.NewTextItem(t, "api key", initial.ProviderAPIKey, true)
	modelItem := form.NewSelectItem(t, "model", initial.Model, 12)
	oracleModelItem := form.NewSelectItem(t, "oracle model", initial.OracleModel, 12)
	effortItem := form.NewCycleItem(t, "thinking effort",
		[]string{"none", "low", "medium", "high", "xhigh"}, initial.ThinkingEffort)

	f := form.NewFormWithButtons(t, "Settings",
		"[ interactive settings · arrows/tab: navigate · enter: edit · esc: return ]",
		[]form.Button{{Label: "Save", Key: "s", Color: t.Colors.Success}, {Label: "Cancel", Key: "c", Color: t.Colors.Error, Cancel: true}},
		providerItem, baseURLItem, apiKeyItem, modelItem, oracleModelItem, effortItem)

	return Model{
		theme:               t,
		form:                f,
		providerID:          initial.ProviderID,
		activePresets:       presets,
		modelCtx:            initial.ModelContextWindow,
		oracleCtx:           initial.OracleModelContextWindow,
		modelInPrice:        initial.MainInputPrice,
		modelOutPrice:       initial.MainOutputPrice,
		modelCachePrice:     initial.MainCacheReadPrice,
		oracleInPrice:       initial.OracleInputPrice,
		oracleOutPrice:      initial.OracleOutputPrice,
		oracleCachePrice:    initial.OracleCacheReadPrice,
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
	oldOracleModel := m.form.ItemValue(4)

	updated, cmd, action := m.form.Update(msg)
	m.form = updated

	if action == form.FormActionExpand {
		expanded := m.form.ExpandedIndex()
		if expanded == 3 || expanded == 4 {
			isOracle := expanded == 4
			if !m.canFetchModels() {
			} else if m.form.ItemOptionCount(expanded) == 0 && !m.form.IsItemLoading(expanded) {
				m.form.SetItemLoading(expanded, true, "fetching models...")
				fetchCmd := m.fetchModels(isOracle)
				if fetchCmd != nil {
					return m, fetchCmd, nil
				}
			}
		}
	}

	newProvider := m.form.ItemValue(0)
	if newProvider != oldProvider {
		m.updateProviderFields(newProvider)
	}

	newModel := m.form.ItemValue(3)
	if newModel != oldModel {
		m.updateModelMetadata(newModel, false)
	}

	newOracleModel := m.form.ItemValue(4)
	if newOracleModel != oldOracleModel {
		m.updateModelMetadata(newOracleModel, true)
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

func (m *Model) SetModels(models []types.ModelEntry, isOracle bool) {
	m.modelsData = models
	items := make([]components.ListItem, 0, len(models))
	for _, mod := range models {
		detail := ""
		if mod.ContextWindow > 0 {
			detail = formatCtxWindow(mod.ContextWindow)
		}
		name := mod.Name
		if name == "" {
			name = mod.ID
		}
		items = append(items, components.ListItem{ID: mod.ID, Label: name, Detail: detail})
	}

	idx := 3
	if isOracle {
		idx = 4
	}
	m.form.SetItemSelectOptions(idx, items)
	m.form.SetItemLoading(idx, false, "")
}

func (m *Model) SetModelsError(err string, isOracle bool) {
	idx := 3
	if isOracle {
		idx = 4
	}
	m.form.SetItemLoading(idx, false, "")
	m.form.SetItemFetchError(idx, err)
}

func (m *Model) SaveSnapshot() {
	m.form.SaveSnapshot()
	m.snapshot = &settingsSnapshot{
		providerID:      m.providerID,
		modelCtx:        m.modelCtx,
		oracleCtx:       m.oracleCtx,
		modelInPrice:    m.modelInPrice,
		modelOutPrice:   m.modelOutPrice,
		modelCachePrice: m.modelCachePrice,
		oracleInPrice:   m.oracleInPrice,
		oracleOutPrice:  m.oracleOutPrice,
		oracleCachePrice: m.oracleCachePrice,
	}
}

func (m *Model) RestoreSnapshot() {
	if m.snapshot != nil {
		m.providerID = m.snapshot.providerID
		m.modelCtx = m.snapshot.modelCtx
		m.oracleCtx = m.snapshot.oracleCtx
		m.modelInPrice = m.snapshot.modelInPrice
		m.modelOutPrice = m.snapshot.modelOutPrice
		m.modelCachePrice = m.snapshot.modelCachePrice
		m.oracleInPrice = m.snapshot.oracleInPrice
		m.oracleOutPrice = m.snapshot.oracleOutPrice
		m.oracleCachePrice = m.snapshot.oracleCachePrice
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
	return types.Settings{
		ProviderID:              m.form.ItemValue(0),
		ProviderURL:             m.form.ItemValue(1),
		ProviderAPIKey:          m.form.ItemValue(2),
		Model:                   m.form.ItemValue(3),
		OracleModel:             m.form.ItemValue(4),
		ThinkingEffort:          m.form.ItemValue(5),
		ModelContextWindow:       m.modelCtx,
		MainInputPrice:           m.modelInPrice,
		MainOutputPrice:          m.modelOutPrice,
		MainCacheReadPrice:       m.modelCachePrice,
		OracleModelContextWindow: m.oracleCtx,
		OracleInputPrice:         m.oracleInPrice,
		OracleOutputPrice:        m.oracleOutPrice,
		OracleCacheReadPrice:     m.oracleCachePrice,
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

func (m Model) fetchModels(isOracle bool) tea.Cmd {
	providerID := m.form.ItemValue(0)
	baseURL := m.form.ItemValue(1)
	apiKey := m.form.ItemValue(2)
	if m.fetchModelsFn != nil {
		return m.fetchModelsFn(providerID, baseURL, apiKey, isOracle)
	}
	return nil
}

func (m *Model) updateProviderFields(newProvider string) {
	oldID := m.providerID
	m.providerID = newProvider

	if newProvider != oldID {
		m.form.SetItemSelectOptions(3, nil)
		m.form.SetItemSelectOptions(4, nil)
		m.modelCtx = 0
		m.modelInPrice = 0
		m.modelOutPrice = 0
		m.modelCachePrice = 0
		m.oracleCtx = 0
		m.oracleInPrice = 0
		m.oracleOutPrice = 0
		m.oracleCachePrice = 0

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
			m.form.SetItemValue(4, savedCfg.OracleModel)
			if savedCfg.ProviderURL == "" {
				m.form.SetItemValue(1, preset.BaseURL)
			}
		} else {
			m.form.SetItemValue(1, preset.BaseURL)
		}
	}
}

func (m *Model) updateModelMetadata(modelID string, isOracle bool) {
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

	if isOracle {
		m.oracleCtx = ctx
		m.oracleInPrice = inPrice
		m.oracleOutPrice = outPrice
		m.oracleCachePrice = cachePrice
	} else {
		m.modelCtx = ctx
		m.modelInPrice = inPrice
		m.modelOutPrice = outPrice
		m.modelCachePrice = cachePrice
	}
}

func formatCtxWindow(n int) string {
	switch {
	case n >= 1000000:
		return fmt.Sprintf("%.1fM", float64(n)/1000000)
	case n >= 1000:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	default:
		return fmt.Sprintf("%d", n)
	}
}
