package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"

	"crude/fileio"
	"crude/tui/input"
	"crude/tui/settings"
	ttypes "crude/tui/types"
)

func (m *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.chat.SetSize(msg.Width, msg.Height)
		m.input.SetWidth(msg.Width)
		m.statusbar.SetWidth(msg.Width)
		return m, nil

	case ttypes.TickMsg:
		m.spinFrame++
		m.statusbar.SetSpinFrame(m.spinFrame)
		m.input.SetSpinFrame(m.spinFrame)
		m.statusbar.ClearExpired()
		m.chat.InvalidateCache()
		return m, tickCmd()

	case tea.KeyMsg:
		if m.chat.HasActiveSelection() {
			m.chat.ClearSelection()
			return m, nil
		}
		if msg.Type == tea.KeyCtrlC {
			return m, m.handleCancel(msg)
		}
		if msg.Type == tea.KeyEsc && m.mode != modeSettings {
			return m, m.handleCancel(msg)
		}

	case tea.MouseMsg:
		if m.mode == modeChat {
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
					m.setMode(modeSettings)
				}
			} else {
				updatedChat, chatCmd := m.chat.Update(msg)
				m.chat = updatedChat
				if chatCmd != nil {
					cmds = append(cmds, chatCmd)
				}
			}
		} else {
			updatedChat, chatCmd := m.chat.Update(msg)
			m.chat = updatedChat
			if chatCmd != nil {
				cmds = append(cmds, chatCmd)
			}
		}

	case ttypes.SelectionTickMsg:
		updatedChat, chatCmd := m.chat.Update(msg)
		m.chat = updatedChat
		if chatCmd != nil {
			cmds = append(cmds, chatCmd)
		}
	}

	switch m.mode {
	case modeSettings:
		updated, cmd, evt := m.settings.Update(msg)
		m.settings = updated
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
		switch e := evt.(type) {
		case settings.SaveEvent:
			s := e.Settings
			m.modelName = s.Model
			m.lastSettings = s
			if m.OnSettingsSave != nil {
				m.OnSettingsSave(s)
			}
		m.setMode(modeChat)
		m.chat.InvalidateCache()
	case settings.CancelEvent:
			m.settings.RestoreSnapshot()
		m.setMode(modeChat)
		m.chat.InvalidateCache()
		}

	case modeConfirm:
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
			m.chat.AppendMessage(ttypes.ChatMessage{
				Role:    ttypes.RoleAssistant,
				Content: resultMsg,
				Time:    time.Now(),
			})
			m.setMode(modeChat)
		}

	case modeYolo:
		m.statusbar.SetConfirming(true)
		updated, enabled, done := m.yoloDlg.Update(msg)
		m.yoloDlg = updated
		if done {
			m.statusbar.SetConfirming(false)
			m.yolo = enabled
			if enabled {
				m.chat.AppendMessage(ttypes.ChatMessage{
					Role:    ttypes.RoleAssistant,
					Content: "⚠ YOLO mode ACTIVE — all safety checks disabled. Use /yolo again to restore.",
					Time:    time.Now(),
				})
			} else {
				m.chat.AppendMessage(ttypes.ChatMessage{
					Role:    ttypes.RoleAssistant,
					Content: "YOLO mode cancelled. Safety checks remain active.",
					Time:    time.Now(),
				})
			}
			if m.OnYoloCmd != nil {
				m.OnYoloCmd(enabled)
			}
			m.setMode(modeChat)
		}

	case modeChat:
		if m.thinking && !m.blocking {
			if keyMsg, ok := msg.(tea.KeyMsg); ok {
				switch keyMsg.Type {
				case tea.KeyHome, tea.KeyEnd, tea.KeyCtrlHome, tea.KeyCtrlEnd:
					updatedChat, chatCmd := m.chat.Update(keyMsg)
					m.chat = updatedChat
					if chatCmd != nil {
						cmds = append(cmds, chatCmd)
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
				m.setMode(modeSettings)
			}
		}

		if _, ok := msg.(tea.KeyMsg); ok {
			updatedChat, chatCmd := m.chat.Update(msg)
			m.chat = updatedChat
			if chatCmd != nil {
				cmds = append(cmds, chatCmd)
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
		m.chat.SetModelName(string(msg))
	case ttypes.OpenSettingsMsg:
		m.settings.SaveSnapshot()
		m.setMode(modeSettings)
	case ttypes.ModelsListMsg:
		m.settings.SetModels([]ttypes.ModelEntry(msg), false)
	case ttypes.OracleModelsListMsg:
		m.settings.SetModels([]ttypes.ModelEntry(msg), true)
	case ttypes.ModelsErrorMsg:
		m.settings.SetModelsError(string(msg), false)
	case ttypes.OracleModelsErrorMsg:
		m.settings.SetModelsError(string(msg), true)
	case ttypes.ClipboardCopiedMsg:
		m.statusbar.SetInfo("copied to clipboard")
	case ttypes.ClipboardErrorMsg:
		m.statusbar.SetError(string(msg))
	case ttypes.UndoResultMsg:
		if msg.Error != "" {
			m.statusbar.SetError(msg.Error)
		}
		if msg.Desc != "" {
			m.chat.AppendMessage(ttypes.ChatMessage{
				Role:    ttypes.RoleAssistant,
				Content: msg.Desc,
				Time:    time.Now(),
			})
		}
	}

	return m, tea.Batch(cmds...)
}

func (m *App) submitInput(text string) tea.Cmd {
	m.chat.AppendMessage(ttypes.ChatMessage{
		Role:    ttypes.RoleUser,
		Content: text,
		Time:    time.Now(),
	})
	m.history = append(m.history, text)
	if len(m.history) > 500 {
		m.history = m.history[len(m.history)-500:]
	}
	if OnHistorySave != nil {
		OnHistorySave(m.history)
	}
	m.thinking = true
	m.streaming = false
	m.statusbar.SetThinking(true, false, false)
	m.chat.ScrollToBottom()
	ch := m.inputCh
	return func() tea.Msg {
		select {
		case ch <- text:
			return nil
		default:
			return ttypes.ErrorMsg("input channel blocked - please try again")
		}
	}
}

func (m *App) executeSlashCommand(cmd string) tea.Cmd {
	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		return nil
	}

	cmdName := parts[0]

	switch cmdName {
	case "/settings":
		m.settings.SaveSnapshot()
		m.setMode(modeSettings)
	case "/provider":
		m.settings.SaveSnapshot()
		m.setMode(modeSettings)
		m.settings.FocusField(0)
	case "/model":
		m.settings.SaveSnapshot()
		m.setMode(modeSettings)
		m.settings.FocusField(3)
	case "/oracle-model":
		m.settings.SaveSnapshot()
		m.setMode(modeSettings)
		m.settings.FocusField(4)
	case "/api-key":
		m.settings.SaveSnapshot()
		m.setMode(modeSettings)
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
			go m.OnSettingsSave(m.lastSettings)
		}
		m.statusbar.SetInfo(fmt.Sprintf("Thinking effort: %s", efforts[idx]))
	case "/help":
		var b strings.Builder
		b.WriteString("Available commands:\n")
		for _, c := range input.AvailableCmds() {
			b.WriteString(fmt.Sprintf("  %-16s %s\n", "/"+c.Label, c.Desc))
		}
		m.chat.AppendMessage(ttypes.ChatMessage{Role: ttypes.RoleAssistant, Content: b.String(), Time: time.Now()})
	case "/yolo":
		if m.yolo {
			m.yolo = false
			m.chat.ScrollToBottom()
			m.statusbar.SetInfo("YOLO mode disabled. Safety checks restored.")
			if m.OnYoloCmd != nil {
				go m.OnYoloCmd(false)
			}
		} else {
			m.yoloDlg.Reset()
			m.setMode(modeYolo)
		}
	case "/undo", "/undo-n", "/undo-all", "/changes":
		if m.OnUndoCmd != nil {
			go m.OnUndoCmd(cmd)
		}
	default:
		m.statusbar.SetError(fmt.Sprintf("Unknown command: %s (try /help)", cmdName))
	}

	return nil
}

func (m *App) handleCancel(msg tea.KeyMsg) tea.Cmd {
	now := time.Now()

	if m.mode == modeSettings {
		m.settings.RestoreSnapshot()
		m.setMode(modeChat)
		return nil
	}

	if m.mode == modeYolo {
		m.yoloDlg.Dismiss()
		m.setMode(modeChat)
		return nil
	}

	if m.mode == modeConfirm {
		m.confirm.Dismiss()
		m.blocking = false
		m.setMode(modeChat)
		if m.OnConfirmResult != nil {
			m.OnConfirmResult(ttypes.ConfirmResult{Choice: ttypes.ConfirmDeny})
		}
		return nil
	}

	if m.thinking {
		select {
		case m.cancelCh <- struct{}{}:
		default:
		}
		m.statusbar.SetInfo("cancelling...")
		return nil
	}

	inputText := strings.TrimSpace(m.input.Value())
	if inputText != "" {
		m.input.ClearInput()
		m.input.DismissHint()
		return nil
	}

	if now.Sub(m.lastCancelTime) < 2*time.Second {
		m.quit = true
		return tea.Quit
	}
	m.lastCancelTime = now
	m.statusbar.SetError("press ctrl+c or esc again to exit")
	return nil
}

func (m *App) handleAgentEvent(evt ttypes.AgentEventMsg) tea.Cmd {
	switch evt.Type {
	case "phase":
		if phase, ok := evt.Data.(string); ok {
			m.statusbar.SetPhase(phase)
			var statusLine string
			switch phase {
			case "grounding":
				statusLine = "Gathering context"
			case "planning":
				statusLine = "Planning"
			case "executing":
				statusLine = "Executing"
			default:
				statusLine = phase
			}
			m.statusbar.SetStatusLine(statusLine)
		}

	case "thinking":
		if s, ok := evt.Data.(string); ok && s != "" {
			m.chat.AppendThinking(s)
			m.thinking = true
			m.statusbar.SetThinking(true, false, false)
		}

	case "stream_token":
		if s, ok := evt.Data.(string); ok && s != "" {
			m.chat.AppendToken(s)
			m.streaming = true
			m.statusbar.SetThinking(true, true, false)
		}

	case "tool_call_streaming":
		if data, ok := evt.Data.(map[string]any); ok {
			toolID, _ := data["tool_id"].(string)
			name, _ := data["name"].(string)
			if name == "" || name == "reply" {
				break
			}
			path, _ := data["path"].(string)
			command, _ := data["command"].(string)
			urlStr, _ := data["url"].(string)
			query, _ := data["query"].(string)
			source, _ := data["source"].(string)
			dest, _ := data["destination"].(string)
			pattern, _ := data["pattern"].(string)

			var target string
			target = path
			if target == "" && command != "" {
				target = command
			}
			if target == "" && source != "" && dest != "" {
				target = source + " → " + dest
			} else if target == "" && source != "" {
				target = source
			}
			if target == "" && urlStr != "" {
				target = urlStr
			}
			if target == "" && query != "" {
				target = query
			}
			if target == "" && pattern != "" {
				target = pattern
			}

			contentLines, _ := data["content_lines"].(float64)
			oldLines, _ := data["old_lines"].(float64)
			newLines, _ := data["new_lines"].(float64)

			if name == "read_file" && target != "" {
				offset, _ := data["offset"].(float64)
				limit, _ := data["limit"].(float64)
				if offset > 0 && limit > 0 {
					target = fmt.Sprintf("%s line %d-%d", target, int(offset), int(offset+limit-1))
				} else if offset > 0 {
					target = fmt.Sprintf("%s line %d+", target, int(offset))
				} else if limit > 0 {
					target = fmt.Sprintf("%s (first %d lines)", target, int(limit))
				}
			}

			data["target"] = target
			data["content_lines_int"] = int(contentLines)
			data["old_lines_int"] = int(oldLines)
			data["new_lines_int"] = int(newLines)

			m.chat.UpdateToolStreaming(toolID, name, data)
		}

	case "tool_call":
		m.statusbar.ClearRetry()
		if data, ok := evt.Data.(map[string]any); ok {
			toolID, _ := data["tool_id"].(string)
			name, _ := data["name"].(string)
			if name == "" || name == "reply" {
				break
			}
			args, _ := data["args"].(string)
			argsCompact := summarizeToolArgs(name, args)
			m.statusbar.SetStatusLine(toolLabel(name, argsCompact))
			m.chat.UpdateToolCall(toolID, name, argsCompact)
		}

	case "tool_progress":
		if data, ok := evt.Data.(map[string]any); ok {
			toolID, _ := data["tool_id"].(string)
			name, _ := data["name"].(string)
			if name == "" || name == "reply" {
				break
			}
			phase, _ := data["phase"].(string)
			message, _ := data["message"].(string)
			path, _ := data["path"].(string)
			linesAdded, _ := data["lines_added"].(float64)
			linesRemoved, _ := data["lines_removed"].(float64)

			m.chat.UpdateToolProgress(toolID, name, phase, message, path, int(linesAdded), int(linesRemoved))
			if phase == "done" {
				m.statusbar.SetStatusLine(name)
			} else if phase != "" && message != "" {
				m.statusbar.SetStatusLine(message)
			}
		}

	case "tool_result":
		if data, ok := evt.Data.(map[string]any); ok {
			toolID, _ := data["tool_id"].(string)
			name, _ := data["name"].(string)
			if name == "" || name == "reply" {
				break
			}
			result, _ := data["result"].(string)
			errFlag, _ := data["error"].(string)
			isError := errFlag == "true"
			m.statusbar.SetStatusLine(name)
			m.chat.UpdateToolResult(toolID, name, result, isError)
		}

	case "blocking":
		var data map[string]string
		if d, ok := evt.Data.(map[string]any); ok {
			data = make(map[string]string, len(d))
			for k, v := range d {
				data[k] = fmt.Sprintf("%v", v)
			}
		} else if d, ok := evt.Data.(map[string]string); ok {
			data = d
		} else {
			question := fmt.Sprintf("%v", evt.Data)
			data = map[string]string{"question": question}
		}
		question := data["question"]
		operation := data["operation"]
		tool := data["tool"]
		target := data["target"]
		risk := data["risk"]
		reason := data["reason"]
		suggestion := data["suggestion"]
		m.thinking = false
		m.blocking = true
		m.statusbar.SetStatusLine("")
		m.statusbar.SetThinking(false, false, true)
		m.chat.MarkAllThinkingDone()
		if risk != "" {
			m.confirm.ResetWithDetails(question, operation, tool, target, risk, reason, suggestion)
		} else {
			m.confirm.Reset(question, operation, tool, target)
		}
		m.chat.AppendMessage(ttypes.ChatMessage{
			Role:    ttypes.RoleAssistant,
			Content: "⚠  " + question,
			Time:    time.Now(),
		})
		m.setMode(modeConfirm)

	case "blocking_complete":
		m.blocking = false
		m.confirm.Dismiss()
		m.statusbar.SetConfirming(false)
		m.statusbar.SetThinking(false, false, false)
		m.setMode(modeChat)

	case "done":
		wasStreaming := m.streaming
		m.chat.MarkAllThinkingDone()
		m.chat.ClearStreamingTools()
		m.thinking = false
		m.streaming = false
		m.statusbar.SetStatusLine("")
		m.statusbar.SetThinking(false, false, false)

		var content, reason string
		switch d := evt.Data.(type) {
		case string:
			content = d
		case map[string]any:
			content, _ = d["content"].(string)
			reason, _ = d["reason"].(string)
		}

		if reason == "completed" {
			m.statusbar.SetInfo("✓ done")
		} else if reason != "" {
			m.statusbar.SetError("⚠ " + reason)
		}

		for _, msg := range m.chat.Messages() {
			msg.CachedLines = nil
		}

		if content != "" {
			msgs := m.chat.Messages()
			merged := false
			if wasStreaming && len(msgs) > 0 {
				last := msgs[len(msgs)-1]
				if last.Role == ttypes.RoleAssistant && last.Content == "" {
					last.Content = content
					last.CursorLine = -1
					merged = true
				}
			}
			if !merged {
				m.chat.AppendMessage(ttypes.ChatMessage{
					Role:    ttypes.RoleAssistant,
					Content: content,
					Time:    time.Now(),
				})
			}
		}
		m.chat.ScrollToBottom()

	case "error":
		m.chat.MarkAllThinkingDone()
		m.chat.ClearStreamingTools()
		errMsg := fmt.Sprintf("%v", evt.Data)
		m.chat.AppendMessage(ttypes.ChatMessage{Role: ttypes.RoleError, Content: errMsg, Time: time.Now()})
		m.statusbar.SetError(errMsg)
		m.thinking = false
		m.streaming = false
		m.blocking = false
		m.statusbar.SetThinking(false, false, false)
		m.statusbar.ClearRetry()
		m.statusbar.SetStatusLine("")
		m.chat.ScrollToBottom()

	case "usage":
		switch d := evt.Data.(type) {
		case map[string]any:
			prompt, _ := d["prompt"].(float64)
			completion, _ := d["completion"].(float64)
			cached, _ := d["cached"].(float64)
			cost, _ := d["cost"].(float64)
			ctxWindow, _ := d["context_window"].(float64)
			ctxUsed, _ := d["context_used"].(float64)
			m.tokenPrompt = int(prompt)
			m.tokenCompletion += int(completion)
			m.tokenCached = int(cached)
			m.sessionCost += cost
			if int(ctxWindow) > 0 {
				m.contextWindow = int(ctxWindow)
				m.contextUsed = int(ctxUsed)
			}
			m.statusbar.UpdateUsage(m.tokenPrompt, m.tokenCompletion, m.tokenCached, m.sessionCost, m.contextWindow, m.contextUsed)
		case map[string]int:
			m.tokenCached = d["prompt_cached"]
			m.tokenCompletion += d["completion"]
			m.tokenPrompt = d["prompt"]
			if cw := d["context_window"]; cw > 0 {
				m.contextWindow = cw
				m.contextUsed = d["prompt"]
			}
			m.statusbar.UpdateUsage(m.tokenPrompt, m.tokenCompletion, m.tokenCached, m.sessionCost, m.contextWindow, m.contextUsed)
		}

	case "retry":
		switch d := evt.Data.(type) {
		case map[string]any:
			attempt := 0
			switch a := d["attempt"].(type) {
			case float64:
				attempt = int(a)
			case int:
				attempt = a
			}
			backoff, _ := d["backoff"].(float64)
			m.statusbar.SetRetry(fmt.Sprintf("⚠ retrying in %.0fs... (attempt %d)", backoff, attempt))
		case string:
			m.statusbar.SetRetry(d)
		}

	case "async_command_status":
		if cmds, ok := evt.Data.([]fileio.AsyncCmdInfo); ok {
			m.statusbar.SetAsyncCommands(cmds)
		}
	}

	return nil
}

func tickCmd() tea.Cmd {
	return tea.Tick(500*time.Millisecond, func(t time.Time) tea.Msg {
		return ttypes.TickMsg(t)
	})
}

func summarizeToolArgs(tool, argsStr string) string {
	path := extractArg(argsStr, "path")
	if path != "" {
		return path
	}
	if pattern := extractArg(argsStr, "pattern"); pattern != "" {
		return pattern
	}
	if command := extractArg(argsStr, "command"); command != "" {
		return truncateLine(command, 50)
	}
	if content := extractArg(argsStr, "content"); content != "" {
		return truncateLine(content, 50) + " (content)"
	}
	if query := extractArg(argsStr, "query"); query != "" {
		return truncateLine(query, 50)
	}
	if url := extractArg(argsStr, "url"); url != "" {
		return truncateLine(url, 50)
	}
	if source := extractArg(argsStr, "source"); source != "" {
		dest := extractArg(argsStr, "destination")
		if dest != "" {
			return fmt.Sprintf("%s → %s", source, dest)
		}
		return source
	}
	if tool == "plan" || tool == "list_goal_status" {
		return pascalCaseTool(tool)
	}
	if tool == "mark_step_complete" || tool == "mark_step_failed" {
		if stepID := extractArg(argsStr, "step_id"); stepID != "" {
			return stepID
		}
		return pascalCaseTool(tool)
	}
	if tool == "report_blocker" {
		if msg := extractArg(argsStr, "message"); msg != "" {
			return truncateLine(msg, 50)
		}
		return pascalCaseTool(tool)
	}
	if tool == "resolve_blocker" {
		return pascalCaseTool(tool)
	}
	if code := extractArg(argsStr, "code"); code != "" {
		return truncateLine(code, 50)
	}
	return truncateLine(argsStr, 60)
}

func extractArg(str, key string) string {
	prefixes := []string{key + ":", key + ": "}
	for _, prefix := range prefixes {
		idx := strings.Index(str, prefix)
		if idx == -1 {
			continue
		}
		start := idx + len(prefix)
		if start >= len(str) {
			return ""
		}
		end := strings.IndexAny(str[start:], " ,}]")
		if end == -1 {
			return strings.TrimSpace(str[start:])
		}
		return strings.TrimSpace(str[start : start+end])
	}
	return ""
}

func toolLabel(tool, argsSummary string) string {
	if argsSummary != "" {
		return fmt.Sprintf("%s %s", tool, argsSummary)
	}
	return tool
}

func pascalCaseTool(name string) string {
	parts := strings.Split(name, "_")
	for i, p := range parts {
		if len(p) > 0 {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, " ")
}

func truncateLine(s string, max int) string {
	if runewidth.StringWidth(s) <= max {
		return s
	}
	return runewidth.Truncate(s, max, "…")
}
