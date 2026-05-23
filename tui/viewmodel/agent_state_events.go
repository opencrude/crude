package viewmodel

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"crude/tui/rendering"
	"crude/tui/tools"
	ttypes "crude/tui/types"
	"crude/tui/widgets"
)

func (a *AgentState) handleToolCallStreaming(evt ttypes.AgentEventMsg, target *widgets.MessageList, onStatusLine func(string)) tea.Cmd {
	origData, ok := evt.Data.(map[string]any)
	if !ok {
		return nil
	}
	toolID, _ := origData["tool_id"].(string)
	internalID, _ := origData["internal_id"].(string)
	name, _ := origData["name"].(string)
	if name == "" || name == "reply" {
		return nil
	}
	path, _ := origData["path"].(string)
	command, _ := origData["command"].(string)
	urlStr, _ := origData["url"].(string)
	query, _ := origData["query"].(string)
	source, _ := origData["source"].(string)
	dest, _ := origData["destination"].(string)
	pattern, _ := origData["pattern"].(string)
	handle, _ := origData["handle"].(string)
	handleID, _ := origData["handle_id"].(string)
	seconds, _ := origData["seconds"].(string)

	var tgt string
	if name == "grep_files" && pattern != "" {
		tgt = fmt.Sprintf("\"%s\"", rendering.TruncateLine(pattern, 30))
		if path != "" && path != "." {
			tgt += " in " + path
		}
	} else {
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
		if tgt == "" && handleID != "" {
			tgt = handleID
		}
		if tgt == "" && seconds != "" {
			tgt = seconds + "s"
		}
	}

	contentLines := toInt(origData["content_lines"])
	oldLines := toInt(origData["old_lines"])
	newLines := toInt(origData["new_lines"])

	if contentLines == 0 && name == "write_report" {
		if rawArgs, _ := origData["args"].(string); rawArgs != "" {
			if r := extractJSONStringValue(rawArgs, "report"); r != "" {
				contentLines = strings.Count(r, "\n") + 1
			}
		}
	}

	if tgt == "" {
		if rawArgs, _ := origData["args"].(string); rawArgs != "" {
			switch name {
			case "write_file", "edit_file", "read_file", "delete_file", "list_directory":
				if p := extractJSONStringValue(rawArgs, "path"); p != "" {
					tgt = p
				}
			case "move_file", "copy_file":
				s := extractJSONStringValue(rawArgs, "source")
				d := extractJSONStringValue(rawArgs, "destination")
				if s != "" && d != "" {
					tgt = s + " → " + d
				} else if s != "" {
					tgt = s
				}
			case "execute_command", "async_execute_command":
				if c := extractJSONStringValue(rawArgs, "command"); c != "" {
					tgt = rendering.TruncateLine(c, 50)
				}
			case "web_search", "arxiv_search":
				if q := extractJSONStringValue(rawArgs, "query"); q != "" {
					tgt = fmt.Sprintf("\"%s\"", rendering.TruncateLine(q, 40))
				}
			case "web_fetch":
				if u := extractJSONStringValue(rawArgs, "url"); u != "" {
					tgt = u
				}
			case "grep_files":
				if p := extractJSONStringValue(rawArgs, "pattern"); p != "" {
					tgt = fmt.Sprintf("\"%s\"", rendering.TruncateLine(p, 30))
				}
			case "write_report":
				if r := extractJSONStringValue(rawArgs, "report"); r != "" {
					tgt = "report"
				}
			case "poll_sub_agent", "cancel_sub_agent":
				if hid := extractJSONStringValue(rawArgs, "handle_id"); hid != "" {
					if len(hid) > 12 {
						tgt = hid[:12] + "…"
					} else {
						tgt = hid
					}
				}
			}
		}
	}

	if name == "read_file" && tgt != "" {
		offset := toInt(origData["offset"])
		limit := toInt(origData["limit"])
		if offset > 0 && limit > 0 {
			tgt = fmt.Sprintf("%s line %d-%d", tgt, offset, offset+limit-1)
		} else if offset > 0 {
			tgt = fmt.Sprintf("%s line %d+", tgt, offset)
		} else if limit > 0 {
			tgt = fmt.Sprintf("%s (first %d lines)", tgt, limit)
		}
	}

	data := make(map[string]any, len(origData)+4)
	for k, v := range origData {
		data[k] = v
	}
	data["target"] = tgt
	data["content_lines_int"] = int(contentLines)
	data["old_lines_int"] = int(oldLines)
	data["new_lines_int"] = int(newLines)

	target.UpdateToolStreaming(toolID, internalID, name, data)
	return nil
}

func (a *AgentState) handleToolCall(evt ttypes.AgentEventMsg, target *widgets.MessageList, onStatusLine func(string)) tea.Cmd {
	if data, ok := evt.Data.(map[string]any); ok {
		toolID, _ := data["tool_id"].(string)
		internalID, _ := data["internal_id"].(string)
		name, _ := data["name"].(string)
		if name == "" || name == "reply" {
			return nil
		}
		args, _ := data["args"].(string)
		argsCompact := tools.SummarizeToolArgs(name, args)
		onStatusLine(rendering.ToolLabel(name, argsCompact))
		target.UpdateToolCall(toolID, internalID, name, argsCompact)
		if a.pendingPermTag != "" && (a.pendingPermTool == "" || a.pendingPermTool == name) {
			target.SetToolPermTag(toolID, internalID, a.pendingPermTag)
			a.pendingPermTag = ""
			a.pendingPermTool = ""
		}
	}
	return nil
}

func (a *AgentState) handleToolProgress(evt ttypes.AgentEventMsg, target *widgets.MessageList, onStatusLine func(string)) tea.Cmd {
	if data, ok := evt.Data.(map[string]any); ok {
		toolID, _ := data["tool_id"].(string)
		internalID, _ := data["internal_id"].(string)
		name, _ := data["name"].(string)
		if name == "" || name == "reply" {
			return nil
		}
		phase, _ := data["phase"].(string)
		message, _ := data["message"].(string)
		path, _ := data["path"].(string)
		linesAdded := toInt(data["lines_added"])
		linesRemoved := toInt(data["lines_removed"])
		estTokens := toInt(data["tool_est_tokens"])

		target.UpdateToolProgress(toolID, internalID, name, phase, message, path, int(linesAdded), int(linesRemoved), int(estTokens))
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
		internalID, _ := data["internal_id"].(string)
		name, _ := data["name"].(string)
		if name == "" || name == "reply" {
			return nil
		}
		result, _ := data["result"].(string)
		errFlag, _ := data["error"].(string)
		isError := errFlag == "true"
		estTokens := toInt(data["tool_est_tokens"])
		linesAdded := toInt(data["lines_added"])
		linesRemoved := toInt(data["lines_removed"])
		onStatusLine(name)
		target.UpdateToolResult(toolID, internalID, name, result, isError, int(estTokens), int(linesAdded), int(linesRemoved))
	}
	return nil
}

func (a *AgentState) handleBlocking(evt ttypes.AgentEventMsg, target *widgets.MessageList, thinkTarget *widgets.MessageList, onBlocking func(), onConfirmReset func(question, operation, tool, target, risk, reason, suggestion string), onQuestionReset func(questionsJSON string)) tea.Cmd {
	var data map[string]string
	questionsJSON := ""
	if d, ok := evt.Data.(map[string]any); ok {
		data = make(map[string]string, len(d))
		for k, v := range d {
			if k == "questions_json" {
				if s, ok := v.(string); ok {
					questionsJSON = s
				}
			}
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

	if operation == "questions" || operation == "question" {
		onQuestionReset(questionsJSON)
	} else {
		onConfirmReset(question, operation, tool, tgt, risk, reason, suggestion)
	}

	a.pendingPermTool = tool
	a.blockingMsgIdx = -1
	return nil
}

// handleBlockingAuto handles operations that are auto-allowed because the user
// previously chose "Always Allow". It stores a compact permission tag that will
// be applied to the next matching tool_call message, instead of showing a
// separate verbose message.
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
	tool := data["tool"]
	target.MarkAllThinkingDone()
	if thinkTarget != target {
		thinkTarget.MarkAllThinkingDone()
	}
	// Store the tag so it's applied as a muted suffix on the next tool_call.
	a.pendingPermTag = "auto"
	a.pendingPermTool = tool
	return nil
}

func (a *AgentState) handleDone(evt ttypes.AgentEventMsg, target *widgets.MessageList, thinkTarget *widgets.MessageList, onThinking func(thinking, streaming, blocking bool), onStatusLine func(string)) tea.Cmd {
	if a.TokenBuf != "" {
		target.AppendToken(a.TokenBuf)
		a.TokenBuf = ""
	}
	wasStreaming := a.Streaming

	var content, reason string
	switch d := evt.Data.(type) {
	case string:
		content = d
		if content == "Cancelled." || content == "Cancelled" {
			reason = "cancelled"
		}
	case map[string]any:
		content, _ = d["content"].(string)
		reason, _ = d["reason"].(string)
	}

	if reason == "cancelled" {
		target.MarkAllThinkingInterrupted()
		if thinkTarget != target {
			thinkTarget.MarkAllThinkingInterrupted()
		}
		target.MarkStreamingToolsCancelled()
		if thinkTarget != target {
			thinkTarget.MarkStreamingToolsCancelled()
		}
	} else if reason != "completed" && reason != "" {
		target.MarkAllThinkingInterrupted()
		if thinkTarget != target {
			thinkTarget.MarkAllThinkingInterrupted()
		}
		target.MarkStreamingToolsCancelled()
		if thinkTarget != target {
			thinkTarget.MarkStreamingToolsCancelled()
		}
	} else {
		target.MarkAllThinkingDone()
		if thinkTarget != target {
			thinkTarget.MarkAllThinkingDone()
		}
	}
	a.Thinking = false
	a.Streaming = false
	a.pendingPermTag = ""
	a.pendingPermTool = ""
	a.blockingMsgIdx = -1
	onThinking(false, false, false)
	onStatusLine("")

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

	isIncomplete := reason != "" && reason != "completed"
	incompleteLabel := reason
	if reason == "cancelled" {
		incompleteLabel = "operation cancelled by user"
	}

	if content != "" && !isIncomplete {
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

	if isIncomplete {
		msgs := target.Messages()
		marked := false
		for i := len(msgs) - 1; i >= 0; i-- {
			last := msgs[i]
			if last.Role == ttypes.RoleAssistant || last.Role == ttypes.RoleThinking {
				last.Incomplete = true
				last.IncompleteReason = incompleteLabel
				last.CursorLine = -1
				last.CachedLines = nil
				last.StreamCompletedLines = nil
				last.StreamCompletedLen = 0
				marked = true
				break
			}
		}
		if !marked {
			target.AppendMessage(ttypes.ChatMessage{
				Role:            ttypes.RoleAssistant,
				Incomplete:      true,
				IncompleteReason: incompleteLabel,
				Time:            time.Now(),
			})
		}
		target.RebuildItems()
	}

	target.ScrollToBottom()

	return a.doneResultCmd(reason)
}

func formatErr(data any) string {
	return fmt.Sprintf("%v", data)
}

func extractJSONStringValue(raw, key string) string {
	needle := `"` + key + `"`
	idx := strings.Index(raw, needle)
	if idx < 0 {
		return ""
	}
	pos := idx + len(needle)
	for pos < len(raw) && raw[pos] != ':' {
		if raw[pos] == ',' || raw[pos] == '}' {
			return ""
		}
		pos++
	}
	if pos >= len(raw) || raw[pos] != ':' {
		return ""
	}
	pos++
	for pos < len(raw) && (raw[pos] == ' ' || raw[pos] == '\t') {
		pos++
	}
	if pos >= len(raw) || raw[pos] != '"' {
		return ""
	}
	pos++
	var b strings.Builder
	for pos < len(raw) {
		ch := raw[pos]
		if ch == '\\' && pos+1 < len(raw) {
			switch raw[pos+1] {
			case '"':
				b.WriteByte('"')
			case '\\':
				b.WriteByte('\\')
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			default:
				b.WriteByte(raw[pos+1])
			}
			pos += 2
			continue
		}
		if ch == '"' {
			break
		}
		b.WriteByte(ch)
		pos++
	}
	return b.String()
}
