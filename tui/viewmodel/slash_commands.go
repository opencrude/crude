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
	if m.ws.Logger() != nil {
		m.ws.Logger().Info("INPUT", "slash_command", "cmd", cmd)
	}

	cmdName, remainder := parseSlashCommand(cmd)
	if cmdName == "" {
		return nil
	}

	var cmds []tea.Cmd
	agentID := m.AgentView.ActiveID()

	switch cmdName {
	case "/retry":
		return m.retryAgentRecovery(agentID)
	case "/skills":
		return m.openCapabilityManager(activitySkills, remainder, "")
	case "/mcp":
		return m.openCapabilityManager(activityMCP, remainder, "")
	case "/skill":
		return m.invokeSkill(remainder, cmd)
	case "/settings":
		m.ws.Logger().Info("INPUT", "slash", "command", "settings")
		m.openSessionSettings()
	case "/history":
		cmds = append(cmds, m.openPromptHistory())
	case "/model":
		m.ws.Logger().Info("INPUT", "slash", "command", "model")
		if cmd := m.openSessionModelPicker(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case "/sessions":
		m.ws.Logger().Info("INPUT", "slash", "command", "sessions")
		m.openSessionPicker()
	case "/memory":
		m.ws.Logger().Info("INPUT", "slash", "command", "memory")
		_, cmd := m.toggleMemoryBrowser()
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	case "/help":
		m.ws.Logger().Info("INPUT", "slash", "command", "help")
		m.openHelp()
	case "/compact":
		m.ws.Logger().Info("INPUT", "slash", "command", "compact")
		if agentID == "" {
			m.Feedback.NotifyError("No active session")
			break
		}
		if m.ws.AgentTurnActive(agentID) {
			m.Feedback.NotifyError("Stop the active turn before compacting context")
			break
		}
		prepared, result := m.ws.PrepareCompactForAgent(agentID)
		if prepared.Run == nil {
			if result.Error != "" {
				m.Feedback.NotifyError(result.Error)
			} else if result.Desc != "" {
				m.Feedback.NotifyInfo(result.Desc)
			}
			break
		}
		_ = m.handleAgentEvent(ttypes.AgentEventMsg{Type: "context_compaction_started", AgentID: agentID, Data: map[string]any{
			"request_id": prepared.OperationID, "trigger": "manual", "role": "root",
			"provider_id": prepared.ProviderID, "model_id": prepared.ModelID,
			"stage": 0, "completed_stages": 0, "stage_count": 4, "retry_limit": 3,
		}})
		return func() tea.Msg {
			result := prepared.Run()
			return slashBackendResultMsg{agentID: agentID, command: cmdName, compact: &result}
		}
	case "/debug-lsp":
		var b strings.Builder
		b.WriteString("LSP Debug:\n")
		if m.ws.LSPMgr() != nil {
			conn, status, errMsg := m.ws.LSPMgr().Status()
			fmt.Fprintf(&b, "  connected=%v  status=%d  error=%q\n", conn, status, errMsg)
			ins := m.ws.LSPMgr().ProjectInsights()
			fmt.Fprintf(&b, "  server=%s  ready=%v  files=%d  err=%d  warn=%d  hints=%d\n",
				ins.ServerName, ins.ServerReady, ins.FilesAffected, ins.Errors, ins.Warnings, ins.Hints)
		}
		if m.eventBus != nil {
			b.WriteString("  Recent events:\n")
			for _, e := range m.eventBus.RecentEvents(20) {
				fmt.Fprintf(&b, "    %s\n", e)
			}
		}
		if m.AgentView.ActiveID() != "" {
			m.appendActiveMessage(ttypes.ChatMessage{Role: ttypes.RoleAssistant, Content: b.String(), Time: time.Now()})
		}

	case "/yolo":
		if agentID == "" {
			m.Feedback.NotifyError("No active session")
			break
		}
		if m.ws.YoloForAgent(agentID) {
			m.setAgentYolo(agentID, false)
		} else {
			m.openSessionYoloPanel()
			m.ws.Logger().Info("SAFETY", "yolo_prompt", "source", "slash")
		}
	case "/undo":
		m.ws.Logger().Info("INPUT", "slash", "command", cmd)
		if agentID == "" {
			m.Feedback.NotifyError("No active session")
			break
		}
		if m.ws.AgentTurnActive(agentID) {
			m.Feedback.NotifyError("Stop the active turn before undoing file operations")
			break
		}
		return m.openLatestHistoryCmd(agentID)
	case "/undo-n", "/undo-all":
		m.Feedback.NotifyError("Legacy destructive undo commands were removed. Use /undo to review message changes safely.")
	default:
		m.ws.Logger().Warn("INPUT", "slash_unknown", "cmd", cmdName)
		m.Feedback.NotifyError(fmt.Sprintf("Unknown command: %s (try /help)", cmdName))
	}

	if len(cmds) > 0 {
		return tea.Batch(cmds...)
	}
	return nil
}

func parseSlashCommand(command string) (string, string) {
	command = strings.TrimLeft(command, " \t\r\n")
	if command == "" {
		return "", ""
	}
	index := strings.IndexAny(command, " \t\r\n")
	if index < 0 {
		return strings.ToLower(command), ""
	}
	return strings.ToLower(command[:index]), strings.TrimLeft(command[index:], " \t\r\n")
}

func (m *App) handleSlashBackendResult(msg slashBackendResultMsg) {
	if msg.undo != nil {
		if msg.undo.Error != "" {
			m.ws.Logger().Error("JOURNAL", "slash_failed", "command", msg.command, "error", msg.undo.Error)
			m.Feedback.NotifyError(msg.undo.Error)
			return
		}
		if msg.undo.Desc != "" {
			m.Feedback.NotifyInfo(msg.undo.Desc)
			m.appendAgentMessage(msg.agentID, ttypes.ChatMessage{Role: ttypes.RoleAssistant, Content: msg.undo.Desc, Time: time.Now()})
		}
		return
	}
	if msg.compact != nil {
		if msg.compact.Cancelled {
			m.ws.Logger().Info("COMPACT", "slash_cancelled", "operation_id", msg.compact.OperationID)
			return
		}
		if msg.compact.Error != "" {
			m.ws.Logger().Error("COMPACT", "slash_failed", "error", msg.compact.Error)
			m.Feedback.NotifyError(msg.compact.Error)
			return
		}
		m.ws.Logger().Info("COMPACT", "slash_ok", "before", msg.compact.Before, "after", msg.compact.After)
	}
}
