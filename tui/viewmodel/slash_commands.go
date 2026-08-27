package viewmodel

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	ttypes "crude/tui/types"
)

type slashBackendResultMsg struct {
	agentID string
	command string
	undo    *ttypes.UndoResultMsg
	compact *ttypes.CompactResultMsg
}

func (m *App) executeSlashCommand(cmd string) tea.Cmd {
	if m.Logger != nil {
		m.Logger.Info("INPUT", "slash_command", "cmd", cmd)
	}

	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		return nil
	}

	var cmds []tea.Cmd
	cmdName := strings.ToLower(parts[0])
	agentID := m.AgentMgr.ActiveID()

	switch cmdName {
	case "/settings":
		m.Logger.Info("INPUT", "slash", "command", "settings")
		m.openSessionSettings()
	case "/model":
		m.Logger.Info("INPUT", "slash", "command", "model")
		if cmd := m.openSessionModelPicker(); cmd != nil {
			cmds = append(cmds, cmd)
		}
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
	case "/compact":
		m.Logger.Info("INPUT", "slash", "command", "compact")
		if agentID == "" {
			m.StatusBar.SetError("No active session")
			break
		}
		if m.AgentTurnActive(agentID) {
			m.StatusBar.SetError("Stop the active turn before compacting context")
			break
		}
		return func() tea.Msg {
			result := m.HandleCompactForAgent(agentID)
			return slashBackendResultMsg{agentID: agentID, command: cmdName, compact: &result}
		}
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
		if agentID == "" {
			m.StatusBar.SetError("No active session")
			break
		}
		if m.YoloForAgent(agentID) || m.OverlayState.IsYolo() {
			m.OverlayState.SetYolo(false)
			if m.chatScreen != nil {
				scroll := m.chatScreen.ScrollModel()
				scroll.SetAutoScroll(true)
				scroll.StickToBottom()
			}
			m.StatusBar.SetInfo("YOLO mode disabled. Safety checks restored.")
			m.Logger.Info("SAFETY", "yolo_disabled", "source", "slash")
			m.SetYoloForAgent(agentID, false)
		} else {
			m.openSessionYoloPanel()
			m.Logger.Info("SAFETY", "yolo_prompt", "source", "slash")
		}
	case "/undo", "/undo-n", "/undo-all":
		m.Logger.Info("INPUT", "slash", "command", cmd)
		if agentID == "" {
			m.StatusBar.SetError("No active session")
			break
		}
		if m.AgentTurnActive(agentID) {
			m.StatusBar.SetError("Stop the active turn before undoing file operations")
			break
		}
		return func() tea.Msg {
			result := m.HandleUndoForAgent(agentID, cmd)
			return slashBackendResultMsg{agentID: agentID, command: cmdName, undo: &result}
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

func (m *App) handleSlashBackendResult(msg slashBackendResultMsg) {
	if msg.undo != nil {
		if msg.undo.Error != "" {
			m.Logger.Error("JOURNAL", "slash_failed", "command", msg.command, "error", msg.undo.Error)
			m.StatusBar.SetError(msg.undo.Error)
			return
		}
		if msg.undo.Desc != "" {
			m.StatusBar.SetInfo(msg.undo.Desc)
			m.appendAgentMessage(msg.agentID, ttypes.ChatMessage{Role: ttypes.RoleAssistant, Content: msg.undo.Desc, Time: time.Now()})
		}
		return
	}
	if msg.compact != nil {
		if msg.compact.Error != "" {
			m.Logger.Error("COMPACT", "slash_failed", "error", msg.compact.Error)
			m.StatusBar.SetError(msg.compact.Error)
			return
		}
		m.Logger.Info("COMPACT", "slash_ok", "before", msg.compact.Before, "after", msg.compact.After)
		m.StatusBar.SetInfo(msg.compact.Desc)
		if msg.compact.Desc != "" {
			m.appendAgentMessage(msg.agentID, ttypes.ChatMessage{Role: ttypes.RoleAssistant, Content: msg.compact.Desc, Time: time.Now()})
		}
	}
}
