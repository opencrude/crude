package viewmodel

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"crude/tui/input"
	"crude/tui/rendering"
	"crude/tui/scrollbar"
	"crude/tui/settings"
	ttypes "crude/tui/types"
	"crude/tui/window"
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
		if msg.Type == tea.KeyCtrlC {
			m.logger.Debug("CANCEL", "ctrl_c", "mode", modeName(m.Mode))
			return m, m.handleCancel(msg)
		}
		if msg.Type == tea.KeyEsc && m.Mode != ModeSettings {
			m.logger.Debug("CANCEL", "esc", "mode", modeName(m.Mode))
			return m, m.handleCancel(msg)
		}

	case tea.MouseMsg:
		m.logger.Debug("UI", "mouse", "type", fmt.Sprintf("%d", msg.Type), "x", msg.X, "y", msg.Y, "button", fmt.Sprintf("%d", msg.Button))

		if m.win.IsOpen() && msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
			var hit window.TabHitResult

			if m.win.Layout() == window.TabVertical {
				windowStartLine := m.chatAreaHeight + 3
				windowEndLine := windowStartLine + len(m.renderWindowContent())
				if msg.Y >= windowStartLine && msg.Y < windowEndLine {
					adjustedX := msg.X - m.theme.Spacing.PadLR
					adjustedY := msg.Y - windowStartLine
					if adjustedX < 0 {
						adjustedX = 0
					}
					if adjustedY < 0 {
						adjustedY = 0
					}
					hit = m.win.HitTest(adjustedX, adjustedY)
				}
			} else {
				windowTabBarLine := m.chatAreaHeight + 5
				if msg.Y == windowTabBarLine {
					adjustedX := msg.X - m.theme.Spacing.PadLR
					if adjustedX < 0 {
						adjustedX = 0
					}
					hit = m.win.HitTest(adjustedX, 0)
				}
			}

			if hit.TabIndex >= 0 {
				if hit.IsClose {
					cmds = append(cmds, m.handleWindowTabClose(hit.TabIndex))
					return m, tea.Batch(cmds...)
				}
				m.win.SwitchTo(hit.TabIndex)
				m.syncActiveTabMode()
				return m, nil
			}
		}

		if m.Mode == ModeChat {
			inputStartLine := m.chatAreaHeight + 4
			msgListCapturing := m.mainMsgList.IsSelecting() || m.mainMsgList.IsScrollbarDragging()
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
			} else {
				handled, paneCmd := m.routeMouseToInlinePane(msg)
				if paneCmd != nil {
					cmds = append(cmds, paneCmd)
				}
				if !handled {
					_, cmd := m.mainMsgList.Update(msg)
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
			if tab := m.win.TabByTitle("Settings"); tab != nil {
				m.win.CloseTabByID(tab.ID)
			}
			if !m.win.IsOpen() || !m.win.HasClosableTabs() {
				m.setMode(ModeChat)
			} else {
				m.syncActiveTabMode()
			}
			m.mainMsgList.InvalidateCache()
		}
	case settings.CancelEvent:
		m.logger.Info("CONFIG", "settings_cancel")
		m.settings.RestoreSnapshot()
		if tab := m.win.TabByTitle("Settings"); tab != nil {
			m.win.CloseTabByID(tab.ID)
		}
		if !m.win.IsOpen() || !m.win.HasClosableTabs() {
			m.setMode(ModeChat)
		} else {
			m.syncActiveTabMode()
		}
		m.mainMsgList.InvalidateCache()
	}

	case ModeConfirm:
		m.statusbar.SetConfirming(true)
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
			if tab := m.win.TabByTitle("Confirm"); tab != nil {
				m.win.CloseTabByID(tab.ID)
			}
			if !m.win.IsOpen() || !m.win.HasClosableTabs() {
				m.setMode(ModeChat)
			} else {
				m.syncActiveTabMode()
			}
		}

	case ModeQuestion:
		m.statusbar.SetConfirming(true)
		updated, result, done := m.questionDlg.Update(msg)
		m.questionDlg = updated
		if done {
			m.statusbar.SetConfirming(false)
			m.blocking = false
			ma := m.mainAgent()
			ma.Thinking = true
			m.statusbar.SetThinking(true, false, false)
			if m.OnQuestionResult != nil {
				m.OnQuestionResult(ttypes.QuestionResult(result))
			}
			if tab := m.win.TabByTitle("Question"); tab != nil {
				m.win.CloseTabByID(tab.ID)
			}
			if !m.win.IsOpen() || !m.win.HasClosableTabs() {
				m.setMode(ModeChat)
			} else {
				m.syncActiveTabMode()
			}
		}

	case ModeYolo:
		m.statusbar.SetConfirming(true)
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
			if tab := m.win.TabByTitle("YOLO"); tab != nil {
				m.win.CloseTabByID(tab.ID)
			}
			if !m.win.IsOpen() || !m.win.HasClosableTabs() {
				m.setMode(ModeChat)
			} else {
				m.syncActiveTabMode()
			}
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
			if tab := m.win.TabByTitle("Settings"); tab != nil {
				m.win.CloseTabByID(tab.ID)
			}
			if !m.win.IsOpen() || !m.win.HasClosableTabs() {
				m.setMode(ModeChat)
			} else {
				m.syncActiveTabMode()
			}
			m.mainMsgList.InvalidateCache()
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
	paneLineMap := m.mainMsgList.PaneLineMap()
	y := msg.Y - chatAreaTop
	if y < 0 || y >= len(paneLineMap) {
		return false, nil
	}

	info := paneLineMap[y]
	if info.PaneID == "" {
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

func (m *App) handleWindowTabClose(index int) tea.Cmd {
	if index < 0 || index >= m.win.TabCount() {
		return nil
	}
	tab := m.win.Tabs()[index]
	if !tab.Closable {
		return nil
	}

	switch tab.Title {
	case "Settings":
		m.settings.RestoreSnapshot()
		m.win.CloseTab(index)
		if !m.win.IsOpen() || !m.win.HasClosableTabs() {
			m.setMode(ModeChat)
		} else {
			m.syncActiveTabMode()
		}
		m.mainMsgList.InvalidateCache()
	case "Confirm":
		m.confirm.Dismiss()
		m.blocking = false
		m.statusbar.SetConfirming(false)
		m.win.CloseTab(index)
		if !m.win.IsOpen() || !m.win.HasClosableTabs() {
			m.setMode(ModeChat)
		} else {
			m.syncActiveTabMode()
		}
		if m.OnConfirmResult != nil {
			m.OnConfirmResult(ttypes.ConfirmResult{Choice: ttypes.ConfirmDeny})
		}
	case "Question":
		m.questionDlg.Dismiss()
		m.blocking = false
		m.statusbar.SetConfirming(false)
		m.win.CloseTab(index)
		if !m.win.IsOpen() || !m.win.HasClosableTabs() {
			m.setMode(ModeChat)
		} else {
			m.syncActiveTabMode()
		}
		if m.OnQuestionResult != nil {
			m.OnQuestionResult(ttypes.QuestionResult{Cancelled: true})
		}
	case "YOLO":
		m.yoloDlg.Dismiss()
		m.statusbar.SetConfirming(false)
		m.win.CloseTab(index)
		if !m.win.IsOpen() || !m.win.HasClosableTabs() {
			m.setMode(ModeChat)
		} else {
			m.syncActiveTabMode()
		}
	default:
		m.win.CloseTab(index)
		if !m.win.IsOpen() || !m.win.HasClosableTabs() {
			m.setMode(ModeChat)
		} else {
			m.syncActiveTabMode()
		}
	}

	return nil
}

func (m *App) syncActiveTabMode() {
	activeTab := m.win.ActiveTab()
	if activeTab == nil {
		m.Mode = ModeChat
		m.formHint = ""
		return
	}
	switch activeTab.Title {
	case "Settings":
		m.Mode = ModeSettings
	case "Confirm":
		m.Mode = ModeConfirm
	case "Question":
		m.Mode = ModeQuestion
	case "YOLO":
		m.Mode = ModeYolo
	default:
		m.Mode = ModeChat
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
