package tui

import (
	"fmt"
	"strings"
	"time"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
)

func (m *Model) executeSlashCommand(text string) (tea.Model, tea.Cmd) {
	parts := strings.Fields(text)
	cmd := parts[0]

	m.inputBuf = nil
	m.cursorPos = 0
	m.messages = append(m.messages, ChatMessage{Role: RoleUser, Content: text, Time: time.Now()})
	m.chatOffset = 0

	switch cmd {
	case "/settings":
		m.settingsActive = true
	case "/provider":
		m.settingsActive = true
		m.inlineForm.focused = sfProvider
		m.inlineForm.isProviderSelect = true
		m.inlineForm.providerListIdx = 0
		for i, preset := range m.inlineForm.providerPresets {
			if preset.ID == m.inlineForm.providerID {
				m.inlineForm.providerListIdx = i
				break
			}
		}
	case "/model":
		m.settingsActive = true
		m.inlineForm.focused = sfModel
		if m.inlineForm.providerID == "ollama" || m.inlineForm.providerID == "custom" {
			m.inlineForm.editingField = true
			m.lastError = fmt.Sprintf("Type model name manually for %s.", m.inlineForm.providerID)
			m.errorTime = time.Now()
		} else if string(m.inlineForm.fields[sfAPIKey].value) == "" && m.inlineForm.providerID != "openrouter" {
			m.inlineForm.editingField = true
			m.lastError = fmt.Sprintf("No API key provided for %s. Type model name manually.", m.inlineForm.providerID)
			m.errorTime = time.Now()
		} else {
			m.inlineForm.isModelSelect = true
			if len(m.inlineForm.availableModels) == 0 && !m.inlineForm.fetchingModels {
				m.inlineForm.fetchError = ""
				m.inlineForm.fetchingModels = true
				current := fetchModelsMsg{
					ProviderID: m.inlineForm.providerID,
					BaseURL:    string(m.inlineForm.fields[sfBaseURL].value),
					APIKey:     string(m.inlineForm.fields[sfAPIKey].value),
				}
				return m, func() tea.Msg { return current }
			}
			m.inlineForm.updateModelFilter()
		}
	case "/embed-model":
		m.settingsActive = true
		m.inlineForm.focused = sfEmbedModel
		if m.inlineForm.providerID == "ollama" || m.inlineForm.providerID == "custom" {
			m.inlineForm.editingField = true
			m.lastError = fmt.Sprintf("Type embedding model name manually for %s.", m.inlineForm.providerID)
			m.errorTime = time.Now()
		} else if string(m.inlineForm.fields[sfAPIKey].value) == "" && m.inlineForm.providerID != "openrouter" {
			m.inlineForm.editingField = true
			m.lastError = fmt.Sprintf("No API key provided for %s. Type embedding model name manually.", m.inlineForm.providerID)
			m.errorTime = time.Now()
		} else {
			m.inlineForm.isEmbedModelSelect = true
			if len(m.inlineForm.availableEmbedModels) == 0 && !m.inlineForm.fetchingEmbedModels {
				m.inlineForm.fetchEmbedError = ""
				m.inlineForm.fetchingEmbedModels = true
				current := fetchModelsMsg{
					ProviderID: m.inlineForm.providerID,
					BaseURL:    string(m.inlineForm.fields[sfBaseURL].value),
					APIKey:     string(m.inlineForm.fields[sfAPIKey].value),
					IsEmbed:    true,
				}
				return m, func() tea.Msg { return current }
			}
			m.inlineForm.updateEmbedModelFilter()
		}
	case "/api-key":
		m.settingsActive = true
		m.inlineForm.focused = sfAPIKey
		m.inlineForm.editingField = true
	case "/backend":
		m.settingsActive = true
		m.inlineForm.focused = sfBackend
		m.inlineForm.toggleBackend()
	case "/help":
		helpText := "Available commands:\n"
		for _, c := range availableCmds {
			helpText += fmt.Sprintf("  %-16s %s\n", c.name, c.desc)
		}
		m.messages = append(m.messages, ChatMessage{Role: RoleAssistant, Content: helpText, Time: time.Now()})
	default:
		m.lastError = fmt.Sprintf("Unknown command: %s (try /help)", cmd)
		m.errorTime = time.Now()
	}

	return m, nil
}

func tickCmd() tea.Cmd {
	return tea.Tick(500*time.Millisecond, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Always handle resize and tick regardless of mode.
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tickMsg:
		m.spinFrame = (m.spinFrame + 1) % len(spinnerFrames)
		if m.lastError != "" && time.Since(m.errorTime) > 8*time.Second {
			m.lastError = ""
		}
		return m, tickCmd()
	}

	// ── Global cancel: Esc and Ctrl+C ────────────────────────
	if msg, ok := msg.(tea.KeyMsg); ok && (msg.Type == tea.KeyCtrlC || msg.Type == tea.KeyEsc) {
		// Cancel hierarchy: editing → model select → provider select → settings → double-press quit
		if m.settingsActive {
			if m.inlineForm.editingField {
				m.inlineForm.editingField = false
				return m, nil
			}
			if m.inlineForm.isModelSelect {
				m.inlineForm.isModelSelect = false
				m.inlineForm.modelSearch = ""
				return m, nil
			}
			if m.inlineForm.isEmbedModelSelect {
				m.inlineForm.isEmbedModelSelect = false
				m.inlineForm.embedModelSearch = ""
				return m, nil
			}
			if m.inlineForm.isProviderSelect {
				m.inlineForm.isProviderSelect = false
				return m, nil
			}
			m.settingsActive = false
			s := m.inlineForm.toSettings()
			m.lastSettings = s
			summary := formatSettingsSummary(s)
			m.messages = append(m.messages, ChatMessage{Role: RoleAssistant, Content: summary, Time: time.Now()})
			m.chatOffset = 0
			return m, nil
		}
		// Nothing to cancel — double-press to quit
		if time.Since(m.lastCancelTime) < 2*time.Second {
			m.quit = true
			return m, tea.Quit
		}
		m.lastCancelTime = time.Now()
		m.lastError = "press ctrl+c or esc again to exit"
		m.errorTime = time.Now()
		return m, nil
	}

	// Settings mode — handle key input and pass-through async fetch results.
	if m.settingsActive {
		switch msg := msg.(type) {
		case tea.KeyMsg:
			updated, cmd, saved, cancelled := m.inlineForm.Update(msg)
			m.inlineForm = updated
			if saved {
				s := m.inlineForm.toSettings()
				m.settingsActive = false
				m.modelName = s.Model
				if m.onSaveSettings != nil {
					go m.onSaveSettings(s)
				}
				m.messages = append(m.messages, ChatMessage{
					Role:    RoleAssistant,
					Content: formatSettingsSummary(s),
					Time:    time.Now(),
				})
				m.chatOffset = 0
			}
			if cancelled {
				s := m.inlineForm.toSettings()
				m.settingsActive = false
				m.messages = append(m.messages, ChatMessage{
					Role:    RoleAssistant,
					Content: formatSettingsSummary(s),
					Time:    time.Now(),
				})
				m.chatOffset = 0
			}
			return m, cmd
		case ModelsListMsg:
			m.inlineForm.fetchingModels = false
			m.inlineForm.availableModels = []ModelEntry(msg)
			m.inlineForm.updateModelFilter()
			return m, nil
		case ModelsErrorMsg:
			m.inlineForm.fetchingModels = false
			m.inlineForm.fetchError = "Fetch failed (see bottom left). Press enter to type manually."
			m.lastError = string(msg)
			m.errorTime = time.Now()
			return m, nil
		case EmbedModelsListMsg:
			m.inlineForm.fetchingEmbedModels = false
			m.inlineForm.availableEmbedModels = []ModelEntry(msg)
			m.inlineForm.updateEmbedModelFilter()
			return m, nil
		case EmbedModelsErrorMsg:
			m.inlineForm.fetchingEmbedModels = false
			m.inlineForm.fetchEmbedError = "Fetch failed (see bottom left). Press enter to type manually."
			m.lastError = string(msg)
			m.errorTime = time.Now()
			return m, nil
		case errorMsg:
			m.lastError = string(msg)
			m.errorTime = time.Now()
			return m, nil
		case fetchModelsMsg:
			if m.fetchModelsFn != nil {
				info := msg
				if info.IsEmbed {
					m.inlineForm.fetchingEmbedModels = true
					m.inlineForm.fetchEmbedError = ""
				} else {
					m.inlineForm.fetchingModels = true
					m.inlineForm.fetchError = ""
				}
				return m, func() tea.Msg {
					cmd := m.fetchModelsFn(info.ProviderID, info.BaseURL, info.APIKey)
					if cmd == nil {
						return nil
					}
					res := cmd()
					if info.IsEmbed {
						switch r := res.(type) {
						case ModelsListMsg:
							return EmbedModelsListMsg(r)
						case ModelsErrorMsg:
							return EmbedModelsErrorMsg(r)
						}
					}
					return res
				}
			}
			return m, nil
		case openSettingsMsg:
			// No longer triggers a fetch; fetching is lazy (user opens model selector).
			return m, nil
		}
		return m, nil
	}

	// Normal chat mode.
	switch msg := msg.(type) {

	// ── Keyboard ──────────────────────────────────────────────
	case tea.KeyMsg:
		switch msg.Type {

		case tea.KeyCtrlS:
			m.settingsActive = true
			m.messages = append(m.messages, ChatMessage{Role: RoleUser, Content: "/settings", Time: time.Now()})
			m.chatOffset = 0
			return m, nil

		case tea.KeyEnter:
			// Shift+Enter inserts a newline for multi-line input.
			if msg.Alt {
				m.inputBuf = append(m.inputBuf, '\n')
				m.cursorPos = len(m.inputBuf)
				return m, nil
			}
			if m.slashActive && len(m.slashFiltered) > 0 {
				selected := m.slashFiltered[m.slashIdx]
				m.slashActive = false
				m.inputBuf = []rune(selected.name + " ")
				m.cursorPos = len(m.inputBuf)
				text := strings.TrimSpace(string(m.inputBuf))
				if text != "" && strings.HasPrefix(text, "/") {
					return m.executeSlashCommand(text)
				}
				return m, nil
			}
			if (!m.thinking || m.blocking) && len(m.inputBuf) > 0 {
				text := strings.TrimSpace(string(m.inputBuf))
				m.inputBuf = nil
				m.cursorPos = 0
				m.slashActive = false
				m.resetHistory()
				if text != "" {
					if strings.HasPrefix(text, "/") {
						return m.executeSlashCommand(text)
					}
					m.appendHistory(text)
					if OnHistorySave != nil {
						OnHistorySave(m.history)
					}
					m.messages = append(m.messages, ChatMessage{
						Role:    RoleUser,
						Content: text,
						Time:    time.Now(),
					})
					m.thinking = true
					m.chatOffset = 0
					ch := m.inputCh
					return m, func() tea.Msg {
						ch <- text
						return nil
					}
				}
			}

		case tea.KeyTab:
			if m.slashActive && len(m.slashFiltered) > 0 {
				selected := m.slashFiltered[m.slashIdx]
				m.inputBuf = []rune(selected.name + " ")
				m.cursorPos = len(m.inputBuf)
				m.slashActive = false
				return m, nil
			}

		case tea.KeyBackspace, tea.KeyDelete:
			if (!m.thinking || m.blocking) && len(m.inputBuf) > 0 {
				// If browsing history and user edits, update prefix
				if m.historyIdx != -1 {
					m.historyPrefix = strings.TrimSpace(string(m.inputBuf))
					m.historyDraft = string(m.inputBuf)
					m.historyIdx = -1
				}
				m.inputBuf = m.inputBuf[:len(m.inputBuf)-1]
				if m.cursorPos > len(m.inputBuf) {
					m.cursorPos = len(m.inputBuf)
				}
				inputStr := string(m.inputBuf)
				if strings.HasPrefix(inputStr, "/") && !strings.Contains(inputStr, " ") {
					m.slashFilter = inputStr
					m.updateSlashFilter()
					m.slashActive = len(m.slashFiltered) > 0
				} else {
					m.slashActive = false
				}
			}

		case tea.KeyUp:
			if m.slashActive && len(m.slashFiltered) > 0 {
				m.slashIdx--
				if m.slashIdx < 0 {
					m.slashIdx = len(m.slashFiltered) - 1
				}
				return m, nil
			}
			// Prompt history navigation
			if !m.thinking || m.blocking {
				m.browseHistoryUp()
			}

		case tea.KeyDown:
			if m.slashActive && len(m.slashFiltered) > 0 {
				m.slashIdx++
				if m.slashIdx >= len(m.slashFiltered) {
					m.slashIdx = 0
				}
				return m, nil
			}
			// Prompt history navigation
			if !m.thinking || m.blocking {
				m.browseHistoryDown()
			}

		case tea.KeyPgUp:
			m.chatOffset += m.chatAreaHeight()

		case tea.KeyPgDown:
			m.chatOffset -= m.chatAreaHeight()
			if m.chatOffset < 0 {
				m.chatOffset = 0
			}

		case tea.KeyHome:
			m.chatOffset = 1 << 30

		case tea.KeyEnd:
			m.chatOffset = 0

		default:
			if !m.thinking || m.blocking {
				s := msg.String()
				if strings.HasPrefix(s, "<") && strings.HasSuffix(s, "M") {
					return m, nil
				}
				if strings.HasPrefix(s, "\x1b[<") && strings.HasSuffix(s, "M") {
					return m, nil
				}
				// If browsing history and user types, update prefix and reset position
				if m.historyIdx != -1 {
					m.historyPrefix = strings.TrimSpace(string(m.inputBuf))
					m.historyDraft = string(m.inputBuf)
					m.historyIdx = -1
				}
				for _, r := range msg.Runes {
					if unicode.IsPrint(r) {
						m.inputBuf = append(m.inputBuf, r)
						m.cursorPos = len(m.inputBuf)
					}
				}
				inputStr := string(m.inputBuf)
				if strings.HasPrefix(inputStr, "/") && !strings.Contains(inputStr, " ") {
					m.slashFilter = inputStr
					m.updateSlashFilter()
					m.slashActive = len(m.slashFiltered) > 0
				} else {
					m.slashActive = false
				}
			}
		}
	case tea.MouseMsg:
		if msg.Action == tea.MouseActionPress {
			switch msg.Button {
			case tea.MouseButtonWheelUp:
				m.chatOffset++
			case tea.MouseButtonWheelDown:
				m.chatOffset--
				if m.chatOffset < 0 {
					m.chatOffset = 0
				}
			}
		}
	// ── Agent events ──────────────────────────────────────────
	case agentMsg:
		evt := AgentEventMsg(msg)
		switch evt.Type {
		case "phase":
			if s, ok := evt.Data.(string); ok && s != "" {
				m.statusPhase = s
				switch s {
				case "grounding":
					m.statusLine = "Gathering context"
				case "planning":
					m.statusLine = "Planning"
				case "executing":
					m.statusLine = "Executing"
				default:
					m.statusLine = s
				}
			}

		case "thinking":
			if s, ok := evt.Data.(string); ok && s != "" {
				m.statusLine = truncateLine(s, 80)
				m.messages = append(m.messages, ChatMessage{
					Role:           RoleThinking,
					Content:        s,
					Time:           time.Now(),
					ThinkingActive: true,
				})
			}

		case "stream_token":
			if token, ok := evt.Data.(string); ok && token != "" {
				m.streaming = true
				if len(m.messages) > 0 && m.messages[len(m.messages)-1].Role == RoleAssistant {
					m.messages[len(m.messages)-1].Content += token
				} else {
					m.messages = append(m.messages, ChatMessage{
						Role:    RoleAssistant,
						Content: token,
						Time:    time.Now(),
					})
				}
				m.chatOffset = 0
			}

		case "done":
			m.thinking = false
			m.blocking = false
			m.streaming = false
			m.statusLine = ""
			m.chatOffset = 0
			for i := range m.messages {
				if m.messages[i].ThinkingActive {
					m.messages[i].ThinkingActive = false
				}
			}
			if content, ok := evt.Data.(string); ok && content != "" {
				if m.streaming && len(m.messages) > 0 && m.messages[len(m.messages)-1].Role == RoleAssistant {
					// Streaming already populated the last message — ensure it has the full content
					m.messages[len(m.messages)-1].Content = content
				} else {
					m.messages = append(m.messages, ChatMessage{
						Role:    RoleAssistant,
						Content: content,
						Time:    time.Now(),
					})
				}
			}

		case "tool_call":
			if d, ok := evt.Data.(map[string]string); ok {
				name := d["name"]
				args := d["args"]
				argsCompact := summarizeToolArgs(name, args)
				m.statusLine = toolLabel(name, argsCompact)
				idx := len(m.messages)
				m.messages = append(m.messages, ChatMessage{
					Role:       RoleTool,
					Content:    toolLabel(name, argsCompact),
					Time:       time.Now(),
					ToolName:   name,
					ToolArgs:   argsCompact,
					ToolStatus: "calling",
					ToolIndex:  idx,
				})
			}

		case "tool_result":
			if d, ok := evt.Data.(map[string]string); ok {
				name := d["name"]
				m.statusLine = name
				for i := len(m.messages) - 1; i >= 0; i-- {
					msg := &m.messages[i]
					if msg.Role == RoleTool && msg.ToolName == name && msg.ToolStatus == "calling" {
						msg.ToolStatus = "success"
						msg.Content = msg.ToolName + " " + msg.ToolArgs
						break
					}
				}
			}

		case "blocking":
			// Agent needs user input to continue — re-enable the input box.
			question := fmt.Sprintf("%v", evt.Data)
			m.thinking = false
			m.blocking = true
			m.statusLine = ""
			for i := range m.messages {
				if m.messages[i].ThinkingActive {
					m.messages[i].ThinkingActive = false
				}
			}
			m.messages = append(m.messages, ChatMessage{
				Role:    RoleAssistant,
				Content: "⚠  " + question,
				Time:    time.Now(),
			})

		case "error":
			m.thinking = false
			m.blocking = false
			m.statusLine = ""
			for i := range m.messages {
				if m.messages[i].ThinkingActive {
					m.messages[i].ThinkingActive = false
				}
			}
			errMsg := fmt.Sprintf("%v", evt.Data)
			m.lastError = errMsg
			m.errorTime = time.Now()
			m.messages = append(m.messages, ChatMessage{
				Role:    RoleError,
				Content: errMsg,
				Time:    time.Now(),
			})

		case "usage":
			if d, ok := evt.Data.(map[string]int); ok {
				m.tokenPrompt += d["prompt"]
				m.tokenCompletion += d["completion"]
			}
		}

	// ── External messages ──────────────────────────────────────
	case openSettingsMsg:
		m.settingsActive = true
		// Fetch is lazy — no auto-fetch here.

	case fetchModelsMsg:
		if m.fetchModelsFn != nil {
			info := msg
			if info.IsEmbed {
				m.inlineForm.fetchingEmbedModels = true
				m.inlineForm.fetchEmbedError = ""
			} else {
				m.inlineForm.fetchingModels = true
				m.inlineForm.fetchError = ""
			}
			return m, func() tea.Msg {
				cmd := m.fetchModelsFn(info.ProviderID, info.BaseURL, info.APIKey)
				if cmd == nil {
					return nil
				}
				res := cmd()
				if info.IsEmbed {
					switch r := res.(type) {
					case ModelsListMsg:
						return EmbedModelsListMsg(r)
					case ModelsErrorMsg:
						return EmbedModelsErrorMsg(r)
					}
				}
				return res
			}
		}

	case ModelsListMsg:
		m.inlineForm.fetchingModels = false
		m.inlineForm.availableModels = []ModelEntry(msg)
		m.inlineForm.updateModelFilter()

	case ModelsErrorMsg:
		m.inlineForm.fetchingModels = false
		m.inlineForm.fetchError = string(msg)

	case EmbedModelsListMsg:
		m.inlineForm.fetchingEmbedModels = false
		m.inlineForm.availableEmbedModels = []ModelEntry(msg)
		m.inlineForm.updateEmbedModelFilter()

	case EmbedModelsErrorMsg:
		m.inlineForm.fetchingEmbedModels = false
		m.inlineForm.fetchEmbedError = string(msg)

	case updateModelMsg:
		m.modelName = string(msg)

	case statusMsg:
		// reserved

	case errorMsg:
		m.lastError = string(msg)
		m.errorTime = time.Now()
	}

	return m, nil
}
