package session

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"crude/agent"
	"crude/codexio"
	"crude/memory"
	rootprompt "crude/prompt"
	"crude/provider"
	"crude/tools"
)

// Background memory job tuning. Everything here runs OFF the turn hotpath:
// the interactive loop never waits on extraction, summaries, or
// consolidation. The durable turn mirror completes on the finalizer.
const (
	defaultExtractEveryTurns = 3
	extractWindowMaxTurns    = 12
	extractTimeout           = 90 * time.Second
	maintenanceTimeout       = 3 * time.Minute
	maintenanceStartDelay    = 3 * time.Second
	consolidateLockTTL       = 10 * time.Minute
	consolidateMinActive     = 8
	consolidateMinInterval   = 6 * time.Hour
	memoryActiveCap          = 200
	memoryDecayUnused        = 45 * 24 * time.Hour
	maintenanceSweepSessions = 3
)

// memoryJobState serializes background memory work for one session.
type memoryJobState struct {
	extracting  atomic.Bool
	maintaining atomic.Bool
	wg          sync.WaitGroup
	mu          sync.Mutex
	ctx         context.Context
	cancel      context.CancelFunc
	closed      bool
}

func (j *memoryJobState) begin() (context.Context, bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return nil, false
	}
	if j.ctx == nil {
		j.ctx, j.cancel = context.WithCancel(context.Background())
	}
	j.wg.Add(1)
	return j.ctx, true
}

// afterTurnMemory runs post-turn memory work: mirror the finished turn into
// the cross-session history index, then (on cadence) extract durable
// memories. Called from finalizeTurn off the UI thread; the mirror is durable
// before this function returns, while extraction remains optional work.
func (s *Session) afterTurnMemory(turn int, turnMsgs []provider.ChatMessage, finalContent string) {
	store := s.MemoryStore()
	if store == nil || turn <= 0 || !containsHumanInput(turnMsgs) {
		return
	}
	record := turnRecordFromMessages(s.sessionID, turn, turnMsgs, finalContent)
	if _, ok := s.memoryJobs.begin(); !ok {
		return
	}
	defer s.memoryJobs.wg.Done()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := store.RecordTurn(ctx, record); err != nil {
		s.logger.Warn("MEMORY", "turn_mirror_failed", "error", err.Error())
		return
	}
	s.maybeExtractMemories(turn)
}

func containsHumanInput(messages []provider.ChatMessage) bool {
	for _, message := range messages {
		if message.IsGenuineUserInput() {
			return true
		}
	}
	return false
}

// turnRecordFromMessages condenses one turn into a history mirror row.
func turnRecordFromMessages(
	sessionID string, turn int, msgs []provider.ChatMessage, finalContent string,
) memory.TurnRecord {
	record := memory.TurnRecord{
		SessionID: sessionID,
		Turn:      turn,
		CreatedAt: time.Now().UnixNano(),
	}
	var toolNames []string
	seenTools := make(map[string]struct{})
	var files []string
	seenFiles := make(map[string]struct{})
	var lastAssistant string
	for _, m := range msgs {
		switch m.Role {
		case "user":
			if record.UserText == "" && m.IsGenuineUserInput() {
				record.UserText = m.DisplayContent
				if record.UserText == "" {
					record.UserText = m.ContentString()
				}
			}
		case "assistant":
			if text := m.ContentString(); text != "" {
				lastAssistant = text
			}
			for _, call := range m.ToolCalls {
				name := call.Function.Name
				if name != "" {
					if _, dup := seenTools[name]; !dup {
						seenTools[name] = struct{}{}
						toolNames = append(toolNames, name)
					}
				}
				for _, path := range pathsFromToolArgs(call.Function.Arguments) {
					if len(files) >= 16 {
						break
					}
					if _, dup := seenFiles[path]; dup {
						continue
					}
					seenFiles[path] = struct{}{}
					files = append(files, path)
				}
			}
		}
	}
	record.AssistantText = finalContent
	if record.AssistantText == "" {
		record.AssistantText = lastAssistant
	}
	record.ToolNames = toolNames
	record.Files = files
	record.Observations = observationsFromMessages(sessionID, turn, msgs)
	return record
}

func observationsFromMessages(sessionID string, turn int, messages []provider.ChatMessage) []memory.Observation {
	calls := make(map[string]provider.ToolCall)
	for _, message := range messages {
		if message.EffectiveOrigin() == provider.OriginAssistant {
			for _, call := range message.ToolCalls {
				calls[call.ID] = call
			}
		}
	}
	var observations []memory.Observation
	for _, message := range messages {
		if message.IsGenuineUserInput() && message.InternalID != "" {
			observations = append(observations, memory.Observation{Evidence: memory.Evidence{
				SessionID: sessionID, MessageID: message.InternalID, Kind: memory.EvidenceUser,
				Summary: message.ContentString(), Outcome: memory.OutcomeUnmeasured}, Turn: turn})
			continue
		}
		if message.EffectiveOrigin() != provider.OriginTool || message.ToolCallID == "" {
			continue
		}
		call, found := calls[message.ToolCallID]
		if !found || !memoryObservableTool(call.Function.Name) {
			continue
		}
		var arguments map[string]any
		_ = json.Unmarshal([]byte(call.Function.Arguments), &arguments)
		method := call.Function.Name
		operand := tools.SummarizeArgs(call.Function.Name, call.Function.Arguments)
		if operand != "" {
			method += " " + operand
		}
		kind, outcome := memory.EvidenceTool, memory.OutcomeSucceeded
		switch call.Function.Name {
		case "edit_file", "write_file", "apply_patch":
			kind = memory.EvidenceChange
		}
		summary := "Operation completed: " + call.Function.Name
		if kind == memory.EvidenceChange {
			change := memoryChangeSummary(call.Function.Name, arguments)
			if change != "" {
				summary += ": " + change
			}
		}
		if call.Function.Name == "async_execute_command" || call.Function.Name == "write_stdin" || call.Function.Name == "check_async_command" || call.Function.Name == "read_async_output" {
			outcome = memory.OutcomeUnmeasured
		}
		if call.Function.Name == "execute_command" && strings.HasPrefix(message.ContentString(), "Command still running after ") {
			outcome = memory.OutcomeUnmeasured
		}
		if message.ToolError || agent.IsToolError(message.ContentString()) {
			outcome = memory.OutcomeFailed
			summary = message.ContentString()
			if strings.HasPrefix(strings.TrimSpace(message.ContentString()), "[cancelled]") {
				outcome = memory.OutcomeCancelled
			}
		}
		if strings.TrimSpace(message.ContentString()) == "Command cancelled." ||
			strings.HasPrefix(strings.TrimSpace(message.ContentString()), "[cancelled]") {
			outcome = memory.OutcomeCancelled
			summary = "Attempt cancelled without a measured result"
		}
		command := getStrParam(arguments, "command")
		if command == "" {
			command = getStrParam(arguments, "cmd")
		}
		if call.Function.Name == "exec_command" {
			outcome = memory.OutcomeUnmeasured
			var result struct {
				ExitCode *int   `json:"exit_code"`
				Output   string `json:"output"`
			}
			if err := json.Unmarshal([]byte(message.ContentString()), &result); err == nil && result.ExitCode != nil {
				outcome = memory.OutcomeSucceeded
				if *result.ExitCode != 0 {
					outcome = memory.OutcomeFailed
				}
				summary = result.Output
			} else if message.ToolError {
				outcome = memory.OutcomeFailed
			}
		}
		if (call.Function.Name == "execute_command" || call.Function.Name == "exec_command") && memoryValidationCommand(command) {
			kind = memory.EvidenceValidation
			if call.Function.Name == "execute_command" {
				summary = message.ContentString()
			}
		}
		if len(summary) > memory.MaxEvidenceSummaryBytes && outcome == memory.OutcomeSucceeded && kind == memory.EvidenceValidation {
			outcome = memory.OutcomeUnmeasured
			summary = "Validation returned an oversized result; inspect the original tool call before claiming success"
		}
		if kind == memory.EvidenceValidation && outcome == memory.OutcomeSucceeded && !memoryValidationObserved(command, summary) {
			outcome = memory.OutcomeUnmeasured
		}
		if strings.TrimSpace(summary) == "" {
			summary = "Observed outcome: " + outcome
		}
		conditions := getStrParam(arguments, "cwd")
		if conditions == "" {
			conditions = getStrParam(arguments, "workdir")
		}
		messageID := message.InternalID
		if messageID == "" {
			messageID = call.InternalID
		}
		observations = append(observations, memory.Observation{Evidence: memory.Evidence{
			SessionID: sessionID, MessageID: messageID, ToolCallID: message.ToolCallID,
			Kind: kind, Summary: summary, Outcome: outcome}, Turn: turn,
			Method: method, Conditions: conditions, Paths: pathsFromToolArgs(call.Function.Arguments)})
	}
	return memory.SanitizeObservations(observations)
}

func memoryChangeSummary(name string, arguments map[string]any) string {
	if name == "edit_file" {
		previous := memory.TruncateBytes(memory.Redact(getStrParam(arguments, "old_string")), 160)
		next := memory.TruncateBytes(memory.Redact(getStrParam(arguments, "new_string")), 160)
		if previous != "" && next != "" {
			return fmt.Sprintf("replaced %q with %q", previous, next)
		}
	}
	return ""
}

func memoryObservableTool(name string) bool {
	switch name {
	case "read_file", "list_directory", "glob_files", "grep_files", "web_search", "web_fetch", "arxiv_search",
		"edit_file", "write_file", "apply_patch", "delete_file", "move_file", "copy_file",
		"execute_command", "exec_command", "async_execute_command", "write_stdin", "check_async_command", "read_async_output", "kill_async_command":
		return true
	}
	return false
}

func memoryValidationObserved(command, output string) bool {
	if strings.HasPrefix(strings.TrimSpace(command), "go test") {
		for _, line := range strings.Split(output, "\n") {
			if line == "PASS" || strings.HasPrefix(line, "ok\t") || strings.HasPrefix(line, "ok ") {
				return true
			}
		}
		return false
	}
	lower := strings.ToLower(output)
	return strings.Contains(lower, "passed") || strings.Contains(lower, "test result: ok") || strings.Contains(lower, "tests passed") || strings.Contains(lower, "\nok\n")
}

func memoryValidationCommand(command string) bool {
	if strings.ContainsAny(command, ";&|`\n<>") || strings.Contains(command, "$(") {
		return false
	}
	parts := strings.Fields(strings.TrimSpace(command))
	if len(parts) == 0 {
		return false
	}
	if parts[0] == "pytest" {
		return true
	}
	if len(parts) < 2 {
		return false
	}
	switch parts[0] {
	case "go", "cargo", "zig", "npm", "pnpm", "yarn":
		return parts[1] == "test"
	case "python", "python3":
		return len(parts) >= 3 && parts[1] == "-m" && (parts[2] == "pytest" || parts[2] == "unittest")
	}
	return false
}

// pathsFromToolArgs pulls file-path parameters out of a tool call's JSON
// arguments.
func pathsFromToolArgs(arguments string) []string {
	if patch, err := codexio.Parse(arguments); err == nil {
		return patch.Paths("")
	}
	if arguments == "" {
		return nil
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return nil
	}
	var out []string
	for _, key := range []string{"path", "source", "destination", "file_path"} {
		if value, ok := args[key].(string); ok {
			if value = strings.TrimSpace(value); value != "" {
				out = append(out, value)
			}
		}
	}
	return out
}

func extractWatermarkKey(sessionID string) string { return "extract:" + sessionID }

func (s *Session) extractWatermark(ctx context.Context, store *memory.Store, sessionID string) int {
	raw, err := store.GetMeta(ctx, extractWatermarkKey(sessionID))
	if err != nil || raw == "" {
		return 0
	}
	var turn int
	if _, err := fmt.Sscanf(raw, "%d", &turn); err != nil {
		return 0
	}
	return turn
}

// maybeExtractMemories starts a background extraction run when the cadence
// threshold is reached. At most one extraction runs per session at a time.
func (s *Session) maybeExtractMemories(turn int) {
	settings := s.memorySettings()
	if !settings.AutoExtractEnabled() {
		return
	}
	store := s.MemoryStore()
	if store == nil {
		return
	}
	parent, ok := s.memoryJobs.begin()
	if !ok {
		return
	}
	defer s.memoryJobs.wg.Done()
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	watermark := s.extractWatermark(ctx, store, s.sessionID)
	cancel()
	if turn-watermark < settings.ExtractCadenceOrDefault(defaultExtractEveryTurns) {
		return
	}
	if !s.memoryJobs.extracting.CompareAndSwap(false, true) {
		return
	}
	parent, ok = s.memoryJobs.begin()
	if !ok {
		s.memoryJobs.extracting.Store(false)
		return
	}
	go func() {
		defer s.memoryJobs.wg.Done()
		defer s.memoryJobs.extracting.Store(false)
		s.runExtraction(parent, s.sessionID, true)
	}()
}

// runExtraction extracts durable memories + a rolling session summary from
// the unprocessed turn window of sessionID (read from the history mirror,
// which persists — a run that never happens is picked up by a later
// maintenance sweep; nothing is ever lost).
func (s *Session) runExtraction(parent context.Context, sessionID string, notify bool) {
	store := s.MemoryStore()
	if store == nil {
		return
	}
	ctx, cancel := context.WithTimeout(parent, extractTimeout)
	defer cancel()

	watermark := s.extractWatermark(ctx, store, sessionID)
	turns, err := store.TurnsForSession(ctx, sessionID, watermark, extractWindowMaxTurns)
	if err != nil {
		s.logger.Warn("MEMORY", "extract_turns_failed", "error", err.Error())
		return
	}
	if len(turns) == 0 {
		return
	}
	lastTurn := turns[len(turns)-1].Turn
	generation, err := store.KnowledgeGeneration(ctx)
	if err != nil {
		return
	}
	observations, err := store.ObservationsForSession(ctx, sessionID, watermark, lastTurn)
	if err != nil {
		s.logger.Warn("MEMORY", "extract_observations_failed", "error", err.Error())
		return
	}
	observations = memory.SelectExtractionObservations(observations)

	previousSummary := ""
	if summary, found, err := store.GetSessionSummary(ctx, sessionID); err == nil && found {
		previousSummary = summary.Summary
	}
	existingTitles := s.existingMemoryTitles(ctx, store)

	complete, err := s.memoryCompleter()
	if err != nil {
		s.logger.Warn("MEMORY", "extract_no_provider", "error", err.Error())
		return
	}
	raw, err := complete(ctx, rootprompt.MemoryExtractionSystem(),
		rootprompt.MemoryExtractionUser(memory.PrepareExtractionInput(previousSummary, existingTitles, buildTranscript(turns), observations)))
	if err != nil {
		s.logger.Warn("MEMORY", "extract_call_failed", "error", err.Error())
		return
	}
	result, err := memory.ParseExtraction(raw)
	if err != nil {
		s.logger.Warn("MEMORY", "extract_parse_failed", "error", err.Error())
		return
	}

	provenance := fmt.Sprintf("session:%s turns:%d-%d (auto)", sessionID, watermark+1, lastTurn)
	batch := memory.ExtractionBatch{SessionID: sessionID, ExpectedWatermark: watermark,
		ThroughTurn: lastTurn, ExpectedGeneration: generation,
		Records: memory.GroundExtraction(result, observations, sessionID, provenance)}
	if result.Summary != nil {
		batch.Summary = &memory.SessionSummary{
			SessionID: sessionID,
			Title:     result.Summary.Title,
			Summary:   result.Summary.Summary,
			KeyFiles:  result.Summary.KeyFiles,
			Outcome:   result.Summary.Outcome,
			TurnCount: lastTurn,
		}
	}
	saved, err := store.CommitExtraction(ctx, batch)
	if err != nil {
		s.logger.Warn("MEMORY", "extract_commit_failed", "error", err.Error())
		return
	}
	s.refreshMemorySummary(ctx, store)
	var savedTitles []string
	for _, record := range saved {
		savedTitles = append(savedTitles, record.Title)
	}
	s.logger.Info("MEMORY", "extract_done",
		"session", sessionID, "turns", len(turns), "saved", len(saved))
	if notify && len(saved) > 0 && s.sender != nil {
		s.sender.SendAgentEvent("memory_saved", map[string]any{
			"count":  len(saved),
			"titles": savedTitles,
		})
	}
}

// existingMemoryTitles renders current memory titles for the extraction
// prompt's dedup list.
func (s *Session) existingMemoryTitles(ctx context.Context, store *memory.Store) string {
	records, err := store.List(ctx, memory.ListQuery{Status: "all", Limit: 40})
	if err != nil {
		return ""
	}
	var b strings.Builder
	for _, record := range records {
		fmt.Fprintf(&b, "- %s@%d (%s, %s): %s\n", record.ID, record.Revision, record.Kind, record.Status, record.Title)
	}
	return strings.TrimSpace(b.String())
}

// buildTranscript renders mirrored turns as a compact extraction input.
func buildTranscript(turns []memory.HistoryHit) string {
	var b strings.Builder
	for _, turn := range turns {
		fmt.Fprintf(&b, "--- session %s turn %d ---\n", turn.SessionID, turn.Turn)
		if turn.UserText != "" {
			b.WriteString("USER: ")
			b.WriteString(memory.TruncateBytes(turn.UserText, 1200))
			b.WriteString("\n")
		}
		if turn.ToolNames != "" {
			b.WriteString("TOOLS: ")
			b.WriteString(turn.ToolNames)
			if turn.Files != "" {
				b.WriteString(" | FILES: ")
				b.WriteString(turn.Files)
			}
			b.WriteString("\n")
		}
		if turn.AssistantText != "" {
			b.WriteString("ASSISTANT CLAIM (not verification): ")
			b.WriteString(memory.TruncateBytes(turn.AssistantText, 1600))
			b.WriteString("\n")
		}
	}
	return b.String()
}

// memoryCompleter builds the one-shot completion function for background
// jobs: the memory agent's provider/model, which inherits the main agent
// live unless configured otherwise.
func (s *Session) memoryCompleter() (func(context.Context, string, string) (string, error), error) {
	s.mu.Lock()
	cfg := s.cfg
	s.mu.Unlock()
	if cfg == nil {
		return nil, fmt.Errorf("session has no config")
	}
	category := cfg.GetMemoryCategory()
	if category.Enabled != nil && !*category.Enabled {
		return nil, fmt.Errorf("memory agent is disabled")
	}
	p, err := cfg.providerForCategory(category)
	if err != nil {
		return nil, err
	}
	model := category.Model
	return func(ctx context.Context, system, user string) (string, error) {
		return agent.CompleteTextWithMaxOutput(ctx, p, model, rootprompt.MemoryAgentSystem(system, category.Instructions), user, category.ModelMaxOutput)
	}, nil
}

// StartMemoryMaintenance runs the background "dreaming" pass shortly after
// session start: finish extraction leftovers from earlier sessions, then
// consolidate (dedup, contradictions, decay, cap) under a workspace lock.
// Running at session START rather than close guarantees process lifetime.
func (s *Session) StartMemoryMaintenance() {
	store := s.MemoryStore()
	if store == nil {
		return
	}
	if !s.memoryJobs.maintaining.CompareAndSwap(false, true) {
		return
	}
	ctx, ok := s.memoryJobs.begin()
	if !ok {
		s.memoryJobs.maintaining.Store(false)
		return
	}
	go func() {
		defer s.memoryJobs.wg.Done()
		defer s.memoryJobs.maintaining.Store(false)
		timer := time.NewTimer(maintenanceStartDelay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		if atomic.LoadInt32(&s.closing) == 1 {
			return
		}
		s.runMemoryMaintenance(ctx)
	}()
}

func (s *Session) runMemoryMaintenance(parent context.Context) {
	store := s.MemoryStore()
	if store == nil {
		return
	}
	settings := s.memorySettings()
	ctx, cancel := context.WithTimeout(parent, maintenanceTimeout)
	defer cancel()

	// Sweep: finish extraction for sessions that closed with unprocessed
	// turns (their mirrors persist; watermarks say where they stopped).
	if settings.AutoExtractEnabled() {
		// Resume may reopen this same session rather than create another one.
		// Give its pending short turns the same recovery path as older sessions.
		if s.memoryJobs.extracting.CompareAndSwap(false, true) {
			s.runExtraction(ctx, s.sessionID, false)
			s.memoryJobs.extracting.Store(false)
		}
		heads, err := store.SessionTurnHeads(ctx)
		if err != nil {
			s.logger.Warn("MEMORY", "sweep_heads_failed", "error", err.Error())
		} else {
			swept := 0
			for sessionID, head := range heads {
				if swept >= maintenanceSweepSessions || ctx.Err() != nil {
					break
				}
				if sessionID == s.sessionID {
					continue
				}
				if head > s.extractWatermark(ctx, store, sessionID) {
					s.runExtraction(ctx, sessionID, false)
					swept++
				}
			}
		}
	}

	if !settings.ConsolidationEnabled() {
		return
	}
	locked, err := store.TryLock(ctx, memory.ConsolidationLockName, consolidateLockTTL)
	if err != nil || !locked {
		return
	}
	defer func() {
		if err := store.Unlock(context.Background(), memory.ConsolidationLockName); err != nil {
			s.logger.Warn("MEMORY", "unlock_failed", "error", err.Error())
		}
	}()

	// Deterministic passes are cheap and always safe.
	if groups, err := store.DuplicateSuggestions(ctx); err != nil {
		s.logger.Warn("MEMORY", "dedupe_failed", "error", err.Error())
	} else if len(groups) > 0 {
		s.logger.Info("MEMORY", "dedupe_suggested", "groups", len(groups))
	}
	if n, err := store.DecayArchive(ctx, memoryDecayUnused); err != nil {
		s.logger.Warn("MEMORY", "decay_failed", "error", err.Error())
	} else if n > 0 {
		s.logger.Info("MEMORY", "decay_done", "archived", n)
	}
	if n, err := store.EnforceCap(ctx, memoryActiveCap); err != nil {
		s.logger.Warn("MEMORY", "cap_failed", "error", err.Error())
	} else if n > 0 {
		s.logger.Info("MEMORY", "cap_done", "archived", n)
	}

	// LLM pass: rate-limited, threshold-gated, conservative.
	if !s.shouldRunLLMConsolidation(ctx, store) {
		return
	}
	candidates, err := store.ConsolidationCandidates(ctx)
	if err != nil || len(candidates) < consolidateMinActive {
		return
	}
	complete, err := s.memoryCompleter()
	if err != nil {
		return
	}
	raw, err := complete(ctx, rootprompt.MemoryConsolidationSystem(),
		rootprompt.MemoryConsolidationUser(memory.PrepareConsolidationInput(candidates)))
	if err != nil {
		s.logger.Warn("MEMORY", "consolidate_call_failed", "error", err.Error())
		return
	}
	ops, err := memory.ParseConsolidation(raw)
	if err != nil {
		s.logger.Warn("MEMORY", "consolidate_parse_failed", "error", err.Error())
		return
	}
	applied, err := store.ApplyConsolidation(ctx, ops, s.sessionID)
	if err != nil {
		s.logger.Warn("MEMORY", "consolidate_apply_failed", "error", err.Error())
		return
	}
	if err := store.SetMeta(ctx, "consolidate_last",
		fmt.Sprintf("%d", time.Now().UnixNano())); err != nil {
		s.logger.Warn("MEMORY", "consolidate_meta_failed", "error", err.Error())
	}
	s.logger.Info("MEMORY", "consolidate_done", "proposed", len(ops), "applied", applied)
}

func (s *Session) shouldRunLLMConsolidation(ctx context.Context, store *memory.Store) bool {
	raw, err := store.GetMeta(ctx, "consolidate_last")
	if err == nil && raw != "" {
		var last int64
		if _, err := fmt.Sscanf(raw, "%d", &last); err == nil {
			if time.Since(time.Unix(0, last)) < consolidateMinInterval {
				return false
			}
		}
	}
	return true
}

// finishMemoryJobs stops optional provider work and joins workers before the
// workspace DB closes. Turn mirrors finish durably; unprocessed extraction
// watermarks are picked up by the next maintenance sweep, including short turns.
func (s *Session) finishMemoryJobs() {
	s.memoryJobs.mu.Lock()
	s.memoryJobs.closed = true
	if s.memoryJobs.cancel != nil {
		s.memoryJobs.cancel()
	}
	s.memoryJobs.mu.Unlock()
	s.memoryJobs.wg.Wait()
}
