package session

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"crude/memory"
	rootprompt "crude/prompt"
	"crude/tools"
	ttypes "crude/types"
)

// Recall defaults; overridable via memory settings (types.MemorySettings).
const (
	defaultRecallBudgetTokens = 1500
	defaultRecallLimit        = 10
)

// SetMemoryStore wires the workspace-owned durable memory store into this
// session. A nil store disables all memory features gracefully.
func (s *Session) SetMemoryStore(store *memory.Store) {
	s.mu.Lock()
	previous := s.memoryStore
	s.memoryStore = store
	s.mu.Unlock()
	if store == nil || store == previous {
		return
	}
	ctx, ok := s.memoryJobs.begin()
	if !ok {
		return
	}
	go func() {
		defer s.memoryJobs.wg.Done()
		s.refreshMemorySummary(ctx, store)
	}()
}

// SummaryTitle is a cached projection of the existing durable memory summary.
// Reading it never performs I/O or starts a provider request.
func (s *Session) SummaryTitle() string {
	if summary := s.memorySummary.Load(); summary != nil {
		return summary.Title
	}
	return ""
}

func (s *Session) refreshMemorySummary(ctx context.Context, store *memory.Store) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	summary, found, err := store.GetSessionSummary(ctx, s.sessionID)
	if err != nil || !found || s.MemoryStore() != store {
		return
	}
	for {
		previous := s.memorySummary.Load()
		if previous != nil && previous.UpdatedAt >= summary.UpdatedAt {
			return
		}
		if s.memorySummary.CompareAndSwap(previous, &summary) {
			return
		}
	}
}

// MemoryStore returns the wired store (nil when memory is unavailable).
func (s *Session) MemoryStore() *memory.Store {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.memoryStore
}

// memorySettings returns the effective memory configuration.
func (s *Session) memorySettings() ttypes.MemorySettings {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cfg == nil {
		return ttypes.MemorySettings{}
	}
	settings := s.cfg.Memory
	if settings.MemoryCategory.Enabled != nil && !*settings.MemoryCategory.Enabled {
		settings.AutoRecall, settings.AutoExtract, settings.Consolidation = "off", "off", "off"
	}
	return settings
}

// recordToolTouch accumulates files touched by tools during this session.
// Touched paths boost recall of memories scoped to the same files.
func (s *Session) recordToolTouch(tool string, params map[string]any) {
	var keys []string
	var paths []string
	switch tool {
	case "apply_patch":
		paths = writeToolTargets(tool, params)
	case "read_file", "write_file", "edit_file", "delete_file", "list_directory":
		keys = []string{"path"}
	case "move_file", "copy_file":
		keys = []string{"source", "destination"}
	default:
		return
	}
	s.touchedMu.Lock()
	defer s.touchedMu.Unlock()
	if s.touchedFiles == nil {
		s.touchedFiles = make(map[string]struct{})
	}
	for _, key := range keys {
		paths = append(paths, getStrParam(params, key))
	}
	for _, raw := range paths {
		path := strings.TrimSpace(raw)
		if path == "" || len(s.touchedFiles) >= 128 {
			continue
		}
		s.touchedFiles[path] = struct{}{}
	}
}

// touchedFilesSnapshot returns the touched-file set as a sorted slice.
func (s *Session) touchedFilesSnapshot() []string {
	s.touchedMu.Lock()
	defer s.touchedMu.Unlock()
	if len(s.touchedFiles) == 0 {
		return nil
	}
	out := make([]string, 0, len(s.touchedFiles))
	for path := range s.touchedFiles {
		out = append(out, path)
	}
	sort.Strings(out)
	return out
}

// recallMemoryContext builds the auto-injected system prompt block: scored
// recall over the user prompt plus IDE context, packed under the recall
// token budget. Exposure is logged without reinforcing knowledge.
func (s *Session) recallMemoryContext(
	ctx context.Context, query string, activePaths []string,
) (string, error) {
	store := s.MemoryStore()
	if store == nil {
		return "", nil
	}
	if !s.memorySettings().AutoRecallEnabled() {
		return "", nil
	}
	records, err := store.Recall(ctx, memory.RecallQuery{
		Text:         query,
		ActivePaths:  activePaths,
		TouchedPaths: s.touchedFilesSnapshot(),
		Limit:        s.memorySettings().RecallLimitOrDefault(defaultRecallLimit),
		BudgetTokens: s.memorySettings().RecallBudgetOrDefault(defaultRecallBudgetTokens),
	})
	if err != nil {
		return "", fmt.Errorf("recall workspace memory: %w", err)
	}
	if len(records) == 0 {
		return "", nil
	}
	refs := make([]memory.RevisionRef, 0, len(records))
	projection := make([]rootprompt.WorkspaceMemoryRecord, 0, len(records))
	for _, record := range records {
		refs = append(refs, memory.RevisionRef{ID: record.ID, Revision: record.Revision})
		projection = append(projection, rootprompt.WorkspaceMemoryRecord{ID: record.ID, Revision: record.Revision, Kind: record.Kind,
			Title: record.Title, Source: record.Source, EvidenceCount: len(record.Evidence), AppliesWhen: record.AppliesWhen,
			Content: record.Content, Pinned: record.Pinned})
	}
	if err := store.Expose(ctx, refs, s.sessionID, "automatic"); err != nil {
		return "", fmt.Errorf("record workspace memory exposure: %w", err)
	}
	return rootprompt.WorkspaceMemory(projection), nil
}

// previousSessionContext builds the continuity block injected on the first
// turn of a session: the most recent prior session summaries.
func (s *Session) previousSessionContext(ctx context.Context) (string, error) {
	store := s.MemoryStore()
	if store == nil || !s.memorySettings().AutoRecallEnabled() {
		return "", nil
	}
	summaries, err := store.LatestSummaries(ctx, s.sessionID, 2)
	if err != nil {
		return "", fmt.Errorf("load previous session summaries: %w", err)
	}
	if len(summaries) == 0 {
		return "", nil
	}
	projection := make([]rootprompt.PreviousSession, 0, len(summaries))
	for _, summary := range summaries {
		title := summary.Title
		if title == "" {
			title = "Session " + summary.SessionID
		}
		projection = append(projection, rootprompt.PreviousSession{ID: summary.SessionID, Title: title, Age: formatAge(summary.UpdatedAt),
			Summary: summary.Summary, Outcome: summary.Outcome, KeyFiles: append([]string(nil), summary.KeyFiles...)})
	}
	return rootprompt.PreviousSessions(projection), nil
}

func formatAge(unixNano int64) string {
	if unixNano <= 0 {
		return "unknown time"
	}
	age := time.Since(time.Unix(0, unixNano))
	switch {
	case age < time.Hour:
		return fmt.Sprintf("%dm ago", int(age.Minutes()))
	case age < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(age.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(age.Hours()/24))
	}
}

const (
	historyUserSnippetBytes      = 400
	historyAssistantSnippetBytes = 700
)

// executeMemoryTool handles the memory tool family. Returns handled=false
// for tools that are not memory tools.
func (s *Session) executeMemoryTool(
	ctx context.Context,
	name string,
	params map[string]any,
) (tools.ToolResult, bool, error) {
	switch name {
	case "remember", "recall_memory", "forget_memory", "update_memory", "search_history", "memory_feedback":
	default:
		return tools.ToolResult{}, false, nil
	}
	store := s.MemoryStore()
	if store == nil {
		return tools.ToolResult{}, true, fmt.Errorf("workspace memory is unavailable")
	}
	switch name {
	case "remember":
		evidence, err := memoryEvidenceParam(params)
		if err != nil {
			return tools.ToolResult{}, true, err
		}
		record, err := store.Remember(ctx, memory.Record{
			Kind:            getStrParam(params, "kind"),
			Title:           getStrParam(params, "title"),
			Content:         getStrParam(params, "content"),
			ScopePaths:      getStrSliceParam(params, "paths"),
			Importance:      getIntParam(params, "importance"),
			SourceSessionID: s.sessionID,
			Pinned:          memoryBoolParam(params, "pinned"),
			Source:          memory.SourceTool,
			Provenance:      s.memoryProvenance(),
			AppliesWhen:     getStrParam(params, "applies_when"),
			Evidence:        evidence,
		})
		if err != nil {
			return tools.ToolResult{}, true, err
		}
		data, err := json.Marshal(map[string]any{
			"id": record.ID, "revision": record.Revision, "kind": record.Kind, "title": record.Title,
			"status": "remembered",
		})
		return tools.ToolResult{Content: string(data)}, true, err

	case "recall_memory":
		query := getStrParam(params, "query")
		records, err := store.Recall(ctx, memory.RecallQuery{
			Text:            query,
			ActivePaths:     s.touchedFilesSnapshot(),
			Limit:           getIntParam(params, "limit"),
			IncludeArchived: true,
		})
		if err != nil {
			return tools.ToolResult{}, true, err
		}
		if len(records) == 0 {
			// Explicit empty answer — silent recall failures are a known
			// agent-memory failure mode.
			return tools.ToolResult{Content: fmt.Sprintf(
				"No workspace memories matched %q. Memory search is working; nothing has been stored about this yet.",
				query,
			)}, true, nil
		}
		refs := make([]memory.RevisionRef, 0, len(records))
		results := make([]map[string]any, 0, len(records))
		for _, record := range records {
			refs = append(refs, memory.RevisionRef{ID: record.ID, Revision: record.Revision})
			results = append(results, map[string]any{
				"id": record.ID, "revision": record.Revision, "kind": record.Kind, "title": record.Title,
				"content": record.Content, "importance": record.Importance,
				"pinned": record.Pinned, "status": record.Status,
				"source": record.Source, "evidence_count": len(record.Evidence),
				"evidence": compactMemoryEvidence(record.Evidence), "applies_when": record.AppliesWhen,
				"from_session": record.SourceSessionID,
				"last_updated": formatAge(record.UpdatedAt),
			})
		}
		if err := store.Expose(ctx, refs, s.sessionID, "recall"); err != nil {
			s.logger.Warn("MEMORY", "exposure_failed", "error", err.Error())
		}
		data, err := json.Marshal(map[string]any{"results": results, "total": len(results)})
		return tools.ToolResult{Content: string(data)}, true, err

	case "update_memory":
		id := getStrParam(params, "id")
		if id == "" {
			return tools.ToolResult{}, true, fmt.Errorf("update_memory: id is required")
		}
		content := getStrParam(params, "content")
		if content == "" {
			return tools.ToolResult{}, true, fmt.Errorf("update_memory: content is required")
		}
		expectedRevision, err := memoryExpectedRevision(params, "expected_revision")
		if err != nil {
			return tools.ToolResult{}, true, err
		}
		evidence, err := memoryEvidenceParam(params)
		if err != nil {
			return tools.ToolResult{}, true, err
		}
		existing, err := store.Get(ctx, id)
		if err != nil {
			return tools.ToolResult{}, true, err
		}
		pinned := existing.Pinned
		if _, specified := params["pinned"]; specified {
			pinned = memoryBoolParam(params, "pinned")
		}
		record, err := store.Update(ctx, id, memory.Record{
			Kind:        getStrParam(params, "kind"),
			Title:       getStrParam(params, "title"),
			Content:     content,
			ScopePaths:  getStrSliceParam(params, "paths"),
			Importance:  getIntParam(params, "importance"),
			Pinned:      pinned,
			Source:      memory.SourceTool,
			Provenance:  s.memoryProvenance(),
			Revision:    expectedRevision,
			AppliesWhen: getStrParam(params, "applies_when"),
			Evidence:    evidence,
		}, memory.ActorTool, s.sessionID)
		if err != nil {
			return tools.ToolResult{}, true, err
		}
		data, err := json.Marshal(map[string]any{
			"id": record.ID, "revision": record.Revision, "previous_revision": expectedRevision, "status": "updated",
		})
		return tools.ToolResult{Content: string(data)}, true, err

	case "forget_memory":
		id := getStrParam(params, "id")
		if id == "" {
			return tools.ToolResult{}, true, fmt.Errorf("forget_memory: id is required")
		}
		expectedRevision, err := memoryExpectedRevision(params, "expected_revision")
		if err != nil {
			return tools.ToolResult{}, true, err
		}
		if err := store.Supersede(ctx, id, expectedRevision, "", memory.ActorTool, s.sessionID); err != nil {
			return tools.ToolResult{}, true, err
		}
		return tools.ToolResult{Content: fmt.Sprintf(
			`{"status":"forgotten","id":%q,"revision":%d}`, id, expectedRevision+1,
		)}, true, nil

	case "memory_feedback":
		payload, err := json.Marshal(params["items"])
		if err != nil {
			return tools.ToolResult{}, true, err
		}
		var items []memory.Feedback
		if err := json.Unmarshal(payload, &items); err != nil {
			return tools.ToolResult{}, true, fmt.Errorf("memory_feedback: invalid items: %w", err)
		}
		for index := range items {
			items[index].SessionID, items[index].Actor = s.sessionID, memory.ActorTool
		}
		if err := store.Feedback(ctx, items); err != nil {
			return tools.ToolResult{}, true, err
		}
		return tools.ToolResult{Content: fmt.Sprintf(`{"status":"recorded","count":%d}`, len(items))}, true, nil

	case "search_history":
		query := getStrParam(params, "query")
		if strings.TrimSpace(query) == "" {
			return tools.ToolResult{}, true, fmt.Errorf("search_history: query is required")
		}
		hits, summaries, err := store.SearchHistory(ctx, query, getIntParam(params, "limit"))
		if err != nil {
			return tools.ToolResult{}, true, err
		}
		if len(hits) == 0 && len(summaries) == 0 {
			return tools.ToolResult{Content: fmt.Sprintf(
				"No past turns or session summaries matched %q. History search is working; this workspace has no recorded work on that topic.",
				query,
			)}, true, nil
		}
		turnResults := make([]map[string]any, 0, len(hits))
		for _, hit := range hits {
			turnResults = append(turnResults, map[string]any{
				"session_id": hit.SessionID,
				"turn":       hit.Turn,
				"when":       formatAge(hit.CreatedAt),
				"user":       memory.TruncateBytes(hit.UserText, historyUserSnippetBytes),
				"assistant":  memory.TruncateBytes(hit.AssistantText, historyAssistantSnippetBytes),
				"tools":      hit.ToolNames,
				"files":      hit.Files,
			})
		}
		sessionResults := make([]map[string]any, 0, len(summaries))
		for _, summary := range summaries {
			sessionResults = append(sessionResults, map[string]any{
				"session_id": summary.SessionID,
				"title":      summary.Title,
				"summary":    summary.Summary,
				"outcome":    summary.Outcome,
				"key_files":  summary.KeyFiles,
				"when":       formatAge(summary.UpdatedAt),
			})
		}
		data, err := json.Marshal(map[string]any{
			"turns": turnResults, "sessions": sessionResults,
		})
		return tools.ToolResult{Content: string(data)}, true, err
	}
	return tools.ToolResult{}, false, nil
}

func compactMemoryEvidence(evidence []memory.Evidence) []memory.Evidence {
	compact := append([]memory.Evidence(nil), evidence...)
	for index := range compact {
		compact[index].Summary = memory.TruncateBytes(compact[index].Summary, 160)
	}
	return compact
}

func memoryEvidenceParam(params map[string]any) ([]memory.Evidence, error) {
	raw, found := params["evidence"]
	if !found {
		return nil, nil
	}
	payload, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var evidence []memory.Evidence
	if err := json.Unmarshal(payload, &evidence); err != nil {
		return nil, fmt.Errorf("memory: evidence must be an array of origin-attributed references: %w", err)
	}
	return evidence, nil
}

func memoryExpectedRevision(params map[string]any, key string) (int64, error) {
	var revision int64
	switch value := params[key].(type) {
	case int:
		revision = int64(value)
	case int64:
		revision = value
	case float64:
		if value <= 0 || value > 1<<53 || math.Trunc(value) != value {
			return 0, fmt.Errorf("memory: %s must be a positive integer", key)
		}
		revision = int64(value)
	case json.Number:
		var err error
		revision, err = value.Int64()
		if err != nil {
			return 0, fmt.Errorf("memory: invalid %s", key)
		}
	}
	if revision <= 0 {
		return 0, fmt.Errorf("memory: %s is required and must be a positive integer", key)
	}
	return revision, nil
}

func (s *Session) memoryProvenance() string {
	return fmt.Sprintf("session:%s turn:%d", s.sessionID, s.turnNumber)
}

func memoryBoolParam(params map[string]any, key string) bool {
	value, _ := params[key].(bool)
	return value
}

func getStrSliceParam(params map[string]any, key string) []string {
	raw, ok := params[key]
	if !ok {
		return nil
	}
	switch v := raw.(type) {
	case []string:
		return v
	case []any:
		var out []string
		for _, item := range v {
			if str, ok := item.(string); ok && strings.TrimSpace(str) != "" {
				out = append(out, strings.TrimSpace(str))
			}
		}
		return out
	case string:
		var out []string
		for _, part := range strings.Split(v, ",") {
			if part = strings.TrimSpace(part); part != "" {
				out = append(out, part)
			}
		}
		return out
	}
	return nil
}
