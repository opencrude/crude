package sessiondb

import (
	"database/sql"
	"fmt"
	"strconv"
)

// UsageRecord is one idempotent provider request accounting record.
type UsageRecord struct {
	RequestID         string
	SessionID         string
	AgentID           string
	ParentAgentID     string
	TurnGeneration    uint64
	Attempt           int
	RequestKind       string
	ProviderID        string
	ModelID           string
	PromptTokens      int
	CompletionTokens  int
	CachedTokens      int
	CacheReadKnown    bool
	CacheWriteTokens  int
	CacheWriteKnown   bool
	ReasoningTokens   int
	ReasoningKnown    bool
	ReasoningInOutput bool
	ContextUsed       int
	ContextWindow     int
	CostUSD           float64
	CostKnown         bool
	CostSource        string
	Estimated         bool
	Partial           bool
	CostUpperBound    bool
	CreatedAt         int64
	UpdatedAt         int64
}

func UpsertUsageRecord(db *sql.DB, r UsageRecord) error {
	if db == nil || r.RequestID == "" {
		return nil
	}
	_, err := db.Exec(`
		INSERT INTO usage_requests (
			request_id, session_id, agent_id, parent_agent_id, turn_generation,
			attempt, request_kind, provider_id, model_id, prompt_tokens, completion_tokens,
			cached_tokens, cache_read_known, cache_write_tokens, cache_write_known,
			reasoning_tokens, reasoning_known, reasoning_in_output,
			context_used, context_window, cost_usd, cost_known, estimated, partial,
			cost_source, cost_upper_bound, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(request_id) DO UPDATE SET
			prompt_tokens=excluded.prompt_tokens,
			completion_tokens=excluded.completion_tokens,
			request_kind=excluded.request_kind,
			cached_tokens=excluded.cached_tokens,
			cache_read_known=excluded.cache_read_known,
			cache_write_tokens=excluded.cache_write_tokens,
			cache_write_known=excluded.cache_write_known,
			reasoning_tokens=excluded.reasoning_tokens,
			reasoning_known=excluded.reasoning_known,
			reasoning_in_output=excluded.reasoning_in_output,
			context_used=excluded.context_used,
			context_window=excluded.context_window,
			cost_usd=excluded.cost_usd,
			cost_known=excluded.cost_known,
			cost_source=excluded.cost_source,
			estimated=excluded.estimated,
			partial=excluded.partial,
			cost_upper_bound=excluded.cost_upper_bound,
			updated_at=excluded.updated_at
		WHERE excluded.updated_at > usage_requests.updated_at
	`, r.RequestID, r.SessionID, r.AgentID, r.ParentAgentID, strconv.FormatUint(r.TurnGeneration, 10),
		r.Attempt, requestKind(r.RequestKind), r.ProviderID, r.ModelID, r.PromptTokens, r.CompletionTokens,
		r.CachedTokens, r.CacheReadKnown, r.CacheWriteTokens, r.CacheWriteKnown,
		r.ReasoningTokens, r.ReasoningKnown, r.ReasoningInOutput,
		r.ContextUsed, r.ContextWindow, r.CostUSD, r.CostKnown, r.Estimated, r.Partial,
		r.CostSource, r.CostUpperBound, r.CreatedAt, r.UpdatedAt)
	if err != nil {
		return fmt.Errorf("sessiondb: upsert usage request: %w", err)
	}
	return nil
}

func ListUsageRecords(db *sql.DB) ([]UsageRecord, error) {
	if db == nil {
		return nil, nil
	}
	rows, err := db.Query(`
		SELECT request_id, session_id, agent_id, parent_agent_id, turn_generation,
		       attempt, request_kind, provider_id, model_id, prompt_tokens, completion_tokens,
		       cached_tokens, cache_read_known, cache_write_tokens, cache_write_known,
		       reasoning_tokens, reasoning_known, reasoning_in_output,
		       context_used, context_window, cost_usd, cost_known, estimated, partial,
		       cost_source, cost_upper_bound, created_at, updated_at
		FROM usage_requests ORDER BY created_at, request_id`)
	if err != nil {
		return nil, fmt.Errorf("sessiondb: list usage requests: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []UsageRecord
	for rows.Next() {
		var r UsageRecord
		if err := rows.Scan(&r.RequestID, &r.SessionID, &r.AgentID, &r.ParentAgentID,
			&r.TurnGeneration, &r.Attempt, &r.RequestKind, &r.ProviderID, &r.ModelID, &r.PromptTokens,
			&r.CompletionTokens, &r.CachedTokens, &r.CacheReadKnown,
			&r.CacheWriteTokens, &r.CacheWriteKnown, &r.ReasoningTokens,
			&r.ReasoningKnown, &r.ReasoningInOutput, &r.ContextUsed, &r.ContextWindow, &r.CostUSD,
			&r.CostKnown, &r.Estimated, &r.Partial, &r.CostSource, &r.CostUpperBound,
			&r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, fmt.Errorf("sessiondb: scan usage request: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func requestKind(kind string) string {
	if kind == "" {
		return "normal"
	}
	return kind
}
