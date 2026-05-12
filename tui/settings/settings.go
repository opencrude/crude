package settings

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"crude/tui/types"
	"crude/tui/widget"
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
	theme    *widget.Theme
	form     widget.Form
	provider widget.List
	models   widget.List
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

	thinkingEffort string
}

func NewModel(t *widget.Theme, initial types.Settings, presets []types.ProviderPreset, getProviderConfigFn func(string) types.ProviderConfig, fetchModelsFn func(string, string, string) tea.Cmd) Model {
	fields := []widget.Field{
		{Label: "provider", Value: []rune(initial.ProviderID)},
		{Label: "base url", Value: []rune(initial.ProviderURL)},
		{Label: "api key", Value: []rune(initial.ProviderAPIKey), Masked: true},
		{Label: "model", Value: []rune(initial.Model)},
		{Label: "oracle model", Value: []rune(initial.OracleModel)},
		{Label: "thinking effort", Value: []rune(initial.ThinkingEffort)},
	}

	return Model{
		theme:               t,
		form:                widget.NewForm(t, fields...),
		provider:            widget.NewList(t, 12),
		models:              widget.NewList(t, 12),
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
		return m.renderModelSelect()
	default:
		return m.form.View(spinFrame)
	}
}

func (m *Model) SetPresets(presets []types.ProviderPreset) {
	m.activePresets = presets
}

func (m *Model) SaveSnapshot() {
	m.form.SaveSnapshot()
}

func (m *Model) RestoreSnapshot() {
	m.form.RestoreSnapshot()
	m.mode = modeForm
}

func (m Model) updateForm(msg tea.Msg) (Model, tea.Cmd, Event) {
	keyMsg, ok := msg.(tea.KeyMsg)
	if ok && keyMsg.Type == tea.KeyEnter && m.form.FocusedField() >= 0 {
		switch m.form.FocusedField() {
		case 0:
			items := make([]widget.ListItem, len(m.activePresets))
			for i, p := range m.activePresets {
				items[i] = widget.ListItem{ID: p.ID, Label: p.Name, Detail: p.HelpText}
			}
			m.provider.SetItems(items)
			m.mode = modeProviderSelect
			return m, nil, nil
		case 3:
			m.selectTarget = "model"
			m.models.SetFetching(true)
			if m.fetchModelsFn != nil {
				pid := m.form.FieldValue(0)
				baseURL := m.form.FieldValue(1)
				apiKey := m.form.FieldValue(2)
				return m, m.fetchModelsFn(pid, baseURL, apiKey), nil
			}
			m.mode = modeModelSelect
			return m, nil, nil
		case 4:
			m.selectTarget = "oracle"
			m.models.SetFetching(true)
			if m.fetchModelsFn != nil {
				pid := m.form.FieldValue(0)
				baseURL := m.form.FieldValue(1)
				apiKey := m.form.FieldValue(2)
				return m, m.fetchModelsFn(pid, baseURL, apiKey), nil
			}
			m.mode = modeOracleModelSelect
			return m, nil, nil
		case 5:
			m = m.cycleEffort(1)
			m.form.SetFieldValue(5, m.thinkingEffort)
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
	case widget.FormActionSave:
		return m, nil, SaveEvent{Settings: m.toSettings()}
	case widget.FormActionCancel:
		return m, nil, CancelEvent{}
	}

	return m, cmd, nil
}

func (m Model) cycleEffort(delta int) Model {
	_ = m.thinkingEffort
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
	case tea.KeyEsc:
		m.mode = modeForm
	case tea.KeyEnter:
		if selected, ok := m.provider.Selected(); ok {
			oldID := m.providerID
			m.providerID = selected.ID
			m.form.SetFieldValue(0, selected.ID)
			if selected.ID != oldID {
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
			if m.selectTarget == "model" {
				m.form.SetFieldValue(3, selected.ID)
			} else {
				m.form.SetFieldValue(4, selected.ID)
			}
		} else if text := m.models.TypedValue(); text != "" {
			if m.selectTarget == "model" {
				m.form.SetFieldValue(3, text)
			} else {
				m.form.SetFieldValue(4, text)
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
	var items []widget.ListItem
	for _, p := range m.activePresets {
		items = append(items, widget.ListItem{ID: p.ID, Label: p.Name, Detail: p.HelpText})
	}
	m.provider.SetItems(items)
	return m.provider.View(60)
}

func (m Model) renderModelSelect() string {
	return m.models.View(60)
}

func (m *Model) SetModels(models []types.ModelEntry, isOracle bool) {
	items := make([]widget.ListItem, len(models))
	for _, mod := range models {
		detail := ""
		if mod.ContextWindow > 0 {
			detail = fmt.Sprintf("%dK", mod.ContextWindow/1000)
		}
		items = append(items, widget.ListItem{ID: mod.ID, Label: mod.Name, Detail: detail})
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

func (m *Model) FocusField(idx int) {
	m.mode = modeForm
	m.form.FocusField(idx)
}
