package session

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	rand2 "math/rand/v2"
	"sync/atomic"
	"time"

	"crude/agent"
	"crude/provider"
	"crude/sessiondb"
	ttypes "crude/types"
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
	Run         func() ttypes.CompactResultMsg
	Cancel      func()
}

func newCompactionOperationID() string {
	return fmt.Sprintf("compact-%016x%016x", rand2.Uint64(), rand2.Uint64())
}

func (s *Session) PrepareCompaction(agentID string) (PreparedCompaction, ttypes.CompactResultMsg) {
	if s == nil || atomic.LoadInt32(&s.closing) != 0 {
		return PreparedCompaction{}, ttypes.CompactResultMsg{Error: compactionClosingText}
	}
	release, ok := s.beginIdleWork()
	if !ok {
		return PreparedCompaction{}, ttypes.CompactResultMsg{Error: compactionReservedText}
	}
	defer release()
	if agentID == "" {
		agentID = s.sessionID
	}
	if s.compactionActive(agentID) {
		return PreparedCompaction{}, ttypes.CompactResultMsg{Desc: compactionRunningText}
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
		return PreparedCompaction{}, ttypes.CompactResultMsg{Desc: compactionNothingText}
	}
	prepared, registered := s.registerCompaction(agentID, operationID)
	if !registered {
		return PreparedCompaction{}, ttypes.CompactResultMsg{Desc: compactionRunningText}
	}
	// A turn reserved before the registration saw no compaction; hand the
	// request to it. Later reservations see the registration and wait.
	if id, handed := orchestrator.requestCompaction(operationID); handed {
		s.releaseUnstartedCompaction(operationID)
		return PreparedCompaction{}, requestedCompactionResult(id, operationID)
	}
	return prepared, ttypes.CompactResultMsg{}
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

func requestedCompactionResult(id, requested string) ttypes.CompactResultMsg {
	if id != requested {
		return ttypes.CompactResultMsg{OperationID: id, Desc: compactionPendingText}
	}
	return ttypes.CompactResultMsg{OperationID: id, Desc: compactionRequestedText}
}

// registerCompaction reserves the session's one exclusive compaction. From
// here on new turns and queued input wait for it, and Stop or Close cancels it.
func (s *Session) registerCompaction(agentID, operationID string) (PreparedCompaction, bool) {
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
	prepared.Started = compactionLifecycleData(operationID, "manual", "root", 0, 0, 0, 0)
	prepared.Started["provider_id"] = prepared.ProviderID
	prepared.Started["model_id"] = prepared.ModelID
	started := prepared.Started
	prepared.Run = func() ttypes.CompactResultMsg {
		return s.runExclusiveCompaction(ctx, cancel, operationID, agentID, started)
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
		effectiveCfg = s.runtimeProfile.GetEffectiveConfig(agentID)
	}
	compactionCfg := effectiveCfg.GetCategoryConfig(CategoryAeron)
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
	operationID, agentID string,
	started map[string]any,
) ttypes.CompactResultMsg {
	s.compactionMu.Lock()
	if s.compactionID != operationID || s.compactionCancel == nil || s.compactionStarted {
		s.compactionMu.Unlock()
		return ttypes.CompactResultMsg{OperationID: operationID, Cancelled: true}
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
	emit(agent.AgentEvent{Type: "context_compaction_started", Data: started})
	if err := s.turnGate.LockContext(ctx); err != nil {
		emit(agent.AgentEvent{Type: "context_compaction_cancelled", Data: started})
		return ttypes.CompactResultMsg{OperationID: operationID, Cancelled: true}
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
			return s.finishManualCompactionError(ctx, emit, operationID, agent.CompactResult{}, fmt.Errorf("load current context: %w", err))
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
		emit(agent.AgentEvent{Type: "context_compaction_cancelled", Data: started})
		return ttypes.CompactResultMsg{OperationID: operationID, Desc: compactionNothingText}
	}
	return s.runPreparedCompaction(ctx, operationID, agentID, active, revision, generation, len(active))
}

// startTurnEndCompaction runs a request the turn accepted but ended before
// taking. The caller still owns the turn, so no other turn can start between
// the turn's end and this registration.
func (s *Session) startTurnEndCompaction(agentID, operationID string) func() {
	prepared, registered := s.registerCompaction(agentID, operationID)
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

// settleDroppedCompaction records a requested compaction that will not run
// because its turn stopped or failed first, so the request never vanishes.
func (s *Session) settleDroppedCompaction(agentID, operationID string) {
	if operationID == "" {
		return
	}
	s.compactionEmitter(agentID, nil)(agent.AgentEvent{Type: "context_compaction_cancelled", Data: compactionLifecycleData(operationID, "requested", "root", 0, 0, 0, 0)})
}

// compactionEmitter persists a compaction lifecycle event and delivers it to
// every shell. usageModel prices extraction usage when the caller has one.
func (s *Session) compactionEmitter(agentID string, usageModel *string) func(agent.AgentEvent) {
	sender := s.orchestratorSender(agentID)
	return func(event agent.AgentEvent) {
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
	operationID, agentID string,
	active []provider.ChatMessage,
	revision uint64,
	generation uint64,
	beforeMessages int,
) ttypes.CompactResultMsg {
	s.logger.Info("COMPACT", "manual_request", "operation_id", operationID)
	usageModel := ""
	emit := s.compactionEmitter(agentID, &usageModel)

	if s.cfg == nil {
		return s.finishManualCompactionError(ctx, emit, operationID, agent.CompactResult{}, errors.New("session configuration is unavailable"))
	}
	effectiveCfg := s.cfg
	if s.runtimeProfile != nil {
		effectiveCfg = s.runtimeProfile.GetEffectiveConfig(agentID)
	}
	activeCfg := effectiveCfg.GetActiveDefaultCategory()
	compactionCfg := effectiveCfg.GetCategoryConfig(CategoryAeron)
	usageModel = compactionCfg.Model
	turnProvider, err := effectiveCfg.providerForCategory(compactionCfg)
	if err != nil {
		return s.finishManualCompactionError(ctx, emit, operationID, agent.CompactResult{}, fmt.Errorf("configure compaction provider: %w", err))
	}
	compactAgent := s.newAgent(turnProvider, effectiveCfg.IsMultiAgentEnabled(), activeCfg.ModelModalities)
	compactAgent.AgentID = agent.AgentID(agentID)
	compactAgent.TurnGeneration = generation + 1
	compactAgent.CompactionTargetModel = activeCfg.Model
	compactAgent.MaxOutputTokens = compactionCfg.ModelMaxOutput
	_, agentProfile := activeProfileCfg(effectiveCfg)
	compactAgent.CompactionAgentProfile = agentProfile + "/" + CategoryAeron
	if targetProvider, targetErr := effectiveCfg.providerForCategory(activeCfg); targetErr == nil {
		compactAgent.CompactionTargetWindow = targetProvider.ContextWindow(activeCfg.Model)
	}
	compactAgent.PrepareCompaction = func(source agent.CompactionSource) error {
		var err error
		revision, err = s.beginRootCompaction(agentID, source)
		return err
	}
	result, err := compactAgent.CompactWithRequestID(ctx, active, s.systemPromptForModel(effectiveCfg.IsMultiAgentEnabled(), activeCfg.Model), operationID, emit)
	if err != nil {
		return s.finishManualCompactionError(ctx, emit, operationID, result, err)
	}

	commitData := compactionLifecycleData(operationID, "manual", "root", compactionStageCount, compactionStageCount, result.BeforeTokens, result.AfterTokens)
	commitData["context_window"] = result.ContextWindow
	commitData["provider_id"] = result.ProviderID
	commitData["account_id"] = result.AccountID
	commitData["model_id"] = result.ModelID
	emit(agent.AgentEvent{Type: "context_compaction_committing", Data: commitData})
	if generation == 0 {
		generation = 1
	}
	nextGeneration := generation + 1
	s.msgsMu.Lock()
	contextUnchanged := s.contextGeneration == generation || s.contextGeneration == 0
	s.msgsMu.Unlock()
	if !contextUnchanged {
		return s.finishManualCompactionError(ctx, emit, operationID, result, errors.New("active context changed before commit"))
	}
	record := compactionRecordFromData(s.sessionID, agentID, sessiondb.CompactionCompleted, commitData)
	s.msgsMu.Lock()
	_, err = s.publishRootContext(sessiondb.ContextPublication{
		SessionID: s.sessionID, AgentID: s.sessionID, RuntimeAgentID: agentID, ExpectedRevision: revision,
		Mutation:   sessiondb.ContextMutation{Replace: &sessiondb.ContextReplacement{Generation: nextGeneration, Reason: "manual_compaction", Messages: canonicalContextMessages(recoveryArchiveMessages(result.Messages))}},
		Compaction: &record, ArchivePrevious: true,
	})
	s.msgsMu.Unlock()
	if err != nil {
		return s.finishManualCompactionError(ctx, emit, operationID, result, fmt.Errorf("persist compacted context: %w", err))
	}

	emit(agent.AgentEvent{Type: "context_compacted", Data: commitData})
	s.logger.Info("COMPACT", "manual_complete", "operation_id", operationID, "before_tokens", result.BeforeTokens, "after_tokens", result.AfterTokens)
	return ttypes.CompactResultMsg{
		Desc:   fmt.Sprintf("Compacted active context from %d to about %d tokens; full session history remains archived.", result.BeforeTokens, result.AfterTokens),
		Before: beforeMessages, After: len(result.Messages), OperationID: operationID,
	}
}

func (s *Session) finishManualCompactionError(
	ctx context.Context,
	emit func(agent.AgentEvent),
	operationID string,
	result agent.CompactResult,
	err error,
) ttypes.CompactResultMsg {
	data := compactionLifecycleData(operationID, "manual", "root", result.StageIndex, result.Completed, result.BeforeTokens, 0)
	data["stage_label"] = result.StageName
	data["retry"] = result.RetryAttempt
	data["provider_id"] = result.ProviderID
	data["account_id"] = result.AccountID
	data["model_id"] = result.ModelID
	if ctx.Err() != nil || errors.Is(err, context.Canceled) {
		emit(agent.AgentEvent{Type: "context_compaction_cancelled", Data: data})
		return ttypes.CompactResultMsg{OperationID: operationID, Cancelled: true}
	}
	data["error"] = compactLifecycleError(err)
	emit(agent.AgentEvent{Type: "context_compaction_failed", Data: data})
	return ttypes.CompactResultMsg{
		Error:       "Compaction failed; original context retained: " + err.Error(),
		OperationID: operationID,
	}
}

func (s *Session) HandleCompact() ttypes.CompactResultMsg {
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

func (s *Session) persistCompactionEvent(db *sql.DB, sessionID, agentID string, event agent.AgentEvent) error {
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

func (s *Session) commitRootCompaction(agentID string, revision uint64, commit agent.CompactionCommit) error {
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

func (s *Session) beginRootCompaction(agentID string, source agent.CompactionSource) (uint64, error) {
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
