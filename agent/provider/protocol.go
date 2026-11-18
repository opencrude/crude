package provider

import (
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"sync"
)

// The provider package speaks several wire protocols (OpenAI Chat
// Completions, OpenAI Responses, Anthropic Messages, Ollama native, ChatGPT
// Codex). The architecture splits the concern three ways:
//
//   - Protocol implementations know the wire format: how to encode a request,
//     decode a response, and decode a stream. They never touch HTTP.
//   - Client owns transport: URL construction, headers, auth refresh,
//     pagination, retries, timeouts, response size limits. It never parses
//     protocol payloads beyond delegating to a Protocol.
//   - Provider is configuration: identity, credentials, base URL, dialer
//     tuning. It composes a Client from a Protocol plus its own auth state.
//
// Workflows (agent loop, model discovery) depend only on Client.

// ProtocolInfo is the static descriptor of a protocol. Endpoint paths are
// declared here as leaf paths relative to the API root, so URL construction
// is data rather than per-standard string concatenation.
type ProtocolInfo struct {
	// ID is the stable identifier persisted in provider configuration
	// (e.g. "openai", "anthropic").
	ID string
	// Name is the human label shown in the settings UI.
	Name string
	// Description explains when to pick this protocol, shown in the
	// settings UI.
	Description string
	// Root is the canonical API root segment of the protocol's reference
	// host (e.g. "/v1" for OpenAI, "/api" for Ollama). When a configured
	// base URL is a bare origin, the root is appended. A base URL that
	// already carries a path is kept as-is; proxies and gateways may
	// mount the API anywhere.
	Root string
	// ChatPath is the leaf path of the chat endpoint.
	ChatPath string
	// ModelsPath is the leaf path of the model listing endpoint. An empty
	// ModelsPath means the protocol has no listing endpoint; such
	// protocols also do not implement ModelLister.
	ModelsPath string
	// Streaming reports the wire format of the streaming response body,
	// which determines how the client frames it.
	Streaming StreamFraming
	// Options declares the protocol-level switches the settings UI may
	// expose. A false field means the protocol has no such knob.
	Options ProtocolOptions
}

// ProtocolOptions declares which protocol-level request switches exist for a
// protocol, so the settings UI can offer them without type-switching on the
// concrete implementation.
type ProtocolOptions struct {
	// Store: the backend can persist conversation context server-side
	// between requests (OpenAI "store").
	Store bool
	// StoreDefault is the wire default for Store when the user has not
	// chosen (Responses stores by default, Chat Completions does not).
	StoreDefault bool
	// MediaReplayRecovery: stored-response continuation can adapt when a
	// gateway rejects locally valid inline image data while reconstructing an
	// inherited prefix.
	MediaReplayRecovery bool
	// ZeroDataRetention: the protocol can enforce no-server-retention
	// per request (Responses store:false + encrypted reasoning
	// passthrough).
	ZeroDataRetention bool
	// ReasoningSummaries: the backend can return summarized reasoning
	// alongside the answer (OpenAI "reasoning.summary").
	ReasoningSummaries bool
	// Truncation: the protocol can auto-truncate context that exceeds
	// the model window instead of failing the request.
	Truncation bool
	// ServiceTiers lists the supported processing tiers, first entry is
	// the default. Empty when the protocol has no tier knob.
	ServiceTiers []string
}

// ProtocolSettings is the user's persisted choice for the protocol-level
// request switches. Nil pointers mean "inherit the protocol default"; a
// present pointer is an explicit on/off.
type ProtocolSettings struct {
	// Store enables server-side context caching: the backend keeps the
	// conversation so subsequent requests don't re-upload it. When nil the
	// protocol default applies.
	Store *bool
	// MediaReplayRecovery permits one exact, pre-output full-context recovery
	// when stored-response replay corrupts a locally valid inline image. Nil
	// uses the protocol default.
	MediaReplayRecovery *bool
	// ZeroDataRetention requests that the backend neither stores the
	// conversation nor retains it after the response (OpenAI Responses
	// "store:false" plus encrypted reasoning passthrough). It overrides
	// Store.
	ZeroDataRetention bool
	// ReasoningSummaries toggles summary reasoning output. When nil the
	// protocol default applies.
	ReasoningSummaries *bool
	// Truncation auto-truncates context overflowing the model window
	// instead of failing the request. When nil the protocol default
	// applies.
	Truncation *bool
	// ServiceTier selects the processing tier ("flex", "priority", ...).
	// Empty means the backend default.
	ServiceTier string
}

// StreamFraming describes how a streaming response body is delimited.
type StreamFraming int

const (
	// FramingSSE is text/event-stream: "data:" lines, [DONE] sentinel.
	FramingSSE StreamFraming = iota
	// FramingNDJSON is newline-delimited JSON, one JSON object per line.
	FramingNDJSON
)

// Protocol is the wire-format contract. Every protocol implements the core;
// richer backends additionally implement the optional capability interfaces
// below (ProtocolHeaders, ModelLister, ModelMeta, RequestExtras).
type Protocol interface {
	// Info returns the static protocol descriptor.
	Info() ProtocolInfo
	// EncodeRequest serializes a chat request into the wire format body.
	EncodeRequest(req *ChatRequest) ([]byte, error)
	// DecodeResponse parses a non-streaming chat response from the wire.
	DecodeResponse(body []byte) (*ChatResponse, error)
	// NewStreamDecoder returns a fresh request-scoped stream decoder.
	// Decoders may retain state needed to assemble signed thinking or
	// reasoning items.
	NewStreamDecoder() StreamDecoder
}

// ProtocolHeaders is implemented by protocols that require static request
// headers on every call (e.g. anthropic-version).
type ProtocolHeaders interface {
	Headers() http.Header
}

// ProtocolAuth is implemented by protocols that place credentials in
// specific headers (e.g. Anthropic's x-api-key). When not implemented,
// credentials are sent as a Bearer Authorization header.
type ProtocolAuth interface {
	// AuthHeaders returns the credential headers for the given API key.
	AuthHeaders(apiKey string) http.Header
}

// SessionScope is implemented by protocols whose backend requires
// per-conversation request headers derived from the session credentials
// (e.g. the ChatGPT codex backend's originator / account-id / session-id
// headers). The client hands the protocol a stable session ID scoped to
// the Client instance.
type SessionScope interface {
	// SessionHeaders returns the per-conversation headers for the given
	// auth context and session ID.
	SessionHeaders(auth AuthContext, sessionID string) http.Header
}

// AuthContext carries the resolved credentials for a request.
type AuthContext struct {
	// APIKey is the effective credential: the OAuth access token when
	// present, otherwise the static API key.
	APIKey string
	// OAuth is the stored credential set, or nil.
	OAuth *OAuthCredentials
}

// ModelLister is implemented by protocols whose backend can enumerate
// models. The protocol builds the request URL (including any required
// query parameters and the pagination cursor) and decodes one page; the
// client owns the pagination loop.
type ModelLister interface {
	// ModelsURL resolves the listing endpoint for a page. cursor is empty
	// for the first page and the previous page's Next cursor afterwards.
	ModelsURL(base *url.URL, cursor string) *url.URL
	// DecodeModelsPage parses one page of the listing.
	DecodeModelsPage(body []byte) (ModelPage, error)
}

// ModelPage is one page of a model listing.
type ModelPage struct {
	Models []ModelInfo
	// Next is the cursor to request the following page. Empty means the
	// listing is complete.
	Next string
}

// ProtocolMetadata is implemented by protocols that can derive model
// metadata without a network call.
type ProtocolMetadata interface {
	// ContextWindow returns the known context window for a model in
	// tokens, or 0 when unknown.
	ContextWindow(modelID string) int
}

// RequestExtras is implemented by protocols that inject protocol-specific
// fields into the chat request body (e.g. reasoning effort, thinking
// toggles, storage switches).
type RequestExtras interface {
	// RequestExtras returns fields to merge into the chat request body,
	// derived from the model ID, thinking effort, and the user's protocol
	// settings. Returns nil when nothing applies.
	RequestExtras(modelID, thinkingEffort string, settings ProtocolSettings) map[string]any
}

// ResolveStore reports the effective store choice: ZeroDataRetention forces
// storage off; an explicit Store setting wins; otherwise the protocol
// default (dflt) applies.
func (s ProtocolSettings) ResolveStore(dflt bool) bool {
	if s.ZeroDataRetention {
		return false
	}
	if s.Store != nil {
		return *s.Store
	}
	return dflt
}

// ResolveMediaReplayRecovery reports whether evidence-based inline-media
// continuation recovery is enabled.
func (s ProtocolSettings) ResolveMediaReplayRecovery(dflt bool) bool {
	if s.MediaReplayRecovery != nil {
		return *s.MediaReplayRecovery
	}
	return dflt
}

// ResolveReasoningSummaries reports the effective summary choice.
func (s ProtocolSettings) ResolveReasoningSummaries(dflt bool) bool {
	if s.ReasoningSummaries != nil {
		return *s.ReasoningSummaries
	}
	return dflt
}

// ModelInfo is a minimal model descriptor returned by a model listing.
type ModelInfo struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	ContextWindow int    `json:"context_window"`
}

// ErrModelsUnsupported is returned by Client.ListModels when the provider
// does not offer a model listing (protocol without ModelsPath, or the
// endpoint missing server-side). Callers should test with errors.Is.
var ErrModelsUnsupported = fmt.Errorf("model listing not supported")

// ErrUnauthorized is returned when the provider rejects the credentials.
// Callers should test with errors.Is and point the user at Settings.
var ErrUnauthorized = fmt.Errorf("authentication failed")

// --- registry ---

var (
	registryMu sync.RWMutex
	registry   = map[string]Protocol{}
)

// RegisterProtocol adds a protocol to the registry. Built-ins register in
// init(); out-of-package protocols (future plugins, forks) may register from
// their own init(). Registration replaces an existing entry with the same
// ID.
func RegisterProtocol(p Protocol) {
	if p == nil {
		panic("provider: RegisterProtocol(nil)")
	}
	info := p.Info()
	if info.ID == "" {
		panic("provider: protocol with empty ID")
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	registry[info.ID] = p
}

// ProtocolByID looks a protocol up in the registry.
func ProtocolByID(id string) (Protocol, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	p, ok := registry[id]
	return p, ok
}

// ProtocolList returns every registered protocol ordered by ID.
func ProtocolList() []Protocol {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]Protocol, 0, len(registry))
	for _, p := range registry {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Info().ID < out[j].Info().ID })
	return out
}

// canonicalProtocolID maps legacy aliases and casing to registry IDs.
func canonicalProtocolID(id string) (string, bool) {
	aliases := map[string]string{
		"openai-chat": "openai",
		"responses":   "openai-responses",
	}
	if p, ok := ProtocolByID(id); ok {
		return p.Info().ID, true
	}
	if canonical, ok := aliases[id]; ok {
		return canonical, true
	}
	return "", false
}

// isKnownProtocolID reports whether id names a registered protocol,
// directly or through an alias.
func isKnownProtocolID(id string) bool {
	_, ok := canonicalProtocolID(id)
	return ok
}
