package provider

import (
	"bytes"
	"crude/types/command"
	"crude/types/control"
	"crude/types/reasoning"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// UserInput represents a user's text input with optional prompt references.
type UserInput struct {
	InternalID   string
	Text         string
	DisplayText  string
	ReferenceIDs []string
	Origin       MessageOrigin
	ContextParts []string
	// ActivePaths carries IDE context (active editor file, open tabs) so
	// the memory system can boost recall of memories scoped to those paths.
	ActivePaths   []string
	Collaboration *CollaborationAttribution
	// ControlTarget is trusted IDE admission context, not model-authored text.
	// The immutable submitting attachment does not follow later tab selections.
	ControlTarget *control.Target
}

type CollaborationAttribution struct {
	EventID          string           `json:"event_id"`
	ThreadID         string           `json:"thread_id"`
	SenderID         string           `json:"sender_id"`
	SenderName       string           `json:"sender_name"`
	RecipientIDs     []string         `json:"recipient_ids,omitempty"`
	RecipientNames   []string         `json:"recipient_names,omitempty"`
	Broadcast        bool             `json:"broadcast,omitempty"`
	Kind             string           `json:"kind"`
	RequestID        string           `json:"request_id,omitempty"`
	RequestRevision  uint64           `json:"request_revision,omitempty"`
	RunID            string           `json:"run_id,omitempty"`
	HistoryMessageID string           `json:"history_message_id,omitempty"`
	ArtifactID       string           `json:"artifact_id,omitempty"`
	ArtifactRevision int              `json:"artifact_revision,omitempty"`
	Command          *command.Outcome `json:"command,omitempty"`
}

func PlainInput(text string) UserInput {
	return UserInput{Text: text, Origin: OriginHuman}
}

func InputWithRefs(text string, refIDs []string) UserInput {
	return UserInput{Text: text, ReferenceIDs: refIDs, Origin: OriginHuman}
}

func RuntimeInput(origin MessageOrigin, text string) UserInput {
	return UserInput{Text: text, Origin: origin}
}

// ControlInputProvenance is immutable source evidence, never human intent.
// Its serialized ContextPart survives preparation, checkpoints and provider
// projection without coupling provider contracts to workspace/core owners.
type ControlInputProvenance struct {
	ControlRequestID string         `json:"control_request_id"`
	Caller           control.Caller `json:"caller"`
}

func InputFromControl(text string, refs []string, provenance ControlInputProvenance) UserInput {
	evidence, _ := json.Marshal(struct {
		ControlInput ControlInputProvenance `json:"control_input"`
	}{provenance})
	return UserInput{Text: text, ReferenceIDs: append([]string(nil), refs...), Origin: OriginRuntimeContext, ContextParts: []string{string(evidence)}}
}

func (i UserInput) EffectiveOrigin() MessageOrigin {
	if i.Origin == "" {
		return OriginHuman
	}
	return i.Origin
}

type MessageOrigin string

const (
	OriginHuman             MessageOrigin = "human"
	OriginHumanSteering     MessageOrigin = "human_steering"
	OriginAssistant         MessageOrigin = "assistant"
	OriginTool              MessageOrigin = "tool"
	OriginRuntimeContext    MessageOrigin = "runtime_context"
	OriginTurnStopped       MessageOrigin = "turn_stopped"
	OriginContinuation      MessageOrigin = "continuation"
	OriginPeer              MessageOrigin = "peer"
	OriginAssignment        MessageOrigin = "assignment"
	OriginCheckpoint        MessageOrigin = "checkpoint"
	OriginCompactionRequest MessageOrigin = "compaction_request"
)

type MessageVisibility string

const (
	VisibilityConversation MessageVisibility = "conversation"
	VisibilityLifecycle    MessageVisibility = "lifecycle"
	VisibilityContextOnly  MessageVisibility = "context_only"
)

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
	InternalID        string                    `json:"internal_id,omitempty"`
	DisplayContent    string                    `json:"display_content,omitempty"`
	ReferenceLabels   []string                  `json:"reference_labels,omitempty"`
	Role              string                    `json:"role"`
	Origin            MessageOrigin             `json:"origin,omitempty"`
	Visibility        MessageVisibility         `json:"visibility,omitempty"`
	Content           MessageContent            `json:"content"`
	ReasoningContent  string                    `json:"reasoning_content,omitempty"`
	Reasoning         string                    `json:"reasoning,omitempty"`
	Refusal           string                    `json:"refusal,omitempty"`
	ToolCalls         []ToolCall                `json:"tool_calls,omitempty"`
	ToolCallID        string                    `json:"tool_call_id,omitempty"`
	CacheControl      *CacheControl             `json:"cache_control,omitempty"`
	ReasoningDetails  []json.RawMessage         `json:"reasoning_details,omitempty"`
	ReasoningDuration time.Duration             `json:"reasoning_duration,omitempty"`
	ReasoningDisplay  reasoning.Display         `json:"reasoning_display,omitempty"`
	ProviderItems     []ProviderItem            `json:"provider_items,omitempty"`
	ReplayScope       string                    `json:"replay_scope,omitempty"`
	ReplaySourceModel string                    `json:"replay_source_model,omitempty"`
	ReplayPrefixHash  string                    `json:"replay_prefix_hash,omitempty"`
	ResponseReference *ResponseReference        `json:"response_reference,omitempty"`
	ToolError         bool                      `json:"tool_error,omitempty"`
	Collaboration     *CollaborationAttribution `json:"collaboration,omitempty"`
}

func (m ChatMessage) ContentString() string {
	return m.Content.String()
}

func NewTextMessage(role, text string) ChatMessage {
	return ChatMessage{
		Role: role, Content: TextContent(text),
		Origin: defaultMessageOrigin(role), Visibility: VisibilityConversation,
	}
}

func NewMultiPartMessage(role string, parts []ContentPart) ChatMessage {
	return ChatMessage{
		Role: role, Content: PartsContent(parts),
		Origin: defaultMessageOrigin(role), Visibility: VisibilityConversation,
	}
}

func (m ChatMessage) EffectiveOrigin() MessageOrigin {
	if m.Origin != "" {
		return m.Origin
	}
	return defaultMessageOrigin(m.Role)
}

func (m ChatMessage) EffectiveVisibility() MessageVisibility {
	if m.Visibility != "" {
		return m.Visibility
	}
	return VisibilityConversation
}

func (m ChatMessage) IsGenuineUserInput() bool {
	return m.EffectiveOrigin() == OriginHuman || m.EffectiveOrigin() == OriginHumanSteering
}

func defaultMessageOrigin(role string) MessageOrigin {
	switch role {
	case "assistant":
		return OriginAssistant
	case "tool":
		return OriginTool
	case "user":
		return OriginHuman
	default:
		return MessageOrigin(role)
	}
}

type CacheControl struct {
	Type string `json:"type"`
}

type ToolCall struct {
	Index      int          `json:"index"`
	ID         string       `json:"id"`
	Type       string       `json:"type"`
	Function   FunctionCall `json:"function"`
	InternalID string       `json:"internal_id,omitempty"`
}

// UnmarshalJSON normalizes Chat Completions custom calls into the same raw
// input representation used by Responses. Marshaling retains canonical state.
func (t *ToolCall) UnmarshalJSON(data []byte) error {
	type plain ToolCall
	var wire struct {
		plain
		Custom *struct {
			Name  string `json:"name"`
			Input string `json:"input"`
		} `json:"custom"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	*t = ToolCall(wire.plain)
	t.Function.Name = canonicalToolName(t.Function.Name)
	if wire.Custom != nil {
		t.Type = "custom"
		t.Function = FunctionCall{Name: wire.Custom.Name, Arguments: wire.Custom.Input}
	}
	return nil
}

type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// The synthetic parallel tool uses a legal flat function name on every wire.
// Its dotted execution identity is internal, not an OpenAI namespace or an
// Anthropic function name. Other tool names remain untouched.
func wireFunctionName(name string) string {
	if name == "multi_tool_use.parallel" {
		return "multi_tool_use_parallel"
	}
	return name
}

func canonicalToolName(name string) string {
	if name == "multi_tool_use_parallel" {
		return "multi_tool_use.parallel"
	}
	return name
}

// ProviderItem preserves protocol-specific response items that must be sent
// back unchanged on a later request. Examples include OpenAI reasoning items
// and Anthropic signed thinking blocks.
type ProviderItem struct {
	Standard    string          `json:"standard"`
	Type        string          `json:"type"`
	Data        json.RawMessage `json:"data"`
	ReplayScope string          `json:"replay_scope,omitempty"`
	SourceModel string          `json:"source_model,omitempty"`
	PrefixHash  string          `json:"prefix_hash,omitempty"`
}

// TokenUsage is the protocol-neutral token accounting returned by a provider.
type TokenUsage struct {
	PromptTokens        int  `json:"prompt_tokens"`
	CompletionTokens    int  `json:"completion_tokens"`
	PromptTokensCached  int  `json:"prompt_tokens_cached,omitempty"`
	PromptTokensWritten int  `json:"prompt_tokens_written,omitempty"`
	CacheReadKnown      bool `json:"cache_read_known,omitempty"`
	CacheWriteKnown     bool `json:"cache_write_known,omitempty"`
	ReasoningTokens     int  `json:"reasoning_tokens,omitempty"`
	ReasoningKnown      bool `json:"reasoning_known,omitempty"`
	ReasoningInOutput   bool `json:"reasoning_in_output,omitempty"`
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
	ContentDelta       string
	Reasoning          []reasoning.Delta
	ToolCalls          []ToolCallDelta
	FinishReason       string
	Usage              *TokenUsage
	ProviderItems      []ProviderItem
	ContinuationID     string
	ContinuationReason string
	Done               bool
}

func (u StreamUpdate) ReasoningText() string {
	var text strings.Builder
	for _, delta := range u.Reasoning {
		if delta.Kind != reasoning.Encrypted {
			text.WriteString(delta.Text)
		}
	}
	return text.String()
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
	Param   string
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
	Model                     string         `json:"model"`
	Messages                  []ChatMessage  `json:"messages"`
	Tools                     []ToolDef      `json:"tools,omitempty"`
	ToolChoice                any            `json:"tool_choice,omitempty"`
	Stream                    bool           `json:"stream"`
	StreamOptions             *StreamOptions `json:"stream_options,omitempty"`
	MaxTokens                 int            `json:"max_tokens,omitempty"`
	MaxCompletionTokens       int            `json:"max_completion_tokens,omitempty"`
	MaxOutputTokens           int            `json:"-"`
	ResponseContinuationScope string         `json:"-"`
	// MediaReplayRecovery enables evidence-based recovery and learned
	// full-upload routing for stored inline-image replay failures.
	MediaReplayRecovery bool `json:"-"`
	// Temperature and TopP are optional sampling overrides; nil means the
	// backend default applies. Each protocol maps them to its wire shape.
	Temperature *float64 `json:"-"`
	TopP        *float64 `json:"-"`
	// ReasoningContentKind applies only to unlabelled compatibility fields.
	// The caller supplies an observation about this serving connection, never
	// a model-name guess; explicit wire kinds always take precedence.
	ReasoningContentKind reasoning.Kind `json:"-"`
}

type ChatResponse struct {
	ContinuationID     string        `json:"-"`
	ContinuationReason string        `json:"-"`
	ID                 string        `json:"id,omitempty"`
	Model              string        `json:"model,omitempty"`
	Choices            []ChatChoice  `json:"choices"`
	Usage              *ChatUsage    `json:"usage,omitempty"`
	Error              *ChatAPIError `json:"error,omitempty"`
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
	PromptTokens            int                     `json:"prompt_tokens"`
	CompletionTokens        int                     `json:"completion_tokens"`
	TotalTokens             int                     `json:"total_tokens"`
	PromptTokensDetails     *PromptTokenDetails     `json:"prompt_tokens_details,omitempty"`
	CompletionTokensDetails *CompletionTokenDetails `json:"completion_tokens_details,omitempty"`
	PromptCacheHitTokens    *int                    `json:"prompt_cache_hit_tokens,omitempty"`
}

type PromptTokenDetails struct {
	CachedTokens     int  `json:"cached_tokens"`
	CacheWriteTokens int  `json:"cache_write_tokens,omitempty"`
	CacheReadKnown   bool `json:"-"`
	CacheWriteKnown  bool `json:"-"`
}

type CompletionTokenDetails struct {
	ReasoningTokens int  `json:"reasoning_tokens"`
	ReasoningKnown  bool `json:"-"`
}

type ChatAPIError struct {
	Message string `json:"message"`
	Type    string `json:"type,omitempty"`
	Code    any    `json:"code,omitempty"`
	Param   string `json:"param,omitempty"`
}

type ToolDef struct {
	Type         string        `json:"type"`
	Function     FunctionDef   `json:"function"`
	Format       *ToolFormat   `json:"format,omitempty"`
	CacheControl *CacheControl `json:"cache_control,omitempty"`
}

// ToolFormat is the native custom-tool grammar contract.
type ToolFormat struct {
	Type       string `json:"type"`
	Syntax     string `json:"syntax"`
	Definition string `json:"definition"`
}

type FunctionDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}
