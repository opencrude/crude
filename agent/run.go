package agent

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	rand2 "math/rand/v2"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"crude/provider"
	"crude/statemachine"
	"crude/tools"
	ttypes "crude/types"
)

const continuePreviousResponse = "Continue the previous response exactly where it stopped. Do not repeat completed content. Continue using tools if the task still requires them."

const recoverPartialToolCall = "The previous provider stream stopped while constructing one or more tool calls. No tool from that response was executed. Re-evaluate the pending step and issue any required tool calls again with complete arguments. Do not assume the partial call or any side effect occurred."

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
	msgs := []provider.ChatMessage{
		{Role: "system", Content: provider.TextContent(systemPrompt), CacheControl: &provider.CacheControl{Type: "ephemeral"}},
	}
	systemHash := sha256.Sum256([]byte(systemPrompt))
	l.Debug("CACHE", "prefix", "position", 0, "len", len(systemPrompt), "sha256", fmt.Sprintf("%x", systemHash[:8]), "protocol", a.Provider.ProtocolID())
	msgs = append(msgs, sessionMessages...)
	frozen := []provider.ChatMessage{msgs[0]}
	frozen = append(frozen, a.FrozenPrefix...)
	prefix, prefixErr := newPrefixVerifier(frozen)
	if prefixErr != nil {
		a.emitError(emit, fmt.Errorf("initialize context prefix verifier: %w", prefixErr))
		return append([]provider.ChatMessage(nil), msgs...)
	}
	archiveMsgs := []provider.ChatMessage{msgs[0]}
	if a.ContextArchive != nil {
		archiveMsgs = append(archiveMsgs, a.ContextArchive...)
	} else {
		archiveMsgs = append(archiveMsgs, sessionMessages...)
	}
	appendWire := func(messages ...provider.ChatMessage) {
		msgs = append(msgs, messages...)
		archiveMsgs = append(archiveMsgs, messages...)
	}
	returnMessages := func() []provider.ChatMessage {
		a.lastActiveContext = append([]provider.ChatMessage(nil), msgs[1:]...)
		return append([]provider.ChatMessage(nil), archiveMsgs...)
	}

	var steps []StepRecord
	var totalPromptTokens, totalCompletionTokens int
	var lastPromptTokens int
	contextUsage := a.contextBaseline

	ctxWindow := a.Provider.ContextWindow(a.Model)
	l.Debug("CTX", "window", "model", a.Model, "ctx_window", ctxWindow, "compaction_percent", a.ContextCompactionPercent)

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
		archiveMsgs = removeMessagesByInternalID(archiveMsgs, admittedBtwIDs)
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
		if a.ContextCompactionPercent > 0 && ctxWindow > 0 {
			activeTokens := contextUsage.projected(msgs, allTools, a.Model)
			threshold := compactionThreshold(ctxWindow, a.ContextCompactionPercent)
			if threshold > 0 && activeTokens >= threshold {
				requestID := fmt.Sprintf("%016x%016x", rand2.Uint64(), rand2.Uint64())
				compactor := a.contextCompactor(ctxWindow)
				outcome, compactErr := compactor.compactWithModel(ctx, msgs, allTools, requestID, activeTokens, "automatic", emit,
					func(compactionRequest []provider.ChatMessage) {
						if err := prefix.accept(compactionRequest); err != nil {
							l.Error("CACHE", "compaction_prefix_accept_failed", "error", err.Error())
						}
						for _, internalID := range admittedBtwIDs {
							emit(AgentEvent{Type: "btw_consumed", Data: map[string]any{"internal_id": internalID}})
						}
						admittedBtwIDs = nil
					})
				if compactErr != nil {
					clearUnsentBtw()
					data := compactionEventData(requestID, "automatic", checkpointRole(a.Depth), outcome.StageIndex, outcome.Completed, activeTokens, 0, ctxWindow)
					data["provider_id"] = outcome.ProviderID
					data["model_id"] = outcome.ModelID
					data["stage_label"] = outcome.StageName
					data["retry"] = outcome.RetryAttempt
					data["error"] = compactErrorText(compactErr)
					if ctx.Err() != nil {
						emit(AgentEvent{Type: "context_compaction_cancelled", Data: data})
						emit(AgentEvent{Type: "done", Data: map[string]any{"content": "Cancelled.", "reason": "cancelled"}})
					} else {
						emit(AgentEvent{Type: "context_compaction_failed", Data: data})
						a.emitError(emit, fmt.Errorf("context compaction failed; original context retained: %w", compactErr))
					}
					return returnMessages()
				}
				if outcome.AfterTokens >= threshold {
					compactErr := fmt.Errorf("compacted context remains above threshold (%d >= %d tokens)", outcome.AfterTokens, threshold)
					data := compactionEventData(requestID, "automatic", outcome.Role, len(compactionStages), len(compactionStages), activeTokens, outcome.AfterTokens, outcome.ContextWindow)
					data["provider_id"] = outcome.ProviderID
					data["model_id"] = outcome.ModelID
					data["error"] = compactErrorText(compactErr)
					emit(AgentEvent{Type: "context_compaction_failed", Data: data})
					a.emitError(emit, fmt.Errorf("context compaction failed; original context retained: %w", compactErr))
					return returnMessages()
				}
				nextMessages := append([]provider.ChatMessage{msgs[0]}, outcome.Messages...)
				commitData := compactionEventData(requestID, "automatic", outcome.Role, len(compactionStages), len(compactionStages), activeTokens, outcome.AfterTokens, outcome.ContextWindow)
				commitData["provider_id"] = outcome.ProviderID
				commitData["model_id"] = outcome.ModelID
				emit(AgentEvent{Type: "context_compaction_committing", Data: commitData})
				if a.CommitCompaction != nil {
					if err := a.CommitCompaction(CompactionCommit{
						Messages: outcome.Messages, BeforeTokens: activeTokens, AfterTokens: outcome.AfterTokens,
						ContextWindow: outcome.ContextWindow, RequestID: requestID, Trigger: "automatic", Role: outcome.Role,
						ProviderID: outcome.ProviderID, ModelID: outcome.ModelID,
					}); err != nil {
						commitData["error"] = compactErrorText(err)
						emit(AgentEvent{Type: "context_compaction_failed", Data: commitData})
						a.emitError(emit, fmt.Errorf("context compaction commit failed; original context retained: %w", err))
						return returnMessages()
					}
				}
				msgs = nextMessages
				emit(AgentEvent{Type: "context_compacted", Data: commitData})
				contextUsage.reset()
				totalPromptTokens += outcome.Usage.PromptTokens
				totalCompletionTokens += outcome.Usage.CompletionTokens
				prefix.reset()
				l.Info("COMPACT", "automatic", "threshold_percent", a.ContextCompactionPercent,
					"active_before", activeTokens, "active_after", outcome.AfterTokens, "active_messages", len(msgs)-1)
				continue
			}
		}

		// A local turn counter is not a durable request identity: it can restart
		// after resume or agent recreation. Revisions reuse this ID; new calls do not.
		requestID := fmt.Sprintf("%016x%016x", rand2.Uint64(), rand2.Uint64())
		emit(AgentEvent{Type: "phase", Data: map[string]any{
			"name": "preparing", "progress": 0, "provider": providerName,
			"request_id": requestID, "turn_generation": a.TurnGeneration,
		}})

		requestMsgs := msgs
		if err := prefix.check(requestMsgs); err != nil {
			a.emitError(emit, fmt.Errorf("refusing mutated context before provider request: %w", err))
			return returnMessages()
		}

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
					"attempt": attempt, "backoff": backoffSeconds,
					"request_id": requestID, "turn_generation": a.TurnGeneration,
				}})
			},
			func() {
				if err := prefix.accept(requestMsgs); err != nil {
					l.Error("CACHE", "prefix_accept_failed", "error", err.Error())
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
						"uploaded_bytes": uploaded, "total_bytes": total,
						"request_id": requestID, "turn_generation": a.TurnGeneration,
					}})
					return
				}
				emit(AgentEvent{Type: "phase", Data: map[string]any{
					"name": "preparing", "progress": percent, "provider": providerName,
					"uploaded_bytes": uploaded, "total_bytes": total,
					"request_id": requestID, "turn_generation": a.TurnGeneration,
				}})
			},
		)
		if err != nil {
			clearUnsentBtw()
			if completion.Usage.PromptTokens > 0 || completion.Usage.CompletionTokens > 0 ||
				completion.Usage.PromptTokensCached > 0 || completion.Usage.PromptTokensWritten > 0 {
				price, costKnown, costSource := provider.LookupPriceKnown(a.Model)
				turnCost := price.CalculateCost(
					completion.Usage.PromptTokens,
					completion.Usage.CompletionTokens,
					completion.Usage.PromptTokensCached,
				)
				emit(AgentEvent{Type: "usage", Data: map[string]any{
					"prompt":              completion.Usage.PromptTokens,
					"completion":          completion.Usage.CompletionTokens,
					"prompt_cached":       completion.Usage.PromptTokensCached,
					"prompt_written":      completion.Usage.PromptTokensWritten,
					"cache_read_known":    completion.Usage.CacheReadKnown,
					"cache_write_known":   completion.Usage.CacheWriteKnown,
					"reasoning":           completion.Usage.ReasoningTokens,
					"reasoning_known":     completion.Usage.ReasoningKnown,
					"reasoning_in_output": completion.Usage.ReasoningInOutput,
					"context_window":      ctxWindow,
					"cost":                turnCost,
					"cost_known":          costKnown,
					"cost_source":         costSource,
					"model":               a.Model,
					"provider":            providerName,
					"request_id":          requestID,
					"request_kind":        "normal",
					"turn_generation":     a.TurnGeneration,
					"reported_at":         time.Now().UnixNano(),
					"estimated":           false,
					"cost_upper_bound":    cacheUsageUnknown,
					"partial_or_retry":    true,
				}})
			}
			if completion.hasOutput() {
				partialMessage := provider.ChatMessage{
					Role:              "assistant",
					Content:           provider.TextContent(completion.Content),
					ReasoningContent:  completion.ReasoningContent,
					Reasoning:         completion.ReasoningContent,
					ReasoningDuration: completion.ReasoningDuration,
					ProviderItems:     completion.ProviderItems,
				}
				if len(completion.ToolCalls) > 0 {
					// A stalled tool-call frame has no valid replay contract. No tool
					// has executed yet, so preserve only ordinary visible content and
					// let the next request re-evaluate the operation from scratch.
					partialMessage.ReasoningContent = ""
					partialMessage.Reasoning = ""
					partialMessage.ProviderItems = nil
				}
				if partialMessage.ContentString() != "" || partialMessage.ReasoningContent != "" || len(partialMessage.ProviderItems) > 0 {
					appendWire(partialMessage)
				}
			}
			var apiErr *apiCallError
			if errors.As(err, &apiErr) && apiErr.IsCancellation() {
				emit(AgentEvent{Type: "done", Data: map[string]any{"content": "Cancelled.", "reason": "cancelled"}})
				return returnMessages()
			}
			if apiErr != nil && apiErr.Partial && isContinuableStreamInterruption(err) &&
				completion.hasOutput() &&
				continuations < maxAutoContinuations {
				continuations++
				recovery := continuePreviousResponse
				reason := "partial_response"
				if len(completion.ToolCalls) > 0 {
					recovery = recoverPartialToolCall
					reason = "partial_tool_call"
					for _, call := range completion.ToolCalls {
						emit(AgentEvent{Type: "tool_result", Data: map[string]any{
							"tool_id": call.ID, "internal_id": call.InternalID,
							"name": call.Function.Name, "error": "true",
							"result": "Provider stream interrupted before execution; this partial call was discarded and will be re-evaluated.",
						}})
					}
				}
				l.Warn("AGENT", "auto_continue_interrupted_stream", "continuation", continuations, "reason", reason, "error", err.Error())
				emit(AgentEvent{Type: "retry", Data: map[string]any{
					"attempt": continuations + 1,
					"backoff": float64(0),
					"reason":  reason,
				}})
				continuation := provider.NewTextMessage("user", "<runtime_continuation reason=\""+reason+"\">"+recovery+"</runtime_continuation>")
				continuation.Origin = provider.OriginContinuation
				continuation.Visibility = provider.VisibilityContextOnly
				appendWire(continuation)
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
		contextUsage.observe(contextPromptTokens, completionTokens, len(requestMsgs))

		price, costKnown, costSource := provider.LookupPriceKnown(a.Model)
		turnCost := price.CalculateCost(promptTokens, completionTokens, promptTokensCached)
		emit(AgentEvent{Type: "usage", Data: map[string]any{
			"prompt":              promptTokens,
			"completion":          completionTokens,
			"prompt_cached":       promptTokensCached,
			"prompt_written":      promptTokensWritten,
			"cache_read_known":    completion.Usage.CacheReadKnown,
			"cache_write_known":   completion.Usage.CacheWriteKnown,
			"reasoning":           completion.Usage.ReasoningTokens,
			"reasoning_known":     completion.Usage.ReasoningKnown,
			"reasoning_in_output": completion.Usage.ReasoningInOutput,
			"context_prompt":      contextPromptTokens,
			"context_window":      ctxWindow,
			"cost":                turnCost,
			"cost_known":          costKnown,
			"cost_source":         costSource,
			"model":               a.Model,
			"provider":            providerName,
			"request_id":          requestID,
			"request_kind":        "normal",
			"turn_generation":     a.TurnGeneration,
			"reported_at":         time.Now().UnixNano(),
			"estimated":           estimated,
			"cost_upper_bound":    cacheUsageUnknown,
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
				continuation := provider.NewTextMessage("user", "<runtime_continuation>"+continuePreviousResponse+"</runtime_continuation>")
				continuation.Origin = provider.OriginContinuation
				continuation.Visibility = provider.VisibilityContextOnly
				appendWire(continuation)
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
				reminder := provider.NewTextMessage("user", "<runtime_report_reminder>You must call write_report with a detailed report before ending. The parent cannot consume unreported child chat.</runtime_report_reminder>")
				reminder.Origin = provider.OriginContinuation
				reminder.Visibility = provider.VisibilityContextOnly
				appendWire(reminder)
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
		for received := 0; received < len(toolCalls); received++ {
			r := <-resultCh
			if r.index >= 0 && r.index < len(results) {
				results[r.index] = r
				receivedResults[r.index] = true
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
			content := provider.TextContent(r.result.Content)
			if len(r.result.Attachments) > 0 {
				parts := []provider.ContentPart{{Type: "text", Text: r.result.Content}}
				parts = append(parts, r.result.Attachments...)
				content = provider.PartsContent(parts)
			}
			toolMessage := provider.ChatMessage{
				Role:       "tool",
				ToolCallID: r.id,
				Content:    content,
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

func isContinuableStreamInterruption(err error) bool {
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var stalled *provider.ProgressStallError
	return errors.As(err, &stalled) && stalled.Phase == "download"
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
