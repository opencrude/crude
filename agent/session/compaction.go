package session

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	rand2 "math/rand/v2"
	"sync/atomic"
	"time"

	rootprompt "crude/agent/prompt"
	"crude/agent/provider"
	"crude/agent/sessiondb"
	"crude/agent/turnloop"
	"crude/types/conversation"
)

const (
	compactionStageCount = 4
	compactionRetryLimit = 3
)

// User-visible outcomes of a human compaction request. The session owns
// this wording; shells only present it.
const (
	compactionRequestedText = "Compaction will start after the current step."
	compactionPendingText   = "Compaction is already requested. It will start after the current step."
	compactionRunningText   = "Context compaction is already running."
	compactionNothingText   = "Nothing to compact yet."
	compactionClosingText   = "This session is closing. Context was not compacted."
	compactionReservedText  = "Crude is stopping this session. Context was not compacted."
)

// PreparedCompaction is the session's single admission decision for a human
// compaction request. A human request is never refused because work only looks
// busy: background commands, children and a finishing turn do not block it.
//
//   - Run set: the session registered an exclusive operation. Run must execute
//     exactly once; it waits for any finishing turn, then compacts. Started is
//     its boundary, which a core may reduce before Run begins.
//   - Run nil: the result already states the outcome. OperationID set with a
//     Desc means the active turn accepted the request and compacts before its
//     next model request (or exclusively when it ends first).
type PreparedCompaction struct {
	OperationID string
	AgentID     string
	ProviderID  string
	AccountID   string
	ModelID     string
	Started     map[string]any
	Run         func() conversation.CompactResultMsg
	Cancel      func()
}

func newCompactionOperationID() string {
	return fmt.Sprintf("compact-%016x%016x", rand2.Uint64(), rand2.Uint64())
}

// PrepareCompaction reserves idle work and returns a run that compacts the transcript.
func (s *Session) PrepareCompaction(agentID string) (PreparedCompaction, conversation.CompactResultMsg) {
	if s == nil || atomic.LoadInt32(&s.closing) != 0 {
		return PreparedCompaction{}, conversation.CompactResultMsg{Error: compactionClosingText}
	}
	release, ok := s.beginIdleWork()
	if !ok {
		return PreparedCompaction{}, conversation.CompactResultMsg{Error: compactionReservedText}
	}
	defer release()
	if agentID == "" {
		agentID = s.sessionID
	}
	if s.compactionActive(agentID) {
		return PreparedCompaction{}, conversation.CompactResultMsg{Desc: compactionRunningText}
	}
	s.orchMu.Lock()
	orchestrator := s.orchestrators[agentID]
	s.orchMu.Unlock()
	operationID := newCompactionOperationID()
	// A running turn owns the context; it compacts at its next safe boundary.
	if id, handed := orchestrator.requestCompaction(operationID); handed {
		return PreparedCompaction{}, requestedCompactionResult(id, operationID)
	}
	if !s.hasActiveContext() {
		return PreparedCompaction{}, conversation.CompactResultMsg{Desc: compactionNothingText}
	}
	prepared, registered := s.registerCompaction(agentID, operationID, turnloop.CompactionTriggerManual)
	if !registered {
		return PreparedCompaction{}, conversation.CompactResultMsg{Desc: compactionRunningText}
	}
	// A turn reserved before the registration saw no compaction; hand the
	// request to it. Later reservations see the registration and wait.
	if id, handed := orchestrator.requestCompaction(operationID); handed {
		s.releaseUnstartedCompaction(operationID)
		return PreparedCompaction{}, requestedCompactionResult(id, operationID)
	}
	return prepared, conversation.CompactResultMsg{}
}

// hasActiveContext reports whether there is anything to compact. Durable
// state is the authority; it is read only when the live mirror is empty.
func (s *Session) hasActiveContext() bool {
	s.msgsMu.Lock()
	live := len(s.activeContext) != 0
	s.msgsMu.Unlock()
	if live || s.db == nil {
		return live
	}
	state, found, err := sessiondb.LoadContextState(s.db, s.sessionID, s.sessionID)
	// A load failure is reported by the compaction itself, never as "nothing".
	return err != nil || found && len(state.Messages) != 0
}

func requestedCompactionResult(id, requested string) conversation.CompactResultMsg {
	if id != requested {
		return conversation.CompactResultMsg{OperationID: id, Desc: compactionPendingText}
	}
	return conversation.CompactResultMsg{OperationID: id, Desc: compactionRequestedText}
}

// registerCompaction reserves the session's one exclusive compaction. From
// here on new turns and queued input wait for it, and Stop or Close cancels it.
func (s *Session) registerCompaction(agentID, operationID, trigger string) (PreparedCompaction, bool) {
	ctx, cancel := context.WithCancel(context.Background())
	s.compactionMu.Lock()
	if atomic.LoadInt32(&s.closing) != 0 || s.compactionCancel != nil {
		s.compactionMu.Unlock()
		cancel()
		return PreparedCompaction{}, false
	}
	s.compactionCancel = cancel
	s.compactionID = operationID
	s.compactionAgent = agentID
	s.compactionStarted = false
	s.compactionWg.Add(1)
	s.compactionMu.Unlock()

	prepared := PreparedCompaction{OperationID: operationID, AgentID: agentID, Cancel: cancel}
	prepared.ProviderID, prepared.AccountID, prepared.ModelID = s.compactionIdentity(agentID)
	prepared.Started = compactionLifecycleData(operationID, trigger, "root", 0, 0, 0, 0)
	prepared.Started["provider_id"] = prepared.ProviderID
	prepared.Started["model_id"] = prepared.ModelID
	started := prepared.Started
	prepared.Run = func() conversation.CompactResultMsg {
		return s.runExclusiveCompaction(ctx, cancel, operationID, agentID, trigger, started)
	}
	return prepared, true
}

// compactionIdentity is the connection that writes the agent's checkpoints.
func (s *Session) compactionIdentity(agentID string) (providerID, accountID, modelID string) {
	if s.cfg == nil {
		return "", "", ""
	}
	effectiveCfg := s.cfg
	if s.runtimeProfile != nil {
		effectiveCfg = s.runtimeProfile.EffectiveConfig(agentID)
	}
	main := effectiveCfg.ActiveDefaultCategory()
	if p, err := effectiveCfg.providerForCategory(main); err == nil && p.UsesNativeCompaction() {
		return p.ID, p.AccountID, main.Model
	}
	compactionCfg := effectiveCfg.CategoryConfig(CategoryErrand)
	return effectiveCfg.providerIDForAccount(compactionCfg.AccountID), compactionCfg.AccountID, compactionCfg.Model
}

// releaseUnstartedCompaction withdraws a registration whose Run never began.
func (s *Session) releaseUnstartedCompaction(operationID string) bool {
	s.compactionMu.Lock()
	defer s.compactionMu.Unlock()
	if s.compactionID != operationID || s.compactionCancel == nil || s.compactionStarted {
		return false
	}
	s.compactionCancel()
	s.compactionCancel = nil
	s.compactionID = ""
	s.compactionAgent = ""
	s.compactionWg.Done()
	return true
}

// runExclusiveCompaction waits for exclusive context ownership (a finishing or
// stopping turn releases it), then compacts the settled context.
func (s *Session) runExclusiveCompaction(
	ctx context.Context,
	cancel context.CancelFunc,
	operationID, agentID, trigger string,
	started map[string]any,
) conversation.CompactResultMsg {
	s.compactionMu.Lock()
	if s.compactionID != operationID || s.compactionCancel == nil || s.compactionStarted {
		s.compactionMu.Unlock()
		return conversation.CompactResultMsg{OperationID: operationID, Cancelled: true}
	}
	s.compactionStarted = true
	s.compactionMu.Unlock()
	gateHeld := false
	defer func() {
		completed := ctx.Err() == nil
		cancel()
		s.compactionMu.Lock()
		if s.compactionID == operationID {
			s.compactionCancel = nil
			s.compactionID = ""
			s.compactionAgent = ""
			s.compactionStarted = false
		}
		s.compactionMu.Unlock()
		if gateHeld {
			s.turnGate.Unlock()
		}
		s.compactionWg.Done()
		// Stop/close preserves queued work without launching another turn.
		if completed {
			go s.AdmitQueuedInputs(agentID)
		}
	}()
	emit := s.compactionEmitter(agentID, nil)
	emit(turnloop.AgentEvent{Type: "context_compaction_started", Data: started})
	if err := s.turnGate.LockContext(ctx); err != nil {
		emit(turnloop.AgentEvent{Type: "context_compaction_cancelled", Data: started})
		return conversation.CompactResultMsg{OperationID: operationID, Cancelled: true}
	}
	gateHeld = true

	s.msgsMu.Lock()
	active := append([]provider.ChatMessage(nil), s.activeContext...)
	revision := s.contextRevision
	generation := s.contextGeneration
	s.msgsMu.Unlock()
	if s.db != nil {
		state, found, err := sessiondb.LoadContextState(s.db, s.sessionID, s.sessionID)
		if err != nil {
			return s.finishManualCompactionError(ctx, emit, operationID, trigger, turnloop.CompactResult{}, fmt.Errorf("load current context: %w", err))
		}
		if found {
			revision, generation = state.Revision, state.Generation
			active = append([]provider.ChatMessage(nil), state.Messages...)
			s.msgsMu.Lock()
			s.contextRevision, s.contextGeneration, s.contextReason = state.Revision, state.Generation, state.Reason
			s.activeContext = append([]provider.ChatMessage(nil), state.Messages...)
			s.msgsMu.Unlock()
		}
	}
	if len(active) == 0 {
		emit(turnloop.AgentEvent{Type: "context_compaction_cancelled", Data: started})
		return conversation.CompactResultMsg{OperationID: operationID, Desc: compactionNothingText}
	}
	return s.runPreparedCompaction(ctx, operationID, agentID, trigger, active, revision, generation, len(active))
}

// startTurnEndCompaction runs a request the turn accepted but ended before
// taking. The caller still owns the turn, so no other turn can start between
// the turn's end and this registration.
func (s *Session) startTurnEndCompaction(agentID, operationID string) func() {
	prepared, registered := s.registerCompaction(agentID, operationID, turnloop.CompactionTriggerManual)
	if !registered {
		s.settleDroppedCompaction(agentID, operationID)
		return func() {}
	}
	return func() {
		go func() {
			if result := prepared.Run(); result.Error != "" {
				s.logger.Warn("COMPACT", "turn_end_failed", "operation_id", operationID, "error", result.Error)
			}
		}()
	}
}

// isErrandSession reports whether this is the workspace's permanent Errand.
func (s *Session) isErrandSession() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.selectedMainAgent == CategoryErrand
}

// loopEndCompactionMinimumTokens is the least conversation (excluding the
// fixed system prompt and tools) worth a loop-end checkpoint. A smaller
// context is already about the size of a checkpoint plus its runtime
// continuity, so compacting it would not make Errand smaller.
const loopEndCompactionMinimumTokens = 8192

func (s *Session) loopEndCompactionWorthwhile(agentID string) bool {
	model := ""
	if _, _, id := s.compactionIdentity(agentID); id != "" {
		model = id
	}
	s.msgsMu.Lock()
	defer s.msgsMu.Unlock()
	total := 0
	for _, m := range s.activeContext {
		if total += provider.EstimateMessageTokens(m, model); total >= loopEndCompactionMinimumTokens {
			return true
		}
	}
	return false
}

// startLoopEndCompaction compacts the workspace Errand after its agentic loop
// ends naturally. Errand is permanent, so each finished loop leaves a small
// checkpoint instead of a growing transcript. Like a requested compaction it
// registers while the finishing turn still owns admission. A compaction that
// is already registered (a human /compact) makes this one unnecessary.
func (s *Session) startLoopEndCompaction(agentID string) func() {
	if !s.loopEndCompactionWorthwhile(agentID) {
		return func() {}
	}
	prepared, registered := s.registerCompaction(agentID, newCompactionOperationID(), turnloop.CompactionTriggerLoopEnd)
	if !registered {
		return func() {}
	}
	return func() {
		go func() {
			if result := prepared.Run(); result.Error != "" {
				s.logger.Warn("COMPACT", "loop_end_failed", "operation_id", prepared.OperationID, "error", result.Error)
			}
		}()
	}
}

// settleDroppedCompaction records a requested compaction that will not run
// because its turn stopped or failed first, so the request never vanishes.
func (s *Session) settleDroppedCompaction(agentID, operationID string) {
	if operationID == "" {
		return
	}
	s.compactionEmitter(agentID, nil)(turnloop.AgentEvent{Type: "context_compaction_cancelled", Data: compactionLifecycleData(operationID, "requested", "root", 0, 0, 0, 0)})
}

// compactionEmitter persists a compaction lifecycle event and delivers it to
// every shell. usageModel prices extraction usage when the caller has one.
func (s *Session) compactionEmitter(agentID string, usageModel *string) func(turnloop.AgentEvent) {
	sender := s.orchestratorSender(agentID)
	return func(event turnloop.AgentEvent) {
		event.AgentID = agentID
		if err := s.persistCompactionEvent(s.db, s.sessionID, agentID, event); err != nil {
			s.logger.Warn("COMPACT", "lifecycle_persist_failed", "agent_id", agentID, "error", err.Error())
		}
		if event.Type == "usage" && usageModel != nil {
			s.processUsageEvent(event.Data, *usageModel, agentID, "")
		}
		if sender != nil {
			sender.SendAgentEventWithID(event.Type, event.Data, agentID)
		}
	}
}

func (s *Session) stopCompactionForClose() {
	if s == nil {
		return
	}
	s.compactionMu.Lock()
	if s.compactionCancel == nil {
		s.compactionMu.Unlock()
		return
	}
	s.compactionCancel()
	if !s.compactionStarted {
		s.compactionCancel = nil
		s.compactionID = ""
		s.compactionAgent = ""
		s.compactionWg.Done()
	}
	s.compactionMu.Unlock()
}

func (s *Session) runPreparedCompaction(
	ctx context.Context,
	operationID, agentID, trigger string,
	active []provider.ChatMessage,
	revision uint64,
	generation uint64,
	beforeMessages int,
) conversation.CompactResultMsg {
	s.logger.Info("COMPACT", "manual_request", "operation_id", operationID)
	// The checkpoint covers the complete current source; an older background
	// preparation can never be adopted after it.
	s.precompactionMu.Lock()
	slot := s.precompaction[agentID]
	s.precompactionMu.Unlock()
	slot.Discard()
	usageModel := ""
	emit := s.compactionEmitter(agentID, &usageModel)

	if s.cfg == nil {
		return s.finishManualCompactionError(ctx, emit, operationID, trigger, turnloop.CompactResult{}, errors.New("session configuration is unavailable"))
	}
	effectiveCfg := s.cfg
	if s.runtimeProfile != nil {
		effectiveCfg = s.runtimeProfile.EffectiveConfig(agentID)
	}
	activeCfg := effectiveCfg.ActiveDefaultCategory()
	compactionCfg := effectiveCfg.CategoryConfig(CategoryErrand)
	main, err := effectiveCfg.providerForCategory(activeCfg)
	if err != nil {
		return s.finishManualCompactionError(ctx, emit, operationID, trigger, turnloop.CompactResult{}, err)
	}
	extractor, configErr := effectiveCfg.providerForCategory(compactionCfg)
	if configErr != nil {
		if main.CompactionMethod() == provider.CompactionErrandOnly {
			return s.finishManualCompactionError(ctx, emit, operationID, trigger, turnloop.CompactResult{}, configErr)
		}
		extractor = main
	}
	usageModel = extractor.Model
	compactAgent := s.newAgent(extractor, effectiveCfg.IsMultiAgentEnabled(), activeCfg.ModelModalities)
	compactAgent.NativeCompactionProvider = main
	compactAgent.CompactionTargetOutput = activeCfg.ModelMaxOutput
	compactAgent.CompactionConfigError = configErr
	compactAgent.AgentID = turnloop.AgentID(agentID)
	compactAgent.TurnGeneration = generation + 1
	compactAgent.CompactionTargetModel = activeCfg.Model
	compactAgent.CompactionTargetWindow = main.ContextWindow(activeCfg.Model)
	compactAgent.MaxOutputTokens = compactionCfg.ModelMaxOutput
	_, agentProfile := activeProfileCfg(effectiveCfg)
	compactAgent.CompactionAgentProfile = agentProfile + "/" + CategoryErrand
	_, compactAgent.CompactionRetentionTokens = rootCompactionChoice(activeCfg)
	compactAgent.PrepareCompaction = func(source turnloop.CompactionSource) error {
		var err error
		revision, err = s.beginRootCompaction(agentID, source)
		return err
	}
	optionsFor := func(cfg *Config) rootprompt.RootOptions {
		return s.rootOptions(cfg, cfg.IsMultiAgentEnabled(), s.PlanMode(), cfg.ActiveDefaultCategory().Model, compactAgent.WorkDir)
	}
	epoch := newRootPromptEpoch(optionsFor(effectiveCfg), max(1, generation)+1)
	result, err := compactAgent.CompactWithRequestID(ctx, active, epoch.base, operationID, trigger, emit)
	if err != nil {
		return s.finishManualCompactionError(ctx, emit, operationID, trigger, result, err)
	}

	commitData := compactionLifecycleData(operationID, trigger, "root", compactionStageCount, compactionStageCount, result.BeforeTokens, result.AfterTokens)
	commitData["context_window"] = result.ContextWindow
	commitData["provider_id"] = result.ProviderID
	commitData["account_id"] = result.AccountID
	commitData["model_id"] = result.ModelID
	emit(turnloop.AgentEvent{Type: "context_compaction_committing", Data: commitData})
	if generation == 0 {
		generation = 1
	}
	nextGeneration := generation + 1
	s.msgsMu.Lock()
	contextUnchanged := s.contextGeneration == generation || s.contextGeneration == 0
	s.msgsMu.Unlock()
	if !contextUnchanged {
		return s.finishManualCompactionError(ctx, emit, operationID, trigger, result, errors.New("active context changed before commit"))
	}
	// A settings save can finish while extraction is running. The new base is
	// the contract at adoption, not the one the extractor started with.
	if s.runtimeProfile != nil {
		effectiveCfg = s.runtimeProfile.EffectiveConfig(agentID)
	}
	if current := optionsFor(effectiveCfg); current != epoch.baseOptions {
		epoch = newRootPromptEpoch(current, nextGeneration)
	}
	record := compactionRecordFromData(s.sessionID, agentID, sessiondb.CompactionCompleted, commitData)
	s.msgsMu.Lock()
	_, err = s.publishRootContext(sessiondb.ContextPublication{
		SessionID: s.sessionID, AgentID: s.sessionID, RuntimeAgentID: agentID, ExpectedRevision: revision,
		Mutation:   sessiondb.ContextMutation{Replace: &sessiondb.ContextReplacement{Generation: nextGeneration, Reason: trigger + "_compaction", Messages: canonicalContextMessages(recoveryArchiveMessages(result.Messages))}},
		Compaction: &record, ArchivePrevious: true,
	})
	if err == nil {
		// The serving agent loads guidance for its own model budget and active paths.
		s.promptEpoch = epoch
	}
	s.msgsMu.Unlock()
	if err != nil {
		return s.finishManualCompactionError(ctx, emit, operationID, trigger, result, fmt.Errorf("persist compacted context: %w", err))
	}

	emit(turnloop.AgentEvent{Type: "context_compacted", Data: commitData})
	s.logger.Info("COMPACT", "manual_complete", "operation_id", operationID, "before_tokens", result.BeforeTokens, "after_tokens", result.AfterTokens)
	// The boundary row and the activity strip report the result; a second
	// notice would repeat it.
	return conversation.CompactResultMsg{Before: beforeMessages, After: len(result.Messages), OperationID: operationID}
}

func (s *Session) finishManualCompactionError(
	ctx context.Context,
	emit func(turnloop.AgentEvent),
	operationID, trigger string,
	result turnloop.CompactResult,
	err error,
) conversation.CompactResultMsg {
	data := compactionLifecycleData(operationID, trigger, "root", result.StageIndex, result.Completed, result.BeforeTokens, 0)
	data["stage_label"] = result.StageName
	data["retry"] = result.RetryAttempt
	data["provider_id"] = result.ProviderID
	data["account_id"] = result.AccountID
	data["model_id"] = result.ModelID
	if ctx.Err() != nil || errors.Is(err, context.Canceled) {
		emit(turnloop.AgentEvent{Type: "context_compaction_cancelled", Data: data})
		return conversation.CompactResultMsg{OperationID: operationID, Cancelled: true}
	}
	data["error"] = compactLifecycleError(err)
	emit(turnloop.AgentEvent{Type: "context_compaction_failed", Data: data})
	return conversation.CompactResultMsg{
		Error:       "Compaction failed: " + err.Error() + ". The original context is kept.",
		OperationID: operationID,
	}
}

// HandleCompact compacts the session transcript and returns the result message.
func (s *Session) HandleCompact() conversation.CompactResultMsg {
	prepared, result := s.PrepareCompaction(s.sessionID)
	if prepared.Run == nil {
		return result
	}
	return prepared.Run()
}

func (s *Session) CancelCompaction(agentID string) bool {
	if s == nil {
		return false
	}
	s.compactionMu.Lock()
	defer s.compactionMu.Unlock()
	if s.compactionCancel == nil || (agentID != "" && s.compactionAgent != agentID) {
		return false
	}
	s.compactionCancel()
	return true
}

func (s *Session) compactionActive(agentID string) bool {
	if s == nil {
		return false
	}
	s.compactionMu.Lock()
	defer s.compactionMu.Unlock()
	return s.compactionCancel != nil && (agentID == "" || s.compactionAgent == agentID)
}

func (s *Session) persistCompactionEvent(db *sql.DB, sessionID, agentID string, event turnloop.AgentEvent) error {
	state := ""
	switch event.Type {
	case "context_compaction_started":
		state = sessiondb.CompactionPreparing
	case "context_compaction_stage":
		state = sessiondb.CompactionExtracting
	case "context_compaction_retrying":
		state = sessiondb.CompactionRetrying
	case "context_compaction_committing":
		state = sessiondb.CompactionCommitting
	case "context_compacted":
		state = sessiondb.CompactionCompleted
	case "context_compaction_cancelled":
		state = sessiondb.CompactionCancelled
	case "context_compaction_failed":
		state = sessiondb.CompactionFailed
	default:
		return nil
	}
	data, ok := event.Data.(map[string]any)
	if !ok {
		return nil
	}
	record := compactionRecordFromData(sessionID, agentID, state, data)
	if record.OperationID == "" {
		return nil
	}
	return sessiondb.SaveCompactionRecord(db, record)
}

func compactionRecordFromData(sessionID, agentID, state string, data map[string]any) sessiondb.CompactionRecord {
	now := time.Now()
	record := sessiondb.CompactionRecord{
		OperationID: compactionEventString(data, "request_id"), SessionID: sessionID, AgentID: agentID,
		Trigger: compactionEventString(data, "trigger"), Role: compactionEventString(data, "role"), State: state,
		ProviderID: compactionEventString(data, "provider_id"), AccountID: compactionEventString(data, "account_id"), ModelID: compactionEventString(data, "model_id"),
		StageName: compactionEventString(data, "stage_name"), StageIndex: compactionEventInt(data, "stage"),
		CompletedStages: compactionEventInt(data, "completed_stages"), StageCount: compactionEventInt(data, "stage_count"),
		RetryAttempt: compactionEventInt(data, "retry"), RetryLimit: compactionEventInt(data, "retry_limit"),
		BeforeTokens: compactionEventInt(data, "before_tokens"), AfterTokens: compactionEventInt(data, "after_tokens"),
		ContextWindow: compactionEventInt(data, "context_window"), Error: compactionEventString(data, "error"),
		StartedAt: now, UpdatedAt: now,
	}
	if sessiondb.IsCompactionTerminal(state) {
		record.EndedAt = now
	}
	return record
}

func compactionLifecycleData(operationID, trigger, role string, stage, completed, before, after int) map[string]any {
	return map[string]any{
		"request_id": operationID, "trigger": trigger, "role": role,
		"stage": stage, "completed_stages": completed, "stage_count": compactionStageCount,
		"retry_limit": compactionRetryLimit, "before_tokens": before, "after_tokens": after,
	}
}

func compactLifecycleError(err error) string {
	if err == nil {
		return ""
	}
	text := err.Error()
	if runes := []rune(text); len(runes) > 240 {
		text = string(runes[:240]) + "…"
	}
	return text
}

func compactionEventString(data map[string]any, key string) string {
	value, _ := data[key].(string)
	return value
}

func compactionEventInt(data map[string]any, key string) int {
	switch value := data[key].(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	default:
		return 0
	}
}

func (s *Session) commitRootCompaction(agentID string, revision uint64, commit turnloop.CompactionCommit) error {
	s.msgsMu.Lock()
	defer s.msgsMu.Unlock()
	generation := s.contextGeneration
	if generation == 0 {
		generation = 1
	}
	nextGeneration := generation + 1
	data := compactionLifecycleData(commit.RequestID, commit.Trigger, commit.Role, compactionStageCount, compactionStageCount, commit.BeforeTokens, commit.AfterTokens)
	data["context_window"] = commit.ContextWindow
	data["provider_id"] = commit.ProviderID
	data["account_id"] = commit.AccountID
	data["model_id"] = commit.ModelID
	record := compactionRecordFromData(s.sessionID, agentID, sessiondb.CompactionCompleted, data)
	_, err := s.publishRootContext(sessiondb.ContextPublication{
		SessionID: s.sessionID, AgentID: s.sessionID, RuntimeAgentID: agentID, ExpectedRevision: revision,
		Mutation:   sessiondb.ContextMutation{Replace: &sessiondb.ContextReplacement{Generation: nextGeneration, Reason: "automatic_compaction", Messages: canonicalContextMessages(recoveryArchiveMessages(commit.Messages))}},
		Compaction: &record, ArchivePrevious: true,
	})
	return err
}

func (s *Session) beginRootCompaction(agentID string, source turnloop.CompactionSource) (uint64, error) {
	s.msgsMu.Lock()
	defer s.msgsMu.Unlock()
	revision := s.contextRevision
	generation := s.contextGeneration
	if generation == 0 {
		generation = 1
	}
	messages := canonicalContextMessages(recoveryArchiveMessages(source.Messages))
	if !retainsContextSource(messages, s.activeContext) {
		return 0, errors.New("compaction source does not retain current context")
	}
	data := compactionLifecycleData(source.RequestID, source.Trigger, source.Role, 0, 0, source.BeforeTokens, 0)
	data["context_window"] = source.ContextWindow
	data["provider_id"] = source.ProviderID
	data["account_id"] = source.AccountID
	data["model_id"] = source.ModelID
	record := compactionRecordFromData(s.sessionID, agentID, sessiondb.CompactionPreparing, data)
	state, err := s.publishRootContext(sessiondb.ContextPublication{
		SessionID: s.sessionID, AgentID: s.sessionID, RuntimeAgentID: agentID, ExpectedRevision: revision,
		Mutation:   sessiondb.ContextMutation{Replace: &sessiondb.ContextReplacement{Generation: generation, Reason: "compaction_source", Messages: messages}},
		Compaction: &record,
	})
	if err != nil {
		return 0, err
	}
	return state.Revision, nil
}

func (s *Session) CompactionRecords(agentID string) []sessiondb.CompactionRecord {
	if s == nil || s.db == nil {
		return nil
	}
	records, err := sessiondb.ListCompactionRecords(s.db, s.sessionID, agentID)
	if err != nil {
		s.logger.Warn("COMPACT", "lifecycle_load_failed", "agent_id", agentID, "error", err.Error())
		return nil
	}
	return records
}
