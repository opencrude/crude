package agent

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"

	"crude/provider"
	"crude/statemachine"
	"crude/tools"
	ttypes "crude/types"
)

const continuePreviousResponse = "Continue the previous response exactly where it stopped. Do not repeat completed content. Continue using tools if the task still requires them."

type BtwMessage struct {
	Message provider.ChatMessage
	Accept  func() bool
}

func (a *Agent) Run(
	ctx context.Context,
	sessionMessages []provider.ChatMessage,
	systemPrompt string,
	emit func(AgentEvent),
	blockResolveCh <-chan ttypes.BlockResponse,
	sm *statemachine.ConversationStateMachine,
	sessionID string,
	btwCh <-chan BtwMessage,
) []provider.ChatMessage {
	if ctx == nil {
		ctx = context.Background()
	}
	if emit == nil {
		emit = func(AgentEvent) {}
	}
	l := a.logger
	a.writeReportCalled = false
	var emitActive atomic.Bool
	emitActive.Store(true)
	origEmit := emit
	var emitMu sync.Mutex
	defer func() {
		emitMu.Lock()
		emitActive.Store(false)
		emitMu.Unlock()
	}()
	emit = func(evt AgentEvent) {
		if !emitActive.Load() {
			return
		}
		emitMu.Lock()
		defer emitMu.Unlock()
		if !emitActive.Load() {
			return
		}
		if evt.Type == "done" || evt.Type == "error" {
			emitActive.Store(false)
		}
		origEmit(evt)
	}
	if a.Provider == nil {
		a.emitError(emit, fmt.Errorf("agent provider is nil"))
		return append([]provider.ChatMessage(nil), sessionMessages...)
	}

	allTools := toolDefsToProviderDefs(a.FilteredTools)
	if len(allTools) == 0 {
		allTools = allToolDefs()
	}

	l.Debug("AGENT", "run_enter", "msgs", len(sessionMessages), "tools", len(allTools), "session_id", sessionID)

	lastReply := ""
	guard := a.WireGuard
	if guard == nil {
		guard = NewWireGuard()
		a.WireGuard = guard
	}
	sampling := a.Provider.Sampling()
	envelopeOptions := struct {
		Protocol        string   `json:"protocol"`
		Temperature     *float64 `json:"temperature,omitempty"`
		TopP            *float64 `json:"top_p,omitempty"`
		Think           bool     `json:"think"`
		MaxOutputTokens int      `json:"max_output_tokens"`
	}{
		Protocol: a.Provider.ProtocolID(), Temperature: sampling.Temperature, TopP: sampling.TopP,
		Think: true, MaxOutputTokens: maxOutputTokensForContext(a.Provider.ContextWindow(a.Model)),
	}
	if rotated, err := guard.EnsureEnvelope(a.Model, systemPrompt, allTools, envelopeOptions); err != nil {
		a.emitError(emit, fmt.Errorf("prepare wire envelope: %w", err))
		return append([]provider.ChatMessage(nil), sessionMessages...)
	} else if rotated {
		l.Warn("CACHE", "epoch_rotated", "reason", "envelope_changed", "epoch", guard.Epoch())
	}
	msgs := []provider.ChatMessage{
		{Role: "system", Content: provider.TextContent(systemPrompt), CacheControl: &provider.CacheControl{Type: "ephemeral"}},
	}
	systemHash := sha256.Sum256([]byte(systemPrompt))
	l.Debug("CACHE", "prefix", "position", 0, "len", len(systemPrompt), "sha256", fmt.Sprintf("%x", systemHash[:8]), "protocol", a.Provider.ProtocolID())
	msgs = append(msgs, sessionMessages...)
	ledger := append([]provider.ChatMessage(nil), msgs...)
	appendWire := func(messages ...provider.ChatMessage) {
		msgs = append(msgs, messages...)
		ledger = append(ledger, messages...)
	}
	returnMessages := func() []provider.ChatMessage {
		a.lastWireMessages = append([]provider.ChatMessage(nil), msgs[1:]...)
		return append([]provider.ChatMessage(nil), ledger...)
	}
	lastRuntimeStatus := ""
	for i := len(sessionMessages) - 1; i >= 0; i-- {
		if sessionMessages[i].WireHidden && strings.HasPrefix(sessionMessages[i].Content.String(), "## Runtime Status") {
			lastRuntimeStatus = sessionMessages[i].Content.String()
			break
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
		content := "## Runtime Status\n\n" + strings.Join(parts, "\n\n")
		message := provider.NewWireHiddenMessage(content)
		return &message
	}

	var steps []StepRecord
	var totalPromptTokens, totalCompletionTokens int
	var lastPromptTokens int

	ctxWindow := a.Provider.ContextWindow(a.Model)
	activeWindow := activeWireContextWindow(a.Provider.ProtocolID(), ctxWindow)
	ctxLimit := int(float64(activeWindow) * activeWireSafetyPct)
	l.Debug("CTX", "window", "model", a.Model, "ctx_window", ctxWindow, "active_window", activeWindow, "safety_limit", ctxLimit, "safety_pct", activeWireSafetyPct)

	stopConditions := []stopCondition{
		func(steps []StepRecord) bool {
			return hasRepeatedToolCalls(steps, loopDetectWindow, loopDetectRepeats)
		},
	}

	btwInput := btwCh
	continuations := 0
	providerName := a.providerDisplayName()
	cacheUsageUnknown := a.Provider.ProtocolID() == "ollama"
	var admittedBtwIDs []string
	clearUnsentBtw := func() {
		if len(admittedBtwIDs) == 0 {
			return
		}
		msgs = removeMessagesByInternalID(msgs, admittedBtwIDs)
		ledger = removeMessagesByInternalID(ledger, admittedBtwIDs)
		emit(AgentEvent{Type: "btw_cleared", Data: map[string]any{
			"internal_ids": append([]string(nil), admittedBtwIDs...),
		}})
		admittedBtwIDs = nil
	}
	for step := 0; ; step++ {
		if ctx.Err() != nil {
			clearUnsentBtw()
			l.Warn("CANCEL", "context_cancelled", "step", step, "reason", ctx.Err().Error())
			emit(AgentEvent{Type: "done", Data: map[string]any{"content": "Cancelled.", "reason": "cancelled"}})
			return returnMessages()
		}

		emit(AgentEvent{Type: "phase", Data: map[string]any{
			"name": "preparing", "progress": 0, "provider": providerName,
		}})

		drainBtw := func() int {
			consumed := 0
			for {
				select {
				case btw, ok := <-btwInput:
					if !ok {
						btwInput = nil
						return consumed
					}
					if btw.Accept != nil && !btw.Accept() {
						continue
					}
					btwMsg := btw.Message
					l.Info("BTW", "user_interruption", "step", step)
					appendWire(btwMsg)
					admittedBtwIDs = append(admittedBtwIDs, btwMsg.InternalID)
					consumed++
				default:
					return consumed
				}
			}
		}
		_ = drainBtw()

		if smStatus := buildSMStatusMsg(); smStatus != nil {
			if content := smStatus.Content.String(); content != lastRuntimeStatus {
				appendWire(*smStatus)
				lastRuntimeStatus = content
			}
		}
		requestMsgs := msgs
		wireCandidate, wireErr := guard.Check(requestMsgs)
		if wireErr != nil {
			a.emitError(emit, fmt.Errorf("refusing non-append-only provider request: %w", wireErr))
			return returnMessages()
		}
		l.Debug("CACHE", "wire_request", "epoch", wireCandidate.Epoch(), "digest", wireCandidate.Digest(), "messages", len(requestMsgs))

		prevPreviewKeys := make(map[string]string)

		completion, err := a.completeStream(ctx, requestMsgs, allTools,
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
			func() {
				if acceptErr := guard.Accept(wireCandidate); acceptErr != nil {
					l.Error("CACHE", "wire_accept_failed", "error", acceptErr.Error())
				}
				for _, internalID := range admittedBtwIDs {
					emit(AgentEvent{Type: "btw_consumed", Data: map[string]any{"internal_id": internalID}})
				}
				admittedBtwIDs = nil
			},
			func(uploaded, total int64) {
				percent := 0
				if total > 0 {
					percent = int(uploaded * 100 / total)
				}
				if percent >= 100 {
					emit(AgentEvent{Type: "phase", Data: map[string]any{
						"name": "waiting_provider", "provider": providerName,
					}})
					return
				}
				emit(AgentEvent{Type: "phase", Data: map[string]any{
					"name": "preparing", "progress": percent, "provider": providerName,
					"uploaded_bytes": uploaded, "total_bytes": total,
				}})
			},
		)
		if err != nil {
			clearUnsentBtw()
			if completion.Usage.PromptTokens > 0 || completion.Usage.CompletionTokens > 0 ||
				completion.Usage.PromptTokensCached > 0 || completion.Usage.PromptTokensWritten > 0 {
				price := provider.LookupPrice(a.Model)
				turnCost := price.CalculateCost(
					completion.Usage.PromptTokens,
					completion.Usage.CompletionTokens,
					completion.Usage.PromptTokensCached,
				)
				emit(AgentEvent{Type: "usage", Data: map[string]any{
					"prompt":           completion.Usage.PromptTokens,
					"completion":       completion.Usage.CompletionTokens,
					"prompt_cached":    completion.Usage.PromptTokensCached,
					"prompt_written":   completion.Usage.PromptTokensWritten,
					"context_window":   ctxWindow,
					"cost":             turnCost,
					"model":            a.Model,
					"estimated":        false,
					"cost_upper_bound": cacheUsageUnknown,
					"partial_or_retry": true,
				}})
			}
			if completion.Content != "" || completion.ReasoningContent != "" {
				appendWire(provider.ChatMessage{
					Role:              "assistant",
					Content:           provider.TextContent(completion.Content),
					ReasoningContent:  completion.ReasoningContent,
					Reasoning:         completion.ReasoningContent,
					ReasoningDuration: completion.ReasoningDuration,
					ProviderItems:     completion.ProviderItems,
				})
			}
			var apiErr *apiCallError
			if errors.As(err, &apiErr) && apiErr.IsCancellation() {
				emit(AgentEvent{Type: "done", Data: map[string]any{"content": "Cancelled.", "reason": "cancelled"}})
				return returnMessages()
			}
			if apiErr != nil && apiErr.Partial && errors.Is(err, io.ErrUnexpectedEOF) &&
				len(completion.ToolCalls) == 0 &&
				(completion.Content != "" || completion.ReasoningContent != "") &&
				continuations < maxAutoContinuations {
				continuations++
				l.Warn("AGENT", "auto_continue_interrupted_stream", "continuation", continuations, "error", err.Error())
				emit(AgentEvent{Type: "retry", Data: map[string]any{
					"attempt": continuations + 1,
					"backoff": float64(0),
				}})
				appendWire(provider.NewWireHiddenMessage(continuePreviousResponse))
				continue
			}
			a.emitError(emit, err)
			return returnMessages()
		}

		promptTokens := completion.Usage.PromptTokens
		completionTokens := completion.Usage.CompletionTokens
		promptTokensCached := completion.Usage.PromptTokensCached
		promptTokensWritten := completion.Usage.PromptTokensWritten
		contextPromptTokens := completion.LastPromptTokens

		estimated := false
		if promptTokens == 0 {
			promptTokens = provider.EstimateMessagesTotal(requestMsgs, a.Model)
			estimated = true
		}
		if contextPromptTokens == 0 {
			contextPromptTokens = provider.EstimateMessagesTotal(requestMsgs, a.Model)
		}
		if completionTokens == 0 {
			completionTokens = provider.EstimateTokens(
				completion.Content+completion.ReasoningContent, a.Model,
			)
			for _, call := range completion.ToolCalls {
				completionTokens += provider.EstimateTokens(
					call.Function.Name+call.Function.Arguments, a.Model,
				)
			}
			estimated = estimated || completionTokens > 0
		}
		totalPromptTokens += promptTokens
		totalCompletionTokens += completionTokens
		lastPromptTokens = contextPromptTokens

		price := provider.LookupPrice(a.Model)
		turnCost := price.CalculateCost(promptTokens, completionTokens, promptTokensCached)
		emit(AgentEvent{Type: "usage", Data: map[string]any{
			"prompt":           promptTokens,
			"completion":       completionTokens,
			"prompt_cached":    promptTokensCached,
			"prompt_written":   promptTokensWritten,
			"context_prompt":   contextPromptTokens,
			"context_window":   ctxWindow,
			"cost":             turnCost,
			"model":            a.Model,
			"estimated":        estimated,
			"cost_upper_bound": cacheUsageUnknown,
		}})

		accumulatedText := completion.Content
		toolCalls := completion.ToolCalls
		finishReason := completion.FinishReason

		a.logger.Info("LLM", "step_done", "text_len", len(accumulatedText), "tool_calls", len(toolCalls), "finish_reason", finishReason, "cumulative_prompt_tokens", totalPromptTokens, "ctx_window", ctxWindow)

		hasToolCalls := len(toolCalls) > 0
		if accumulatedText == "" && !hasToolCalls {
			accumulatedText = lastReply
		}
		if hasToolCalls {
			l.Debug("AGENT", "branch_has_tool_calls", "count", len(toolCalls), "step", step)
		} else {
			l.Debug("AGENT", "branch_text_only", "text_len", len(accumulatedText), "step", step)
		}
		if hasToolCalls && isTruncatedFinishReason(finishReason) {
			a.emitError(emit, fmt.Errorf(
				"provider truncated a tool call response (%s); tools were not executed",
				finishReason,
			))
			return returnMessages()
		}

		assistantMsg := provider.ChatMessage{
			Role:              "assistant",
			Content:           provider.TextContent(accumulatedText),
			ReasoningContent:  completion.ReasoningContent,
			Reasoning:         completion.ReasoningContent,
			ReasoningDuration: completion.ReasoningDuration,
			ProviderItems:     completion.ProviderItems,
		}
		if hasToolCalls {
			assistantMsg.ToolCalls = toolCalls
		}
		appendWire(assistantMsg)

		if !hasToolCalls {
			if isAutoContinuableFinishReason(finishReason) && continuations < maxAutoContinuations {
				continuations++
				l.Warn("AGENT", "auto_continue", "finish_reason", finishReason, "continuation", continuations)
				appendWire(provider.NewWireHiddenMessage(continuePreviousResponse))
				continue
			}
			// A BTW message can arrive while the terminal completion is in
			// flight. Consume it before emitting done so it becomes the next LLM
			// step instead of being stranded when the turn goroutine shuts down.
			if drainBtw() > 0 {
				continuations = 0
				continue
			}
			if a.Depth > 0 && !a.writeReportCalled {
				a.logger.Warn("SUBAGENT", "write_report_reminder", "agent_id", a.AgentID, "depth", a.Depth, "step", step)
				appendWire(provider.ChatMessage{
					Role:    "user",
					Content: provider.TextContent("[SYSTEM] You are about to end your loop without calling write_report. Your work will be LOST — the main agent cannot see your chat messages. You MUST call write_report with a detailed report before ending. Do it now."),
				})
				_ = drainBtw()
				continue
			}
			doneReason := "completed"
			if finishReason != "" && finishReason != "stop" && finishReason != "end_turn" {
				doneReason = finishReason
			}
			if accumulatedText != "" {
				emit(AgentEvent{Type: "done", Data: map[string]any{"content": accumulatedText, "reason": doneReason}})
			} else {
				emit(AgentEvent{Type: "done", Data: map[string]any{"content": "Done.", "reason": doneReason}})
			}
			return returnMessages()
		}
		continuations = 0

		emit(AgentEvent{Type: "phase", Data: "executing"})

		resultCh := a.executeToolCalls(ctx, toolCalls, emit, blockResolveCh, &lastReply, step)

		results := make([]execResult, len(toolCalls))
		receivedResults := make([]bool, len(toolCalls))
	collectResults:
		for received := 0; received < len(toolCalls); received++ {
			select {
			case r := <-resultCh:
				if r.index >= 0 && r.index < len(results) {
					results[r.index] = r
					receivedResults[r.index] = true
				}
			case <-ctx.Done():
				l.Warn("CANCEL", "context_cancelled", "step", step, "reason", ctx.Err().Error())
				break collectResults
			}
		}
		for i := range results {
			if receivedResults[i] {
				continue
			}
			results[i] = execResult{
				index: i,
				id:    toolCalls[i].ID,
				name:  toolCalls[i].Function.Name,
				result: tools.ToolResult{
					Content: "Turn cancelled by user.",
				},
				toolError: true,
			}
		}

		var lastToolResultIdx int
		for _, r := range results {
			toolError := r.toolError || IsToolError(r.result.Content)
			toolMessage := provider.ChatMessage{
				Role:       "tool",
				ToolCallID: r.id,
				Content:    provider.TextContent(r.result.Content),
				ToolError:  toolError,
			}
			if r.index == len(results)-1 {
				toolMessage.CacheControl = &provider.CacheControl{Type: "ephemeral"}
			}
			appendWire(toolMessage)
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
		}

		if lastToolResultIdx > 0 && lastToolResultIdx < len(msgs) {
			l.Debug("CACHE", "breakpoint", "msg_idx", lastToolResultIdx, "role", msgs[lastToolResultIdx].Role)
		}

		if ctx.Err() != nil {
			l.Warn("CANCEL", "turn_cancelled_after_tools", "step", step)
			emit(AgentEvent{Type: "done", Data: map[string]any{"content": "Cancelled.", "reason": "cancelled"}})
			return returnMessages()
		}
		if ctxLimit > 0 && lastPromptTokens > ctxLimit {
			compacted := compactWireEpoch(msgs[1:], activeWindow, a.Model)
			beforeTokens := provider.EstimateMessagesTotal(msgs[1:], a.Model)
			afterTokens := provider.EstimateMessagesTotal(compacted, a.Model)
			if afterTokens < beforeTokens {
				ledger = append(ledger, compacted[0])
				msgs = append([]provider.ChatMessage{msgs[0]}, compacted...)
				guard.Rotate("active_context_compaction")
				_, _ = guard.EnsureEnvelope(a.Model, systemPrompt, allTools, envelopeOptions)
				lastRuntimeStatus = ""
				l.Warn("CACHE", "epoch_rotated", "reason", "active_context_compaction", "epoch", guard.Epoch(), "messages", len(msgs), "prompt_tokens", lastPromptTokens)
				continue
			}
		}

		if isStopConditionMet(stopConditions, steps) {
			reason := "stop_condition_met"
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
			return returnMessages()
		}
	}
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

func isTruncatedFinishReason(reason string) bool {
	switch strings.ToLower(strings.TrimSpace(reason)) {
	case "length", "max_tokens", "max_output_tokens", "model_context_window_exceeded", "incomplete":
		return true
	default:
		return false
	}
}

func isAutoContinuableFinishReason(reason string) bool {
	switch strings.ToLower(strings.TrimSpace(reason)) {
	case "length", "max_tokens", "max_output_tokens", "incomplete":
		return true
	default:
		return false
	}
}
