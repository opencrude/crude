package settings

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"crude/tui/components"
	"crude/tui/form"
	"crude/tui/theme"
	"crude/tui/types"
)

type settingsMode int

const (
	modeForm settingsMode = iota
	modeProviderSelect
	modeModelSelect
	modeOracleModelSelect
)

type Event interface{ event() }

type SaveEvent struct{ Settings types.Settings }

func (SaveEvent) event() {}

type CancelEvent struct{}

func (CancelEvent) event() {}

type FetchModelsEvent struct {
	ProviderID, BaseURL, APIKey string
}

func (FetchModelsEvent) event() {}

type Model struct {
	theme    *theme.Theme
	form     form.Form
	provider components.List
	models   components.List
	mode     settingsMode

	providerID  string
	activePresets []types.ProviderPreset

	modelCtx, oracleCtx        int
	modelInPrice, modelOutPrice          float64
	modelCachePrice              float64
	oracleInPrice, oracleOutPrice        float64
	oracleCachePrice             float64

	getProviderConfigFn func(string) types.ProviderConfig
	fetchModelsFn       func(string, string, string) tea.Cmd
	selectTarget        string

	modelsData []types.ModelEntry

	thinkingEffort string

	snapshot *settingsSnapshot
}

func NewModel(t *theme.Theme, initial types.Settings, presets []types.ProviderPreset, getProviderConfigFn func(string) types.ProviderConfig, fetchModelsFn func(string, string, string) tea.Cmd) Model {
	fields := []form.Field{
		{Label: "provider", Value: []rune(initial.ProviderID)},
		{Label: "base url", Value: []rune(initial.ProviderURL)},
		{Label: "api key", Value: []rune(initial.ProviderAPIKey), Masked: true},
		{Label: "model", Value: []rune(initial.Model)},
		{Label: "oracle model", Value: []rune(initial.OracleModel)},
		{Label: "thinking effort", Value: []rune(initial.ThinkingEffort)},
	}

	return Model{
		theme:               t,
		form:                form.NewForm(t, fields...),
		provider:            components.NewList(t, 12),
		models:              components.NewList(t, 12),
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
		thinkingEffort:      initial.ThinkingEffort,
	}
}

func (m Model) Update(msg tea.Msg) (Model, tea.Cmd, Event) {
	switch m.mode {
	case modeProviderSelect:
		return m.updateProviderSelect(msg)
	case modeModelSelect, modeOracleModelSelect:
		return m.updateModelSelect(msg)
	default:
		return m.updateForm(msg)
	}
}

func (m Model) View(spinFrame int) string {
	switch m.mode {
	case modeProviderSelect:
		return m.renderProviderSelect()
	case modeModelSelect, modeOracleModelSelect:
		return m.renderModelSelect(spinFrame)
	default:
		return m.form.View(spinFrame)
	}
}

func (m *Model) SetPresets(presets []types.ProviderPreset) {
	m.activePresets = presets
}

type settingsSnapshot struct {
	providerID      string
	mode            settingsMode
	modelCtx, oracleCtx        int
	modelInPrice, modelOutPrice          float64
	modelCachePrice              float64
	oracleInPrice, oracleOutPrice        float64
	oracleCachePrice             float64
	thinkingEffort string
	formSnapshot   *form.Form
}

func (m *Model) SaveSnapshot() {
	m.snapshot = &settingsSnapshot{
		providerID:      m.providerID,
		mode:            m.mode,
		modelCtx:        m.modelCtx,
		oracleCtx:       m.oracleCtx,
		modelInPrice:    m.modelInPrice,
		modelOutPrice:   m.modelOutPrice,
		modelCachePrice: m.modelCachePrice,
		oracleInPrice:   m.oracleInPrice,
		oracleOutPrice:  m.oracleOutPrice,
		oracleCachePrice: m.oracleCachePrice,
		thinkingEffort:  m.thinkingEffort,
		formSnapshot:    nil,
	}
	// use form's snapshot mechanism for fields
	m.form.SaveSnapshot()
}

func (m *Model) RestoreSnapshot() {
	if m.snapshot != nil {
		m.providerID = m.snapshot.providerID
		m.mode = modeForm
		m.modelCtx = m.snapshot.modelCtx
		m.oracleCtx = m.snapshot.oracleCtx
		m.modelInPrice = m.snapshot.modelInPrice
		m.modelOutPrice = m.snapshot.modelOutPrice
		m.modelCachePrice = m.snapshot.modelCachePrice
		m.oracleInPrice = m.snapshot.oracleInPrice
		m.oracleOutPrice = m.snapshot.oracleOutPrice
		m.oracleCachePrice = m.snapshot.oracleCachePrice
		m.thinkingEffort = m.snapshot.thinkingEffort
	}
	m.form.RestoreSnapshot()
	m.mode = modeForm
}

func (m Model) updateForm(msg tea.Msg) (Model, tea.Cmd, Event) {
	keyMsg, ok := msg.(tea.KeyMsg)
	if ok {
		// vim-style navigation
		if !m.form.IsEditing() {
			switch keyMsg.String() {
			case "j":
				keyMsg = tea.KeyMsg{Type: tea.KeyDown}
			case "k":
				keyMsg = tea.KeyMsg{Type: tea.KeyUp}
			case "e":
				keyMsg = tea.KeyMsg{Type: tea.KeyEnter}
			}
		}
	}
	if ok && keyMsg.Type == tea.KeyEnter && m.form.FocusedField() >= 0 {
		switch m.form.FocusedField() {
		case 0:
			items := make([]components.ListItem, len(m.activePresets))
			for i, p := range m.activePresets {
				items[i] = components.ListItem{ID: p.ID, Label: p.Name, Detail: p.HelpText}
			}
			m.provider.SetItems(items)
			m.mode = modeProviderSelect
			return m, nil, nil
		case 3:
			providerID := m.form.FieldValue(0)
			apiKey := m.form.FieldValue(2)
			if providerID == "ollama" || providerID == "custom" {
				m.form.StartEditing(3)
				return m, func() tea.Msg {
					return types.ErrorMsg("Type model name manually for " + providerID + ".")
				}, nil
			}
			if apiKey == "" && providerID != "openrouter" {
				m.form.StartEditing(3)
				return m, func() tea.Msg {
					return types.ErrorMsg("No API key provided for " + providerID + ". Type model name manually.")
				}, nil
			}
			m.selectTarget = "model"
			m.models.SetFetching(true)
			if m.fetchModelsFn != nil {
				pid := providerID
				baseURL := m.form.FieldValue(1)
				return m, m.fetchModelsFn(pid, baseURL, apiKey), nil
			}
			m.mode = modeModelSelect
			return m, nil, nil
		case 4:
			providerID := m.form.FieldValue(0)
			apiKey := m.form.FieldValue(2)
			if providerID == "ollama" || providerID == "custom" {
				m.form.StartEditing(4)
				return m, func() tea.Msg {
					return types.ErrorMsg("Type oracle model name manually for " + providerID + ".")
				}, nil
			}
			if apiKey == "" && providerID != "openrouter" {
				m.form.StartEditing(4)
				return m, func() tea.Msg {
					return types.ErrorMsg("No API key provided for " + providerID + ". Type oracle model name manually.")
				}, nil
			}
			m.selectTarget = "oracle"
			m.models.SetFetching(true)
			if m.fetchModelsFn != nil {
				pid := providerID
				baseURL := m.form.FieldValue(1)
				return m, m.fetchModelsFn(pid, baseURL, apiKey), nil
			}
			m.mode = modeOracleModelSelect
			return m, nil, nil
		case 5:
			// Enter on thinking effort focuses Save button; arrows cycle
			m.form.SetFocusedBtn(1)
			return m, nil, nil
		}
	}

	if ok && keyMsg.Type == tea.KeyLeft && m.form.FocusedField() == 5 {
		m = m.cycleEffort(-1)
		m.form.SetFieldValue(5, m.thinkingEffort)
		return m, nil, nil
	}
	if ok && keyMsg.Type == tea.KeyRight && m.form.FocusedField() == 5 {
		m = m.cycleEffort(1)
		m.form.SetFieldValue(5, m.thinkingEffort)
		return m, nil, nil
	}

	updated, cmd, action := m.form.Update(msg)
	m.form = updated

	switch action {
	case form.FormActionSave:
		return m, nil, SaveEvent{Settings: m.toSettings()}
	case form.FormActionCancel:
		return m, nil, CancelEvent{}
	}

	return m, cmd, nil
}

func (m Model) cycleEffort(delta int) Model {
	efforts := []string{"none", "low", "medium", "high", "xhigh"}
	idx := 0
	for i, v := range efforts {
		if v == m.thinkingEffort {
			idx = i
			break
		}
	}
	idx += delta
	if idx < 0 {
		idx = len(efforts) - 1
	}
	if idx >= len(efforts) {
		idx = 0
	}
	m.thinkingEffort = efforts[idx]
	return m
}

func (m Model) toSettings() types.Settings {
	return types.Settings{
		ProviderID:              m.form.FieldValue(0),
		ProviderURL:             m.form.FieldValue(1),
		ProviderAPIKey:          m.form.FieldValue(2),
		Model:                   m.form.FieldValue(3),
		OracleModel:             m.form.FieldValue(4),
		ThinkingEffort:          m.thinkingEffort,
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

func (m Model) updateProviderSelect(msg tea.Msg) (Model, tea.Cmd, Event) {
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil, nil
	}

	switch keyMsg.Type {
	case tea.KeyEsc, tea.KeyBackspace:
		m.mode = modeForm
	case tea.KeyEnter:
		if selected, ok := m.provider.Selected(); ok {
			oldID := m.providerID
			m.providerID = selected.ID
			m.form.SetFieldValue(0, selected.ID)
			if selected.ID != oldID {
				m.models.SetItems(nil)
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
					if p.ID == selected.ID {
						preset = p
						break
					}
				}
				if m.getProviderConfigFn != nil {
					savedCfg := m.getProviderConfigFn(selected.ID)
					m.form.SetFieldValue(1, savedCfg.ProviderURL)
					m.form.SetFieldValue(2, savedCfg.APIKey)
					m.form.SetFieldValue(3, savedCfg.Model)
					m.form.SetFieldValue(4, savedCfg.OracleModel)
					if savedCfg.ProviderURL == "" {
						m.form.SetFieldValue(1, preset.BaseURL)
					}
				} else {
					m.form.SetFieldValue(1, preset.BaseURL)
				}
			}
		}
		m.mode = modeForm
	default:
		updated, cmd := m.provider.Update(msg)
		m.provider = updated
		return m, cmd, nil
	}

	return m, nil, nil
}

func (m Model) updateModelSelect(msg tea.Msg) (Model, tea.Cmd, Event) {
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil, nil
	}

	switch keyMsg.Type {
	case tea.KeyEsc:
		m.mode = modeForm
	case tea.KeyEnter:
		if selected, ok := m.models.Selected(); ok {
			m.handleModelSelectResult(selected, m.selectTarget)
		} else if text := m.models.TypedValue(); text != "" {
			if m.selectTarget == "model" {
				m.form.SetFieldValue(3, text)
				m.modelCtx = 0
				m.modelInPrice = 0
				m.modelOutPrice = 0
				m.modelCachePrice = 0
			} else {
				m.form.SetFieldValue(4, text)
				m.oracleCtx = 0
				m.oracleInPrice = 0
				m.oracleOutPrice = 0
				m.oracleCachePrice = 0
			}
		}
		m.mode = modeForm
	default:
		updated, cmd := m.models.Update(msg)
		m.models = updated
		return m, cmd, nil
	}

	return m, nil, nil
}

func (m Model) renderProviderSelect() string {
	m.provider.SetTitle("provider")
	var items []components.ListItem
	for _, p := range m.activePresets {
		items = append(items, components.ListItem{ID: p.ID, Label: p.Name, Detail: p.HelpText})
	}
	m.provider.SetItems(items)
	return m.provider.View(60)
}

func (m Model) renderModelSelect(spinFrame int) string {
	m.models.SetSpinFrame(spinFrame)
	if m.selectTarget == "model" {
		m.models.SetTitle("model select")
	} else {
		m.models.SetTitle("oracle model select")
	}
	return m.models.View(60)
}

func (m *Model) SetModels(models []types.ModelEntry, isOracle bool) {
	m.modelsData = models
	items := make([]components.ListItem, 0, len(models))
	for _, mod := range models {
		detail := ""
		if mod.ContextWindow > 0 {
			detail = formatCtxWindow(mod.ContextWindow)
		}
		items = append(items, components.ListItem{ID: mod.ID, Label: mod.Name, Detail: detail})
	}
	m.models.SetItems(items)
	m.models.SetFetching(false)
	if isOracle {
		m.mode = modeOracleModelSelect
	} else {
		m.mode = modeModelSelect
	}
}

func (m *Model) SetModelsError(err string) {
	m.models.SetFetchError(err)
	m.models.SetFetching(false)
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

func (m *Model) FocusField(idx int) {
	m.mode = modeForm
	m.form.FocusField(idx)
}

func (m *Model) SetFieldValue(idx int, val string) {
	m.form.SetFieldValue(idx, val)
	// also update our local thinking effort tracker
	if idx == 5 {
		m.thinkingEffort = val
	}
}

func (m Model) ToSettings() types.Settings {
	return m.toSettings()
}
