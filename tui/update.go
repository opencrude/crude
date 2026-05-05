package tui

import (
	"fmt"
	"math"
	"strings"
	"time"
	"unicode"

	"crude/provider"
	tea "github.com/charmbracelet/bubbletea"
)

func (m *Model) executeSlashCommand(text string) (tea.Model, tea.Cmd) {
	parts := strings.Fields(text)
	cmd := parts[0]

	m.clearInput()
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
	case "/oracle-model":
		m.inlineForm.saveSnapshot()
		m.settingsActive = true
		m.inlineForm.focused = sfOracleModel
		if m.inlineForm.providerID == "ollama" || m.inlineForm.providerID == "custom" {
			m.inlineForm.editingField = true
			m.lastError = fmt.Sprintf("Type oracle model name manually for %s.", m.inlineForm.providerID)
			m.errorTime = time.Now()
		} else if string(m.inlineForm.fields[sfAPIKey].value) == "" && m.inlineForm.providerID != "openrouter" {
			m.inlineForm.editingField = true
			m.lastError = fmt.Sprintf("No API key provided for %s. Type oracle model name manually.", m.inlineForm.providerID)
			m.errorTime = time.Now()
		} else {
			m.inlineForm.isOracleModelSelect = true
			if len(m.inlineForm.availableOracleModels) == 0 && !m.inlineForm.fetchingOracleModels {
				m.inlineForm.fetchOracleError = ""
				m.inlineForm.fetchingOracleModels = true
				current := fetchModelsMsg{
					ProviderID: m.inlineForm.providerID,
					BaseURL:    string(m.inlineForm.fields[sfBaseURL].value),
					APIKey:     string(m.inlineForm.fields[sfAPIKey].value),
				}
				return m, func() tea.Msg { return current }
			}
			m.inlineForm.updateOracleModelFilter()
		}
	case "/api-key":
		m.inlineForm.saveSnapshot()
		m.settingsActive = true
		m.inlineForm.focused = sfAPIKey
		m.inlineForm.editingField = true
	case "/effort":
		current := m.lastSettings.ThinkingEffort
		if current == "" {
			current = "auto"
		}
		next := cycleEffort(current, 1)
		m.lastSettings.ThinkingEffort = next
		m.inlineForm.fields[sfThinkingEffort].value = []rune(next)
		if m.onSaveSettings != nil {
			go m.onSaveSettings(m.lastSettings)
		}
		effortMsg := fmt.Sprintf("Thinking effort: %s", next)
		m.messages = append(m.messages, ChatMessage{Role: RoleAssistant, Content: effortMsg, Time: time.Now()})
	case "/help":
		helpText := "Available commands:\n"
		for _, c := range availableCmds {
			helpText += fmt.Sprintf("  %-16s %s\n", c.name, c.desc)
		}
		m.messages = append(m.messages, ChatMessage{Role: RoleAssistant, Content: helpText, Time: time.Now()})
	case "/undo", "/undo-n", "/undo-all", "/changes":
		if m.OnUndoCmd != nil {
			go m.OnUndoCmd(text)
		}
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
	if (!m.thinking || m.blocking) && m.inputLen() > 0 {
		text := strings.TrimSpace(m.inputText())
		m.clearInput()
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
				select {
				case ch <- text:
					return nil
				default:
					return errorMsg("input channel blocked - please try again")
				}
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

	// Global cancel
	if msg, ok := msg.(tea.KeyMsg); ok && (msg.Type == tea.KeyCtrlC || msg.Type == tea.KeyEsc) {
		// Cancel hierarchy: editing → model select → provider select → settings → agent → double-press quit
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
			if m.inlineForm.isOracleModelSelect {
				m.inlineForm.isOracleModelSelect = false
				m.inlineForm.oracleModelSearch = ""
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
		// Agent is thinking — cancel the current operation
		if m.thinking {
			select {
			case m.cancelCh <- struct{}{}:
			default:
			}
			m.lastError = "cancelling…"
			m.errorTime = time.Now()
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
				for i := range m.messages {
					m.messages[i].cachedLines = nil
				}
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
		case OracleModelsListMsg:
			m.inlineForm.fetchingOracleModels = false
			m.inlineForm.availableOracleModels = []ModelEntry(msg)
			m.inlineForm.updateOracleModelFilter()
			return m, nil
		case OracleModelsErrorMsg:
			m.inlineForm.fetchingOracleModels = false
			m.inlineForm.fetchOracleError = "Fetch failed (see bottom left). Press enter to type manually."
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
				isOracle := m.inlineForm.isOracleModelSelect && m.inlineForm.fetchingOracleModels
				if isOracle {
					m.inlineForm.fetchingOracleModels = true
					m.inlineForm.fetchOracleError = ""
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
					if isOracle {
						switch r := res.(type) {
						case ModelsListMsg:
							return OracleModelsListMsg(r)
						case ModelsErrorMsg:
							return OracleModelsErrorMsg(r)
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

	// Confirmation form mode — handle button navigation and selection.
	if m.confirmForm.active {
		switch msg := msg.(type) {
		case tea.KeyMsg:
			updated, result, done := m.confirmForm.Update(msg)
			m.confirmForm = updated
			if done {
				m.blocking = false
				m.thinking = true
				if m.OnConfirmResult != nil {
					go m.OnConfirmResult(result)
				}
				resultMsg := "✓ Allowed"
				switch result.Choice {
				case ConfirmAlways:
					resultMsg = "✓ Always allowed (session)"
				case ConfirmDeny:
					resultMsg = "✗ Denied"
				}
				m.messages = append(m.messages, ChatMessage{
					Role:    RoleAssistant,
					Content: resultMsg,
					Time:    time.Now(),
				})
				m.chatOffset = 0
			}
			return m, nil
		}
		return m, nil
	}

	// Normal chat mode.
	switch msg := msg.(type) {

	// Keyboard
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
				m.splitLineAt(m.cursorLine, m.cursorCol)
				m.cursorLine++
				m.cursorCol = 0
				return m, nil
			}
			if m.slashActive && len(m.slashFiltered) > 0 {
				selected := m.slashFiltered[m.slashIdx]
				m.slashActive = false
				m.setInput(selected.name + " ")
				text := strings.TrimSpace(m.inputText())
				if text != "" && strings.HasPrefix(text, "/") {
					return m.executeSlashCommand(text)
				}
				return m, nil
			}
			return m.submitInput()

		case tea.KeyTab:
			if m.slashActive && len(m.slashFiltered) > 0 {
				selected := m.slashFiltered[m.slashIdx]
				m.setInput(selected.name + " ")
				m.slashActive = false
				return m, nil
			}

		case tea.KeyBackspace, tea.KeyDelete:
			if (!m.thinking || m.blocking) && m.inputLen() > 0 {
				m.lastInputTime = time.Now()
				m.lastVisualCol = -1
				if m.historyIdx != -1 {
					m.historyPrefix = strings.TrimSpace(m.inputText())
					m.historyDraft = m.inputText()
					m.historyIdx = -1
				}
				if msg.Type == tea.KeyBackspace {
					cf := m.cursorFlat()
					if cf > 0 {
						m.deleteRuneAt(cf)
						m.setCursorFlat(cf - 1)
					}
				} else {
					cf := m.cursorFlat()
					if cf < m.inputLen() {
						m.deleteForwardAt(cf)
					}
				}
				inputStr := m.inputText()
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
				if m.cursorLine == 0 && m.cursorCol == 0 {
					m.browseHistoryUp()
					return m, nil
				}
				info := m.cursorVisualInfo()
				if info.visualLine > 0 {
					m.lastVisualCol = info.visualCol
					m.setCursorFlat(m.cursorPosFromVisual(info.visualLine-1, m.lastVisualCol))
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
				totalVis := m.visualLineCount()
				info := m.cursorVisualInfo()
				if info.visualLine < totalVis-1 {
					m.lastVisualCol = info.visualCol
					m.setCursorFlat(m.cursorPosFromVisual(info.visualLine+1, m.lastVisualCol))
				} else {
					m.browseHistoryDown()
				}
			}

		case tea.KeyLeft:
			if !m.thinking || m.blocking {
				if m.cursorCol > 0 {
					m.cursorCol--
				} else if m.cursorLine > 0 {
					m.cursorLine--
					m.cursorCol = len(m.inputBuf[m.cursorLine])
				}
				m.lastVisualCol = -1
			}

		case tea.KeyRight:
			if !m.thinking || m.blocking {
				lineLen := len(m.inputBuf[m.cursorLine])
				if m.cursorCol < lineLen {
					m.cursorCol++
				} else if m.cursorLine < len(m.inputBuf)-1 {
					m.cursorLine++
					m.cursorCol = 0
				}
				m.lastVisualCol = -1
			}

		case tea.KeyCtrlLeft:
			if !m.thinking || m.blocking {
				m.setCursorFlat(m.cursorPrevWord())
				m.lastVisualCol = -1
			}

		case tea.KeyCtrlRight:
			if !m.thinking || m.blocking {
				m.setCursorFlat(m.cursorNextWord())
				m.lastVisualCol = -1
			}

		case tea.KeyHome:
			if !m.thinking || m.blocking {
				m.cursorCol = 0
				m.lastVisualCol = -1
			} else {
				m.chatOffset = 1 << 30
				m.userScrolled = true
			}

		case tea.KeyEnd:
			if !m.thinking || m.blocking {
				m.cursorCol = len(m.inputBuf[m.cursorLine])
				m.lastVisualCol = -1
			} else {
				m.chatOffset = 0
				m.userScrolled = false
			}

		case tea.KeyCtrlHome:
			if !m.thinking || m.blocking {
				m.cursorLine, m.cursorCol = 0, 0
				m.lastVisualCol = -1
			} else {
				m.chatOffset = 1 << 30
				m.userScrolled = true
			}

		case tea.KeyCtrlEnd:
			if !m.thinking || m.blocking {
				m.cursorLine = len(m.inputBuf) - 1
				if m.cursorLine < 0 {
					m.cursorLine = 0
				}
				m.cursorCol = len(m.inputBuf[m.cursorLine])
				m.lastVisualCol = -1
			} else {
				m.chatOffset = 0
				m.userScrolled = false
			}

		case tea.KeyCtrlA:
			if !m.thinking || m.blocking {
				m.cursorCol = 0
			}

		case tea.KeyCtrlE:
			if !m.thinking || m.blocking {
				m.cursorCol = len(m.inputBuf[m.cursorLine])
			}

		case tea.KeyCtrlU:
			if !m.thinking || m.blocking {
				start := m.cursorLineStart()
				cf := m.cursorFlat()
				if start < cf {
					if m.historyIdx != -1 {
						m.historyPrefix = strings.TrimSpace(m.inputText())
						m.historyDraft = m.inputText()
						m.historyIdx = -1
					}
					m.deleteRange(start, cf)
					m.setCursorFlat(start)
				}
			}

		case tea.KeyCtrlK:
			if !m.thinking || m.blocking {
				end := m.cursorLineEnd()
				cf := m.cursorFlat()
				if end > cf {
					if m.historyIdx != -1 {
						m.historyPrefix = strings.TrimSpace(m.inputText())
						m.historyDraft = m.inputText()
						m.historyIdx = -1
					}
					m.deleteRange(cf, end)
				}
			}

		case tea.KeyCtrlW:
			if (!m.thinking || m.blocking) && m.cursorFlat() > 0 {
				if m.historyIdx != -1 {
					m.historyPrefix = strings.TrimSpace(m.inputText())
					m.historyDraft = m.inputText()
					m.historyIdx = -1
				}
				pos := m.cursorPrevWord()
				m.deleteRange(pos, m.cursorFlat())
				m.setCursorFlat(pos)
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
				m.lastVisualCol = -1
				s := msg.String()
				if strings.HasPrefix(s, "<") && strings.HasSuffix(s, "M") {
					return m, nil
				}
				if strings.HasPrefix(s, "\x1b[<") && strings.HasSuffix(s, "M") {
					return m, nil
				}
				if m.historyIdx != -1 {
					m.historyPrefix = strings.TrimSpace(m.inputText())
					m.historyDraft = m.inputText()
					m.historyIdx = -1
				}
				hasPrintable := false
				for _, r := range msg.Runes {
					if unicode.IsPrint(r) {
						hasPrintable = true
						m.insertRuneAt(m.cursorFlat(), r)
						m.cursorCol++
					}
				}
				if hasPrintable {
					m.lastInputTime = time.Now()
				}
				inputStr := m.inputText()
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
						m.inputSelEnd = m.inputLen()
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
				if selStart != selEnd && selStart < m.inputLen() {
					end := selEnd
					if end > m.inputLen() {
						end = m.inputLen()
					}
					text := string(m.subRunes(selStart, end))
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
	// Agent events
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
					m.messages[len(m.messages)-1].cachedLines = nil
				} else {
					for i := range m.messages {
						if m.messages[i].ThinkingActive {
							m.messages[i].ThinkingActive = false
							m.messages[i].cachedLines = nil
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
					m.messages[len(m.messages)-1].cachedLines = nil
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
					m.messages[i].cachedLines = nil
				}
			}
			if content, ok := evt.Data.(string); ok && content != "" {
				if wasStreaming && len(m.messages) > 0 && m.messages[len(m.messages)-1].Role == RoleAssistant {
					m.messages[len(m.messages)-1].Content = content
					m.messages[len(m.messages)-1].cachedLines = nil
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
				result := d["result"]
				m.statusLine = name
				for i := len(m.messages) - 1; i >= 0; i-- {
					msg := &m.messages[i]
					if msg.Role == RoleTool && msg.ToolName == name && msg.ToolStatus == "calling" {
						msg.ToolStatus = "success"
						msg.cachedLines = nil
						if result != "" {
							msg.Content = result
						} else {
							msg.Content = msg.ToolName + " " + msg.ToolArgs
						}
						break
					}
				}
			}

		case "blocking":
			data, _ := evt.Data.(map[string]string)
			if data == nil {
				question := fmt.Sprintf("%v", evt.Data)
				data = map[string]string{"question": question}
			}
			question := data["question"]
			operation := data["operation"]
			tool := data["tool"]
			target := data["target"]
			m.thinking = false
			m.blocking = true
			m.statusLine = ""
			for i := range m.messages {
				if m.messages[i].ThinkingActive {
					m.messages[i].ThinkingActive = false
					m.messages[i].cachedLines = nil
				}
			}
			if operation != "" {
				m.confirmForm.Reset(question, operation, tool, target)
				m.messages = append(m.messages, ChatMessage{
					Role:    RoleAssistant,
					Content: "⚠  " + question,
					Time:    time.Now(),
				})
			} else {
				m.messages = append(m.messages, ChatMessage{
					Role:    RoleAssistant,
					Content: "⚠  " + question,
					Time:    time.Now(),
				})
			}

		case "blocking_complete":
			m.blocking = false
			m.confirmForm.Dismiss()

		case "error":
			m.thinking = false
			m.blocking = false
			m.statusLine = ""
			for i := range m.messages {
				if m.messages[i].ThinkingActive {
					m.messages[i].ThinkingActive = false
					m.messages[i].cachedLines = nil
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
				m.tokenCached += d["prompt_cached"]
				m.sessionCost += provider.LookupPrice(m.modelName).CalculateCost(
					d["prompt"], d["completion"], d["prompt_cached"],
				)
			}
		}

	// External messages
	case openSettingsMsg:
		m.inlineForm.saveSnapshot()
		m.settingsActive = true
		// Fetch is lazy — no auto-fetch here.

	case fetchModelsMsg:
		if m.fetchModelsFn != nil {
			info := msg
			isOracle := m.inlineForm.isOracleModelSelect && m.inlineForm.fetchingOracleModels
			if isOracle {
				m.inlineForm.fetchingOracleModels = true
				m.inlineForm.fetchOracleError = ""
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
				if isOracle {
					switch r := res.(type) {
					case ModelsListMsg:
						return OracleModelsListMsg(r)
					case ModelsErrorMsg:
						return OracleModelsErrorMsg(r)
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

	case OracleModelsListMsg:
		m.inlineForm.fetchingOracleModels = false
		m.inlineForm.availableOracleModels = []ModelEntry(msg)
		m.inlineForm.updateOracleModelFilter()

	case OracleModelsErrorMsg:
		m.inlineForm.fetchingOracleModels = false
		m.inlineForm.fetchOracleError = string(msg)

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

	case UndoResultMsg:
		if msg.Error != "" {
			m.lastError = msg.Error
			m.errorTime = time.Now()
		}
		if msg.Desc != "" {
			m.messages = append(m.messages, ChatMessage{
				Role:    RoleAssistant,
				Content: msg.Desc,
				Time:    time.Now(),
			})
			m.chatOffset = 0
		}
	}

	return m, nil
}
