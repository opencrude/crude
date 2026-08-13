package provider

// APIStandard defines the contract for an LLM API wire protocol.
// Each standard implements a specific API format (OpenAI Chat Completions,
// Anthropic Messages, Google Gemini, etc.). Providers are instances of
// a standard with a base URL, API key, and optional extras.
type APIStandard interface {
	// ID returns a unique identifier for this standard (e.g. "openai", "anthropic").
	ID() string

	// ChatEndpoint returns the full URL for chat completions.
	ChatEndpoint(baseURL string) string

	// ModelsEndpoint returns the URL for listing available models.
	// May be empty if the standard/provider doesn't support model listing.
	ModelsEndpoint(baseURL string) string

	// Auth returns (headerName, headerValue) for API authentication.
	Auth(apiKey string) (header, value string)

	// Headers returns every protocol-required request header. Auth is retained
	// for backwards compatibility, but callers making requests should use this
	// method because standards such as Anthropic require multiple headers.
	Headers(apiKey string) map[string]string

	// EncodeRequest serializes a chat request into the wire format body.
	EncodeRequest(req *ChatRequest) ([]byte, error)

	// DecodeResponse parses a non-streaming chat response from the wire.
	DecodeResponse(body []byte) (*ChatResponse, error)

	// DecodeStreamChunk parses a single SSE data chunk from a streaming response.
	// Returns nil chunk when the stream is done (e.g. "[DONE]" sentinel).
	// Deprecated: request-scoped stream processing should use NewStreamDecoder.
	DecodeStreamChunk(data string) (*ChatResponse, error)

	// NewStreamDecoder returns a fresh request-scoped stream decoder. Decoders
	// may retain state needed to assemble signed thinking or reasoning items.
	NewStreamDecoder() StreamDecoder

	// DecodeModels parses a model list response into ModelInfo entries.
	// Returns nil if the standard doesn't support model listing.
	DecodeModels(body []byte) ([]ModelInfo, error)

	// ExtraBody returns provider-specific fields to merge into the chat request body.
	// Called with the model ID and thinking effort level.
	// Returns nil if no extras are needed.
	ExtraBody(modelID, thinkingEffort string) map[string]any

	// ContextWindow returns the known context window for a model in tokens.
	// Returns 0 if unknown — caller should fall back to a hardcoded hint table.
	ContextWindow(modelID string) int

	// UsageFromResponse extracts token usage from a decoded ChatResponse.
	UsageFromResponse(resp *ChatResponse) (promptTokens, completionTokens, promptTokensCached int)
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

// Compile-time check helpers.
type StreamDecoder interface {
	Decode(event StreamEvent) (StreamUpdate, error)
}

func streamUpdateToChatResponse(update StreamUpdate) *ChatResponse {
	if update.Done && update.ContentDelta == "" && update.ReasoningDelta == "" &&
		len(update.ToolCalls) == 0 && len(update.ProviderItems) == 0 &&
		update.FinishReason == "" && update.Usage == nil {
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
