package provider

import (
	"encoding/json"
	"fmt"
	"strings"
)

// UserInput represents a user's text input with optional prompt references.
type UserInput struct {
	Text         string
	ReferenceIDs []string
}

func PlainInput(text string) UserInput {
	return UserInput{Text: text}
}

func InputWithRefs(text string, refIDs []string) UserInput {
	return UserInput{Text: text, ReferenceIDs: refIDs}
}

type ContentPart struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageURL *ImageURL `json:"image_url,omitempty"`
}

type ImageURL struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"`
}

// multipartSentinel is the prefix used to distinguish multipart content from
// plain text in the in-memory string representation. A null byte (\x00) can
// never appear in valid JSON text sent to LLMs, so it provides an unambiguous
// discriminant: text content is stored as-is, while multipart content is
// stored as multipartSentinel + rawJSON.
const multipartSentinel = "\x00multipart:"

// MessageContent represents message content that is either plain text or multipart.
// Text content is stored as-is; multipart content is stored with the sentinel
// prefix "\x00multipart:" followed by the JSON array of ContentPart objects.
// This allows `Content: "literal"` to work in struct literals for simple text,
// while PartsContent() handles multipart construction.
type MessageContent string

// TextContent creates a text-only MessageContent.
func TextContent(s string) MessageContent {
	return MessageContent(s)
}

// PartsContent creates a multipart MessageContent from content parts.
// The parts are marshaled to JSON and stored with the sentinel prefix so that
// IsText() can unambiguously distinguish multipart from plain text.
func PartsContent(parts []ContentPart) MessageContent {
	b, _ := json.Marshal(parts)
	return MessageContent(multipartSentinel + string(b))
}

// IsText returns true if this is plain text content (not multipart).
// Uses the sentinel prefix to distinguish — no reliance on '[' heuristic.
func (c MessageContent) IsText() bool {
	s := string(c)
	if len(s) == 0 {
		return true
	}
	return !strings.HasPrefix(s, multipartSentinel)
}

// String returns the text representation of the content.
// For multipart content, this extracts and joins text parts only,
// silently skipping image parts. For display purposes, use ContentDescription().
func (c MessageContent) String() string {
	s := string(c)
	if len(s) == 0 || !strings.HasPrefix(s, multipartSentinel) {
		return s
	}
	// Multipart: extract text parts
	raw := strings.TrimPrefix(s, multipartSentinel)
	var parts []ContentPart
	if err := json.Unmarshal([]byte(raw), &parts); err != nil {
		return s
	}
	var b strings.Builder
	for _, p := range parts {
		if p.Type == "text" && p.Text != "" {
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			b.WriteString(p.Text)
		}
	}
	return b.String()
}

// Parts returns the content as a slice of ContentPart.
// For text content, returns a single text part.
// For multipart content, parses the stored JSON.
func (c MessageContent) Parts() []ContentPart {
	s := string(c)
	if len(s) == 0 {
		return nil
	}
	if !strings.HasPrefix(s, multipartSentinel) {
		return []ContentPart{{Type: "text", Text: s}}
	}
	raw := strings.TrimPrefix(s, multipartSentinel)
	var parts []ContentPart
	if err := json.Unmarshal([]byte(raw), &parts); err != nil {
		return []ContentPart{{Type: "text", Text: s}}
	}
	return parts
}

// HasImage returns true if any part is an image_url type.
func (c MessageContent) HasImage() bool {
	for _, p := range c.Parts() {
		if p.Type == "image_url" && p.ImageURL != nil {
			return true
		}
	}
	return false
}

// ContentDescription returns a human-readable description of all content,
// including image markers for non-text parts. For text-only content, this
// is equivalent to String(). For multipart content, image parts are shown
// as [Image: mime/type] markers.
func (c MessageContent) ContentDescription() string {
	s := string(c)
	if len(s) == 0 {
		return ""
	}
	if !strings.HasPrefix(s, multipartSentinel) {
		return s
	}
	raw := strings.TrimPrefix(s, multipartSentinel)
	var parts []ContentPart
	if err := json.Unmarshal([]byte(raw), &parts); err != nil {
		return s
	}
	var b strings.Builder
	for _, p := range parts {
		switch p.Type {
		case "text":
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			b.WriteString(p.Text)
		case "image_url":
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			mime := "image"
			if p.ImageURL != nil && strings.HasPrefix(p.ImageURL.URL, "data:") {
				mime = extractMimeFromDataURL(p.ImageURL.URL)
			}
			fmt.Fprintf(&b, "[Image: %s]", mime)
		}
	}
	return b.String()
}

// MultipartJSON returns the raw JSON array string for multipart content
// (without the sentinel prefix), or empty string for text content.
// Used by DB serialization to store only the JSON array.
func (c MessageContent) MultipartJSON() string {
	s := string(c)
	if !strings.HasPrefix(s, multipartSentinel) {
		return ""
	}
	return strings.TrimPrefix(s, multipartSentinel)
}

func extractMimeFromDataURL(dataURL string) string {
	// data:image/png;base64,... → image/png
	if idx := strings.Index(dataURL, ":"); idx >= 0 {
		rest := dataURL[idx+1:]
		if end := strings.Index(rest, ";"); end > 0 {
			return rest[:end]
		}
		if end := strings.Index(rest, ","); end > 0 {
			return rest[:end]
		}
	}
	return "image"
}

// MarshalJSON implements json.Marshaler.
// Produces "text" for plain text, raw JSON array for multipart.
func (c MessageContent) MarshalJSON() ([]byte, error) {
	s := string(c)
	if len(s) == 0 {
		return json.Marshal("")
	}
	if !strings.HasPrefix(s, multipartSentinel) {
		// Plain text — marshal as JSON string
		return json.Marshal(s)
	}
	// Multipart — strip sentinel and emit raw JSON array
	return []byte(strings.TrimPrefix(s, multipartSentinel)), nil
}

// UnmarshalJSON implements json.Unmarshaler.
// Accepts both "string" and [{...}] formats.
// Array content is stored with the sentinel prefix for unambiguous detection.
func (c *MessageContent) UnmarshalJSON(data []byte) error {
	if len(data) == 0 {
		*c = MessageContent("")
		return nil
	}
	if data[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		*c = MessageContent(s)
		return nil
	}
	// JSON array — store with sentinel prefix for multipart detection
	*c = MessageContent(multipartSentinel + string(data))
	return nil
}

type ChatMessage struct {
	Role             string         `json:"role"`
	Content          MessageContent `json:"content"`
	ReasoningContent string         `json:"reasoning_content,omitempty"`
	Reasoning        string         `json:"reasoning,omitempty"`
	ToolCalls        []ToolCall     `json:"tool_calls,omitempty"`
	ToolCallID       string         `json:"tool_call_id,omitempty"`
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
	Index      int          `json:"index"`
	ID         string       `json:"id"`
	Type       string       `json:"type"`
	Function   FunctionCall `json:"function"`
	InternalID string       `json:"-"`
}

type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type StreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type ChatRequest struct {
	Model               string         `json:"model"`
	Messages            []ChatMessage  `json:"messages"`
	Tools               []ToolDef      `json:"tools,omitempty"`
	ToolChoice          any            `json:"tool_choice,omitempty"`
	Stream              bool           `json:"stream"`
	StreamOptions       *StreamOptions `json:"stream_options,omitempty"`
	MaxTokens           int            `json:"max_tokens,omitempty"`
	MaxCompletionTokens int            `json:"max_completion_tokens,omitempty"`
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
		PromptTokens        int `json:"prompt_tokens"`
		CompletionTokens    int `json:"completion_tokens"`
		TotalTokens         int `json:"total_tokens"`
		PromptTokensDetails *struct {
			CachedTokens int `json:"cached_tokens"`
		} `json:"prompt_tokens_details,omitempty"`
	} `json:"usage,omitempty"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type ToolDef struct {
	Type         string        `json:"type"`
	Function     FunctionDef   `json:"function"`
	CacheControl *CacheControl `json:"cache_control,omitempty"`
}

type FunctionDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}
