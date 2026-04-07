package tui

import (
	"fmt"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
)

// fields for inline settings
const (
	sfProvider    = 0
	sfBaseURL     = 1
	sfAPIKey      = 2
	sfModel       = 3
	sfEmbedModel  = 4
	sfBackend     = 5
	sfSSHHost     = 6
	sfSSHPort     = 7
	sfSSHUser     = 8
	sfSSHPassword = 9
	sfSSHKeyFile  = 10
	sfCount       = 11
)

type sfEntry struct {
	label  string
	value  []rune
	masked bool
}

type inlineSettings struct {
	fields       [sfCount]sfEntry
	focused      int
	backend      string
	providerID   string
	editingField bool
	focusedBtn   int

	// Model selection state
	isModelSelect   bool
	modelSearch     string
	availableModels []ModelEntry
	filteredModels  []ModelEntry
	modelListIdx    int
	modelListOffset int // scroll offset for visible window
	fetchingModels  bool
	fetchError      string

	// Embed model selection state
	isEmbedModelSelect   bool
	embedModelSearch     string
	availableEmbedModels []ModelEntry
	filteredEmbedModels  []ModelEntry
	embedModelListIdx    int
	embedModelListOffset int
	fetchingEmbedModels  bool
	fetchEmbedError      string

	// Provider selection state
	isProviderSelect bool
	providerListIdx  int
	providerPresets  []ProviderPreset
}

// ProviderPreset is a pre-configured provider for the TUI selector.
type ProviderPreset struct {
	ID       string
	Name     string
	HelpText string
	BaseURL  string
}

func newInlineSettings(s Settings) inlineSettings {
	p := inlineSettings{
		backend:    s.Backend,
		providerID: s.ProviderID,
	}
	if p.backend == "" {
		p.backend = "local"
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
	set(sfEmbedModel, "embed model", s.EmbedModel, false)
	set(sfBackend, "backend", s.Backend, false)
	set(sfSSHHost, "host", s.SSHHost, false)
	set(sfSSHPort, "port", s.SSHPort, false)
	set(sfSSHUser, "user", s.SSHUser, false)
	set(sfSSHPassword, "password", s.SSHPassword, false)
	set(sfSSHKeyFile, "key file", s.SSHKeyFile, false)

	return p
}

func (p *inlineSettings) toSettings() Settings {
	str := func(idx int) string { return strings.TrimSpace(string(p.fields[idx].value)) }
	return Settings{
		ProviderID:     p.providerID,
		ProviderURL:    str(sfBaseURL),
		ProviderAPIKey: str(sfAPIKey),
		APIKey:         str(sfAPIKey),
		Model:          str(sfModel),
		EmbedModel:     str(sfEmbedModel),
		Backend:        p.backend,
		SSHHost:        str(sfSSHHost),
		SSHPort:        str(sfSSHPort),
		SSHUser:        str(sfSSHUser),
		SSHPassword:    str(sfSSHPassword),
		SSHKeyFile:     str(sfSSHKeyFile),
	}
}

func (p *inlineSettings) activeFieldCount() int {
	if p.backend == "ssh" {
		return sfCount
	}
	return sfBackend + 1
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

func (p *inlineSettings) toggleBackend() {
	if p.backend == "local" {
		p.backend = "ssh"
	} else {
		p.backend = "local"
	}
	p.clampFocus()
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

	case EmbedModelsListMsg:
		p.fetchingEmbedModels = false
		p.availableEmbedModels = []ModelEntry(msg)
		p.updateEmbedModelFilter()
		return p, nil, false, false

	case EmbedModelsErrorMsg:
		p.fetchingEmbedModels = false
		p.fetchEmbedError = string(msg)
		return p, nil, false, false

	case tea.KeyMsg:
		if p.isModelSelect {
			return p.updateModelSelect(msg)
		}
		if p.isEmbedModelSelect {
			return p.updateEmbedModelSelect(msg)
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

// updateEditing handles text input for a specific field
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
	case tea.KeyBackspace, tea.KeyDelete:
		f := &p.fields[p.focused]
		if len(f.value) > 0 {
			f.value = f.value[:len(f.value)-1]
		}
	default:
		for _, r := range msg.Runes {
			if unicode.IsPrint(r) {
				f := &p.fields[p.focused]
				f.value = append(f.value, r)
			}
		}
	}
	return p, nil, false, false
}

// updateNav handles up/down navigation and entering edit mode
func (p inlineSettings) updateNav(msg tea.KeyMsg) (inlineSettings, tea.Cmd, bool, bool) {
	switch msg.Type {
	case tea.KeyCtrlS, tea.KeyEsc:
		return p, nil, true, true
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
			// Initialize cursor to current provider
			p.providerListIdx = 0
			for i, preset := range p.providerPresets {
				if preset.ID == p.providerID {
					p.providerListIdx = i
					break
				}
			}
		case sfBackend:
			p.toggleBackend()
		case sfModel:
			if p.providerID == "ollama" || p.providerID == "custom" {
				p.editingField = true
				msgText := fmt.Sprintf("Type model name manually for %s.", p.providerID)
				return p, func() tea.Msg { return errorMsg(msgText) }, false, false
			}
			if string(p.fields[sfAPIKey].value) == "" && p.providerID != "openrouter" {
				p.editingField = true
				msgText := fmt.Sprintf("No API key provided for %s. Type model name manually.", p.providerID)
				return p, func() tea.Msg { return errorMsg(msgText) }, false, false
			}

			p.isModelSelect = true
			if len(p.availableModels) == 0 && !p.fetchingModels {
				// Clear any stale error and trigger a fresh fetch
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
		case sfEmbedModel:
			if p.providerID == "ollama" || p.providerID == "custom" {
				p.editingField = true
				msgText := fmt.Sprintf("Type embedding model name manually for %s.", p.providerID)
				return p, func() tea.Msg { return errorMsg(msgText) }, false, false
			}
			if string(p.fields[sfAPIKey].value) == "" && p.providerID != "openrouter" {
				p.editingField = true
				msgText := fmt.Sprintf("No API key provided for %s. Type embedding model name manually.", p.providerID)
				return p, func() tea.Msg { return errorMsg(msgText) }, false, false
			}

			p.isEmbedModelSelect = true
			if len(p.availableEmbedModels) == 0 && !p.fetchingEmbedModels {
				p.fetchEmbedError = ""
				p.fetchingEmbedModels = true
				current := fetchModelsMsg{
					ProviderID: p.providerID,
					BaseURL:    strings.TrimSpace(string(p.fields[sfBaseURL].value)),
					APIKey:     strings.TrimSpace(string(p.fields[sfAPIKey].value)),
					IsEmbed:    true,
				}
				return p, func() tea.Msg { return current }, false, false
			}
			p.updateEmbedModelFilter()
		default:
			if p.focusedBtn == 0 {
				// Clear masked fields before entering edit so user types fresh value
				if p.fields[p.focused].masked {
					p.fields[p.focused].value = nil
				}
				p.editingField = true
			}
		}
	case tea.KeyLeft, tea.KeyRight:
		if p.focusedBtn > 0 {
			if p.focusedBtn == 1 && msg.Type == tea.KeyRight {
				p.focusedBtn = 2
			} else if p.focusedBtn == 2 && msg.Type == tea.KeyLeft {
				p.focusedBtn = 1
			}
		} else if p.focused == sfBackend {
			p.toggleBackend()
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
			// vim shortcut to edit
			switch p.focused {
			case sfBackend:
				p.toggleBackend()
			case sfModel:
				if p.providerID == "ollama" || p.providerID == "custom" {
					p.editingField = true
					msgText := fmt.Sprintf("Type model name manually for %s.", p.providerID)
					return p, func() tea.Msg { return errorMsg(msgText) }, false, false
				}
				if string(p.fields[sfAPIKey].value) == "" && p.providerID != "openrouter" {
					p.editingField = true
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
			case sfEmbedModel:
				if p.providerID == "ollama" || p.providerID == "custom" {
					p.editingField = true
					msgText := fmt.Sprintf("Type embedding model name manually for %s.", p.providerID)
					return p, func() tea.Msg { return errorMsg(msgText) }, false, false
				}
				if string(p.fields[sfAPIKey].value) == "" && p.providerID != "openrouter" {
					p.editingField = true
					msgText := fmt.Sprintf("No API key provided for %s. Type embedding model name manually.", p.providerID)
					return p, func() tea.Msg { return errorMsg(msgText) }, false, false
				}

				p.isEmbedModelSelect = true
				if len(p.availableEmbedModels) == 0 && !p.fetchingEmbedModels {
					p.fetchEmbedError = ""
					p.fetchingEmbedModels = true
					current := fetchModelsMsg{
						ProviderID: p.providerID,
						BaseURL:    strings.TrimSpace(string(p.fields[sfBaseURL].value)),
						APIKey:     strings.TrimSpace(string(p.fields[sfAPIKey].value)),
						IsEmbed:    true,
					}
					return p, func() tea.Msg { return current }, false, false
				}
				p.updateEmbedModelFilter()
			default:
				// Clear masked fields before entering edit so user types fresh value
				if p.fields[p.focused].masked {
					p.fields[p.focused].value = nil
				}
				p.editingField = true
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
