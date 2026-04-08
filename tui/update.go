package tui

import (
	"fmt"
	"math"
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
		m.inlineForm.saveSnapshot()
		m.settingsActive = true
	case "/provider":
		m.inlineForm.saveSnapshot()
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
		m.inlineForm.saveSnapshot()
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
		m.inlineForm.saveSnapshot()
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
		m.inlineForm.saveSnapshot()
		m.settingsActive = true
		m.inlineForm.focused = sfAPIKey
		m.inlineForm.editingField = true
	case "/backend":
		m.inlineForm.saveSnapshot()
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

// submitInput processes the current input buffer as a submitted message.
func (m *Model) submitInput() (tea.Model, tea.Cmd) {
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
	return m, nil
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
		if m.infoMsg != "" && time.Since(m.infoMsgTime) > 5*time.Second {
			m.infoMsg = ""
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
			m.inlineForm.restoreSnapshot()
			s := m.inlineForm.toSettings()
			m.settingsActive = false
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
				m.inlineForm.restoreSnapshot()
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
		m.showHint = false

		if m.selecting {
			m.selecting = false
			m.selAutoScroll = false
			return m, nil
		}
		if m.inputSelecting {
			m.inputSelecting = false
			return m, nil
		}

		switch msg.Type {

		case tea.KeyCtrlS:
			m.inlineForm.saveSnapshot()
			m.settingsActive = true
			m.messages = append(m.messages, ChatMessage{Role: RoleUser, Content: "/settings", Time: time.Now()})
			m.chatOffset = 0
			return m, nil

		case tea.KeyEnter:
			if msg.Alt {
				m.inputBuf = append(m.inputBuf[:m.cursorPos], append([]rune{'\n'}, m.inputBuf[m.cursorPos:]...)...)
				m.cursorPos++
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
			return m.submitInput()

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
				if m.historyIdx != -1 {
					m.historyPrefix = strings.TrimSpace(string(m.inputBuf))
					m.historyDraft = string(m.inputBuf)
					m.historyIdx = -1
				}
				if msg.Type == tea.KeyBackspace {
					if m.cursorPos > 0 {
						m.inputBuf = append(m.inputBuf[:m.cursorPos-1], m.inputBuf[m.cursorPos:]...)
						m.cursorPos--
					}
				} else {
					if m.cursorPos < len(m.inputBuf) {
						m.inputBuf = append(m.inputBuf[:m.cursorPos], m.inputBuf[m.cursorPos+1:]...)
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

		case tea.KeyUp:
			if m.slashActive && len(m.slashFiltered) > 0 {
				m.slashIdx--
				if m.slashIdx < 0 {
					m.slashIdx = len(m.slashFiltered) - 1
				}
				return m, nil
			}
			if !m.thinking || m.blocking {
				lines := m.inputLines()
				curLine, curCol := m.cursorLineCol()
				if curLine > 0 {
					prevLineLen := len(lines[curLine-1])
					newCol := curCol
					if newCol > prevLineLen {
						newCol = prevLineLen
					}
					m.cursorPos = 0
					for i := 0; i < curLine-1; i++ {
						m.cursorPos += len(lines[i]) + 1
					}
					m.cursorPos += newCol
				} else {
					m.browseHistoryUp()
				}
			}

		case tea.KeyDown:
			if m.slashActive && len(m.slashFiltered) > 0 {
				m.slashIdx++
				if m.slashIdx >= len(m.slashFiltered) {
					m.slashIdx = 0
				}
				return m, nil
			}
			if !m.thinking || m.blocking {
				lines := m.inputLines()
				curLine, curCol := m.cursorLineCol()
				if curLine < len(lines)-1 {
					nextLineLen := len(lines[curLine+1])
					newCol := curCol
					if newCol > nextLineLen {
						newCol = nextLineLen
					}
					m.cursorPos = 0
					for i := 0; i <= curLine; i++ {
						m.cursorPos += len(lines[i]) + 1
					}
					m.cursorPos += newCol
				} else {
					m.browseHistoryDown()
				}
			}

		case tea.KeyLeft:
			if !m.thinking || m.blocking {
				if m.cursorPos > 0 {
					m.cursorPos--
				}
			}

		case tea.KeyRight:
			if !m.thinking || m.blocking {
				if m.cursorPos < len(m.inputBuf) {
					m.cursorPos++
				}
			}

		case tea.KeyCtrlLeft:
			if !m.thinking || m.blocking {
				m.cursorPos = m.cursorPrevWord()
			}

		case tea.KeyCtrlRight:
			if !m.thinking || m.blocking {
				m.cursorPos = m.cursorNextWord()
			}

		case tea.KeyHome:
			if !m.thinking || m.blocking {
				m.cursorPos = m.cursorLineStart()
			} else {
				m.chatOffset = 1 << 30
				m.userScrolled = true
			}

		case tea.KeyEnd:
			if !m.thinking || m.blocking {
				m.cursorPos = m.cursorLineEnd()
			} else {
				m.chatOffset = 0
				m.userScrolled = false
			}

		case tea.KeyCtrlHome:
			if !m.thinking || m.blocking {
				m.cursorPos = 0
			} else {
				m.chatOffset = 1 << 30
				m.userScrolled = true
			}

		case tea.KeyCtrlEnd:
			if !m.thinking || m.blocking {
				m.cursorPos = len(m.inputBuf)
			} else {
				m.chatOffset = 0
				m.userScrolled = false
			}

		case tea.KeyCtrlA:
			if !m.thinking || m.blocking {
				m.cursorPos = m.cursorLineStart()
			}

		case tea.KeyCtrlE:
			if !m.thinking || m.blocking {
				m.cursorPos = m.cursorLineEnd()
			}

		case tea.KeyCtrlU:
			if !m.thinking || m.blocking {
				start := m.cursorLineStart()
				if start < m.cursorPos {
					if m.historyIdx != -1 {
						m.historyPrefix = strings.TrimSpace(string(m.inputBuf))
						m.historyDraft = string(m.inputBuf)
						m.historyIdx = -1
					}
					m.inputBuf = append(m.inputBuf[:start], m.inputBuf[m.cursorPos:]...)
					m.cursorPos = start
				}
			}

		case tea.KeyCtrlK:
			if !m.thinking || m.blocking {
				end := m.cursorLineEnd()
				if end > m.cursorPos {
					if m.historyIdx != -1 {
						m.historyPrefix = strings.TrimSpace(string(m.inputBuf))
						m.historyDraft = string(m.inputBuf)
						m.historyIdx = -1
					}
					m.inputBuf = append(m.inputBuf[:m.cursorPos], m.inputBuf[end:]...)
				}
			}

		case tea.KeyCtrlW:
			if (!m.thinking || m.blocking) && m.cursorPos > 0 {
				if m.historyIdx != -1 {
					m.historyPrefix = strings.TrimSpace(string(m.inputBuf))
					m.historyDraft = string(m.inputBuf)
					m.historyIdx = -1
				}
				pos := m.cursorPrevWord()
				m.inputBuf = append(m.inputBuf[:pos], m.inputBuf[m.cursorPos:]...)
				m.cursorPos = pos
			}

		case tea.KeyPgUp:
			m.chatOffset += m.chatAreaHeight()
			m.userScrolled = true

		case tea.KeyPgDown:
			m.chatOffset -= m.chatAreaHeight()
			if m.chatOffset < 0 {
				m.chatOffset = 0
			}
			if m.chatOffset == 0 {
				m.userScrolled = false
			}

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
						m.inputBuf = append(m.inputBuf[:m.cursorPos], append([]rune{r}, m.inputBuf[m.cursorPos:]...)...)
						m.cursorPos++
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
		chatH := m.chatAreaHeight()
		chatTop := 3
		chatBottom := chatTop + chatH - 1
		inputStartLine := chatH + 4
		inInputArea := msg.Y >= inputStartLine && !m.thinking

		switch {
		case msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft:
			if msg.Y >= chatTop && msg.Y <= chatBottom && !m.settingsActive {
				allIdx, ok := m.mouseToAllLine(msg.Y)
				if ok {
					m.selecting = true
					m.selAnchorLine = allIdx
					m.selAnchorCol = max(0, msg.X-padLR)
					m.selEndLine = allIdx
					m.selEndCol = max(0, msg.X-padLR)
					m.selMouseY = msg.Y
					m.selAutoScroll = false
				}
			} else if pos, ok := m.mouseToInputPos(msg.X, msg.Y); ok {
				m.inputSelecting = true
				m.inputSelAnchor = pos
				m.inputSelEnd = pos
			} else {
				if m.selecting {
					m.selecting = false
					m.selAutoScroll = false
				}
				if m.inputSelecting {
					m.inputSelecting = false
				}
			}

		case msg.Action == tea.MouseActionMotion && (m.selecting || m.inputSelecting):
			if m.selecting {
				allIdx, ok := m.mouseToAllLine(msg.Y)
				if ok {
					m.selEndLine = allIdx
					m.selEndCol = max(0, msg.X-padLR)
				} else if msg.Y < chatTop && len(m.cachedAllLines) > 0 {
					first, _ := m.chatAreaContentBounds()
					if first >= 0 {
						m.selEndLine = first
						m.selEndCol = 0
					}
				} else if msg.Y > chatBottom && len(m.cachedAllLines) > 0 {
					_, last := m.chatAreaContentBounds()
					if last >= 0 {
						m.selEndLine = last
						m.selEndCol = 1 << 30
					}
				}
				m.selMouseY = msg.Y

				inScrollZone := msg.Y <= chatTop-1 || msg.Y >= chatBottom+1
				if inScrollZone && !m.selAutoScroll {
					m.selAutoScroll = true
					return m, tea.Tick(selScrollInterval, func(t time.Time) tea.Msg {
						return selectionTickMsg(t)
					})
				} else if !inScrollZone {
					m.selAutoScroll = false
				}
			}
			if m.inputSelecting {
				pos, ok := m.mouseToInputPos(msg.X, msg.Y)
				if ok {
					m.inputSelEnd = pos
				} else {
					firstY := m.inputFirstLineY()
					if msg.Y < firstY {
						m.inputSelEnd = 0
					} else {
						m.inputSelEnd = len(m.inputBuf)
					}
				}
			}

		case msg.Action == tea.MouseActionRelease && (m.selecting || m.inputSelecting):
			var cmd tea.Cmd
			if m.selecting {
				m.selecting = false
				m.selAutoScroll = false
				sLine, sCol, eLine, eCol := m.selectionRange()
				if sLine != eLine || sCol != eCol {
					allLines := m.renderAllMessages()
					text := extractSelectedText(allLines, sLine, sCol, eLine, eCol)
					text = strings.TrimRight(text, " \t")
					text = strings.TrimRight(text, "\n")
					if text != "" {
						cmd = copyToClipboardCmd(text)
					}
				}
			}
			if m.inputSelecting {
				m.inputSelecting = false
				selStart, selEnd := m.inputSelectionRange()
				if selStart != selEnd && selStart < len(m.inputBuf) {
					end := selEnd
					if end > len(m.inputBuf) {
						end = len(m.inputBuf)
					}
					text := string(m.inputBuf[selStart:end])
					if text != "" && cmd == nil {
						cmd = copyToClipboardCmd(text)
					}
				}
			}
			if cmd != nil {
				return m, cmd
			}

		case msg.Action == tea.MouseActionPress:
			switch msg.Button {
			case tea.MouseButtonWheelUp:
				if inInputArea && len(m.history) > 0 {
					m.browseHistoryUp()
					return m, nil
				}
				now := time.Now()
				elapsed := now.Sub(m.lastScrollTime).Seconds()
				if elapsed < 0.1 {
					m.scrollVelocity = math.Min(m.scrollVelocity+0.5, 5)
				} else {
					m.scrollVelocity = 1
				}
				m.lastScrollTime = now
				m.chatOffset += int(m.scrollVelocity)
				m.userScrolled = true
			case tea.MouseButtonWheelDown:
				if inInputArea && len(m.history) > 0 {
					m.browseHistoryDown()
					return m, nil
				}
				now := time.Now()
				elapsed := now.Sub(m.lastScrollTime).Seconds()
				if elapsed < 0.1 {
					m.scrollVelocity = math.Min(m.scrollVelocity+0.5, 5)
				} else {
					m.scrollVelocity = 1
				}
				m.lastScrollTime = now
				m.chatOffset -= int(m.scrollVelocity)
				if m.chatOffset < 0 {
					m.chatOffset = 0
				}
				if m.chatOffset == 0 {
					m.userScrolled = false
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
				if len(m.messages) > 0 && m.messages[len(m.messages)-1].Role == RoleThinking {
					m.messages[len(m.messages)-1].Content += s
				} else {
					for i := range m.messages {
						if m.messages[i].ThinkingActive {
							m.messages[i].ThinkingActive = false
						}
					}
					m.messages = append(m.messages, ChatMessage{
						Role:           RoleThinking,
						Content:        s,
						Time:           time.Now(),
						ThinkingActive: true,
					})
				}
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
				if !m.userScrolled {
					m.chatOffset = 0
				}
			}

		case "done":
			m.thinking = false
			m.blocking = false
			wasStreaming := m.streaming
			m.streaming = false
			m.statusLine = ""
			m.userScrolled = false
			m.chatOffset = 0
			for i := range m.messages {
				if m.messages[i].ThinkingActive {
					m.messages[i].ThinkingActive = false
				}
			}
			if content, ok := evt.Data.(string); ok && content != "" {
				if wasStreaming && len(m.messages) > 0 && m.messages[len(m.messages)-1].Role == RoleAssistant {
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
		m.inlineForm.saveSnapshot()
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

	case selectionTickMsg:
		if m.selecting && m.selAutoScroll {
			chatH := m.chatAreaHeight()
			chatTop := 3
			chatBottom := chatTop + chatH - 1
			mouseY := m.selMouseY

			if mouseY <= chatTop-1 {
				m.chatOffset++
				m.userScrolled = true
				allIdx, ok := m.mouseToAllLine(chatTop)
				if ok {
					m.selEndLine = allIdx
				}
			} else if mouseY >= chatBottom+1 {
				if m.chatOffset > 0 {
					m.chatOffset--
					if m.chatOffset == 0 {
						m.userScrolled = false
					}
				}
				allIdx, ok := m.mouseToAllLine(chatBottom)
				if ok {
					m.selEndLine = allIdx
				}
			}

			if mouseY <= chatTop-1 || mouseY >= chatBottom+1 {
				return m, tea.Tick(selScrollInterval, func(t time.Time) tea.Msg {
					return selectionTickMsg(t)
				})
			}
			m.selAutoScroll = false
		}

	case clipboardCopiedMsg:
		m.infoMsg = "copied to clipboard"
		m.infoMsgTime = time.Now()

	case clipboardErrorMsg:
		m.lastError = string(msg)
		m.errorTime = time.Now()
	}

	return m, nil
}
