package session

import (
	"sync"

	"crude/provider"
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
}

// ModelCost tracks usage and cost for a single model within a session.
type ModelCost struct {
	PromptTokens     int
	CompletionTokens int
	CachedTokens     int
	Cost             float64
}

// NewCostAccumulator creates a ready-to-use CostAccumulator.
func NewCostAccumulator() *CostAccumulator {
	return &CostAccumulator{
		ByModel: make(map[string]*ModelCost),
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
	// (Note: this is NOT useful for budget calculation since
	// prompt_tokens is absolute, not delta — see agent.go comments.)
	CumulativePromptTokens int
	CumulativeCompletionTokens int
}

// NewTokenTracker creates a ready-to-use TokenTracker.
func NewTokenTracker() *TokenTracker {
	return &TokenTracker{}
}

// Record records token counts from a single API response.
func (t *TokenTracker) Record(promptTokens, completionTokens int) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.LastRealPromptTokens = promptTokens
	t.CumulativePromptTokens += promptTokens
	t.CumulativeCompletionTokens += completionTokens
}

// GetLastRealPromptTokens returns the most recent real prompt token count.
func (t *TokenTracker) GetLastRealPromptTokens() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.LastRealPromptTokens
}
