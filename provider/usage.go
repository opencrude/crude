package provider

import "encoding/json"

func (d *PromptTokenDetails) UnmarshalJSON(data []byte) error {
	var wire struct {
		Cached  *int `json:"cached_tokens"`
		Written *int `json:"cache_write_tokens"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	*d = PromptTokenDetails{}
	if wire.Cached != nil {
		d.CachedTokens, d.CacheReadKnown = *wire.Cached, true
	}
	if wire.Written != nil {
		d.CacheWriteTokens, d.CacheWriteKnown = *wire.Written, true
	}
	return nil
}

func (d *CompletionTokenDetails) UnmarshalJSON(data []byte) error {
	var wire struct {
		Reasoning *int `json:"reasoning_tokens"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	*d = CompletionTokenDetails{}
	if wire.Reasoning != nil {
		d.ReasoningTokens, d.ReasoningKnown = *wire.Reasoning, true
	}
	return nil
}

// normalized is shared by streaming and non-streaming OpenAI-compatible paths.
// Presence is per field, not inferred from an enclosing details object.
func (u *ChatUsage) normalized() TokenUsage {
	if u == nil {
		return TokenUsage{}
	}
	usage := TokenUsage{PromptTokens: u.PromptTokens, CompletionTokens: u.CompletionTokens}
	if d := u.PromptTokensDetails; d != nil {
		usage.PromptTokensCached, usage.PromptTokensWritten = d.CachedTokens, d.CacheWriteTokens
		usage.CacheReadKnown = d.CacheReadKnown || d.CachedTokens > 0
		usage.CacheWriteKnown = d.CacheWriteKnown || d.CacheWriteTokens > 0
	}
	if !usage.CacheReadKnown && u.PromptCacheHitTokens != nil {
		usage.PromptTokensCached, usage.CacheReadKnown = *u.PromptCacheHitTokens, true
	}
	if d := u.CompletionTokensDetails; d != nil {
		usage.ReasoningTokens = d.ReasoningTokens
		usage.ReasoningKnown = d.ReasoningKnown || d.ReasoningTokens > 0
		usage.ReasoningInOutput = usage.ReasoningKnown
	}
	return usage
}
