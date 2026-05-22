package viewmodel

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"crude/tui/input"
	"crude/tui/rendering"
	"crude/tui/scrollbar"
	"crude/tui/settings"
	ttypes "crude/tui/types"
)

func (m *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.logger.Debug("UI", "resize", "width", msg.Width, "height", msg.Height)
		m.width = msg.Width
		m.height = msg.Height
		m.mainMsgList.SetSize(msg.Width, msg.Height)
		for _, a := range m.agents {
			if a.MsgList != m.mainMsgList {
				a.MsgList.SetSize(msg.Width, msg.Height)
			}
		}
		m.paneMgr.SetSize(msg.Width, msg.Height)
		m.input.SetWidth(msg.Width)
		m.statusbar.SetWidth(msg.Width)
		return m, nil

	case ttypes.TickMsg:
		spinFrame := int(time.Now().UnixMilli())
		m.spinFrame = spinFrame
		m.statusbar.SetSpinFrame(spinFrame)
		m.input.SetSpinFrame(spinFrame)
		m.statusbar.ClearExpired()
		for _, a := range m.agents {
			a.Tick(spinFrame)
		}
		m.paneMgr.TickAll(spinFrame)
		for _, sp := range m.paneMgr.panes {
			if !sp.Done && sp.MsgList != nil {
				if agent, ok := m.agents[sp.ID]; ok {
					if agent.TokenBuf != "" {
						sp.MsgList.AppendToken(agent.TokenBuf)
						agent.TokenBuf = ""
						agent.LastTokenTime = time.Now()
					}
				}
				m.mainMsgList.InvalidateSubPane(sp.ID)
			}
		}
		if m.anyActive() || m.Mode == ModeLauncher {
			return m, streamingTickCmd()
		}
		return m, tickCmd()

	case tea.KeyMsg:
		if m.mainMsgList.HasActiveSelection() {
			m.mainMsgList.ClearSelection()
			return m, nil
		}
		// In launcher mode, route all keys to launcher handler
		if m.Mode == ModeLauncher {
			if msg.Type == tea.KeyCtrlC {
				m.Quit = true
				return m, tea.Quit
			}
			if msg.Type == tea.KeyEsc {
				if m.launcher != nil && m.launcher.filterActive {
					m.launcher.deactivateFilter()
					return m, nil
				}
				m.Quit = true
				return m, tea.Quit
			}
			return m.handleLauncherKey(msg)
		}
		if msg.Type == tea.KeyCtrlC && m.Mode != ModeQuestion {
			m.logger.Debug("CANCEL", "ctrl_c", "mode", modeName(m.Mode))
			return m, m.handleCancel(msg)
		}
		if msg.Type == tea.KeyEsc && m.Mode != ModeSettings && m.Mode != ModeQuestion {
			m.logger.Debug("CANCEL", "esc", "mode", modeName(m.Mode))
			return m, m.handleCancel(msg)
		}

	case tea.MouseMsg:
		m.logger.Debug("UI", "mouse", "type", fmt.Sprintf("%d", msg.Type), "x", msg.X, "y", msg.Y, "button", fmt.Sprintf("%d", msg.Button))

		if m.win.IsOpen() && m.win.TabCount() >= 2 && msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft && msg.Y == 3 {
			adjustedX := msg.X - m.theme.Spacing.PadLR
			if adjustedX < 0 {
				adjustedX = 0
			}
			hit := m.win.HitTest(adjustedX, 0)
			if hit.TabIndex >= 0 {
				if hit.IsClose {
					cmds = append(cmds, m.handleAgentTabClose(hit.TabIndex))
					return m, tea.Batch(cmds...)
				}
				m.win.SwitchTo(hit.TabIndex)
				m.handleTabSwitch()
				return m, nil
			}
		}

		if m.overlay.IsOpen() && msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
			overlayStartLine := m.chatAreaHeight + 3
			if m.win.IsOpen() && m.win.TabCount() >= 2 {
				overlayStartLine += 2
			}

			if m.overlay.TabCount() >= 2 && msg.Y == overlayStartLine+1 {
				adjustedX := msg.X - m.theme.Spacing.PadLR
				if adjustedX < 0 {
					adjustedX = 0
				}
				hit := m.overlay.HitTest(adjustedX, 0)
				if hit.TabIndex >= 0 {
					if hit.IsClose {
						cmds = append(cmds, m.handleOverlayTabClose(hit.TabIndex))
						return m, tea.Batch(cmds...)
					}
					m.overlay.SwitchTo(hit.TabIndex)
					m.syncOverlayMode()
					return m, nil
				}
			}

			overlayContentOffset := 1
			if m.overlay.TabCount() >= 2 {
				overlayContentOffset = 3
			}
			contentStartLine := overlayStartLine + overlayContentOffset

			activeTab := m.overlay.ActiveTab()
			if activeTab != nil && activeTab.Title == "Question" && m.questionForm.Count() > 1 && msg.Y >= contentStartLine {
				contentY := msg.Y - contentStartLine
				innerW := m.width - 2*m.theme.Spacing.PadLR
				sidebarW := m.questionForm.ComputeSidebarWidth(innerW)
				sidebarStartX := m.theme.Spacing.PadLR + innerW - sidebarW
				if msg.X >= sidebarStartX {
					itemIdx := contentY / 2
					if itemIdx >= 0 && itemIdx < m.questionForm.Count() {
						m.questionForm.SetFocused(itemIdx)
						m.syncQuestionInputPrompt()
						return m, nil
					}
				}
			}
		}

		if m.Mode == ModeChat {
			tabBarExtra := 0
			if m.win.IsOpen() && m.win.TabCount() >= 2 {
				tabBarExtra = 2
			}
			overlayExtra := 0
			if m.overlay.IsOpen() {
				overlayExtra = len(m.renderOverlayArea())
			}
			inputStartLine := m.chatAreaHeight + 4 + tabBarExtra + overlayExtra
			activeML := m.activeMessageList()
			msgListCapturing := activeML.IsSelecting() || activeML.IsScrollbarDragging()
			if m.input.InputSelecting() || (msg.Y >= inputStartLine && !msgListCapturing) {
				updated, cmd, evt := m.input.Update(msg, m.anyActive() && !m.blocking, m.blocking, m.chatAreaHeight, m.input.ShowHint())
				m.input = updated
				if cmd != nil {
					cmds = append(cmds, cmd)
				}
				switch e := evt.(type) {
				case input.SubmitEvent:
					cmds = append(cmds, m.submitInput(e.Text))
				case input.BtwSubmitEvent:
					cmds = append(cmds, m.submitBtw(e.Text))
				case input.SlashCommandEvent:
					cmds = append(cmds, m.executeSlashCommand(e.Command))
				case input.SettingsOpenEvent:
					m.settings.SaveSnapshot()
					m.setMode(ModeSettings)
				}
			} else if msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionPress {
				handled, paneCmd := m.routeMouseToInlinePane(msg)
				if paneCmd != nil {
					cmds = append(cmds, paneCmd)
				}
				if !handled {
					_, cmd := activeML.Update(msg)
					if cmd != nil {
						cmds = append(cmds, cmd)
					}
				}
			} else {
				handled, paneCmd := m.routeMouseToInlinePane(msg)
				if paneCmd != nil {
					cmds = append(cmds, paneCmd)
				}
				if !handled {
					_, cmd := activeML.Update(msg)
					if cmd != nil {
						cmds = append(cmds, cmd)
					}
				}
			}
		} else if m.Mode == ModeLauncher {
			// Mouse handling in launcher mode
			if m.launcher != nil {
				m.lastInteractionTime = time.Now()
				if msg.Button == tea.MouseButtonWheelUp {
					m.launcher.moveUp()
					return m, nil
				}
				if msg.Button == tea.MouseButtonWheelDown {
					m.launcher.moveDown()
					return m, nil
				}
			}
		} else {
			_, cmd := m.mainMsgList.Update(msg)
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
		}

	case ttypes.SelectionTickMsg:
		_, cmd := m.mainMsgList.Update(msg)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}

	case scrollbar.FadeTickMsg, scrollbar.HideTickMsg:
		cmd := m.mainMsgList.HandleScrollbarTick(msg)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
		for _, sp := range m.paneMgr.panes {
			if !sp.Done && sp.MsgList != nil {
				paneCmd := sp.MsgList.HandleScrollbarTick(msg)
				if paneCmd != nil {
					cmds = append(cmds, paneCmd)
				}
			}
		}
	}

	switch m.Mode {
	case ModeSettings:
		updated, cmd, evt := m.settings.Update(msg)
		m.settings = updated
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	switch e := evt.(type) {
	case settings.SaveEvent:
		s := e.Settings
		m.logger.Info("CONFIG", "settings_save", "provider", s.ProviderID, "model", s.Model)
		if m.OnSettingsSave != nil {
			m.statusbar.SetInfo("saving...")
			onSave := m.OnSettingsSave
			cmds = append(cmds, func() tea.Msg {
				err := onSave(s)
				return ttypes.SettingsSaveResultMsg{Settings: s, Err: err}
			})
		} else {
			m.modelName = s.Model
			m.lastSettings = s
			m.closeOverlayTab("Settings")
		}
	case settings.CancelEvent:
		m.logger.Info("CONFIG", "settings_cancel")
		m.settings.RestoreSnapshot()
		m.closeOverlayTab("Settings")
	}

	case ModeConfirm:
		m.statusbar.SetConfirming(true)
		if keyMsg, ok := msg.(tea.KeyMsg); ok && m.overlay.TabCount() >= 2 {
			switch keyMsg.String() {
			case "ctrl+]":
				m.overlay.Next()
				m.syncOverlayMode()
				return m, nil
			case "ctrl+[":
				m.overlay.Prev()
				m.syncOverlayMode()
				return m, nil
			}
		}
		updated, result, done := m.confirm.Update(msg)
		m.confirm = updated
		if done {
			m.statusbar.SetConfirming(false)
			m.blocking = false
			ma := m.mainAgent()
			ma.Thinking = true
			m.statusbar.SetThinking(true, false, false)
			if m.OnConfirmResult != nil {
				m.OnConfirmResult(result)
				m.logger.Info("BLOCK", "resolved", "response", result.Choice, "operation", result.Operation, "tool", result.Tool)
			}
			switch result.Choice {
			case ttypes.ConfirmAllow:
				ma.pendingPermTag = "allowed"
			case ttypes.ConfirmAlways:
				ma.pendingPermTag = "always"
			case ttypes.ConfirmDeny:
				ma.pendingPermTag = "denied"
				m.mainMsgList.SetToolPermTag(result.Tool, "", "denied")
			}
			m.closeOverlayTab("Confirm")
		}

	case ModeQuestion:
		m.statusbar.SetConfirming(true)
		if keyMsg, ok := msg.(tea.KeyMsg); ok && m.overlay.TabCount() >= 2 {
			switch keyMsg.String() {
			case "ctrl+]":
				m.overlay.Next()
				m.syncOverlayMode()
				return m, nil
			case "ctrl+[":
				m.overlay.Prev()
				m.syncOverlayMode()
				return m, nil
			}
		}
		keyMsg, isKey := msg.(tea.KeyMsg)
		inTextMode := m.questionForm.IsTextQuestion(m.questionForm.Focused())

		if inTextMode {
			isCancelKey := isKey && (keyMsg.Type == tea.KeyEsc || keyMsg.Type == tea.KeyCtrlC)
			isSubmitKey := isKey && keyMsg.String() == "ctrl+s"
			isTabKey := isKey && (keyMsg.Type == tea.KeyTab || keyMsg.Type == tea.KeyShiftTab)

			if isCancelKey {
				m.questionForm.ExitTextMode(m.questionForm.Focused())
				m.input.ClearInput()
				m.syncQuestionInputPrompt()
			} else if isSubmitKey {
				text := strings.TrimSpace(m.input.Value())
				if text != "" {
					m.questionForm.SetAnswer(m.questionForm.Focused(), text)
				}
				if m.questionForm.AllAnswered() {
					result := m.questionForm.BuildResult()
					m.statusbar.SetConfirming(false)
					m.blocking = false
					ma := m.mainAgent()
					ma.Thinking = true
					m.statusbar.SetThinking(true, false, false)
					if m.OnQuestionResult != nil {
						m.OnQuestionResult(ttypes.QuestionResult(result))
					}
					m.closeAllQuestionOverlayTabs()
					m.input.ResetPrompt()
					m.input.ClearInput()
				}
			} else if isTabKey {
				if keyMsg.Type == tea.KeyShiftTab {
					m.questionForm.FocusPrev()
				} else {
					m.questionForm.FocusNext()
				}
				m.syncQuestionInputPrompt()
			} else {
				if _, ok := msg.(tea.MouseMsg); !ok {
					updated, cmd, evt := m.input.Update(msg, false, false, m.chatAreaHeight, false)
					m.input = updated
					if cmd != nil {
						cmds = append(cmds, cmd)
					}
					switch e := evt.(type) {
					case input.SubmitEvent:
						m.questionForm.SetAnswer(m.questionForm.Focused(), e.Text)
						m.input.ClearInput()
						if m.questionForm.AllAnswered() {
							result := m.questionForm.BuildResult()
							m.statusbar.SetConfirming(false)
							m.blocking = false
							ma := m.mainAgent()
							ma.Thinking = true
							m.statusbar.SetThinking(true, false, false)
							if m.OnQuestionResult != nil {
								m.OnQuestionResult(ttypes.QuestionResult(result))
							}
							m.closeAllQuestionOverlayTabs()
							m.input.ResetPrompt()
						} else {
							m.questionForm.FocusNext()
							m.syncQuestionInputPrompt()
						}
					}
				}
			}
		} else {
			isCancelKey := isKey && (keyMsg.Type == tea.KeyEsc || keyMsg.Type == tea.KeyCtrlC)
			if isCancelKey {
				m.questionForm.Dismiss()
				m.blocking = false
				m.statusbar.SetConfirming(false)
				m.input.ResetPrompt()
				m.input.ClearInput()
				m.closeAllQuestionOverlayTabs()
				if m.OnQuestionResult != nil {
					m.OnQuestionResult(ttypes.QuestionResult{Cancelled: true})
				}
			} else {
				updated, result, done := m.questionForm.Update(msg)
				m.questionForm = updated
				if !done && m.questionForm.IsTextQuestion(m.questionForm.Focused()) {
					existing := m.questionForm.Answer(m.questionForm.Focused())
					if existing != "" {
						m.input.SetInput(existing)
					} else {
						m.input.ClearInput()
					}
				}
				m.syncQuestionInputPrompt()
				if done {
					m.statusbar.SetConfirming(false)
					m.blocking = false
					ma := m.mainAgent()
					ma.Thinking = true
					m.statusbar.SetThinking(true, false, false)
					if m.OnQuestionResult != nil {
						m.OnQuestionResult(ttypes.QuestionResult(result))
					}
					m.closeAllQuestionOverlayTabs()
					m.input.ResetPrompt()
					m.input.ClearInput()
				} else {
					m.syncQuestionInputPrompt()
				}
			}
		}

	case ModeYolo:
		m.statusbar.SetConfirming(true)
		if keyMsg, ok := msg.(tea.KeyMsg); ok && m.overlay.TabCount() >= 2 {
			switch keyMsg.String() {
			case "ctrl+]":
				m.overlay.Next()
				m.syncOverlayMode()
				return m, nil
			case "ctrl+[":
				m.overlay.Prev()
				m.syncOverlayMode()
				return m, nil
			}
		}
		updated, enabled, done := m.yoloDlg.Update(msg)
		m.yoloDlg = updated
		if done {
			m.statusbar.SetConfirming(false)
			m.yolo = enabled
			var content string
			if enabled {
				content = "⚠ YOLO mode ACTIVE — all safety checks disabled. Use /yolo again to restore."
				m.logger.Warn("SAFETY", "yolo_enabled")
			} else {
				content = "YOLO mode cancelled. Safety checks remain active."
				m.logger.Info("SAFETY", "yolo_disabled")
			}
			m.mainMsgList.AppendMessage(ttypes.ChatMessage{
				Role:    ttypes.RoleAssistant,
				Content: content,
				Time:    time.Now(),
			})
			if m.OnYoloCmd != nil {
				m.OnYoloCmd(enabled)
			}
			m.closeOverlayTab("YOLO")
		}

		case ModeChat:
		if m.anyActive() && !m.blocking {
			if keyMsg, ok := msg.(tea.KeyMsg); ok {
				switch keyMsg.Type {
				case tea.KeyHome, tea.KeyEnd, tea.KeyCtrlHome, tea.KeyCtrlEnd:
					_, cmd := m.mainMsgList.Update(keyMsg)
					if cmd != nil {
						cmds = append(cmds, cmd)
					}
					return m, tea.Batch(cmds...)
				}
			}
		}

		if keyMsg, ok := msg.(tea.KeyMsg); ok {
			if m.tabBarFocused {
				switch keyMsg.Type {
				case tea.KeyLeft:
					m.win.Prev()
					m.handleTabSwitch()
					return m, nil
				case tea.KeyRight:
					m.win.Next()
					m.handleTabSwitch()
					return m, nil
				case tea.KeyEnter:
					m.tabBarFocused = false
					m.handleTabSwitch()
					return m, nil
				case tea.KeyEsc:
					m.tabBarFocused = false
					return m, nil
				default:
					m.tabBarFocused = false
				}
			}

			switch keyMsg.String() {
			case "ctrl+]", "ctrl+[":
				if m.win.IsOpen() {
					if keyMsg.String() == "ctrl+]" {
						m.win.Next()
					} else {
						m.win.Prev()
					}
					m.handleTabSwitch()
					return m, nil
				}
			case "ctrl+t":
				if m.win.IsOpen() && m.win.TabCount() >= 2 {
					m.tabBarFocused = !m.tabBarFocused
					return m, nil
				}
			}
		}

		if _, isMouse := msg.(tea.MouseMsg); !isMouse {
			updated, cmd, evt := m.input.Update(msg, m.anyActive() && !m.blocking, m.blocking, m.chatAreaHeight, m.input.ShowHint())
			m.input = updated
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
			switch e := evt.(type) {
			case input.SubmitEvent:
				cmds = append(cmds, m.submitInput(e.Text))
			case input.BtwSubmitEvent:
				cmds = append(cmds, m.submitBtw(e.Text))
			case input.SlashCommandEvent:
				cmds = append(cmds, m.executeSlashCommand(e.Command))
			case input.SettingsOpenEvent:
				m.settings.SaveSnapshot()
				m.setMode(ModeSettings)
			}
		}

		if _, ok := msg.(tea.KeyMsg); ok {
			_, cmd := m.mainMsgList.Update(msg)
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
	}

	if msg, ok := msg.(ttypes.AgentMsg); ok {
		cmds = append(cmds, m.handleAgentEvent(ttypes.AgentEventMsg(msg)))
	}

	switch msg := msg.(type) {
	case ttypes.ErrorMsg:
		m.logger.Debug("UI", "error", "msg", string(msg))
		m.statusbar.SetError(string(msg))
	case ttypes.UpdateModelMsg:
		m.modelName = string(msg)
		m.logger.Debug("UI", "model_update", "model", string(msg))
		m.mainMsgList.SetModelName(string(msg))
		for _, a := range m.agents {
			if a.MsgList != m.mainMsgList {
				a.MsgList.SetModelName(string(msg))
			}
		}
	case ttypes.OpenSettingsMsg:
		m.logger.Info("UI", "open_settings")
		m.settings.SaveSnapshot()
		m.setMode(ModeSettings)
	case ttypes.ModelsListMsg:
		m.logger.Info("MODEL", "list_received", "count", len(msg))
		m.settings.SetModels([]ttypes.ModelEntry(msg))
	case ttypes.ModelsErrorMsg:
		m.logger.Error("MODEL", "list_error", "error", string(msg))
		m.settings.SetModelsError(string(msg))
	case ttypes.SettingsSaveResultMsg:
		if msg.Err != nil {
			m.logger.Error("CONFIG", "save_error", "error", msg.Err.Error())
			m.statusbar.SetError(msg.Err.Error())
		} else {
			m.logger.Info("CONFIG", "saved")
			m.modelName = msg.Settings.Model
			m.lastSettings = msg.Settings
			m.closeOverlayTab("Settings")
			m.statusbar.SetInfo("settings saved")
		}
	case rendering.ClipboardCopiedMsg:
		m.logger.Debug("CLIPBOARD", "copied")
		m.statusbar.SetInfo("copied to clipboard")
	case rendering.ClipboardErrorMsg:
		m.logger.Error("CLIPBOARD", "error", "error", string(msg))
		m.statusbar.SetError(string(msg))
	case ttypes.UndoResultMsg:
		if msg.Error != "" {
			m.logger.Error("JOURNAL", "undo_error", "error", msg.Error)
			m.statusbar.SetError(msg.Error)
		}
		if msg.Desc != "" {
			m.logger.Info("JOURNAL", "undo_result", "desc", msg.Desc)
			m.mainMsgList.AppendMessage(ttypes.ChatMessage{
				Role:    ttypes.RoleAssistant,
				Content: msg.Desc,
				Time:    time.Now(),
			})
		}
	case ttypes.HistoryMessagesMsg:
		for _, hm := range msg {
			m.mainMsgList.AppendMessage(hm)
		}
		m.mainMsgList.ScrollToBottom()
	}

	return m, tea.Batch(cmds...)
}

func (m *App) routeMouseToInlinePane(msg tea.MouseMsg) (bool, tea.Cmd) {
	chatAreaTop := 3
	if m.win.IsOpen() && m.win.TabCount() >= 2 {
		chatAreaTop += 2
	}
	paneLineMap := m.mainMsgList.PaneLineMap()
	y := msg.Y - chatAreaTop
	if y < 0 || y >= len(paneLineMap) {
		return false, nil
	}

	info := paneLineMap[y]
	if info.PaneID == "" {
		return false, nil
	}

	// LineIdx < 0 means this is the subpane header line (not content),
	// which has no scrollbar position — skip mouse routing for it.
	if info.LineIdx < 0 {
		return false, nil
	}

	sp := m.paneMgr.Get(info.PaneID)
	if sp == nil || sp.Done || sp.MsgList == nil {
		return false, nil
	}

	if msg.Button == tea.MouseButtonWheelUp || msg.Button == tea.MouseButtonWheelDown {
		if !sp.MsgList.CanScroll() {
			return false, nil
		}
	}

	paneMsg := msg
	paneMsg.Y = info.LineIdx
	_, cmd := sp.MsgList.Update(paneMsg)
	return true, cmd
}

func (m *App) handleAgentTabClose(index int) tea.Cmd {
	if index < 0 || index >= m.win.TabCount() {
		return nil
	}
	tab := m.win.Tabs()[index]
	if !tab.Closable {
		return nil
	}

	if m.isAgentTabTitle(tab.Title) {
		agentID := m.agentIDFromTabTitle(tab.Title)
		m.completeSubAgent(agentID)
		if agentID != MainAgentID && agentID != "" {
			if m.OnCancelSubAgent != nil {
				cancelFn := m.OnCancelSubAgent
				go cancelFn(agentID)
			}
		}
		return nil
	}

	m.win.CloseTab(index)
	return nil
}

func (m *App) handleOverlayTabClose(index int) tea.Cmd {
	if index < 0 || index >= m.overlay.TabCount() {
		return nil
	}
	tab := m.overlay.Tabs()[index]
	if !tab.Closable {
		return nil
	}

	switch tab.Title {
	case "Settings":
		m.settings.RestoreSnapshot()
		m.overlay.CloseTab(index)
		m.syncOverlayMode()
		m.mainMsgList.InvalidateCache()
	case "Confirm":
		m.confirm.Dismiss()
		m.blocking = false
		m.statusbar.SetConfirming(false)
		m.overlay.CloseTab(index)
		m.syncOverlayMode()
		if m.OnConfirmResult != nil {
			m.OnConfirmResult(ttypes.ConfirmResult{Choice: ttypes.ConfirmDeny})
		}
	case "Question":
		m.questionForm.Dismiss()
		m.blocking = false
		m.statusbar.SetConfirming(false)
		m.input.ResetPrompt()
		m.input.ClearInput()
		m.overlay.CloseTab(index)
		m.syncOverlayMode()
		if m.OnQuestionResult != nil {
			m.OnQuestionResult(ttypes.QuestionResult{Cancelled: true})
		}
	case "YOLO":
		m.yoloDlg.Dismiss()
		m.statusbar.SetConfirming(false)
		m.overlay.CloseTab(index)
		m.syncOverlayMode()
	default:
		m.overlay.CloseTab(index)
		m.syncOverlayMode()
	}

	return nil
}

func (m *App) closeAllQuestionOverlayTabs() {
	m.closeOverlayTab("Question")
}

func (m *App) purgeQuestionOverlayTabs() {
	if tab := m.overlay.TabByTitle("Question"); tab != nil {
		m.overlay.CloseTabByID(tab.ID)
	}
}

func (m *App) closeOverlayTab(title string) {
	if tab := m.overlay.TabByTitle(title); tab != nil {
		m.overlay.CloseTabByID(tab.ID)
	}
	m.syncOverlayMode()
	m.mainMsgList.InvalidateCache()
}

func (m *App) syncQuestionInputPrompt() {
	if m.Mode != ModeQuestion {
		m.input.ResetPrompt()
		m.input.ClearInput()
		return
	}
	if tab := m.overlay.TabByTitle("Question"); tab != nil {
		m.overlay.SetTabIconByIndex(m.overlay.IndexOf(tab.ID), m.questionForm.FocusedIcon())
	}
	if m.questionForm.IsTextQuestion(m.questionForm.Focused()) {
		q, _ := m.questionForm.FocusedItem()
		label := q.Header
		if label == "" {
			runes := []rune(q.Question)
			if len(runes) > 16 {
				label = string(runes[:16]) + "…"
			} else {
				label = q.Question
			}
		}
		m.input.SetPrompt(fmt.Sprintf("Q%d %s", m.questionForm.Focused()+1, label))
		existing := m.questionForm.Answer(m.questionForm.Focused())
		if existing != "" {
			m.input.SetInput(existing)
		}
	} else {
		m.input.ResetPrompt()
		m.input.ClearInput()
	}
}

func (m *App) handleTabSwitch() {
	activeTab := m.win.ActiveTab()
	if activeTab == nil {
		return
	}
	if m.isAgentTabTitle(activeTab.Title) {
		agentID := m.agentIDFromTabTitle(activeTab.Title)
		if agentID != m.activeAgentView && agentID != "" {
			m.activeAgentView = agentID
			if agentID == MainAgentID {
				m.activeAgentView = ""
			}
			if m.mainMsgList != nil {
				m.mainMsgList.InvalidateAllMessages()
			}
			if agent, ok := m.agents[agentID]; ok && agent.MsgList != nil {
				agent.MsgList.InvalidateAllMessages()
			}
		}
	}
}

func tickCmd() tea.Cmd {
	return tea.Tick(400*time.Millisecond, func(t time.Time) tea.Msg {
		return ttypes.TickMsg(t)
	})
}

func streamingTickCmd() tea.Cmd {
	return tea.Tick(16*time.Millisecond, func(t time.Time) tea.Msg {
		return ttypes.TickMsg(t)
	})
}
