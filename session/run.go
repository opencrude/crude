package session

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"crude/agent"
	"crude/docconv"
	"crude/fileio"
	"crude/logger"
	"crude/provider"
	"crude/safety"
	"crude/sessiondb"
	ttypes "crude/types"
)

func (s *Session) RunOrchestrator(o *Orchestrator, sender EventSender) {
	l := s.logger
	l.Info("SESSION", "run_start", "orchestrator_id", o.ID)
	s.sender = sender
	if o.subAgentSup != nil {
		o.subAgentSup.SetSender(sender)
		o.subAgentSup.SetOrchestratorID(o.ID)
	}
	defer func() {
		if r := recover(); r != nil {
			l.Error("SESSION", "panic", "error", fmt.Sprintf("%v", r))
			sender.SendAgentEventWithID("error", fmt.Sprintf("Internal error: %v", r), o.ID)
		}
	}()

	go func() {
		l.Debug("MODEL", "models_dev_start")
		metas, err := provider.FetchModelsDevAll()
		if err != nil {
			l.Warn("MODEL", "load_error", "error", err.Error())
			return
		}
		provider.LoadModelMetas(metas)
		l.Info("MODEL", "load_ok", "count", len(metas))
	}()

	asyncPollingStop := make(chan struct{})
	defer close(asyncPollingStop)
	go func() {
		l.Debug("ASYNC", "poll_start", "interval", "1s")
		tick := time.NewTicker(1 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-tick.C:
				cmds := s.asyncMgr.List()
				// Attribute to this orchestrator: the TUI resolves the owning
				// session projection from the ID (empty IDs are dropped by
				// sessionProjectionForAgent, which kept the deck hidden forever).
				sender.SendAgentEventWithID("async_command_status", cmds, o.ID)
			case <-asyncPollingStop:
				l.Debug("ASYNC", "poll_stop")
				return
			}
		}
	}()

	var pendingWakes []WakeEvent
	queueWake := func(wake WakeEvent) bool {
		if wake.Type == WakeShutdown {
			l.Info("SLEEP", "wake_shutdown")
			return false
		}
		if wake.Type != WakeSubAgentDone {
			return true
		}
		if s.isHardCancelled() {
			l.Info("SLEEP", "wake_suppressed_hard_cancel", "source", wake.SourceID)
			return true
		}
		if s.IsAndClearReportConsumed(wake.SourceID) {
			l.Info("SLEEP", "wake_suppressed_already_consumed", "source", wake.SourceID)
			return true
		}
		l.Info("SLEEP", "wake_sub_agent_done", "source", wake.SourceID)
		pendingWakes = append(pendingWakes, wake)
		return true
	}

	for {
		if len(pendingWakes) > 0 && !o.TurnActive() {
			wakeInput := provider.RuntimeInput(provider.OriginSubAgentWake, formatSubAgentWakeBatch(pendingWakes))
			if o.SubmitInput(wakeInput) {
				pendingWakes = pendingWakes[:0]
				request := <-o.inputCh
				s.processTurnOrchestrator(request, o, sender, l)
				continue
			}
		}
		select {
		case <-o.stopCh:
			l.Info("SESSION", "run_exit_closed", "orchestrator_id", o.ID)
			return
		case request := <-o.inputCh:
			if o.closing.Load() {
				l.Info("SESSION", "run_exit_closing", "orchestrator_id", o.ID)
				return
			}
			if s.isHardCancelled() {
				s.clearHardCancel()
				s.drainWakeCh()
			}
			s.processTurnOrchestrator(request, o, sender, l)

		case wake := <-s.wakeCh:
			if o.closing.Load() {
				return
			}
			if !queueWake(wake) {
				return
			}
			for {
				select {
				case wake = <-s.wakeCh:
					if !queueWake(wake) {
						return
					}
				default:
					goto wakesDrained
				}
			}
		wakesDrained:
		}
	}
}

func formatSubAgentWakeBatch(wakes []WakeEvent) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[System notification: %d sub-agent result(s) are ready.]", len(wakes))
	for _, wake := range wakes {
		b.WriteString("\n\n")
		if result, ok := wake.Data.(agent.SubAgentResult); ok {
			b.WriteString(formatSubAgentResult(result))
			continue
		}
		fmt.Fprintf(&b, "Sub-agent %q reached a terminal state, but its structured result was unavailable.", wake.SourceID)
	}
	b.WriteString("\n\nReview every status above. A cancelled or failed sub-agent is not a completed result; retry or continue explicitly as appropriate.")
	return b.String()
}

func (s *Session) processTurnOrchestrator(request turnRequest, o *Orchestrator, sender EventSender, l *logger.Logger) {
	userInput := request.input
	ctx := request.ctx
	turnGeneration := request.generation
	turnFinished := false
	turnFinalState := sessiondb.TurnCompleted
	finishTurn := func() {
		if turnFinished {
			return
		}
		turnFinished = true
		if ctx.Err() != nil {
			turnFinalState = sessiondb.TurnInterrupted
		}
		if s.db != nil {
			if err := sessiondb.FinishAgentTurn(s.db, s.sessionID, o.ID, turnGeneration, turnFinalState); err != nil {
				l.Warn("SESSION", "turn_state_finish_failed", "error", err.Error())
			}
		}
		if cleared := o.endTurn(turnGeneration); len(cleared) > 0 {
			sender.SendAgentEventWithID("btw_cleared", map[string]any{"internal_ids": cleared}, o.ID)
		}
	}
	defer finishTurn()
	if s.db != nil {
		if err := sessiondb.BeginAgentTurn(s.db, s.sessionID, o.ID, turnGeneration, string(userInput.EffectiveOrigin())); err != nil {
			l.Error("SESSION", "turn_state_begin_failed", "error", err.Error())
			sender.SendAgentEventWithID("error", "Failed to persist turn state: "+err.Error(), o.ID)
			return
		}
	}

	// Use per-orchestrator effective config from runtime profile.
	effectiveCfg := s.runtimeProfile.GetEffectiveConfig(o.ID)
	activeCfg := effectiveCfg.GetActiveDefaultCategory()
	provider.MergeModelCap(activeCfg.Model, slices.Contains(activeCfg.ModelModalities, provider.ModalityImage), activeCfg.ModelSupportsTools)
	provider.MergeModelModalities(activeCfg.Model, activeCfg.ModelModalities)
	// The ChatGPT subscription provider authenticates via OAuth, not an API
	// key, so an empty key is fine when OAuth credentials are present.
	if activeCfg.APIKey == "" && activeCfg.OAuth == nil {
		l.Warn("INPUT", "no_api_key")
		sender.SendAgentEventWithID("error", "No API key configured. Press Ctrl+S to open settings.", o.ID)
		return
	}

	s.turnGate.RLock()
	defer s.turnGate.RUnlock()
	s.activeTurns.Add(1)
	defer s.activeTurns.Add(-1)

	s.turnNumber++
	s.toolCallCountMu.Lock()
	s.toolCallCount = 0
	s.toolCallCountMu.Unlock()
	s.msgCounterMu.Lock()
	s.msgCounter++
	currentMsgID := fmt.Sprintf("msg-%d", s.msgCounter)
	s.msgCounterMu.Unlock()
	historyStart, historyEnd := s.historyHooks()
	historyStarted := false
	if historyStart != nil {
		if err := historyStart(ctx, s.sessionID, currentMsgID); err != nil {
			l.Warn("HISTORY", "message_scope_start_failed", "msg_id", currentMsgID, "error", err.Error())
		} else {
			historyStarted = true
		}
	}
	if historyStarted && historyEnd != nil {
		defer func() {
			finishHistory := func() {
				if err := historyEnd(context.Background(), s.sessionID, currentMsgID); err != nil {
					l.Warn("HISTORY", "message_scope_end_failed", "msg_id", currentMsgID, "error", err.Error())
				}
			}
			if o.subAgentSup != nil && o.subAgentSup.Manager().ActiveCount() > 0 {
				go func() { o.subAgentSup.Wait(); finishHistory() }()
				return
			}
			finishHistory()
		}()
	}
	l.Info("INPUT", "received", "len", len(userInput.Text), "refs", len(userInput.ReferenceIDs), "msg_id", currentMsgID, "turn", s.turnNumber)

	inputOrigin := userInput.EffectiveOrigin()
	humanTurn := inputOrigin == provider.OriginHuman
	userMsg, referenceLabels := s.buildUserMessageWithDisplay(userInput, activeCfg.Model)
	userMsg.InternalID = currentMsgID
	userMsg.DisplayContent = userInput.Text
	var requestContextParts []string
	if humanTurn {
		if memoryContext := s.recallMemoryContext(ctx, userInput.Text, userInput.ActivePaths); memoryContext != "" {
			requestContextParts = append(requestContextParts, memoryContext)
		}
		if s.prevContextInjected.CompareAndSwap(false, true) {
			if prevContext := s.previousSessionContext(ctx); prevContext != "" {
				requestContextParts = append(requestContextParts, prevContext)
			}
		}
	}
	var runtimeParts []string
	if status := agent.BuildStateMachineStatus(s.sm, s.sessionID); status != "" {
		runtimeParts = append(runtimeParts, "## Current State\n"+status)
	}
	if status := agent.BuildAsyncCommandStatus(s.asyncMgr); status != "" {
		runtimeParts = append(runtimeParts, status)
	}
	if o.subAgentSup != nil {
		if status := agent.BuildSubAgentStatus(o.subAgentSup.Manager()); status != "" {
			runtimeParts = append(runtimeParts, status)
		}
	}
	if len(runtimeParts) > 0 {
		requestContextParts = append(requestContextParts, "## Runtime Status\n\n"+strings.Join(runtimeParts, "\n\n"))
	}
	var runtimeContext *provider.ChatMessage
	if content := strings.TrimSpace(strings.Join(requestContextParts, "\n\n")); content != "" {
		message := provider.NewTextMessage("user", "<turn_context>\n"+content+"\n</turn_context>")
		message.InternalID = currentMsgID + "-context"
		message.Origin = provider.OriginRuntimeContext
		message.Visibility = provider.VisibilityContextOnly
		runtimeContext = &message
	}

	if s.db != nil && humanTurn {
		_ = sessiondb.UpdateFirstPrompt(s.db, s.sessionID, userInput.Text)
		_ = sessiondb.UpdateLastPrompt(s.db, s.sessionID, userInput.Text)
	}

	s.msgsMu.Lock()
	if s.activeContext == nil {
		s.activeContext = append([]provider.ChatMessage(nil), s.msgs...)
	}
	frozenPrefix := append([]provider.ChatMessage(nil), s.activeContext...)
	if runtimeContext != nil {
		s.msgs = append(s.msgs, *runtimeContext)
		s.activeContext = append(s.activeContext, *runtimeContext)
		s.msgTurns = append(s.msgTurns, s.turnNumber)
		s.displayMetas = append(s.displayMetas, sessiondb.DisplayMeta{
			DisplayRole: "hidden_runtime_context", ContextOnly: true,
		})
	}
	s.msgs = append(s.msgs, userMsg)
	s.activeContext = append(s.activeContext, userMsg)
	s.msgTurns = append(s.msgTurns, s.turnNumber)
	userMeta := sessiondb.DisplayMeta{
		DisplayRole:     "user",
		DisplayContent:  userInput.Text,
		ReferenceLabels: referenceLabels,
	}
	if userMsg.EffectiveVisibility() != provider.VisibilityConversation {
		userMeta.DisplayRole = "hidden_runtime_context"
		userMeta.ContextOnly = true
	}
	userMeta = canonicalizeDisplayMeta(userMsg, userMeta, "", len(s.msgs)-1, time.Now())
	s.displayMetas = append(s.displayMetas, userMeta)
	archiveCopy := append([]provider.ChatMessage(nil), s.msgs...)
	contextMsgs := append([]provider.ChatMessage(nil), s.activeContext...)
	s.msgsMu.Unlock()
	l.Info("CONV", "user_msg", "msg_id", currentMsgID, "len", len(userInput.Text))

	l.Info("AGENT", "loop_start", "msg_id", currentMsgID)

	if s.editor != nil {
		s.editor = s.editor.WithMessage(currentMsgID)
	}

	turnProvider, err := effectiveCfg.providerForCategory(activeCfg)
	if err != nil {
		l.Error("AGENT", "provider_config_error", "error", err.Error())
		sender.SendAgentEventWithID("error", "Invalid provider communication standard: "+err.Error(), o.ID)
		return
	}
	compactionCfg := effectiveCfg.GetCategoryConfig(CategoryAeron)
	compactionProvider, err := effectiveCfg.providerForCategory(compactionCfg)
	if err != nil {
		l.Error("AGENT", "compaction_provider_config_error", "error", err.Error())
		sender.SendAgentEventWithID("error", "Invalid Aeron compaction provider: "+err.Error(), o.ID)
		return
	}
	turnIdentity := ttypes.TurnIdentity{
		Generation: turnGeneration,
		ProviderID: turnProvider.ID,
		ModelID:    turnProvider.Model,
	}
	sender.SendAgentEventWithID("turn_started", turnIdentity, o.ID)
	ag := s.newAgent(turnProvider, effectiveCfg.IsMultiAgentEnabled(), activeCfg.ModelModalities)
	ag.CompactionProvider = compactionProvider
	ag.AgentID = agent.AgentID(o.ID)
	ag.TurnGeneration = turnGeneration
	ag.PrepareCompaction = func(source agent.CompactionSource) error {
		return s.beginRootCompaction(o.ID, source)
	}
	ag.CommitCompaction = func(commit agent.CompactionCommit) error {
		return s.commitRootCompaction(o.ID, commit)
	}
	if activeCfg.ContextCompactionPercent != nil {
		ag.ContextCompactionPercent = *activeCfg.ContextCompactionPercent
	}
	ag.ContextArchive = archiveCopy
	ag.FrozenPrefix = frozenPrefix
	if o.subAgentSup != nil {
		o.subAgentSup.SetParentTurnGeneration(turnGeneration)
		o.subAgentSup.SetParentToolFn(ag.ToolFn)
		ag.SubAgentMgr = o.subAgentSup.Manager()
	}
	ag.SetToolCallSeqStart(s.toolCallSeq)
	var finalContent string
	prompt := s.systemPrompt(effectiveCfg.IsMultiAgentEnabled())

	displayTracker := &displayMetaTracker{
		metas: make(map[string]*sessiondb.DisplayMeta),
	}

	ag.Ctx = ctx
	activeCfg = effectiveCfg.GetActiveDefaultCategory()

	btwMsgCh := make(chan agent.BtwMessage, 8)
	btwForwardDone := make(chan struct{})
	go func() {
		defer close(btwForwardDone)
		defer close(btwMsgCh)
		for {
			select {
			case <-o.stopCh:
				return
			case btwInput := <-o.btwCh:
				btwMsg := s.buildBtwMessage(btwInput, activeCfg.Model)
				internalID := btwMsg.InternalID
				select {
				case btwMsgCh <- agent.BtwMessage{
					Message: btwMsg,
					Accept: func() bool {
						return o.consumeBtw(internalID)
					},
				}:
				case <-ctx.Done():
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	var emitActive atomic.Bool
	emitActive.Store(true)

	emit := func(evt agent.AgentEvent) {
		if evt.Type == "error" && ctx.Err() == nil {
			turnFinalState = sessiondb.TurnFailed
		}
		if !emitActive.Load() {
			return
		}
		if evt.Type == "blocking" {
			data, _ := evt.Data.(map[string]string)
			if o.handleBlockingEvent(data, evt, sender) {
				return
			}
		}
		if evt.Type == "confirmation_always" {
			if data, ok := evt.Data.(map[string]string); ok {
				s.handleConfirmationAlways(data)
			}
			return
		}
		agentID := evt.AgentID
		if agentID == "" {
			agentID = o.ID
		}
		if err := s.persistCompactionEvent(s.db, s.sessionID, agentID, evt); err != nil {
			s.logger.Warn("COMPACT", "lifecycle_persist_failed", "agent_id", agentID, "error", err.Error())
		}
		sender.SendAgentEventWithID(evt.Type, evt.Data, agentID)
		s.updateDisplayFromEvent(evt, displayTracker)
		if evt.Type == "usage" {
			s.processUsageEvent(evt.Data, activeCfg.Model, agentID, "")
		}
		if evt.Type == "done" {
			if content, ok := evt.Data.(string); ok {
				finalContent = content
			} else if m, ok := evt.Data.(map[string]any); ok {
				if c, ok := m["content"].(string); ok {
					finalContent = c
				}
			}
		}
	}
	ag.Emit = emit
	agentMsgs := ag.Run(ctx, contextMsgs, prompt, emit, o.BlockResolveCh, s.sm, s.sessionID, btwMsgCh)
	finishTurn()
	<-btwForwardDone
	emitActive.Store(false)
	s.toolCallSeq = ag.ToolCallSeq()

	l.Info("AGENT", "loop_end", "len", len(finalContent))
	s.finalizeTurn(agentMsgs, len(archiveCopy), displayTracker, finalContent, currentMsgID, turnIdentity, ag.ActiveContext())
}

func (o *Orchestrator) handleBlockingEvent(data map[string]string, evt agent.AgentEvent, sender EventSender) bool {
	s := o.session
	if data != nil {
		op := safety.CRUDEOp(data["operation"])
		tool := data["tool"]
		target := data["target"]
		if s.permStore.IsAlwaysAllowed(op, tool, target) {
			s.logger.Debug("PERM", "auto_allowed", "operation", op, "tool", tool, "target", target)
			o.ResolveBlock(ttypes.BlockResponse{RequestID: data["block_id"], Outcome: ttypes.BlockAnswered, Value: "allow"})
			agentID := evt.AgentID
			if agentID == "" {
				agentID = o.ID
			}
			sender.SendAgentEventWithID("blocking_auto", evt.Data, agentID)
			return true
		}
	}
	if data != nil && data["risk"] != "" {
		riskStr := data["risk"]
		for _, r := range []safety.CommandRisk{
			safety.RiskDestructive, safety.RiskNetwork,
			safety.RiskProjectWrite, safety.RiskUnknown,
		} {
			if r.String() == riskStr {
				sr := &safety.CommandSafetyResult{Risk: r, BaseCommand: data["tool"]}
				if s.permStore.IsCommandAllowed(sr) {
					s.logger.Debug("PERM", "command_auto_allowed", "risk", riskStr, "tool", data["tool"])
					o.ResolveBlock(ttypes.BlockResponse{RequestID: data["block_id"], Outcome: ttypes.BlockAnswered, Value: "always"})
					agentID := evt.AgentID
					if agentID == "" {
						agentID = o.ID
					}
					sender.SendAgentEventWithID("blocking_auto", evt.Data, agentID)
					return true
				}
				break
			}
		}
	}
	s.logger.Info("BLOCK", "prompt", "operation", data["operation"], "tool", data["tool"], "target", data["target"], "question", data["question"])
	agentID := evt.AgentID
	if agentID == "" {
		agentID = o.ID
	}
	sender.SendAgentEventWithID(evt.Type, evt.Data, agentID)
	return true
}

func (s *Session) handleConfirmationAlways(data map[string]string) {
	opStr := data["operation"]
	tool := data["tool"]
	target := data["target"]
	s.logger.Info("PERM", "grant", "operation", opStr, "tool", tool, "target", target)

	switch tool {
	case "write_file", "edit_file", "delete_file", "move_file", "copy_file":
		s.permStore.GrantAlways(safety.CRUDEOp(opStr), tool, target)
	default:
		for _, r := range []safety.CommandRisk{
			safety.RiskDestructive, safety.RiskNetwork,
			safety.RiskProjectWrite, safety.RiskUnknown,
		} {
			if r.String() == opStr {
				s.permStore.GrantCommandRisk(r)
				s.logger.Debug("PERM", "risk_granted", "risk", opStr)
				break
			}
		}
	}
}

func (s *Session) processUsageEvent(evtData any, model, agentID, parentAgentID string) {
	record, ok := sessiondb.UsageRecordFromEvent(evtData, s.sessionID, agentID, parentAgentID, model)
	if !ok {
		return
	}
	s.usageMu.Lock()
	previous, exists := s.usageRecords[record.RequestID]
	if exists && previous.UpdatedAt >= record.UpdatedAt {
		s.usageMu.Unlock()
		return
	}
	if s.usageRecords == nil {
		s.usageRecords = make(map[string]sessiondb.UsageRecord)
	}
	s.usageRecords[record.RequestID] = record
	s.usageMu.Unlock()
	if err := sessiondb.UpsertUsageRecord(s.db, record); err != nil {
		s.logger.Warn("COST", "request_usage_persist_failed", "request_id", record.RequestID, "error", err.Error())
	}
	if !record.Estimated && !exists && s.tokens != nil {
		s.tokens.Record(record.PromptTokens, record.CompletionTokens)
	}
	s.logger.Info("COST", "turn_usage", "request_id", record.RequestID, "model", record.ModelID,
		"prompt", record.PromptTokens, "completion", record.CompletionTokens, "cached", record.CachedTokens,
		"turn_cost", record.CostUSD, "session_total", s.CostSnapshot().TotalCost, "estimated", record.Estimated)
}

func (s *Session) updateDisplayFromEvent(evt agent.AgentEvent, tracker *displayMetaTracker) {
	switch evt.Type {
	case "tool_call_streaming", "tool_call":
		if data, ok := evt.Data.(map[string]any); ok {
			toolID, _ := data["tool_id"].(string)
			name, _ := data["name"].(string)
			if name != "" && name != "reply" && toolID != "" {
				args, _ := data["args"].(string)
				tracker.updateToolCall(toolID, name, args)
				label, _ := data["target"].(string)
				if label == "" {
					label, _ = data["path"].(string)
				}
				if label != "" {
					tracker.updateToolProgress(toolID, name, "streaming", "", label,
						eventInt(data["content_lines_int"]), 0, eventInt(data["est_tokens_int"]))
				}
			}
		}
	case "tool_result":
		if data, ok := evt.Data.(map[string]any); ok {
			toolID, _ := data["tool_id"].(string)
			if toolID != "" {
				name, _ := data["name"].(string)
				result, _ := data["result"].(string)
				errFlag, _ := data["error"].(string)
				status := "success"
				if errFlag == "true" {
					status = "error"
				}
				tracker.updateToolResult(toolID, name, result, status, int64(eventInt(data["tool_dur_ms"])),
					eventInt(data["tool_est_tokens"]), eventInt(data["lines_added"]), eventInt(data["lines_removed"]))
			}
		}
	case "tool_progress":
		if data, ok := evt.Data.(map[string]any); ok {
			toolID, _ := data["tool_id"].(string)
			if toolID == "" {
				return
			}
			name, _ := data["name"].(string)
			phase, _ := data["phase"].(string)
			detail, _ := data["detail"].(string)
			label, _ := data["path"].(string)
			tracker.updateToolProgress(toolID, name, phase, detail, label,
				eventInt(data["lines_added"]), eventInt(data["lines_removed"]), eventInt(data["tool_est_tokens"]))
		}
	case "blocking_auto":
		if data, ok := evt.Data.(map[string]any); ok {
			tool, _ := data["tool"].(string)
			tracker.setPendingPermTag(tool, "auto")
		} else if data, ok := evt.Data.(map[string]string); ok {
			tool := data["tool"]
			tracker.setPendingPermTag(tool, "auto")
		}
	case "blocking":
		if data, ok := evt.Data.(map[string]string); ok {
			tracker.setPendingPermTag(data["tool"], "")
		}
	case "confirmation_always":
		if data, ok := evt.Data.(map[string]string); ok {
			tracker.setPendingPermTag(data["tool"], "always")
		}
	case "permission_resolved":
		if data, ok := evt.Data.(map[string]any); ok {
			toolID, _ := data["tool_id"].(string)
			tool, _ := data["tool"].(string)
			decision, _ := data["decision"].(string)
			tracker.setPermTag(toolID, tool, decision)
		}
	}
}

func eventInt(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	default:
		return 0
	}
}

func (s *Session) finalizeTurn(
	agentMsgs []provider.ChatMessage,
	contextPrefixLen int,
	tracker *displayMetaTracker,
	finalContent string,
	msgID string,
	turnIdentity ttypes.TurnIdentity,
	activeContext ...[]provider.ChatMessage,
) {
	s.msgsMu.Lock()
	returned := append([]provider.ChatMessage(nil), agentMsgs...)
	if len(returned) > 0 && returned[0].Role == "system" {
		returned = returned[1:]
	}
	if contextPrefixLen > len(returned) {
		s.logger.Warn(
			"COMPACT", "prefix_mismatch",
			"expected", contextPrefixLen, "returned", len(returned),
		)
		contextPrefixLen = len(returned)
	}
	additions := append([]provider.ChatMessage(nil), returned[contextPrefixLen:]...)
	additions = sanitizeMessages(additions)
	rawToolArgs := toolCallArgsIndex(returned)

	additionTurns := make([]int, 0, len(additions))
	additionMetas := make([]sessiondb.DisplayMeta, 0, len(additions))
	for i, m := range additions {
		additionTurns = append(additionTurns, s.turnNumber)
		var dm sessiondb.DisplayMeta
		if isProviderControlMessage(m) {
			dm = sessiondb.DisplayMeta{
				SnapshotVersion: sessiondb.ConversationSnapshotVersion,
				DisplayRole:     "hidden_wire_context",
				ContextOnly:     true,
			}
			additionMetas = append(additionMetas, dm)
			continue
		}
		switch m.Role {
		case "tool":
			dm = tracker.get(m.ToolCallID)
			dm.ToolStatus = persistedToolStatus(m, dm.ToolStatus)
		case "assistant":
			dm.DisplayRole = "assistant"
			if m.ReasoningContent != "" || m.Reasoning != "" {
				dm.DisplayRole = "thinking"
				dm.DurationMs = m.ReasoningDuration.Milliseconds()
			}
		case "user":
			dm.DisplayRole = "user"
			dm.DisplayContent = m.DisplayContent
			dm.ReferenceLabels = append([]string(nil), m.ReferenceLabels...)
		case "system":
			dm = sessiondb.DisplayMeta{
				SnapshotVersion: sessiondb.ConversationSnapshotVersion,
				DisplayRole:     "hidden_wire_context",
				ContextOnly:     true,
			}
		}
		if m.Role == "assistant" || m.Role == "tool" {
			dm.TurnGeneration = turnIdentity.Generation
			dm.ProviderID = turnIdentity.ProviderID
			dm.ModelID = turnIdentity.ModelID
		}
		if m.Role != "system" {
			dm = canonicalizeDisplayMeta(m, dm, rawToolArgs[m.ToolCallID], len(s.msgs)+i, time.Now())
		}
		additionMetas = append(additionMetas, dm)
	}

	if len(additions) > 0 {
		s.msgs = append(s.msgs, additions...)
		s.msgTurns = append(s.msgTurns, additionTurns...)
		s.displayMetas = append(s.displayMetas, additionMetas...)
	} else if finalContent != "" {
		message := provider.ChatMessage{Role: "assistant", Content: provider.TextContent(finalContent)}
		s.msgs = append(s.msgs, message)
		returned = append(returned, message)
		s.msgTurns = append(s.msgTurns, s.turnNumber)
		s.displayMetas = append(s.displayMetas, canonicalizeDisplayMeta(message,
			sessiondb.DisplayMeta{
				DisplayRole: "assistant", TurnGeneration: turnIdentity.Generation,
				ProviderID: turnIdentity.ProviderID, ModelID: turnIdentity.ModelID,
			}, "", len(s.msgs)-1, time.Now()))
	}
	nextContext := returned
	if len(activeContext) > 0 && activeContext[0] != nil {
		nextContext = activeContext[0]
	}
	if len(additions) == 0 && finalContent != "" {
		nextContext = append(nextContext, provider.ChatMessage{Role: "assistant", Content: provider.TextContent(finalContent)})
	}
	if s.contextGeneration == 0 {
		s.contextGeneration = 1
	}
	if s.contextReason == "" {
		s.contextReason = "session_start"
	}
	nextCheckpoint := contextCheckpointContent(nextContext)
	if nextCheckpoint != "" {
		if contextCheckpointContent(s.activeContext) != nextCheckpoint {
			s.contextGeneration++
			s.contextReason = "automatic_compaction"
		}
	}
	s.activeContext = append([]provider.ChatMessage(nil), nextContext...)
	contextState := sessiondb.ContextState{
		Generation: s.contextGeneration, Reason: s.contextReason,
		Messages: append([]provider.ChatMessage(nil), s.activeContext...),
	}

	if s.db != nil && len(s.msgs) > 0 {
		s.appendToWriteBuf(s.msgs, s.msgTurns, s.displayMetas)
	}

	turnMsgs := s.messagesForTurnLocked(s.turnNumber)
	turnNumber := s.turnNumber

	s.msgsMu.Unlock()

	if s.db != nil {
		if err := sessiondb.SaveContextState(s.db, s.sessionID, s.sessionID, contextState); err != nil {
			s.logger.Error("COMPACT", "context_state_persist_failed", "error", err.Error())
		}
		s.flushWriteBufSync()
	}

	s.afterTurnMemory(turnNumber, turnMsgs, finalContent)

	if finalContent != "" {
		s.logger.Info("CONV", "assistant_msg", "msg_id", msgID, "len", len(finalContent))
	}
}

func contextCheckpointContent(messages []provider.ChatMessage) string {
	for _, message := range messages {
		if message.EffectiveOrigin() == provider.OriginCheckpoint {
			return message.ContentString()
		}
	}
	return ""
}

func persistedToolStatus(message provider.ChatMessage, current string) string {
	if !message.ToolError {
		return current
	}
	if strings.Contains(strings.ToLower(message.ContentString()), "cancelled") {
		return "cancelled"
	}
	if current == "" {
		return "error"
	}
	return current
}

// messagesForTurnLocked snapshots this turn's messages. Caller holds msgsMu.
func (s *Session) messagesForTurnLocked(turn int) []provider.ChatMessage {
	var out []provider.ChatMessage
	for i, msgTurn := range s.msgTurns {
		if msgTurn == turn && i < len(s.msgs) {
			out = append(out, s.msgs[i])
		}
	}
	return out
}

func (s *Session) buildUserMessage(input provider.UserInput, model string) provider.ChatMessage {
	msg, _ := s.buildUserMessageWithDisplay(input, model)
	return msg
}

func (s *Session) buildUserMessageWithDisplay(input provider.UserInput, model string) (provider.ChatMessage, []string) {
	if len(input.ReferenceIDs) == 0 || s.db == nil {
		return inputMessage(provider.NewTextMessage("user", input.Text), input), nil
	}

	// Unknown custom/alias models should get the attachment and let the provider
	// decide. Treating missing catalog metadata as an explicit "no vision"
	// silently replaced valid images with local paths the model could not read.
	hasVision, visionKnown := provider.VisionCapability(model)

	// Load only the referenced prompt references (filtered query)
	refs, err := sessiondb.LoadPromptReferencesByIDs(s.db, input.ReferenceIDs)
	if err != nil {
		s.logger.Warn("REF", "load_refs_error", "error", err.Error())
		return inputMessage(provider.NewTextMessage("user", input.Text), input), nil
	}

	// Build an order-preserving map keyed by reference ID
	refMap := make(map[string]sessiondb.Reference, len(refs))
	for _, r := range refs {
		refMap[r.ID] = r
	}

	orderedRefs := make([]sessiondb.Reference, 0, len(input.ReferenceIDs))
	referenceLabels := make([]string, 0, len(input.ReferenceIDs))
	promptText := input.Text
	for _, id := range input.ReferenceIDs {
		ref, ok := refMap[id]
		if !ok || !ref.Active() {
			continue
		}
		orderedRefs = append(orderedRefs, ref)
		label := ref.DisplayName()
		if label != "" {
			referenceLabels = append(referenceLabels, label)
			promptText = strings.ReplaceAll(promptText, label, "")
		}
	}

	var parts []provider.ContentPart
	if promptText = strings.TrimSpace(promptText); promptText != "" {
		parts = append(parts, provider.ContentPart{Type: "text", Text: promptText})
	}

	for _, ref := range orderedRefs {
		switch ref.Kind {
		case sessiondb.KindPastedImage:
			if visionKnown && !hasVision {
				parts = append(parts, provider.ContentPart{
					Type: "text",
					Text: fmt.Sprintf(
						"--- %s (image omitted: model does not advertise image input) ---",
						ref.DisplayName(),
					),
				})
				continue
			}
			var dataURL string
			if ref.FilePath != "" {
				// Validate file size before reading
				if warning := provider.FileSizeExceeded(ref.FilePath, provider.MaxImageFileSizeFor(model)); warning != "" {
					s.logger.Warn("REF", "image_too_large", "warning", warning)
					parts = append(parts, provider.ContentPart{
						Type: "text",
						Text: fmt.Sprintf("--- %s (image omitted: too large) ---", ref.DisplayName()),
					})
					continue
				}
				// Read from file (preferred path — no base64 bloat in DB)
				imgData, err := os.ReadFile(ref.FilePath)
				if err != nil {
					s.logger.Warn("REF", "image_read_error", "path", ref.FilePath, "error", err.Error())
					continue
				}
				mime := ref.Mime
				if mime == "" {
					if detected, ok := provider.ImageMimeFromExt(filepath.Ext(ref.FilePath)); ok {
						mime = detected
					}
				}
				if mime == "" {
					mime = "image/png"
				}
				dataURL = "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(imgData)
			} else if ref.Content != "" {
				// Validate inline content size (base64 string length approximates byte size)
				if int64(len(ref.Content)) > provider.MaxInlineContentSizeFor(model) {
					s.logger.Warn("REF", "inline_image_too_large", "content_len", len(ref.Content))
					parts = append(parts, provider.ContentPart{
						Type: "text",
						Text: fmt.Sprintf("--- %s (image omitted: too large) ---", ref.DisplayName()),
					})
					continue
				}
				// Fallback for legacy entries or inline content
				mime := ref.Mime
				if mime == "" {
					mime = "image/png"
				}
				dataURL = "data:" + mime + ";base64," + ref.Content
			}
			if dataURL == "" {
				continue
			}
			parts = append(parts, provider.ContentPart{
				Type:     "image_url",
				ImageURL: &provider.ImageURL{URL: dataURL},
			})
		case sessiondb.KindPastedText:
			parts = append(parts, provider.ContentPart{Type: "text", Text: ref.Content})
		case sessiondb.KindPastedTextFile, sessiondb.KindSelectedFile:
			content := ref.Content
			if content == "" && ref.FilePath != "" {
				// Validate file size before reading
				if warning := provider.FileSizeExceeded(ref.FilePath, provider.MaxTextFileSizeFor(model)); warning != "" {
					s.logger.Warn("REF", "file_too_large", "warning", warning)
					parts = append(parts, provider.ContentPart{
						Type: "text",
						Text: fmt.Sprintf("--- %s (omitted: too large) ---", ref.DisplayName()),
					})
					continue
				}
				ext := strings.ToLower(filepath.Ext(ref.FilePath))
				if docconv.SupportedExt(ext) {
					markdown, metadata, err := docconv.ConvertFile(ref.FilePath)
					if err != nil {
						s.logger.Warn("REF", "document_convert_error", "path", ref.FilePath, "error", err.Error())
						content = fmt.Sprintf(
							"Document %s could not be converted: %s",
							ref.DisplayName(), err.Error(),
						)
					} else {
						content = docconv.Header(ref.FilePath, metadata) + markdown
					}
				} else if prev, perr := fileio.InspectFile(ref.FilePath, provider.MaxTextFileSizeFor(model), fileio.DefaultHexHeadBytes); perr == nil {
					content = prev.ReadableText()
				}
			}
			if content != "" {
				label := ref.DisplayName()
				parts = append(parts, provider.ContentPart{
					Type: "text",
					Text: fmt.Sprintf("--- %s ---\n%s", label, content),
				})
			}
		}
	}

	if len(parts) == 0 {
		return inputMessage(provider.NewTextMessage("user", promptText), input), referenceLabels
	}
	return inputMessage(provider.NewMultiPartMessage("user", parts), input), referenceLabels
}

func inputMessage(message provider.ChatMessage, input provider.UserInput) provider.ChatMessage {
	message.Origin = input.EffectiveOrigin()
	if message.Origin == provider.OriginHuman || message.Origin == provider.OriginBTW {
		message.Visibility = provider.VisibilityConversation
	} else {
		message.Visibility = provider.VisibilityContextOnly
	}
	return message
}

const btwUrgentPrefix = "[USER INTERRUPTION — READ THIS NOW]\nThe user interrupted your work with an important message while you were running. Stop what you're doing, read this carefully, and adjust your approach accordingly. This takes priority over whatever you are doing.\n\n"

// buildBtwMessage creates a ChatMessage for a BTW (by-the-way) interruption.
// It resolves any reference IDs via buildUserMessage and prepends the urgent
// interruption prefix so the LLM prioritises the message.
func (s *Session) buildBtwMessage(input provider.UserInput, model string) provider.ChatMessage {
	msg, referenceLabels := s.buildUserMessageWithDisplay(input, model)
	if msg.Content.IsText() {
		return provider.ChatMessage{
			InternalID:      input.InternalID,
			DisplayContent:  input.Text,
			ReferenceLabels: referenceLabels,
			Role:            "user",
			Origin:          provider.OriginBTW,
			Visibility:      provider.VisibilityConversation,
			Content:         provider.TextContent(btwUrgentPrefix + msg.Content.String()),
		}
	}
	// Multipart: insert the urgent prefix as a text part at the beginning
	prefixPart := provider.ContentPart{Type: "text", Text: btwUrgentPrefix}
	parts := []provider.ContentPart{prefixPart}
	parts = append(parts, msg.Content.Parts()...)
	result := provider.NewMultiPartMessage("user", parts)
	result.InternalID = input.InternalID
	result.DisplayContent = input.Text
	result.ReferenceLabels = referenceLabels
	result.Origin = provider.OriginBTW
	result.Visibility = provider.VisibilityConversation
	return result
}
