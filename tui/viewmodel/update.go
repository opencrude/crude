package viewmodel

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

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
		m.syncAgentWindowTabs()
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

	case tea.KeyPressMsg:
		if m.mainMsgList.HasActiveSelection() {
			m.mainMsgList.ClearSelection()
			return m, nil
		}
		// In launcher mode, route all keys to launcher handler
		if m.Mode == ModeLauncher {
			if msg.String() == "ctrl+c" {
				m.Quit = true
				return m, tea.Quit
			}
			if msg.Code == tea.KeyEsc {
				if m.launcher != nil && m.launcher.filterActive {
					m.launcher.deactivateFilter()
					return m, nil
				}
				m.Quit = true
				return m, tea.Quit
			}
			return m.handleLauncherKey(msg)
		}
		if msg.String() == "ctrl+c" && m.Mode != ModeQuestion {
			m.logger.Debug("CANCEL", "ctrl_c", "mode", modeName(m.Mode))
			return m, m.handleCancel(msg)
		}
		if msg.Code == tea.KeyEsc && m.Mode != ModeSettings && m.Mode != ModeQuestion {
			m.logger.Debug("CANCEL", "esc", "mode", modeName(m.Mode))
			return m, m.handleCancel(msg)
		}

	case tea.MouseClickMsg:
		mouse := msg.Mouse()
		m.logger.Debug("UI", "mouse", "click", "x", mouse.X, "y", mouse.Y, "button", fmt.Sprintf("%d", msg.Button))

		if m.win.IsOpen() && m.win.TabCount() >= 2 && msg.Button == tea.MouseLeft && mouse.Y == 3 {
			adjustedX := mouse.X - m.theme.Spacing.PadLR
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

		if m.overlay.IsOpen() && msg.Button == tea.MouseLeft {
			overlayStartLine := m.chatAreaHeight + 3
			if m.win.IsOpen() && m.win.TabCount() >= 2 {
				overlayStartLine += 2
			}

			if m.overlay.TabCount() >= 2 && mouse.Y == overlayStartLine+1 {
				adjustedX := mouse.X - m.theme.Spacing.PadLR
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
			if activeTab != nil && activeTab.Title == "Question" && m.questionForm.Count() > 1 && mouse.Y >= contentStartLine {
				contentY := mouse.Y - contentStartLine
				innerW := m.width - 2*m.theme.Spacing.PadLR
				sidebarW := m.questionForm.ComputeSidebarWidth(innerW)
				sidebarStartX := m.theme.Spacing.PadLR + innerW - sidebarW
				if mouse.X >= sidebarStartX {
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
			if m.input.InputSelecting() || (mouse.Y >= inputStartLine && !msgListCapturing) {
				updated, cmd, evt := m.input.Update(msg, m.mainAgentActive() && !m.blocking, m.blocking, m.chatAreaHeight, m.input.ShowHint())
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
			} else if msg.Button == tea.MouseLeft {
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
		} else if m.Mode != ModeLauncher {
			_, cmd := m.mainMsgList.Update(msg)
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
		}

	case tea.MouseWheelMsg:
		m.logger.Debug("UI", "mouse", "wheel", "x", msg.X, "y", msg.Y, "button", fmt.Sprintf("%d", msg.Button))

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
				updated, cmd, evt := m.input.Update(msg, m.mainAgentActive() && !m.blocking, m.blocking, m.chatAreaHeight, m.input.ShowHint())
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
			if m.launcher != nil {
				m.lastInteractionTime = time.Now()
				if msg.Button == tea.MouseWheelUp {
					m.launcher.moveUp()
					return m, nil
				}
				if msg.Button == tea.MouseWheelDown {
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

	case tea.MouseMotionMsg:
		mouse := msg.Mouse()
		m.logger.Debug("UI", "mouse", "motion", "x", mouse.X, "y", mouse.Y, "button", fmt.Sprintf("%d", msg.Button))

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
			if m.input.InputSelecting() || (mouse.Y >= inputStartLine && !msgListCapturing) {
				updated, cmd, evt := m.input.Update(msg, m.mainAgentActive() && !m.blocking, m.blocking, m.chatAreaHeight, m.input.ShowHint())
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
			} else {
				_, cmd := activeML.Update(msg)
				if cmd != nil {
					cmds = append(cmds, cmd)
				}
			}
		} else {
			_, cmd := m.mainMsgList.Update(msg)
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
		}

	case tea.MouseReleaseMsg:
		mouse := msg.Mouse()
		m.logger.Debug("UI", "mouse", "release", "x", mouse.X, "y", mouse.Y)

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
			if m.input.InputSelecting() || (mouse.Y >= inputStartLine && !msgListCapturing) {
				updated, cmd, evt := m.input.Update(msg, m.mainAgentActive() && !m.blocking, m.blocking, m.chatAreaHeight, m.input.ShowHint())
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
			} else {
				_, cmd := activeML.Update(msg)
				if cmd != nil {
					cmds = append(cmds, cmd)
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
			if !sp.Done {
				if sp.MsgList != nil {
					paneCmd := sp.MsgList.HandleScrollbarTick(msg)
					if paneCmd != nil {
						cmds = append(cmds, paneCmd)
					}
				}
				sp.HandleScrollbarTick(msg)
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
		if keyMsg, ok := msg.(tea.KeyPressMsg); ok && m.overlay.TabCount() >= 2 {
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
		if keyMsg, ok := msg.(tea.KeyPressMsg); ok && m.overlay.TabCount() >= 2 {
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
		keyMsg, isKey := msg.(tea.KeyPressMsg)
		inTextMode := m.questionForm.IsTextQuestion(m.questionForm.Focused())

		if inTextMode {
			isCancelKey := isKey && (keyMsg.Code == tea.KeyEsc || keyMsg.String() == "ctrl+c")
			isSubmitKey := isKey && keyMsg.String() == "ctrl+s"
			isTabKey := isKey && keyMsg.Code == tea.KeyTab

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
				if keyMsg.Mod.Contains(tea.ModShift) {
					m.questionForm.FocusPrev()
				} else {
					m.questionForm.FocusNext()
				}
				m.syncQuestionInputPrompt()
			} else {
				if !m.isMouseMsg(msg) {
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
			isCancelKey := isKey && (keyMsg.Code == tea.KeyEsc || keyMsg.String() == "ctrl+c")
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
		if keyMsg, ok := msg.(tea.KeyPressMsg); ok && m.overlay.TabCount() >= 2 {
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
			if keyMsg, ok := msg.(tea.KeyPressMsg); ok {
				switch keyMsg.Code {
				case tea.KeyHome, tea.KeyEnd:
					_, cmd := m.mainMsgList.Update(keyMsg)
					if cmd != nil {
						cmds = append(cmds, cmd)
					}
					return m, tea.Batch(cmds...)
				}
			}
		}

		if keyMsg, ok := msg.(tea.KeyPressMsg); ok {
			if m.tabBarFocused {
				switch keyMsg.Code {
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

		if !m.isMouseMsg(msg) {
			updated, cmd, evt := m.input.Update(msg, m.mainAgentActive() && !m.blocking, m.blocking, m.chatAreaHeight, m.input.ShowHint())
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

		if _, ok := msg.(tea.KeyPressMsg); ok {
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

// isMouseMsg returns true if the message is any of the Bubble Tea v2 mouse message types.
func (m *App) isMouseMsg(msg tea.Msg) bool {
	switch msg.(type) {
	case tea.MouseClickMsg, tea.MouseMotionMsg, tea.MouseReleaseMsg, tea.MouseWheelMsg:
		return true
	}
	return false
}

func (m *App) routeMouseToInlinePane(msg tea.Msg) (bool, tea.Cmd) {
	chatAreaTop := 3
	if m.win.IsOpen() && m.win.TabCount() >= 2 {
		chatAreaTop += 2
	}

	// Extract Y coordinate and button from the mouse message
	var y int
	var isWheel bool
	var wheelUp, wheelDown bool

	switch mm := msg.(type) {
	case tea.MouseClickMsg:
		mouse := mm.Mouse()
		y = mouse.Y
	case tea.MouseWheelMsg:
		y = mm.Y
		isWheel = true
		wheelUp = mm.Button == tea.MouseWheelUp
		wheelDown = mm.Button == tea.MouseWheelDown
	case tea.MouseMotionMsg:
		y = mm.Mouse().Y
	case tea.MouseReleaseMsg:
		y = mm.Mouse().Y
	default:
		return false, nil
	}

	paneLineMap := m.mainMsgList.PaneLineMap()
	y2 := y - chatAreaTop
	if y2 < 0 || y2 >= len(paneLineMap) {
		return false, nil
	}

	info := paneLineMap[y2]
	if info.PaneID == "" {
		return false, nil
	}

	// LineIdx < 0 means this is the subpane header line (not content),
	// which has no scrollbar position — skip mouse routing for it.
	if info.LineIdx < 0 {
		return false, nil
	}

	sp := m.paneMgr.Get(info.PaneID)
	if sp == nil || sp.Done {
		return false, nil
	}

	if sp.CanScrollCompact() {
		if wheelUp {
			sp.ScrollCompactUp()
			m.mainMsgList.InvalidateSubPane(sp.ID)
			return true, nil
		}
		if wheelDown {
			sp.ScrollCompactDown()
			m.mainMsgList.InvalidateSubPane(sp.ID)
			return true, nil
		}
		return true, nil
	}

	if sp.MsgList == nil {
		return false, nil
	}

	if isWheel && !sp.MsgList.CanScroll() {
		return false, nil
	}

	_, cmd := sp.MsgList.Update(msg)
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
			label = q.Question
			if rendering.VisibleWidth(label) > 16 {
				label = rendering.TruncateVisual(label, 16, "…")
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
