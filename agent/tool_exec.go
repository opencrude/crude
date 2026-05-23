package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"crude/provider"
	"crude/safety"
	"crude/tools"
)

type execResult struct {
	id     string
	params map[string]any
	name   string
	result tools.ToolResult
}

func (a *Agent) executeToolCalls(ctx context.Context, toolCalls []provider.ToolCall, emit func(AgentEvent), blockResolveCh <-chan string, lastReply *string, step int) <-chan execResult {
	ch := make(chan execResult, len(toolCalls))

	var nonBlocking, blocking []provider.ToolCall
	for _, tc := range toolCalls {
		if isPotentiallyBlocking(tc.Function.Name, a.Yolo) {
			blocking = append(blocking, tc)
		} else {
			nonBlocking = append(nonBlocking, tc)
		}
	}

	for _, tc := range nonBlocking {
		a.logger.Debug("AGENT", "goroutine_start", "tool", tc.Function.Name, "step", step)
		go func(tc provider.ToolCall) {
			ch <- a.executeOneToolCall(ctx, tc, emit, nil, lastReply)
		}(tc)
	}

	if len(blocking) > 0 {
		go func() {
			for _, tc := range blocking {
				a.logger.Debug("AGENT", "blocking_start", "tool", tc.Function.Name, "step", step)
				ch <- a.executeOneToolCall(ctx, tc, emit, blockResolveCh, lastReply)
			}
		}()
	}

	return ch
}

func isPotentiallyBlocking(name string, yolo bool) bool {
	switch name {
	case "question":
		return !yolo
	case "execute_command":
		return true
	case "write_file", "edit_file", "delete_file", "move_file", "copy_file":
		return !yolo
	case "plan", "mark_step_complete", "mark_step_doing", "mark_step_failed",
		"report_blocker", "resolve_blocker", "list_goal_status":
		return true
	default:
		return false
	}
}

func (a *Agent) executeOneToolCall(ctx context.Context, tc provider.ToolCall, emit func(AgentEvent), blockResolveCh <-chan string, lastReply *string) execResult {
	internalID := tc.InternalID
	goroutineStart := time.Now()

	var params map[string]any
	if err := json.Unmarshal([]byte(tc.Function.Arguments), &params); err != nil {
		a.logger.Error("TOOL", "invalid_args", "name", tc.Function.Name, "error", err.Error())
		errResult := "Error: invalid arguments: " + err.Error()
		emit(AgentEvent{Type: "tool_call", Data: map[string]any{
			"internal_id": internalID,
			"tool_id":     tc.ID,
			"name":        tc.Function.Name,
			"args":        tc.Function.Arguments,
		}})
		emit(AgentEvent{Type: "tool_progress", Data: map[string]any{
			"internal_id": internalID,
			"tool_id":     tc.ID,
			"name":        tc.Function.Name,
			"phase":       "error",
		}})
		emit(AgentEvent{Type: "tool_result", Data: map[string]any{
			"internal_id": internalID,
			"tool_id":     tc.ID,
			"name":        tc.Function.Name,
			"result":      errResult,
			"error":       "true",
		}})
		return execResult{id: tc.ID, name: tc.Function.Name, result: tools.ToolResult{Content: errResult}}
	}

	if tc.Function.Name == "reply" {
		content := getStr(params, "content")
		*lastReply = content
		a.logger.Debug("AGENT", "goroutine_reply", "content_len", len(content), "tool_id", tc.ID)
		return execResult{id: tc.ID, params: params, name: tc.Function.Name, result: tools.ToolResult{Content: content}}
	}

	if tc.Function.Name == "question" && blockResolveCh != nil {
		questionText := getStr(params, "question")
		rawQuestions, hasQuestions := params["questions"]

		var questions []map[string]any
		if hasQuestions && rawQuestions != nil {
			if arr, ok := rawQuestions.([]any); ok {
				for _, q := range arr {
					if m, ok := q.(map[string]any); ok {
						questions = append(questions, m)
					}
				}
			}
		} else if questionText != "" {
			rawOpts, _ := params["options"]
			var optStrs []string
			if rawOpts != nil {
				if arr, ok := rawOpts.([]any); ok {
					for _, v := range arr {
						if s, ok := v.(string); ok {
							optStrs = append(optStrs, s)
						}
					}
				}
			}
			q := map[string]any{"question": questionText}
			if len(optStrs) > 0 {
				q["options"] = optStrs
			}
			questions = []map[string]any{q}
		}

		if len(questions) == 0 {
			return execResult{id: tc.ID, name: "question", result: tools.ToolResult{Content: "Error: question tool requires a 'questions' array or 'question' parameter"}}
		}

		questionsJSON, _ := json.Marshal(questions)

		emit(AgentEvent{Type: "tool_call", Data: map[string]any{
			"internal_id": internalID,
			"tool_id":     tc.ID,
			"name":        "question",
			"args":        formatToolArgs(params),
		}})
		emit(AgentEvent{Type: "blocking", Data: map[string]any{
			"operation":      "questions",
			"tool":           "question",
			"target":         getStr(params, "question"),
			"questions_json":  string(questionsJSON),
		}})
		resp := <-blockResolveCh
		emit(AgentEvent{Type: "blocking_complete"})
		emit(AgentEvent{Type: "tool_result", Data: map[string]any{
			"internal_id": internalID,
			"tool_id":     tc.ID,
			"name":        "question",
			"result":      resp,
		}})
		return execResult{id: tc.ID, name: "question", result: tools.ToolResult{Content: resp}}
	}

	if tc.Function.Name == "execute_command" {
		cmd := getStr(params, "command")
		sr := safety.CheckCommandSafety(cmd, a.logger)
		if sr.Risk.IsAlwaysBlocked() {
			errMsg := fmt.Sprintf("[SAFETY BLOCK] This command was automatically blocked and will never execute. DO NOT retry this command or any variant of it.\nBlock reason: %s\nSuggestion: %s\nRaw command: %s\n\nThis is NOT a command error — the system prevented a dangerous operation. Move on to a different approach. Explain to the user why this was blocked and what you'll do instead.", sr.Reason, sr.Suggestion, cmd)
			a.logger.Warn("SAFETY", "blocked", "command", cmd, "reason", sr.Reason)
			emit(AgentEvent{Type: "tool_progress", Data: map[string]any{
				"internal_id": internalID,
				"tool_id":     tc.ID,
				"name":        "execute_command",
				"phase":       "error",
			}})
			emit(AgentEvent{Type: "tool_result", Data: map[string]any{
				"internal_id": internalID,
				"tool_id":     tc.ID,
				"name":        "execute_command",
				"result":      errMsg,
				"error":       "true",
			}})
			return execResult{id: tc.ID, name: "execute_command", result: tools.ToolResult{Content: errMsg}}
		}
		if !a.Yolo && sr.Risk.NeedsConfirmation() {
			emit(AgentEvent{Type: "blocking", Data: map[string]string{
				"operation":  sr.Risk.String(),
				"tool":       "execute_command",
				"target":     cmd,
				"question":   fmt.Sprintf("Allow this %s command?", sr.Risk.String()),
				"risk":       sr.Risk.String(),
				"reason":     sr.Reason,
				"suggestion": sr.Suggestion,
			}})
			resp := <-blockResolveCh
			if resp == "deny" {
				errMsg := fmt.Sprintf("[SAFETY BLOCK] The user denied this command. DO NOT retry it or any variant.\nRisk level: %s\nDeny reason: %s\nSuggestion: %s\nRaw command: %s\n\nThis is NOT a command error — the user explicitly rejected this operation. Move on to a different approach. Tell the user what you'll do instead.", sr.Risk.String(), sr.Reason, sr.Suggestion, cmd)
				a.logger.Warn("SAFETY", "denied", "command", cmd, "risk", sr.Risk.String())
				emit(AgentEvent{Type: "tool_progress", Data: map[string]any{
					"internal_id": internalID,
					"tool_id":     tc.ID,
					"name":        "execute_command",
					"phase":       "error",
				}})
				emit(AgentEvent{Type: "tool_result", Data: map[string]any{
					"internal_id": internalID,
					"tool_id":     tc.ID,
					"name":        "execute_command",
					"result":      errMsg,
					"error":       "true",
				}})
				return execResult{id: tc.ID, name: "execute_command", result: tools.ToolResult{Content: errMsg}}
			}
			a.logger.Info("BLOCK", "resolved", "response", resp, "operation", sr.Risk.String(), "tool", "execute_command")
			emit(AgentEvent{Type: "blocking_complete"})
			if resp == "always" {
				emit(AgentEvent{Type: "confirmation_always", Data: map[string]string{
					"operation": sr.Risk.String(),
					"tool":      "execute_command",
					"target":    cmd,
				}})
			}
		} else if a.Yolo {
			a.logger.Debug("SAFETY", "yolo_skip", "tool", "execute_command")
		}
	}

	fileCheck := false
	fileOp := ""
	fileTarget := ""
	filePath := ""
	switch tc.Function.Name {
	case "write_file":
		fileCheck = true
		fileOp = "Create"
		filePath = getStr(params, "path")
		fileTarget = filePath
	case "edit_file":
		fileCheck = true
		fileOp = "Update"
		filePath = getStr(params, "path")
		fileTarget = filePath
	case "delete_file":
		fileCheck = true
		fileOp = "Delete"
		filePath = getStr(params, "path")
		fileTarget = filePath
	case "move_file":
		fileCheck = true
		fileOp = "Update"
		src := getStr(params, "source")
		dst := getStr(params, "destination")
		filePath = dst
		fileTarget = src + " → " + dst
	case "copy_file":
		fileCheck = true
		fileOp = "Create"
		src := getStr(params, "source")
		dst := getStr(params, "destination")
		filePath = dst
		fileTarget = src + " → " + dst
	}

	if fileCheck && !a.Yolo {
		question := fmt.Sprintf("Allow this %s operation on %s?", fileOp, fileTarget)
		emit(AgentEvent{Type: "blocking", Data: map[string]string{
			"operation":  fileOp,
			"tool":       tc.Function.Name,
			"target":     fileTarget,
			"question":   question,
			"risk":       "write",
			"reason":     fmt.Sprintf("The %s tool modifies files on disk", tc.Function.Name),
			"suggestion": "",
		}})
		resp := <-blockResolveCh
		if resp == "deny" {
			errMsg := fmt.Sprintf("[SAFETY BLOCK] The user denied this %s operation. DO NOT retry it or any variant.\nTool: %s\nTarget: %s\n\nThis is NOT a tool error — the user explicitly rejected this operation. Move on to a different approach. Tell the user what you'll do instead.", fileOp, tc.Function.Name, fileTarget)
			a.logger.Warn("SAFETY", "denied", "tool", tc.Function.Name, "target", fileTarget, "op", fileOp)
			emit(AgentEvent{Type: "tool_progress", Data: map[string]any{
				"internal_id": internalID,
				"tool_id":     tc.ID,
				"name":        tc.Function.Name,
				"phase":       "error",
			}})
			emit(AgentEvent{Type: "tool_result", Data: map[string]any{
				"internal_id": internalID,
				"tool_id":     tc.ID,
				"name":        tc.Function.Name,
				"result":      errMsg,
				"error":       "true",
			}})
			return execResult{id: tc.ID, name: tc.Function.Name, result: tools.ToolResult{Content: errMsg}}
		}
		a.logger.Info("BLOCK", "resolved", "response", resp, "operation", fileOp, "tool", tc.Function.Name)
		emit(AgentEvent{Type: "blocking_complete"})
		if resp == "always" {
			emit(AgentEvent{Type: "confirmation_always", Data: map[string]string{
				"operation": fileOp,
				"tool":      tc.Function.Name,
				"target":    filePath,
			}})
		}
	} else if fileCheck && a.Yolo {
		a.logger.Debug("SAFETY", "yolo_skip", "tool", tc.Function.Name, "op", fileOp)
	}

	argsStr := formatToolArgs(params)
	emit(AgentEvent{Type: "tool_call", Data: map[string]any{
		"internal_id": internalID,
		"tool_id":     tc.ID,
		"name":        tc.Function.Name,
		"args":        argsStr,
	}})

	path := getStr(params, "path")

	switch tc.Function.Name {
	case "write_file":
		content := getStr(params, "content")
		lines := CountLines(content)
		emit(AgentEvent{Type: "tool_progress", Data: map[string]any{
			"internal_id": internalID,
			"tool_id":     tc.ID,
			"name":        tc.Function.Name,
			"phase":       "writing",
			"path":        path,
			"message":     fmt.Sprintf("Writing %d lines to %s", lines, path),
		}})
	case "write_report":
		report := getStr(params, "report")
		lines := CountLines(report)
		emit(AgentEvent{Type: "tool_progress", Data: map[string]any{
			"internal_id": internalID,
			"tool_id":     tc.ID,
			"name":        tc.Function.Name,
			"phase":       "writing",
			"path":        "report",
			"message":     fmt.Sprintf("Writing report (%d lines)", lines),
		}})
	case "edit_file":
		oldStr := getStr(params, "old_string")
		newStr := getStr(params, "new_string")
		oldLines := CountLines(oldStr)
		newLines := CountLines(newStr)
		emit(AgentEvent{Type: "tool_progress", Data: map[string]any{
			"internal_id": internalID,
			"tool_id":     tc.ID,
			"name":        tc.Function.Name,
			"phase":       "patching",
			"path":        path,
			"message":     fmt.Sprintf("Editing %s (−%d/+%d lines)", path, oldLines, newLines),
		}})
	case "read_file":
		if path != "" {
			emit(AgentEvent{Type: "tool_progress", Data: map[string]any{
				"internal_id": internalID,
				"tool_id":     tc.ID,
				"name":        tc.Function.Name,
				"phase":       "reading",
				"path":        path,
				"message":     fmt.Sprintf("Reading %s", path),
			}})
		}
	case "execute_command":
		cmd := getStr(params, "command")
		if cmd != "" {
			display := cmd
			if len([]rune(display)) > 60 {
				display = string([]rune(display)[:60]) + "\u2026"
			}
			emit(AgentEvent{Type: "tool_progress", Data: map[string]any{
				"internal_id": internalID,
				"tool_id":     tc.ID,
				"name":        tc.Function.Name,
				"phase":       "running",
				"message":     fmt.Sprintf("Running %s", display),
			}})
		}
	}

	a.logger.Info("TOOL", "call", "name", tc.Function.Name, "params", formatToolArgs(params))
	toolStart := time.Now()

	var result tools.ToolResult
	var toolErr error
	if fileCheck && filePath != "" {
		mu := a.fileMutex(filePath)
		mu.Lock()
		result, toolErr = a.ToolFn(ctx, tc.Function.Name, params)
		mu.Unlock()
	} else {
		result, toolErr = a.ToolFn(ctx, tc.Function.Name, params)
	}
	if toolErr != nil && result.Content == "" {
		result.Content = fmt.Sprintf("Error: %s", toolErr.Error())
	}
	toolDur := time.Since(toolStart).Milliseconds()

	goroutineDur := time.Since(goroutineStart).Milliseconds()
	a.logger.Debug("AGENT", "goroutine_complete", "tool", tc.Function.Name, "tool_id", tc.ID, "goroutine_dur_ms", goroutineDur, "tool_dur_ms", toolDur)

	if IsToolError(result.Content) {
		a.logger.Error("TOOL", "error", "name", tc.Function.Name, "error", result.Content, "latency_ms", toolDur)
	} else {
		a.logger.Info("TOOL", "result", "name", tc.Function.Name, "latency_ms", toolDur, "len", len(result.Content))
	}

	if (tc.Function.Name == "write_file" || tc.Function.Name == "edit_file") && !IsToolError(result.Content) {
		emit(AgentEvent{Type: "tool_progress", Data: map[string]any{
			"internal_id":   internalID,
			"tool_id":       tc.ID,
			"name":          tc.Function.Name,
			"phase":         "done",
			"path":          path,
			"message":       fmt.Sprintf("\u2212%d/+%d lines", result.Removed, result.Added),
			"lines_added":   result.Added,
			"lines_removed": result.Removed,
			"tool_dur_ms":   toolDur,
			"tool_est_tokens": provider.EstimateTokens(tc.Function.Arguments, a.Model),
		}})
	}

	if tc.Function.Name == "write_report" && !IsToolError(result.Content) {
		reportPath := ""
		if raw := result.Content; raw != "" {
			var parsed struct {
				ReportPath string `json:"report_path"`
			}
			if json.Unmarshal([]byte(raw), &parsed) == nil {
				reportPath = parsed.ReportPath
			}
		}
		emit(AgentEvent{Type: "tool_progress", Data: map[string]any{
			"internal_id":     internalID,
			"tool_id":         tc.ID,
			"name":            tc.Function.Name,
			"phase":           "done",
			"path":            reportPath,
			"message":          fmt.Sprintf("+%d lines", result.Added),
			"lines_added":     result.Added,
			"lines_removed":   0,
			"tool_dur_ms":     toolDur,
			"tool_est_tokens": provider.EstimateTokens(tc.Function.Arguments, a.Model),
		}})
	}

	resultData := map[string]any{
		"internal_id":     internalID,
		"tool_id":         tc.ID,
		"name":            tc.Function.Name,
		"result":          result.Content,
		"tool_dur_ms":     toolDur,
		"tool_est_tokens": provider.EstimateTokens(tc.Function.Arguments, a.Model),
	}
	if tc.Function.Name == "write_file" || tc.Function.Name == "edit_file" || tc.Function.Name == "write_report" {
		resultData["lines_added"] = result.Added
		resultData["lines_removed"] = result.Removed
	}
	if IsToolError(result.Content) {
		resultData["error"] = "true"
		emit(AgentEvent{Type: "tool_progress", Data: map[string]any{
			"internal_id": internalID,
			"tool_id":     tc.ID,
			"name":        tc.Function.Name,
			"phase":       "error",
		}})
	}
	emit(AgentEvent{Type: "tool_result", Data: resultData})

	return execResult{id: tc.ID, params: params, name: tc.Function.Name, result: result}
}
