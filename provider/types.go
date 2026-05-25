package provider

import (
	"encoding/json"
	"strings"
)

type ContentPart struct {
	Type     string       `json:"type"`
	Text     string       `json:"text,omitempty"`
	ImageURL *ImageURL    `json:"image_url,omitempty"`
}

type ImageURL struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"`
}

type MessageContent struct {
	parts []ContentPart
	text  string
	isText bool
}

func TextContent(s string) MessageContent {
	return MessageContent{text: s, isText: true}
}

func PartsContent(parts []ContentPart) MessageContent {
	return MessageContent{parts: parts, isText: false}
}

func (c MessageContent) IsText() bool {
	return c.isText || len(c.parts) == 0
}

func (c MessageContent) String() string {
	if c.isText {
		return c.text
	}
	var b strings.Builder
	for i, p := range c.parts {
		if p.Type == "text" {
			if i > 0 && b.Len() > 0 {
				b.WriteString("\n")
			}
			b.WriteString(p.Text)
		}
	}
	return b.String()
}

func (c MessageContent) Parts() []ContentPart {
	if c.isText {
		if c.text == "" {
			return nil
		}
		return []ContentPart{{Type: "text", Text: c.text}}
	}
	return c.parts
}

func (c MessageContent) HasImage() bool {
	for _, p := range c.parts {
		if p.Type == "image_url" && p.ImageURL != nil {
			return true
		}
	}
	return false
}

func (c *MessageContent) UnmarshalJSON(data []byte) error {
	if len(data) == 0 {
		c.isText = true
		c.text = ""
		return nil
	}
	if data[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		c.text = s
		c.isText = true
		return nil
	}
	var parts []ContentPart
	if err := json.Unmarshal(data, &parts); err != nil {
		return err
	}
	c.parts = parts
	c.isText = false
	return nil
}

func (c MessageContent) MarshalJSON() ([]byte, error) {
	if c.isText || len(c.parts) == 0 {
		return json.Marshal(c.text)
	}
	return json.Marshal(c.parts)
}

type ChatMessage struct {
	Role             string          `json:"role"`
	Content          MessageContent  `json:"content"`
	ReasoningContent string          `json:"reasoning_content,omitempty"`
	Reasoning        string          `json:"reasoning,omitempty"`
	ToolCalls        []ToolCall      `json:"tool_calls,omitempty"`
	ToolCallID       string          `json:"tool_call_id,omitempty"`
	CacheControl     *CacheControl  `json:"cache_control,omitempty"`
}

func (m ChatMessage) ContentString() string {
	return m.Content.String()
}

func NewTextMessage(role, text string) ChatMessage {
	return ChatMessage{Role: role, Content: TextContent(text)}
}

func NewMultiPartMessage(role string, parts []ContentPart) ChatMessage {
	return ChatMessage{Role: role, Content: PartsContent(parts)}
}

type CacheControl struct {
	Type string `json:"type"`
}

type ToolCall struct {
	Index     int          `json:"index"`
	ID        string       `json:"id"`
	Type      string       `json:"type"`
	Function  FunctionCall `json:"function"`
	InternalID string      `json:"-"`
}

type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type StreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type ChatRequest struct {
	Model            string        `json:"model"`
	Messages         []ChatMessage `json:"messages"`
	Tools            []ToolDef     `json:"tools,omitempty"`
	ToolChoice       any           `json:"tool_choice,omitempty"`
	Stream           bool          `json:"stream"`
	StreamOptions    *StreamOptions `json:"stream_options,omitempty"`
	MaxTokens        int           `json:"max_tokens,omitempty"`
	MaxCompletionTokens int        `json:"max_completion_tokens,omitempty"`
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
