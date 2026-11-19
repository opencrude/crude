package session

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"crude/agent"
	"crude/agent/memory"
	"crude/agent/prompt"
	"crude/agent/provider"
	"crude/agent/tools"
	"crude/types"
)

// Ranking is a bounded optional read, on the owning run's goroutine. No worker
// outlives cancellation, and failed or slow inference cannot stop the agent.
const rankingDeadline = 2 * time.Second

type actionMemoryState struct {
	Task       string            `json:"task"`
	Actions    []string          `json:"actions"`
	Candidates map[string]string `json:"memories"`
}

func (s *Session) bindMemoryRanker(a *agent.Agent, owner string) {
	var preceding int
	initialized := false
	a.RankActionMemories = func(ctx context.Context, round int, active, batch []provider.ChatMessage) (func() (string, error), error) {
		if round == 1 {
			initialized = false
		}
		if !initialized && s.memorySettings().Ranker.Enabled == "on" {
			// Count completed rounds once in this agent's own durable archive. Short
			// turns and resumed children keep the same cadence as a long root turn.
			preceding = 0
			inRound := false
			for _, m := range a.ContextArchive {
				if m.Role != "tool" {
					inRound = false
					continue
				}
				if !inRound {
					preceding++
					inRound = true
				}
			}
			initialized = true
		}
		return s.rankActionMemories(ctx, owner, a.WorkDir, preceding+round, active, batch)
	}
}

func (s *Session) rankActionMemories(parent context.Context, owner, root string, round int, active, batch []provider.ChatMessage) (func() (string, error), error) {
	s.mu.Lock()
	settings, store, revision := s.memorySettingsLocked(), s.memoryStore, s.settingsRevision
	s.mu.Unlock()
	r := settings.Ranker.WithDefaults()
	if !settings.AutoRecallEnabled() || r.Enabled != "on" {
		return nil, nil
	}
	if err := r.Validate(); err != nil {
		return nil, err
	}
	if round < 1 || round%r.EveryActions != 0 || len(batch) < 2 {
		return nil, nil
	}
	if store == nil {
		return nil, nil
	}
	deadline := rankingDeadline
	if r.Endpoint == types.MemoryRankerChat {
		deadline = 8 * time.Second
	}
	ctx, cancel := context.WithTimeout(parent, deadline)
	defer cancel()
	state, paths := actionRankingState(active, batch, root)
	if len(state.Actions) == 0 {
		return nil, nil
	}
	seen, spent, count := activeRecallUsage(active)
	budget := settings.RecallBudgetOrDefault(defaultRecallBudgetTokens) - spent
	limit := min(r.TopK, settings.RecallLimitOrDefault(defaultRecallLimit)-count)
	if budget <= 0 || limit <= 0 {
		return nil, nil
	}
	records, err := store.Recall(ctx, memory.RecallQuery{Text: strings.Join(state.Actions, " ") + " " + state.Task, ActivePaths: paths, Limit: 50})
	if err != nil {
		return nil, err
	}
	var pool []memory.ScoredRecord
	var questions []string
	for _, rec := range records {
		if seen[memory.RevisionRef{ID: rec.ID, Revision: rec.Revision}] || memory.EstimateTokens(recallBlock([]memory.ScoredRecord{rec})) > budget {
			continue
		}
		key := fmt.Sprintf("m%d", len(pool))
		item := rankExcerpt(rec.Title, 64) + ": " + rankExcerpt(rec.Content, 216)
		if rec.AppliesWhen != "" {
			item += "; when: " + rankExcerpt(rec.AppliesWhen, 80)
		}
		// Evidence-backed feedback about this revision can change the next decision.
		// Exposure, popularity and a successful turn are deliberately not rewards.
		feedback, err := store.FeedbackForMemory(ctx, rec.ID, 4)
		if err != nil {
			return nil, err
		}
		for _, f := range feedback {
			if f.Revision == rec.Revision && len(f.Evidence) > 0 {
				item += "; prior feedback: " + f.Kind + " (" + rankExcerpt(f.Evidence[0].Summary, 72) + ")"
				break
			}
		}
		state.Candidates[key] = item
		pool = append(pool, rec)
		questions = append(questions, prompt.MemoryRelevanceQuestion(key))
		if len(pool) == types.MaxRankedMemories {
			break
		}
	}
	if len(pool) == 0 {
		return nil, nil
	}
	wire, err := json.Marshal(state)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	var conn *provider.Provider
	if now := s.memorySettingsLocked(); s.settingsRevision == revision && s.cfg != nil && now.AutoRecallEnabled() && now.Ranker == settings.Ranker && s.memoryStore == store {
		conn, err = s.cfg.providerForCategory(CategoryConfig{AccountID: r.AccountID, Model: r.Model})
	}
	s.mu.Unlock()
	if err != nil || conn == nil {
		return nil, err
	}
	var scores []float64
	if r.Endpoint == types.MemoryRankerChat {
		scores, err = conn.ScoreChatRelevance(ctx, prompt.MemoryChatRankingSystem(), string(wire), len(pool))
	} else {
		scores, err = conn.DecideRelevance(ctx, r.Endpoint, string(wire), questions)
	}
	if err != nil {
		return nil, err
	}
	for i := range pool {
		pool[i].Score = scores[i]
	}
	sort.SliceStable(pool, func(i, j int) bool { return pool[i].Score > pool[j].Score })
	var selected []memory.ScoredRecord
	for _, rec := range pool {
		if rec.Score <= 0.5 || len(selected) >= limit {
			break
		}
		next := append(selected, rec)
		if memory.EstimateTokens(recallBlock(next)) <= budget {
			selected = next
		}
	}
	if len(selected) == 0 {
		return nil, nil
	}
	return func() (string, error) {
		s.mu.Lock()
		now, liveStore, currentRevision := s.memorySettingsLocked(), s.memoryStore, s.settingsRevision
		s.mu.Unlock()
		if currentRevision != revision || parent.Err() != nil || !now.AutoRecallEnabled() || now.Ranker != settings.Ranker || now.RecallBudgetTokens != settings.RecallBudgetTokens || now.RecallLimit != settings.RecallLimit || liveStore != store {
			return "", nil
		}
		return projectRecall(parent, store, selected, owner, "ranked")
	}, nil
}

func rankExcerpt(text string, limit int) string {
	return memory.TruncateBytes(memory.Redact(strings.TrimSpace(text)), limit)
}

func actionRankingState(active, batch []provider.ChatMessage, root string) (actionMemoryState, []string) {
	state := actionMemoryState{Candidates: map[string]string{}}
	for i := len(active) - 1; i >= 0; i-- {
		m := active[i]
		if m.IsGenuineUserInput() || m.Origin == provider.OriginAssignment {
			text := m.DisplayContent
			if text == "" && m.Content.IsText() {
				text = string(m.Content)
			}
			state.Task = rankExcerpt(text, 384)
			break
		}
	}
	var paths []string
	add := func(call provider.ToolCall, result provider.ChatMessage) {
		if len(state.Actions) >= 4 {
			return
		}
		name := call.Function.Name
		summary := name
		if len(call.Function.Arguments) <= 64<<10 {
			summary += " " + rankExcerpt(tools.SummarizeArgs(name, call.Function.Arguments), 120)
			for _, path := range pathsFromToolArgs(call.Function.Arguments) {
				if filepath.IsAbs(path) {
					rel, e := filepath.Rel(root, path)
					if e != nil || !filepath.IsLocal(rel) {
						continue
					}
					path = rel
				}
				paths = append(paths, filepath.ToSlash(path))
			}
		}
		if result.ToolOutcome != nil && result.ToolOutcome.State != "" {
			summary += " (" + string(result.ToolOutcome.State) + ")"
		} else if result.ToolError {
			summary += " (failed)"
		}
		if result.Content.IsText() && memoryObservableTool(name) {
			text := string(result.Content)
			if (name == "execute_command" || name == "exec_command") && len(text) <= 64<<10 {
				if output, ok := tools.ParseCommandResult(name, text); ok {
					text = output.Output
				}
			}
			summary += ": " + rankExcerpt(memory.TruncateBytes(text, 4096), 160)
		}
		state.Actions = append(state.Actions, summary)
	}
	results := make(map[string]provider.ChatMessage, len(batch)-1)
	for _, m := range batch[1:] {
		results[m.ToolCallID] = m
	}
	for _, call := range batch[0].ToolCalls {
		result, ok := results[call.ID]
		if !ok {
			continue
		}
		if call.Function.Name == "multi_tool_use.parallel" && len(call.Function.Arguments) <= 64<<10 {
			var in tools.ParallelInput
			var out tools.ParallelOutput
			if len(result.DisplayContent) <= 128<<10 {
				_ = json.Unmarshal([]byte(result.DisplayContent), &out)
			}
			if json.Unmarshal([]byte(call.Function.Arguments), &in) == nil && len(in.Uses) > 0 {
				for i, use := range in.Uses {
					if len(state.Actions) >= 4 {
						break
					}
					name := strings.TrimPrefix(use.Name, "functions.")
					leaf := provider.ChatMessage{}
					if i < len(out.Results) && out.Results[i] != nil && out.Results[i].Name == name {
						value := out.Results[i]
						leaf.Content = provider.TextContent(value.Result)
						leaf.ToolError = value.Error
						leaf.ToolOutcome = &value.Outcome
					}
					add(provider.ToolCall{Function: provider.FunctionCall{Name: name, Arguments: string(use.Parameters)}}, leaf)
				}
				continue
			}
		}
		add(call, result)
		if len(state.Actions) >= 4 {
			break
		}
	}
	return state, paths
}

// Only count Crude's hidden recall blocks. A tool result or human message
// quoting one cannot consume another agent's budget or suppress a memory.
func activeRecallUsage(active []provider.ChatMessage) (map[memory.RevisionRef]bool, int, int) {
	seen := map[memory.RevisionRef]bool{}
	spent, count := 0, 0
	for _, m := range active {
		if m.IsGenuineUserInput() || m.Origin == provider.OriginAssignment {
			spent, count = 0, 0
			continue
		}
		if m.Origin != provider.OriginRuntimeContext || m.Visibility != provider.VisibilityContextOnly || !m.Content.IsText() {
			continue
		}
		block, items := prompt.ReadWorkspaceMemory(string(m.Content))
		if len(items) == 0 {
			continue
		}
		spent += memory.EstimateTokens(block)
		count += len(items)
		for _, v := range items {
			seen[memory.RevisionRef{ID: v.ID, Revision: v.Revision}] = true
		}
	}
	return seen, spent, count
}
