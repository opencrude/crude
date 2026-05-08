package tui

import (
	"fmt"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
)

const (
	sfProvider       = 0
	sfBaseURL        = 1
	sfAPIKey         = 2
	sfModel          = 3
	sfOracleModel    = 4
	sfThinkingEffort = 5
	sfCount          = 6
)

var thinkingEfforts = []string{"auto", "low", "medium", "high"}

func effortIndex(effort string) int {
	for i, v := range thinkingEfforts {
		if v == effort {
			return i
		}
	}
	return 0
}

func cycleEffort(current string, delta int) string {
	idx := effortIndex(current)
	idx += delta
	if idx < 0 {
		idx = len(thinkingEfforts) - 1
	}
	if idx >= len(thinkingEfforts) {
		idx = 0
	}
	return thinkingEfforts[idx]
}

type sfEntry struct {
	label  string
	value  []rune
	masked bool
}

type inlineSettings struct {
	fields       [sfCount]sfEntry
	focused      int
	providerID   string
	editingField bool
	editCursor   int
	focusedBtn   int
	snapshot     *inlineSettings

	modelContextWindow       int
	oracleModelContextWindow int

	isModelSelect   bool
	modelSearch     string
	availableModels []ModelEntry
	filteredModels  []ModelEntry
	modelListIdx    int
	modelListOffset int
	fetchingModels  bool
	fetchError      string

	isOracleModelSelect   bool
	oracleModelSearch     string
	availableOracleModels []ModelEntry
	filteredOracleModels  []ModelEntry
	oracleModelListIdx    int
	oracleModelListOffset int
	fetchingOracleModels  bool
	fetchOracleError      string

	isProviderSelect bool
	providerListIdx  int
	providerPresets  []ProviderPreset

	getProviderConfigFn func(providerID string) ProviderConfig
}

type ProviderPreset struct {
	ID       string
	Name     string
	HelpText string
	BaseURL  string
}

func newInlineSettings(s Settings, getProviderConfigFn func(providerID string) ProviderConfig) inlineSettings {
	p := inlineSettings{
		providerID:          s.ProviderID,
		getProviderConfigFn: getProviderConfigFn,
		modelContextWindow:   s.ModelContextWindow,
	}
	if p.providerID == "" {
		p.providerID = "openrouter"
	}

	set := func(idx int, label, val string, masked bool) {
		p.fields[idx] = sfEntry{label: label, value: []rune(val), masked: masked}
	}
	set(sfProvider, "provider", s.ProviderID, false)
	set(sfBaseURL, "base url", s.ProviderURL, false)
	set(sfAPIKey, "api key", s.APIKey, false)
	set(sfModel, "model", s.Model, false)
	set(sfOracleModel, "oracle model", s.OracleModel, false)
	set(sfThinkingEffort, "thinking", s.ThinkingEffort, false)

	return p
}

func (p *inlineSettings) toSettings() Settings {
	str := func(idx int) string { return strings.TrimSpace(string(p.fields[idx].value)) }
	return Settings{
		ProviderID:         p.providerID,
		ProviderURL:        str(sfBaseURL),
		ProviderAPIKey:     str(sfAPIKey),
		APIKey:             str(sfAPIKey),
		Model:              str(sfModel),
		ModelContextWindow: p.modelContextWindow,
		OracleModel:        str(sfOracleModel),
		OracleModelContextWindow: p.oracleModelContextWindow,
		ThinkingEffort:     str(sfThinkingEffort),
	}
}

func (p *inlineSettings) activeFieldCount() int {
	return sfCount
}

func (p *inlineSettings) clampFocus() {
	max := p.activeFieldCount() - 1
	if p.focused > max {
		p.focused = max
	}
	if p.focused < 0 {
		p.focused = 0
	}
}

func (p *inlineSettings) startEditing(idx int) {
	p.editingField = true
	p.editCursor = len(p.fields[idx].value)
}

func (p *inlineSettings) saveSnapshot() {
	cp := *p
	p.snapshot = &cp
}

func (p *inlineSettings) restoreSnapshot() {
	if p.snapshot != nil {
		p.fields = p.snapshot.fields
		p.providerID = p.snapshot.providerID
		p.focused = p.snapshot.focused
		p.focusedBtn = p.snapshot.focusedBtn
		p.modelContextWindow = p.snapshot.modelContextWindow
		p.oracleModelContextWindow = p.snapshot.oracleModelContextWindow
		p.isModelSelect = p.snapshot.isModelSelect
		p.isOracleModelSelect = p.snapshot.isOracleModelSelect
		p.isProviderSelect = p.snapshot.isProviderSelect
		p.editingField = false
		p.editCursor = 0
	}
}

func (p inlineSettings) Update(msg tea.Msg) (inlineSettings, tea.Cmd, bool, bool) {
	switch msg := msg.(type) {
	case ModelsListMsg:
		p.fetchingModels = false
		p.availableModels = []ModelEntry(msg)
		p.updateModelFilter()
		return p, nil, false, false

	case ModelsErrorMsg:
		p.fetchingModels = false
		p.fetchError = string(msg)
		return p, nil, false, false

	case OracleModelsListMsg:
		p.fetchingOracleModels = false
		p.availableOracleModels = []ModelEntry(msg)
		p.updateOracleModelFilter()
		return p, nil, false, false

	case OracleModelsErrorMsg:
		p.fetchingOracleModels = false
		p.fetchOracleError = string(msg)
		return p, nil, false, false

	case tea.KeyMsg:
		if p.isModelSelect {
			return p.updateModelSelect(msg)
		}
		if p.isOracleModelSelect {
			return p.updateOracleModelSelect(msg)
		}
		if p.isProviderSelect {
			return p.updateProviderSelect(msg)
		}
		if p.editingField {
			return p.updateEditing(msg)
		}
		return p.updateNav(msg)
	}

	return p, nil, false, false
}

func (p inlineSettings) updateEditing(msg tea.KeyMsg) (inlineSettings, tea.Cmd, bool, bool) {
	switch msg.Type {
	case tea.KeyEsc:
		p.editingField = false
		return p, nil, false, false
	case tea.KeyEnter:
		p.editingField = false
		p.focused++
		p.clampFocus()
		return p, nil, false, false
	case tea.KeyTab, tea.KeyShiftTab:
		p.editingField = false
		return p.updateNav(msg)
	case tea.KeyBackspace:
		f := &p.fields[p.focused]
		if p.editCursor > 0 && len(f.value) > 0 {
			f.value = append(f.value[:p.editCursor-1], f.value[p.editCursor:]...)
			p.editCursor--
		}
	case tea.KeyDelete:
		f := &p.fields[p.focused]
		if p.editCursor < len(f.value) {
			f.value = append(f.value[:p.editCursor], f.value[p.editCursor+1:]...)
		}
	case tea.KeyLeft:
		if p.editCursor > 0 {
			p.editCursor--
		}
	case tea.KeyRight:
		f := &p.fields[p.focused]
		if p.editCursor < len(f.value) {
			p.editCursor++
		}
	case tea.KeyHome:
		p.editCursor = 0
	case tea.KeyEnd:
		p.editCursor = len(p.fields[p.focused].value)
	default:
		for _, r := range msg.Runes {
			if unicode.IsPrint(r) {
				f := &p.fields[p.focused]
				f.value = append(f.value[:p.editCursor], append([]rune{r}, f.value[p.editCursor:]...)...)
				p.editCursor++
			}
		}
	}
	return p, nil, false, false
}

func (p inlineSettings) updateNav(msg tea.KeyMsg) (inlineSettings, tea.Cmd, bool, bool) {
	switch msg.Type {
	case tea.KeyCtrlS:
		return p, nil, true, false
	case tea.KeyEsc:
		return p, nil, false, true
	case tea.KeyUp, tea.KeyShiftTab:
		if p.focusedBtn == 2 {
			if msg.Type == tea.KeyShiftTab {
				p.focusedBtn = 1
			} else {
				p.focusedBtn = 0
				p.focused = p.activeFieldCount() - 1
			}
		} else if p.focusedBtn == 1 {
			p.focusedBtn = 0
			p.focused = p.activeFieldCount() - 1
		} else {
			if p.focused == 0 {
				p.focusedBtn = 2
			} else {
				p.focused--
			}
		}
	case tea.KeyDown, tea.KeyTab:
		if p.focusedBtn == 0 {
			if p.focused >= p.activeFieldCount()-1 {
				p.focusedBtn = 1
			} else {
				p.focused++
			}
		} else if p.focusedBtn == 1 {
			if msg.Type == tea.KeyTab {
				p.focusedBtn = 2
			} else {
				p.focusedBtn = 0
				p.focused = 0
			}
		} else if p.focusedBtn == 2 {
			p.focusedBtn = 0
			p.focused = 0
		}
	case tea.KeyEnter:
		if p.focusedBtn > 0 {
			if p.focusedBtn == 1 {
				return p, nil, true, false
			}
			if p.focusedBtn == 2 {
				return p, nil, false, true
			}
			p.focusedBtn = 0
			return p, nil, false, false
		}
		switch p.focused {
		case sfProvider:
			p.isProviderSelect = true
			p.providerListIdx = 0
			for i, preset := range p.providerPresets {
				if preset.ID == p.providerID {
					p.providerListIdx = i
					break
				}
			}
		case sfThinkingEffort:
			p.focusedBtn = 1
		case sfModel:
			if p.providerID == "ollama" || p.providerID == "custom" {
				p.startEditing(sfModel)
				msgText := fmt.Sprintf("Type model name manually for %s.", p.providerID)
				return p, func() tea.Msg { return errorMsg(msgText) }, false, false
			}
			if string(p.fields[sfAPIKey].value) == "" && p.providerID != "openrouter" {
				p.startEditing(sfModel)
				msgText := fmt.Sprintf("No API key provided for %s. Type model name manually.", p.providerID)
				return p, func() tea.Msg { return errorMsg(msgText) }, false, false
			}

			p.isModelSelect = true
			if len(p.availableModels) == 0 && !p.fetchingModels {
				p.fetchError = ""
				p.fetchingModels = true
				current := fetchModelsMsg{
					ProviderID: p.providerID,
					BaseURL:    strings.TrimSpace(string(p.fields[sfBaseURL].value)),
					APIKey:     strings.TrimSpace(string(p.fields[sfAPIKey].value)),
				}
				return p, func() tea.Msg { return current }, false, false
			}
			p.updateModelFilter()
		case sfOracleModel:
			if p.providerID == "ollama" || p.providerID == "custom" {
				p.startEditing(sfOracleModel)
				msgText := fmt.Sprintf("Type oracle model name manually for %s.", p.providerID)
				return p, func() tea.Msg { return errorMsg(msgText) }, false, false
			}
			if string(p.fields[sfAPIKey].value) == "" && p.providerID != "openrouter" {
				p.startEditing(sfOracleModel)
				msgText := fmt.Sprintf("No API key provided for %s. Type oracle model name manually.", p.providerID)
				return p, func() tea.Msg { return errorMsg(msgText) }, false, false
			}

			p.isOracleModelSelect = true
			if len(p.availableOracleModels) == 0 && !p.fetchingOracleModels {
				p.fetchOracleError = ""
				p.fetchingOracleModels = true
				current := fetchModelsMsg{
					ProviderID: p.providerID,
					BaseURL:    strings.TrimSpace(string(p.fields[sfBaseURL].value)),
					APIKey:     strings.TrimSpace(string(p.fields[sfAPIKey].value)),
				}
				return p, func() tea.Msg { return current }, false, false
			}
			p.updateOracleModelFilter()
		default:
			if p.focusedBtn == 0 {
				if p.fields[p.focused].masked {
					p.fields[p.focused].value = nil
				}
				p.startEditing(p.focused)
			}
		}
	case tea.KeyLeft, tea.KeyRight:
		if p.focusedBtn > 0 {
			if p.focusedBtn == 1 && msg.Type == tea.KeyRight {
				p.focusedBtn = 2
			} else if p.focusedBtn == 2 && msg.Type == tea.KeyLeft {
				p.focusedBtn = 1
			}
		} else if p.focused == sfThinkingEffort {
			delta := 1
			if msg.Type == tea.KeyLeft {
				delta = -1
			}
			p.fields[sfThinkingEffort].value = []rune(cycleEffort(string(p.fields[sfThinkingEffort].value), delta))
		}
	default:
		s := msg.String()
		switch s {
		case "j":
			if p.focusedBtn == 0 {
				if p.focused >= p.activeFieldCount()-1 {
					p.focusedBtn = 1
				} else {
					p.focused++
				}
			} else if p.focusedBtn == 1 {
				p.focusedBtn = 0
				p.focused = 0
			} else if p.focusedBtn == 2 {
				p.focusedBtn = 0
				p.focused = 0
			}
		case "k":
			if p.focusedBtn == 2 {
				p.focusedBtn = 0
				p.focused = p.activeFieldCount() - 1
			} else if p.focusedBtn == 1 {
				p.focusedBtn = 0
				p.focused = p.activeFieldCount() - 1
			} else {
				if p.focused == 0 {
					p.focusedBtn = 2
				} else {
					p.focused--
				}
			}
		case "e":
			switch p.focused {
			case sfModel:
				if p.providerID == "ollama" || p.providerID == "custom" {
					p.startEditing(sfModel)
					msgText := fmt.Sprintf("Type model name manually for %s.", p.providerID)
					return p, func() tea.Msg { return errorMsg(msgText) }, false, false
				}
				if string(p.fields[sfAPIKey].value) == "" && p.providerID != "openrouter" {
					p.startEditing(sfModel)
					msgText := fmt.Sprintf("No API key provided for %s. Type model name manually.", p.providerID)
					return p, func() tea.Msg { return errorMsg(msgText) }, false, false
				}

				p.isModelSelect = true
				if len(p.availableModels) == 0 && !p.fetchingModels {
					p.fetchError = ""
					p.fetchingModels = true
					current := fetchModelsMsg{
						ProviderID: p.providerID,
						BaseURL:    strings.TrimSpace(string(p.fields[sfBaseURL].value)),
						APIKey:     strings.TrimSpace(string(p.fields[sfAPIKey].value)),
					}
					return p, func() tea.Msg { return current }, false, false
				}
				p.updateModelFilter()
			case sfOracleModel:
				if p.providerID == "ollama" || p.providerID == "custom" {
					p.startEditing(sfOracleModel)
					msgText := fmt.Sprintf("Type oracle model name manually for %s.", p.providerID)
					return p, func() tea.Msg { return errorMsg(msgText) }, false, false
				}
				if string(p.fields[sfAPIKey].value) == "" && p.providerID != "openrouter" {
					p.startEditing(sfOracleModel)
					msgText := fmt.Sprintf("No API key provided for %s. Type oracle model name manually.", p.providerID)
					return p, func() tea.Msg { return errorMsg(msgText) }, false, false
				}

				p.isOracleModelSelect = true
				if len(p.availableOracleModels) == 0 && !p.fetchingOracleModels {
					p.fetchOracleError = ""
					p.fetchingOracleModels = true
					current := fetchModelsMsg{
						ProviderID: p.providerID,
						BaseURL:    strings.TrimSpace(string(p.fields[sfBaseURL].value)),
						APIKey:     strings.TrimSpace(string(p.fields[sfAPIKey].value)),
					}
					return p, func() tea.Msg { return current }, false, false
				}
				p.updateOracleModelFilter()
			default:
				if p.fields[p.focused].masked {
					p.fields[p.focused].value = nil
				}
				p.startEditing(p.focused)
				for _, r := range msg.Runes {
					if unicode.IsPrint(r) {
						p.fields[p.focused].value = append(p.fields[p.focused].value, r)
					}
				}
			}
		}
	}
	return p, nil, false, false
}
