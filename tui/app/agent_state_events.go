package app

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"crude/tui/rendering"
	"crude/tui/tools"
	ttypes "crude/tui/types"
	"crude/tui/widgets"
)

func (a *AgentState) handleToolCallStreaming(evt ttypes.AgentEventMsg, target *widgets.MessageList, onStatusLine func(string)) tea.Cmd {
	if data, ok := evt.Data.(map[string]any); ok {
		toolID, _ := data["tool_id"].(string)
		name, _ := data["name"].(string)
		if name == "" || name == "reply" {
			return nil
		}
		path, _ := data["path"].(string)
		command, _ := data["command"].(string)
		urlStr, _ := data["url"].(string)
		query, _ := data["query"].(string)
		source, _ := data["source"].(string)
		dest, _ := data["destination"].(string)
		pattern, _ := data["pattern"].(string)
		handle, _ := data["handle"].(string)
		seconds, _ := data["seconds"].(string)

		var tgt string
		tgt = path
		if tgt == "" && command != "" {
			tgt = command
		}
		if tgt == "" && source != "" && dest != "" {
			tgt = source + " → " + dest
		} else if tgt == "" && source != "" {
			tgt = source
		}
		if tgt == "" && urlStr != "" {
			tgt = urlStr
		}
		if tgt == "" && query != "" {
			tgt = query
		}
		if tgt == "" && pattern != "" {
			tgt = pattern
		}
		if tgt == "" && handle != "" {
			if command != "" {
				tgt = rendering.TruncateLine(command, 40) + " [" + handle + "]"
			} else {
				tgt = handle
			}
		}
		if tgt == "" && seconds != "" {
			tgt = seconds + "s"
		}

		contentLines := toInt(data["content_lines"])
		oldLines := toInt(data["old_lines"])
		newLines := toInt(data["new_lines"])

		if name == "read_file" && tgt != "" {
			offset := toInt(data["offset"])
			limit := toInt(data["limit"])
			if offset > 0 && limit > 0 {
				tgt = fmt.Sprintf("%s line %d-%d", tgt, offset, offset+limit-1)
			} else if offset > 0 {
				tgt = fmt.Sprintf("%s line %d+", tgt, offset)
			} else if limit > 0 {
				tgt = fmt.Sprintf("%s (first %d lines)", tgt, limit)
			}
		}

		data["target"] = tgt
		data["content_lines_int"] = int(contentLines)
		data["old_lines_int"] = int(oldLines)
		data["new_lines_int"] = int(newLines)

		target.UpdateToolStreaming(toolID, name, data)
	}
	return nil
}

func (a *AgentState) handleToolCall(evt ttypes.AgentEventMsg, target *widgets.MessageList, onStatusLine func(string)) tea.Cmd {
	if data, ok := evt.Data.(map[string]any); ok {
		toolID, _ := data["tool_id"].(string)
		name, _ := data["name"].(string)
		if name == "" || name == "reply" {
			return nil
		}
		args, _ := data["args"].(string)
		argsCompact := tools.SummarizeToolArgs(name, args)
		onStatusLine(rendering.ToolLabel(name, argsCompact))
		target.UpdateToolCall(toolID, name, argsCompact)
	}
	return nil
}

func (a *AgentState) handleToolProgress(evt ttypes.AgentEventMsg, target *widgets.MessageList, onStatusLine func(string)) tea.Cmd {
	if data, ok := evt.Data.(map[string]any); ok {
		toolID, _ := data["tool_id"].(string)
		name, _ := data["name"].(string)
		if name == "" || name == "reply" {
			return nil
		}
		phase, _ := data["phase"].(string)
		message, _ := data["message"].(string)
		path, _ := data["path"].(string)
		linesAdded := toInt(data["lines_added"])
		linesRemoved := toInt(data["lines_removed"])

		target.UpdateToolProgress(toolID, name, phase, message, path, int(linesAdded), int(linesRemoved))
		if phase == "done" {
			onStatusLine(name)
		} else if phase != "" && message != "" {
			onStatusLine(message)
		}
	}
	return nil
}

func (a *AgentState) handleToolResult(evt ttypes.AgentEventMsg, target *widgets.MessageList, onStatusLine func(string)) tea.Cmd {
	if data, ok := evt.Data.(map[string]any); ok {
		toolID, _ := data["tool_id"].(string)
		name, _ := data["name"].(string)
		if name == "" || name == "reply" {
			return nil
		}
		result, _ := data["result"].(string)
		errFlag, _ := data["error"].(string)
		isError := errFlag == "true"
		onStatusLine(name)
		target.UpdateToolResult(toolID, name, result, isError)
	}
	return nil
}

func (a *AgentState) handleBlocking(evt ttypes.AgentEventMsg, target *widgets.MessageList, thinkTarget *widgets.MessageList, onBlocking func(), onConfirmReset func(question, operation, tool, target, risk, reason, suggestion string)) tea.Cmd {
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
	tgt := data["target"]
	risk := data["risk"]
	reason := data["reason"]
	suggestion := data["suggestion"]
	a.Thinking = false
	onBlocking()
	target.MarkAllThinkingDone()
	if thinkTarget != target {
		thinkTarget.MarkAllThinkingDone()
	}
	onConfirmReset(question, operation, tool, tgt, risk, reason, suggestion)
	target.AppendMessage(ttypes.ChatMessage{
		Role:    ttypes.RoleAssistant,
		Content: "⚠  " + question,
		Time:    time.Now(),
	})
	// Record the index of this question message so it can be updated in-place later.
	a.blockingMsgIdx = len(target.Messages()) - 1
	return nil
}

// handleBlockingAuto handles operations that are auto-allowed because the user
// previously chose "Always Allow". It shows the question with a checkmark
// indicating it was auto-resolved, without showing a confirmation dialog.
func (a *AgentState) handleBlockingAuto(evt ttypes.AgentEventMsg, target *widgets.MessageList, thinkTarget *widgets.MessageList) tea.Cmd {
	var data map[string]string
	if d, ok := evt.Data.(map[string]any); ok {
		data = make(map[string]string, len(d))
		for k, v := range d {
			data[k] = fmt.Sprintf("%v", v)
		}
	} else if d, ok := evt.Data.(map[string]string); ok {
		data = d
	} else {
		data = map[string]string{"question": fmt.Sprintf("%v", evt.Data)}
	}
	operation := data["operation"]
	tgt := data["target"]
	target.MarkAllThinkingDone()
	if thinkTarget != target {
		thinkTarget.MarkAllThinkingDone()
	}
	// Show a compact, muted tool-like line instead of a verbose assistant message.
	label := operation
	if tgt != "" {
		label += " " + tgt
	}
	target.AppendMessage(ttypes.ChatMessage{
		Role:              ttypes.RoleTool,
		ToolName:          "auto_allowed",
		ToolStatus:        "success",
		ToolProgressLabel: label,
		Time:              time.Now(),
	})
	return nil
}

func (a *AgentState) handleDone(evt ttypes.AgentEventMsg, target *widgets.MessageList, thinkTarget *widgets.MessageList, onThinking func(thinking, streaming, blocking bool), onStatusLine func(string)) tea.Cmd {
	if a.TokenBuf != "" {
		target.AppendToken(a.TokenBuf)
		a.TokenBuf = ""
	}
	wasStreaming := a.Streaming
	target.MarkAllThinkingDone()
	target.ClearStreamingTools()
	if thinkTarget != target {
		thinkTarget.MarkAllThinkingDone()
		thinkTarget.ClearStreamingTools()
	}
	a.Thinking = false
	a.Streaming = false
	onThinking(false, false, false)
	onStatusLine("")

	var content, reason string
	switch d := evt.Data.(type) {
	case string:
		content = d
	case map[string]any:
		content, _ = d["content"].(string)
		reason, _ = d["reason"].(string)
	}

	for _, msg := range target.Messages() {
		msg.CachedLines = nil
		msg.StreamCompletedLines = nil
		msg.StreamCompletedLen = 0
	}
	target.InvalidateAllMessages()
	if thinkTarget != target {
		for _, msg := range thinkTarget.Messages() {
			msg.CachedLines = nil
			msg.StreamCompletedLines = nil
			msg.StreamCompletedLen = 0
		}
		thinkTarget.InvalidateAllMessages()
	}

	if content != "" {
		msgs := target.Messages()
		merged := false
		if wasStreaming && len(msgs) > 0 {
			last := msgs[len(msgs)-1]
			if last.Role == ttypes.RoleAssistant {
				if last.Content == "" {
					last.Content = content
				}
				last.CursorLine = -1
				merged = true
			}
		}
		if !merged {
			target.AppendMessage(ttypes.ChatMessage{
				Role:    ttypes.RoleAssistant,
				Content: content,
				Time:    time.Now(),
			})
		} else {
			target.RebuildItems()
		}
	}
	target.ScrollToBottom()

	return a.doneResultCmd(reason)
}

func formatErr(data any) string {
	return fmt.Sprintf("%v", data)
}
