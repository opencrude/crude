package provider

import "io"

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

	// EncodeRequest serializes a chat request into the wire format body.
	EncodeRequest(req *ChatRequest) ([]byte, error)

	// DecodeResponse parses a non-streaming chat response from the wire.
	DecodeResponse(body []byte) (*ChatResponse, error)

	// DecodeStreamChunk parses a single SSE data chunk from a streaming response.
	// Returns nil chunk when the stream is done (e.g. "[DONE]" sentinel).
	DecodeStreamChunk(data string) (*ChatResponse, error)

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
	Decode(data string) (*ChatResponse, error)
}

// writeCounter wraps an io.Writer and counts bytes written.
type writeCounter struct {
	w     io.Writer
	count int64
}

func (wc *writeCounter) Write(p []byte) (int, error) {
	n, err := wc.w.Write(p)
	wc.count += int64(n)
	return n, err
}
