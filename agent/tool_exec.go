package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"crude/provider"
	"crude/safety"
	"crude/tools"
)

type execResult struct {
	index     int
	id        string
	params    map[string]any
	name      string
	result    tools.ToolResult
	toolError bool
}

type toolParamSchema struct {
	Required []string `json:"required"`
}

func findToolDef(name string, defs []tools.ToolDef) *tools.ToolFuncDef {
	for i := range defs {
		if defs[i].Function.Name == name {
			return &defs[i].Function
		}
	}
	return nil
}

func validateRequiredParams(toolName string, params map[string]any, defs []tools.ToolDef) string {
	def := findToolDef(toolName, defs)
	if def == nil {
		return ""
	}
	var schema toolParamSchema
	if err := json.Unmarshal(def.Parameters, &schema); err != nil || len(schema.Required) == 0 {
		return ""
	}
	var empty []string
	for _, req := range schema.Required {
		val, exists := params[req]
		if !exists {
			empty = append(empty, req)
			continue
		}
		if s, ok := val.(string); ok && strings.TrimSpace(s) == "" {
			empty = append(empty, req)
		}
	}
	if len(empty) == 0 {
		return ""
	}
	return fmt.Sprintf("Error: tool %q has empty required parameter(s): %s. You MUST provide a non-empty value for each required parameter. Call %s again with %s filled in.", toolName, strings.Join(empty, ", "), toolName, strings.Join(empty, ", "))
}

func (a *Agent) executeToolCalls(ctx context.Context, toolCalls []provider.ToolCall, emit func(AgentEvent), blockResolveCh <-chan string, lastReply *string, step int) <-chan execResult {
	ch := make(chan execResult, len(toolCalls))

	type indexedToolCall struct {
		index int
		call  provider.ToolCall
	}
	var nonBlocking, blocking []indexedToolCall
	for index, tc := range toolCalls {
		indexed := indexedToolCall{index: index, call: tc}
		if isPotentiallyBlocking(tc.Function.Name) {
			blocking = append(blocking, indexed)
		} else {
			nonBlocking = append(nonBlocking, indexed)
		}
	}

	send := func(result execResult) {
		select {
		case ch <- result:
		case <-ctx.Done():
		}
	}

	for _, indexed := range nonBlocking {
		a.logger.Debug("AGENT", "goroutine_start", "tool", indexed.call.Function.Name, "step", step)
		go func(indexed indexedToolCall) {
			result := a.executeOneToolCallSafely(ctx, indexed.call, emit, nil, lastReply)
			result.index = indexed.index
			send(result)
		}(indexed)
	}

	if len(blocking) > 0 {
		go func() {
			for _, indexed := range blocking {
				a.logger.Debug("AGENT", "blocking_start", "tool", indexed.call.Function.Name, "step", step)
				result := a.executeOneToolCallSafely(ctx, indexed.call, emit, blockResolveCh, lastReply)
				result.index = indexed.index
				send(result)
			}
		}()
	}

	return ch
}

func (a *Agent) executeOneToolCallSafely(
	ctx context.Context,
	tc provider.ToolCall,
	emit func(AgentEvent),
	blockResolveCh <-chan string,
	lastReply *string,
) (result execResult) {
	defer func() {
		if recovered := recover(); recovered != nil {
			message := fmt.Sprintf("Error: tool %q panicked: %v", tc.Function.Name, recovered)
			a.logger.Error("TOOL", "panic", "name", tc.Function.Name, "error", recovered)
			emit(AgentEvent{Type: "tool_progress", Data: map[string]any{
				"internal_id": tc.InternalID,
				"tool_id":     tc.ID,
				"name":        tc.Function.Name,
				"phase":       "error",
			}})
			emit(AgentEvent{Type: "tool_result", Data: map[string]any{
				"internal_id": tc.InternalID,
				"tool_id":     tc.ID,
				"name":        tc.Function.Name,
				"result":      message,
				"error":       "true",
			}})
			result = execResult{
				id: tc.ID, name: tc.Function.Name,
				result: tools.ToolResult{Content: message}, toolError: true,
			}
		}
	}()
	return a.executeOneToolCall(ctx, tc, emit, blockResolveCh, lastReply)
}

func isPotentiallyBlocking(name string) bool {
	switch name {
	case "question":
		return true
	case "execute_command":
		return true
	case "write_file", "edit_file", "delete_file", "move_file", "copy_file":
		return true
	case "reply", "write_report":
		return true
	case "update_plan", "get_plan":
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
		return execResult{id: tc.ID, name: tc.Function.Name, result: tools.ToolResult{Content: errResult}, toolError: true}
	}

	validationDefs := a.FilteredTools
	if len(validationDefs) == 0 {
		validationDefs = tools.AgentTools
	}
	if errMsg := validateRequiredParams(tc.Function.Name, params, validationDefs); errMsg != "" {
		a.logger.Warn("TOOL", "empty_required_param", "name", tc.Function.Name)
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
			"result":      errMsg,
			"error":       "true",
		}})
		return execResult{id: tc.ID, name: tc.Function.Name, result: tools.ToolResult{Content: errMsg}, toolError: true}
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
			rawOpts := params["options"]
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
			return execResult{id: tc.ID, name: "question", result: tools.ToolResult{Content: "Error: question tool requires a 'questions' array or 'question' parameter"}, toolError: true}
		}

		questionsJSON, _ := json.Marshal(questions)

		emit(AgentEvent{Type: "tool_call", Data: map[string]any{
			"internal_id": internalID,
			"tool_id":     tc.ID,
			"name":        "question",
			"args":        formatToolArgs(params),
		}})
		drainStaleBlockResponses(blockResolveCh)
		emit(AgentEvent{Type: "blocking", Data: map[string]any{
			"operation":      "questions",
			"tool":           "question",
			"target":         getStr(params, "question"),
			"questions_json": string(questionsJSON),
		}})
		resp, ok := waitForBlockResponse(ctx, blockResolveCh)
		if !ok {
			emit(AgentEvent{Type: "blocking_complete"})
			emit(AgentEvent{Type: "tool_result", Data: map[string]any{
				"internal_id": internalID,
				"tool_id":     tc.ID,
				"name":        "question",
				"result":      "Turn cancelled by user.",
			}})
			return execResult{id: tc.ID, name: "question", result: tools.ToolResult{Content: "Turn cancelled by user."}, toolError: true}
		}
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
			return execResult{id: tc.ID, name: "execute_command", result: tools.ToolResult{Content: errMsg}, toolError: true}
		}
		if !a.yoloEnabled() && sr.Risk.NeedsConfirmation() {
			drainStaleBlockResponses(blockResolveCh)
			emit(AgentEvent{Type: "blocking", Data: map[string]string{
				"operation":  sr.Risk.String(),
				"tool":       "execute_command",
				"target":     cmd,
				"question":   fmt.Sprintf("Allow this %s command?", sr.Risk.String()),
				"risk":       sr.Risk.String(),
				"reason":     sr.Reason,
				"suggestion": sr.Suggestion,
			}})
			resp, ok := waitForBlockResponse(ctx, blockResolveCh)
			if !ok {
				emit(AgentEvent{Type: "blocking_complete"})
				return execResult{id: tc.ID, name: "execute_command", result: tools.ToolResult{Content: "Turn cancelled by user."}, toolError: true}
			}
			emitPermissionDecision(emit, tc, resp)
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
				return execResult{id: tc.ID, name: "execute_command", result: tools.ToolResult{Content: errMsg}, toolError: true}
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
		} else if a.yoloEnabled() {
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

	if fileCheck && !a.yoloEnabled() {
		question := fmt.Sprintf("Allow this %s operation on %s?", fileOp, fileTarget)
		drainStaleBlockResponses(blockResolveCh)
		emit(AgentEvent{Type: "blocking", Data: map[string]string{
			"operation":  fileOp,
			"tool":       tc.Function.Name,
			"target":     fileTarget,
			"question":   question,
			"risk":       "write",
			"reason":     fmt.Sprintf("The %s tool modifies files on disk", tc.Function.Name),
			"suggestion": "",
		}})
		resp, ok := waitForBlockResponse(ctx, blockResolveCh)
		if !ok {
			emit(AgentEvent{Type: "blocking_complete"})
			return execResult{id: tc.ID, name: tc.Function.Name, result: tools.ToolResult{Content: "Turn cancelled by user."}, toolError: true}
		}
		emitPermissionDecision(emit, tc, resp)
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
			return execResult{id: tc.ID, name: tc.Function.Name, result: tools.ToolResult{Content: errMsg}, toolError: true}
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
	} else if fileCheck && a.yoloEnabled() {
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
	toolFailed := toolErr != nil || IsToolError(result.Content)
	if tc.Function.Name == "write_report" && !toolFailed {
		a.writeReportCalled = true
	}
	toolDur := time.Since(toolStart).Milliseconds()

	goroutineDur := time.Since(goroutineStart).Milliseconds()
	a.logger.Debug("AGENT", "goroutine_complete", "tool", tc.Function.Name, "tool_id", tc.ID, "goroutine_dur_ms", goroutineDur, "tool_dur_ms", toolDur)

	if toolFailed {
		a.logger.Error("TOOL", "error", "name", tc.Function.Name, "error", result.Content, "latency_ms", toolDur)
	} else {
		a.logger.Info("TOOL", "result", "name", tc.Function.Name, "latency_ms", toolDur, "len", len(result.Content))
	}

	if (tc.Function.Name == "write_file" || tc.Function.Name == "edit_file") && !toolFailed {
		emit(AgentEvent{Type: "tool_progress", Data: map[string]any{
			"internal_id":     internalID,
			"tool_id":         tc.ID,
			"name":            tc.Function.Name,
			"phase":           "done",
			"path":            path,
			"message":         fmt.Sprintf("\u2212%d/+%d lines", result.Removed, result.Added),
			"lines_added":     result.Added,
			"lines_removed":   result.Removed,
			"tool_dur_ms":     toolDur,
			"tool_est_tokens": provider.EstimateTokens(tc.Function.Arguments, a.Model),
		}})
	}

	if tc.Function.Name == "write_report" && !toolFailed {
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
			"message":         fmt.Sprintf("+%d lines", result.Added),
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
	if toolFailed {
		resultData["error"] = "true"
		emit(AgentEvent{Type: "tool_progress", Data: map[string]any{
			"internal_id": internalID,
			"tool_id":     tc.ID,
			"name":        tc.Function.Name,
			"phase":       "error",
		}})
	}
	emit(AgentEvent{Type: "tool_result", Data: resultData})

	return execResult{id: tc.ID, params: params, name: tc.Function.Name, result: result, toolError: toolFailed}
}

func emitPermissionDecision(emit func(AgentEvent), tc provider.ToolCall, response string) {
	var decision string
	switch response {
	case "allow":
		decision = "allowed"
	case "always":
		decision = "always"
	case "deny":
		decision = "denied"
	default:
		return
	}
	emit(AgentEvent{Type: "permission_resolved", Data: map[string]any{
		"internal_id": tc.InternalID,
		"tool_id":     tc.ID,
		"tool":        tc.Function.Name,
		"decision":    decision,
	}})
}

// drainStaleBlockResponses drops any value already buffered in the shared
// block-resolve channel. The channel is size-1 and lives for the
// orchestrator's whole lifetime, so a late response from a replaced panel can
// otherwise resolve a brand-new request before its UI renders. Call this
// immediately before emitting a blocking event, when any buffered response is
// necessarily stale.
func drainStaleBlockResponses(responses <-chan string) {
	if responses == nil {
		return
	}
	for {
		select {
		case <-responses:
		default:
			return
		}
	}
}

func waitForBlockResponse(ctx context.Context, responses <-chan string) (string, bool) {
	if responses == nil {
		return "", false
	}
	select {
	case response, ok := <-responses:
		if !ok {
			return "", false
		}
		return response, true
	case <-ctx.Done():
		return "", false
	}
}
