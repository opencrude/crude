package session

import (
	"sync"

	"crude/logger"
)

// ModelCost is a request-ledger projection for one model.
type ModelCost struct {
	PromptTokens     int
	CompletionTokens int
	CachedTokens     int
	Cost             float64
}

// CostSnapshot is derived from immutable request records. It is not an
// independently accumulated source of truth.
type CostSnapshot struct {
	TotalPromptTokens     int                  `json:"total_prompt_tokens"`
	TotalCompletionTokens int                  `json:"total_completion_tokens"`
	TotalCachedTokens     int                  `json:"total_cached_tokens"`
	TotalCost             float64              `json:"total_cost"`
	ByModel               map[string]ModelCost `json:"by_model"`
	LastPromptTokens      int                  `json:"last_prompt_tokens"`
	LastCompletionTokens  int                  `json:"last_completion_tokens"`
	LastCachedTokens      int                  `json:"last_cached_tokens"`
}

// TokenTracker retains real provider counts used for context truncation. It is
// deliberately separate from billing/accounting projections.
type TokenTracker struct {
	mu sync.Mutex

	LastRealPromptTokens       int
	CumulativePromptTokens     int
	CumulativeCompletionTokens int
	logger                     *logger.Logger
}

func NewTokenTracker(lg *logger.Logger) *TokenTracker { return &TokenTracker{logger: lg} }

func (t *TokenTracker) Record(promptTokens, completionTokens int) {
	t.mu.Lock()
	t.LastRealPromptTokens = promptTokens
	t.CumulativePromptTokens += promptTokens
	t.CumulativeCompletionTokens += completionTokens
	cumulative := t.CumulativePromptTokens
	t.mu.Unlock()
	if t.logger != nil {
		t.logger.Debug("TOKENS", "real_usage", "prompt", promptTokens, "completion", completionTokens, "cumulative_prompt", cumulative)
	}
}

func (t *TokenTracker) GetLastRealPromptTokens() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.LastRealPromptTokens
}
