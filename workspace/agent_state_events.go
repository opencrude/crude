package workspace

import (
	"fmt"
	"strings"
	"time"

	"crude/tools"

	tea "charm.land/bubbletea/v2"
)

func (a *AgentState) handleToolCallStreaming(evt AgentEvent, target *Conversation, onStatusLine func(string)) tea.Cmd {
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
	agentID, _ := origData["agent_id"].(string)
	runID, _ := origData["run_id"].(string)
	artifactID, _ := origData["artifact_id"].(string)
	seconds, _ := origData["seconds"].(string)

	var tgt string
	if name == "grep_files" && pattern != "" {
		tgt = fmt.Sprintf("\"%s\"", pattern)
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
				tgt = command + " [" + handle + "]"
			} else {
				tgt = handle
			}
		}
		if tgt == "" && runID != "" {
			tgt = runID
		}
		if tgt == "" && agentID != "" {
			tgt = agentID
		}
		if tgt == "" && artifactID != "" {
			tgt = artifactID
		}
		if tgt == "" && seconds != "" {
			tgt = seconds + "s"
		}
	}

	contentLines := toInt(origData["content_lines"])
	oldLines := toInt(origData["old_lines"])
	newLines := toInt(origData["new_lines"])

	if contentLines == 0 && name == "publish_report" {
		if rawArgs, _ := origData["args"].(string); rawArgs != "" {
			if r := ExtractArg(rawArgs, "body"); r != "" {
				contentLines = strings.Count(r, "\n") + 1
			}
		}
	}

	if tgt == "" {
		if rawArgs, _ := origData["args"].(string); rawArgs != "" {
			switch name {
			case "write_file", "edit_file", "read_file", "delete_file", "list_directory":
				if p := ExtractArg(rawArgs, "path"); p != "" {
					tgt = p
				}
			case "move_file", "copy_file":
				s := ExtractArg(rawArgs, "source")
				d := ExtractArg(rawArgs, "destination")
				if s != "" && d != "" {
					tgt = s + " → " + d
				} else if s != "" {
					tgt = s
				}
			case "exec_command":
				if c := ExtractArg(rawArgs, "cmd"); c != "" {
					tgt = c
				}
			case "execute_command", "async_execute_command":
				if c := ExtractArg(rawArgs, "command"); c != "" {
					tgt = c
				}
			case "web_search", "arxiv_search":
				if q := ExtractArg(rawArgs, "query"); q != "" {
					tgt = fmt.Sprintf("\"%s\"", q)
				}
			case "web_fetch":
				if u := ExtractArg(rawArgs, "url"); u != "" {
					tgt = u
				}
			case "grep_files":
				if p := ExtractArg(rawArgs, "pattern"); p != "" {
					tgt = fmt.Sprintf("\"%s\"", p)
				}
			case "publish_report":
				tgt = ExtractArg(rawArgs, "summary")
			case "agent_control":
				tgt = ExtractArg(rawArgs, "agent_id")
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

	onStatusLine(toolOperationStatus(name, "preparing"))
	target.UpdateToolStreaming(toolID, internalID, name, data)
	if round, _ := origData["request_id"].(string); round != "" {
		target.SetToolRound(toolID, internalID, round)
	}
	if name == "multi_tool_use.parallel" {
		if input, _ := origData["batch_input"].(string); input != "" {
			target.SetToolInput(toolID, internalID, input, "")
		}
	}
	return nil
}

func (a *AgentState) handleToolCall(evt AgentEvent, target *Conversation, onStatusLine func(string)) tea.Cmd {
	if data, ok := evt.Data.(map[string]any); ok {
		toolID, _ := data["tool_id"].(string)
		internalID, _ := data["internal_id"].(string)
		name, _ := data["name"].(string)
		if name == "" || name == "reply" {
			// Reply tool calls only contribute a user-facing message when their
			// result arrives; they never allocate a tool row.
			return nil
		}
		args, _ := data["args"].(string)
		argsCompact := SummarizeToolArgs(name, args)
		onStatusLine(ToolLabel(name, argsCompact))
		target.UpdateToolCall(toolID, internalID, name, argsCompact)
		if round, _ := data["request_id"].(string); round != "" {
			target.SetToolRound(toolID, internalID, round)
		}
		if name == "execute_command" || name == "async_execute_command" {
			target.SetToolCommand(toolID, internalID, ExtractArg(args, "command"))
		}
		if name == "exec_command" {
			target.SetToolCommand(toolID, internalID, ExtractArg(args, "cmd"))
		}
		switch name {
		case "multi_tool_use.parallel":
			target.SetToolInput(toolID, internalID, args, "")
		case "grep_files":
			target.SetToolInput(toolID, internalID, ExtractArg(args, "pattern"), ExtractArg(args, "path"))
		case "web_search", "arxiv_search":
			target.SetToolInput(toolID, internalID, ExtractArg(args, "query"), "")
		case "web_fetch":
			target.SetToolInput(toolID, internalID, ExtractArg(args, "url"), "")
		default:
			if tools.RetainsStructuredToolInput(name) {
				target.SetToolInput(toolID, internalID, args, "")
			}
		}
		if a.PendingPermTag != "" && (a.PendingPermTool == "" || a.PendingPermTool == name) {
			target.SetToolPermTag(toolID, internalID, a.PendingPermTag)
			a.PendingPermTag = ""
			a.PendingPermTool = ""
		}
	}
	return nil
}

func (a *AgentState) handleToolProgress(evt AgentEvent, target *Conversation, onStatusLine func(string)) tea.Cmd {
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
			onStatusLine(toolOperationStatus(name, "done"))
		} else if phase == "error" {
			onStatusLine(toolOperationStatus(name, "failed"))
		} else if phase != "" && message != "" {
			onStatusLine(message)
		}
	}
	return nil
}

func (a *AgentState) handleToolResult(evt AgentEvent, target *Conversation, onStatusLine func(string)) tea.Cmd {
	if data, ok := evt.Data.(map[string]any); ok {
		toolID, _ := data["tool_id"].(string)
		internalID, _ := data["internal_id"].(string)
		name, _ := data["name"].(string)
		if name == "" {
			return nil
		}
		result, _ := data["result"].(string)
		errFlag, _ := data["error"].(string)
		isError := errFlag == "true"
		if name == "reply" {
			if result != "" {
				role := MessageRoleAssistant
				if isError {
					role = MessageRoleError
				}
				target.AppendMessage(Message{InternalID: "reply:" + toolID, Role: role,
					Content: SanitizeForTerminal(result), Time: time.Now(),
					TurnGeneration: target.turnIdentity.Generation, AccountID: target.turnIdentity.AccountID,
					ProviderID: target.turnIdentity.ProviderID, ModelID: target.turnIdentity.ModelID})
			}
			return nil
		}
		if !target.HasToolMessage(toolID, internalID) && result != "" {
			target.AppendMessage(Message{InternalID: "tool-result:" + internalID, Role: MessageRoleTool,
				ToolID: toolID, ToolName: name, Content: SanitizeForTerminal(result), Time: time.Now(),
				TurnGeneration: target.turnIdentity.Generation, AccountID: target.turnIdentity.AccountID,
				ProviderID: target.turnIdentity.ProviderID, ModelID: target.turnIdentity.ModelID})
		}
		estTokens := toInt(data["tool_est_tokens"])
		linesAdded := toInt(data["lines_added"])
		linesRemoved := toInt(data["lines_removed"])
		if isError {
			onStatusLine(toolOperationStatus(name, "failed"))
		} else {
			onStatusLine(toolOperationStatus(name, "done"))
		}
		target.UpdateToolResult(toolID, internalID, name, result, isError, int(estTokens), int(linesAdded), int(linesRemoved))
		if interrupted, _ := data["interrupted"].(bool); interrupted {
			target.MarkToolNotExecuted(toolID, internalID)
			onStatusLine("Reconnecting before running tool")
		}
	}
	return nil
}

func toolOperationStatus(name, phase string) string {
	if name == "multi_tool_use.parallel" {
		switch phase {
		case "preparing":
			return "Preparing tools"
		case "failed":
			return "Parallel tools failed"
		default:
			return "Parallel tools finished"
		}
	}
	if phase == "preparing" {
		return "Preparing " + PascalCaseTool(name)
	}
	if phase == "failed" {
		return PascalCaseTool(name) + " failed"
	}
	return PascalCaseTool(name) + " done"
}

func (a *AgentState) handlePermissionResolved(evt AgentEvent, target *Conversation, onStatusLine func(string)) tea.Cmd {
	data, ok := evt.Data.(map[string]any)
	if !ok {
		return nil
	}
	toolID, _ := data["tool_id"].(string)
	internalID, _ := data["internal_id"].(string)
	tool, _ := data["tool"].(string)
	decision, _ := data["decision"].(string)
	if decision == "" {
		return nil
	}
	if !target.SetToolPermTag(toolID, internalID, decision) {
		a.PendingPermTag = decision
		a.PendingPermTool = tool
	}
	switch decision {
	case "denied":
		onStatusLine("Permission denied for " + PascalCaseTool(tool))
	case "reviewer-denied":
		onStatusLine("Automatic reviewer denied " + PascalCaseTool(tool))
	case "reviewer-allowed":
		onStatusLine("Automatic reviewer allowed " + PascalCaseTool(tool))
	case "always":
		onStatusLine("Permission always allowed for " + PascalCaseTool(tool))
	default:
		onStatusLine("Permission allowed for " + PascalCaseTool(tool))
	}
	return nil
}

func (a *AgentState) handleBlocking(evt AgentEvent, target *Conversation, thinkTarget *Conversation, onBlocking func(), onConfirmReset func(question, operation, tool, target, risk, reason, suggestion string), onQuestionReset func(questionsJSON string)) tea.Cmd {
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
	a.PendingBlockID = data["block_id"]
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

	a.PendingPermTool = tool
	a.BlockingMsgIdx = -1
	return nil
}

// handleBlockingAuto handles operations that are auto-allowed because the user
// previously chose "Always Allow". It stores a compact permission tag that will
// be applied to the next matching tool_call message, instead of showing a
// separate verbose message.
func (a *AgentState) handleBlockingAuto(evt AgentEvent, target *Conversation, thinkTarget *Conversation) tea.Cmd {
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
	a.PendingPermTag = "auto"
	a.PendingPermTool = tool
	return nil
}

func (a *AgentState) handleDone(evt AgentEvent, target *Conversation, thinkTarget *Conversation, onThinking func(thinking, streaming, blocking bool), onStatusLine func(string)) tea.Cmd {
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
	a.Running = false
	a.Cancelling = false
	a.PendingPermTag = ""
	a.PendingPermTool = ""
	a.PendingBlockID = ""
	a.BlockingMsgIdx = -1
	onThinking(false, false, false)
	onStatusLine("")

	boundaryID := ""
	if data, ok := evt.Data.(map[string]any); ok {
		boundaryID, _ = data["internal_id"].(string)
	}
	target.Finish(content, reason, wasStreaming, boundaryID)

	return DoneResultCmd(a.ID, reason)
}

func FormatErr(data any) string {
	return fmt.Sprintf("%v", data)
}

func toInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case float64:
		return int(n)
	case int64:
		return int(n)
	}
	return 0
}
