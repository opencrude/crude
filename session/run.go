package session

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

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
	"crude/worktree"
)

func (s *Session) RunOrchestrator(o *Orchestrator, sender EventSender) {
	l := s.logger
	l.Info("SESSION", "run_start", "orchestrator_id", o.ID)
	s.sender = sender
	s.observeCommandOutcomes(s.asyncMgr, o.ID)
	defer s.detachCollaborator(context.Background(), o.ID)
	s.reconcileCollaborationAdmissions(s.db, o.ID)
	const collaborationPollInterval = 500 * time.Millisecond
	passiveReady := true
	var passiveRetryAt time.Time
	if err := s.admitPassiveCollaboration(o, sender); err != nil {
		passiveReady = false
		passiveRetryAt = time.Now().Add(collaborationPollInterval)
		l.Warn("COLLAB", "passive_recovery_deferred", "agent_id", o.ID, "error", err.Error())
	}

	var collaborationNotifications <-chan struct{}
	unsubscribeCollaboration := func() {}
	if store := s.CollaborationStore(); store != nil {
		collaborationNotifications, unsubscribeCollaboration = store.Subscribe()
	}
	defer unsubscribeCollaboration()
	collaborationPoll := time.NewTicker(collaborationPollInterval)
	defer collaborationPoll.Stop()
	startCollaborationTurn := func() bool {
		if o.RuntimeEpoch == 0 || o.TurnActive() || o.closing.Load() || atomic.LoadInt32(&s.closing) != 0 {
			return false
		}
		if !passiveReady {
			// Reservation release also notifies this loop; failed storage must not
			// turn its own notifications into an unbounded retry/wake cycle.
			if time.Now().Before(passiveRetryAt) {
				return false
			}
			if err := s.admitPassiveCollaboration(o, sender); err != nil {
				passiveRetryAt = time.Now().Add(collaborationPollInterval)
				return false
			}
			passiveReady = true
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

	// startGoalTurn continues an active goal when nothing else owns the agent:
	// collaboration deliveries and queued human input always go first.
	startGoalTurn := func() bool {
		if o.RuntimeEpoch == 0 || o.TurnActive() || o.closing.Load() || atomic.LoadInt32(&s.closing) != 0 {
			return false
		}
		input, ok := s.goalContinuation()
		return ok && o.SubmitInput(input)
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
					for {
						select {
						case request := <-o.inputCh:
							s.preserveUnstartedInput(request, o)
						default:
							l.Info("SESSION", "run_exit_closed", "orchestrator_id", o.ID)
							return false
						}
					}
				case request := <-o.inputCh:
					if o.closing.Load() {
						s.preserveUnstartedInput(request, o)
						l.Info("SESSION", "run_exit_closing", "orchestrator_id", o.ID)
						return false
					}
					if s.isHardCancelled() {
						s.clearHardCancel()
					}
					s.processTurnOrchestrator(request, o, sender, l)
				case <-collaborationNotifications:
					if startCollaborationTurn() || startGoalTurn() {
						request := <-o.inputCh
						s.processTurnOrchestrator(request, o, sender, l)
					}
				case <-collaborationPoll.C:
					if startCollaborationTurn() || startGoalTurn() {
						request := <-o.inputCh
						s.processTurnOrchestrator(request, o, sender, l)
					}
				case <-s.goalWake:
					if startCollaborationTurn() || startGoalTurn() {
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
	if request.recovery == nil {
		s.msgsMu.Lock()
		recoveryState.contextOwnerID = s.sessionID
		recoveryState.contextRevision = s.contextRevision
		recoveryState.contextGeneration = max(1, s.contextGeneration)
		recoveryState.contextReason = "run_recovery"
		recoveryState.archive = append([]provider.ChatMessage(nil), s.msgs...)
		recoveryState.active = append([]provider.ChatMessage(nil), s.activeContext...)
		s.msgsMu.Unlock()
	}
	var initialRecovery *recoverytypes.Status
	if request.recovery != nil && request.recovery.Status.ID != "" {
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
	s.msgsMu.Lock()
	if request.sessionTurn == 0 && request.retry {
		for i, message := range s.msgs {
			if message.InternalID == currentMsgID && i < len(s.msgTurns) {
				request.sessionTurn = s.msgTurns[i]
				break
			}
		}
	}
	if request.sessionTurn == 0 {
		request.sessionTurn = s.turnNumber + 1
	}
	s.turnNumber = max(s.turnNumber, request.sessionTurn)
	s.msgsMu.Unlock()
	recoveryState.setPending(userInput)
	locallyAdmitted := request.recovery != nil && request.recovery.InputAdmitted
	if !locallyAdmitted {
		// Submission is immutable even when subsequent reference preparation fails.
		userMsg := inputMessage(provider.NewTextMessage("user", userInput.Text), userInput)
		userMsg.InternalID = currentMsgID
		userMsg.DisplayContent = userInput.Text
		if userInput.DisplayText != "" {
			userMsg.DisplayContent = userInput.DisplayText
		}
		createdAt := time.Now()
		meta := sessiondb.DisplayMeta{DisplayRole: "user", DisplayContent: userInput.Text}
		if userMsg.EffectiveVisibility() != provider.VisibilityConversation {
			// Runtime-started turns (goal continuations) are provider input,
			// never a human message in the transcript.
			meta = sessiondb.DisplayMeta{DisplayRole: "hidden_runtime_context", ContextOnly: true}
		}
		meta = canonicalizeDisplayMeta(userMsg, meta, "", len(s.msgs), createdAt)
		if len(request.deliveries) > 0 {
			// The collaboration event owns the canonical message identity, payload and
			// timestamp. The later admission transaction reuses this exact delta while
			// committing the receipt, so it is idempotent rather than a conflicting
			// second transcript author.
			userMsg = collaborationMessage(request.deliveries[0])
			currentMsgID = userMsg.InternalID
			userInput.InternalID = currentMsgID
			createdAt = request.deliveries[0].Event.CreatedAt
			meta = sessiondb.DisplayMeta{DisplayRole: "hidden_runtime_context", ContextOnly: true}
			if userMsg.EffectiveVisibility() == provider.VisibilityConversation {
				meta = sessiondb.DisplayMeta{DisplayRole: "user", DisplayContent: userMsg.DisplayContent}
			}
			meta = canonicalizeDisplayMeta(userMsg, meta, "", len(s.msgs), createdAt)
		}
		s.msgsMu.Lock()
		state, err := s.publishRootContext(sessiondb.ContextPublication{
			Mutation: sessiondb.ContextMutation{Append: []sessiondb.ContextMessageDelta{
				contextDelta(userMsg, meta, s.turnNumber, createdAt, o.ID),
			}},
		})
		if err == nil {
			s.msgs = append(s.msgs, userMsg)
			s.msgTurns = append(s.msgTurns, s.turnNumber)
			s.displayMetas = append(s.displayMetas, meta)
			s.createdAts = append(s.createdAts, createdAt.UnixNano())
		}
		archive := append([]provider.ChatMessage(nil), s.msgs...)
		active := append([]provider.ChatMessage(nil), state.Messages...)
		s.msgsMu.Unlock()
		if err != nil {
			l.Warn("INPUT", "local_admission_failed", "agent_id", o.ID)
			o.endTurn(turnGeneration)
			return
		}
		recoveryState.setInputAdmitted(true)
		recoveryState.snapshot(archive, active)
		recoveryState.setContext(s.sessionID, s.contextRevision, s.contextGeneration, s.contextReason)
	}
	turnFinished := false
	turnFinalState := sessiondb.TurnFailed
	recoveryParked := false
	var collaborationRun collaboration.Run
	executionRoot := s.WorkDir()
	finishTurn := func() error {
		if turnFinished {
			return nil
		}
		if ctx.Err() != nil && turnFinalState != sessiondb.TurnCompleted {
			turnFinalState = sessiondb.TurnInterrupted
		}
		if !recoveryParked && (turnFinalState == sessiondb.TurnCompleted || turnFinalState == sessiondb.TurnInterrupted) {
			state, outcome := collaboration.RunCancelled, "cancelled"
			if turnFinalState == sessiondb.TurnCompleted {
				state, outcome = collaboration.RunSucceeded, "completed"
			}
			if err := recoveryState.captureTerminalSettlement(terminalSettlementIntent{
				SessionID: s.sessionID, AgentID: o.ID, OwnerID: s.sessionID, Generation: turnGeneration, TurnState: turnFinalState, Stopped: ctx.Err() != nil,
				Run: collaboration.FinishRunRequest{RunID: collaborationRun.ID, AgentID: o.ID, RuntimeEpoch: o.RuntimeEpoch, ExpectedVersion: collaborationRun.Version, State: state, Outcome: outcome, ContextCommitID: currentMsgID},
			}); err != nil {
				return err
			}
			if err := supervisor.Persist(supervisor.CheckpointStatus()); err != nil {
				return err
			}
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
			if store == nil {
				return errors.New("root terminal collaboration store is unavailable")
			} else {
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
		s.revokeCapabilityRunFor(captypes.RuntimeActor{ID: o.ID, SessionID: s.sessionID, RunID: collaborationRun.ID, RuntimeEpoch: o.RuntimeEpoch, TurnGeneration: turnGeneration, ExecutionRoot: executionRoot}, userInput.ControlTarget)
		s.reconcileRootExecutionPins()
		return nil
	}
	// The goal settles after the unwind below has decided whether the turn
	// completed, failed, was stopped or parked for recovery.
	var goalProgress *goalTurn
	var turnFailure string
	defer func() {
		// The turn context is always cancelled once the turn ends; only the
		// final state says whether the user stopped it.
		s.goalTurnEnd(goalProgress, turnFinalState == sessiondb.TurnInterrupted, turnFinalState == sessiondb.TurnFailed, turnFailure, recoveryParked)
	}()
	defer func() {
		// Parking during unwind must not advertise Retry while the old turn
		// still owns its reservation, nor publish between slot removal and
		// installing the exact passive checkpoint. Await's live wake path is
		// unaffected; only terminal unwind holds this emission until teardown.
		emit := supervisor.Emit
		var parkedEvents []agent.AgentEvent
		park := func(failure agent.RecoveryFailure) recoverytypes.Status {
			supervisor.Emit = func(event agent.AgentEvent) { parkedEvents = append(parkedEvents, event) }
			defer func() { supervisor.Emit = emit }()
			return supervisor.Park(failure)
		}
		if recovered := recover(); recovered != nil {
			panicErr := fmt.Errorf("session turn panic: %v", recovered)
			l.Error("SESSION", "turn_panic", "agent_id", o.ID, "error", panicErr.Error())
			turnFinalState = sessiondb.TurnInterrupted
			recoveryParked = true
			if err := finishTurn(); err != nil {
				l.Warn("SESSION", "turn_state_finish_failed", "error", err.Error())
			}
			s.finishRecovery(o.ID, supervisor.IncidentID(), turnGeneration)
			status := park(agent.AttentionRecovery(agent.RecoveryFailurePanic, panicErr))
			checkpoint := recoveryState.checkpoint(status)
			s.parkRecovery(checkpoint)
		}
		if err := finishTurn(); err != nil {
			l.Warn("SESSION", "turn_state_finish_failed", "error", err.Error())
			status := park(agent.AttentionRecovery(agent.RecoveryFailureStorage, err))
			s.parkRecovery(recoveryState.checkpoint(status))
			recoveryParked = true
		}
		o.endTurn(turnGeneration)
		if !recoveryParked {
			s.finishRecovery(o.ID, supervisor.IncidentID(), turnGeneration)
		}
		for _, event := range parkedEvents {
			if emit != nil {
				emit(event)
			}
		}
	}()
	recoverStep := func(kind agent.RecoveryFailureKind, operation func() error) bool {
		for {
			if err := operation(); err != nil {
				l.Warn("RECOVERY", "preflight_blocked", "agent_id", o.ID, "kind", kind, "error", err.Error())
				if !supervisor.Await(ctx, agent.AttentionRecovery(kind, err)) {
					if turnFinalState != sessiondb.TurnCompleted {
						turnFinalState = sessiondb.TurnInterrupted
					}
					settlementCtx, stopSettlement := context.WithCancel(context.WithoutCancel(ctx))
					defer stopSettlement()
					go func() {
						select {
						case <-o.stopCh:
							stopSettlement()
						case <-settlementCtx.Done():
						}
					}()
					for {
						// Stop cancels execution, not the mandatory terminal publisher.
						s.msgsMu.Lock()
						boundaryID := "turn-stopped:" + supervisor.IncidentID()
						var cancelErr error
						if !slices.ContainsFunc(s.activeContext, func(message provider.ChatMessage) bool { return message.InternalID == boundaryID }) {
							stamp := time.Now()
							boundary := provider.NewTextMessage("user", rootprompt.TurnStoppedBoundary())
							boundary.InternalID, boundary.Origin, boundary.Visibility = boundaryID, provider.OriginTurnStopped, provider.VisibilityContextOnly
							meta := canonicalizeDisplayMeta(boundary, sessiondb.DisplayMeta{}, "", len(s.msgs), stamp)
							_, cancelErr = s.publishRootContext(sessiondb.ContextPublication{Mutation: sessiondb.ContextMutation{
								Append: []sessiondb.ContextMessageDelta{contextDelta(boundary, meta, request.sessionTurn, stamp, o.ID)},
							}})
							if cancelErr == nil {
								s.msgs = append(s.msgs, boundary)
								s.msgTurns = append(s.msgTurns, request.sessionTurn)
								s.displayMetas = append(s.displayMetas, meta)
								s.createdAts = append(s.createdAts, stamp.UnixNano())
							}
						}
						if cancelErr == nil {
							recoveryState.snapshot(s.msgs, s.activeContext)
							recoveryState.setContext(s.sessionID, s.contextRevision, s.contextGeneration, s.contextReason)
						}
						s.msgsMu.Unlock()
						if cancelErr == nil {
							cancelErr = finishTurn()
						}
						if cancelErr == nil {
							cancelErr = supervisor.Cancel()
						}
						if cancelErr == nil {
							break
						}
						if !supervisor.Await(settlementCtx, agent.AttentionRecovery(agent.RecoveryFailureStorage, cancelErr)) {
							status := supervisor.Park(agent.AttentionRecovery(agent.RecoveryFailureStorage, cancelErr))
							s.parkRecovery(recoveryState.checkpoint(status))
							recoveryParked = true
							return false
						}
					}
					content, reason := "Cancelled.", "cancelled"
					if turnFinalState == sessiondb.TurnCompleted {
						content, reason = "Completed.", "end_turn"
					}
					source := &runtimeEventSource{ownerID: s.sessionID, agentID: o.ID, runID: collaborationRun.ID, runtimeEpoch: o.RuntimeEpoch, generation: turnGeneration}
					sendRuntimeEvent(sender, source.bind(agent.AgentEvent{Type: "done", Data: map[string]any{"content": content, "reason": reason}}))
					return false
				}
				continue
			}
			return true
		}
	}
	if request.retry {
		s.msgsMu.Lock()
		recoveryState.snapshot(s.msgs, s.activeContext)
		recoveryState.setContext(s.sessionID, s.contextRevision, s.contextGeneration, s.contextReason)
		s.msgsMu.Unlock()
		if err := supervisor.BeginRetry(); err != nil &&
			!recoverStep(agent.RecoveryFailureStorage, func() error { return supervisor.BeginRetry() }) {
			return
		}
		recoveryState.appendContinuation(rootprompt.RecoveryInterrupted)
		if !recoverStep(agent.RecoveryFailureStorage, func() error {
			return supervisor.Persist(supervisor.CheckpointStatus())
		}) {
			return
		}
		request.recovery.Archive, request.recovery.ActiveContext = recoveryState.contexts()
	}
	if !locallyAdmitted && !request.retry {
		// The connected admission receipt already committed this exact input,
		// preparation and initial recovery envelope atomically. Retry likewise
		// published its continuation above. Direct execution, which just
		// admitted the submitted row here, still needs its first
		// envelope; never rewrite an unchanged image snapshot before every run.
		if !recoverStep(agent.RecoveryFailureStorage, func() error {
			return s.persistPendingRecovery(supervisor)
		}) {
			return
		}
	}
	s.msgsMu.Lock()
	// Admission's immutable checkpoint may precede a journal control committed
	// before this execution starts. Reconcile that checkpoint with the live
	// publication owner; never install its older revision back into the owner.
	contextErr := s.reconcileRootRecoveryLocked(recoveryState)
	s.msgsMu.Unlock()
	if contextErr != nil && !recoverStep(agent.RecoveryFailureStorage, func() error {
		s.msgsMu.Lock()
		defer s.msgsMu.Unlock()
		return s.reconcileRootRecoveryLocked(recoveryState)
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

	if userInput.ControlTarget != nil && userInput.ControlTarget.ExecutionID != "" {
		if !recoverStep(agent.RecoveryFailureConfiguration, func() error {
			var err error
			executionRoot, err = s.pinRootExecution(ctx, userInput.ControlTarget.ExecutionID, worktree.Owner{AgentID: o.ID, RunID: collaborationRun.ID})
			return err
		}) {
			return
		}
	}

	// Use per-orchestrator effective config from runtime profile.
	activeCfg := effectiveCfg.GetActiveDefaultCategory()
	provider.MergeModelCap(activeCfg.Model, slices.Contains(activeCfg.ModelModalities, provider.ModalityImage), activeCfg.ModelSupportsTools)
	provider.MergeModelModalities(activeCfg.Model, activeCfg.ModelModalities)

	s.turnGate.RLock()
	defer s.turnGate.RUnlock()
	s.activeTurns.Add(1)
	defer s.activeTurns.Add(-1)

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
			if errors.Is(err, ErrHistoryDisabled) || errors.Is(err, ErrHistoryRecorded) {
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
				if errors.Is(err, ErrHistoryRecorded) {
					historyCtx = withHistoryScope(ctx, s.sessionID, currentMsgID, o.ID)
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
	admittedArchive, admittedActive := recoveryState.contexts()
	archiveCopy = recoveryArchiveMessages(admittedArchive)
	contextMsgs = append([]provider.ChatMessage(nil), admittedActive...)
	// A completed preparation is an immutable, input-owned suffix. Continue must
	// reuse it, not reload references or take a second plan/memory/runtime snapshot.
	prepared := slices.ContainsFunc(archiveCopy, func(message provider.ChatMessage) bool {
		return message.InternalID == currentMsgID+"-context"
	})
	if !prepared {
		var input preparedInput
		if !recoverStep(agent.RecoveryFailureConfiguration, func() error {
			var err error
			input, err = s.prepareInput(ctx, userInput, effectiveCfg, o, archiveCopy...)
			return err
		}) {
			return
		}
		createdAt := time.Now()
		deltas := make([]sessiondb.ContextMessageDelta, 0, len(input.Enrichment))
		for _, message := range input.Enrichment {
			meta := canonicalizeDisplayMeta(message, sessiondb.DisplayMeta{DisplayRole: "hidden_runtime_context", ContextOnly: true}, "", 0, createdAt)
			deltas = append(deltas, contextDelta(message, meta, s.turnNumber, createdAt, o.ID))
		}
		if !recoverStep(agent.RecoveryFailureStorage, func() error {
			s.msgsMu.Lock()
			defer s.msgsMu.Unlock()
			pending := make([]sessiondb.ContextMessageDelta, 0, len(deltas))
			for _, delta := range deltas {
				index := slices.IndexFunc(s.msgs, func(message provider.ChatMessage) bool { return message.InternalID == delta.Message.InternalID })
				if index >= 0 {
					if !reflect.DeepEqual(canonicalContextMessage(s.msgs[index]), delta.Message) {
						return &sessiondb.ContextMessageConflictError{InternalID: delta.Message.InternalID, Location: "retained preparation"}
					}
					continue
				}
				pending = append(pending, delta)
			}
			state, err := s.publishRootContext(sessiondb.ContextPublication{Mutation: sessiondb.ContextMutation{Append: pending}})
			if err != nil {
				return err
			}
			for _, delta := range pending {
				s.msgs = append(s.msgs, delta.Message)
				s.msgTurns = append(s.msgTurns, delta.Turn)
				s.displayMetas = append(s.displayMetas, delta.Meta)
				s.createdAts = append(s.createdAts, delta.CreatedAt)
			}
			archiveCopy = append([]provider.ChatMessage(nil), s.msgs...)
			contextMsgs = append([]provider.ChatMessage(nil), state.Messages...)
			recoveryState.snapshot(archiveCopy, contextMsgs)
			recoveryState.setContext(s.sessionID, state.Revision, state.Generation, state.Reason)
			return nil
		}) {
			return
		}
		if !recoverStep(agent.RecoveryFailureStorage, func() error {
			return s.persistPendingRecovery(supervisor)
		}) {
			return
		}
		notifyAttachmentIssues(sender, o.ID, input.AttachmentIssues)
	}
	if humanTurn {
		s.prevContextInjected.Store(true)
	}
	if !recoverStep(agent.RecoveryFailureStorage, func() error {
		s.msgsMu.Lock()
		defer s.msgsMu.Unlock()
		if err := s.reconcileRootRecoveryLocked(recoveryState); err != nil {
			return err
		}
		archiveCopy, contextMsgs = recoveryState.contexts()
		frozenPrefix = append([]provider.ChatMessage(nil), contextMsgs...)
		persistedPrefixLen = len(s.msgs)
		return nil
	}) {
		return
	}
	if len(request.deliveries) > 0 {
		if !recoverStep(agent.RecoveryFailureStorage, func() error {
			// The event message was already admitted through the exact same
			// revision-owned context source above. This transaction now publishes
			// only the durable delivery receipt; re-appending through a separately
			// reconstructed display delta creates a second metadata authority.
			_, err := s.commitCollaborationAdmissions(
				s.db, s.sessionID, s.sessionID, o.ID, collaborationRun.ID, o.RuntimeEpoch,
				recoveryState.contextRevisionValue(), nil, request.deliveries,
			)
			return err
		}) {
			s.releaseCollaborationReservations(o.ID, o.RuntimeEpoch, request.deliveries, "context admission failed")
			return
		}
		s.msgsMu.Lock()
		recoveryState.snapshot(append([]provider.ChatMessage(nil), s.msgs...), s.activeContext)
		recoveryState.setContext(s.sessionID, s.contextRevision, s.contextGeneration, s.contextReason)
		s.msgsMu.Unlock()
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
	eventSource := &runtimeEventSource{ownerID: s.sessionID, agentID: o.ID, runID: collaborationRun.ID, runtimeEpoch: o.RuntimeEpoch, generation: turnGeneration}
	sendRuntimeEvent(sender, eventSource.bind(agent.AgentEvent{Type: "turn_started", Data: turnIdentity}))
	// Plan mode is fixed for the whole turn; a toggle applies from the next one.
	planMode := s.PlanMode()
	planModeEnded := s.notePlanModeTurn(planMode)
	goalProgress = s.goalTurnBegin(userInput, planMode)
	ag := s.newAgent(turnProvider, effectiveCfg.IsMultiAgentEnabled(), activeCfg.ModelModalities)
	ag.WorkDir = executionRoot
	ag.ToolFn = s.buildToolFnAtRoot(effectiveCfg.IsMultiAgentEnabled(), turnProvider, activeCfg.ModelModalities, executionRoot)
	if planMode {
		ag.Policy = safety.ReadOnlyPolicy()
	}
	ag.GuidancePaths = append([]string(nil), userInput.ActivePaths...)
	ag.MaxOutputTokens = activeCfg.ModelMaxOutput
	ag.ReasoningContentKind = s.inferredReasoningKind(o.ID, turnProvider.ID, turnProvider.AccountID, turnProvider.Model)
	ag.CompactionProvider = compactionProvider
	ag.CompactionMaxOutput = compactionCfg.ModelMaxOutput
	_, agentProfile := activeProfileCfg(effectiveCfg)
	ag.CompactionAgentProfile = agentProfile + "/" + CategoryAeron
	ag.Precompaction = s.precompactionSlot(o.ID)
	ag.PriorCheckpointTokens = lastCheckpointTokens(s.CompactionRecords(o.ID))
	ag.AgentID = agent.AgentID(o.ID)
	ag.RunID = collaborationRun.ID
	ag.TurnGeneration = turnGeneration
	var compactionRevision uint64
	ag.PrepareCompaction = func(source agent.CompactionSource) error {
		var err error
		compactionRevision, err = s.beginRootCompaction(o.ID, source)
		return err
	}
	ag.CommitCompaction = func(commit agent.CompactionCommit) error {
		return s.commitRootCompaction(o.ID, compactionRevision, commit)
	}
	ag.Checkpoint = func(archive, active []provider.ChatMessage) error {
		recoveryState.snapshot(archive, active)
		return persistRecoveryBoundary(ctx, supervisor)
	}
	ag.CheckpointTools = func(archive, active, inFlight []provider.ChatMessage) error {
		recoveryState.snapshotTools(archive, active, inFlight)
		return persistRecoveryBoundary(ctx, supervisor)
	}
	ag.ContextCompactionPercent = agent.CompactionPercentSetting(activeCfg.ContextCompactionPercent)
	ag.CompactionRetentionTokens = agent.RetentionSetting(activeCfg.ContextRetentionTokens)
	// The provider-reported size of the last request seeds the projection of
	// the first one: local estimates can be off by half for dense content. The
	// new human turn is already admitted after that request's final reply.
	lastReply := -1
	for index := len(frozenPrefix) - 1; index >= 0; index-- {
		if frozenPrefix[index].Role == "assistant" {
			lastReply = index
			break
		}
	}
	if lastReply >= 0 && s.db != nil {
		if baseline, ok, baselineErr := sessiondb.LatestNormalUsageRecord(s.db, s.sessionID, o.ID); baselineErr != nil {
			l.Warn("COMPACT", "context_baseline_load_failed", "error", baselineErr.Error())
		} else if ok && baseline.ModelID == activeCfg.Model && baseline.ProviderID == turnProvider.ID && baseline.AccountID == turnProvider.AccountID {
			// Agent.Run prepends one system message, so the final reply sits at
			// index lastReply+1 of the new request; messages after it are new.
			ag.SeedContextUsage(baseline.ContextUsed, baseline.CompletionTokens, lastReply+1)
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
		if planMode {
			if err := planModeRefusal(tool, params, ag.ToolMetadata, s.logger); err != nil {
				return tools.ToolResult{}, err
			}
		}
		return baseToolFn(withDelegationOwner(toolCtx, o.ID, turnGeneration), tool, params)
	}
	ag.PrepareStep = func() ([]tools.ToolDef, []provider.ChatMessage) {
		// Publish the tool snapshot and its settings notices together, after
		// any concurrent save has completed its launch-admission barrier.
		s.delegationMu.Lock()
		defer s.delegationMu.Unlock()
		current := s.runtimeProfile.GetEffectiveConfig(o.ID)
		defs, notices := configuredOrchestratorTools(current, current.IsMultiAgentEnabled(), ag.Model), s.takeDelegationNotices()
		if planMode {
			defs = planModeTools(defs, ag.ToolMetadata)
		}
		if planModeEnded {
			planModeEnded = false
			notices = append(notices, planModeEndedNotice())
		}
		return defs, append(notices, s.takeGoalNotices()...)
	}
	s.bindAgentRuntime(ctx, ag, captypes.RuntimeActor{ID: o.ID, SessionID: s.sessionID, AllowedTools: activeCfg.AllowedTools, DeniedTools: activeCfg.DeniedTools, SkillModes: activeCfg.SkillModes}, s.AgentName())
	s.bindAgentControl(ctx, ag, userInput.ControlTarget)
	s.bindCodeIntelligence(ag)
	s.bindPlanDiscipline(ag)
	defer s.FileOwnership().Release(agent.AgentID(o.ID))
	var pendingCollaboration []collaboration.PendingDelivery
	defer func() {
		s.releaseCollaborationReservations(o.ID, o.RuntimeEpoch, pendingCollaboration, "turn ended before context admission")
	}()
	childrenDigest := ""
	hygieneDigest := ""
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
		if notice, digest := s.childrenStatusNotice(ctx, o.ID, childrenDigest); notice != nil {
			notices = append(notices, *notice)
			childrenDigest = digest
		}
		current := s.runtimeProfile.GetEffectiveConfig(o.ID)
		if notice, digest := s.executionHygieneNotice(ctx, current.Agents.ExecutionHygiene, hygieneDigest); notice != nil {
			notices = append(notices, *notice)
			hygieneDigest = digest
		} else {
			hygieneDigest = digest
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
		if _, err := s.commitCollaborationAdmissions(
			s.db, s.sessionID, s.sessionID, o.ID, collaborationRun.ID, o.RuntimeEpoch,
			s.contextRevision, collaborationDeltas(pendingCollaboration, contextMessages, o.ID), pendingCollaboration,
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
	var finalContent string
	prompt := s.systemPromptForModelAtRoot(effectiveCfg.IsMultiAgentEnabled(), planMode, ag.Model, ag.WorkDir)

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
	ag.SealRuntimeMessages = func(ctx context.Context) error {
		o.turnMu.Lock()
		o.turnSealed = true
		o.turnMu.Unlock()
		if flushRuntime != nil {
			return flushRuntime(ctx)
		}
		return nil
	}
	ag.ResumeRuntimeMessages = func() {
		o.turnMu.Lock()
		o.turnSealed = false
		o.turnMu.Unlock()
	}

	var emitActive atomic.Bool
	emitActive.Store(true)
	var terminalDone *agent.AgentEvent
	emit := func(evt agent.AgentEvent) {
		evt = eventSource.bind(evt)
		if evt.AgentID == "" || evt.AgentID == o.ID {
			goalProgress.observe(evt)
		}
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
			sendRuntimeEvent(sender, evt)
		}
		s.updateDisplayFromEvent(evt, displayTracker)
		if evt.Type == "usage" {
			s.processUsageEvent(evt.Data, activeCfg.Model, agentID, "")
			if goalProgress != nil {
				s.enforceGoalBudget()
			}
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
		o.turnMu.Lock()
		o.turnSealed = false
		o.turnMu.Unlock()
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
		ag.ContextCompactionPercent = agent.CompactionPercentSetting(category.ContextCompactionPercent)
		ag.CompactionRetentionTokens = agent.RetentionSetting(category.ContextRetentionTokens)
		turnIdentity = ttypes.TurnIdentity{
			Generation: turnGeneration, ProviderID: currentProvider.ID,
			AccountID: currentProvider.AccountID, ModelID: currentProvider.Model,
		}
		return s.systemPromptForModelAtRoot(current.IsMultiAgentEnabled(), planMode, ag.Model, ag.WorkDir), nil
	}
	runResult := ag.RunSupervised(
		historyCtx, contextMsgs, prompt, emit, o.BlockResolveCh, s.sessionID, runtimeMessages,
		agent.SupervisedRunOptions{
			Supervisor: supervisor,
			Prepare:    prepareAttempt,
			Snapshot:   recoveryState.snapshot,
			// A permanent provider verdict (refusal, auth, quota, schema) ends the
			// turn with its reason, as Codex and Claude Code do: the session is
			// idle and the next message continues. Waiting for Retry cannot fix
			// it. Local preparation faults keep waiting with their pending input.
			OnAttention: func(failure agent.RecoveryFailure) agent.AttentionDisposition {
				if failure.Kind == agent.RecoveryFailureConfiguration && failure.ProviderVerdict {
					return agent.AttentionEndTurn
				}
				return agent.AttentionWait
			},
			DeferSettlement: true,
		},
	)
	agentMsgs := runResult.Archive
	stopSteering()
	<-runtimeMessagesDone
	emitActive.Store(false)

	l.Info("AGENT", "loop_end", "len", len(finalContent))
	activeContext := runResult.Active
	s.msgsMu.Lock()
	committed := append([]provider.ChatMessage(nil), s.activeContext...)
	if retainsContextSource(committed, activeContext) {
		activeContext = committed
		for _, message := range s.msgs {
			if message.InternalID != "" && !slices.ContainsFunc(agentMsgs, func(existing provider.ChatMessage) bool { return existing.InternalID == message.InternalID }) {
				agentMsgs = append(agentMsgs, message)
			}
		}
	}
	s.msgsMu.Unlock()
	recoveryState.snapshot(agentMsgs, activeContext)
	finalize := func() error {
		s.msgsMu.Lock()
		err := s.reconcileRootRecoveryLocked(recoveryState)
		if err != nil {
			s.msgsMu.Unlock()
			return err
		}
		agentMsgs, activeContext = recoveryState.contexts()
		return s.finalizeTurnLocked(agentMsgs, persistedPrefixLen, displayTracker, finalContent, currentMsgID, turnIdentity, activeContext)
	}
	finalizeErr := finalize()
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
		finalizeErr = finalize()
	}
	s.msgsMu.Lock()
	recoveryState.snapshot(agentMsgs, s.activeContext)
	recoveryState.setContext(s.sessionID, s.contextRevision, s.contextGeneration, s.contextReason)
	s.msgsMu.Unlock()
	cancelled := false
	if terminalDone != nil {
		cancelled = terminalReasonFromEvent(*terminalDone) == "cancelled"
	}
	// Do not discard the recovery envelope or acknowledge success while the
	// durable turn/collaboration lifecycle still says running.
	if !recoverStep(agent.RecoveryFailureStorage, finishTurn) {
		return
	}
	if runResult.State == agent.SupervisedRunFailed {
		if err := supervisor.Fail(runResult.Failure); err != nil {
			status := supervisor.Park(agent.AttentionRecovery(agent.RecoveryFailureStorage, err))
			s.parkRecovery(recoveryState.checkpoint(status))
			recoveryParked = true
			return
		}
		turnFailure = supervisor.CheckpointStatus().Detail
	} else if cancelled {
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
		sendRuntimeEvent(sender, *terminalDone)
	}
	// A natural completion admits the next queued prompt; Stop never does.
	if !cancelled {
		go s.AdmitQueuedInputs(o.ID)
	}
}

func (s *Session) preserveUnstartedInput(request turnRequest, o *Orchestrator) {
	if s == nil || s.db == nil || o == nil || strings.TrimSpace(request.input.Text) == "" {
		return
	}
	if request.recovery != nil && request.recovery.InputAdmitted {
		return
	}
	input := cloneUserInput(request.input)
	if input.InternalID == "" {
		input.InternalID = newMessageID()
	}
	message := inputMessage(provider.NewTextMessage("user", input.Text), input)
	message.InternalID = input.InternalID
	message.DisplayContent = input.Text
	meta := canonicalizeDisplayMeta(message, sessiondb.DisplayMeta{
		DisplayRole: "user", DisplayContent: input.Text,
	}, "", 0, time.Now())
	createdAt := time.Now()
	s.msgsMu.Lock()
	_, err := s.publishRootContext(sessiondb.ContextPublication{Mutation: sessiondb.ContextMutation{Append: []sessiondb.ContextMessageDelta{
		contextDelta(message, meta, s.turnNumber, createdAt, o.ID),
	}}})
	if err == nil {
		s.msgs = append(s.msgs, message)
		s.msgTurns = append(s.msgTurns, s.turnNumber)
		s.displayMetas = append(s.displayMetas, meta)
		s.createdAts = append(s.createdAts, createdAt.UnixNano())
	}
	s.msgsMu.Unlock()
	if err != nil && s.logger != nil {
		s.logger.Error("INPUT", "preserve_unstarted_failed", "agent_id", o.ID, "error", err.Error())
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
			evt.Type, evt.AgentID = "blocking_auto", agentID
			sendRuntimeEvent(sender, evt)
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
					evt.Type, evt.AgentID = "blocking_auto", agentID
					sendRuntimeEvent(sender, evt)
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
	evt.AgentID = agentID
	sendRuntimeEvent(sender, evt)
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
) (resultErr error) {
	s.msgsMu.Lock()
	return s.finalizeTurnLocked(agentMsgs, contextPrefixLen, tracker, finalContent, msgID, turnIdentity, activeContext...)
}

// finalizeTurnLocked owns release of msgsMu, including before the off-UI memory
// mirror. Root reconciliation and this publication must share one lock boundary:
// a control admitted between them would otherwise strand an already stopped run.
func (s *Session) finalizeTurnLocked(
	agentMsgs []provider.ChatMessage,
	contextPrefixLen int,
	tracker *displayMetaTracker,
	finalContent string,
	msgID string,
	turnIdentity ttypes.TurnIdentity,
	activeContext ...[]provider.ChatMessage,
) (resultErr error) {
	previousMessages := append([]provider.ChatMessage(nil), s.msgs...)
	previousTurns := append([]int(nil), s.msgTurns...)
	previousMetas := append([]sessiondb.DisplayMeta(nil), s.displayMetas...)
	previousCreated := append([]int64(nil), s.createdAts...)
	previousActive := append([]provider.ChatMessage(nil), s.activeContext...)
	previousRevision, previousGeneration, previousReason := s.contextRevision, s.contextGeneration, s.contextReason
	locked := true
	defer func() {
		if !locked {
			return
		}
		if resultErr != nil {
			s.msgs = previousMessages
			s.msgTurns = previousTurns
			s.displayMetas = previousMetas
			s.createdAts = previousCreated
			s.activeContext = previousActive
			s.contextRevision, s.contextGeneration, s.contextReason = previousRevision, previousGeneration, previousReason
		}
		s.msgsMu.Unlock()
	}()
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
	existing := make(map[string]provider.ChatMessage, len(s.msgs))
	for _, message := range s.msgs {
		if message.InternalID != "" {
			existing[message.InternalID] = message
		}
	}
	additions := make([]provider.ChatMessage, 0, len(returned)-contextPrefixLen)
	for i := contextPrefixLen; i < len(returned); i++ {
		message := returned[i]
		if earlier, found := existing[message.InternalID]; found {
			if !reflect.DeepEqual(canonicalContextMessage(message), canonicalContextMessage(earlier)) {
				return &sessiondb.ContextMessageConflictError{InternalID: message.InternalID, Location: "live transcript"}
			}
			continue
		}
		if message.InternalID == "" {
			message.InternalID = newMessageID()
			returned[i].InternalID = message.InternalID
		}
		additions = append(additions, message)
	}
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
	additionCreated := make([]time.Time, 0, len(additions))
	for i, m := range additions {
		createdAt := time.Now()
		if m.InternalID == "" {
			m.InternalID = newMessageID()
			additions[i].InternalID = m.InternalID
		}
		additionTurns = append(additionTurns, s.turnNumber)
		var dm sessiondb.DisplayMeta
		if isProviderControlMessage(m) {
			dm = sessiondb.DisplayMeta{
				SnapshotVersion: sessiondb.ConversationSnapshotVersion,
				DisplayRole:     "hidden_wire_context",
				ContextOnly:     true,
			}
			additionMetas = append(additionMetas, dm)
			additionCreated = append(additionCreated, createdAt)
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
			dm = canonicalizeDisplayMeta(m, dm, rawToolArgs[m.ToolCallID], len(s.msgs)+i, createdAt)
		}
		additionMetas = append(additionMetas, dm)
		additionCreated = append(additionCreated, createdAt)
	}

	if len(additions) > 0 {
		s.msgs = append(s.msgs, additions...)
		s.msgTurns = append(s.msgTurns, additionTurns...)
		s.displayMetas = append(s.displayMetas, additionMetas...)
		for _, when := range additionCreated {
			s.createdAts = append(s.createdAts, when.UnixNano())
		}
	}
	nextContext := returned
	if len(activeContext) > 0 && activeContext[0] != nil {
		nextContext = activeContext[0]
	}
	if s.contextGeneration == 0 {
		s.contextGeneration = 1
	}
	if s.contextReason == "" {
		s.contextReason = "session_start"
	}
	nextContext = canonicalContextMessages(inheritContextMessageIDs(canonicalContextMessages(nextContext), canonicalContextMessages(returned)))
	if !retainsContextSource(nextContext, s.activeContext) {
		return fmt.Errorf("finalization does not retain committed context revision %d", s.contextRevision)
	}
	nextCheckpoint := contextCheckpointContent(nextContext)
	checkpointChanged := nextCheckpoint != "" && contextCheckpointContent(s.activeContext) != nextCheckpoint
	expectedAppendContext := append(append([]provider.ChatMessage(nil), s.activeContext...), canonicalContextMessages(additions)...)
	contextChanged := !reflect.DeepEqual(nextContext, expectedAppendContext)
	if checkpointChanged {
		s.contextGeneration++
		s.contextReason = "automatic_compaction"
		contextChanged = true
	}
	s.activeContext = append([]provider.ChatMessage(nil), nextContext...)
	contextRevision := s.contextRevision
	deltas := make([]sessiondb.ContextMessageDelta, 0, len(additions))
	for i, message := range additions {
		deltas = append(deltas, contextDelta(message, additionMetas[i], additionTurns[i], additionCreated[i], s.sessionID))
	}

	turnMsgs := s.messagesForTurnLocked(s.turnNumber)
	turnNumber := s.turnNumber

	if s.db != nil {
		publication := sessiondb.ContextPublication{SessionID: s.sessionID, AgentID: s.sessionID, ExpectedRevision: contextRevision}
		if contextChanged {
			for i, delta := range deltas {
				if !additionMetas[i].ContextOnly {
					publication.TranscriptAppend = append(publication.TranscriptAppend, delta)
				}
			}
			publication.Mutation.Replace = &sessiondb.ContextReplacement{Generation: s.contextGeneration, Reason: s.contextReason, Messages: append([]provider.ChatMessage(nil), nextContext...)}
			publication.ArchivePrevious = checkpointChanged
		} else {
			publication.Mutation.Append = deltas
		}
		state, err := sessiondb.PublishContext(s.db, publication)
		if err != nil {
			s.logger.Error("COMPACT", "context_state_persist_failed", "error", err.Error())
			return err
		}
		s.contextRevision, s.contextGeneration, s.contextReason = state.Revision, state.Generation, state.Reason
		s.activeContext = append([]provider.ChatMessage(nil), state.Messages...)
	} else if len(s.msgs) > 0 {
		return errors.New("session database is unavailable")
	}
	s.msgsMu.Unlock()
	locked = false

	if err := s.afterTurnMemory(turnNumber, turnMsgs, finalContent); err != nil {
		return err
	}

	if finalContent != "" {
		s.logger.Info("CONV", "assistant_msg", "msg_id", msgID, "len", len(finalContent))
	}
	return nil
}

// Agent request projection may copy canonical messages before locally assigned
// transcript IDs are attached. When the returned active context is otherwise
// byte-for-byte the expected append, retain those authoritative IDs so ordinary
// finalization stays an append rather than falsely replacing immutable history.
func inheritContextMessageIDs(actual, expected []provider.ChatMessage) []provider.ChatMessage {
	if len(actual) != len(expected) {
		return actual
	}
	result := append([]provider.ChatMessage(nil), actual...)
	for i := range result {
		if result[i].InternalID != "" || expected[i].InternalID == "" {
			continue
		}
		candidate := result[i]
		candidate.InternalID = expected[i].InternalID
		if reflect.DeepEqual(candidate, expected[i]) {
			result[i].InternalID = expected[i].InternalID
		}
	}
	return result
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

type preparedInput struct {
	Message          provider.ChatMessage
	ReferenceLabels  []string
	Enrichment       []provider.ChatMessage
	AttachmentIssues []string
}

// prepareInput never changes the submitted row. Its suffix is published with
// that row at admission, or appended once when an already-admitted turn starts.
func (s *Session) prepareInput(ctx context.Context, input provider.UserInput, cfg *Config, o *Orchestrator, retained ...provider.ChatMessage) (preparedInput, error) {
	result := preparedInput{Message: inputMessage(provider.NewTextMessage("user", input.Text), input)}
	result.Message.InternalID = input.InternalID
	if input.InternalID == "" {
		return result, errors.New("input preparation requires the immutable submitted input identity")
	}
	// A preflight Continue may follow a settings repair while its caller still
	// holds the old snapshot. Validate against the exact owning agent's selection.
	if o != nil && s.runtimeProfile != nil {
		cfg = s.runtimeProfile.GetEffectiveConfig(o.ID)
	}
	if cfg == nil {
		return result, errors.New("input preparation requires the selected session configuration")
	}
	if err := cfg.ValidateSelectedAgent(); err != nil {
		return result, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	category := cfg.GetActiveDefaultCategory()
	var references, turnContext *provider.ChatMessage
	for i := range retained {
		message := &retained[i]
		var target **provider.ChatMessage
		switch message.InternalID {
		case input.InternalID + "-references":
			target = &references
		case input.InternalID + "-context":
			target = &turnContext
		default:
			continue
		}
		if message.Role != "user" || message.EffectiveOrigin() != provider.OriginRuntimeContext || message.EffectiveVisibility() != provider.VisibilityContextOnly {
			return result, fmt.Errorf("input %q has invalid retained preparation ownership", input.InternalID)
		}
		if *target != nil && (*target).Content != message.Content {
			return result, fmt.Errorf("input %q has conflicting immutable preparation data", input.InternalID)
		}
		*target = message
	}
	var message provider.ChatMessage
	var err error
	if len(input.ReferenceIDs) > 0 {
		if references != nil {
			message = *references
		} else {
			if turnContext != nil {
				return result, fmt.Errorf("input %q is missing its captured selected references; restore the exact preparation before Continue", input.InternalID)
			}
			selection := input
			selection.Text = ""
			message, result.ReferenceLabels, result.AttachmentIssues, err = s.buildUserMessageWithDisplay(selection, category.Model)
			if err != nil {
				return result, err
			}
			message.InternalID = input.InternalID + "-references"
			message.Origin, message.Visibility = provider.OriginRuntimeContext, provider.VisibilityContextOnly
			message.Collaboration = nil
		}
		// Capture precedes capability validation: an unsupported selection still
		// has an immutable preimage for repair, Stop and passive resume.
		result.Enrichment = append(result.Enrichment, message)
		var connection *provider.Provider
		for _, part := range message.Content.Parts() {
			modality := ""
			switch part.Type {
			case "image_url":
				modality = provider.ModalityImage
			case "document":
				modality = provider.ModalityPDF
			case "audio":
				modality = provider.ModalityAudio
			case "video":
				modality = provider.ModalityVideo
			}
			if modality == "" {
				continue
			}
			supported, known := provider.ModalityCapability(category.Model, modality)
			if category.ModelModalities != nil {
				supported, known = slices.Contains(category.ModelModalities, modality), true
			}
			if known && !supported {
				return result, fmt.Errorf("selected %s attachment is unsupported by model %q; enable its modality or select a supported model before Continue", modality, category.Model)
			}
			if connection == nil {
				connection, err = cfg.providerForCategory(category)
				if err != nil {
					return result, err
				}
			}
			if !provider.ProtocolSupportsMedia(connection.ProtocolID(), modality, part.MimeType) {
				return result, fmt.Errorf("selected %s attachment (%s) is unsupported by protocol %q; select a compatible connection or convert the attachment before Continue", modality, part.MimeType, connection.ProtocolID())
			}
		}
	} else if references != nil {
		return result, fmt.Errorf("input %q has captured references without a selected reference identity", input.InternalID)
	}
	if turnContext != nil {
		result.Enrichment = append(result.Enrichment, *turnContext)
		return result, nil
	}
	parts := append([]string(nil), input.ContextParts...)
	if input.EffectiveOrigin() == provider.OriginHuman {
		currentPlan, err := s.activePlanTurnContext(ctx)
		if err != nil {
			return result, fmt.Errorf("prepare current plan: %w", err)
		}
		if currentPlan != "" {
			parts = append(parts, currentPlan)
		}
		recalled, err := s.recallMemoryContext(ctx, input.Text, input.ActivePaths)
		if err != nil {
			return result, err
		}
		if recalled != "" {
			parts = append(parts, recalled)
		}
		if !s.prevContextInjected.Load() {
			previous, err := s.previousSessionContext(ctx)
			if err != nil {
				return result, err
			}
			if previous != "" {
				parts = append(parts, previous)
			}
		}
	}
	var runtimeParts []string
	if s.asyncMgr != nil {
		if status := agent.BuildAsyncCommandStatus(s.asyncMgr); status != "" {
			runtimeParts = append(runtimeParts, status)
		}
	}
	if o != nil {
		if runner := o.subAgentRunner(); runner != nil {
			if status := agent.BuildSubAgentStatus(runner.Manager()); status != "" {
				runtimeParts = append(runtimeParts, status)
			}
		}
	}
	if len(runtimeParts) > 0 {
		parts = append(parts, "## Runtime Status\n\n"+strings.Join(runtimeParts, "\n\n"))
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	// Even an empty snapshot marks preparation complete, avoiding mutable rereads
	// after Stop/Continue or passive resume of this same input identity.
	message = provider.NewTextMessage("user", rootprompt.TurnContext(strings.Join(parts, "\n\n")))
	message.InternalID = input.InternalID + "-context"
	message.Origin, message.Visibility = provider.OriginRuntimeContext, provider.VisibilityContextOnly
	result.Enrichment = append(result.Enrichment, message)
	return result, nil
}

func (s *Session) buildUserMessageWithDisplay(input provider.UserInput, model string) (provider.ChatMessage, []string, []string, error) {
	if len(input.ReferenceIDs) == 0 {
		return inputMessage(provider.NewTextMessage("user", input.Text), input), nil, nil, nil
	}
	if s.db == nil {
		return provider.ChatMessage{}, nil, nil, errors.New("selected references cannot be prepared: session database is unavailable; restore storage before Continue")
	}
	refs, err := sessiondb.LoadPromptReferencesByIDs(s.db, input.ReferenceIDs)
	if err != nil {
		return provider.ChatMessage{}, nil, nil, fmt.Errorf("load selected references before Continue: %w", err)
	}
	byID := make(map[string]sessiondb.Reference, len(refs))
	for _, ref := range refs {
		byID[ref.ID] = ref
	}
	seen := make(map[string]bool, len(input.ReferenceIDs))
	var labels, issues []string
	var parts []provider.ContentPart
	omit := func(ref sessiondb.Reference, reason string) {
		labels = append(labels, ref.DisplayName())
		issues = append(issues, fmt.Sprintf("Attachment %s (%s) excluded: %s. Original message preserved; work continues.", ref.DisplayName(), ref.ID, reason))
		parts = append(parts, provider.ContentPart{Type: "text", Text: rootprompt.UnavailableAttachment(ref.ID, ref.DisplayName(), reason)})
	}
	if input.Text != "" {
		parts = append(parts, provider.ContentPart{Type: "text", Text: input.Text})
	}
referencesLoop:
	for _, id := range input.ReferenceIDs {
		if seen[id] {
			continue
		}
		seen[id] = true
		ref, found := byID[id]
		if !found || !ref.Active() {
			return provider.ChatMessage{}, nil, nil, fmt.Errorf("selected reference %q is missing or inactive; restore that exact reference before Continue", id)
		}
		if s.sessionID != "" && ref.SessionID != s.sessionID {
			return provider.ChatMessage{}, nil, nil, fmt.Errorf("selected reference %q belongs to another session; reselect it for this input owner", id)
		}
		var media *provider.ContentPart
		switch ref.Kind {
		case sessiondb.KindPastedText, sessiondb.KindMCPTool, sessiondb.KindMCPContext:
			if ref.Content == "" {
				return provider.ChatMessage{}, nil, nil, fmt.Errorf("selected reference %q has no captured content; restore its preview before Continue", id)
			}
			if (ref.Kind == sessiondb.KindMCPTool || ref.Kind == sessiondb.KindMCPContext) && ref.Title == "" {
				return provider.ChatMessage{}, nil, nil, fmt.Errorf("selected MCP reference %q has no exact source identity; restore its preview before Continue", id)
			}
		case sessiondb.KindPastedImage, sessiondb.KindPastedTextFile, sessiondb.KindSelectedFile:
			var info os.FileInfo
			if ref.FilePath != "" && (ref.Kind == sessiondb.KindPastedImage || ref.Content == "") {
				info, err = os.Lstat(ref.FilePath)
				if err != nil {
					if ref.Kind == sessiondb.KindPastedImage && os.IsNotExist(err) {
						omit(ref, "The captured image file is missing")
						continue referencesLoop
					}
					return provider.ChatMessage{}, nil, nil, fmt.Errorf("selected reference %q is unreadable; restore %q before Continue: %w", id, ref.FilePath, err)
				}
				if !info.Mode().IsRegular() {
					return provider.ChatMessage{}, nil, nil, fmt.Errorf("selected reference %q must be a regular file; repair %q before Continue", id, ref.FilePath)
				}
			}
			modality, mime, isMedia := "", "", false
			if ref.Kind == sessiondb.KindPastedImage {
				modality, mime, isMedia = provider.ModalityImage, ref.Mime, true
			} else if ref.Kind == sessiondb.KindSelectedFile && ref.Content == "" && ref.FilePath != "" {
				modality, mime, isMedia = provider.MediaModalityFromPath(ref.FilePath)
			}
			limit := provider.MaxTextFileSizeFor(model)
			if isMedia {
				limit = provider.MaxImageFileSizeFor(model)
			}
			var data []byte
			if info != nil {
				if info.Size() > limit {
					return provider.ChatMessage{}, nil, nil, fmt.Errorf("selected reference %q exceeds the %d-byte model limit; repair %q before Continue", id, limit, ref.FilePath)
				}
				if isMedia {
					file, err := os.Open(ref.FilePath)
					if err != nil {
						return provider.ChatMessage{}, nil, nil, fmt.Errorf("read selected reference %q before Continue: %w", id, err)
					}
					data, err = io.ReadAll(io.LimitReader(file, limit+1))
					err = errors.Join(err, file.Close())
					if err != nil {
						return provider.ChatMessage{}, nil, nil, fmt.Errorf("read selected reference %q before Continue: %w", id, err)
					}
					if int64(len(data)) > limit {
						return provider.ChatMessage{}, nil, nil, fmt.Errorf("selected reference %q grew beyond its %d-byte model limit; repair its capture before Continue", id, limit)
					}
				} else if ext := strings.ToLower(filepath.Ext(ref.FilePath)); docconv.SupportedExt(ext) {
					markdown, metadata, err := docconv.ConvertFile(ref.FilePath)
					if err != nil {
						return provider.ChatMessage{}, nil, nil, fmt.Errorf("convert selected reference %q before Continue: %w", id, err)
					}
					ref.Content = docconv.Header(ref.FilePath, metadata) + markdown
				} else {
					preview, err := fileio.InspectFile(ref.FilePath, limit, fileio.DefaultHexHeadBytes)
					if err != nil {
						return provider.ChatMessage{}, nil, nil, fmt.Errorf("read selected reference %q before Continue: %w", id, err)
					}
					if preview.Truncated {
						return provider.ChatMessage{}, nil, nil, fmt.Errorf("selected reference %q grew beyond its %d-byte model limit; repair its capture before Continue", id, limit)
					}
					ref.Content = preview.ReadableText()
				}
			} else if isMedia && ref.Content != "" {
				if int64(len(ref.Content)) > provider.MaxInlineContentSizeFor(model) {
					return provider.ChatMessage{}, nil, nil, fmt.Errorf("selected reference %q exceeds the inline media limit; repair its captured media before Continue", id)
				}
				data, err = base64.StdEncoding.DecodeString(ref.Content)
				if err != nil {
					if ref.Kind == sessiondb.KindPastedImage {
						omit(ref, "The captured image encoding is invalid")
						continue referencesLoop
					}
					return provider.ChatMessage{}, nil, nil, fmt.Errorf("selected reference %q contains invalid Base64 media; repair its capture before Continue: %w", id, err)
				}
			} else if ref.Content == "" {
				if ref.Kind == sessiondb.KindPastedImage {
					omit(ref, "The image capture is empty")
					continue referencesLoop
				}
				return provider.ChatMessage{}, nil, nil, fmt.Errorf("selected reference %q has no readable content or file; restore its capture before Continue", id)
			}
			if isMedia {
				if len(data) == 0 && ref.Kind == sessiondb.KindPastedImage {
					omit(ref, "The image capture is empty")
					continue referencesLoop
				}
				if len(data) == 0 || int64(len(data)) > limit {
					return provider.ChatMessage{}, nil, nil, fmt.Errorf("selected reference %q has empty or oversized media; repair its capture before Continue", id)
				}
				if mime == "" {
					mime = http.DetectContentType(data)
				}
				part := provider.ContentPart{MimeType: mime, Filename: filepath.Base(ref.FilePath)}
				switch modality {
				case provider.ModalityImage:
					if err := provider.ValidateImageBytes(mime, data); err != nil {
						omit(ref, err.Error())
						continue referencesLoop
					}
					part.Type, part.ImageURL = "image_url", &provider.ImageURL{URL: "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)}
				case provider.ModalityPDF:
					part.Type, part.Data = "document", data
				case provider.ModalityAudio:
					part.Type, part.Data = "audio", data
				case provider.ModalityVideo:
					part.Type, part.Data = "video", data
				}
				media = &part
				ref.Content = ""
			}
		default:
			return provider.ChatMessage{}, nil, nil, fmt.Errorf("selected reference %q has unsupported kind %q; repair its capture before Continue", id, ref.Kind)
		}
		if !utf8.ValidString(ref.Content) || int64(len(ref.Content)) > provider.MaxTextFileSizeFor(model) {
			return provider.ChatMessage{}, nil, nil, fmt.Errorf("selected reference %q requires complete UTF-8 text within the model limit; repair its capture before Continue", id)
		}
		labels = append(labels, ref.DisplayName())
		parts = append(parts, provider.ContentPart{Type: "text", Text: ref.AgentText()})
		if media != nil {
			parts = append(parts, *media)
		}
	}
	return inputMessage(provider.NewMultiPartMessage("user", parts), input), labels, issues, nil
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
