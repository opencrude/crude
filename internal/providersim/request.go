package providersim

import (
	"encoding/json"
	"net/http"
	"slices"
)

// Request is one HTTP request observed by the simulator. The journal and the
// responder receive independent snapshots.
type Request struct {
	// Index is the one-based position in the server's journal.
	Index int
	// Wire identifies the HTTP dialect.
	Wire Wire
	// Model is the requested model ID, when present.
	Model string
	// Prompt is the literal text before a FIM insertion.
	Prompt string
	// Suffix is the literal text after a FIM insertion.
	Suffix string
	// Method is the incoming HTTP method.
	Method string
	// Path is the decoded operation path relative to the wire mount.
	Path string
	// Target is the exact request URI, including the mount and query.
	Target string
	// Header is a copy of the request's HTTP header, including repeated values.
	Header http.Header
	// Raw is the exact request body received from the client.
	Raw []byte
	// Conversation is the input sent by the client.
	Conversation
	// Effective is the input seen by the model after wire-specific resolution.
	Effective Conversation
	// ToolChoice holds the wire's tool-choice value, including object forms.
	ToolChoice json.RawMessage
	// Reasoning holds the wire's reasoning or thinking setting as sent, for
	// example Responses reasoning, Messages thinking or Gemini thinkingConfig.
	Reasoning json.RawMessage
	// OutputLimit is the requested token limit. An empty value means absent.
	OutputLimit json.Number
	// MinOutputLimit is the optional minimum output token count on Codestral.
	MinOutputLimit json.Number
	// Stream reports whether the client requested streaming.
	Stream bool
	// Store is the client's setting, with nil distinguishing an omitted field.
	Store *bool
	// PreviousResponseID is the continuation ID sent by the client.
	PreviousResponseID string
	// CacheMarkers records explicit cache settings and content breakpoints.
	CacheMarkers []CacheMarker
	// Prefix describes the best shared prompt prefix with an earlier request.
	Prefix Prefix
	// Violation is the validation error for a rejected request, otherwise empty.
	Violation string
}

// Conversation is a provider-neutral model input.
type Conversation struct {
	// System contains system and developer content in wire order.
	System []Part
	// Messages contains the dialog, excluding separately projected system content.
	Messages []Message
	// Tools contains the tool declarations in wire order.
	Tools []Tool
}

// Message is one role-bearing model input message.
type Message struct {
	// Role is the role sent by the client.
	Role string
	// Name is the optional participant name.
	Name string
	// Parts is the ordered content of the message.
	Parts []Part
}

// PartKind identifies one type of normalized content.
type PartKind string

const (
	// PartText is plain text content.
	PartText PartKind = "text"
	// PartImage is an image reference or inline image.
	PartImage PartKind = "image"
	// PartToolCall asks the client to run a tool.
	PartToolCall PartKind = "tool_call"
	// PartToolResult carries a tool result.
	PartToolResult PartKind = "tool_result"
	// PartReasoning carries model reasoning or its replay payload.
	PartReasoning PartKind = "reasoning"
	// PartRefusal is model refusal text.
	PartRefusal PartKind = "refusal"
	// PartOpaque is a typed wire item with no neutral projection.
	PartOpaque PartKind = "opaque"
)

// Part is a normalized content block from a wire message.
type Part struct {
	// Kind is the normalized content type.
	Kind PartKind
	// Path is the JSON Pointer to the source content in the request.
	Path string
	// Role holds the source role for separately projected system content.
	Role string
	// Text holds the text of a text, result, refusal or reasoning part.
	Text string
	// ImageURL holds the URL of an image part, if supplied.
	ImageURL string
	// Call holds the tool call for a PartToolCall.
	Call ToolCall
	// CallID identifies the originating call for a PartToolResult.
	CallID string
	// Signature holds the opaque reasoning signature, when present.
	Signature string
	// Encrypted holds opaque reasoning ciphertext, when present.
	Encrypted string
	// Raw preserves wire data not fully represented by the other fields.
	Raw json.RawMessage
}

// Tool is a tool declaration in a request.
type Tool struct {
	// Name is the name given to the model.
	Name string
	// Description says what the tool does.
	Description string
	// Schema holds the input schema, when supplied.
	Schema json.RawMessage
	// Raw preserves other wire metadata, including custom tool formats.
	Raw json.RawMessage
}

// CacheMarker describes a wire cache instruction carried by the request.
type CacheMarker struct {
	// Path is the JSON Pointer to the cache instruction.
	Path string
	// Value is the cache instruction as JSON.
	Value json.RawMessage
}

func (r Request) snapshot() Request {
	r.Header = r.Header.Clone()
	r.Raw = slices.Clone(r.Raw)
	r.Conversation = r.Conversation.snapshot()
	r.Effective = r.Effective.snapshot()
	r.ToolChoice = slices.Clone(r.ToolChoice)
	r.Reasoning = slices.Clone(r.Reasoning)
	if r.Store != nil {
		value := *r.Store
		r.Store = &value
	}
	r.CacheMarkers = slices.Clone(r.CacheMarkers)
	for i := range r.CacheMarkers {
		r.CacheMarkers[i].Value = slices.Clone(r.CacheMarkers[i].Value)
	}
	return r
}

// Capture the exact field bytes (including null, escapes and object order).
// The request already passed the wire's validation before neutral projection.
// Sources: CreateChatCompletionRequest, CreateResponse and Codex Reasoning
// at the OpenAI and Codex revisions in schemas/manifest.json.
func (r *Request) captureReasoning() {
	var fields struct {
		Effort    json.RawMessage `json:"reasoning_effort"`
		Reasoning json.RawMessage `json:"reasoning"`
	}
	_ = json.Unmarshal(r.Raw, &fields)
	if r.Wire == Chat {
		r.Reasoning = fields.Effort
	} else {
		r.Reasoning = fields.Reasoning
	}
}

func (c Conversation) snapshot() Conversation {
	c.System = copyParts(c.System)
	c.Messages = slices.Clone(c.Messages)
	for i := range c.Messages {
		c.Messages[i].Parts = copyParts(c.Messages[i].Parts)
	}
	c.Tools = slices.Clone(c.Tools)
	for i := range c.Tools {
		c.Tools[i].Schema = slices.Clone(c.Tools[i].Schema)
		c.Tools[i].Raw = slices.Clone(c.Tools[i].Raw)
	}
	return c
}

func copyParts(parts []Part) []Part {
	parts = slices.Clone(parts)
	for i := range parts {
		parts[i].Raw = slices.Clone(parts[i].Raw)
	}
	return parts
}
