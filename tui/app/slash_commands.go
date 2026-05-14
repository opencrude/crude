package app

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"crude/tui/input"
	ttypes "crude/tui/types"
)

func (m *App) executeSlashCommand(cmd string) tea.Cmd {
	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		return nil
	}

	var cmds []tea.Cmd
	cmdName := parts[0]

	switch cmdName {
	case "/settings":
		m.settings.SaveSnapshot()
		m.setMode(ModeSettings)
	case "/provider":
		m.settings.SaveSnapshot()
		m.setMode(ModeSettings)
		m.settings.FocusField(0)
	case "/model":
		m.settings.SaveSnapshot()
		m.setMode(ModeSettings)
		m.settings.FocusField(3)
	case "/oracle-model":
		m.settings.SaveSnapshot()
		m.setMode(ModeSettings)
		m.settings.FocusField(4)
	case "/api-key":
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
			if m.OnYoloCmd != nil {
				go m.OnYoloCmd(false)
			}
		} else {
			m.yoloDlg.Reset()
			m.setMode(ModeYolo)
		}
	case "/undo", "/undo-n", "/undo-all", "/changes":
		if m.OnUndoCmd != nil {
			go m.OnUndoCmd(cmd)
		}
	default:
		m.statusbar.SetError(fmt.Sprintf("Unknown command: %s (try /help)", cmdName))
	}

	if len(cmds) > 0 {
		return tea.Batch(cmds...)
	}
	return nil
}
