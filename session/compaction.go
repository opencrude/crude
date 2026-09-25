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

// PreparedCompaction registers cancellation and shutdown ownership before the
// TUI launches provider work. Run must execute exactly once from a tea.Cmd.
type PreparedCompaction struct {
	OperationID string
	AgentID     string
	ProviderID  string
	AccountID   string
	ModelID     string
	Run         func() ttypes.CompactResultMsg
}

func (s *Session) PrepareCompaction(agentID string) (PreparedCompaction, ttypes.CompactResultMsg) {
	if s == nil || atomic.LoadInt32(&s.closing) != 0 {
		return PreparedCompaction{}, ttypes.CompactResultMsg{Error: "Session is closing"}
	}
	if agentID == "" {
		agentID = s.sessionID
	}
	if !s.turnGate.TryLock() {
		return PreparedCompaction{}, ttypes.CompactResultMsg{Error: "Stop the active turn before compacting context"}
	}

	s.msgsMu.Lock()
	active := append([]provider.ChatMessage(nil), s.activeContext...)
	generation := s.contextGeneration
	beforeMessages := len(active)
	s.msgsMu.Unlock()
	if len(active) == 0 {
		s.turnGate.Unlock()
		return PreparedCompaction{}, ttypes.CompactResultMsg{Desc: "Nothing to compact yet."}
	}

	operationID := fmt.Sprintf("compact-%016x%016x", rand2.Uint64(), rand2.Uint64())
	ctx, cancel := context.WithCancel(context.Background())
	s.compactionMu.Lock()
	if atomic.LoadInt32(&s.closing) != 0 || s.compactionCancel != nil {
		s.compactionMu.Unlock()
		cancel()
		s.turnGate.Unlock()
		return PreparedCompaction{}, ttypes.CompactResultMsg{Error: "Context compaction is already active"}
	}
	s.compactionCancel = cancel
	s.compactionID = operationID
	s.compactionAgent = agentID
	s.compactionStarted = false
	s.compactionWg.Add(1)
	s.compactionMu.Unlock()

	prepared := PreparedCompaction{OperationID: operationID, AgentID: agentID}
	if s.cfg != nil {
		effectiveCfg := s.cfg
		if s.runtimeProfile != nil {
			effectiveCfg = s.runtimeProfile.GetEffectiveConfig(agentID)
		}
		compactionCfg := effectiveCfg.GetCategoryConfig(CategoryAeron)
		prepared.ProviderID = effectiveCfg.templateIDForAccount(compactionCfg.AccountID)
		prepared.AccountID = compactionCfg.AccountID
		prepared.ModelID = compactionCfg.Model
	}
	prepared.Run = func() ttypes.CompactResultMsg {
		s.compactionMu.Lock()
		if s.compactionID != operationID || s.compactionCancel == nil {
			s.compactionMu.Unlock()
			return ttypes.CompactResultMsg{OperationID: operationID, Cancelled: true}
		}
		s.compactionStarted = true
		s.compactionMu.Unlock()
		defer func() {
			cancel()
			s.compactionMu.Lock()
			if s.compactionID == operationID {
				s.compactionCancel = nil
				s.compactionID = ""
				s.compactionAgent = ""
				s.compactionStarted = false
			}
			s.compactionMu.Unlock()
			s.compactionWg.Done()
			s.turnGate.Unlock()
		}()
		return s.runPreparedCompaction(ctx, operationID, agentID, prepared.ProviderID, prepared.ModelID, active, generation, beforeMessages)
	}
	return prepared, ttypes.CompactResultMsg{}
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
		s.turnGate.Unlock()
	}
	s.compactionMu.Unlock()
}

func (s *Session) runPreparedCompaction(
	ctx context.Context,
	operationID, agentID, providerID, modelID string,
	active []provider.ChatMessage,
	generation uint64,
	beforeMessages int,
) ttypes.CompactResultMsg {
	s.logger.Info("COMPACT", "manual_request", "operation_id", operationID)
	usageModel := ""
	emit := func(event agent.AgentEvent) {
		event.AgentID = agentID
		if err := s.persistCompactionEvent(s.db, s.sessionID, agentID, event); err != nil {
			s.logger.Warn("COMPACT", "lifecycle_persist_failed", "operation_id", operationID, "error", err.Error())
		}
		if event.Type == "usage" {
			s.processUsageEvent(event.Data, usageModel, agentID, "")
		}
		if s.sender != nil {
			s.sender.SendAgentEventWithID(event.Type, event.Data, agentID)
		}
	}
	startData := compactionLifecycleData(operationID, "manual", "root", 0, 0, 0, 0)
	startData["provider_id"] = providerID
	startData["model_id"] = modelID
	emit(agent.AgentEvent{Type: "context_compaction_started", Data: startData})

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
	if targetProvider, targetErr := effectiveCfg.providerForCategory(activeCfg); targetErr == nil {
		compactAgent.CompactionTargetWindow = targetProvider.ContextWindow(activeCfg.Model)
	}
	compactAgent.PrepareCompaction = func(source agent.CompactionSource) error {
		return s.beginRootCompaction(agentID, source)
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
	state := sessiondb.ContextState{
		AgentID: s.sessionID, Generation: nextGeneration, Reason: "manual_compaction",
		Messages: append([]provider.ChatMessage(nil), result.Messages...),
	}
	if err := sessiondb.CommitContextCompaction(s.db, s.sessionID, s.sessionID, state, record); err != nil {
		return s.finishManualCompactionError(ctx, emit, operationID, result, fmt.Errorf("persist compacted context: %w", err))
	}

	s.msgsMu.Lock()
	s.activeContext = append([]provider.ChatMessage(nil), result.Messages...)
	s.contextGeneration = nextGeneration
	s.contextReason = "manual_compaction"
	s.msgsMu.Unlock()

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

func (s *Session) commitRootCompaction(agentID string, commit agent.CompactionCommit) error {
	s.msgsMu.Lock()
	generation := s.contextGeneration
	if generation == 0 {
		generation = 1
	}
	nextGeneration := generation + 1
	s.msgsMu.Unlock()
	data := compactionLifecycleData(commit.RequestID, commit.Trigger, commit.Role, compactionStageCount, compactionStageCount, commit.BeforeTokens, commit.AfterTokens)
	data["context_window"] = commit.ContextWindow
	data["provider_id"] = commit.ProviderID
	data["account_id"] = commit.AccountID
	data["model_id"] = commit.ModelID
	record := compactionRecordFromData(s.sessionID, agentID, sessiondb.CompactionCompleted, data)
	state := sessiondb.ContextState{
		AgentID: s.sessionID, Generation: nextGeneration, Reason: "automatic_compaction",
		Messages: append([]provider.ChatMessage(nil), commit.Messages...),
	}
	if err := sessiondb.CommitContextCompaction(s.db, s.sessionID, s.sessionID, state, record); err != nil {
		return err
	}
	s.msgsMu.Lock()
	s.activeContext = append([]provider.ChatMessage(nil), commit.Messages...)
	s.contextGeneration = nextGeneration
	s.contextReason = "automatic_compaction"
	s.msgsMu.Unlock()
	return nil
}

func (s *Session) beginRootCompaction(agentID string, source agent.CompactionSource) error {
	s.msgsMu.Lock()
	generation := s.contextGeneration
	if generation == 0 {
		generation = 1
	}
	s.msgsMu.Unlock()
	data := compactionLifecycleData(source.RequestID, source.Trigger, source.Role, 0, 0, source.BeforeTokens, 0)
	data["context_window"] = source.ContextWindow
	data["provider_id"] = source.ProviderID
	data["account_id"] = source.AccountID
	data["model_id"] = source.ModelID
	record := compactionRecordFromData(s.sessionID, agentID, sessiondb.CompactionPreparing, data)
	state := sessiondb.ContextState{
		AgentID: s.sessionID, Generation: generation, Reason: "compaction_source",
		Messages: append([]provider.ChatMessage(nil), source.Messages...),
	}
	if err := sessiondb.BeginContextCompaction(s.db, s.sessionID, s.sessionID, state, record); err != nil {
		return err
	}
	s.msgsMu.Lock()
	s.activeContext = append([]provider.ChatMessage(nil), source.Messages...)
	s.contextGeneration = generation
	s.contextReason = "compaction_source"
	s.msgsMu.Unlock()
	return nil
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
