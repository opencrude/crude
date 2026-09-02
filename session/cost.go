package session

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"crude/logger"
	"crude/provider"
	"crude/sessiondb"
)

// CostAccumulator tracks cumulative token usage and cost across a session.
// It accumulates real token counts from API usage responses and computes
// USD cost using provider pricing data.
//
// Thread-safe: all methods are safe to call from concurrent goroutines
// (the agent loop emits usage events from a goroutine).
type CostAccumulator struct {
	mu sync.Mutex

	// Running totals across all API calls in the session
	TotalPromptTokens     int
	TotalCompletionTokens int
	TotalCachedTokens     int
	TotalCost             float64

	// Last API response token counts (for TUI display of per-turn usage)
	LastPromptTokens     int
	LastCompletionTokens int
	LastCachedTokens     int
	LastPromptCost       float64
	LastCompletionCost   float64

	// Per-model breakdown (model ID → cumulative stats)
	ByModel map[string]*ModelCost

	logger *logger.Logger
}

// ModelCost tracks usage and cost for a single model within a session.
type ModelCost struct {
	PromptTokens     int
	CompletionTokens int
	CachedTokens     int
	Cost             float64
}

// NewCostAccumulator creates a ready-to-use CostAccumulator.
func NewCostAccumulator(lg *logger.Logger) *CostAccumulator {
	return &CostAccumulator{
		ByModel: make(map[string]*ModelCost),
		logger:  lg,
	}
}

// RecordUsage records token usage from a single API call.
// model is the model ID used for the call. price is looked up
// from the provider pricing map and may be zero-valued if
// the model's pricing is unknown.
func (c *CostAccumulator) RecordUsage(model string, promptTokens, completionTokens, cachedTokens int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	price := provider.LookupPrice(model)
	cost := price.CalculateCost(promptTokens, completionTokens, cachedTokens)

	if cost == 0 && promptTokens+completionTokens > 0 {
		if c.logger != nil {
			c.logger.Warn("COST", "unknown_model_pricing", "model", model, "prompt", promptTokens, "completion", completionTokens)
		}
	}

	c.TotalPromptTokens += promptTokens
	c.TotalCompletionTokens += completionTokens
	c.TotalCachedTokens += cachedTokens
	c.TotalCost += cost

	c.LastPromptTokens = promptTokens
	c.LastCompletionTokens = completionTokens
	c.LastCachedTokens = cachedTokens
	c.LastPromptCost = price.InputPricePerToken * float64(promptTokens-cachedTokens)
	if cachedTokens > 0 {
		cachedPrice := price.InputCachedPricePerToken
		if cachedPrice == 0 {
			cachedPrice = price.InputPricePerToken
		}
		c.LastPromptCost += cachedPrice * float64(cachedTokens)
	}
	c.LastCompletionCost = price.OutputPricePerToken * float64(completionTokens)

	// Per-model breakdown
	mc, ok := c.ByModel[model]
	if !ok {
		mc = &ModelCost{}
		c.ByModel[model] = mc
	}
	mc.PromptTokens += promptTokens
	mc.CompletionTokens += completionTokens
	mc.CachedTokens += cachedTokens
	mc.Cost += cost

	if c.logger != nil {
		c.logger.Debug("COST", "record", "model", model, "prompt", promptTokens, "completion", completionTokens, "cached", cachedTokens, "cost", cost, "total", c.TotalCost)
	}
}

// Snapshot returns a copy of the accumulator's state suitable for
// serialization or display. The copy is not connected to the original.
func (c *CostAccumulator) Snapshot() CostSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()

	snap := CostSnapshot{
		TotalPromptTokens:     c.TotalPromptTokens,
		TotalCompletionTokens: c.TotalCompletionTokens,
		TotalCachedTokens:     c.TotalCachedTokens,
		TotalCost:             c.TotalCost,
		ByModel:               make(map[string]ModelCost, len(c.ByModel)),
	}
	for k, v := range c.ByModel {
		snap.ByModel[k] = ModelCost{
			PromptTokens:     v.PromptTokens,
			CompletionTokens: v.CompletionTokens,
			CachedTokens:     v.CachedTokens,
			Cost:             v.Cost,
		}
	}
	return snap
}

func (c *CostAccumulator) Restore(snapshot CostSnapshot) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.TotalPromptTokens = snapshot.TotalPromptTokens
	c.TotalCompletionTokens = snapshot.TotalCompletionTokens
	c.TotalCachedTokens = snapshot.TotalCachedTokens
	c.TotalCost = snapshot.TotalCost
	c.ByModel = make(map[string]*ModelCost, len(snapshot.ByModel))
	for model, value := range snapshot.ByModel {
		copy := value
		c.ByModel[model] = &copy
	}
}

func restoreCostAccumulator(db *sql.DB, accumulator *CostAccumulator) error {
	if db == nil || accumulator == nil {
		return nil
	}
	totals, ok, err := sessiondb.GetLatestCostTotals(db)
	if err != nil || !ok {
		return err
	}
	snapshot := CostSnapshot{
		TotalPromptTokens: totals.PromptTokens, TotalCompletionTokens: totals.CompletionTokens,
		TotalCachedTokens: totals.CachedTokens, TotalCost: totals.TotalCost,
		ByModel: make(map[string]ModelCost, len(totals.ByModel)),
	}
	for model, raw := range totals.ByModel {
		var value ModelCost
		if json.Unmarshal(raw, &value) == nil {
			snapshot.ByModel[model] = value
		}
	}
	accumulator.Restore(snapshot)
	return nil
}

// CostSnapshot is a serializable copy of the CostAccumulator state.
type CostSnapshot struct {
	TotalPromptTokens     int                  `json:"total_prompt_tokens"`
	TotalCompletionTokens int                  `json:"total_completion_tokens"`
	TotalCachedTokens     int                  `json:"total_cached_tokens"`
	TotalCost             float64              `json:"total_cost"`
	ByModel               map[string]ModelCost `json:"by_model"`
}

// TokenTracker tracks real token counts from API responses, separate
// from cost. This is used by the session to make truncation decisions
// based on actual (not estimated) token counts when available.
type TokenTracker struct {
	mu sync.Mutex

	// The most recent real prompt token count from the API.
	// This is the actual size of the context sent in the last turn.
	LastRealPromptTokens int

	// Cumulative real prompt tokens across all turns.
	CumulativePromptTokens     int
	CumulativeCompletionTokens int

	logger *logger.Logger
}

// NewTokenTracker creates a ready-to-use TokenTracker.
func NewTokenTracker(lg *logger.Logger) *TokenTracker {
	return &TokenTracker{logger: lg}
}

// Record records token counts from a single API response.
func (t *TokenTracker) Record(promptTokens, completionTokens int) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.LastRealPromptTokens = promptTokens
	t.CumulativePromptTokens += promptTokens
	t.CumulativeCompletionTokens += completionTokens

	if t.logger != nil {
		t.logger.Debug("TOKENS", "real_usage", "prompt", promptTokens, "completion", completionTokens, "cumulative_prompt", t.CumulativePromptTokens)
	}
}

// GetLastRealPromptTokens returns the most recent real prompt token count.
func (t *TokenTracker) GetLastRealPromptTokens() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.LastRealPromptTokens
}

func (s *Session) persistCostSnapshot() {
	snap := s.cost.Snapshot()
	snapData, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		s.logger.Error("COST", "persist_error", "error", err.Error())
		return
	}

	costPath := filepath.Join(s.logger.SessionDir(), "cost.json")
	if err := os.WriteFile(costPath, snapData, 0o644); err != nil {
		s.logger.Error("COST", "persist_error", "error", err.Error())
		return
	}

	s.logger.Info("COST", "persisted", "path", costPath, "total_cost", snap.TotalCost,
		"prompt_tokens", snap.TotalPromptTokens, "completion_tokens", snap.TotalCompletionTokens)
}

func (s *Session) persistCostToDB() {
	if s.db == nil {
		return
	}
	snap := s.cost.Snapshot()
	byModel := make(map[string]json.RawMessage, len(snap.ByModel))
	for k, v := range snap.ByModel {
		b, _ := json.Marshal(v)
		byModel[k] = b
	}
	if err := sessiondb.InsertCostSnapshot(s.db,
		snap.TotalPromptTokens, snap.TotalCompletionTokens,
		snap.TotalCachedTokens, snap.TotalCost, byModel); err != nil {
		s.logger.Error("COST", "db_persist_error", "error", err.Error())
	}
}
