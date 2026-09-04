package provider

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// UserInput represents a user's text input with optional prompt references.
type UserInput struct {
	InternalID   string
	Text         string
	ReferenceIDs []string
	// ActivePaths carries IDE context (active editor file, open tabs) so
	// the memory system can boost recall of memories scoped to those paths.
	ActivePaths []string
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
	// Media attachment fields (document/video/audio parts and inline image
	// bytes). Data is raw file bytes; encoders base64-encode per protocol.
	MimeType string `json:"mime_type,omitempty"`
	Filename string `json:"filename,omitempty"`
	Data     []byte `json:"data,omitempty"`
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

// HasMedia returns true if any part carries inline media bytes (inline
// image, document, video, or audio attachment).
func (c MessageContent) HasMedia() bool {
	for _, p := range c.Parts() {
		if len(p.Data) > 0 {
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
		case "document", "video", "audio":
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			name := p.Filename
			if name == "" {
				name = "attachment"
			}
			fmt.Fprintf(&b, "[%s: %s (%s, %d bytes)]", strings.ToUpper(p.Type[:1])+p.Type[1:], name, p.MimeType, len(p.Data))
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
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		*c = MessageContent("")
		return nil
	}
	if bytes.Equal(data, []byte("null")) {
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
	if data[0] == '[' {
		var parts []ContentPart
		if err := json.Unmarshal(data, &parts); err != nil {
			return err
		}
		*c = MessageContent(multipartSentinel + string(data))
		return nil
	}
	return fmt.Errorf("message content must be a string, array, or null")
}

type ChatMessage struct {
	InternalID        string            `json:"-"`
	DisplayContent    string            `json:"-"`
	ReferenceLabels   []string          `json:"-"`
	Role              string            `json:"role"`
	Content           MessageContent    `json:"content"`
	ReasoningContent  string            `json:"reasoning_content,omitempty"`
	Reasoning         string            `json:"reasoning,omitempty"`
	Refusal           string            `json:"refusal,omitempty"`
	ToolCalls         []ToolCall        `json:"tool_calls,omitempty"`
	ToolCallID        string            `json:"tool_call_id,omitempty"`
	CacheControl      *CacheControl     `json:"cache_control,omitempty"`
	ReasoningDetails  []json.RawMessage `json:"reasoning_details,omitempty"`
	ReasoningDuration time.Duration     `json:"-"`
	ProviderItems     []ProviderItem    `json:"-"`
	ToolError         bool              `json:"-"`
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

// ProviderItem preserves protocol-specific response items that must be sent
// back unchanged on a later request. Examples include OpenAI reasoning items
// and Anthropic signed thinking blocks.
type ProviderItem struct {
	Standard string          `json:"standard"`
	Type     string          `json:"type"`
	Data     json.RawMessage `json:"data"`
}

// TokenUsage is the protocol-neutral token accounting returned by a provider.
type TokenUsage struct {
	PromptTokens        int `json:"prompt_tokens"`
	CompletionTokens    int `json:"completion_tokens"`
	PromptTokensCached  int `json:"prompt_tokens_cached,omitempty"`
	PromptTokensWritten int `json:"prompt_tokens_written,omitempty"`
	// Native runtimes such as Ollama report timing but not cache-read token
	// counts. These diagnostics let live probes distinguish fast prefix reuse
	// from a full prompt evaluation without pretending they are billing data.
	TotalDuration      time.Duration `json:"-"`
	LoadDuration       time.Duration `json:"-"`
	PromptEvalDuration time.Duration `json:"-"`
	EvalDuration       time.Duration `json:"-"`
}

// ToolCallDelta is a protocol-neutral fragment of a streamed tool call.
// ReplaceArguments is used by standards whose final event contains the full
// argument string after earlier delta events.
type ToolCallDelta struct {
	Index            int
	ID               string
	Type             string
	Name             string
	Arguments        string
	ReplaceArguments bool
}

// StreamEvent is one decoded Server-Sent Event.
type StreamEvent struct {
	Event string
	ID    string
	Data  []byte
}

// StreamUpdate is the normalized output of one provider stream event.
type StreamUpdate struct {
	ContentDelta   string
	ReasoningDelta string
	ToolCalls      []ToolCallDelta
	FinishReason   string
	Usage          *TokenUsage
	ProviderItems  []ProviderItem
	Done           bool
}

// CompletionResponse is the normalized form of a non-streaming response.
type CompletionResponse struct {
	ID           string
	Model        string
	Message      ChatMessage
	FinishReason string
	Usage        TokenUsage
}

// APIError is a structured error received inside a successful HTTP response
// or an SSE stream.
type APIError struct {
	Type    string
	Code    string
	Message string
}

func (e *APIError) Error() string {
	if e == nil {
		return ""
	}
	switch {
	case e.Type != "" && e.Code != "":
		return fmt.Sprintf("%s (%s): %s", e.Type, e.Code, e.Message)
	case e.Type != "":
		return fmt.Sprintf("%s: %s", e.Type, e.Message)
	case e.Code != "":
		return fmt.Sprintf("%s: %s", e.Code, e.Message)
	default:
		return e.Message
	}
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
	MaxOutputTokens     int            `json:"-"`
	// Temperature and TopP are optional sampling overrides; nil means the
	// backend default applies. Each protocol maps them to its wire shape.
	Temperature *float64 `json:"-"`
	TopP        *float64 `json:"-"`
}

type ChatResponse struct {
	ID      string        `json:"id,omitempty"`
	Model   string        `json:"model,omitempty"`
	Choices []ChatChoice  `json:"choices"`
	Usage   *ChatUsage    `json:"usage,omitempty"`
	Error   *ChatAPIError `json:"error,omitempty"`
}

type ChatChoice struct {
	Message      ChatMessage `json:"message"`
	FinishReason string      `json:"finish_reason"`
	Delta        ChatDelta   `json:"delta"`
}

type ChatDelta struct {
	Content          string            `json:"content"`
	ReasoningContent string            `json:"reasoning_content"`
	Reasoning        string            `json:"reasoning"`
	Refusal          string            `json:"refusal"`
	ReasoningDetails []json.RawMessage `json:"reasoning_details,omitempty"`
	ToolCalls        []ToolCall        `json:"tool_calls,omitempty"`
}

type ChatUsage struct {
	PromptTokens        int                 `json:"prompt_tokens"`
	CompletionTokens    int                 `json:"completion_tokens"`
	TotalTokens         int                 `json:"total_tokens"`
	PromptTokensDetails *PromptTokenDetails `json:"prompt_tokens_details,omitempty"`
}

type PromptTokenDetails struct {
	CachedTokens     int `json:"cached_tokens"`
	CacheWriteTokens int `json:"cache_write_tokens,omitempty"`
}

type ChatAPIError struct {
	Message string `json:"message"`
	Type    string `json:"type,omitempty"`
	Code    any    `json:"code,omitempty"`
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
