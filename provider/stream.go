package provider

// StreamDecoder decodes protocol-agnostic stream events into normalized
// updates. Decoders are request-scoped and may retain state needed to
// assemble signed thinking or reasoning items.
type StreamDecoder interface {
	Decode(event StreamEvent) (StreamUpdate, error)
}

// UsageFromResponse extracts token usage from an OpenAI-standard ChatResponse.
func UsageFromResponse(resp *ChatResponse) (int, int, int) {
	if resp == nil || resp.Usage == nil {
		return 0, 0, 0
	}
	cached := 0
	if resp.Usage.PromptTokensDetails != nil {
		cached = resp.Usage.PromptTokensDetails.CachedTokens
	}
	return resp.Usage.PromptTokens, resp.Usage.CompletionTokens, cached
}
