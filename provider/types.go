package provider

import "encoding/json"

type ChatMessage struct {
	Role             string          `json:"role"`
	Content          string          `json:"content,omitempty"`
	ReasoningContent string          `json:"reasoning_content,omitempty"`
	Reasoning        string          `json:"reasoning,omitempty"`
	ToolCalls        []ToolCall      `json:"tool_calls,omitempty"`
	ToolCallID       string          `json:"tool_call_id,omitempty"`
	CacheControl     *CacheControl  `json:"cache_control,omitempty"`
}

type CacheControl struct {
	Type string `json:"type"`
}

type ToolCall struct {
	Index    int          `json:"index"`
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type StreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type ChatRequest struct {
	Model        string        `json:"model"`
	Messages     []ChatMessage `json:"messages"`
	Tools        []ToolDef     `json:"tools,omitempty"`
	ToolChoice   any           `json:"tool_choice,omitempty"`
	Stream       bool          `json:"stream"`
	StreamOptions *StreamOptions `json:"stream_options,omitempty"`
}

type ChatResponse struct {
	Choices []struct {
		Message      ChatMessage `json:"message"`
		FinishReason string      `json:"finish_reason"`
		Delta        struct {
			Content          string     `json:"content"`
			ReasoningContent string     `json:"reasoning_content"`
			Reasoning        string     `json:"reasoning"`
			ToolCalls        []ToolCall `json:"tool_calls,omitempty"`
		} `json:"delta"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
		PromptTokensDetails *struct {
			CachedTokens int `json:"cached_tokens"`
		} `json:"prompt_tokens_details,omitempty"`
	} `json:"usage,omitempty"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type ToolDef struct {
	Type        string       `json:"type"`
	Function    FunctionDef  `json:"function"`
	CacheControl *CacheControl `json:"cache_control,omitempty"`
}

type FunctionDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}
