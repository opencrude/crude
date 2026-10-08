package agent

import (
	"context"
	"crude/types/accounting"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	rand2 "math/rand/v2"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	rootprompt "crude/prompt"
	"crude/provider"
	"crude/retry"
	"crude/tools"
	ttypes "crude/types"
	"crude/types/reasoning"
)

type RuntimeMessage struct {
	Message provider.ChatMessage
	// Prepare returns immutable, hidden enrichment. An error never permits the
	// original to be sent without that data; Admit may still preserve the input.
	Prepare func(context.Context, []provider.ChatMessage) ([]provider.ChatMessage, error)
	Admit   func([]provider.ChatMessage) error
	Release func(string)
}

func (runtime RuntimeMessage) staged(ctx context.Context, current []provider.ChatMessage) ([]provider.ChatMessage, error) {
	if runtime.Prepare != nil && runtime.Admit == nil {
		return nil, errors.New("runtime input preparation has no durable admission owner")
	}
	messages := []provider.ChatMessage{runtime.Message}
	var err error
	if runtime.Prepare != nil {
		var enrichment []provider.ChatMessage
		enrichment, err = runtime.Prepare(ctx, current)
		messages = append(messages, enrichment...)
	}
	seen := make(map[string]bool, len(current)+len(messages))
	for _, message := range current {
		if message.InternalID != "" {
			seen[message.InternalID] = true
		}
	}
	delta := messages[:0]
	for _, message := range messages {
		if message.InternalID != "" && seen[message.InternalID] {
			continue
		}
		delta = append(delta, message)
		if message.InternalID != "" {
			seen[message.InternalID] = true
		}
	}
	return delta, err
}

func (a *Agent) Run(
	ctx context.Context,
	sessionMessages []provider.ChatMessage,
	systemPrompt string,
	emit func(AgentEvent),
	blockResolveCh <-chan ttypes.BlockResponse,
	sessionID string,
	runtimeCh <-chan RuntimeMessage,
) []provider.ChatMessage {
	if ctx == nil {
		ctx = context.Background()
	}
	if emit == nil {
		emit = func(AgentEvent) {}
	}
	l := a.logger
	a.runFailure = nil
	a.runFailureKind = ""
	a.runRecoveryKind = ""
	a.lastActiveContext = append([]provider.ChatMessage(nil), sessionMessages...)
	a.lastArchive = append([]provider.ChatMessage(nil), a.ContextArchive...)
	if a.ContextArchive == nil {
		a.lastArchive = append([]provider.ChatMessage(nil), sessionMessages...)
	}
	if ctx.Err() != nil && runtimeCh != nil {
		for {
			select {
			case runtime, ok := <-runtimeCh:
				if !ok {
					runtimeCh = nil
					continue
				}
				staged, prepareErr := runtime.staged(ctx, sessionMessages)
				prospective := append(append([]provider.ChatMessage(nil), sessionMessages...), staged...)
				if runtime.Admit != nil {
					if err := runtime.Admit(prospective); err != nil {
						if runtime.Release != nil {
							runtime.Release("context admission failed")
						}
						continue
					}
				}
				sessionMessages = append(sessionMessages, staged...)
				a.lastActiveContext = append([]provider.ChatMessage(nil), sessionMessages...)
				a.lastArchive = append(a.lastArchive, staged...)
				if prepareErr != nil && runtime.Release != nil {
					runtime.Release("human steering preparation failed")
				}
			default:
				return append([]provider.ChatMessage(nil), a.lastArchive...)
			}
		}
	}
	if !a.preserveReportState {
		a.reportPublished = false
	}
	var emitActive atomic.Bool
	emitActive.Store(true)
	origEmit := emit
	stopID := fmt.Sprintf("turn-stopped:%016x", rand2.Uint64())
	stopped := false
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
			if data, ok := evt.Data.(map[string]any); ok && data["reason"] == "cancelled" {
				data["internal_id"] = stopID
				stopped = true
			}
			emitActive.Store(false)
		}
		origEmit(evt)
	}
	if a.Provider == nil {
		a.setRunFailure(fmt.Errorf("agent provider is nil"), rootprompt.RecoveryInterrupted, RecoveryFailureConfiguration)
		a.emitError(emit, fmt.Errorf("agent provider is nil"))
		return a.ArchiveContext()
	}
	var guidanceErr error
	systemPrompt, guidanceErr = a.loadProjectGuidance(ctx, systemPrompt)
	if guidanceErr != nil {
		a.setRunFailure(guidanceErr, rootprompt.RecoveryInterrupted, RecoveryFailureGuidance)
		a.emitError(emit, fmt.Errorf("cannot start project work before reading complete guidance: %w", guidanceErr))
		return a.ArchiveContext()
	}

	a.configureModelTools()
	allTools := toolDefsToProviderDefs(a.FilteredTools)
	if a.FilteredTools == nil {
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
	var checkpointErr error
	appendWire := func(messages ...provider.ChatMessage) {
		for i := range messages {
			if messages[i].InternalID == "" {
				messages[i].InternalID = fmt.Sprintf("msg-%016x%016x", rand2.Uint64(), rand2.Uint64())
			}
		}
		msgs = append(msgs, messages...)
		archiveMsgs = append(archiveMsgs, messages...)
		a.lastActiveContext = append([]provider.ChatMessage(nil), msgs[1:]...)
		a.lastArchive = append([]provider.ChatMessage(nil), archiveMsgs...)
		if checkpointErr == nil && a.Checkpoint != nil {
			checkpointErr = a.Checkpoint(
				append([]provider.ChatMessage(nil), archiveMsgs...),
				append([]provider.ChatMessage(nil), msgs[1:]...),
			)
		}
	}
	returnMessages := func() []provider.ChatMessage {
		if stopped {
			boundary := provider.NewTextMessage("user", rootprompt.TurnStoppedBoundary())
			boundary.Origin, boundary.Visibility = provider.OriginTurnStopped, provider.VisibilityContextOnly
			boundary.InternalID = stopID
			appendWire(boundary)
			stopped = false
		}
		a.lastActiveContext = append([]provider.ChatMessage(nil), msgs[1:]...)
		a.lastArchive = append([]provider.ChatMessage(nil), archiveMsgs...)
		return append([]provider.ChatMessage(nil), archiveMsgs...)
	}
	if a.WorkDir != "" || a.AsyncMgr != nil || a.SubAgentMgr != nil || a.ContinuationContext != nil {
		continuity, err := a.continuityMessage(ctx, false)
		if err != nil {
			a.setRunFailure(err, rootprompt.RecoveryInterrupted, RecoveryFailureStorage)
			a.emitError(emit, err)
			return returnMessages()
		}
		appendWire(continuity)
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

	runtimeInput := runtimeCh
	continuations := 0
	finalReportReminders := 0
	interruptionRecoveries := 0
	overflowRecoveries := 0
	continuationWarningSent := false
	overflowPressure := false
	providerName := a.providerDisplayName()
	cacheUsageUnknown := a.Provider.ProtocolID() == "ollama"
	var bufferedRuntime []RuntimeMessage
	admitRuntime := func(runtime RuntimeMessage) error {
		staged, prepareErr := runtime.staged(ctx, msgs)
		prospective := append(append([]provider.ChatMessage(nil), msgs...), staged...)
		if runtime.Admit != nil {
			if err := runtime.Admit(prospective); err != nil {
				if runtime.Release != nil {
					runtime.Release("context admission failed")
				}
				a.setRunFailure(err, rootprompt.RecoveryInterrupted, RecoveryFailureStorage)
				return err
			}
		}
		if len(staged) > 0 {
			appendWire(staged...)
		}
		if prepareErr != nil {
			if runtime.Release != nil {
				runtime.Release("human steering preparation failed")
			}
			a.setRunFailure(prepareErr, rootprompt.RecoveryInterrupted, RecoveryFailureConfiguration)
			return prepareErr
		}
		return nil
	}
	admitBufferedRuntime := func() error {
		for len(bufferedRuntime) > 0 {
			runtime := bufferedRuntime[0]
			bufferedRuntime = bufferedRuntime[1:]
			if err := admitRuntime(runtime); err != nil {
				return err
			}
		}
		return nil
	}
	defer func() {
		for _, runtime := range bufferedRuntime {
			if runtime.Release != nil {
				runtime.Release("attempt ended before context admission")
			}
		}
	}()
	for step := 0; ; step++ {
		if ctx.Err() != nil {
			_ = admitBufferedRuntime()
			l.Warn("CANCEL", "context_cancelled", "step", step, "reason", ctx.Err().Error())
			emit(AgentEvent{Type: "done", Data: map[string]any{"content": "Cancelled.", "reason": "cancelled"}})
			return returnMessages()
		}
		if checkpointErr != nil {
			a.setRunFailure(checkpointErr, rootprompt.RecoveryInterrupted, RecoveryFailureStorage)
			a.emitError(emit, fmt.Errorf("persist safe run checkpoint: %w", checkpointErr))
			return returnMessages()
		}

		if a.PrepareStep != nil {
			var notices []provider.ChatMessage
			a.FilteredTools, notices = a.PrepareStep()
			a.configureModelTools()
			allTools = toolDefsToProviderDefs(a.FilteredTools)
			for _, notice := range notices {
				appendWire(notice)
			}
		}
		if a.Depth > 0 && !a.reportPublished && findToolDef("publish_report", a.FilteredTools) == nil {
			err := fmt.Errorf("required child report tool publish_report is unavailable; repair the effective tool configuration")
			a.setRunFailure(err, rootprompt.RecoveryInterrupted, RecoveryFailureConfiguration)
			a.emitError(emit, err)
			return returnMessages()
		}

		drainRuntime := func() (int, error) {
			if a.FlushRuntimeMessages != nil {
				if err := a.FlushRuntimeMessages(ctx); err != nil {
					a.setRunFailure(err, rootprompt.RecoveryInterrupted, RecoveryFailureStorage)
					return 0, fmt.Errorf("flush runtime inbox: %w", err)
				}
			}
			consumed := 0
			admit := func(runtime RuntimeMessage) error {
				if err := admitRuntime(runtime); err != nil {
					l.Warn("RUNTIME", "message_admission_failed", "step", step, "error", err.Error())
					return err
				}
				l.Info("RUNTIME", "human_steering_admitted", "step", step, "message_id", runtime.Message.InternalID)
				consumed++
				return nil
			}
			for len(bufferedRuntime) > 0 {
				runtime := bufferedRuntime[0]
				bufferedRuntime = bufferedRuntime[1:]
				if err := admit(runtime); err != nil {
					return consumed, err
				}
			}
			for {
				select {
				case runtime, ok := <-runtimeInput:
					if !ok {
						runtimeInput = nil
						return consumed, nil
					}
					if err := admit(runtime); err != nil {
						return consumed, err
					}
				default:
					return consumed, nil
				}
			}
		}
		if _, err := drainRuntime(); err != nil {
			a.emitError(emit, err)
			return returnMessages()
		}
		var runtimeNoticeIDs []string
		var runtimeNotices []provider.ChatMessage
		if a.PrepareRuntimeNotices != nil {
			runtimeNotices = a.PrepareRuntimeNotices()
		}
		runtimeNoticesAccepted := false
		if len(runtimeNotices) > 0 && a.CommitRuntimeNotices != nil {
			// Admission owns these facts and receipts. Never checkpoint a reserved
			// notice first and then remove that committed message if admission fails.
			prospective := append(append([]provider.ChatMessage(nil), msgs...), runtimeNotices...)
			if err := a.CommitRuntimeNotices(prospective); err != nil {
				a.setRunFailure(err, rootprompt.RecoveryInterrupted, RecoveryFailureStorage)
				a.emitError(emit, fmt.Errorf("runtime notice context admission failed: %w", err))
				return returnMessages()
			}
			runtimeNoticesAccepted = true
		}
		if len(runtimeNotices) > 0 {
			appendWire(runtimeNotices...)
		}
		for _, notice := range runtimeNotices {
			runtimeNoticeIDs = append(runtimeNoticeIDs, notice.InternalID)
		}
		if len(runtimeNotices) > 0 && a.Checkpoint != nil && checkpointErr == nil {
			runtimeNoticesAccepted = true
		}
		if runtimeNoticesAccepted && a.RuntimeNoticesAccepted != nil {
			a.RuntimeNoticesAccepted()
		}
		if checkpointErr != nil {
			a.setRunFailure(checkpointErr, rootprompt.RecoveryInterrupted, RecoveryFailureStorage)
			a.emitError(emit, fmt.Errorf("persist admitted runtime boundary: %w", checkpointErr))
			return returnMessages()
		}
		clearUnsentRuntimeNotices := func() {
			if runtimeNoticesAccepted || len(runtimeNoticeIDs) == 0 {
				return
			}
			msgs = removeMessagesByInternalID(msgs, runtimeNoticeIDs)
			archiveMsgs = removeMessagesByInternalID(archiveMsgs, runtimeNoticeIDs)
		}
		ctxWindow = a.Provider.ContextWindow(a.Model)
		if a.ContextCompactionPercent > 0 && ctxWindow > 0 {
			activeTokens := contextUsage.projected(msgs, allTools, a.Model)
			threshold := a.contextThreshold(ctxWindow)
			if threshold > 0 && (activeTokens >= threshold || overflowPressure) {
				requestID := fmt.Sprintf("%016x%016x", rand2.Uint64(), rand2.Uint64())
				compactor := a.contextCompactor(ctxWindow)
				compactor.CompactionTargetTokens = threshold
				if overflowPressure {
					compactor.CompactionTargetTokens = min(threshold, activeTokens/2)
				}
				outcome, compactErr := compactor.compactWithModel(ctx, msgs, allTools, requestID, activeTokens, "automatic", emit)
				if compactErr != nil {
					clearUnsentRuntimeNotices()
					data := compactionEventData(requestID, "automatic", checkpointRole(a.Depth), outcome.StageIndex, outcome.Completed, activeTokens, 0, ctxWindow)
					data["account_id"] = outcome.AccountID
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
						a.setRunFailure(compactErr, rootprompt.RecoveryInterrupted)
						a.emitError(emit, fmt.Errorf("context compaction failed; original context retained: %w", compactErr))
					}
					return returnMessages()
				}
				if outcome.AfterTokens >= threshold {
					clearUnsentRuntimeNotices()
					compactErr := fmt.Errorf("compacted context remains above threshold (%d >= %d tokens)", outcome.AfterTokens, threshold)
					stageCount := len(rootprompt.CompactionStages())
					data := compactionEventData(requestID, "automatic", outcome.Role, stageCount, stageCount, activeTokens, outcome.AfterTokens, outcome.ContextWindow)
					data["account_id"] = outcome.AccountID
					data["provider_id"] = outcome.ProviderID
					data["model_id"] = outcome.ModelID
					data["error"] = compactErrorText(compactErr)
					emit(AgentEvent{Type: "context_compaction_failed", Data: data})
					a.setRunFailure(compactErr, rootprompt.RecoveryInterrupted)
					a.emitError(emit, fmt.Errorf("context compaction failed; original context retained: %w", compactErr))
					return returnMessages()
				}
				nextMessages := append([]provider.ChatMessage{msgs[0]}, outcome.Messages...)
				stageCount := len(rootprompt.CompactionStages())
				commitData := compactionEventData(requestID, "automatic", outcome.Role, stageCount, stageCount, activeTokens, outcome.AfterTokens, outcome.ContextWindow)
				commitData["account_id"] = outcome.AccountID
				commitData["provider_id"] = outcome.ProviderID
				commitData["model_id"] = outcome.ModelID
				emit(AgentEvent{Type: "context_compaction_committing", Data: commitData})
				if a.CommitCompaction != nil {
					if err := a.CommitCompaction(CompactionCommit{
						Messages: outcome.Messages, BeforeTokens: activeTokens, AfterTokens: outcome.AfterTokens,
						ContextWindow: outcome.ContextWindow, RequestID: requestID, Trigger: "automatic", Role: outcome.Role,
						AccountID: outcome.AccountID, ProviderID: outcome.ProviderID, ModelID: outcome.ModelID,
					}); err != nil {
						clearUnsentRuntimeNotices()
						commitData["error"] = compactErrorText(err)
						emit(AgentEvent{Type: "context_compaction_failed", Data: commitData})
						a.setRunFailure(err, rootprompt.RecoveryInterrupted, RecoveryFailureStorage)
						a.emitError(emit, fmt.Errorf("context compaction commit failed; original context retained: %w", err))
						return returnMessages()
					}
				}
				msgs = nextMessages
				if !runtimeNoticesAccepted && len(runtimeNoticeIDs) > 0 && a.RuntimeNoticesAccepted != nil {
					a.RuntimeNoticesAccepted()
					runtimeNoticesAccepted = true
				}
				overflowPressure = false
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
			clearUnsentRuntimeNotices()
			a.setRunFailure(err, rootprompt.RecoveryUncertain, RecoveryFailureUnknown)
			a.emitError(emit, fmt.Errorf("refusing mutated context before provider request: %w", err))
			return returnMessages()
		}

		prevPreviewKeys := make(map[string]string)
		requestCtx, cancelRequest := context.WithCancelCause(ctx)
		uploadController := newRuntimeUploadController(cancelRequest)
		watchRuntime := runtimeInput != nil
		watchStop := make(chan struct{})
		watchDone := make(chan bool, 1)
		if watchRuntime {
			input := runtimeInput
			go func() {
				inputClosed := false
				defer func() { watchDone <- inputClosed }()
				for {
					select {
					case <-watchStop:
						return
					case runtime, ok := <-input:
						if !ok {
							inputClosed = true
							return
						}
						uploadController.Queue(runtime)
					}
				}
			}()
		}
		// Upload notifications may arrive on a transport goroutine after the
		// first streamed output. They must not resurrect a provider-wait row.
		beginOutput := uploadController.OutputStarted
		requestEmit := func(event AgentEvent) {
			if data, ok := event.Data.(map[string]any); ok && strings.HasPrefix(event.Type, "tool_") && data["request_id"] == nil {
				data["request_id"] = requestID
			}
			emit(event)
		}
		toolDelta := a.makeStreamingDeltaCallback(requestEmit, prevPreviewKeys)

		completion, err := a.completeStreamWithOptions(requestCtx, requestMsgs, allTools,
			func(token string) {
				beginOutput()
				emit(AgentEvent{Type: "stream_token", Data: token})
			},
			func(text reasoning.Delta) {
				beginOutput()
				emit(AgentEvent{Type: "thinking", Data: text})
			},
			func(internalID, id, name string) {
				beginOutput()
				requestEmit(AgentEvent{Type: "tool_call_streaming", Data: map[string]any{
					"internal_id": internalID,
					"tool_id":     id,
					"name":        name,
					"preview":     tools.FormatPreview(name, ExtractPreview(id, name, "", a.logger).Params),
				}})
			},
			func(internalID, id, name, args string) { beginOutput(); toolDelta(internalID, id, name, args) },
			func(attempt int, backoffSeconds float64, reason string) {
				limit := a.Provider.DialSettings().MaxAttempts
				if a.MaxRetries > 0 {
					limit = a.MaxRetries
				}
				if limit <= 0 {
					limit = provider.DefaultMaxAttempts
				}
				emit(AgentEvent{Type: "retry", Data: map[string]any{
					"attempt": attempt, "backoff": backoffSeconds, "limit": limit, "reason": reason,
					"request_id": requestID, "turn_generation": a.TurnGeneration,
				}})
			},
			func() {
				uploadController.Accepted()
				if err := prefix.accept(requestMsgs); err != nil {
					l.Error("CACHE", "prefix_accept_failed", "error", err.Error())
				}
				if !runtimeNoticesAccepted && len(runtimeNoticeIDs) > 0 && a.RuntimeNoticesAccepted != nil {
					a.RuntimeNoticesAccepted()
					runtimeNoticesAccepted = true
				}
			},
			func(uploaded, total int64) {
				outputStarted, restarting := uploadController.Progress(uploaded, total)
				if outputStarted || restarting {
					return
				}
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
			completionRequestOptions{inputTokens: contextUsage.projected(requestMsgs, allTools, a.Model)},
		)
		if watchRuntime {
			close(watchStop)
			if <-watchDone {
				runtimeInput = nil
			}
		}
		bufferedRuntime = append(bufferedRuntime, uploadController.TakeQueued()...)
		restartUpload := uploadController.RestartRequested() &&
			errors.Is(context.Cause(requestCtx), errRuntimeUploadRestart)
		cancelRequest(nil)
		if restartUpload && ctx.Err() == nil && errors.Is(err, context.Canceled) {
			if !completion.hasOutput() {
				clearUnsentRuntimeNotices()
				queued, drainErr := drainRuntime()
				if drainErr != nil {
					a.emitError(emit, drainErr)
					return returnMessages()
				}
				l.Info("RUNTIME", "restart_upload_for_human_steering", "step", step, "queued", queued,
					"threshold_percent", runtimeUploadRestartPercent)
				continue
			}
			// Output won the narrow race with cancellation. Preserve it as an
			// accepted partial response and continue from that exact prefix;
			// never replay stochastic output merely to coalesce human steering.
			err = &apiCallError{
				Message: "stream interrupted after partial response while coalescing human steering",
				Partial: true,
				Cause:   io.ErrUnexpectedEOF,
			}
		}
		if err != nil {
			if ctx.Err() == nil && !completion.hasOutput() && provider.IsContextWindowExceeded(err) &&
				a.ContextCompactionPercent > 0 && overflowRecoveries < compactionStageRecoveries {
				overflowRecoveries++
				overflowPressure = true
				clearUnsentRuntimeNotices()
				l.Warn("COMPACT", "provider_context_recovery", "attempt", overflowRecoveries, "error", err.Error())
				continue
			}
			runtimeErr := admitBufferedRuntime()
			clearUnsentRuntimeNotices()
			outcome := accounting.Failed
			if completion.hasOutput() || ctx.Err() != nil {
				outcome = accounting.Interrupted
			}
			if completion.Usage.PromptTokens > 0 || completion.Usage.CompletionTokens > 0 ||
				completion.Usage.PromptTokensCached > 0 || completion.Usage.PromptTokensWritten > 0 {
				price, costKnown, costSource := provider.LookupPriceKnown(a.Model)
				turnCost := price.CalculateCost(
					completion.Usage.PromptTokens,
					completion.Usage.CompletionTokens,
					completion.Usage.PromptTokensCached,
				)
				emit(AgentEvent{Type: "usage", Data: map[string]any{
					"request_outcome":     string(outcome),
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
					"provider":            a.Provider.ID,
					"account_id":          a.Provider.AccountID,
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
					ReasoningDisplay:  completion.ReasoningDisplay,
					ProviderItems:     completion.ProviderItems,
					ReplayScope:       completion.ReplayScope,
					ReplayPrefixHash:  completion.ReplayPrefixHash,
					ReplaySourceModel: completion.ReplaySourceModel,
				}
				if len(completion.ToolCalls) > 0 {
					// A stalled tool-call frame has no valid replay contract. No tool
					// has executed yet, so preserve only ordinary visible content and
					// let the next request re-evaluate the operation from scratch.
					partialMessage.ProviderItems = nil
					partialMessage.ToolCalls = nil
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
			if runtimeErr != nil {
				a.emitError(emit, runtimeErr)
				return returnMessages()
			}
			if !a.preserveReportState && apiErr != nil && apiErr.Partial && provider.Retryable(err) &&
				ctx.Err() == nil && completion.hasOutput() &&
				interruptionRecoveries < 3 {
				interruptionRecoveries++
				recovery := rootprompt.ContinuePreviousResponse()
				reason := "partial_response"
				if len(completion.ToolCalls) > 0 {
					recovery = rootprompt.RecoverPartialToolCall()
					reason = "partial_tool_call"
					for _, call := range completion.ToolCalls {
						emit(AgentEvent{Type: "tool_result", Data: map[string]any{
							"tool_id": call.ID, "internal_id": call.InternalID,
							"name": call.Function.Name, "error": "true", "interrupted": true,
							"result": "Provider stream interrupted before execution; this partial call was discarded and will be re-evaluated.",
						}})
					}
				}
				a.setRunFailure(err, rootprompt.RecoveryKind(reason), RecoveryFailureTransport)
				l.Warn("AGENT", "auto_continue_interrupted_stream", "continuation", interruptionRecoveries, "reason", reason, "error", err.Error())
				delay := retry.Exponential(interruptionRecoveries, time.Second, retry.MaxCooldown)
				emit(AgentEvent{Type: "retry", Data: map[string]any{
					"attempt": interruptionRecoveries, "limit": 4,
					"backoff": delay.Seconds(),
					"reason":  "Connection interrupted; continuing from saved work",
				}})
				continuation := provider.NewTextMessage("user", rootprompt.RuntimeContinuation(reason, recovery))
				continuation.Origin = provider.OriginContinuation
				continuation.Visibility = provider.VisibilityContextOnly
				appendWire(continuation)
				if waitErr := retry.Wait(ctx, delay); waitErr != nil {
					emit(AgentEvent{Type: "done", Data: map[string]any{"content": "Cancelled.", "reason": "cancelled"}})
					return returnMessages()
				}
				continue
			}
			kind := rootprompt.RecoveryTransport
			if completion.hasOutput() {
				kind = rootprompt.RecoveryPartialResponse
				if len(completion.ToolCalls) > 0 {
					kind = rootprompt.RecoveryPartialTool
				}
			}
			a.setRunFailure(err, kind)
			var streamFailure *streamPanicError
			if errors.As(err, &streamFailure) {
				a.setRunFailure(err, rootprompt.RecoveryUncertain, RecoveryFailurePanic)
			}
			a.emitError(emit, err)
			return returnMessages()
		}
		if settled := a.observeReasoningVisibility(&completion); settled != reasoning.Unknown {
			emit(AgentEvent{Type: "reasoning_classified", Data: settled})
		}
		if !continuationWarningSent && (completion.ResponseContinuationReason == "response_not_stored" || completion.ResponseContinuationReason == "response_id_missing") {
			continuationWarningSent = true
			emit(AgentEvent{Type: "response_continuation_unavailable", Data: map[string]any{"reason": completion.ResponseContinuationReason}})
		}

		interruptionRecoveries = 0
		overflowRecoveries = 0
		promptTokens := completion.Usage.PromptTokens
		completionTokens := completion.Usage.CompletionTokens
		promptTokensCached := completion.Usage.PromptTokensCached
		promptTokensWritten := completion.Usage.PromptTokensWritten
		contextPromptTokens := completion.LastPromptTokens

		estimated := false
		if promptTokens == 0 {
			promptTokens = estimateCanonicalRequest(requestMsgs, allTools, a.Model)
			estimated = true
		}
		if contextPromptTokens == 0 {
			contextPromptTokens = estimateCanonicalRequest(requestMsgs, allTools, a.Model)
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
		contextUsage.observe(contextPromptTokens, completionTokens, len(requestMsgs), estimateCanonicalRequest(requestMsgs[:1], allTools, a.Model))

		price, costKnown, costSource := provider.LookupPriceKnown(a.Model)
		turnCost := price.CalculateCost(promptTokens, completionTokens, promptTokensCached)
		emit(AgentEvent{Type: "usage", Data: map[string]any{
			"request_outcome":     string(accounting.Succeeded),
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
			"provider":            a.Provider.ID,
			"account_id":          a.Provider.AccountID,
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
		if hasToolCalls {
			l.Debug("AGENT", "branch_has_tool_calls", "count", len(toolCalls), "step", step)
		} else {
			l.Debug("AGENT", "branch_text_only", "text_len", len(accumulatedText), "step", step)
		}
		if hasToolCalls && isTruncatedFinishReason(finishReason) {
			truncatedErr := fmt.Errorf(
				"provider truncated a tool call response (%s); tools were not executed",
				finishReason,
			)
			partialMessage := provider.ChatMessage{
				Role:              "assistant",
				Content:           provider.TextContent(accumulatedText),
				ReasoningDuration: completion.ReasoningDuration,
				ReasoningDisplay:  completion.ReasoningDisplay,
			}
			if partialMessage.ContentString() != "" {
				appendWire(partialMessage)
			}
			for _, call := range toolCalls {
				emit(AgentEvent{Type: "tool_result", Data: map[string]any{
					"tool_id": call.ID, "internal_id": call.InternalID,
					"name": call.Function.Name, "error": "true", "interrupted": true,
					"result": "Provider output ended before execution; this incomplete call was discarded.",
				}})
			}
			a.setRunFailure(truncatedErr, rootprompt.RecoveryPartialTool, RecoveryFailureTruncated)
			a.emitError(emit, truncatedErr)
			return returnMessages()
		}

		assistantMsg := provider.ChatMessage{
			Role:              "assistant",
			Content:           provider.TextContent(accumulatedText),
			ReasoningContent:  completion.ReasoningContent,
			Reasoning:         completion.ReasoningContent,
			ReasoningDuration: completion.ReasoningDuration,
			ReasoningDisplay:  completion.ReasoningDisplay,
			ProviderItems:     completion.ProviderItems,
			ReplayScope:       completion.ReplayScope,
			ReplayPrefixHash:  completion.ReplayPrefixHash,
			ReplaySourceModel: completion.ReplaySourceModel,
		}
		if hasToolCalls {
			assistantMsg.ToolCalls = toolCalls
		}
		provider.BindResponseReference(&assistantMsg, completion.ResponseReference)
		appendWire(assistantMsg)
		if checkpointErr != nil {
			a.setRunFailure(checkpointErr, rootprompt.RecoveryInterrupted, RecoveryFailureStorage)
			a.emitError(emit, fmt.Errorf("persist assistant boundary before tools: %w", checkpointErr))
			return returnMessages()
		}

		if !hasToolCalls {
			if isAutoContinuableFinishReason(finishReason) && continuations < maxAutoContinuations {
				continuations++
				l.Warn("AGENT", "auto_continue", "finish_reason", finishReason, "continuation", continuations)
				continuation := provider.NewTextMessage("user", rootprompt.RuntimeContinuation("", rootprompt.ContinuePreviousResponse()))
				continuation.Origin = provider.OriginContinuation
				continuation.Visibility = provider.VisibilityContextOnly
				appendWire(continuation)
				continue
			}
			// Human steering can arrive while the terminal completion is in
			// flight. Admit it before emitting done so it becomes the next LLM
			// step instead of being stranded when the turn goroutine shuts down.
			queued, drainErr := drainRuntime()
			if drainErr != nil {
				a.emitError(emit, drainErr)
				return returnMessages()
			}
			if queued > 0 {
				continuations = 0
				continue
			}
			if a.Depth > 0 && !a.reportPublished {
				if finalReportReminders < maxFinalReportReminders {
					finalReportReminders++
					a.logger.Warn("SUBAGENT", "publish_report_reminder", "agent_id", a.AgentID, "depth", a.Depth, "step", step, "attempt", finalReportReminders)
					reminder := provider.NewTextMessage("user", rootprompt.RuntimeReportReminder())
					reminder.Origin = provider.OriginContinuation
					reminder.Visibility = provider.VisibilityContextOnly
					appendWire(reminder)
					continue
				}
				a.logger.Warn("SUBAGENT", "publish_report_contract_failed", "agent_id", a.AgentID, "depth", a.Depth, "step", step)
				emit(AgentEvent{Type: "done", Data: map[string]any{
					"content": accumulatedText,
					"reason":  "missing_final_report",
				}})
				return returnMessages()
			}
			doneReason := "completed"
			if finishReason != "" && finishReason != "stop" && finishReason != "end_turn" {
				doneReason = finishReason
			}
			if accumulatedText != "" {
				emit(AgentEvent{Type: "done", Data: map[string]any{"content": accumulatedText, "reason": doneReason}})
			} else if lastReply != "" {
				emit(AgentEvent{Type: "done", Data: map[string]any{"content": "", "reason": doneReason}})
			} else {
				emit(AgentEvent{Type: "done", Data: map[string]any{"content": "Done.", "reason": doneReason}})
			}
			return returnMessages()
		}
		continuations = 0

		emit(AgentEvent{Type: "phase", Data: "executing"})

		results := make([]execResult, len(toolCalls))
		resultMessages := make([]provider.ChatMessage, len(toolCalls))
		resultIDs := make([]string, len(toolCalls))
		for i := range resultIDs {
			resultIDs[i] = fmt.Sprintf("msg-%016x%016x", rand2.Uint64(), rand2.Uint64())
		}
		receivedResults := make([]bool, len(toolCalls))
		partialResults := make(map[int]execResult)
		checkpointResults := func() {
			if a.Checkpoint == nil && a.CheckpointTools == nil {
				return
			}
			partial := make([]provider.ChatMessage, 0, len(results))
			var inFlight []provider.ChatMessage
			for i, done := range receivedResults {
				if done {
					partial = append(partial, resultMessages[i])
				} else if progress, ok := partialResults[i]; ok {
					message := toolResultMessage(progress, len(results))
					message.InternalID = resultIDs[i] + "-interrupted"
					inFlight = append(inFlight, message)
				}
			}
			a.lastArchive = append(append([]provider.ChatMessage(nil), archiveMsgs...), partial...)
			a.lastActiveContext = append(append([]provider.ChatMessage(nil), msgs[1:]...), partial...)
			if checkpointErr == nil {
				if a.CheckpointTools != nil {
					checkpointErr = a.CheckpointTools(a.ArchiveContext(), a.ActiveContext(), inFlight)
				} else {
					checkpointErr = a.Checkpoint(a.ArchiveContext(), a.ActiveContext())
				}
			}
		}
		var uncertain error
		guidanceChanged := false
		var guidanceOperandErr *guidanceOperandError
		if a.guidance != nil {
			var guidancePaths []string
			guidancePaths, guidanceErr = guidanceCallPaths(toolCalls)
			if guidanceErr == nil {
				guidanceChanged, guidanceErr = a.guidance.loadPaths(ctx, guidancePaths)
			}
			// Invalid literal operands are ordinary local tool errors; they do not
			// mutate the accepted scope set or park the agent for guidance.
			_ = errors.As(guidanceErr, &guidanceOperandErr)
		}
		if guidanceChanged || guidanceErr != nil {
			// No member of a batch may mutate a newly governed scope before the
			// complete instructions have reached a subsequent model request.
			errorText := ""
			if guidanceErr != nil {
				errorText = guidanceErr.Error()
			}
			message := rootprompt.GuidanceDeferredResult(errorText)
			if guidanceOperandErr != nil {
				message = "Error: " + guidanceOperandErr.Error()
			}
			for i, call := range toolCalls {
				results[i] = execResult{index: i, id: call.ID, name: call.Function.Name, result: tools.ToolResult{Content: message}, toolError: true}
				receivedResults[i] = true
				requestEmit(AgentEvent{Type: "tool_result", Data: map[string]any{"tool_id": call.ID, "name": call.Function.Name, "result": message, "error": "true"}})
			}
		} else {
			// A native parallel wrapper is one canonical call. Its completed
			// children must survive while another leaf runs, too. Tool workers
			// enqueue typed snapshots; only this collector writes checkpoints.
			progressCh := make(chan execResult, 32)
			batchIndices := make(map[string]int)
			for i, call := range toolCalls {
				if call.Function.Name == "multi_tool_use.parallel" {
					batchIndices[call.ID] = i
				}
			}
			batchEmit := func(event AgentEvent) {
				if event.Type == "tool_call_streaming" && a.CheckpointTools != nil {
					data, _ := event.Data.(map[string]any)
					id, _ := data["tool_id"].(string)
					content, _ := data["content"].(string)
					if index, ok := batchIndices[id]; ok && content != "" {
						var payload map[string]json.RawMessage
						if json.Unmarshal([]byte(content), &payload) == nil {
							payload["outcome_unknown"] = json.RawMessage("true")
							encoded, encodeErr := json.Marshal(payload)
							if encodeErr == nil {
								select {
								case progressCh <- execResult{index: index, id: id, name: "multi_tool_use.parallel",
									result: tools.ToolResult{Content: string(encoded), Attachments: checkpointAttachments(data)}, toolError: true}:
								case <-ctx.Done():
								}
							}
						}
					}
				}
				requestEmit(event)
			}
			resultCh := a.executeToolCalls(ctx, toolCalls, batchEmit, blockResolveCh, &lastReply, step)
			for received := 0; received < len(toolCalls); {
				var r execResult
				select {
				case progress := <-progressCh:
					if !receivedResults[progress.index] {
						partialResults[progress.index] = progress
						checkpointResults()
					}
					continue
				case r = <-resultCh:
					received++
				}
				if r.index >= 0 && r.index < len(results) {
					results[r.index] = r
					resultMessages[r.index] = toolResultMessage(r, len(results))
					resultMessages[r.index].InternalID = resultIDs[r.index]
					receivedResults[r.index] = true
					if r.failure != nil {
						uncertain = r.failure
					}
					// Retain each finished sibling before waiting for the rest.
					// Partial snapshots stay call-ordered and never invent outcomes
					// for tools that are still running.
					checkpointResults()
				}
			}
			if a.guidance != nil && ctx.Err() == nil {
				guidanceChanged, guidanceErr = a.guidance.loadPaths(ctx, nil)
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
		toolMessages := make([]provider.ChatMessage, 0, len(results))
		for _, result := range results {
			message := resultMessages[result.index]
			if message.InternalID == "" {
				message = toolResultMessage(result, len(results))
				message.InternalID = resultIDs[result.index]
			}
			toolMessages = append(toolMessages, message)
		}
		// Collection already checkpointed finished siblings. Publish the complete
		// round together instead of temporarily regressing to its first result.
		appendWire(toolMessages...)
		for _, r := range results {
			toolError := r.toolError || IsToolError(r.result.Content)
			if a.ToolResultAccepted != nil && !toolError {
				a.ToolResultAccepted(r.name, r.id, append([]provider.ChatMessage(nil), msgs[1:]...))
			}
			lastToolResultIdx = len(msgs) - 1
			if r.name == "reply" && r.result.Content != "" {
				data := map[string]any{"tool_id": r.id, "name": r.name, "result": r.result.Content}
				if toolError {
					data["error"] = "true"
				}
				emit(AgentEvent{Type: "tool_result", Data: data})
			}
			steps = append(steps, StepRecord{
				Content:      accumulatedText,
				ToolName:     r.name,
				ToolInput:    fmt.Sprintf("%v", r.params),
				ToolResult:   r.result.Content,
				FinishReason: finishReason,
			})
		}
		if checkpointErr != nil {
			a.setRunFailure(checkpointErr, rootprompt.RecoveryUncertain, RecoveryFailureStorage)
			a.emitError(emit, fmt.Errorf("persist completed tool boundary: %w", checkpointErr))
			return returnMessages()
		}
		if uncertain != nil {
			var structural *tools.StructuralFailureError
			if errors.As(uncertain, &structural) {
				// Ownership blockers are deterministic and repairable by the
				// parent. They are not uncertain effects and must not enter
				// automatic panic recovery.
				a.setRunFailure(uncertain, rootprompt.RecoveryInterrupted, RecoveryFailureStructural)
			} else {
				a.setRunFailure(uncertain, rootprompt.RecoveryUncertain, RecoveryFailurePanic)
			}
			a.emitError(emit, uncertain)
			return returnMessages()
		}
		if lastToolResultIdx > 0 && lastToolResultIdx < len(msgs) {
			l.Debug("CACHE", "breakpoint", "msg_idx", lastToolResultIdx, "role", msgs[lastToolResultIdx].Role)
		}
		if guidanceErr != nil && guidanceOperandErr == nil {
			a.setRunFailure(guidanceErr, rootprompt.RecoveryInterrupted, RecoveryFailureGuidance)
			a.emitError(emit, fmt.Errorf("complete project guidance is required: %w", guidanceErr))
			return returnMessages()
		}
		if guidanceChanged {
			rendered, refreshErr := a.refreshProjectGuidance(ctx, systemPrompt)
			if refreshErr != nil {
				a.setRunFailure(refreshErr, rootprompt.RecoveryInterrupted, RecoveryFailureGuidance)
				a.emitError(emit, fmt.Errorf("prepare updated project guidance: %w", refreshErr))
				return returnMessages()
			}
			guidance := provider.NewTextMessage("user", rootprompt.GuidanceProjectionUpdate(rendered))
			guidance.Origin, guidance.Visibility = provider.OriginRuntimeContext, provider.VisibilityContextOnly
			appendWire(guidance)
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
			// The latest reply (if any) was delivered as progress. Do not
			// fabricate a second answer when a stop condition ends the turn.
			emit(AgentEvent{Type: "done", Data: map[string]any{"content": "", "reason": reason}})
			return returnMessages()
		}
	}
}

func checkpointAttachments(data map[string]any) []provider.ContentPart {
	attachments, _ := data["checkpoint_attachments"].([]provider.ContentPart)
	return append([]provider.ContentPart(nil), attachments...)
}

func toolResultMessage(result execResult, count int) provider.ChatMessage {
	content := provider.TextContent(result.result.Content)
	if len(result.result.Attachments) > 0 {
		parts := []provider.ContentPart{{Type: "text", Text: result.result.Content}}
		content = provider.PartsContent(append(parts, result.result.Attachments...))
	}
	message := provider.ChatMessage{Role: "tool", ToolCallID: result.id, Content: content,
		ToolError: result.toolError || IsToolError(result.result.Content)}
	if result.index == count-1 {
		message.CacheControl = &provider.CacheControl{Type: "ephemeral"}
	}
	return message
}

func (a *Agent) observeReasoningVisibility(completion *completionResult) reasoning.Kind {
	if completion == nil {
		return reasoning.Unknown
	}
	if completion.ReasoningDisplay.Kind == reasoning.Raw {
		a.ReasoningContentKind, a.condensedSamples, a.rawSamples, a.sawRawReasoning = reasoning.Unknown, 0, 0, true
		return reasoning.Unknown
	}
	if a.sawRawReasoning || completion.ReasoningDisplay.Kind != reasoning.Provisional && completion.ReasoningDisplay.Kind != reasoning.InferredRaw {
		return reasoning.Unknown
	}
	runes := utf8.RuneCountInString(completion.ReasoningContent)
	if reasoning.CondensedByUsage(runes, completion.Usage.ReasoningTokens, completion.Usage.ReasoningKnown) {
		completion.ReasoningDisplay.Kind = reasoning.InferredSummary
		if a.ReasoningContentKind == reasoning.InferredRaw {
			a.ReasoningContentKind = reasoning.Unknown
		}
		a.rawSamples = 0
		a.condensedSamples++
		if a.condensedSamples >= 2 || completion.Usage.ReasoningTokens >= 16*((runes+3)/4) {
			a.ReasoningContentKind = reasoning.InferredSummary
		}
		return reasoning.InferredSummary
	}
	if completion.Usage.ReasoningKnown && runes >= 12 && completion.Usage.ReasoningTokens >= 128 && completion.Usage.ReasoningTokens <= 2*((runes+3)/4) {
		completion.ReasoningDisplay.Kind = reasoning.InferredRaw
		if a.ReasoningContentKind == reasoning.InferredSummary {
			a.ReasoningContentKind = reasoning.Unknown
		}
		a.condensedSamples = 0
		a.rawSamples++
		if a.rawSamples >= 2 {
			a.ReasoningContentKind = reasoning.InferredRaw
		}
		return reasoning.InferredRaw
	}
	return reasoning.Unknown
}

func (a *Agent) makeStreamingDeltaCallback(emit func(AgentEvent), prevPreviewKeys map[string]string) func(internalID, id, name, args string) {
	return func(internalID, id, name, args string) {
		preview := ExtractPreview(id, name, args, a.logger)
		path := getStr(preview.Params, "path")
		command := getStr(preview.Params, "command")
		if name == "exec_command" {
			command = getStr(preview.Params, "cmd")
		}
		urlStr := getStr(preview.Params, "url")
		query := getStr(preview.Params, "query")
		source := getStr(preview.Params, "source")
		dest := getStr(preview.Params, "destination")
		pattern := getStr(preview.Params, "pattern")
		handle := getStr(preview.Params, "handle")
		agentID := getStr(preview.Params, "agent_id")
		runID := getStr(preview.Params, "run_id")
		artifactID := getStr(preview.Params, "artifact_id")
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

		if name == "apply_patch" || name == "write_file" || name == "edit_file" || name == "publish_report" {
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

		previewKey := fmt.Sprintf("%s|%s|%s|%s|%s|%s|%s|%s|%s|%s|%d|%d|%d|%d|%d",
			path, command, urlStr, query, source, dest, pattern, agentID, runID, artifactID,
			offsetVal, limitVal, contentLines, oldLines, newLines)
		if name == "multi_tool_use.parallel" {
			previewKey = args
		}
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
		if name == "multi_tool_use.parallel" && preview.Params["tool_uses"] != nil {
			if input, err := json.Marshal(preview.Params); err == nil {
				data["batch_input"] = string(input)
			}
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
		if agentID != "" {
			data["agent_id"] = agentID
		}
		if runID != "" {
			data["run_id"] = runID
		}
		if artifactID != "" {
			data["artifact_id"] = artifactID
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
