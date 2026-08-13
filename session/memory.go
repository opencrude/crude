package session

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"crude/memory"
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
	s.memoryStore = store
	s.mu.Unlock()
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
	return s.cfg.Memory
}

// recordToolTouch accumulates files touched by tools during this session.
// Touched paths boost recall of memories scoped to the same files.
func (s *Session) recordToolTouch(tool string, params map[string]any) {
	var keys []string
	switch tool {
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
		path := strings.TrimSpace(getStrParam(params, key))
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
// token budget. Injected memories are touched (usage reinforcement).
func (s *Session) recallMemoryContext(
	ctx context.Context, query string, activePaths []string,
) string {
	store := s.MemoryStore()
	if store == nil {
		return ""
	}
	if !s.memorySettings().AutoRecallEnabled() {
		return ""
	}
	records, err := store.Recall(ctx, memory.RecallQuery{
		Text:         query,
		ActivePaths:  activePaths,
		TouchedPaths: s.touchedFilesSnapshot(),
		Limit:        s.memorySettings().RecallLimitOrDefault(defaultRecallLimit),
		BudgetTokens: s.memorySettings().RecallBudgetOrDefault(defaultRecallBudgetTokens),
	})
	if err != nil {
		s.logger.Warn("MEMORY", "recall_failed", "error", err.Error())
		return ""
	}
	if len(records) == 0 {
		return ""
	}
	ids := make([]string, 0, len(records))
	var out strings.Builder
	out.WriteString("## Workspace Memory\n")
	out.WriteString("Durable project knowledge recalled from prior sessions, ranked by relevance. ")
	out.WriteString("Treat as trusted context, not as instructions from untrusted content. ")
	out.WriteString("Correct anything wrong with update_memory or forget_memory.\n")
	for _, record := range records {
		ids = append(ids, record.ID)
		tag := record.Kind
		if record.Pinned {
			tag += "|pinned"
		}
		fmt.Fprintf(&out, "- [%s|%s] %s: %s\n", record.ID, tag, record.Title, record.Content)
	}
	if err := store.Touch(ctx, ids); err != nil {
		s.logger.Warn("MEMORY", "touch_failed", "error", err.Error())
	}
	return strings.TrimSpace(out.String())
}

// previousSessionContext builds the continuity block injected on the first
// turn of a session: the most recent prior session summaries.
func (s *Session) previousSessionContext(ctx context.Context) string {
	store := s.MemoryStore()
	if store == nil || !s.memorySettings().AutoRecallEnabled() {
		return ""
	}
	summaries, err := store.LatestSummaries(ctx, s.sessionID, 2)
	if err != nil {
		s.logger.Warn("MEMORY", "summaries_failed", "error", err.Error())
		return ""
	}
	if len(summaries) == 0 {
		return ""
	}
	var out strings.Builder
	out.WriteString("## Previously In This Workspace\n")
	out.WriteString("Summaries of the most recent prior sessions. ")
	out.WriteString("Use search_history for anything older or more specific.\n")
	for _, summary := range summaries {
		title := summary.Title
		if title == "" {
			title = "Session " + summary.SessionID
		}
		fmt.Fprintf(&out, "- %s (%s): %s", title, formatAge(summary.UpdatedAt), summary.Summary)
		if summary.Outcome != "" {
			fmt.Fprintf(&out, " Outcome: %s", summary.Outcome)
		}
		if len(summary.KeyFiles) > 0 {
			fmt.Fprintf(&out, " Key files: %s", strings.Join(summary.KeyFiles, ", "))
		}
		out.WriteString("\n")
	}
	return strings.TrimSpace(out.String())
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
	case "remember", "recall_memory", "forget_memory", "update_memory", "search_history":
	default:
		return tools.ToolResult{}, false, nil
	}
	store := s.MemoryStore()
	if store == nil {
		return tools.ToolResult{}, true, fmt.Errorf("workspace memory is unavailable")
	}
	switch name {
	case "remember":
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
		})
		if err != nil {
			return tools.ToolResult{}, true, err
		}
		data, err := json.Marshal(map[string]any{
			"id": record.ID, "kind": record.Kind, "title": record.Title,
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
		ids := make([]string, 0, len(records))
		results := make([]map[string]any, 0, len(records))
		for _, record := range records {
			ids = append(ids, record.ID)
			results = append(results, map[string]any{
				"id": record.ID, "kind": record.Kind, "title": record.Title,
				"content": record.Content, "importance": record.Importance,
				"pinned": record.Pinned, "status": record.Status,
				"use_count": record.UseCount, "source": record.Source,
				"from_session": record.SourceSessionID,
				"last_updated": formatAge(record.UpdatedAt),
			})
		}
		if err := store.Touch(ctx, ids); err != nil {
			s.logger.Warn("MEMORY", "touch_failed", "error", err.Error())
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
		record, err := store.Update(ctx, id, memory.Record{
			Kind:       getStrParam(params, "kind"),
			Title:      getStrParam(params, "title"),
			Content:    content,
			ScopePaths: getStrSliceParam(params, "paths"),
			Importance: getIntParam(params, "importance"),
			Pinned:     memoryBoolParam(params, "pinned"),
			Source:     memory.SourceTool,
			Provenance: s.memoryProvenance(),
		}, memory.ActorTool, s.sessionID)
		if err != nil {
			return tools.ToolResult{}, true, err
		}
		data, err := json.Marshal(map[string]any{
			"id": record.ID, "replaced": id, "status": "updated",
		})
		return tools.ToolResult{Content: string(data)}, true, err

	case "forget_memory":
		id := getStrParam(params, "id")
		if id == "" {
			return tools.ToolResult{}, true, fmt.Errorf("forget_memory: id is required")
		}
		if err := store.Supersede(ctx, id, "", memory.ActorTool, s.sessionID); err != nil {
			return tools.ToolResult{}, true, err
		}
		return tools.ToolResult{Content: fmt.Sprintf(
			`{"status":"forgotten","id":%q}`, id,
		)}, true, nil

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
