package tui

import (
	"fmt"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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

func (p inlineSettings) updateModelSelect(msg tea.KeyMsg) (inlineSettings, tea.Cmd, bool, bool) {
	const pageSize = 12
	switch msg.Type {
	case tea.KeyEsc:
		p.isModelSelect = false
		p.modelSearch = ""
		return p, nil, false, false
	case tea.KeyEnter:
		if !p.fetchingModels && len(p.filteredModels) > 0 && p.modelListIdx >= 0 && p.modelListIdx < len(p.filteredModels) {
			p.fields[sfModel].value = []rune(p.filteredModels[p.modelListIdx].ID)
		} else if p.modelSearch != "" {
			p.fields[sfModel].value = []rune(p.modelSearch)
		}
		p.isModelSelect = false
		p.modelSearch = ""
		return p, nil, false, false
	case tea.KeyUp:
		if len(p.filteredModels) > 0 {
			p.modelListIdx--
			if p.modelListIdx < 0 {
				p.modelListIdx = len(p.filteredModels) - 1
			}
			p.clampModelOffset(pageSize)
		}
		return p, nil, false, false
	case tea.KeyDown:
		if len(p.filteredModels) > 0 {
			p.modelListIdx++
			if p.modelListIdx >= len(p.filteredModels) {
				p.modelListIdx = 0
			}
			p.clampModelOffset(pageSize)
		}
		return p, nil, false, false
	case tea.KeyPgUp:
		p.modelListIdx -= pageSize
		if p.modelListIdx < 0 {
			p.modelListIdx = 0
		}
		p.clampModelOffset(pageSize)
		return p, nil, false, false
	case tea.KeyPgDown:
		p.modelListIdx += pageSize
		if p.modelListIdx >= len(p.filteredModels) {
			p.modelListIdx = len(p.filteredModels) - 1
		}
		p.clampModelOffset(pageSize)
		return p, nil, false, false
	case tea.KeyBackspace, tea.KeyDelete:
		if len(p.modelSearch) > 0 {
			runes := []rune(p.modelSearch)
			p.modelSearch = string(runes[:len(runes)-1])
			p.updateModelFilter()
		}
		return p, nil, false, false
	default:
		for _, r := range msg.Runes {
			if unicode.IsPrint(r) {
				p.modelSearch += string(r)
				p.updateModelFilter()
			}
		}
	}
	return p, nil, false, false
}

func (p *inlineSettings) updateModelFilter() {
	p.filteredModels = nil
	p.modelListIdx = 0
	p.modelListOffset = 0
	s := strings.ToLower(p.modelSearch)
	for _, m := range p.availableModels {
		if strings.Contains(strings.ToLower(m.ID), s) || strings.Contains(strings.ToLower(m.Name), s) {
			p.filteredModels = append(p.filteredModels, m)
		}
	}
}

func (p *inlineSettings) updateEmbedModelFilter() {
	p.filteredEmbedModels = nil
	p.embedModelListIdx = 0
	p.embedModelListOffset = 0
	s := strings.ToLower(p.embedModelSearch)
	for _, m := range p.availableEmbedModels {
		if strings.Contains(strings.ToLower(m.ID), s) || strings.Contains(strings.ToLower(m.Name), s) {
			p.filteredEmbedModels = append(p.filteredEmbedModels, m)
		}
	}
}

func (p inlineSettings) updateEmbedModelSelect(msg tea.KeyMsg) (inlineSettings, tea.Cmd, bool, bool) {
	const pageSize = 12
	switch msg.Type {
	case tea.KeyEsc:
		p.isEmbedModelSelect = false
		p.embedModelSearch = ""
		return p, nil, false, false
	case tea.KeyEnter:
		if !p.fetchingEmbedModels && len(p.filteredEmbedModels) > 0 && p.embedModelListIdx >= 0 && p.embedModelListIdx < len(p.filteredEmbedModels) {
			p.fields[sfEmbedModel].value = []rune(p.filteredEmbedModels[p.embedModelListIdx].ID)
		} else if p.embedModelSearch != "" {
			p.fields[sfEmbedModel].value = []rune(p.embedModelSearch)
		}
		p.isEmbedModelSelect = false
		p.embedModelSearch = ""
		return p, nil, false, false
	case tea.KeyUp:
		if len(p.filteredEmbedModels) > 0 {
			p.embedModelListIdx--
			if p.embedModelListIdx < 0 {
				p.embedModelListIdx = len(p.filteredEmbedModels) - 1
			}
			p.clampEmbedModelOffset(pageSize)
		}
		return p, nil, false, false
	case tea.KeyDown:
		if len(p.filteredEmbedModels) > 0 {
			p.embedModelListIdx++
			if p.embedModelListIdx >= len(p.filteredEmbedModels) {
				p.embedModelListIdx = 0
			}
			p.clampEmbedModelOffset(pageSize)
		}
		return p, nil, false, false
	case tea.KeyPgUp:
		p.embedModelListIdx -= pageSize
		if p.embedModelListIdx < 0 {
			p.embedModelListIdx = 0
		}
		p.clampEmbedModelOffset(pageSize)
		return p, nil, false, false
	case tea.KeyPgDown:
		p.embedModelListIdx += pageSize
		if p.embedModelListIdx >= len(p.filteredEmbedModels) {
			p.embedModelListIdx = len(p.filteredEmbedModels) - 1
		}
		p.clampEmbedModelOffset(pageSize)
		return p, nil, false, false
	case tea.KeyBackspace, tea.KeyDelete:
		if len(p.embedModelSearch) > 0 {
			runes := []rune(p.embedModelSearch)
			p.embedModelSearch = string(runes[:len(runes)-1])
			p.updateEmbedModelFilter()
		}
		return p, nil, false, false
	default:
		for _, r := range msg.Runes {
			if unicode.IsPrint(r) {
				p.embedModelSearch += string(r)
				p.updateEmbedModelFilter()
			}
		}
	}
	return p, nil, false, false
}

func (p *inlineSettings) clampEmbedModelOffset(pageSize int) {
	if p.embedModelListIdx < p.embedModelListOffset {
		p.embedModelListOffset = p.embedModelListIdx
	}
	if p.embedModelListIdx >= p.embedModelListOffset+pageSize {
		p.embedModelListOffset = p.embedModelListIdx - pageSize + 1
	}
	if p.embedModelListOffset < 0 {
		p.embedModelListOffset = 0
	}
}

func (p *inlineSettings) clampModelOffset(pageSize int) {
	if p.modelListIdx < p.modelListOffset {
		p.modelListOffset = p.modelListIdx
	}
	if p.modelListIdx >= p.modelListOffset+pageSize {
		p.modelListOffset = p.modelListIdx - pageSize + 1
	}
	if p.modelListOffset < 0 {
		p.modelListOffset = 0
	}
}

func (p inlineSettings) updateProviderSelect(msg tea.KeyMsg) (inlineSettings, tea.Cmd, bool, bool) {
	switch msg.Type {
	case tea.KeyEsc, tea.KeyBackspace:
		p.isProviderSelect = false
		return p, nil, false, false
	case tea.KeyEnter:
		if p.providerListIdx >= 0 && p.providerListIdx < len(p.providerPresets) {
			selected := p.providerPresets[p.providerListIdx]
			if selected.ID != p.providerID {
				// Provider changed — clear stale model data and reset model field
				p.availableModels = nil
				p.filteredModels = nil
				p.fetchError = ""
				p.fetchingModels = false
				p.fields[sfModel].value = nil

				p.availableEmbedModels = nil
				p.filteredEmbedModels = nil
				p.fetchEmbedError = ""
				p.fetchingEmbedModels = false
				p.fields[sfEmbedModel].value = nil
			}
			p.providerID = selected.ID
			p.fields[sfProvider].value = []rune(selected.ID)
			p.fields[sfBaseURL].value = []rune(selected.BaseURL)
		}
		p.isProviderSelect = false
		return p, nil, false, false
	case tea.KeyUp:
		p.providerListIdx--
		if p.providerListIdx < 0 {
			p.providerListIdx = len(p.providerPresets) - 1
		}
		return p, nil, false, false
	case tea.KeyDown:
		p.providerListIdx++
		if p.providerListIdx >= len(p.providerPresets) {
			p.providerListIdx = 0
		}
		return p, nil, false, false
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

func (p *inlineSettings) toggleBackend() {
	if p.backend == "local" {
		p.backend = "ssh"
	} else {
		p.backend = "local"
	}
	p.clampFocus()
}

// View returns the inline settings string for the chat area
func (p *inlineSettings) View() string {
	dim := lipgloss.NewStyle().Foreground(colorMuted)
	var b strings.Builder

	if p.isProviderSelect {
		b.WriteString(lipgloss.NewStyle().Foreground(colorAccent).Render("provider select"))
		b.WriteString("\n")
		for i, preset := range p.providerPresets {
			indicator := "  "
			if i == p.providerListIdx {
				indicator = lipgloss.NewStyle().Foreground(colorAccent).Render("> ")
			}
			name := lipgloss.NewStyle().Foreground(colorMainText).Render(fmt.Sprintf("%-16s", preset.Name))
			help := dim.Render(preset.HelpText)
			b.WriteString(indicator + name + " " + help + "\n")
		}
		b.WriteString("\n" + dim.Render("arrows navigate / enter select / esc or backspace cancel") + "\n")
		return b.String()
	}

	if p.isModelSelect {
		const pageSize = 12
		accentStyle := lipgloss.NewStyle().Foreground(colorAccent)
		errorStyle := lipgloss.NewStyle().Foreground(colorError)
		highlightStyle := lipgloss.NewStyle().Foreground(colorMainText).Background(lipgloss.Color("#1e2a3a"))

		// ── Header line ──────────────────────────────────────────
		b.WriteString(accentStyle.Bold(true).Render("  model select"))
		if p.fetchingModels {
			b.WriteString(dim.Render("  fetching models..."))
		} else if len(p.availableModels) > 0 {
			b.WriteString(dim.Render(fmt.Sprintf("  %d/%d", len(p.filteredModels), len(p.availableModels))))
		}
		b.WriteString("\n")

		// ── Search bar ───────────────────────────────────────────
		if p.fetchError != "" {
			b.WriteString(errorStyle.Render("  ✗ "+p.fetchError) + "\n")
		} else {
			searchPrompt := accentStyle.Render("  / ")
			searchText := lipgloss.NewStyle().Foreground(colorMainText).Render(p.modelSearch)
			cursor := dim.Render("▌")
			b.WriteString(searchPrompt + searchText + cursor + "\n")
		}

		b.WriteString(dim.Render("  "+strings.Repeat("─", 54)) + "\n")

		// ── Model list with scrolling ─────────────────────────────
		if p.fetchingModels {
			b.WriteString(dim.Render("  waiting for models...") + "\n")
		} else if len(p.filteredModels) == 0 {
			if p.modelSearch != "" {
				b.WriteString(dim.Render("  no matches — press enter to use typed value") + "\n")
			} else {
				b.WriteString(dim.Render("  no models available") + "\n")
			}
		} else {
			end := p.modelListOffset + pageSize
			if end > len(p.filteredModels) {
				end = len(p.filteredModels)
			}
			for i := p.modelListOffset; i < end; i++ {
				m := p.filteredModels[i]
				selected := i == p.modelListIdx

				idStr := m.ID
				if len([]rune(idStr)) > 44 {
					idStr = string([]rune(idStr)[:41]) + "…"
				}

				var line string
				if selected {
					line = accentStyle.Render(" ❯ ") + highlightStyle.Render(fmt.Sprintf("%-44s", idStr))
				} else {
					line = dim.Render("   ") + lipgloss.NewStyle().Foreground(colorFg).Render(fmt.Sprintf("%-44s", idStr))
				}
				b.WriteString(line + "\n")
			}
		}

		b.WriteString(dim.Render("  "+strings.Repeat("─", 54)) + "\n")
		footer := "  ↑↓ navigate · enter select · esc cancel"
		if !p.fetchingModels && len(p.filteredModels) > 0 {
			end := p.modelListOffset + pageSize
			if end > len(p.filteredModels) {
				end = len(p.filteredModels)
			}
			if p.modelListOffset > 0 || end < len(p.filteredModels) {
				scrollHint := fmt.Sprintf("  %d–%d of %d", p.modelListOffset+1, end, len(p.filteredModels))
				if p.modelListOffset > 0 {
					scrollHint += " · ↑pgup"
				}
				if end < len(p.filteredModels) {
					scrollHint += " · ↓pgdn"
				}
				footer = scrollHint + " · enter select · esc cancel"
			}
		}
		b.WriteString(dim.Render(footer) + "\n")
		if p.fetchingModels && p.modelSearch != "" {
			b.WriteString(dim.Render("  enter will use: ") + lipgloss.NewStyle().Foreground(colorMainText).Render(p.modelSearch) + "\n")
		}
		return b.String()
	}

	if p.isEmbedModelSelect {
		const pageSize = 12
		accentStyle := lipgloss.NewStyle().Foreground(colorAccent)
		errorStyle := lipgloss.NewStyle().Foreground(colorError)
		highlightStyle := lipgloss.NewStyle().Foreground(colorMainText).Background(lipgloss.Color("#1e2a3a"))

		b.WriteString(accentStyle.Bold(true).Render("  embed model select"))
		if p.fetchingEmbedModels {
			b.WriteString(dim.Render("  fetching models..."))
		} else if len(p.availableEmbedModels) > 0 {
			b.WriteString(dim.Render(fmt.Sprintf("  %d/%d", len(p.filteredEmbedModels), len(p.availableEmbedModels))))
		}
		b.WriteString("\n")

		if p.fetchEmbedError != "" {
			b.WriteString(errorStyle.Render("  ✗ "+p.fetchEmbedError) + "\n")
		} else {
			searchPrompt := accentStyle.Render("  / ")
			searchText := lipgloss.NewStyle().Foreground(colorMainText).Render(p.embedModelSearch)
			cursor := dim.Render("▌")
			b.WriteString(searchPrompt + searchText + cursor + "\n")
		}

		b.WriteString(dim.Render("  "+strings.Repeat("─", 54)) + "\n")

		if p.fetchingEmbedModels {
			b.WriteString(dim.Render("  waiting for models...") + "\n")
		} else if len(p.filteredEmbedModels) == 0 {
			if p.embedModelSearch != "" {
				b.WriteString(dim.Render("  no matches — press enter to use typed value") + "\n")
			} else {
				b.WriteString(dim.Render("  no models available") + "\n")
			}
		} else {
			end := p.embedModelListOffset + pageSize
			if end > len(p.filteredEmbedModels) {
				end = len(p.filteredEmbedModels)
			}
			for i := p.embedModelListOffset; i < end; i++ {
				m := p.filteredEmbedModels[i]
				selected := i == p.embedModelListIdx

				idStr := m.ID
				if len([]rune(idStr)) > 44 {
					idStr = string([]rune(idStr)[:41]) + "…"
				}

				var line string
				if selected {
					line = accentStyle.Render(" ❯ ") + highlightStyle.Render(fmt.Sprintf("%-44s", idStr))
				} else {
					line = dim.Render("   ") + lipgloss.NewStyle().Foreground(colorFg).Render(fmt.Sprintf("%-44s", idStr))
				}
				b.WriteString(line + "\n")
			}
		}

		b.WriteString(dim.Render("  "+strings.Repeat("─", 54)) + "\n")
		footer := "  ↑↓ navigate · enter select · esc cancel"
		if !p.fetchingEmbedModels && len(p.filteredEmbedModels) > 0 {
			end := p.embedModelListOffset + pageSize
			if end > len(p.filteredEmbedModels) {
				end = len(p.filteredEmbedModels)
			}
			if p.embedModelListOffset > 0 || end < len(p.filteredEmbedModels) {
				scrollHint := fmt.Sprintf("  %d–%d of %d", p.embedModelListOffset+1, end, len(p.filteredEmbedModels))
				if p.embedModelListOffset > 0 {
					scrollHint += " · ↑pgup"
				}
				if end < len(p.filteredEmbedModels) {
					scrollHint += " · ↓pgdn"
				}
				footer = scrollHint + " · enter select · esc cancel"
			}
		}
		b.WriteString(dim.Render(footer) + "\n")
		if p.fetchingEmbedModels && p.embedModelSearch != "" {
			b.WriteString(dim.Render("  enter will use: ") + lipgloss.NewStyle().Foreground(colorMainText).Render(p.embedModelSearch) + "\n")
		}
		return b.String()
	}

	b.WriteString(lipgloss.NewStyle().Foreground(colorAccent).Bold(true).Render("settings") + "\n")

	for idx := 0; idx < sfCount; idx++ {
		isSSH := idx >= sfSSHHost
		if isSSH && p.backend != "ssh" {
			continue
		}
		if idx == sfSSHHost {
			b.WriteString("\n" + dim.Render("  ssh") + "\n")
		}

		f := p.fields[idx]
		focused := idx == p.focused && p.focusedBtn == 0

		label := fmt.Sprintf("%-12s", f.label)
		var labelStyled string
		if focused {
			labelStyled = lipgloss.NewStyle().Foreground(colorMainText).Render(label)
		} else {
			labelStyled = dim.Render(label)
		}

		var valueStyled string
		if idx == sfBackend {
			var localStr, sshStr string
			if p.backend == "local" {
				localStr = lipgloss.NewStyle().Foreground(colorMainText).Render("local")
				sshStr = dim.Render("ssh")
			} else {
				localStr = dim.Render("local")
				sshStr = lipgloss.NewStyle().Foreground(colorMainText).Render("ssh")
			}
			valueStyled = localStr + dim.Render(" / ") + sshStr
		} else {
			var d string
			if f.masked && len(f.value) > 0 {
				d = strings.Repeat("*", len(f.value))
			} else {
				d = string(f.value)
			}
			if len(d) == 0 {
				d = "-"
			}
			if focused && p.editingField {
				valueStyled = lipgloss.NewStyle().Foreground(colorMainText).Render(d) + dim.Render("_")
			} else {
				valueStyled = lipgloss.NewStyle().Foreground(colorFg).Render(d)
			}
		}

		indicator := "  "
		if focused {
			indicator = lipgloss.NewStyle().Foreground(colorAccent).Render("> ")
		}
		b.WriteString(indicator + labelStyled + " " + valueStyled + "\n")
	}

	b.WriteString("\n")
	if p.focusedBtn == 1 {
		saveStyle := lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
		b.WriteString(saveStyle.Render("❯ [ Save ]"))
	} else {
		saveStyle := lipgloss.NewStyle().Foreground(colorMuted)
		b.WriteString(saveStyle.Render("  [ Save ]"))
	}
	b.WriteString("   ")
	if p.focusedBtn == 2 {
		cancelStyle := lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
		b.WriteString(cancelStyle.Render("❯ [ Cancel ]"))
	} else {
		cancelStyle := lipgloss.NewStyle().Foreground(colorMuted)
		b.WriteString(cancelStyle.Render("  [ Cancel ]"))
	}
	b.WriteString("\n")

	return b.String()
}
