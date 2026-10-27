package agent

import (
	"context"
	"crude/types/accounting"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
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
		a.setRunFailure(prefixErr, rootprompt.RecoveryInterrupted, RecoveryFailureUnknown)
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
	l.Debug("CTX", "window", "model", a.Model, "ctx_window", ctxWindow, "compaction_percent", a.ContextCompactionPercent, "compaction_adaptive", a.ContextCompactionPercent == 0)

	stopConditions := []stopCondition{
		func(steps []StepRecord) bool {
			return hasRepeatedToolCalls(steps, loopDetectWindow, loopDetectRepeats)
		},
	}

	runtimeInput := runtimeCh
	continuations := 0
	finalReportReminders := 0
	finishChecked := false
	interruptionRecoveries := 0
	overflowRecoveries := 0
	continuationWarningSent := false
	overflowPressure := false
	postCompaction := 0
	slot := a.Precompaction
	if slot == nil {
		slot = NewPrecompactionSlot(ctx, emit)
		defer slot.Close()
	}
	planner := slot.plannerFor(a.PriorCheckpointTokens)
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
steps:
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

		if a.RequestBoundary != nil && a.RequestBoundary() {
			providerName = a.providerDisplayName()
			cacheUsageUnknown = a.Provider.ProtocolID() == "ollama"
			l.Info("AGENT", "connection_changed", "step", step, "model", a.Model, "provider", providerName, "effort", a.Provider.ThinkingEffort())
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
		if a.ContextCompactionPercent != CompactionOff && ctxWindow > 0 {
			activeTokens := contextUsage.projected(msgs, allTools, a.Model)
			hardLimit := max(1, ctxWindow-a.outputTokenLimit(ctxWindow, 0)-contextSafetyReserve(ctxWindow))
			fixed := estimateCanonicalRequest(msgs[:1], allTools, a.Model)
			role := checkpointRole(a.Depth)
			// A provider-rejected context gets a full checkpoint; otherwise the
			// recent turns stay verbatim after the checkpoint of older context.
			cut, tailHasHuman := len(msgs), false
			if !overflowPressure {
				cut, tailHasHuman = verbatimTailCut(msgs, role, a.Model)
			}
			tailTokens := 0
			if cut < len(msgs) {
				tailTokens = provider.EstimateMessagesTotal(msgs[cut:], a.Model)
			}
			plan := planner.evaluate(a, activeTokens, fixed, tailTokens, hardLimit, ctxWindow, overflowPressure)
			// A failed or insufficient attempt defers the next one while the
			// context still fits: cheap or unstable compaction models never stop
			// a run that can still make requests, and deferred attempts coalesce
			// into one after real growth, also past an explicit percentage. Only
			// capacity overrides a deferral.
			deferred := planner.deferred(msgs, activeTokens) && activeTokens < hardLimit && !overflowPressure
			if deferred {
				plan.swap, plan.force = false, false
			}
			l.Debug("COMPACT", "plan", "active", activeTokens, "hard_limit", hardLimit, "tail", tailTokens, "prepare", plan.prepare, "swap", plan.swap, "force", plan.force,
				"excess", planner.excess, "units", planner.units, "checkpoint", planner.checkpoint, "growth", planner.maxGrowth,
				"meter_input", contextUsage.inputTokens, "meter_messages", contextUsage.requestMessages, "messages", len(msgs))
			postCompaction = fixed + planner.checkpointEstimate() + tailTokens
			checkpointTarget := max(1, hardLimit-tailTokens-planner.maxGrowth)
			if overflowPressure {
				checkpointTarget = max(1, min(hardLimit, activeTokens/2))
			}
			compactionConnection, compactionModel := a.Provider, a.Model
			if a.CompactionProvider != nil {
				compactionConnection, compactionModel = a.CompactionProvider, a.CompactionProvider.Model
			}
			// A connection that refused concurrent requests cannot prepare in
			// the background beside the agent's own requests.
			serial := provider.SameConnection(compactionConnection, a.Provider) && provider.ConcurrencyRefused(compactionConnection, compactionModel)
			deferCompaction := func(at int) bool {
				if overflowPressure || activeTokens >= hardLimit {
					return false
				}
				planner.deferUntil(msgs, at+max(planner.maxGrowth, ctxWindow/50, 1))
				return true
			}
			// adopt commits a checkpoint of msgs[1:cut] followed by the verbatim
			// tail. begin publishes the full current context as the source
			// first; synchronous compaction already did so before extraction.
			adopt := func(outcome compactOutcome, cut int, requestID, trigger string, begin bool) (bool, error) {
				compacted := append([]provider.ChatMessage(nil), outcome.Messages...)
				if len(compacted) > 0 && compacted[len(compacted)-1].Origin == provider.OriginRuntimeContext {
					continuity, err := a.continuityMessage(ctx, true)
					if err != nil {
						return false, err
					}
					compacted[len(compacted)-1] = continuity
				}
				compacted = append(compacted, decayVerbatimTail(msgs[cut:], a.Model)...)
				// Guidance admitted during the run lived in messages the
				// checkpoint replaces; the system message carries the current
				// complete guidance instead of the run's initial render.
				system := msgs[0]
				if a.guidance != nil {
					system.Content = provider.TextContent(a.guidance.basePrompt + "\n\n" + a.guidance.rendered)
				}
				next := append([]provider.ChatMessage{system}, compacted...)
				after := estimateCanonicalRequest(next, allTools, a.Model)
				if after >= hardLimit || after >= activeTokens {
					return false, fmt.Errorf("checkpoint with recent context does not reduce the next request enough (%d -> %d of %d tokens)", activeTokens, after, hardLimit)
				}
				if begin && a.PrepareCompaction != nil {
					if err := a.PrepareCompaction(CompactionSource{
						Messages: append([]provider.ChatMessage(nil), msgs[1:]...), BeforeTokens: activeTokens,
						ContextWindow: ctxWindow, RequestID: requestID, Trigger: trigger, Role: role,
						AccountID: a.Provider.AccountID, ProviderID: a.Provider.ID, ModelID: a.Model,
					}); err != nil {
						return false, &compactionStorageError{err}
					}
				}
				stageCount := len(rootprompt.CompactionStages())
				if begin {
					// Adopting a prepared checkpoint is a compaction boundary like any other.
					started := compactionEventData(requestID, trigger, role, stageCount, stageCount, activeTokens, 0, ctxWindow)
					started["stage_label"] = "Adopting the checkpoint prepared in the background"
					emit(AgentEvent{Type: "context_compaction_started", Data: started})
				}
				commitData := compactionEventData(requestID, trigger, role, stageCount, stageCount, activeTokens, after, ctxWindow)
				commitData["account_id"], commitData["provider_id"], commitData["model_id"] = outcome.AccountID, outcome.ProviderID, outcome.ModelID
				commitData["verbatim_tokens"] = tailTokensOf(next[len(next)-(len(msgs)-cut):], a.Model)
				emit(AgentEvent{Type: "context_compaction_committing", Data: commitData})
				if a.CommitCompaction != nil {
					if err := a.CommitCompaction(CompactionCommit{
						Messages: compacted, BeforeTokens: activeTokens, AfterTokens: after,
						ContextWindow: ctxWindow, RequestID: requestID, Trigger: trigger, Role: role,
						AccountID: outcome.AccountID, ProviderID: outcome.ProviderID, ModelID: outcome.ModelID,
					}); err != nil {
						commitData["error"] = compactErrorText(err)
						emit(AgentEvent{Type: "context_compaction_failed", Data: commitData})
						return false, &compactionStorageError{err}
					}
				}
				msgs = next
				if !runtimeNoticesAccepted && len(runtimeNoticeIDs) > 0 && a.RuntimeNoticesAccepted != nil {
					a.RuntimeNoticesAccepted()
					runtimeNoticesAccepted = true
				}
				overflowPressure = false
				planner.clearDeferral()
				if after+max(planner.maxGrowth, 1) >= planner.retrigger(a, hardLimit, ctxWindow) {
					// The checkpoint cannot absorb one more step below the
					// boundary that asked for it; compacting again next step
					// would reclaim almost nothing, so the next attempt waits.
					planner.deferUntil(msgs, after+max(planner.maxGrowth, ctxWindow/50, 1))
				}
				emit(AgentEvent{Type: "context_compacted", Data: commitData})
				contextUsage.reset()
				prefix.reset()
				checkpointTokens := estimateCanonicalRequest(append([]provider.ChatMessage{system}, outcome.Messages...), allTools, a.Model) - fixed
				planner.learn(a, outcome.SourceTokens, checkpointTokens, after, outcome.Usage)
				l.Info("COMPACT", trigger, "reason", plan.reason, "active_before", activeTokens, "active_after", after, "verbatim_messages", len(msgs)-1-len(outcome.Messages))
				return true, nil
			}
			stopOnCompaction := func(err error) {
				clearUnsentRuntimeNotices()
				var storage *compactionStorageError
				if errors.As(err, &storage) {
					a.setRunFailure(storage.err, rootprompt.RecoveryInterrupted, RecoveryFailureStorage)
					a.emitError(emit, fmt.Errorf("context compaction commit failed; original context retained: %w", storage.err))
					return
				}
				a.setRunFailure(err, rootprompt.RecoveryInterrupted, RecoveryFailureCompaction)
				a.emitError(emit, fmt.Errorf("context compaction failed; original context retained: %w", err))
			}

			// adoptDecay replaces older re-runnable tool results across the
			// whole context with stubs. It needs no model, so it is the last
			// resort when every compaction model failed at capacity.
			adoptDecay := func(requestID string) (bool, error) {
				decayed := decayVerbatimTail(msgs[1:], a.Model)
				next := append([]provider.ChatMessage{msgs[0]}, decayed...)
				after := estimateCanonicalRequest(next, allTools, a.Model)
				if after >= hardLimit || after >= activeTokens {
					return false, nil
				}
				if a.PrepareCompaction != nil {
					if err := a.PrepareCompaction(CompactionSource{
						Messages: append([]provider.ChatMessage(nil), msgs[1:]...), BeforeTokens: activeTokens,
						ContextWindow: ctxWindow, RequestID: requestID, Trigger: "decay", Role: role,
						AccountID: a.Provider.AccountID, ProviderID: a.Provider.ID, ModelID: a.Model,
					}); err != nil {
						return false, &compactionStorageError{err}
					}
				}
				stageCount := len(rootprompt.CompactionStages())
				data := compactionEventData(requestID, "decay", role, stageCount, stageCount, activeTokens, after, ctxWindow)
				data["account_id"], data["provider_id"], data["model_id"] = a.Provider.AccountID, a.Provider.ID, a.Model
				emit(AgentEvent{Type: "context_compaction_committing", Data: data})
				if a.CommitCompaction != nil {
					if err := a.CommitCompaction(CompactionCommit{
						Messages: decayed, BeforeTokens: activeTokens, AfterTokens: after,
						ContextWindow: ctxWindow, RequestID: requestID, Trigger: "decay", Role: role,
						AccountID: a.Provider.AccountID, ProviderID: a.Provider.ID, ModelID: a.Model,
					}); err != nil {
						return false, &compactionStorageError{err}
					}
				}
				msgs = next
				if !runtimeNoticesAccepted && len(runtimeNoticeIDs) > 0 && a.RuntimeNoticesAccepted != nil {
					a.RuntimeNoticesAccepted()
					runtimeNoticesAccepted = true
				}
				overflowPressure = false
				planner.clearDeferral()
				emit(AgentEvent{Type: "context_compacted", Data: data})
				contextUsage.reset()
				prefix.reset()
				planner.learn(a, 0, 0, after, provider.TokenUsage{})
				return true, nil
			}

			job := slot.current()
			if job != nil && !job.matches(msgs, a.Model) {
				// The context no longer starts with what the job summarized
				// (another checkpoint or a model switch replaced it).
				slot.discard(job)
				job = nil
			}
			if job != nil && plan.force && overflowPressure {
				// The provider rejected this context; only a full checkpoint helps.
				slot.discard(job)
				job = nil
			}
			// finishing is the boundary announced while the run waits for the
			// background job; it must settle even when the job is not adopted.
			var finishing map[string]any
			if job != nil && plan.force && !job.ready() {
				finishing = compactionEventData(job.requestID, "background", role, 0, 0, activeTokens, 0, ctxWindow)
				finishing["stage_label"] = "Finishing the checkpoint prepared in the background"
				emit(AgentEvent{Type: "context_compaction_started", Data: finishing})
				if err := job.wait(ctx); err != nil {
					clearUnsentRuntimeNotices()
					emit(AgentEvent{Type: "context_compaction_cancelled", Data: finishing})
					emit(AgentEvent{Type: "done", Data: map[string]any{"content": "Cancelled.", "reason": "cancelled"}})
					return returnMessages()
				}
			}
			if job != nil && job.ready() && job.err == nil && !deferred && planner.adoptPrepared(a, activeTokens, postCompaction, hardLimit) {
				plan.swap, plan.reason = true, "a prepared checkpoint now costs less than carrying the older context"
			}
			if job != nil && job.ready() && (plan.swap || plan.force) {
				slot.discard(job)
				unusable := job.err
				if job.err == nil {
					adopted, err := adopt(job.outcome, job.cut, job.requestID, "background", true)
					if adopted {
						continue steps
					}
					var storage *compactionStorageError
					if errors.As(err, &storage) {
						stopOnCompaction(err)
						return returnMessages()
					}
					l.Warn("COMPACT", "background_checkpoint_discarded", "error", fmt.Sprint(err))
					unusable = err
				}
				if finishing != nil {
					// The run compacts synchronously next; this boundary records
					// why the prepared checkpoint was not used.
					failed := maps.Clone(finishing)
					failed["deferred"] = true
					failed["error"] = "background checkpoint unusable: " + compactErrorText(unusable) + "; compacting now"
					emit(AgentEvent{Type: "context_compaction_failed", Data: failed})
				}
				job = nil
			}
			if job == nil && plan.prepare && !plan.force && !serial && cut >= 2 && !deferred {
				slot.start(a, msgs, allTools, cut, tailHasHuman, checkpointTarget, ctxWindow, activeTokens-tailTokens)
			}
			if plan.force || serial && plan.swap {
				requestID := fmt.Sprintf("%016x%016x", rand2.Uint64(), rand2.Uint64())
				compactor := a.backgroundCompactor(ctxWindow, checkpointTarget, tailHasHuman)
				if a.PrepareCompaction != nil {
					if err := a.PrepareCompaction(CompactionSource{
						Messages: append([]provider.ChatMessage(nil), msgs[1:]...), BeforeTokens: activeTokens,
						ContextWindow: ctxWindow, RequestID: requestID, Trigger: "automatic", Role: role,
						AccountID: a.Provider.AccountID, ProviderID: a.Provider.ID, ModelID: a.Model,
					}); err != nil {
						stopOnCompaction(&compactionStorageError{fmt.Errorf("persist compaction source: %w", err)})
						return returnMessages()
					}
				}
				// Provider-reported usage covers what local estimates miss
				// (media, reasoning, protocol overhead); the source is never
				// smaller than the reported context minus the verbatim tail.
				source := msgs[:cut]
				sourceTokens := max(estimateCanonicalRequest(source, allTools, a.Model), activeTokens-tailTokens)
				outcome, compactErr := compactor.compactWithModel(ctx, source, allTools, requestID, sourceTokens, "automatic", emit)
				atCapacity := overflowPressure || activeTokens >= hardLimit
				if compactErr != nil && ctx.Err() == nil && atCapacity && compactionModelUnavailable(compactErr) &&
					a.CompactionProvider != nil && !provider.SameConnection(a.CompactionProvider, a.Provider) {
					// Last resort: the next request cannot be sent and the
					// configured compaction connection is unavailable (input
					// limits are already adapted by the extractor). The
					// agent's own connection writes this one checkpoint.
					l.Warn("COMPACT", "compaction_model_fallback", "error", compactErr.Error())
					outcome, compactErr = a.compactorFor(true, ctxWindow, checkpointTarget, tailHasHuman).compactWithModel(ctx, source, allTools, requestID, sourceTokens, "automatic", emit)
				}
				if compactErr == nil {
					adopted, err := adopt(outcome, cut, requestID, "automatic", false)
					if adopted {
						continue steps
					}
					compactErr = err
				}
				var storage *compactionStorageError
				if compactErr != nil && ctx.Err() == nil && atCapacity && !errors.As(compactErr, &storage) {
					// No model could summarize. Old re-runnable reads still
					// make room without one: they are stubbed with how to fetch
					// them again, and everything else stays verbatim.
					if adopted, err := adoptDecay(requestID); adopted {
						l.Warn("COMPACT", "decayed_without_model", "error", compactErr.Error())
						continue steps
					} else if err != nil {
						compactErr = err
					}
				}
				data := compactionEventData(requestID, "automatic", role, outcome.StageIndex, outcome.Completed, activeTokens, 0, ctxWindow)
				data["account_id"], data["provider_id"], data["model_id"] = outcome.AccountID, outcome.ProviderID, outcome.ModelID
				data["stage_label"], data["retry"], data["error"] = outcome.StageName, outcome.RetryAttempt, compactErrorText(compactErr)
				if ctx.Err() != nil {
					clearUnsentRuntimeNotices()
					emit(AgentEvent{Type: "context_compaction_cancelled", Data: data})
					emit(AgentEvent{Type: "done", Data: map[string]any{"content": "Cancelled.", "reason": "cancelled"}})
					return returnMessages()
				}
				if !errors.As(compactErr, &storage) && deferCompaction(activeTokens) {
					data["deferred"] = true
					data["error"] = compactErrorText(compactErr) + "; continuing with the full context and retrying later"
					emit(AgentEvent{Type: "context_compaction_failed", Data: data})
					l.Warn("COMPACT", "automatic_deferred", "active", activeTokens, "retry_at", planner.deferredUntil, "hard_limit", hardLimit, "error", compactErr.Error())
				} else {
					if !errors.As(compactErr, &storage) {
						emit(AgentEvent{Type: "context_compaction_failed", Data: data})
					}
					stopOnCompaction(compactErr)
					return returnMessages()
				}
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
				a.ContextCompactionPercent != CompactionOff && overflowRecoveries < compactionStageRecoveries {
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
				price, costKnown, costSource := a.Provider.RequestPriceKnown(a.Model, completion.Usage.ServiceTier)
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
		planner.observe(a, completion.Usage, contextPromptTokens, postCompaction)

		price, costKnown, costSource := a.Provider.RequestPriceKnown(a.Model, completion.Usage.ServiceTier)
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
			if a.BeforeFinish != nil && !finishChecked {
				finishChecked = true
				if notices := a.BeforeFinish(); len(notices) > 0 {
					for _, notice := range notices {
						appendWire(notice)
					}
					continue
				}
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
			if a.SealRuntimeMessages != nil {
				if err := a.SealRuntimeMessages(ctx); err != nil {
					a.setRunFailure(err, rootprompt.RecoveryInterrupted, RecoveryFailureStorage)
					a.emitError(emit, err)
					return returnMessages()
				}
				queued, err := drainRuntime()
				if err != nil {
					a.emitError(emit, err)
					return returnMessages()
				}
				if queued > 0 {
					if a.ResumeRuntimeMessages != nil {
						a.ResumeRuntimeMessages()
					}
					continue
				}
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

		emit(AgentEvent{Type: "phase", Data: map[string]any{"name": "executing"}})

		results := make([]execResult, len(toolCalls))
		resultMessages := make([]provider.ChatMessage, len(toolCalls))
		resultIDs := make([]string, len(toolCalls))
		for i := range resultIDs {
			resultIDs[i] = fmt.Sprintf("msg-%016x%016x", rand2.Uint64(), rand2.Uint64())
		}
		receivedResults := make([]bool, len(toolCalls))
		partialResults := make(map[int]execResult)
		// Executed, deferred and cancelled calls have the same completion
		// boundary. Materialize the immutable result before marking it received:
		// any sibling's next checkpoint may include it immediately.
		acceptResult := func(result execResult) {
			results[result.index] = result
			message := toolResultMessage(result, len(results))
			message.InternalID = resultIDs[result.index]
			resultMessages[result.index] = message
			receivedResults[result.index] = true
		}
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
		guidanceChanged := false
		if a.guidance != nil {
			guidanceChanged, guidanceErr = a.guidance.loadPaths(ctx, guidanceCallPaths(toolCalls))
		}
		// runIndex maps the calls that execute now to their batch positions.
		runIndex := make([]int, 0, len(toolCalls))
		if guidanceChanged || guidanceErr != nil {
			// No member of a batch may mutate a newly governed scope before the
			// complete instructions have reached a subsequent model request.
			// Pure reads cannot violate them and run now; unreadable guidance
			// defers everything.
			errorText := ""
			if guidanceErr != nil {
				errorText = guidanceErr.Error()
			}
			message := rootprompt.GuidanceDeferredResult(errorText)
			for i, call := range toolCalls {
				if guidanceErr == nil && guidanceSafeRead(call) {
					runIndex = append(runIndex, i)
					continue
				}
				acceptResult(execResult{index: i, id: call.ID, name: call.Function.Name, result: tools.ToolResult{Content: message}, toolError: true})
				requestEmit(AgentEvent{Type: "tool_result", Data: map[string]any{"tool_id": call.ID, "name": call.Function.Name, "result": message, "error": "true"}})
			}
		} else {
			for i := range toolCalls {
				runIndex = append(runIndex, i)
			}
		}
		if len(runIndex) > 0 {
			runCalls := make([]provider.ToolCall, len(runIndex))
			for position, index := range runIndex {
				runCalls[position] = toolCalls[index]
			}
			// A native parallel wrapper is one canonical call. Its completed
			// children must survive while another leaf runs, too. Tool workers
			// enqueue typed snapshots; only this collector writes checkpoints.
			progressCh := make(chan execResult, 32)
			batchIndices := make(map[string]int)
			for _, i := range runIndex {
				if toolCalls[i].Function.Name == "multi_tool_use.parallel" {
					batchIndices[toolCalls[i].ID] = i
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
									result: tools.ToolResult{Content: string(encoded), ContentParts: checkpointContentParts(data), Attachments: checkpointAttachments(data)}, toolError: true}:
								case <-ctx.Done():
								}
							}
						}
					}
				}
				requestEmit(event)
			}
			resultCh := a.executeToolCalls(ctx, runCalls, batchEmit, blockResolveCh, &lastReply, step)
			for received := 0; received < len(runCalls); {
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
					if r.index >= 0 && r.index < len(runIndex) {
						r.index = runIndex[r.index]
					} else {
						r.index = -1
					}
				}
				if r.index >= 0 && r.index < len(results) {
					acceptResult(r)
					// Retain each finished sibling before waiting for the rest.
					// Partial snapshots stay call-ordered and never invent outcomes
					// for tools that are still running.
					checkpointResults()
				}
			}
			if a.guidance != nil && ctx.Err() == nil {
				// Keep a change found before execution: its guidance still has
				// to reach the next request.
				changed, err := a.guidance.loadPaths(ctx, nil)
				guidanceChanged = guidanceChanged || changed
				if guidanceErr == nil {
					guidanceErr = err
				}
			}
		}
		for i := range results {
			if receivedResults[i] {
				continue
			}
			acceptResult(execResult{
				index: i,
				id:    toolCalls[i].ID,
				name:  toolCalls[i].Function.Name,
				result: tools.ToolResult{
					Content: "Turn cancelled by user.",
				},
				toolError: true,
			})
		}

		var lastToolResultIdx int
		// Collection already checkpointed finished siblings. Publish the complete
		// round together instead of temporarily regressing to its first result.
		appendWire(resultMessages...)
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
		// Tool failures, including uncertain effects and ownership rejections,
		// are ordinary tool results: the loop continues and the model decides.
		// A guidance read failure deferred every call of this batch (or will
		// defer the next batch on its pre-check), so it fails closed per call
		// without ending the run.
		if lastToolResultIdx > 0 && lastToolResultIdx < len(msgs) {
			l.Debug("CACHE", "breakpoint", "msg_idx", lastToolResultIdx, "role", msgs[lastToolResultIdx].Role)
		}
		if guidanceErr != nil {
			l.Warn("GUIDANCE", "scope_read_failed", "error", guidanceErr.Error())
		}
		if guidanceChanged && guidanceErr == nil {
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

func checkpointContentParts(data map[string]any) []provider.ContentPart {
	parts, _ := data["checkpoint_content_parts"].([]provider.ContentPart)
	return append([]provider.ContentPart(nil), parts...)
}

func toolResultMessage(result execResult, count int) provider.ChatMessage {
	parts := tools.ResultContentParts(result.result)
	content := provider.PartsContent(parts)
	if len(parts) == 0 {
		content = provider.TextContent("")
	} else if len(parts) == 1 && parts[0].Type == "text" {
		content = provider.TextContent(parts[0].Text)
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
