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
	if m.Logger != nil {
		m.Logger.Info("INPUT", "slash_command", "cmd", cmd)
	}

	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		return nil
	}

	var cmds []tea.Cmd
	cmdName := parts[0]

	switch cmdName {
	case "/settings":
		m.Logger.Info("INPUT", "slash", "command", "settings")
		m.Settings.SaveSnapshot()
		// If viewing a sub-agent, open settings for that agent's category
		if n := m.AgentMgr.ActiveNode(); n != nil && n.Category != "" {
			m.OverlayState.SetSettingsCategory(n.Category)
			m.setMode(ModeSettings)
			m.Settings.OpenCategory(n.Category)
			break
		}
		m.OverlayState.SetSettingsCategory("")
		m.setMode(ModeSettings)
	case "/provider":
		m.Logger.Info("INPUT", "slash", "command", "provider")
		m.Settings.SaveSnapshot()
		m.OverlayState.SetSettingsCategory("main")
		m.setMode(ModeSettings)
		m.Settings.OpenDefaultProfileEdit()
	case "/api-key":
		m.Logger.Info("INPUT", "slash", "command", "api-key")
		m.Settings.SaveSnapshot()
		m.OverlayState.SetSettingsCategory("main")
		m.setMode(ModeSettings)
		m.Settings.OpenDefaultProfileEdit()
	case "/sessions":
		m.Logger.Info("INPUT", "slash", "command", "sessions")
		if m.anyActive() {
			m.StatusBar.SetError("Cannot switch sessions while agent is running — press ctrl+c to cancel first")
			return nil
		}
		if m.OnListSessions != nil {
			sessions := m.OnListSessions()
			if len(sessions) > 0 {
				m.launcher = NewLauncherState(sessions)
				m.LastInteractionTime = time.Now()
			} else {
				m.StatusBar.SetInfo("No past sessions found")
			}
		} else {
			m.StatusBar.SetError("Session browser not available")
		}
	case "/help":
		m.Logger.Info("INPUT", "slash", "command", "help")
		var b strings.Builder
		b.WriteString("Available commands:\n")
		for _, c := range input.AvailableCmds() {
			b.WriteString(fmt.Sprintf("  %-16s %s\n", "/"+c.Label, c.Desc))
		}
		if ml := m.activeMsgList(); ml != nil {
			ml.AppendMessage(ttypes.ChatMessage{Role: ttypes.RoleAssistant, Content: b.String(), Time: time.Now()})
		}
	case "/yolo":
		if m.OverlayState.IsYolo() {
			m.OverlayState.SetYolo(false)
			if ml := m.activeMsgList(); ml != nil {
				ml.ScrollToBottom()
			}
			m.StatusBar.SetInfo("YOLO mode disabled. Safety checks restored.")
			m.Logger.Info("SAFETY", "yolo_disabled", "source", "slash")
			if m.OnYoloCmd != nil {
				go m.OnYoloCmd(false)
			}
		} else {
			m.ActiveSession.YoloDlg.Reset()
			m.setMode(ModeYolo)
			m.Logger.Info("SAFETY", "yolo_prompt", "source", "slash")
		}
	case "/undo", "/undo-n", "/undo-all", "/changes":
		m.Logger.Info("INPUT", "slash", "command", cmd)
		if m.OnUndoCmd != nil {
			go m.OnUndoCmd(cmd)
		}
	default:
		m.Logger.Warn("INPUT", "slash_unknown", "cmd", cmdName)
		m.StatusBar.SetError(fmt.Sprintf("Unknown command: %s (try /help)", cmdName))
	}

	if len(cmds) > 0 {
		return tea.Batch(cmds...)
	}
	return nil
}
