package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"

	"crude/provider"
	"crude/statemachine"
	"crude/tools"
)

func (a *Agent) Run(
	ctx context.Context,
	sessionMessages []provider.ChatMessage,
	systemPrompt string,
	emit func(AgentEvent),
	blockResolveCh <-chan string,
	sm *statemachine.ConversationStateMachine,
	sessionID string,
	btwCh <-chan string,
) []provider.ChatMessage {
	l := a.logger
	var emitActive atomic.Bool
	emitActive.Store(true)
	defer emitActive.Store(false)
	origEmit := emit
	emit = func(evt AgentEvent) {
		if !emitActive.Load() {
			return
		}
		origEmit(evt)
	}

	allTools := toolDefsToProviderDefs(a.FilteredTools)
	if len(allTools) == 0 {
		allTools = allToolDefs()
	}

	l.Debug("AGENT", "run_enter", "msgs", len(sessionMessages), "tools", len(allTools), "session_id", sessionID)

	lastReply := ""
	msgs := []provider.ChatMessage{
		{Role: "system", Content: systemPrompt, CacheControl: &provider.CacheControl{Type: "ephemeral"}},
	}
	l.Debug("CACHE", "system_ephemeral", "position", 0, "len", len(systemPrompt))
	for _, m := range sessionMessages {
		if m.Role != "system" {
			msgs = append(msgs, m)
		}
	}

	buildSMStatusMsg := func() *provider.ChatMessage {
		if sm == nil || sessionID == "" {
			return nil
		}
		parts := make([]string, 0, 3)
		if status := BuildStateMachineStatus(sm, sessionID); status != "" {
			parts = append(parts, "## Current State\n"+status)
		}
		if a.AsyncMgr != nil {
			if asyncStatus := BuildAsyncCommandStatus(a.AsyncMgr); asyncStatus != "" {
				parts = append(parts, asyncStatus)
			}
		}
		if a.SubAgentMgr != nil {
			if subStatus := BuildSubAgentStatus(a.SubAgentMgr); subStatus != "" {
				parts = append(parts, subStatus)
			}
		}
		if len(parts) == 0 {
			return nil
		}
		return &provider.ChatMessage{Role: "system", Content: strings.Join(parts, "\n\n"), CacheControl: &provider.CacheControl{Type: "ephemeral"}}
	}

	var steps []StepRecord
	var totalPromptTokens, totalCompletionTokens int
	var lastPromptTokens int

	ctxWindow := a.Provider.ContextWindow()
	ctxLimit := int(float64(ctxWindow) * contextSafetyPct)
	l.Debug("CTX", "window", "model", a.Model, "ctx_window", ctxWindow, "safety_limit", ctxLimit, "safety_pct", contextSafetyPct)

	stopConditions := []stopCondition{
		func(steps []StepRecord) bool {
			return hasRepeatedToolCalls(steps, loopDetectWindow, loopDetectRepeats)
		},
		func(steps []StepRecord) bool {
			return ctxLimit > 0 && lastPromptTokens > ctxLimit
		},
	}

	stepsLimit := maxSteps
	if a.MaxSteps > 0 {
		stepsLimit = a.MaxSteps
	}
	for step := 0; step < stepsLimit; step++ {
		if ctx.Err() != nil {
			l.Warn("CANCEL", "context_cancelled", "step", step, "reason", ctx.Err().Error())
			emit(AgentEvent{Type: "done", Data: map[string]any{"content": "Cancelled.", "reason": "cancelled"}})
			return msgs
		}

		if step == 0 {
			emit(AgentEvent{Type: "phase", Data: "grounding"})
		}

		drainBtw := func() {
			for {
				select {
				case btwText := <-btwCh:
					l.Info("BTW", "user_interruption", "len", len(btwText), "step", step)
					msgs = append(msgs, provider.ChatMessage{
						Role:    "user",
						Content: "[USER INTERRUPTION — READ THIS NOW]\nThe user interrupted your work with an important message while you were running. Stop what you're doing, read this carefully, and adjust your approach accordingly. This takes priority over whatever you were doing.\n\n" + btwText,
					})
				default:
					return
				}
			}
		}
		drainBtw()

		if smStatus := buildSMStatusMsg(); smStatus != nil {
			replaced := false
			for i := len(msgs) - 1; i >= 0; i-- {
				if msgs[i].Role == "system" && containsPrefix(msgs[i].Content, "## Current State") {
					msgs[i] = *smStatus
					replaced = true
					break
				}
			}
			if !replaced {
				msgs = append(msgs, *smStatus)
			}
		}

		prevPreviewKeys := make(map[string]string)

		content, reasoningContent, finishReason, streamToolCalls, promptTokens, completionTokens, promptTokensCached, err := a.completeStream(ctx, msgs, allTools,
			func(token string) {
				emit(AgentEvent{Type: "stream_token", Data: token})
			},
			func(text string) {
				emit(AgentEvent{Type: "thinking", Data: text})
			},
			func(internalID, id, name string) {
				emit(AgentEvent{Type: "tool_call_streaming", Data: map[string]any{
					"internal_id": internalID,
					"tool_id":     id,
					"name":        name,
					"preview":     tools.FormatPreview(name, ExtractPreview(id, name, "", a.logger).Params),
				}})
			},
			a.makeStreamingDeltaCallback(emit, prevPreviewKeys),
			func(attempt int, backoffSeconds float64) {
				if attempt <= 3 {
					return
				}
				emit(AgentEvent{Type: "retry", Data: map[string]any{
					"attempt": attempt,
					"backoff": backoffSeconds,
				}})
			},
		)
		if err != nil {
			var apiErr *apiCallError
			if errors.As(err, &apiErr) && apiErr.IsCancellation() {
				emit(AgentEvent{Type: "done", Data: map[string]any{"content": "Cancelled.", "reason": "cancelled"}})
				return msgs
			}
			a.emitError(emit, err)
			return msgs
		}

		totalPromptTokens += promptTokens
		totalCompletionTokens += completionTokens
		lastPromptTokens = promptTokens

		estimated := false
		if promptTokens == 0 && completionTokens == 0 {
			promptTokens = provider.EstimateMessagesTotal(msgs, a.Model)
			completionTokens = provider.EstimateTokens(content, a.Model)
			estimated = true
		}

		price := provider.LookupPrice(a.Model)
		turnCost := price.CalculateCost(promptTokens, completionTokens, promptTokensCached)
		emit(AgentEvent{Type: "usage", Data: map[string]any{
			"prompt":         promptTokens,
			"completion":     completionTokens,
			"prompt_cached":  promptTokensCached,
			"context_window": ctxWindow,
			"cost":           turnCost,
			"model":          a.Model,
			"estimated":      estimated,
		}})

		accumulatedText := content
		toolCalls := streamToolCalls

		a.logger.Info("LLM", "step_done", "text_len", len(accumulatedText), "tool_calls", len(toolCalls), "finish_reason", finishReason, "cumulative_prompt_tokens", totalPromptTokens, "ctx_window", ctxWindow)

		if accumulatedText == "" {
			accumulatedText = lastReply
		}

		hasToolCalls := len(toolCalls) > 0
		if hasToolCalls {
			l.Debug("AGENT", "branch_has_tool_calls", "count", len(toolCalls), "step", step)
		} else {
			l.Debug("AGENT", "branch_text_only", "text_len", len(accumulatedText), "step", step)
		}

		assistantMsg := provider.ChatMessage{Role: "assistant", Content: accumulatedText, ReasoningContent: reasoningContent, Reasoning: reasoningContent}
		if hasToolCalls {
			assistantMsg.ToolCalls = toolCalls
		}
		msgs = append(msgs, assistantMsg)

		if !hasToolCalls {
			if accumulatedText != "" {
				emit(AgentEvent{Type: "done", Data: map[string]any{"content": accumulatedText, "reason": "completed"}})
			} else {
				emit(AgentEvent{Type: "done", Data: map[string]any{"content": "Done.", "reason": "completed"}})
			}
			return msgs
		}

		emit(AgentEvent{Type: "phase", Data: "executing"})

		resultCh := a.executeToolCalls(ctx, toolCalls, emit, blockResolveCh, &lastReply, step)

		var lastToolResultIdx int
		for i := 0; i < len(toolCalls); i++ {
			select {
			case r := <-resultCh:
				msgs = append(msgs, provider.ChatMessage{
					Role:       "tool",
					ToolCallID: r.id,
					Content:    r.result.Content,
				})
				lastToolResultIdx = len(msgs) - 1
				steps = append(steps, StepRecord{
					Content:      accumulatedText,
					ToolName:     r.name,
					ToolInput:    fmt.Sprintf("%v", r.params),
					ToolResult:   r.result.Content,
					FinishReason: finishReason,
				})
				if r.name == "reply" && r.result.Content != "" {
					emit(AgentEvent{Type: "stream_token", Data: r.result.Content})
				}
			case <-ctx.Done():
				l.Warn("CANCEL", "context_cancelled", "step", step, "reason", ctx.Err().Error())
				emit(AgentEvent{Type: "done", Data: map[string]any{"content": "Cancelled.", "reason": "cancelled"}})
				return msgs
			}
		}

		if lastToolResultIdx > 0 && lastToolResultIdx < len(msgs) {
			msgs[lastToolResultIdx].CacheControl = &provider.CacheControl{Type: "ephemeral"}
			l.Debug("CACHE", "breakpoint", "msg_idx", lastToolResultIdx, "role", msgs[lastToolResultIdx].Role)
		}

		if isStopConditionMet(stopConditions, steps) {
			reason := "stop_condition_met"
			if lastPromptTokens > ctxLimit {
				reason = fmt.Sprintf("context_limit_reached (%d/%d tokens)", lastPromptTokens, ctxWindow)
				l.Warn("CTX", "overflow", "prompt_tokens", lastPromptTokens, "limit", ctxLimit, "window", ctxWindow)
			}
			loopSigs := 0
			if len(steps) >= loopDetectWindow {
				window := steps[len(steps)-loopDetectWindow:]
				sigCount := make(map[string]int)
				for _, s := range window {
					if s.ToolName != "" {
						sigCount[s.ToolName]++
					}
				}
				loopSigs = len(sigCount)
			}
			l.Debug("LOOP", "check", "steps", len(steps), "window", loopDetectWindow, "unique_sigs", loopSigs)
			l.Warn("AGENT", "stop_condition", "reason", reason, "steps", len(steps), "prompt_tokens", lastPromptTokens, "total_prompt_tokens_spent", totalPromptTokens, "ctx_window", ctxWindow)
			if lastReply != "" {
				emit(AgentEvent{Type: "done", Data: map[string]any{"content": lastReply, "reason": reason}})
			} else {
				emit(AgentEvent{Type: "done", Data: map[string]any{"content": accumulatedText, "reason": reason}})
			}
			return msgs
		}
	}

	l.Warn("AGENT", "max_steps", "steps", stepsLimit, "total_prompt_tokens", totalPromptTokens)
	emit(AgentEvent{Type: "done", Data: map[string]any{"content": "Reached maximum " + fmt.Sprint(stepsLimit) + " steps. Task may be incomplete.", "reason": "max_steps_reached"}})
	return msgs
}

func (a *Agent) makeStreamingDeltaCallback(emit func(AgentEvent), prevPreviewKeys map[string]string) func(internalID, id, name, args string) {
	return func(internalID, id, name, args string) {
		preview := ExtractPreview(id, name, args, a.logger)
		path := getStr(preview.Params, "path")
		command := getStr(preview.Params, "command")
		urlStr := getStr(preview.Params, "url")
		query := getStr(preview.Params, "query")
		source := getStr(preview.Params, "source")
		dest := getStr(preview.Params, "destination")
		pattern := getStr(preview.Params, "pattern")
		handle := getStr(preview.Params, "handle")
		handleID := getStr(preview.Params, "handle_id")
		seconds := getStr(preview.Params, "seconds")

		var offsetVal int
		if o, ok := preview.Params["offset"]; ok {
			switch v := o.(type) {
			case float64:
				offsetVal = int(v)
			case int:
				offsetVal = v
			}
		}
		var limitVal int
		if l, ok := preview.Params["limit"]; ok {
			switch v := l.(type) {
			case float64:
				limitVal = int(v)
			case int:
				limitVal = v
			}
		}

		contentStr := getStr(preview.Params, "content")
		if contentStr == "" {
			contentStr = getStr(preview.Params, "report")
		}
		oldStr := getStr(preview.Params, "old_string")
		newStr := getStr(preview.Params, "new_string")
		if newStr == "" {
			newStr = getStr(preview.Params, "new_text")
		}

		contentLines := 0
		if contentStr != "" {
			contentLines = CountLines(contentStr)
		}
		oldLines := 0
		if oldStr != "" {
			oldLines = CountLines(oldStr)
		}
		newLines := 0
		if newStr != "" {
			newLines = CountLines(newStr)
		}

		if name == "write_file" || name == "edit_file" || name == "write_report" {
			estContent, estOld, estNew := EstimateStreamingLines(name, args)
			if contentLines == 0 && estContent > 0 {
				contentLines = estContent
			}
			if oldLines == 0 && estOld > 0 {
				oldLines = estOld
			}
			if newLines == 0 && estNew > 0 {
				newLines = estNew
			}
		}

		previewKey := fmt.Sprintf("%s|%s|%s|%s|%s|%s|%s|%d|%d|%d|%d|%d",
			path, command, urlStr, query, source, dest, pattern,
			offsetVal, limitVal, contentLines, oldLines, newLines)
		if prev, ok := prevPreviewKeys[internalID]; ok && prev == previewKey {
			return
		}
		prevPreviewKeys[internalID] = previewKey

		data := map[string]any{
			"internal_id":     internalID,
			"tool_id":         id,
			"name":            name,
			"preview":         tools.FormatPreview(name, preview.Params),
			"args":            args,
			"tool_est_tokens": provider.EstimateTokens(args, a.Model),
		}
		if path != "" {
			data["path"] = path
		}
		if command != "" {
			data["command"] = command
		}
		if urlStr != "" {
			data["url"] = urlStr
		}
		if query != "" {
			data["query"] = query
		}
		if source != "" {
			data["source"] = source
		}
		if dest != "" {
			data["destination"] = dest
		}
		if pattern != "" {
			data["pattern"] = pattern
		}
		if handle != "" {
			data["handle"] = handle
		}
		if handleID != "" {
			data["handle_id"] = handleID
		}
		if seconds != "" {
			data["seconds"] = seconds
		}
		if offsetVal > 0 {
			data["offset"] = offsetVal
		}
		if limitVal > 0 {
			data["limit"] = limitVal
		}
		if contentLines > 0 {
			data["content_lines"] = contentLines
		}
		if oldLines > 0 {
			data["old_lines"] = oldLines
		}
		if newLines > 0 {
			data["new_lines"] = newLines
		}
		emit(AgentEvent{Type: "tool_call_streaming", Data: data})
	}
}
