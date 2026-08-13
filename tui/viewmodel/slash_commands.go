package viewmodel

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

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
		m.openApplicationSettings()
	case "/sessions":
		m.Logger.Info("INPUT", "slash", "command", "sessions")
		m.openSessionPicker()
	case "/memory":
		m.Logger.Info("INPUT", "slash", "command", "memory")
		_, cmd := m.toggleMemoryBrowser()
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	case "/help":
		m.Logger.Info("INPUT", "slash", "command", "help")
		m.openHelp()
	case "/debug-lsp":
		var b strings.Builder
		b.WriteString("LSP Debug:\n")
		if m.LSPMgr != nil {
			conn, status, errMsg := m.LSPMgr.Status()
			fmt.Fprintf(&b, "  connected=%v  status=%d  error=%q\n", conn, status, errMsg)
			ins := m.LSPMgr.ProjectInsights()
			fmt.Fprintf(&b, "  server=%s  ready=%v  files=%d  err=%d  warn=%d  hints=%d\n",
				ins.ServerName, ins.ServerReady, ins.FilesAffected, ins.Errors, ins.Warnings, ins.Hints)
		}
		if m.eventBus != nil {
			b.WriteString("  Recent events:\n")
			for _, e := range m.eventBus.RecentEvents(20) {
				fmt.Fprintf(&b, "    %s\n", e)
			}
		}
		if m.AgentMgr.ActiveID() != "" {
			m.appendActiveMessage(ttypes.ChatMessage{Role: ttypes.RoleAssistant, Content: b.String(), Time: time.Now()})
		}

	case "/yolo":
		if m.OverlayState.IsYolo() {
			m.OverlayState.SetYolo(false)
			if m.chatScreen != nil {
				scroll := m.chatScreen.ScrollModel()
				scroll.SetAutoScroll(true)
				scroll.StickToBottom()
			}
			m.StatusBar.SetInfo("YOLO mode disabled. Safety checks restored.")
			m.Logger.Info("SAFETY", "yolo_disabled", "source", "slash")
			m.SetYolo(false)
		} else {
			m.openSessionYoloPanel()
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
