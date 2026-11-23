package viewmodel

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/agent/provider"
	captypes "crude/types/capability"
	"crude/types/conversation"
	"crude/types/setting"
	"crude/types/statuscenter"
)

type slashBackendResultMsg struct {
	agentID string
	command string
	compact *conversation.CompactResultMsg
}

func (m *App) executeSlashCommand(cmd string) tea.Cmd {
	return m.executeSlashCommandWithRefs(cmd, nil)
}

// executeSlashCommandWithRefs runs a slash command; commands that take a
// prompt (/plan) send it with the composer's attachments.
func (m *App) executeSlashCommandWithRefs(cmd string, referenceIDs []string) tea.Cmd {
	if m.ws.Logger() != nil {
		m.ws.Logger().Info("INPUT", "slash_command", "cmd", cmd)
	}

	cmdName, remainder := parseSlashCommand(cmd)
	if cmdName == "" {
		return nil
	}
	if len(referenceIDs) > 0 && (cmdName == "/goal" || cmdName == "/loop") {
		m.Feedback.NotifyError("Goals and loops cannot retain attachments. Your draft is kept; describe the task in text or remove the attachment.")
		return nil
	}

	var cmds []tea.Cmd
	agentID := m.AgentView.ActiveID()

	switch cmdName {
	case "/retry":
		return m.retryAgentRecovery(agentID)
	case "/fork":
		return m.forkSessionCommand()
	case "/goal":
		return m.goalCommand(agentID, remainder)
	case "/loop":
		return m.loopCommand(agentID, remainder)
	case "/plan":
		return m.planModeCommand(agentID, provider.InputWithRefs(remainder, referenceIDs))
	case "/skills":
		return m.openCapabilityPickerTab(captypes.KindSkill, remainder)
	case "/mcps":
		return m.openCapabilityPickerTab(captypes.KindMCP, remainder)
	case "/settings":
		m.ws.Logger().Info("INPUT", "slash", "command", "settings")
		m.openSessionSettings()
	case "/history":
		cmds = append(cmds, m.openPromptHistory())
	case "/sessions":
		m.ws.Logger().Info("INPUT", "slash", "command", "sessions")
		m.openSessionPicker()
	case "/memory":
		m.ws.Logger().Info("INPUT", "slash", "command", "memory")
		_, cmd := m.toggleMemoryBrowser()
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	case "/compact":
		m.ws.Logger().Info("INPUT", "slash", "command", "compact")
		if agentID == "" {
			m.Feedback.NotifyError("No active session")
			break
		}
		// The owning session decides; a shell never pre-judges busyness.
		prepared, result := m.ws.PrepareCompactForAgent(agentID)
		if prepared.Run == nil {
			m.presentCompactResult(agentID, result)
			break
		}
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
			m.appendActiveMessage(conversation.ChatMessage{Role: conversation.RoleAssistant, Content: b.String(), Time: time.Now()})
		}

	case "/fast":
		return m.toggleSessionFastTier()
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
		m.Feedback.NotifyError(fmt.Sprintf("Unknown command: %s", cmdName))
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
	if msg.compact != nil {
		m.presentCompactResult(msg.agentID, *msg.compact)
	}
}

// presentCompactResult shows the session's own words for a /compact outcome.
// The boundary row in the chat and the activity strip carry progress and the
// final result, so a completed compaction adds no notice. A notice answers
// only what the transcript does not show, such as a request that waits for
// the current step.
func (m *App) presentCompactResult(agentID string, result conversation.CompactResultMsg) {
	switch {
	case result.Cancelled:
		m.ws.Logger().Info("COMPACT", "slash_cancelled", "operation_id", result.OperationID)
	case result.Error != "":
		m.ws.Logger().Error("COMPACT", "slash_failed", "error", result.Error)
		m.Feedback.NotifyError(result.Error)
	case result.Desc != "":
		m.ws.Logger().Info("COMPACT", "slash_answer", "operation_id", result.OperationID, "answer", result.Desc)
		m.Feedback.NotifyAgentTransient(agentID, result.Desc, statuscenter.SeverityInfo)
	default:
		m.ws.Logger().Info("COMPACT", "slash_ok", "operation_id", result.OperationID, "before", result.Before, "after", result.After)
	}
}

// toggleSessionFastTier switches this session's main agent between the
// provider's fast tier and its inherited role/account setting. It is a session
// override like any other: persisted, applied at the next request, and ignored
// by connections whose protocol has no fast tier.
func (m *App) toggleSessionFastTier() tea.Cmd {
	owner, runtime := m.ActiveSession, m.ws
	if owner == nil || runtime == nil || owner.Settings == nil {
		m.Feedback.NotifyError("No session is selected")
		return nil
	}
	settings := owner.Settings.Effective
	before := setting.SessionPreferencesOf(settings)
	before.Overrides = owner.Settings.Overrides
	overrides := make(map[string]setting.Category, len(owner.Settings.Overrides)+1)
	for key, value := range owner.Settings.Overrides {
		overrides[key] = value
	}
	key := "default"
	if settings.SessionMainAgent != "" {
		key = "root/" + settings.SessionMainAgent
	}
	current := overrides[key]
	enable := current.ServiceTier != "fast"
	current.ServiceTier = ""
	if enable {
		current.ServiceTier = "fast"
	}
	overrides[key] = current
	settings.SessionOverrides = overrides
	settings.SessionEdits = setting.Edits(before, setting.SessionPreferencesOf(settings))
	operation, err := runtime.PrepareSessionSettingsSave(owner.SessionID, settings)
	if err != nil {
		m.Feedback.NotifyError(err.Error())
		return nil
	}
	generation := m.workspaceSwitchGen.Load()
	return func() tea.Msg {
		result := operation.Run()
		label := "Fast off · inherited speed"
		switch result.Snapshot.Effective.FastMode {
		case setting.FastModeOn:
			label = "⚡ Fast on · next request (premium)"
			if !enable {
				label = "⚡ Fast still on · inherited speed"
			}
		case setting.FastModeUnsupported:
			label = "⚡ Fast unsupported"
		case setting.FastModeUnavailable:
			label = "⚡ Fast unavailable"
		}
		return selectedSessionSettingsResult{workspace: runtime, generation: generation, owner: owner, result: result, label: label}
	}
}
