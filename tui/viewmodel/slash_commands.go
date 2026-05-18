package viewmodel

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"crude/tui/input"
	ttypes "crude/tui/types"
)

func (m *App) executeSlashCommand(cmd string) tea.Cmd {
	m.logger.Info("INPUT", "slash_command", "cmd", cmd)

	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		return nil
	}

	var cmds []tea.Cmd
	cmdName := parts[0]

	switch cmdName {
	case "/settings":
		m.logger.Info("INPUT", "slash", "command", "settings")
		m.settings.SaveSnapshot()
		m.setMode(ModeSettings)
	case "/provider":
		m.logger.Info("INPUT", "slash", "command", "provider")
		m.settings.SaveSnapshot()
		m.setMode(ModeSettings)
		m.settings.FocusField(0)
	case "/model":
		m.logger.Info("INPUT", "slash", "command", "model")
		m.settings.SaveSnapshot()
		m.setMode(ModeSettings)
		m.settings.FocusField(3)
	case "/api-key":
		m.logger.Info("INPUT", "slash", "command", "api-key")
		m.settings.SaveSnapshot()
		m.setMode(ModeSettings)
		m.settings.FocusField(2)
	case "/effort":
		efforts := []string{"none", "low", "medium", "high", "xhigh"}
		idx := 0
		for i, v := range efforts {
			if v == m.lastSettings.ThinkingEffort {
				idx = i
				break
			}
		}
		idx = (idx + 1) % len(efforts)
		m.lastSettings.ThinkingEffort = efforts[idx]
		m.settings.SetFieldValue(5, efforts[idx])
		m.logger.Info("INPUT", "slash", "command", "effort", "value", efforts[idx])
		if m.OnSettingsSave != nil {
			onSave := m.OnSettingsSave
			s := m.lastSettings
			cmds = append(cmds, func() tea.Msg {
				err := onSave(s)
				return ttypes.SettingsSaveResultMsg{Settings: s, Err: err}
			})
		}
		m.statusbar.SetInfo(fmt.Sprintf("Thinking effort: %s", efforts[idx]))
	case "/help":
		m.logger.Info("INPUT", "slash", "command", "help")
		var b strings.Builder
		b.WriteString("Available commands:\n")
		for _, c := range input.AvailableCmds() {
			b.WriteString(fmt.Sprintf("  %-16s %s\n", "/"+c.Label, c.Desc))
		}
		m.mainMsgList.AppendMessage(ttypes.ChatMessage{Role: ttypes.RoleAssistant, Content: b.String(), Time: time.Now()})
	case "/yolo":
		if m.yolo {
			m.yolo = false
			m.mainMsgList.ScrollToBottom()
			m.statusbar.SetInfo("YOLO mode disabled. Safety checks restored.")
			m.logger.Info("SAFETY", "yolo_disabled", "source", "slash")
			if m.OnYoloCmd != nil {
				go m.OnYoloCmd(false)
			}
		} else {
			m.yoloDlg.Reset()
			m.setMode(ModeYolo)
			m.logger.Info("SAFETY", "yolo_prompt", "source", "slash")
		}
	case "/undo", "/undo-n", "/undo-all", "/changes":
		m.logger.Info("INPUT", "slash", "command", cmd)
		if m.OnUndoCmd != nil {
			go m.OnUndoCmd(cmd)
		}
	default:
		m.logger.Warn("INPUT", "slash_unknown", "cmd", cmdName)
		m.statusbar.SetError(fmt.Sprintf("Unknown command: %s (try /help)", cmdName))
	}

	if len(cmds) > 0 {
		return tea.Batch(cmds...)
	}
	return nil
}
