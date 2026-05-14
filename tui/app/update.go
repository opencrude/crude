package app

import (
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
		m.width = msg.Width
		m.height = msg.Height
		m.msgList.SetSize(msg.Width, msg.Height)
		m.input.SetWidth(msg.Width)
		m.statusbar.SetWidth(msg.Width)
		return m, nil

	case ttypes.TickMsg:
		if m.tokenBuf != "" {
			m.msgList.AppendToken(m.tokenBuf)
			m.tokenBuf = ""
			m.lastTokenTime = time.Now()
		}
		m.spinFrame = int(time.Now().UnixMilli())
		m.statusbar.SetSpinFrame(m.spinFrame)
		m.input.SetSpinFrame(m.spinFrame)
		m.statusbar.ClearExpired()
		m.msgList.InvalidateCache()
		if m.streaming || m.thinking {
			return m, streamingTickCmd()
		}
		return m, tickCmd()

	case tea.KeyMsg:
		if m.msgList.HasActiveSelection() {
			m.msgList.ClearSelection()
			return m, nil
		}
		if msg.Type == tea.KeyCtrlC {
			return m, m.handleCancel(msg)
		}
		if msg.Type == tea.KeyEsc && m.Mode != ModeSettings {
			return m, m.handleCancel(msg)
		}

	case tea.MouseMsg:
		if m.Mode == ModeChat {
			inputStartLine := m.chatAreaHeight + 4
			if m.input.InputSelecting() || msg.Y >= inputStartLine {
				updated, cmd, evt := m.input.Update(msg, m.thinking && !m.blocking, m.blocking, m.chatAreaHeight, m.input.ShowHint())
				m.input = updated
				if cmd != nil {
					cmds = append(cmds, cmd)
				}
				switch e := evt.(type) {
				case input.SubmitEvent:
					cmds = append(cmds, m.submitInput(e.Text))
				case input.SlashCommandEvent:
					cmds = append(cmds, m.executeSlashCommand(e.Command))
				case input.SettingsOpenEvent:
					m.settings.SaveSnapshot()
					m.setMode(ModeSettings)
				}
			} else {
				_, cmd := m.msgList.Update(msg)
				if cmd != nil {
					cmds = append(cmds, cmd)
				}
			}
		} else {
			_, cmd := m.msgList.Update(msg)
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
		}

	case ttypes.SelectionTickMsg:
		_, cmd := m.msgList.Update(msg)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}

	case scrollbar.FadeTickMsg, scrollbar.HideTickMsg:
		cmd := m.msgList.HandleScrollbarTick(msg)
		if cmd != nil {
			cmds = append(cmds, cmd)
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
			m.setMode(ModeChat)
			m.msgList.InvalidateCache()
		}
	case settings.CancelEvent:
		m.settings.RestoreSnapshot()
		m.setMode(ModeChat)
		m.msgList.InvalidateCache()
	}

	case ModeConfirm:
		m.statusbar.SetConfirming(true)
		updated, result, done := m.confirm.Update(msg)
		m.confirm = updated
		if done {
			m.statusbar.SetConfirming(false)
			m.blocking = false
			m.thinking = true
			if m.OnConfirmResult != nil {
				m.OnConfirmResult(result)
			}
			resultMsg := "✓ Allowed"
			switch result.Choice {
			case ttypes.ConfirmAlways:
				resultMsg = "✓ Always allowed (session)"
			case ttypes.ConfirmDeny:
				resultMsg = "✗ Denied"
			}
			m.msgList.AppendMessage(ttypes.ChatMessage{
				Role:    ttypes.RoleAssistant,
				Content: resultMsg,
				Time:    time.Now(),
			})
			m.setMode(ModeChat)
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
			} else {
				content = "YOLO mode cancelled. Safety checks remain active."
			}
			m.msgList.AppendMessage(ttypes.ChatMessage{
				Role:    ttypes.RoleAssistant,
				Content: content,
				Time:    time.Now(),
			})
			if m.OnYoloCmd != nil {
				m.OnYoloCmd(enabled)
			}
			m.setMode(ModeChat)
		}

	case ModeChat:
		if m.thinking && !m.blocking {
			if keyMsg, ok := msg.(tea.KeyMsg); ok {
				switch keyMsg.Type {
				case tea.KeyHome, tea.KeyEnd, tea.KeyCtrlHome, tea.KeyCtrlEnd:
					_, cmd := m.msgList.Update(keyMsg)
					if cmd != nil {
						cmds = append(cmds, cmd)
					}
					return m, tea.Batch(cmds...)
				}
			}
		}

		if _, isMouse := msg.(tea.MouseMsg); !isMouse {
			updated, cmd, evt := m.input.Update(msg, m.thinking && !m.blocking, m.blocking, m.chatAreaHeight, m.input.ShowHint())
			m.input = updated
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
			switch e := evt.(type) {
			case input.SubmitEvent:
				cmds = append(cmds, m.submitInput(e.Text))
			case input.SlashCommandEvent:
				cmds = append(cmds, m.executeSlashCommand(e.Command))
			case input.SettingsOpenEvent:
				m.settings.SaveSnapshot()
				m.setMode(ModeSettings)
			}
		}

		if _, ok := msg.(tea.KeyMsg); ok {
			_, cmd := m.msgList.Update(msg)
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
		m.statusbar.SetError(string(msg))
	case ttypes.UpdateModelMsg:
		m.modelName = string(msg)
		m.msgList.SetModelName(string(msg))
	case ttypes.OpenSettingsMsg:
		m.settings.SaveSnapshot()
		m.setMode(ModeSettings)
	case ttypes.ModelsListMsg:
		m.settings.SetModels([]ttypes.ModelEntry(msg), false)
	case ttypes.OracleModelsListMsg:
		m.settings.SetModels([]ttypes.ModelEntry(msg), true)
	case ttypes.ModelsErrorMsg:
		m.settings.SetModelsError(string(msg), false)
	case ttypes.OracleModelsErrorMsg:
		m.settings.SetModelsError(string(msg), true)
	case ttypes.SettingsSaveResultMsg:
		if msg.Err != nil {
			m.statusbar.SetError(msg.Err.Error())
		} else {
			m.modelName = msg.Settings.Model
			m.lastSettings = msg.Settings
			m.setMode(ModeChat)
			m.msgList.InvalidateCache()
			m.statusbar.SetInfo("settings saved")
		}
	case rendering.ClipboardCopiedMsg:
		m.statusbar.SetInfo("copied to clipboard")
	case rendering.ClipboardErrorMsg:
		m.statusbar.SetError(string(msg))
	case ttypes.UndoResultMsg:
		if msg.Error != "" {
			m.statusbar.SetError(msg.Error)
		}
		if msg.Desc != "" {
			m.msgList.AppendMessage(ttypes.ChatMessage{
				Role:    ttypes.RoleAssistant,
				Content: msg.Desc,
				Time:    time.Now(),
			})
		}
	}

	return m, tea.Batch(cmds...)
}

func tickCmd() tea.Cmd {
	return tea.Tick(500*time.Millisecond, func(t time.Time) tea.Msg {
		return ttypes.TickMsg(t)
	})
}

func streamingTickCmd() tea.Cmd {
	return tea.Tick(16*time.Millisecond, func(t time.Time) tea.Msg {
		return ttypes.TickMsg(t)
	})
}
