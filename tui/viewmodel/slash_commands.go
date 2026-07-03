package viewmodel

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/input"
	ttypes "crude/tui/types"
)

func (m *App) executeSlashCommand(cmd string) tea.Cmd {
	if m.logger != nil {
		m.logger.Info("INPUT", "slash_command", "cmd", cmd)
	}

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
		// If viewing a sub-agent, open settings for that agent's category
		if n := m.agentMgr.ActiveNode(); n != nil && n.Category != "" {
			m.settingsCategory = n.Category
			m.setMode(ModeSettings)
			m.settings.OpenCategory(n.Category)
			break
		}
		m.settingsCategory = ""
		m.setMode(ModeSettings)
	case "/provider":
		m.logger.Info("INPUT", "slash", "command", "provider")
		m.settings.SaveSnapshot()
		m.settingsCategory = "main"
		m.setMode(ModeSettings)
		m.settings.OpenDefaultProfileEdit()
	case "/api-key":
		m.logger.Info("INPUT", "slash", "command", "api-key")
		m.settings.SaveSnapshot()
		m.settingsCategory = "main"
		m.setMode(ModeSettings)
		m.settings.OpenDefaultProfileEdit()
	case "/sessions":
		m.logger.Info("INPUT", "slash", "command", "sessions")
		if m.anyActive() {
			m.statusbar.SetError("Cannot switch sessions while agent is running — press ctrl+c to cancel first")
			return nil
		}
		if m.onListSessions != nil {
			sessions := m.onListSessions()
			if len(sessions) > 0 {
				m.launcher = NewLauncherState(sessions)
				m.lastInteractionTime = time.Now()
			} else {
				m.statusbar.SetInfo("No past sessions found")
			}
		} else {
			m.statusbar.SetError("Session browser not available")
		}
	case "/help":
		m.logger.Info("INPUT", "slash", "command", "help")
		var b strings.Builder
		b.WriteString("Available commands:\n")
		for _, c := range input.AvailableCmds() {
			b.WriteString(fmt.Sprintf("  %-16s %s\n", "/"+c.Label, c.Desc))
		}
		if ml := m.activeMsgList(); ml != nil {
			ml.AppendMessage(ttypes.ChatMessage{Role: ttypes.RoleAssistant, Content: b.String(), Time: time.Now()})
		}
	case "/yolo":
		if m.yolo {
			m.yolo = false
			if ml := m.activeMsgList(); ml != nil {
				ml.ScrollToBottom()
			}
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
