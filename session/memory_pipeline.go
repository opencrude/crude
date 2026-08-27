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
	"crude/memory"
	"crude/provider"
)

// Background memory job tuning. Everything here runs OFF the turn hotpath:
// the interactive loop never waits on extraction, summaries, or
// consolidation.
const (
	defaultExtractEveryTurns = 3
	extractWindowMaxTurns    = 12
	extractTimeout           = 90 * time.Second
	maintenanceTimeout       = 3 * time.Minute
	maintenanceStartDelay    = 3 * time.Second
	consolidateLockTTL       = 10 * time.Minute
	consolidateMinActive     = 20
	consolidateMinInterval   = 6 * time.Hour
	memoryActiveCap          = 200
	memoryDecayUnused        = 45 * 24 * time.Hour
	closeExtractGrace        = 5 * time.Second
	maintenanceSweepSessions = 3
)

// memoryJobState serializes background memory work for one session.
type memoryJobState struct {
	extracting  atomic.Bool
	maintaining atomic.Bool
	wg          sync.WaitGroup
}

// afterTurnMemory runs post-turn memory work: mirror the finished turn into
// the cross-session history index, then (on cadence) extract durable
// memories. Called from finalizeTurn; returns immediately.
func (s *Session) afterTurnMemory(turn int, turnMsgs []provider.ChatMessage, finalContent string) {
	store := s.MemoryStore()
	if store == nil || turn <= 0 {
		return
	}
	record := turnRecordFromMessages(s.sessionID, turn, turnMsgs, finalContent)
	s.memoryJobs.wg.Add(1)
	go func() {
		defer s.memoryJobs.wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := store.RecordTurn(ctx, record); err != nil {
			s.logger.Warn("MEMORY", "turn_mirror_failed", "error", err.Error())
			return
		}
		// Extraction reads from the mirror, so it only starts after the
		// mirror write landed.
		s.maybeExtractMemories(turn)
	}()
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
			if record.UserText == "" {
				record.UserText = m.ContentString()
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
	return record
}

// pathsFromToolArgs pulls file-path parameters out of a tool call's JSON
// arguments.
func pathsFromToolArgs(arguments string) []string {
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
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	watermark := s.extractWatermark(ctx, store, s.sessionID)
	cancel()
	if turn-watermark < settings.ExtractCadenceOrDefault(defaultExtractEveryTurns) {
		return
	}
	if !s.memoryJobs.extracting.CompareAndSwap(false, true) {
		return
	}
	s.memoryJobs.wg.Add(1)
	go func() {
		defer s.memoryJobs.wg.Done()
		defer s.memoryJobs.extracting.Store(false)
		s.runExtraction(s.sessionID, true)
	}()
}

// runExtraction extracts durable memories + a rolling session summary from
// the unprocessed turn window of sessionID (read from the history mirror,
// which persists — a run that never happens is picked up by a later
// maintenance sweep; nothing is ever lost).
func (s *Session) runExtraction(sessionID string, notify bool) {
	store := s.MemoryStore()
	if store == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), extractTimeout)
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
	raw, err := complete(ctx, memory.ExtractionSystemPrompt,
		memory.BuildExtractionUser(previousSummary, existingTitles, buildTranscript(turns)))
	if err != nil {
		s.logger.Warn("MEMORY", "extract_call_failed", "error", err.Error())
		return
	}
	result, err := memory.ParseExtraction(raw)
	if err != nil {
		s.logger.Warn("MEMORY", "extract_parse_failed", "error", err.Error())
		return
	}

	saved := 0
	var savedTitles []string
	provenance := fmt.Sprintf("session:%s turns:%d-%d (auto)", sessionID, watermark+1, lastTurn)
	for _, m := range result.Memories {
		record, err := store.Remember(ctx, memory.Record{
			Kind:            m.Kind,
			Title:           m.Title,
			Content:         m.Content,
			ScopePaths:      m.Paths,
			Importance:      m.Importance,
			SourceSessionID: sessionID,
			Source:          memory.SourceAuto,
			Provenance:      provenance,
		})
		if err != nil {
			s.logger.Warn("MEMORY", "extract_save_failed", "title", m.Title, "error", err.Error())
			continue
		}
		saved++
		savedTitles = append(savedTitles, record.Title)
	}
	if result.Summary != nil {
		if err := store.SaveSessionSummary(ctx, memory.SessionSummary{
			SessionID: sessionID,
			Title:     result.Summary.Title,
			Summary:   result.Summary.Summary,
			KeyFiles:  result.Summary.KeyFiles,
			Outcome:   result.Summary.Outcome,
			TurnCount: lastTurn,
		}); err != nil {
			s.logger.Warn("MEMORY", "summary_save_failed", "error", err.Error())
		}
	}
	if err := store.SetMeta(ctx, extractWatermarkKey(sessionID),
		fmt.Sprintf("%d", lastTurn)); err != nil {
		s.logger.Warn("MEMORY", "watermark_save_failed", "error", err.Error())
	}
	s.logger.Info("MEMORY", "extract_done",
		"session", sessionID, "turns", len(turns), "saved", saved)
	if notify && saved > 0 && s.sender != nil {
		s.sender.SendAgentEvent("memory_saved", map[string]any{
			"count":  saved,
			"titles": savedTitles,
		})
	}
}

// existingMemoryTitles renders current memory titles for the extraction
// prompt's dedup list.
func (s *Session) existingMemoryTitles(ctx context.Context, store *memory.Store) string {
	records, err := store.List(ctx, memory.ListQuery{Limit: 40})
	if err != nil {
		return ""
	}
	var b strings.Builder
	for _, record := range records {
		fmt.Fprintf(&b, "- %s (%s)\n", record.Title, record.Kind)
	}
	return strings.TrimSpace(b.String())
}

// buildTranscript renders mirrored turns as a compact extraction input.
func buildTranscript(turns []memory.HistoryHit) string {
	var b strings.Builder
	for _, turn := range turns {
		fmt.Fprintf(&b, "--- turn %d ---\n", turn.Turn)
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
			b.WriteString("ASSISTANT: ")
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
	// OAuth providers (e.g. ChatGPT subscription) have no API key; allow them.
	if category.APIKey == "" && category.OAuth == nil {
		return nil, fmt.Errorf("no API key configured")
	}
	p, err := cfg.providerForCategory(category)
	if err != nil {
		return nil, err
	}
	model := category.Model
	return func(ctx context.Context, system, user string) (string, error) {
		return agent.CompleteText(ctx, p, model, system, user)
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
	s.memoryJobs.wg.Add(1)
	go func() {
		defer s.memoryJobs.wg.Done()
		defer s.memoryJobs.maintaining.Store(false)
		time.Sleep(maintenanceStartDelay)
		if atomic.LoadInt32(&s.closing) == 1 {
			return
		}
		s.runMemoryMaintenance()
	}()
}

func (s *Session) runMemoryMaintenance() {
	store := s.MemoryStore()
	if store == nil {
		return
	}
	settings := s.memorySettings()
	ctx, cancel := context.WithTimeout(context.Background(), maintenanceTimeout)
	defer cancel()

	// Sweep: finish extraction for sessions that closed with unprocessed
	// turns (their mirrors persist; watermarks say where they stopped).
	if settings.AutoExtractEnabled() {
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
					s.runExtraction(sessionID, false)
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
	if n, err := store.DedupeExact(ctx); err != nil {
		s.logger.Warn("MEMORY", "dedupe_failed", "error", err.Error())
	} else if n > 0 {
		s.logger.Info("MEMORY", "dedupe_done", "superseded", n)
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
	raw, err := complete(ctx, memory.ConsolidationSystemPrompt,
		memory.BuildConsolidationUser(candidates))
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

// finishMemoryJobs runs the final extraction (short sessions may never hit
// the cadence) and waits briefly for outstanding background jobs. Bounded:
// a slow provider cannot hold session close hostage — whatever does not
// finish is picked up by the next maintenance sweep.
func (s *Session) finishMemoryJobs() {
	store := s.MemoryStore()
	if store == nil {
		return
	}
	if s.memorySettings().AutoExtractEnabled() && s.memoryJobs.extracting.CompareAndSwap(false, true) {
		s.memoryJobs.wg.Add(1)
		go func() {
			defer s.memoryJobs.wg.Done()
			defer s.memoryJobs.extracting.Store(false)
			s.runExtraction(s.sessionID, false)
		}()
	}
	done := make(chan struct{})
	go func() {
		s.memoryJobs.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(closeExtractGrace):
		s.logger.Info("MEMORY", "close_jobs_pending", "note", "next maintenance sweep will finish extraction")
	}
}
