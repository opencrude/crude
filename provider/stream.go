package provider

// StreamDecoder decodes protocol-agnostic stream events into normalized
// updates. Decoders are request-scoped and may retain state needed to
// assemble signed thinking or reasoning items.
type StreamDecoder interface {
	Decode(event StreamEvent) (StreamUpdate, error)
}

// streamUpdateToChatResponse adapts a normalized stream update to the
// ChatResponse shape used by non-streaming responses. Returns nil for an
// empty terminal update.
func streamUpdateToChatResponse(update StreamUpdate) *ChatResponse {
	if update.Done && isEmptyUpdate(update) && len(update.ProviderItems) == 0 {
		return nil
	}

	resp := &ChatResponse{}
	if update.ContentDelta != "" || update.ReasoningDelta != "" ||
		len(update.ToolCalls) > 0 || len(update.ProviderItems) > 0 || update.FinishReason != "" {
		choice := ChatChoice{
			FinishReason: update.FinishReason,
			Message: ChatMessage{
				Role:          "assistant",
				Content:       TextContent(""),
				ProviderItems: update.ProviderItems,
			},
		}
		choice.Delta.Content = update.ContentDelta
		choice.Delta.ReasoningContent = update.ReasoningDelta
		choice.Delta.Reasoning = update.ReasoningDelta
		for _, delta := range update.ToolCalls {
			choice.Delta.ToolCalls = append(choice.Delta.ToolCalls, ToolCall{
				Index: delta.Index,
				ID:    delta.ID,
				Type:  delta.Type,
				Function: FunctionCall{
					Name:      delta.Name,
					Arguments: delta.Arguments,
				},
			})
		}
		resp.Choices = []ChatChoice{choice}
	}
	if update.Usage != nil {
		resp.Usage = &ChatUsage{
			PromptTokens:     update.Usage.PromptTokens,
			CompletionTokens: update.Usage.CompletionTokens,
			TotalTokens:      update.Usage.PromptTokens + update.Usage.CompletionTokens,
		}
		if update.Usage.PromptTokensCached > 0 || update.Usage.PromptTokensWritten > 0 {
			resp.Usage.PromptTokensDetails = &PromptTokenDetails{
				CachedTokens:     update.Usage.PromptTokensCached,
				CacheWriteTokens: update.Usage.PromptTokensWritten,
			}
		}
	}
	return resp
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
