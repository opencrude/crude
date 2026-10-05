package session

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"crude/agent"
	"crude/collaboration"
	"crude/docconv"
	"crude/fileio"
	"crude/logger"
	rootprompt "crude/prompt"
	"crude/provider"
	"crude/safety"
	"crude/sessiondb"
	"crude/tools"
	ttypes "crude/types"
	captypes "crude/types/capability"
	recoverytypes "crude/types/recovery"
)

func (s *Session) RunOrchestrator(o *Orchestrator, sender EventSender) {
	l := s.logger
	l.Info("SESSION", "run_start", "orchestrator_id", o.ID)
	s.sender = sender
	defer s.detachCollaborator(context.Background(), o.ID)
	s.reconcileCollaborationAdmissions(s.db, o.ID)
	if err := s.admitPassiveCollaboration(o, sender); err != nil {
		l.Warn("COLLAB", "passive_recovery_deferred", "agent_id", o.ID, "error", err.Error())
	}

	var collaborationNotifications <-chan struct{}
	unsubscribeCollaboration := func() {}
	if store := s.CollaborationStore(); store != nil {
		collaborationNotifications, unsubscribeCollaboration = store.Subscribe()
	}
	defer unsubscribeCollaboration()
	collaborationPoll := time.NewTicker(500 * time.Millisecond)
	defer collaborationPoll.Stop()
	startCollaborationTurn := func() bool {
		if o.RuntimeEpoch == 0 || o.TurnActive() || o.closing.Load() {
			return false
		}
		deliveries, err := s.reserveCollaboration(context.Background(), o.ID, o.RuntimeEpoch, 1)
		if err != nil {
			if !errors.Is(err, collaboration.ErrUnavailable) {
				l.Warn("COLLAB", "idle_reserve_failed", "agent_id", o.ID, "error", err.Error())
			}
			return false
		}
		if len(deliveries) == 0 {
			return false
		}
		if !o.SubmitCollaboration(collaborationInput(deliveries[0]), deliveries) {
			s.releaseCollaborationReservations(o.ID, o.RuntimeEpoch, deliveries, "target was not idle")
			return false
		}
		return true
	}

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

	for {
		restart := func() (restart bool) {
			defer func() {
				if recovered := recover(); recovered != nil {
					restart = true
					l.Error("SESSION", "input_service_panic", "orchestrator_id", o.ID, "error", fmt.Sprintf("%v", recovered))
				}
			}()
			for {
				select {
				case <-o.stopCh:
					l.Info("SESSION", "run_exit_closed", "orchestrator_id", o.ID)
					return false
				case request := <-o.inputCh:
					if o.closing.Load() {
						l.Info("SESSION", "run_exit_closing", "orchestrator_id", o.ID)
						return false
					}
					if s.isHardCancelled() {
						s.clearHardCancel()
					}
					s.processTurnOrchestrator(request, o, sender, l)
				case <-collaborationNotifications:
					if startCollaborationTurn() {
						request := <-o.inputCh
						s.processTurnOrchestrator(request, o, sender, l)
					}
				case <-collaborationPoll.C:
					if startCollaborationTurn() {
						request := <-o.inputCh
						s.processTurnOrchestrator(request, o, sender, l)
					}
				}
			}
		}()
		if !restart {
			return
		}
		if o.closing.Load() {
			l.Info("SESSION", "run_exit_closing", "orchestrator_id", o.ID)
			return
		}
	}
}

func (s *Session) processTurnOrchestrator(request turnRequest, o *Orchestrator, sender EventSender, l *logger.Logger) {
	userInput := request.input
	ctx := request.ctx
	turnGeneration := request.generation
	recoveryState := newRecoveryRunState(userInput, request.recovery)
	recoveryState.contextOwnerID = s.sessionID
	recoveryState.contextGeneration = max(1, s.contextGeneration)
	recoveryState.contextReason = "run_recovery"
	if request.recovery == nil {
		s.msgsMu.Lock()
		recoveryState.archive = append([]provider.ChatMessage(nil), s.msgs...)
		recoveryState.active = append([]provider.ChatMessage(nil), s.activeContext...)
		s.msgsMu.Unlock()
	}
	var initialRecovery *recoverytypes.Status
	if request.recovery != nil {
		status := request.recovery.Status
		initialRecovery = &status
	}
	supervisor, supervisorErr := s.newRecoverySupervisor(
		s.db, s.sessionID, o.ID, turnGeneration, recoveryState, initialRecovery, sender,
	)
	if supervisorErr != nil {
		l.Warn("RECOVERY", "supervisor_unavailable", "agent_id", o.ID, "error", supervisorErr.Error())
		o.endTurn(turnGeneration)
		return
	}
	currentMsgID := newMessageID()
	if userInput.InternalID != "" {
		currentMsgID = userInput.InternalID
	}
	userInput.InternalID = currentMsgID
	recoveryState.setPending(userInput)
	turnFinished := false
	turnFinalState := sessiondb.TurnFailed
	recoveryParked := false
	var collaborationRun collaboration.Run
	finishTurn := func() error {
		if turnFinished {
			return nil
		}
		if ctx.Err() != nil {
			turnFinalState = sessiondb.TurnInterrupted
		}
		if s.db != nil {
			if err := sessiondb.FinishAgentTurn(s.db, s.sessionID, o.ID, turnGeneration, turnFinalState); err != nil {
				return err
			}
		}
		if collaborationRun.ID != "" {
			runState := collaboration.RunFailed
			outcome := "turn failed"
			switch turnFinalState {
			case sessiondb.TurnCompleted:
				runState, outcome = collaboration.RunSucceeded, "completed"
			case sessiondb.TurnInterrupted:
				runState, outcome = collaboration.RunCancelled, "cancelled"
				if recoveryParked && ctx.Err() == nil {
					runState, outcome = collaboration.RunInterrupted, "recovery pending"
				}
			}
			s.mu.Lock()
			store := s.collaborationStore
			s.mu.Unlock()
			if store != nil {
				_, expectedVersion := o.collaborationRunIdentity()
				if expectedVersion == 0 {
					expectedVersion = collaborationRun.Version
				}
				finished, err := finishCollaborationRun(store, collaboration.FinishRunRequest{
					RunID: collaborationRun.ID, AgentID: o.ID, RuntimeEpoch: o.RuntimeEpoch,
					ExpectedVersion: expectedVersion, State: runState,
					Outcome: outcome, ContextCommitID: currentMsgID,
				})
				if err != nil {
					return err
				} else {
					collaborationRun = finished
				}
			}
		}
		turnFinished = true
		return nil
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			panicErr := fmt.Errorf("session turn panic: %v", recovered)
			l.Error("SESSION", "turn_panic", "agent_id", o.ID, "error", panicErr.Error())
			turnFinalState = sessiondb.TurnInterrupted
			recoveryParked = true
			if err := finishTurn(); err != nil {
				l.Warn("SESSION", "turn_state_finish_failed", "error", err.Error())
			}
			s.finishRecovery(o.ID, supervisor.IncidentID(), turnGeneration)
			status := supervisor.Park(agent.AttentionRecovery(agent.RecoveryFailurePanic, panicErr))
			checkpoint := recoveryState.checkpoint(status)
			s.parkRecovery(checkpoint)
		}
		if err := finishTurn(); err != nil {
			l.Warn("SESSION", "turn_state_finish_failed", "error", err.Error())
		}
		o.endTurn(turnGeneration)
		if !recoveryParked {
			s.finishRecovery(o.ID, supervisor.IncidentID(), turnGeneration)
		}
	}()
	recoverStep := func(kind agent.RecoveryFailureKind, operation func() error) bool {
		for {
			if err := operation(); err != nil {
				l.Warn("RECOVERY", "preflight_blocked", "agent_id", o.ID, "kind", kind, "error", err.Error())
				if !supervisor.Await(ctx, agent.AttentionRecovery(kind, err)) {
					if cancelErr := supervisor.Cancel(); cancelErr != nil {
						status := supervisor.Park(agent.AttentionRecovery(agent.RecoveryFailureStorage, cancelErr))
						s.parkRecovery(recoveryState.checkpoint(status))
						recoveryParked = true
						return false
					}
					turnFinalState = sessiondb.TurnInterrupted
					sender.SendAgentEventWithID("done", map[string]any{
						"content": "Cancelled.", "reason": "cancelled",
					}, o.ID)
					return false
				}
				continue
			}
			return true
		}
	}
	if request.recovery != nil {
		if err := supervisor.BeginRetry(); err != nil &&
			!recoverStep(agent.RecoveryFailureStorage, func() error { return supervisor.BeginRetry() }) {
			return
		}
		recoveryState.appendContinuation(rootprompt.RecoveryInterrupted)
		request.recovery.Archive, request.recovery.ActiveContext = recoveryState.contexts()
	}
	if !recoverStep(agent.RecoveryFailureStorage, func() error {
		return s.persistPendingRecovery(s.db, s.sessionID, supervisor, recoveryState)
	}) {
		return
	}
	if !recoverStep(agent.RecoveryFailureStorage, func() error { return s.runBeforeTurn(ctx) }) {
		return
	}
	// A disabled or removed selected agent must not silently turn into Main Agent.
	var effectiveCfg *Config
	if !recoverStep(agent.RecoveryFailureConfiguration, func() error {
		effectiveCfg = s.runtimeProfile.GetEffectiveConfig(o.ID)
		if effectiveCfg == nil {
			return errors.New("session configuration is unavailable")
		}
		return effectiveCfg.ValidateSelectedAgent()
	}) {
		return
	}
	if !recoverStep(agent.RecoveryFailureConfiguration, func() error {
		s.mu.Lock()
		unpinned := s.accountUnpinned
		s.mu.Unlock()
		if unpinned {
			return ErrAccountRequired
		}
		return nil
	}) {
		return
	}
	if !recoverStep(agent.RecoveryFailureStorage, func() error {
		if s.db == nil {
			return errors.New("session database is unavailable")
		}
		return sessiondb.BeginAgentTurn(s.db, s.sessionID, o.ID, turnGeneration, string(userInput.EffectiveOrigin()))
	}) {
		return
	}
	if store := s.CollaborationStore(); store != nil && o.RuntimeEpoch > 0 {
		requestID := ""
		if len(request.deliveries) > 0 {
			requestID = request.deliveries[0].Event.RequestID
		}
		origin := collaboration.OriginRuntime
		switch userInput.EffectiveOrigin() {
		case provider.OriginHuman, provider.OriginHumanSteering:
			origin = collaboration.OriginHuman
		case provider.OriginPeer:
			origin = collaboration.OriginPeer
		}
		var started collaboration.Run
		if !recoverStep(agent.RecoveryFailureStorage, func() error {
			var err error
			started, err = store.StartRun(ctx, collaboration.StartRunRequest{
				AgentID: o.ID, RuntimeEpoch: o.RuntimeEpoch, RequestID: requestID, Origin: origin,
			})
			return err
		}) {
			s.releaseCollaborationReservations(o.ID, o.RuntimeEpoch, request.deliveries, "run could not start")
			return
		}
		collaborationRun = started
		o.setCollaborationRun(started)
	}

	// Use per-orchestrator effective config from runtime profile.
	activeCfg := effectiveCfg.GetActiveDefaultCategory()
	provider.MergeModelCap(activeCfg.Model, slices.Contains(activeCfg.ModelModalities, provider.ModalityImage), activeCfg.ModelSupportsTools)
	provider.MergeModelModalities(activeCfg.Model, activeCfg.ModelModalities)

	s.turnGate.RLock()
	defer s.turnGate.RUnlock()
	s.activeTurns.Add(1)
	defer s.activeTurns.Add(-1)

	s.turnNumber++
	s.toolCallCountMu.Lock()
	s.toolCallCount = 0
	s.toolCallCountMu.Unlock()
	inputOrigin := userInput.EffectiveOrigin()
	humanTurn := inputOrigin == provider.OriginHuman
	historyStart, historyEnd := s.historyHooks()
	historyContinue, historyContinueEnd := s.historyContinuationHooks()
	historyStarted := false
	historyContinuationStarted := false
	historyContinuationID := ""
	historyCtx := ctx
	if historyStart != nil && humanTurn {
		if err := historyStart(ctx, s.sessionID, currentMsgID); err != nil {
			if errors.Is(err, ErrHistoryDisabled) {
				l.Debug("HISTORY", "message_scope_disabled", "msg_id", currentMsgID)
				if historyContinue != nil {
					if !recoverStep(agent.RecoveryFailureStorage, func() error {
						return historyContinue(ctx, s.sessionID, currentMsgID)
					}) {
						return
					}
					historyContinuationStarted = true
					historyContinuationID = currentMsgID
				}
			} else {
				if !recoverStep(agent.RecoveryFailureStorage, func() error {
					return historyStart(ctx, s.sessionID, currentMsgID)
				}) {
					return
				}
				historyStarted = true
				historyCtx = withHistoryScope(ctx, s.sessionID, currentMsgID, o.ID)
			}
		} else {
			historyStarted = true
			historyCtx = withHistoryScope(ctx, s.sessionID, currentMsgID, o.ID)
		}
	} else if inputOrigin == provider.OriginPeer && userInput.Collaboration != nil && userInput.Collaboration.HistoryMessageID != "" {
		historyMessageID := userInput.Collaboration.HistoryMessageID
		if historyContinue != nil {
			if !recoverStep(agent.RecoveryFailureStorage, func() error {
				return historyContinue(ctx, s.sessionID, historyMessageID)
			}) {
				return
			}
			historyContinuationStarted = true
			historyContinuationID = historyMessageID
		}
		historyCtx = withHistoryScope(ctx, s.sessionID, historyMessageID, o.ID)
	}
	if historyContinuationStarted && historyContinueEnd != nil {
		defer func() {
			finishContinuation := func() {
				if err := historyContinueEnd(context.Background(), s.sessionID, historyContinuationID); err != nil {
					l.Warn("HISTORY", "continuation_scope_end_failed", "msg_id", historyContinuationID, "error", err.Error())
				}
			}
			if runner := o.subAgentRunner(); runner != nil && runner.TurnRunning(turnGeneration) {
				s.historyWg.Add(1)
				s.historyFinalizers.Add(1)
				go func() {
					defer s.historyWg.Done()
					defer s.historyFinalizers.Add(-1)
					runner.WaitForTurn(turnGeneration)
					finishContinuation()
				}()
				return
			}
			finishContinuation()
		}()
	}
	if historyStarted && historyEnd != nil {
		defer func() {
			finishHistory := func() {
				if err := historyEnd(context.Background(), s.sessionID, currentMsgID); err != nil {
					l.Warn("HISTORY", "message_scope_end_failed", "msg_id", currentMsgID, "error", err.Error())
				}
			}
			if runner := o.subAgentRunner(); runner != nil && runner.TurnRunning(turnGeneration) {
				s.historyWg.Add(1)
				s.historyFinalizers.Add(1)
				go func() {
					defer s.historyWg.Done()
					defer s.historyFinalizers.Add(-1)
					runner.WaitForTurn(turnGeneration)
					finishHistory()
				}()
				return
			}
			finishHistory()
		}()
	}
	l.Info("INPUT", "received", "len", len(userInput.Text), "refs", len(userInput.ReferenceIDs), "msg_id", currentMsgID, "turn", s.turnNumber)

	var archiveCopy, contextMsgs, frozenPrefix []provider.ChatMessage
	persistedPrefixLen := 0
	resumingAdmitted := request.recovery != nil && request.recovery.InputAdmitted
	if resumingAdmitted {
		archiveCopy = recoveryArchiveMessages(request.recovery.Archive)
		contextMsgs = append([]provider.ChatMessage(nil), request.recovery.ActiveContext...)
		frozenPrefix = append([]provider.ChatMessage(nil), contextMsgs...)
		s.msgsMu.Lock()
		persistedPrefixLen = len(s.msgs)
		s.activeContext = append([]provider.ChatMessage(nil), contextMsgs...)
		s.contextGeneration = max(s.contextGeneration, request.recovery.ContextGeneration)
		s.contextReason = request.recovery.ContextReason
		s.msgsMu.Unlock()
		recoveryState.snapshot(archiveCopy, contextMsgs)
	} else {
		userMsg, referenceLabels := s.buildUserMessageWithDisplay(userInput, activeCfg.Model)
		userMsg.InternalID = currentMsgID
		userMsg.DisplayContent = userInput.Text
		if userInput.DisplayText != "" {
			userMsg.DisplayContent = userInput.DisplayText
		}
		requestContextParts := append([]string(nil), userInput.ContextParts...)
		if humanTurn {
			var currentPlan string
			if !recoverStep(agent.RecoveryFailureStorage, func() error {
				var err error
				currentPlan, err = s.activePlanTurnContext(ctx)
				return err
			}) {
				return
			}
			if currentPlan != "" {
				requestContextParts = append(requestContextParts, currentPlan)
			}
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
		if status := agent.BuildAsyncCommandStatus(s.asyncMgr); status != "" {
			runtimeParts = append(runtimeParts, status)
		}
		if runner := o.subAgentRunner(); runner != nil {
			if status := agent.BuildSubAgentStatus(runner.Manager()); status != "" {
				runtimeParts = append(runtimeParts, status)
			}
		}
		if len(runtimeParts) > 0 {
			requestContextParts = append(requestContextParts, "## Runtime Status\n\n"+strings.Join(runtimeParts, "\n\n"))
		}
		var runtimeContext *provider.ChatMessage
		if content := strings.TrimSpace(strings.Join(requestContextParts, "\n\n")); content != "" {
			message := provider.NewTextMessage("user", rootprompt.TurnContext(content))
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
		frozenPrefix = append([]provider.ChatMessage(nil), s.activeContext...)
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
		archiveCopy = append([]provider.ChatMessage(nil), s.msgs...)
		contextMsgs = append([]provider.ChatMessage(nil), s.activeContext...)
		persistedPrefixLen = len(archiveCopy)
		s.msgsMu.Unlock()
		recoveryState.setInputAdmitted(true)
		recoveryState.snapshot(archiveCopy, contextMsgs)
		if !recoverStep(agent.RecoveryFailureStorage, func() error {
			return s.persistPendingRecovery(s.db, s.sessionID, supervisor, recoveryState)
		}) {
			return
		}
	}
	if len(request.deliveries) > 0 {
		if !recoverStep(agent.RecoveryFailureStorage, func() error {
			return s.commitCollaborationAdmissions(
				s.db, s.sessionID, o.ID, collaborationRun.ID, o.RuntimeEpoch,
				max(1, s.contextGeneration), contextMsgs, request.deliveries,
			)
		}) {
			s.rollbackTurnInput(currentMsgID)
			s.releaseCollaborationReservations(o.ID, o.RuntimeEpoch, request.deliveries, "context admission failed")
			return
		}
		for _, delivery := range request.deliveries {
			sender.SendAgentEventWithID("collaboration_message", delivery.Event, o.ID)
		}
	}
	l.Info("CONV", "user_msg", "msg_id", currentMsgID, "len", len(userInput.Text))

	l.Info("AGENT", "loop_start", "msg_id", currentMsgID)

	if s.editor != nil {
		s.editor = s.editor.WithMessage(currentMsgID)
	}

	var turnProvider, compactionProvider *provider.Provider
	var compactionCfg CategoryConfig
	if !recoverStep(agent.RecoveryFailureConfiguration, func() error {
		effectiveCfg = s.runtimeProfile.GetEffectiveConfig(o.ID)
		if effectiveCfg == nil {
			return errors.New("session configuration is unavailable")
		}
		if err := effectiveCfg.ValidateSelectedAgent(); err != nil {
			return err
		}
		activeCfg = effectiveCfg.GetActiveDefaultCategory()
		var err error
		turnProvider, err = effectiveCfg.providerForCategory(activeCfg)
		if err != nil {
			return err
		}
		compactionCfg = effectiveCfg.GetCategoryConfig(CategoryAeron)
		compactionProvider, err = effectiveCfg.providerForCategory(compactionCfg)
		return err
	}) {
		return
	}
	turnIdentity := ttypes.TurnIdentity{
		Generation: turnGeneration,
		ProviderID: turnProvider.ID,
		AccountID:  turnProvider.AccountID,
		ModelID:    turnProvider.Model,
	}
	sender.SendAgentEventWithID("turn_started", turnIdentity, o.ID)
	ag := s.newAgent(turnProvider, effectiveCfg.IsMultiAgentEnabled(), activeCfg.ModelModalities)
	ag.GuidancePaths = append([]string(nil), userInput.ActivePaths...)
	ag.MaxOutputTokens = activeCfg.ModelMaxOutput
	ag.ReasoningContentKind = s.inferredReasoningKind(o.ID, turnProvider.ID, turnProvider.AccountID, turnProvider.Model)
	ag.CompactionProvider = compactionProvider
	ag.CompactionMaxOutput = compactionCfg.ModelMaxOutput
	_, agentProfile := activeProfileCfg(effectiveCfg)
	ag.CompactionAgentProfile = agentProfile + "/" + CategoryAeron
	ag.AgentID = agent.AgentID(o.ID)
	ag.TurnGeneration = turnGeneration
	ag.PrepareCompaction = func(source agent.CompactionSource) error {
		return s.beginRootCompaction(o.ID, source)
	}
	ag.CommitCompaction = func(commit agent.CompactionCommit) error {
		return s.commitRootCompaction(o.ID, commit)
	}
	ag.Checkpoint = func(archive, active []provider.ChatMessage) error {
		recoveryState.snapshot(archive, active)
		s.msgsMu.Lock()
		recoveryState.setContext(s.sessionID, s.contextGeneration, s.contextReason)
		s.msgsMu.Unlock()
		return persistRecoveryBoundary(ctx, supervisor)
	}
	if activeCfg.ContextCompactionPercent != nil {
		ag.ContextCompactionPercent = *activeCfg.ContextCompactionPercent
	}
	if len(frozenPrefix) > 0 && frozenPrefix[len(frozenPrefix)-1].Role == "assistant" && s.db != nil {
		if baseline, ok, baselineErr := sessiondb.LatestNormalUsageRecord(s.db, s.sessionID, o.ID); baselineErr != nil {
			l.Warn("COMPACT", "context_baseline_load_failed", "error", baselineErr.Error())
		} else if ok && baseline.ModelID == activeCfg.Model && baseline.ProviderID == turnProvider.ID && baseline.AccountID == turnProvider.AccountID {
			// Agent.Run prepends one system message. The prior final assistant
			// therefore begins at index len(frozenPrefix) in the new request.
			ag.SeedContextUsage(baseline.ContextUsed, baseline.CompletionTokens, len(frozenPrefix))
		}
	}
	ag.ContextArchive = archiveCopy
	ag.FrozenPrefix = frozenPrefix
	if runner := o.subAgentRunner(); runner != nil {
		runner.SetParentTurnGeneration(turnGeneration)
		ag.SubAgentMgr = runner.Manager()
	}
	baseToolFn := ag.ToolFn
	ag.ToolFn = func(toolCtx context.Context, tool string, params map[string]any) (tools.ToolResult, error) {
		return baseToolFn(withDelegationOwner(toolCtx, o.ID, turnGeneration), tool, params)
	}
	ag.PrepareStep = func() ([]tools.ToolDef, []provider.ChatMessage) {
		// Publish the tool snapshot and its settings notices together, after
		// any concurrent save has completed its launch-admission barrier.
		s.delegationMu.Lock()
		defer s.delegationMu.Unlock()
		current := s.runtimeProfile.GetEffectiveConfig(o.ID)
		return configuredOrchestratorTools(current, current.IsMultiAgentEnabled(), ag.Model), s.takeDelegationNotices()
	}
	s.bindAgentRuntime(ctx, ag, captypes.RuntimeActor{ID: o.ID, SessionID: s.sessionID, AllowedTools: activeCfg.AllowedTools, DeniedTools: activeCfg.DeniedTools, SkillModes: activeCfg.SkillModes}, s.AgentName())
	defer s.FileOwnership().Release(agent.AgentID(o.ID))
	var pendingCollaboration []collaboration.PendingDelivery
	defer func() {
		s.releaseCollaborationReservations(o.ID, o.RuntimeEpoch, pendingCollaboration, "turn ended before context admission")
	}()
	ag.PrepareRuntimeNotices = func() []provider.ChatMessage {
		if ctx.Err() != nil || s.isHardCancelled() {
			return nil
		}
		var notices []provider.ChatMessage
		if len(pendingCollaboration) == 0 {
			reserved, err := s.reserveCollaboration(ctx, o.ID, o.RuntimeEpoch, 20)
			if err != nil && !errors.Is(err, collaboration.ErrUnavailable) {
				l.Warn("COLLAB", "active_reserve_failed", "agent_id", o.ID, "error", err.Error())
			} else {
				pendingCollaboration = reserved
			}
		}
		for _, delivery := range pendingCollaboration {
			notices = append(notices, collaborationMessage(delivery))
		}
		return notices
	}
	ag.CommitRuntimeNotices = func(messages []provider.ChatMessage) error {
		if len(pendingCollaboration) == 0 {
			return nil
		}
		contextMessages := messages
		if len(contextMessages) > 0 && contextMessages[0].Role == "system" {
			contextMessages = contextMessages[1:]
		}
		if err := s.commitCollaborationAdmissions(
			s.db, s.sessionID, o.ID, collaborationRun.ID, o.RuntimeEpoch,
			max(1, s.contextGeneration), contextMessages, pendingCollaboration,
		); err != nil {
			s.releaseCollaborationReservations(o.ID, o.RuntimeEpoch, pendingCollaboration, "context admission failed")
			pendingCollaboration = nil
			return err
		}
		for _, delivery := range pendingCollaboration {
			sender.SendAgentEventWithID("collaboration_message", delivery.Event, o.ID)
		}
		pendingCollaboration = nil
		return nil
	}
	ag.SetToolCallSeqStart(s.toolCallSeq)
	var finalContent string
	prompt := s.systemPromptForModel(effectiveCfg.IsMultiAgentEnabled(), ag.Model)

	displayTracker := &displayMetaTracker{
		metas: make(map[string]*sessiondb.DisplayMeta),
	}

	ag.Ctx = historyCtx
	activeCfg = effectiveCfg.GetActiveDefaultCategory()

	steeringCtx, stopSteering := context.WithCancel(historyCtx)
	runtimeMessages, runtimeMessagesDone, flushRuntime := s.humanSteeringMessages(
		steeringCtx, s.db, s.sessionID, o.ID, collaborationRun.ID, o.RuntimeEpoch,
		func() uint64 {
			s.msgsMu.Lock()
			defer s.msgsMu.Unlock()
			return max(1, s.contextGeneration)
		},
		activeCfg.Model, sender,
	)
	ag.FlushRuntimeMessages = flushRuntime

	var emitActive atomic.Bool
	emitActive.Store(true)
	var terminalDone *agent.AgentEvent

	emit := func(evt agent.AgentEvent) {
		if evt.Type == "error" && ctx.Err() == nil {
			turnFinalState = sessiondb.TurnFailed
		}
		if evt.Type == "done" {
			if data, ok := evt.Data.(map[string]any); ok && data["reason"] == "completed" {
				turnFinalState = sessiondb.TurnCompleted
			}
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
		if evt.Type == "done" {
			copy := evt
			copy.AgentID = agentID
			terminalDone = &copy
		} else {
			sender.SendAgentEventWithID(evt.Type, evt.Data, agentID)
		}
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
	prepareAttempt := func() (string, error) {
		current := s.runtimeProfile.GetEffectiveConfig(o.ID)
		if current == nil {
			return "", errors.New("session configuration is unavailable")
		}
		if err := current.ValidateSelectedAgent(); err != nil {
			return "", err
		}
		category := current.GetActiveDefaultCategory()
		currentProvider, err := current.providerForCategory(category)
		if err != nil {
			return "", err
		}
		currentCompactionCfg := current.GetCategoryConfig(CategoryAeron)
		currentCompactionProvider, err := current.providerForCategory(currentCompactionCfg)
		if err != nil {
			return "", err
		}
		activeCfg = category
		ag.Provider, ag.Model = currentProvider, currentProvider.Model
		ag.MaxOutputTokens = category.ModelMaxOutput
		ag.CompactionProvider = currentCompactionProvider
		ag.CompactionMaxOutput = currentCompactionCfg.ModelMaxOutput
		if category.ContextCompactionPercent != nil {
			ag.ContextCompactionPercent = *category.ContextCompactionPercent
		}
		turnIdentity = ttypes.TurnIdentity{
			Generation: turnGeneration, ProviderID: currentProvider.ID,
			AccountID: currentProvider.AccountID, ModelID: currentProvider.Model,
		}
		return s.systemPromptForModel(current.IsMultiAgentEnabled(), ag.Model), nil
	}
	agentMsgs := ag.RunSupervised(
		historyCtx, contextMsgs, prompt, emit, o.BlockResolveCh, s.sessionID, runtimeMessages,
		agent.SupervisedRunOptions{
			Supervisor:      supervisor,
			Prepare:         prepareAttempt,
			Snapshot:        recoveryState.snapshot,
			DeferSettlement: true,
		},
	)
	controlMessages := s.takeCollaborationContext(o.ID)
	if len(controlMessages) > 0 {
		agentMsgs = append(agentMsgs, controlMessages...)
	}
	stopSteering()
	<-runtimeMessagesDone
	emitActive.Store(false)
	s.toolCallSeq = ag.ToolCallSeq()

	l.Info("AGENT", "loop_end", "len", len(finalContent))
	activeContext := ag.ActiveContext()
	activeContext = append(activeContext, controlMessages...)
	recoveryState.snapshot(agentMsgs, activeContext)
	finalizeErr := s.finalizeTurn(agentMsgs, persistedPrefixLen, displayTracker, finalContent, currentMsgID, turnIdentity, activeContext)
	for finalizeErr != nil {
		if !supervisor.Await(ctx, agent.AttentionRecovery(agent.RecoveryFailureStorage, finalizeErr)) {
			if cancelErr := supervisor.Cancel(); cancelErr != nil {
				status := supervisor.Park(agent.AttentionRecovery(agent.RecoveryFailureStorage, cancelErr))
				s.parkRecovery(recoveryState.checkpoint(status))
				recoveryParked = true
				return
			}
			turnFinalState = sessiondb.TurnInterrupted
			sender.SendAgentEventWithID("done", map[string]any{
				"content": "Cancelled.", "reason": "cancelled",
			}, o.ID)
			return
		}
		finalizeErr = s.persistFinalizedTurn()
	}
	cancelled := false
	if terminalDone != nil {
		cancelled = terminalReasonFromEvent(*terminalDone) == "cancelled"
	}
	// Do not discard the recovery envelope or acknowledge success while the
	// durable turn/collaboration lifecycle still says running.
	if !recoverStep(agent.RecoveryFailureStorage, finishTurn) {
		return
	}
	if cancelled {
		if err := supervisor.Cancel(); err != nil {
			status := supervisor.Park(agent.AttentionRecovery(agent.RecoveryFailureStorage, err))
			s.parkRecovery(recoveryState.checkpoint(status))
			recoveryParked = true
			return
		}
	} else {
		for {
			if err := supervisor.Resolve(); err == nil {
				break
			} else if !supervisor.Await(ctx, agent.AttentionRecovery(agent.RecoveryFailureStorage, err)) {
				if cancelErr := supervisor.Cancel(); cancelErr != nil {
					status := supervisor.Park(agent.AttentionRecovery(agent.RecoveryFailureStorage, cancelErr))
					s.parkRecovery(recoveryState.checkpoint(status))
					recoveryParked = true
					return
				}
				turnFinalState = sessiondb.TurnInterrupted
				sender.SendAgentEventWithID("done", map[string]any{
					"content": "Cancelled.", "reason": "cancelled",
				}, o.ID)
				return
			}
		}
	}
	o.endTurn(turnGeneration)
	if terminalDone != nil {
		sender.SendAgentEventWithID(terminalDone.Type, terminalDone.Data, terminalDone.AgentID)
	}
}

func terminalReasonFromEvent(event agent.AgentEvent) string {
	data, _ := event.Data.(map[string]any)
	reason, _ := data["reason"].(string)
	return reason
}

func (o *Orchestrator) handleBlockingEvent(data map[string]string, evt agent.AgentEvent, sender EventSender) bool {
	s := o.session
	if data != nil {
		op := safety.CRUDEOp(data["operation"])
		tool := data["tool"]
		target := data["target"]
		allowed := s.permStore.IsAlwaysAllowed(op, tool, target)
		if tool == "apply_patch" {
			allowed = s.patchAlwaysAllowed(data["patch_input"])
		}
		// Capability publication carries its own immutable preimage. A generic
		// Read/Always permission cannot approve another install or changed grant.
		if allowed && tool != "capability_activate" && tool != "mcp_request_access" {
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
	case "apply_patch":
		s.grantPatchAlways(data["patch_input"])
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
	if previous.Outcome.Known() {
		record.Outcome = previous.Outcome
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
			if name != "" && toolID != "" {
				args, _ := data["args"].(string)
				tracker.updateToolCall(toolID, name, args)
				if round, _ := data["request_id"].(string); round != "" {
					ensureToolSnapshot(tracker.metas[toolID]).ToolRoundID = round
				}
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
) error {
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
	toolNames := make(map[string]string)
	for _, earlier := range returned {
		for _, call := range earlier.ToolCalls {
			toolNames[call.ID] = call.Function.Name
		}
	}

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
			if name := toolNames[m.ToolCallID]; name != "" {
				dm.ToolName = name
			}
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
			dm.AccountID = turnIdentity.AccountID
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
				ProviderID: turnIdentity.ProviderID, AccountID: turnIdentity.AccountID, ModelID: turnIdentity.ModelID,
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
			return err
		}
		if err := s.flushWriteBufSync(); err != nil {
			return err
		}
	} else if len(s.msgs) > 0 {
		return errors.New("session database is unavailable")
	}

	s.afterTurnMemory(turnNumber, turnMsgs, finalContent)

	if finalContent != "" {
		s.logger.Info("CONV", "assistant_msg", "msg_id", msgID, "len", len(finalContent))
	}
	return nil
}

func (s *Session) persistFinalizedTurn() error {
	if s == nil || s.db == nil {
		return errors.New("session database is unavailable")
	}
	s.msgsMu.Lock()
	state := sessiondb.ContextState{
		Generation: s.contextGeneration, Reason: s.contextReason,
		Messages: append([]provider.ChatMessage(nil), s.activeContext...),
	}
	s.msgsMu.Unlock()
	if err := sessiondb.SaveContextState(s.db, s.sessionID, s.sessionID, state); err != nil {
		return err
	}
	return s.flushWriteBufSync()
}

func contextCheckpointContent(messages []provider.ChatMessage) string {
	for _, message := range messages {
		if message.EffectiveOrigin() == provider.OriginCheckpoint {
			return message.ContentString()
		}
	}
	return ""
}

func (s *Session) rollbackTurnInput(messageID string) {
	if s == nil || messageID == "" {
		return
	}
	remove := map[string]bool{messageID: true, messageID + "-context": true}
	s.msgsMu.Lock()
	defer s.msgsMu.Unlock()
	nextMessages := make([]provider.ChatMessage, 0, len(s.msgs))
	nextTurns := make([]int, 0, len(s.msgTurns))
	nextMetas := make([]sessiondb.DisplayMeta, 0, len(s.displayMetas))
	nextCreated := make([]int64, 0, len(s.createdAts))
	for index, message := range s.msgs {
		if remove[message.InternalID] {
			continue
		}
		nextMessages = append(nextMessages, message)
		if index < len(s.msgTurns) {
			nextTurns = append(nextTurns, s.msgTurns[index])
		}
		if index < len(s.displayMetas) {
			nextMetas = append(nextMetas, s.displayMetas[index])
		}
		if index < len(s.createdAts) {
			nextCreated = append(nextCreated, s.createdAts[index])
		}
	}
	s.msgs, s.msgTurns, s.displayMetas, s.createdAts = nextMessages, nextTurns, nextMetas, nextCreated
	s.activeContext = removeSessionMessagesByInternalID(s.activeContext, messageID, messageID+"-context")
}

func removeSessionMessagesByInternalID(messages []provider.ChatMessage, ids ...string) []provider.ChatMessage {
	remove := make(map[string]bool, len(ids))
	for _, id := range ids {
		remove[id] = true
	}
	result := messages[:0]
	for _, message := range messages {
		if !remove[message.InternalID] {
			result = append(result, message)
		}
	}
	return result
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
	if input.Collaboration != nil {
		attribution := *input.Collaboration
		attribution.RecipientIDs = append([]string(nil), input.Collaboration.RecipientIDs...)
		attribution.RecipientNames = append([]string(nil), input.Collaboration.RecipientNames...)
		message.Collaboration = &attribution
	}
	if message.Origin == provider.OriginHuman || message.Origin == provider.OriginHumanSteering || message.Origin == provider.OriginPeer {
		message.Visibility = provider.VisibilityConversation
	} else {
		message.Visibility = provider.VisibilityContextOnly
	}
	return message
}
