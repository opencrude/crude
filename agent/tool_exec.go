package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"crude/codexio"
	"crude/provider"
	"crude/safety"
	"crude/tools"
	ttypes "crude/types"
)

type execResult struct {
	index     int
	id        string
	params    map[string]any
	name      string
	result    tools.ToolResult
	toolError bool
	// failure marks a tool whose effects are uncertain (it panicked). Later
	// serialized calls of the same batch are skipped, but the run continues:
	// the model receives the error and inspects state before acting again.
	failure error
}

type toolPanicError struct{ message string }

func (e *toolPanicError) Error() string { return e.message }

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
	if toolName == "apply_patch" {
		if _, err := codexio.Parse(getStr(params, "input")); err != nil {
			return "Error: " + err.Error()
		}
		return ""
	}
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
	if len(empty) > 0 {
		return fmt.Sprintf("Error: tool %q has empty required parameter(s): %s. You MUST provide a non-empty value for each required parameter. Call %s again with %s filled in.", toolName, strings.Join(empty, ", "), toolName, strings.Join(empty, ", "))
	}
	if toolName == "edit_file" {
		old, oldOK := params["old_string"].(string)
		replacement, newOK := params["new_string"].(string)
		if oldOK && newOK && old == replacement {
			return "Error: no changes to make: old_string and new_string are exactly the same. Do not call edit_file unless the replacement differs."
		}
	}
	return ""
}

func (a *Agent) executeToolCalls(ctx context.Context, toolCalls []provider.ToolCall, emit func(AgentEvent), blockResolveCh <-chan ttypes.BlockResponse, lastReply *string, step int) <-chan execResult {
	ch := make(chan execResult, len(toolCalls))

	type indexedToolCall struct {
		index int
		call  provider.ToolCall
	}
	var nonBlocking, blocking []indexedToolCall
	for index, tc := range toolCalls {
		indexed := indexedToolCall{index: index, call: tc}
		if (isPotentiallyBlocking(tc.Function.Name) || strings.HasPrefix(tc.Function.Name, "mcp__")) && !a.parallelCommandAllowed(tc) {
			blocking = append(blocking, indexed)
		} else {
			nonBlocking = append(nonBlocking, indexed)
		}
	}

	send := func(result execResult) {
		ch <- result
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
			var uncertain error
			for _, indexed := range blocking {
				if uncertain != nil {
					send(execResult{index: indexed.index, id: indexed.call.ID, name: indexed.call.Function.Name,
						result: tools.ToolResult{Content: "Not executed: an earlier tool failed with uncertain effects."}, toolError: true})
					continue
				}
				a.logger.Debug("AGENT", "blocking_start", "tool", indexed.call.Function.Name, "step", step)
				result := a.executeOneToolCallSafely(ctx, indexed.call, emit, blockResolveCh, lastReply)
				result.index = indexed.index
				uncertain = result.failure
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
	blockResolveCh <-chan ttypes.BlockResponse,
	lastReply *string,
) (result execResult) {
	if err := ctx.Err(); err != nil {
		return execResult{id: tc.ID, name: tc.Function.Name, result: tools.ToolResult{Content: "Turn cancelled before tool execution."}, toolError: true}
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			message := fmt.Sprintf("Error: tool %q panicked: %v. Its effects are uncertain: inspect the affected state before relying on or retrying it.", tc.Function.Name, recovered)
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
				failure: &toolPanicError{message: message},
			}
		}
	}()
	return a.executeOneToolCall(ctx, tc, emit, blockResolveCh, lastReply)
}

func isPotentiallyBlocking(name string) bool {
	switch name {
	case "question", "multi_tool_use.parallel":
		return true
	case "exec_command", "write_stdin", "execute_command", "async_execute_command":
		return true
	case "apply_patch", "write_file", "edit_file", "delete_file", "move_file", "copy_file":
		return true
	case "reply", "publish_report":
		return true
	case "update_plan", "get_plan":
		return true
	// Approval-producing operations need the shared block response channel;
	// without it an authorization prompt resolves as an immediate denial.
	case "capability_activate", "mcp_request_access", "crude_control":
		return true
	default:
		return false
	}
}

func (a *Agent) executeOneToolCall(ctx context.Context, tc provider.ToolCall, emit func(AgentEvent), blockResolveCh <-chan ttypes.BlockResponse, lastReply *string) execResult {
	internalID := tc.InternalID
	blockID := internalID
	if blockID == "" {
		blockID = tc.ID
	}
	goroutineStart := time.Now()

	params, parseErr := provider.ToolCallParams(tc)
	if err := parseErr; err != nil {
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
	if validationDefs == nil {
		validationDefs = tools.ForModel(tools.AgentTools, a.Model)
	}
	errMsg := ""
	if a.FilteredTools != nil && findToolDef(tc.Function.Name, validationDefs) == nil {
		errMsg = fmt.Sprintf("Error: tool %q is not available to this agent.", tc.Function.Name)
	} else {
		errMsg = validateRequiredParams(tc.Function.Name, params, validationDefs)
	}
	if errMsg == "" && tc.Function.Name == "apply_patch" {
		errMsg = a.validatePatchPermissions(params)
	}
	if errMsg == "" {
		metadata, known := tools.NativeMetadata(tc.Function.Name)
		if a.ToolMetadata != nil {
			metadata, known = a.ToolMetadata(tc.Function.Name)
		}
		if strings.HasPrefix(tc.Function.Name, "mcp__") && (!known || !PolicyAllowsMetadata(a.Policy, metadata)) {
			errMsg = "Error: dynamic tool is unknown or denied by the effective agent policy."
		}
	}
	if errMsg != "" {
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
		// Deliver progress in the ordered result loop: a parallel reply must
		// occupy the same position live and in the persisted transcript.
		return execResult{id: tc.ID, params: params, name: tc.Function.Name, result: tools.ToolResult{Content: content}}
	}

	if tc.Function.Name == "question" && blockResolveCh != nil {
		questionsJSON, questionErr := normalizeQuestionRequest(params)
		if questionErr != nil {
			errResult := "Error: invalid question request: " + questionErr.Error() + ". Call question again with a valid questions array."
			emit(AgentEvent{Type: "tool_call", Data: map[string]any{
				"internal_id": internalID, "tool_id": tc.ID, "name": "question", "args": formatToolArgs(params),
			}})
			emit(AgentEvent{Type: "tool_progress", Data: map[string]any{
				"internal_id": internalID, "tool_id": tc.ID, "name": "question", "phase": "error",
			}})
			emit(AgentEvent{Type: "tool_result", Data: map[string]any{
				"internal_id": internalID, "tool_id": tc.ID, "name": "question", "result": errResult, "error": "true",
			}})
			return execResult{id: tc.ID, name: "question", result: tools.ToolResult{Content: errResult}, toolError: true}
		}

		emit(AgentEvent{Type: "tool_call", Data: map[string]any{
			"internal_id": internalID,
			"tool_id":     tc.ID,
			"name":        "question",
			"args":        formatToolArgs(params),
		}})
		drainStaleBlockResponses(blockResolveCh)
		emit(AgentEvent{Type: "blocking", Data: map[string]any{
			"block_id":       blockID,
			"operation":      "questions",
			"tool":           "question",
			"questions_json": string(questionsJSON),
		}})
		resp, ok := waitForBlockResponse(ctx, blockResolveCh, blockID)
		if !ok {
			emit(AgentEvent{Type: "blocking_complete", Data: map[string]string{"block_id": blockID}})
			result := "Question interrupted because the active turn was cancelled."
			emit(AgentEvent{Type: "tool_result", Data: map[string]any{
				"internal_id": internalID,
				"tool_id":     tc.ID,
				"name":        "question",
				"result":      result,
				"error":       "true",
			}})
			return execResult{id: tc.ID, name: "question", result: tools.ToolResult{Content: result}, toolError: true}
		}
		emit(AgentEvent{Type: "blocking_complete", Data: map[string]string{"block_id": blockID}})
		result, resultError := questionBlockResult(resp)
		resultData := map[string]any{
			"internal_id": internalID,
			"tool_id":     tc.ID,
			"name":        "question",
			"result":      result,
		}
		if resultError {
			resultData["error"] = "true"
		}
		emit(AgentEvent{Type: "tool_result", Data: resultData})
		return execResult{id: tc.ID, name: "question", result: tools.ToolResult{Content: result}, toolError: resultError}
	}

	if tc.Function.Name == "execute_command" || tc.Function.Name == "async_execute_command" || tc.Function.Name == "exec_command" || (tc.Function.Name == "write_stdin" && getStr(params, "chars") != "") {
		cmd := getStr(params, "command")
		if tc.Function.Name == "exec_command" {
			cmd = getStr(params, "cmd")
		}
		if tc.Function.Name == "write_stdin" {
			cmd = getStr(params, "chars")
		}
		sr := safety.CheckCommandSafetyWithPolicy(cmd, a.logger, a.CommandPolicy)
		if getStr(params, "sandbox_permissions") == "require_escalated" || (tc.Function.Name == "write_stdin" && cmd != "\x03") {
			if !sr.Risk.IsAlwaysBlocked() {
				sr.Risk = safety.RiskUnknown
				sr.Reason = "Explicit execution permission or interactive input requires approval"
			}
		}
		if a.CommandPolicy.NotifyDangerous && sr.Risk >= safety.RiskDestructive {
			emit(AgentEvent{Type: "safety_alert", Data: map[string]any{
				"risk": sr.Risk.String(), "reason": sr.Reason, "command": cmd,
			}})
		}
		if sr.Risk.IsAlwaysBlocked() {
			errMsg := fmt.Sprintf("[SAFETY BLOCK] This command was automatically blocked and will never execute. DO NOT retry this command or any variant of it.\nBlock reason: %s\nSuggestion: %s\nRaw command: %s\n\nThis is NOT a command error — the system prevented a dangerous operation. Move on to a different approach. Explain to the user why this was blocked and what you'll do instead.", sr.Reason, sr.Suggestion, cmd)
			a.logger.Warn("SAFETY", "blocked", "command", cmd, "reason", sr.Reason)
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
		autoAllowed := false
		if !a.yoloEnabled() && sr.NeedsConfirmation() {
			var denied *execResult
			autoAllowed, denied = a.reviewPermission(ctx, tc, params, sr.Risk, emit)
			if denied != nil {
				return *denied
			}
		}
		if !autoAllowed && !a.yoloEnabled() && sr.NeedsConfirmation() {
			drainStaleBlockResponses(blockResolveCh)
			emit(AgentEvent{Type: "blocking", Data: map[string]string{
				"block_id":   blockID,
				"operation":  sr.Risk.String(),
				"tool":       tc.Function.Name,
				"target":     cmd,
				"question":   fmt.Sprintf("Allow this %s command?", sr.Risk.String()),
				"risk":       sr.Risk.String(),
				"reason":     sr.Reason,
				"suggestion": sr.Suggestion,
				"rule":       sr.MatchedRule,
			}})
			response, ok := waitForBlockResponse(ctx, blockResolveCh, blockID)
			if !ok {
				emit(AgentEvent{Type: "blocking_complete", Data: map[string]string{"block_id": blockID}})
				return execResult{id: tc.ID, name: tc.Function.Name, result: tools.ToolResult{Content: "Permission request interrupted because the active turn was cancelled."}, toolError: true}
			}
			resp := response.Value
			emitPermissionDecision(emit, tc, resp)
			emit(AgentEvent{Type: "blocking_complete", Data: map[string]string{"block_id": blockID}})
			if resp == "deny" {
				errMsg := fmt.Sprintf("[SAFETY BLOCK] The user denied this command. DO NOT retry it or any variant.\nRisk level: %s\nDeny reason: %s\nSuggestion: %s\nRaw command: %s\n\nThis is NOT a command error — the user explicitly rejected this operation. Move on to a different approach. Tell the user what you'll do instead.", sr.Risk.String(), sr.Reason, sr.Suggestion, cmd)
				a.logger.Warn("SAFETY", "denied", "command", cmd, "risk", sr.Risk.String())
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
			a.logger.Info("BLOCK", "resolved", "response", resp, "operation", sr.Risk.String(), "tool", "execute_command")
			if resp == "always" {
				emit(AgentEvent{Type: "confirmation_always", Data: map[string]string{
					"operation": sr.Risk.String(),
					"tool":      tc.Function.Name,
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
	case "apply_patch":
		fileCheck = true
		fileOp = "Update"
		parsed, _ := codexio.Parse(getStr(params, "input"))
		fileTarget = strings.Join(parsed.Paths(""), ", ")
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

	autoAllowed := false
	if fileCheck && !a.yoloEnabled() {
		var denied *execResult
		autoAllowed, denied = a.reviewPermission(ctx, tc, params, safety.RiskProjectWrite, emit)
		if denied != nil {
			return *denied
		}
	}
	if fileCheck && !autoAllowed && !a.yoloEnabled() {
		question := fmt.Sprintf("Allow this %s operation on %s?", fileOp, fileTarget)
		drainStaleBlockResponses(blockResolveCh)
		blockingData := map[string]string{
			"block_id":   blockID,
			"operation":  fileOp,
			"tool":       tc.Function.Name,
			"target":     fileTarget,
			"question":   question,
			"risk":       "write",
			"reason":     fmt.Sprintf("The %s tool modifies files on disk", tc.Function.Name),
			"suggestion": "",
		}
		if tc.Function.Name == "apply_patch" {
			blockingData["patch_input"] = getStr(params, "input")
		}
		emit(AgentEvent{Type: "blocking", Data: blockingData})
		response, ok := waitForBlockResponse(ctx, blockResolveCh, blockID)
		if !ok {
			emit(AgentEvent{Type: "blocking_complete", Data: map[string]string{"block_id": blockID}})
			return execResult{id: tc.ID, name: tc.Function.Name, result: tools.ToolResult{Content: "Permission request interrupted because the active turn was cancelled."}, toolError: true}
		}
		resp := response.Value
		emitPermissionDecision(emit, tc, resp)
		emit(AgentEvent{Type: "blocking_complete", Data: map[string]string{"block_id": blockID}})
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
		if resp == "always" && tc.Function.Name == "apply_patch" {
			emit(AgentEvent{Type: "confirmation_always", Data: map[string]string{"operation": fileOp, "tool": tc.Function.Name, "patch_input": getStr(params, "input")}})
		} else if resp == "always" {
			emit(AgentEvent{Type: "confirmation_always", Data: map[string]string{
				"operation": fileOp,
				"tool":      tc.Function.Name,
				"target":    filePath,
			}})
		}
	} else if fileCheck && a.yoloEnabled() {
		a.logger.Debug("SAFETY", "yolo_skip", "tool", tc.Function.Name, "op", fileOp)
	}

	emit(AgentEvent{Type: "tool_call", Data: map[string]any{
		"internal_id": internalID,
		"tool_id":     tc.ID,
		"name":        tc.Function.Name,
		"args":        tc.Function.Arguments,
	}})

	path := getStr(params, "path")

	switch tc.Function.Name {
	case "apply_patch":
		paths, _, _ := codexio.Preview(getStr(params, "input"))
		path = strings.Join(paths, ", ")
		emit(AgentEvent{Type: "tool_progress", Data: map[string]any{"internal_id": internalID, "tool_id": tc.ID, "name": tc.Function.Name, "phase": "patching", "path": path}})
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
	case "publish_report":
		report := getStr(params, "body")
		lines := CountLines(report)
		emit(AgentEvent{Type: "tool_progress", Data: map[string]any{
			"internal_id": internalID,
			"tool_id":     tc.ID,
			"name":        tc.Function.Name,
			"phase":       "publishing",
			"path":        "report",
			"message":     fmt.Sprintf("Publishing report (%d lines)", lines),
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
	ctx = context.WithValue(ctx, tools.ExecutionIdentityKey{}, tools.ExecutionIdentity{AgentID: string(a.AgentID), RunID: a.RunID, MessageID: tc.InternalID, ToolCallID: tc.ID})

	var result tools.ToolResult
	var toolErr error
	if tc.Function.Name == "multi_tool_use.parallel" {
		result, toolErr = a.executeParallel(ctx, tc, params, emit, blockResolveCh, lastReply)
	} else if fileCheck && filePath != "" {
		mu := a.fileMutex(filePath)
		func() {
			mu.Lock()
			defer mu.Unlock()
			result, toolErr = a.ToolFn(ctx, tc.Function.Name, params)
		}()
	} else {
		if tc.Function.Name == "agent_start" {
			ctx = context.WithValue(ctx, SubAgentToolCallIDKey{}, tc.ID)
		}
		result, toolErr = a.ToolFn(ctx, tc.Function.Name, params)
	}
	var approval interface {
		CapabilityApproval() (string, string)
		CapabilityApprovalContext(context.Context) context.Context
	}
	seenApprovals := make(map[string]bool)
	for phase := 0; phase < 3 && errors.As(toolErr, &approval); phase++ {
		id, reason := approval.CapabilityApproval()
		intent := id + "\x00" + reason
		if seenApprovals[intent] {
			toolErr = errors.New("identical authorized intent did not advance; capability execution stopped")
			break
		}
		seenApprovals[intent] = true
		origin := "human"
		authorized := false
		var automatic interface {
			CapabilityApprovalContextFor(context.Context, string) context.Context
			Evidence() map[string]any
		}
		if errors.As(toolErr, &automatic) {
			if a.yoloEnabled() {
				authorized, origin = true, "yolo"
			} else {
				evidence := automatic.Evidence()
				risk := approvalRisk(evidence)
				// The reviewer receives actual redacted phase evidence, not just the
				// model's opaque capability ID or its claimed authorization.
				encoded, _ := json.Marshal(evidence)
				reviewCall := tc
				reviewCall.Function.Arguments = string(encoded)
				var denied *execResult
				authorized, denied = a.reviewPermission(ctx, reviewCall, evidence, risk, emit)
				if denied != nil {
					return *denied
				}
				if authorized {
					origin = "reviewer"
				}
			}
		}
		if !authorized {
			drainStaleBlockResponses(blockResolveCh)
			emit(AgentEvent{Type: "blocking", Data: approvalBlockingData(blockID, tc.Function.Name, id, reason, toolErr)})
			response, answered := waitForBlockResponse(ctx, blockResolveCh, blockID)
			emit(AgentEvent{Type: "blocking_complete", Data: map[string]string{"block_id": blockID}})
			authorized = answered && (response.Value == "allow" || response.Value == "always")
		}
		if authorized {
			if origin == "human" {
				emitPermissionDecision(emit, tc, "allow")
			}
			approvedContext := approval.CapabilityApprovalContext(ctx)
			if automatic != nil {
				approvedContext = automatic.CapabilityApprovalContextFor(ctx, origin)
			}
			ctx = approvedContext
			result, toolErr = a.ToolFn(approvedContext, tc.Function.Name, params)
		} else {
			if tc.Function.Name == "crude_control" {
				result = tools.ToolResult{Content: "Crude control operation was denied or cancelled. Do not retry or broaden the requested operation; use an available adequate method."}
				toolErr = errors.New("crude control operation not approved")
			} else {
				result = tools.ToolResult{Content: "Capability change was denied or cancelled. Do not retry or expand permissions; use an available adequate method."}
				toolErr = errors.New("capability change not approved")
			}
			break
		}
	}
	if toolErr != nil && result.Content == "" {
		result.Content = fmt.Sprintf("Error: %s", toolErr.Error())
		if result.ContentParts != nil {
			result.ContentParts = append(result.ContentParts, provider.ContentPart{Type: "text", Text: result.Content})
		}
	}
	toolFailed := toolErr != nil || IsToolError(result.Content)
	if tc.Function.Name == "publish_report" && !toolFailed && getStr(params, "kind") == "final" {
		a.reportPublished = true
	}
	toolDur := time.Since(toolStart).Milliseconds()

	goroutineDur := time.Since(goroutineStart).Milliseconds()
	a.logger.Debug("AGENT", "goroutine_complete", "tool", tc.Function.Name, "tool_id", tc.ID, "goroutine_dur_ms", goroutineDur, "tool_dur_ms", toolDur)

	if toolFailed {
		a.logger.Error("TOOL", "error", "name", tc.Function.Name, "error", result.Content, "latency_ms", toolDur)
	} else {
		a.logger.Info("TOOL", "result", "name", tc.Function.Name, "latency_ms", toolDur, "len", len(result.Content))
	}

	if (tc.Function.Name == "apply_patch" || tc.Function.Name == "write_file" || tc.Function.Name == "edit_file") && !toolFailed {
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

	if tc.Function.Name == "publish_report" && !toolFailed {
		reportID := ""
		revision := 0
		if raw := result.Content; raw != "" {
			var parsed struct {
				Artifact struct {
					ID       string `json:"id"`
					Revision int    `json:"revision"`
				} `json:"artifact"`
			}
			if json.Unmarshal([]byte(raw), &parsed) == nil {
				reportID, revision = parsed.Artifact.ID, parsed.Artifact.Revision
			}
		}
		emit(AgentEvent{Type: "tool_progress", Data: map[string]any{
			"internal_id":     internalID,
			"tool_id":         tc.ID,
			"name":            tc.Function.Name,
			"phase":           "done",
			"path":            reportID,
			"message":         fmt.Sprintf("Published report revision %d", revision),
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
	if tc.Function.Name == "apply_patch" || tc.Function.Name == "write_file" || tc.Function.Name == "edit_file" {
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

	var panicErr *toolPanicError
	var failure error
	if errors.As(toolErr, &panicErr) {
		failure = panicErr
	}
	return execResult{id: tc.ID, params: params, name: tc.Function.Name, result: result, toolError: toolFailed, failure: failure}
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
func drainStaleBlockResponses(responses <-chan ttypes.BlockResponse) {
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

func waitForBlockResponse(ctx context.Context, responses <-chan ttypes.BlockResponse, requestID string) (ttypes.BlockResponse, bool) {
	if responses == nil {
		return ttypes.BlockResponse{}, false
	}
	for {
		select {
		case response, ok := <-responses:
			if !ok {
				return ttypes.BlockResponse{}, false
			}
			if response.RequestID != requestID {
				continue
			}
			return response, true
		case <-ctx.Done():
			return ttypes.BlockResponse{}, false
		}
	}
}

type normalizedQuestion struct {
	Question string   `json:"question"`
	Header   string   `json:"header"`
	Options  []string `json:"options,omitempty"`
	Multiple bool     `json:"multiple,omitempty"`
}

func normalizeQuestionRequest(params map[string]any) ([]byte, error) {
	raw, exists := params["questions"]
	if !exists || raw == nil {
		return nil, fmt.Errorf("missing required parameter %q", "questions")
	}
	var encoded []byte
	var err error
	if text, ok := raw.(string); ok {
		encoded = []byte(text)
	} else {
		encoded, err = json.Marshal(raw)
		if err != nil {
			return nil, fmt.Errorf("questions cannot be encoded: %w", err)
		}
	}
	var source []struct {
		Question string            `json:"question"`
		Header   string            `json:"header"`
		Options  []json.RawMessage `json:"options"`
		Multiple bool              `json:"multiple"`
	}
	if err := json.Unmarshal(encoded, &source); err != nil {
		return nil, fmt.Errorf("questions must be a JSON array: %w", err)
	}
	if len(source) == 0 || len(source) > 4 {
		return nil, fmt.Errorf("questions must contain between 1 and 4 items")
	}
	questions := make([]normalizedQuestion, 0, len(source))
	for i, item := range source {
		item.Question = strings.TrimSpace(item.Question)
		item.Header = strings.TrimSpace(item.Header)
		if item.Question == "" {
			return nil, fmt.Errorf("questions[%d].question must be non-empty", i)
		}
		if item.Header == "" {
			return nil, fmt.Errorf("questions[%d].header must be non-empty", i)
		}
		if len([]rune(item.Header)) > 30 {
			return nil, fmt.Errorf("questions[%d].header must be at most 30 characters", i)
		}
		if len(item.Options) > 5 {
			return nil, fmt.Errorf("questions[%d].options must contain at most 5 choices", i)
		}
		options := make([]string, 0, len(item.Options))
		seen := make(map[string]struct{}, len(item.Options))
		for optionIndex, optionJSON := range item.Options {
			var label string
			if err := json.Unmarshal(optionJSON, &label); err != nil {
				var option struct {
					Label string `json:"label"`
				}
				if objectErr := json.Unmarshal(optionJSON, &option); objectErr != nil {
					return nil, fmt.Errorf("questions[%d].options[%d] must be a string or label object", i, optionIndex)
				}
				label = option.Label
			}
			label = strings.TrimSpace(label)
			if label == "" {
				return nil, fmt.Errorf("questions[%d].options[%d] must be non-empty", i, optionIndex)
			}
			if _, duplicate := seen[label]; duplicate {
				return nil, fmt.Errorf("questions[%d].options contains duplicate choice %q", i, label)
			}
			seen[label] = struct{}{}
			options = append(options, label)
		}
		questions = append(questions, normalizedQuestion{
			Question: item.Question, Header: item.Header, Options: options,
			Multiple: item.Multiple,
		})
	}
	return json.Marshal(questions)
}

func questionBlockResult(response ttypes.BlockResponse) (string, bool) {
	switch response.Outcome {
	case ttypes.BlockAnswered:
		if strings.TrimSpace(response.Value) == "" {
			return "Question was answered without any selected or written response. Ask again with clearer options if the answer is required.", true
		}
		if response.AttributionKind == "agent" || response.AttributionKind == "external" {
			return fmt.Sprintf("Answer supplied by %s %q, not by the human user:\n%s", response.AttributionKind, response.AttributionID, response.Value), false
		}
		return response.Value, false
	case ttypes.BlockUserCancelled:
		if response.AttributionKind == "agent" || response.AttributionKind == "external" {
			return fmt.Sprintf("The %s %q cancelled this task question without a human answer. Do not treat this as human approval.", response.AttributionKind, response.AttributionID), true
		}
		return "The user cancelled this question without answering. Continue only if the missing answer is not required; otherwise explain what information is still needed.", true
	case ttypes.BlockValidationError:
		detail := strings.TrimSpace(response.Detail)
		if detail == "" {
			detail = "the question payload could not be rendered"
		}
		return "Question request validation failed: " + detail + ". Fix the payload and call question again.", true
	default:
		return "Question request failed because its completion outcome was invalid. Call question again.", true
	}
}

// approvalRisk maps the owner-resolved effect of an exact authorization phase
// to the reviewer's risk vocabulary. Unknown effects stay unknown, never safe.
func approvalRisk(evidence map[string]any) safety.CommandRisk {
	switch fmt.Sprint(evidence["effect"]) {
	case "write":
		return safety.RiskProjectWrite
	case "delete":
		return safety.RiskDestructive
	default:
		return safety.RiskUnknown
	}
}

// approvalBlockingData describes an exact authorization phase by its resolved
// effect. Crude control operations are not capability changes, and a write,
// delete or execute must never be presented as a read.
func approvalBlockingData(blockID, tool, target, reason string, cause error) map[string]string {
	data := map[string]string{"block_id": blockID, "operation": "Read", "tool": tool, "target": target,
		"question": "Approve this exact capability change?", "reason": reason}
	var evidence interface{ Evidence() map[string]any }
	if !errors.As(cause, &evidence) {
		return data
	}
	fields := evidence.Evidence()
	effect := fmt.Sprint(fields["effect"])
	if effect == "" || effect == "<nil>" {
		return data
	}
	data["operation"] = strings.ToUpper(effect[:1]) + effect[1:]
	data["risk"] = approvalRisk(fields).String()
	if fields["phase"] == "control" {
		data["question"] = fmt.Sprintf("Allow Crude to %s: %s?", effect, target)
		if input, ok := fields["input"].(json.RawMessage); ok && controlArgumentLines(input) != "" {
			data["reason"] = controlArgumentLines(input)
		} else {
			data["reason"] = "This operation changes Crude's own state through its semantic control API."
		}
	}
	return data
}

// controlArgumentLines presents exact control arguments as readable
// "path: value" lines rather than a raw JSON envelope.
func controlArgumentLines(raw json.RawMessage) string {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return ""
	}
	var lines []string
	sensitive := func(path string) bool {
		path = strings.ToLower(path)
		for _, term := range []string{"password", "secret", "token", "credential", "api_key", "apikey", "private_key"} {
			if strings.Contains(path, term) {
				return true
			}
		}
		return false
	}
	var walk func(prefix string, value any)
	walk = func(prefix string, value any) {
		if sensitive(prefix) {
			lines = append(lines, prefix+": [redacted]")
			return
		}
		switch typed := value.(type) {
		case map[string]any:
			keys := make([]string, 0, len(typed))
			for key := range typed {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				path := key
				if prefix != "" {
					path = prefix + "." + key
				}
				walk(path, typed[key])
			}
		case []any:
			if len(typed) == 0 {
				lines = append(lines, prefix+": (none)")
			}
			for index, item := range typed {
				walk(fmt.Sprintf("%s[%d]", prefix, index), item)
			}
		case nil:
			lines = append(lines, prefix+": null")
		default:
			lines = append(lines, prefix+": "+fmt.Sprint(typed))
		}
	}
	walk("", value)
	return strings.Join(lines, "\n")
}
