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
		m.openSessionSettings()
	case "/provider":
		m.Logger.Info("INPUT", "slash", "command", "provider")
		m.openSessionSettings()
	case "/api-key":
		m.Logger.Info("INPUT", "slash", "command", "api-key")
		m.openWorkspaceSettings()
	case "/sessions":
		m.Logger.Info("INPUT", "slash", "command", "sessions")
		if m.anyActive() {
			m.StatusBar.SetError("Cannot switch sessions while agent is running — press ctrl+c to cancel first")
			return nil
		}
		if m.AgentMgr.Count() > 0 || m.EditorState.ActiveView != "" {
			m.StatusBar.SetInfo("Close open tabs first, or use Quit from the command palette to exit")
			return nil
		}
		m.openLauncher()
	case "/help":
		m.Logger.Info("INPUT", "slash", "command", "help")
		var b strings.Builder
		b.WriteString("Available commands:\n")
		for _, c := range input.AvailableCmds() {
			b.WriteString(fmt.Sprintf("  %-16s %s\n", "/"+c.Label, c.Desc))
		}
		if m.AgentMgr.ActiveID() != "" {
			m.appendActiveMessage(ttypes.ChatMessage{Role: ttypes.RoleAssistant, Content: b.String(), Time: time.Now()})
		}
	case "/debug-lsp":
		var b strings.Builder
		b.WriteString("LSP Debug:\n")
		if m.LSPMgr != nil {
			conn, status, errMsg := m.LSPMgr.Status()
			b.WriteString(fmt.Sprintf("  connected=%v  status=%d  error=%q\n", conn, status, errMsg))
			ins := m.LSPMgr.ProjectInsights()
			b.WriteString(fmt.Sprintf("  server=%s  ready=%v  files=%d  err=%d  warn=%d  hints=%d\n",
				ins.ServerName, ins.ServerReady, ins.FilesAffected, ins.Errors, ins.Warnings, ins.Hints))
		}
		if m.eventBus != nil {
			b.WriteString("  Recent events:\n")
			for _, e := range m.eventBus.RecentEvents(20) {
				b.WriteString(fmt.Sprintf("    %s\n", e))
			}
		}
		if m.AgentMgr.ActiveID() != "" {
			m.appendActiveMessage(ttypes.ChatMessage{Role: ttypes.RoleAssistant, Content: b.String(), Time: time.Now()})
		}

	case "/yolo":
		if m.OverlayState.IsYolo() {
			m.OverlayState.SetYolo(false)
			if ml := m.activeMsgList(); ml != nil {
				ml.ScrollToBottom()
			}
			m.StatusBar.SetInfo("YOLO mode disabled. Safety checks restored.")
			m.Logger.Info("SAFETY", "yolo_disabled", "source", "slash")
			m.SetYolo(false)
		} else {
			m.ActiveSession.YoloDlg.Reset()
			m.setMode(ModeYolo)
			m.Logger.Info("SAFETY", "yolo_prompt", "source", "slash")
		}
	case "/undo", "/undo-n", "/undo-all", "/changes":
		m.Logger.Info("INPUT", "slash", "command", cmd)
		m.HandleUndo(cmd)
	default:
		m.Logger.Warn("INPUT", "slash_unknown", "cmd", cmdName)
		m.StatusBar.SetError(fmt.Sprintf("Unknown command: %s (try /help)", cmdName))
	}

	if len(cmds) > 0 {
		return tea.Batch(cmds...)
	}
	return nil
}
