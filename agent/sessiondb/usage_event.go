package sessiondb

import (
	"crude/types/accounting"
	"time"
)

// UsageRecordFromEvent is the one accounting decoder for durable writes and
// live projections. It never invents a request ID or infers cost availability.
func UsageRecordFromEvent(data any, sessionID, agentID, parentAgentID, fallbackModel string) (UsageRecord, bool) {
	values, ok := data.(map[string]any)
	if !ok {
		return UsageRecord{}, false
	}
	requestID, _ := values["request_id"].(string)
	if requestID == "" {
		return UsageRecord{}, false
	}
	model, _ := values["model"].(string)
	if model == "" {
		model = fallbackModel
	}
	reported := usageInteger(values["reported_at"])
	if reported == 0 {
		reported = time.Now().UnixNano()
	}
	r := UsageRecord{
		RequestID: requestID, SessionID: sessionID, AgentID: agentID, ParentAgentID: parentAgentID,
		ModelID: model, TurnGeneration: uint64(max(0, usageInteger(values["turn_generation"]))),
		Attempt:      max(1, int(usageInteger(values["attempt"]))),
		PromptTokens: int(usageInteger(values["prompt"])), CompletionTokens: int(usageInteger(values["completion"])),
		CachedTokens: int(usageInteger(values["prompt_cached"])), CacheWriteTokens: int(usageInteger(values["prompt_written"])),
		ReasoningTokens: int(usageInteger(values["reasoning"])), ContextUsed: int(usageInteger(values["context_prompt"])),
		ContextWindow: int(usageInteger(values["context_window"])), CreatedAt: reported, UpdatedAt: reported,
	}
	r.RequestKind, _ = values["request_kind"].(string)
	if outcome, ok := values["request_outcome"].(string); ok && accounting.Outcome(outcome).Known() {
		r.Outcome = accounting.Outcome(outcome)
	}
	if r.RequestKind == "" {
		r.RequestKind = "normal"
	}
	if generation, ok := values["turn_generation"].(uint64); ok {
		r.TurnGeneration = generation
	}
	r.ProviderID, _ = values["provider"].(string)
	r.AccountID, _ = values["account_id"].(string)
	r.CostUSD, _ = values["cost"].(float64)
	r.CostKnown, _ = values["cost_known"].(bool)
	r.CostSource, _ = values["cost_source"].(string)
	r.CacheReadKnown, _ = values["cache_read_known"].(bool)
	r.CacheWriteKnown, _ = values["cache_write_known"].(bool)
	r.ReasoningKnown, _ = values["reasoning_known"].(bool)
	r.ReasoningInOutput, _ = values["reasoning_in_output"].(bool)
	r.Estimated, _ = values["estimated"].(bool)
	r.Partial, _ = values["partial_or_retry"].(bool)
	r.CostUpperBound, _ = values["cost_upper_bound"].(bool)
	return r, r.PromptTokens >= 0 && r.CompletionTokens >= 0 && r.CachedTokens >= 0 && r.CacheWriteTokens >= 0 && r.ReasoningTokens >= 0
}

func usageInteger(value any) int64 {
	switch n := value.(type) {
	case uint64:
		return int64(n)
	case int:
		return int64(n)
	case int64:
		return n
	case float64:
		return int64(n)
	default:
		return 0
	}
}
