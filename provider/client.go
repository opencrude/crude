package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"crude/logger"
	"crude/types/reasoning"
)

// Client performs API calls against one provider endpoint using one wire
// protocol. It owns everything transport-shaped so workflows never build
// URLs, headers, or retry loops themselves:
//
//   - URL resolution from the configured base URL and the protocol's
//     declared paths (see resolveAPIBase for the base-URL convention)
//   - header composition: static protocol headers, auth (with OAuth token
//     refresh), per-session headers, and provider extras
//   - request encoding, including protocol extras merged into the body
//   - a retry loop with exponential backoff, jitter, and server-hinted
//     delays (Retry-After / retry-after-ms)
//   - response framing: SSE, newline-delimited JSON, or a single JSON
//     document
//   - bounded reads and typed errors (StatusError, ErrModelsUnsupported,
//     ErrUnauthorized)
//
// A Client is cheap to build per turn; it holds no conversation state beyond
// a stable session ID for protocols that require one.
type Client struct {
	proto Protocol
	base  *url.URL
	hc    *http.Client
	dial  DialConfig

	// headerSource resolves the full request header set for an attempt,
	// including credential refresh.
	headerSource func(ctx context.Context) (http.Header, error)

	// requestExtras are merged into every chat request body.
	requestExtras map[string]any

	// mediaReplayRecovery permits evidence-based full-context recovery when a
	// gateway proves it cannot reconstruct valid inline images from a stored
	// Responses prefix.
	mediaReplayRecovery bool

	// sessionID is a stable identifier for protocols that require
	// per-conversation headers (see SessionScope).
	sessionID string

	onRetry           func(attempt int, delay time.Duration, err error)
	onUploadProgress  func(uploaded, total int64)
	onRequestEncoded  func(body []byte)
	onRequestAccepted func()
	stall             progressTimeouts
}

// ClientOption configures a Client at construction.
type ClientOption func(*Client)

// WithHeaderSource replaces the default header resolution. The source is
// invoked once per attempt and must return the complete header set.
func WithHeaderSource(source func(ctx context.Context) (http.Header, error)) ClientOption {
	return func(c *Client) { c.headerSource = source }
}

// WithHTTPClient overrides the underlying *http.Client (test injection).
func WithHTTPClient(hc *http.Client) ClientOption {
	return func(c *Client) { c.hc = hc }
}

// WithResponseLimit bounds buffered response and error bodies for small,
// specialized requests. It never increases the provider's existing limit.
func WithResponseLimit(bytes int64) ClientOption {
	return func(c *Client) {
		if bytes > 0 && bytes < c.dial.MaxResponseBytes {
			c.dial.MaxResponseBytes = bytes
		}
	}
}

// WithRequestExtras sets fields merged into every chat request body. Keys
// overwrite what the protocol encoded (shallow merge).
func WithRequestExtras(extra map[string]any) ClientOption {
	return func(c *Client) { c.requestExtras = extra }
}

// WithMediaReplayRecovery controls adaptive stored inline-image recovery.
func WithMediaReplayRecovery(enabled bool) ClientOption {
	return func(c *Client) { c.mediaReplayRecovery = enabled }
}

// OnRequestEncoded registers a callback receiving the fully encoded request
// body (after extras merge) for each chat request — used for turn logging.
func OnRequestEncoded(fn func(body []byte)) ClientOption {
	return func(c *Client) { c.onRequestEncoded = fn }
}

// OnRequestAccepted registers a callback fired after the chat endpoint has
// accepted a request with a successful HTTP response. It is later than body
// encoding/upload and earlier than the first decoded model update.
func OnRequestAccepted(fn func()) ClientOption {
	return func(c *Client) { c.onRequestAccepted = fn }
}

// OnRetry registers a callback fired before each retry wait.
func OnRetry(fn func(attempt int, delay time.Duration, err error)) ClientOption {
	return func(c *Client) { c.onRetry = fn }
}

// WithMaxAttempts overrides the resolved retry-attempt count for one client.
// It is used by agent-level policy and tests without mutating the shared
// Provider configuration.
func WithMaxAttempts(attempts int) ClientOption {
	return func(c *Client) {
		if attempts > 0 {
			c.dial.MaxAttempts = attempts
		}
	}
}

// OnUploadProgress registers a callback for upload progress during request
// streaming. It is called with percentage-deduplicated (uploaded, total)
// byte counts.
func OnUploadProgress(fn func(uploaded, total int64)) ClientOption {
	return func(c *Client) { c.onUploadProgress = fn }
}

// WithProgressTimeouts overrides zero-progress detection. It is primarily a
// fault-injection/test seam; production uses conservative adaptive defaults.
func WithProgressTimeouts(startup, floor, ceiling time.Duration) ClientOption {
	return func(c *Client) {
		c.stall = progressTimeouts{Startup: startup, Floor: floor, Ceiling: ceiling, Margin: floor / 4}
	}
}

// NewClient builds a Client for a protocol at a base URL. headers may be nil
// to use only the protocol's static headers plus a bearer token from apiKey.
func NewClient(proto Protocol, baseURL, apiKey string, dial DialConfig, opts ...ClientOption) (*Client, error) {
	if proto == nil {
		return nil, fmt.Errorf("provider: protocol is nil")
	}
	info := proto.Info()
	base, err := resolveAPIBase(baseURL, info.Root)
	if err != nil {
		return nil, fmt.Errorf("provider %s: invalid base URL: %w", info.ID, err)
	}
	if info.ChatPath == "" {
		return nil, fmt.Errorf("provider %s: protocol declares no chat path", info.ID)
	}
	if dial.MaxAttempts < 1 {
		dial.MaxAttempts = 1
	}
	stall := dial.progressTimeouts()
	if err := stall.validate(); err != nil {
		return nil, fmt.Errorf("provider: %w", err)
	}
	c := &Client{
		proto:               proto,
		base:                base,
		hc:                  newHTTPClient(dial),
		dial:                dial,
		sessionID:           newSessionUUID(),
		stall:               stall,
		mediaReplayRecovery: info.Options.MediaReplayRecovery,
	}
	if apiKey != "" {
		c.headerSource = staticHeaderSource(proto, apiKey)
	} else {
		c.headerSource = func(context.Context) (http.Header, error) {
			return protocolHeaders(proto), nil
		}
	}
	for _, opt := range opts {
		opt(c)
	}
	return c, nil
}

// Protocol returns the wire protocol this client speaks.
func (c *Client) Protocol() Protocol { return c.proto }

// resolveAPIBase normalizes a configured base URL against the protocol's
// canonical API root. The convention: a base URL that is a bare origin
// (no path) receives the protocol's Root segment; a base URL that already
// carries a path is kept as-is, because proxies and gateways mount APIs at
// arbitrary prefixes. Trailing slashes are trimmed.
func resolveAPIBase(raw, root string) (*url.URL, error) {
	trimmed := strings.TrimRight(strings.TrimSpace(raw), "/")
	if trimmed == "" {
		return nil, fmt.Errorf("base URL is empty")
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return nil, err
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("base URL %q has no scheme or host", raw)
	}
	if (parsed.Path == "" || parsed.Path == "/") && root != "" {
		parsed.Path = root
	}
	return parsed, nil
}

// joinPath appends a leaf path to a resolved base URL.
func joinPath(base *url.URL, leaf string) *url.URL {
	joined := *base
	joined.Path = strings.TrimRight(joined.Path, "/") + "/" + strings.TrimLeft(leaf, "/")
	return &joined
}

// protocolHeaders returns the static headers a protocol requires.
func protocolHeaders(proto Protocol) http.Header {
	headers := http.Header{}
	if ph, ok := proto.(ProtocolHeaders); ok {
		for key, values := range ph.Headers() {
			for _, value := range values {
				headers.Set(key, value)
			}
		}
	}
	return headers
}

// staticHeaderSource builds a header source from a static API key, applying
// the protocol's auth header convention (Bearer by default).
func staticHeaderSource(proto Protocol, apiKey string) func(context.Context) (http.Header, error) {
	return func(context.Context) (http.Header, error) {
		headers := protocolHeaders(proto)
		if apiKey != "" {
			if pa, ok := proto.(ProtocolAuth); ok {
				for key, values := range pa.AuthHeaders(apiKey) {
					for _, value := range values {
						headers.Set(key, value)
					}
				}
			} else {
				headers.Set("Authorization", "Bearer "+apiKey)
			}
		}
		return headers, nil
	}
}

// newHTTPClient builds the transport used for all API traffic. No overall
// client timeout is set: streaming turns may legitimately run for minutes,
// so deadlines are enforced per request via context instead.
func newHTTPClient(dial DialConfig) *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			DialContext:         dialContext(dial.ConnectTimeout),
			MaxIdleConns:        10,
			MaxIdleConnsPerHost: 10,
			IdleConnTimeout:     90 * time.Second,
			TLSHandshakeTimeout: dial.ConnectTimeout,
			// The per-attempt progress watchdog owns header waiting. A TCP
			// connect timeout must not also cut off a queued gateway response.
			ResponseHeaderTimeout: 0,
		},
	}
}

// --- chat ---

// Complete performs a non-streaming chat request.
func (c *Client) Complete(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	req = c.withContinuationPolicy(withStreamFlag(req, false))
	canonicalRequest := req
	body, err := c.encodeChatRequest(req)
	if err != nil {
		return nil, err
	}
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()

	var lastErr error
	for attempt := 1; attempt <= c.dial.MaxAttempts; attempt++ {
		resp, err := c.do(ctx, http.MethodPost, c.chatURL(), body)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, ctxErr
			}
			if !Retryable(err) {
				return nil, err
			}
			lastErr = err
			if attempt == c.dial.MaxAttempts {
				break
			}
			if waitErr := c.backoff(ctx, attempt, nil, err); waitErr != nil {
				return nil, waitErr
			}
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			statusErr := readStatusError(resp, c.dial.MaxResponseBytes)
			full, replacement, recovered, recoveryErr := c.rebaseContinuation(req, statusErr)
			if recoveryErr != nil {
				return nil, recoveryErr
			}
			if recovered {
				req, body = full, replacement
				attempt--
				continue
			}
			lastErr = statusErr
			if !statusErr.Retryable() {
				return nil, statusErr
			}
			if attempt == c.dial.MaxAttempts {
				return nil, wrapExhausted(statusErr, c.dial.MaxAttempts)
			}
			if waitErr := c.backoff(ctx, attempt, resp, statusErr); waitErr != nil {
				return nil, waitErr
			}
			continue
		}
		// Body reads have a zero-progress monitor and may fail retryably even
		// after headers were accepted.
		payload, err := readBounded(resp.Body, c.dial.MaxResponseBytes)
		_ = resp.Body.Close()
		if err != nil {
			lastErr = fmt.Errorf("read response: %w", err)
			if attempt < c.dial.MaxAttempts && Retryable(err) {
				if waitErr := c.backoff(ctx, attempt, nil, err); waitErr != nil {
					return nil, waitErr
				}
				continue
			}
			return nil, lastErr
		}
		decoded, err := c.proto.DecodeResponse(payload)
		if err != nil {
			if Retryable(err) && attempt < c.dial.MaxAttempts {
				if waitErr := c.backoff(ctx, attempt, nil, err); waitErr != nil {
					return nil, waitErr
				}
				continue
			}
			return nil, err
		}
		if decoded.ContinuationID != "" && canonicalRequest.ResponseContinuationScope != "" && len(decoded.Choices) > 0 {
			BindResponseReference(&decoded.Choices[0].Message, &ResponseReference{ID: decoded.ContinuationID,
				Scope: canonicalRequest.ResponseContinuationScope, InputHash: ResponseInputHash(canonicalRequest.Messages, canonicalRequest.Tools)})
		}
		return decoded, nil
	}
	return nil, wrapExhausted(lastErr, c.dial.MaxAttempts)
}

// Stream performs a streaming chat request. Retry semantics: attempts are
// made transparently until model output is accepted. Accounting-only updates
// do not commit a stochastic response; content, reasoning, tool calls and opaque
// provider items do. Next returns io.EOF after the terminal update.
func (c *Client) Stream(ctx context.Context, req *ChatRequest) (*ChatStream, error) {
	req = c.withContinuationPolicy(withStreamFlag(req, true))
	body, err := c.encodeChatRequest(req)
	if err != nil {
		return nil, err
	}
	ctx, cancel := c.withTimeout(ctx)
	return &ChatStream{
		client:               c,
		ctx:                  ctx,
		cancel:               cancel,
		body:                 body,
		request:              req,
		reasoningContentKind: req.ReasoningContentKind,
	}, nil
}

// ChatStream is a decoded, ordered view of one streaming response.
type ChatStream struct {
	request              *ChatRequest
	client               *Client
	reasoningContentKind reasoning.Kind
	ctx                  context.Context
	cancel               context.CancelFunc
	body                 []byte
	resp                 *http.Response
	reader               streamEventReader
	decoder              StreamDecoder
	attempt              int
	yielded              bool
	sawDone              bool
	closed               bool
}

// Next returns the next decoded stream update. It returns io.EOF after the
// terminal update. Errors before model output has been accepted are retried
// transparently when retryable; errors after output has been seen
// are surfaced as-is so callers can preserve the partial result.
func (s *ChatStream) Next() (StreamUpdate, error) {
	if s.closed {
		return StreamUpdate{}, io.EOF
	}
	defer func() {
		if s.closed {
			s.cancel()
		}
	}()
	for {
		if s.resp == nil {
			if err := s.establish(); err != nil {
				s.closed = true
				return StreamUpdate{}, err
			}
		}
		event, err := s.reader.next()
		if err != nil {
			s.closeResp()
			if errors.Is(err, io.EOF) {
				if !s.sawDone {
					// The connection closed before a terminal event. If
					// nothing was yielded, this is retryable transport
					// trouble; otherwise the partial result stands.
					err = fmt.Errorf("%w: provider stream ended without terminal event", io.ErrUnexpectedEOF)
				} else {
					s.done()
					return StreamUpdate{}, io.EOF
				}
			}
			if s.canRetryStream(err) {
				if waitErr := s.client.backoff(s.ctx, s.attempt, nil, err); waitErr != nil {
					s.closed = true
					return StreamUpdate{}, waitErr
				}
				continue
			}
			s.closed = true
			return StreamUpdate{}, err
		}
		update, err := s.decoder.Decode(event)
		if err != nil {
			s.closeResp()
			if !s.yielded && s.ctx.Err() == nil {
				full, body, recovered, recoveryErr := s.client.rebaseContinuation(s.request, err)
				if recoveryErr != nil {
					s.closed = true
					return StreamUpdate{}, recoveryErr
				}
				if recovered {
					s.request, s.body = full, body
					s.attempt--
					continue
				}
			}
			if s.canRetryStream(err) {
				if waitErr := s.client.backoff(s.ctx, s.attempt, nil, err); waitErr != nil {
					s.closed = true
					return StreamUpdate{}, waitErr
				}
				continue
			}
			s.closed = true
			return StreamUpdate{}, err
		}
		annotateFlattenedReasoning(&update, s.reasoningContentKind)
		if update.Done {
			s.sawDone = true
		}
		if isEmptyUpdate(update) && !update.Done {
			continue // framing-only event (comments, keep-alives)
		}
		if isEmptyUpdate(update) && update.Done {
			s.done()
			return StreamUpdate{}, io.EOF
		}
		// Accounting/framing alone does not commit model output. Once content,
		// reasoning, tools or opaque state is accepted, replay is never safe.
		s.yielded = s.yielded || update.ContentDelta != "" || update.ReasoningText() != "" || len(update.ToolCalls) > 0 || len(update.ProviderItems) > 0
		if update.Done {
			s.done()
		}
		return update, nil
	}
}

func annotateFlattenedReasoning(update *StreamUpdate, kind reasoning.Kind) {
	if update == nil {
		return
	}
	for i := range update.Reasoning {
		if update.Reasoning[i].Kind == reasoning.Unknown {
			if kind == reasoning.InferredSummary || kind == reasoning.InferredRaw {
				update.Reasoning[i].Kind = kind
			} else {
				update.Reasoning[i].Kind = reasoning.Provisional
			}
		}
	}
}

func (s *ChatStream) canRetryStream(err error) bool {
	if s.attempt >= s.client.dial.MaxAttempts || !Retryable(err) {
		return false
	}
	return !s.yielded && s.ctx.Err() == nil
}

// done marks the stream finished and releases the response body.
func (s *ChatStream) done() {
	s.sawDone = true
	s.closed = true
	s.closeResp()
}

// Close releases the underlying response, if any.
func (s *ChatStream) Close() error {
	s.closed = true
	s.closeResp()
	if s.cancel != nil {
		s.cancel()
	}
	return nil
}

func (s *ChatStream) closeResp() {
	if s.resp != nil {
		_ = s.resp.Body.Close()
		s.resp = nil
	}
}

// establish runs the attempt loop until a 2xx streaming response with a
// framing reader is available, retrying transport errors and retryable
// statuses. It is called whenever a request must be (re)issued and no
// update has been yielded yet.
func (s *ChatStream) establish() error {
	c := s.client
	for s.attempt < c.dial.MaxAttempts {
		s.attempt++
		if err := s.ctx.Err(); err != nil {
			return err
		}
		resp, err := c.do(s.ctx, http.MethodPost, c.chatURL(), s.body)
		if err != nil {
			if ctxErr := s.ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			if !Retryable(err) {
				return err
			}
			if s.attempt >= c.dial.MaxAttempts {
				return &ExhaustedError{Attempts: s.attempt, Cause: err}
			}
			if waitErr := c.backoff(s.ctx, s.attempt, nil, err); waitErr != nil {
				return waitErr
			}
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			statusErr := readStatusError(resp, c.dial.MaxResponseBytes)
			full, body, recovered, recoveryErr := c.rebaseContinuation(s.request, statusErr)
			if recoveryErr != nil {
				return recoveryErr
			}
			if recovered {
				s.request, s.body = full, body
				s.attempt--
				continue
			}
			if !statusErr.Retryable() {
				return statusErr
			}
			if s.attempt >= c.dial.MaxAttempts {
				return wrapExhausted(statusErr, s.attempt)
			}
			if waitErr := c.backoff(s.ctx, s.attempt, resp, statusErr); waitErr != nil {
				return waitErr
			}
			continue
		}
		reader, singleJSON, err := newStreamEventReader(resp, c.proto.Info().Streaming)
		if err != nil {
			_ = resp.Body.Close()
			return err
		}
		s.resp = resp
		s.reader = reader
		if singleJSON {
			s.decoder = chatResponseDecoder{proto: c.proto}
		} else {
			s.decoder = c.proto.NewStreamDecoder()
		}
		if c.onRequestAccepted != nil {
			c.onRequestAccepted()
		}
		return nil
	}
	return &ExhaustedError{Attempts: s.attempt, Cause: errors.New("stream not established")}
}

// --- models ---

// ListModels retrieves the provider's model list, following the protocol's
// pagination. Returns ErrModelsUnsupported when the protocol or endpoint
// does not offer a listing, and ErrUnauthorized when credentials are
// rejected.
func (c *Client) ListModels(ctx context.Context) ([]ModelInfo, error) {
	lister, ok := c.proto.(ModelLister)
	info := c.proto.Info()
	if !ok || info.ModelsPath == "" {
		return nil, fmt.Errorf("%w: %s protocol has no models endpoint", ErrModelsUnsupported, info.ID)
	}

	// Model listing is a short interactive request; bound it even when the
	// dialer does not set an overall timeout.
	timeout := c.dial.RequestTimeout
	if timeout <= 0 {
		timeout = listingRequestTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var models []ModelInfo
	cursor := ""
	seen := map[string]struct{}{}
	for {
		endpoint := lister.ModelsURL(c.base, cursor)
		resp, err := c.do(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, ctxErr
			}
			return nil, err
		}
		if resp.StatusCode == http.StatusNotFound {
			_ = resp.Body.Close()
			return nil, fmt.Errorf("%w: %s returned 404 for %s", ErrModelsUnsupported, info.ID, endpoint.Path)
		}
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			_ = resp.Body.Close()
			return nil, fmt.Errorf("%w: %s rejected the credentials (HTTP %d)", ErrUnauthorized, info.ID, resp.StatusCode)
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			statusErr := readStatusError(resp, c.dial.MaxResponseBytes)
			return nil, statusErr
		}
		payload, err := readBounded(resp.Body, maxModelsResponseBytes)
		_ = resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("read models response: %w", err)
		}
		page, err := lister.DecodeModelsPage(payload)
		if err != nil {
			return nil, fmt.Errorf("parse models response: %w", err)
		}
		models = append(models, page.Models...)
		if page.Next == "" {
			break
		}
		if _, cyclic := seen[page.Next]; cyclic {
			return nil, fmt.Errorf("models pagination repeated cursor %q", page.Next)
		}
		seen[page.Next] = struct{}{}
		cursor = page.Next
	}

	sort.Slice(models, func(i, j int) bool {
		return strings.ToLower(models[i].ID) < strings.ToLower(models[j].ID)
	})
	return models, nil
}

const maxModelsResponseBytes = 16 << 20

// --- internals ---

// withTimeout applies the dialer's overall request timeout, if configured.
func (c *Client) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if c.dial.RequestTimeout > 0 {
		return context.WithTimeout(ctx, c.dial.RequestTimeout)
	}
	return context.WithCancel(ctx)
}

// chatURL resolves the chat endpoint.
func (c *Client) chatURL() *url.URL {
	return joinPath(c.base, c.proto.Info().ChatPath)
}

// encodeChatRequest serializes the request and merges request extras.
func (c *Client) encodeChatRequest(req *ChatRequest) ([]byte, error) {
	if req == nil {
		return nil, fmt.Errorf("request is nil")
	}
	req = c.withContinuationPolicy(req)
	body, err := c.proto.EncodeRequest(req)
	if err != nil {
		return nil, fmt.Errorf("encode request: %w", err)
	}
	body, err = mergeJSONExtras(body, c.requestExtras)
	if err != nil {
		return nil, err
	}
	if c.onRequestEncoded != nil {
		c.onRequestEncoded(body)
	}
	return body, nil
}

func (c *Client) withContinuationPolicy(req *ChatRequest) *ChatRequest {
	if req == nil {
		return req
	}
	allowed := req.ResponseContinuationScope != "" &&
		c.proto.Info().ID == "openai-responses" &&
		c.requestExtras["store"] != false &&
		c.requestExtras["truncation"] != "auto"
	for _, key := range []string{"input", "instructions", "model", "conversation", "previous_response_id"} {
		if _, exists := c.requestExtras[key]; exists {
			allowed = false
		}
	}
	if allowed && req.MediaReplayRecovery == c.mediaReplayRecovery {
		return req
	}
	copy := *req
	if !allowed {
		copy.ResponseContinuationScope = ""
	}
	copy.MediaReplayRecovery = c.mediaReplayRecovery
	return &copy
}

// A rejected reference is a failed context lookup, not an accepted model
// generation. Recover once within the same deadline, before any output, using
// the authoritative full local request. Other errors keep normal retry policy.
func (c *Client) rebaseContinuation(req *ChatRequest, cause error) (*ChatRequest, []byte, bool, error) {
	if c.proto.Info().ID != "openai-responses" || req == nil {
		return nil, nil, false, nil
	}
	plan := planResponseContinuation(req, false)
	if plan.PreviousResponseID == "" {
		return nil, nil, false, nil
	}
	rejectedReference := continuationUnavailable(cause)
	rejectedMediaReplay := c.mediaReplayRecovery && continuationMediaReplayRejected(req, plan, cause)
	if !rejectedReference && !rejectedMediaReplay {
		return nil, nil, false, nil
	}
	full := *req
	full.ResponseContinuationScope = ""
	body, err := c.encodeChatRequest(&full)
	if err != nil {
		return nil, nil, false, err
	}
	if rejectedMediaReplay {
		if err := RecordResponseMediaReplayLimitation(req.ResponseContinuationScope); err != nil {
			logIf(func(lg *logger.Logger) {
				lg.Warn("RESPONSES", "media_replay_limitation_save_failed", "error", err.Error())
			})
		}
		logIf(func(lg *logger.Logger) {
			scope := req.ResponseContinuationScope
			if len(scope) > 12 {
				scope = scope[:12]
			}
			lg.Warn("RESPONSES", "media_replay_recovered", "scope", scope)
		})
	}
	if c.onRequestEncoded != nil {
		c.onRequestEncoded(bytes.Clone(body))
	}
	if c.onRetry != nil {
		c.onRetry(1, 0, cause)
	}
	return &full, body, true, nil
}

// mergeJSONExtras shallow-merges extra fields into an encoded JSON body.
func mergeJSONExtras(body []byte, extra map[string]any) ([]byte, error) {
	if len(extra) == 0 {
		return body, nil
	}
	var merged map[string]any
	if err := json.Unmarshal(body, &merged); err != nil {
		return nil, fmt.Errorf("merge request extras: %w", err)
	}
	for key, value := range extra {
		merged[key] = value
	}
	result, err := json.Marshal(merged)
	if err != nil {
		return nil, fmt.Errorf("marshal request extras: %w", err)
	}
	return result, nil
}

func withStreamFlag(req *ChatRequest, stream bool) *ChatRequest {
	if req == nil {
		req = &ChatRequest{}
	}
	clone := *req
	clone.Stream = stream
	return &clone
}

// do issues one HTTP attempt with composed headers, upload progress, and
// bounded error reporting. It does not inspect the response body.
func (c *Client) do(ctx context.Context, method string, endpoint *url.URL, body []byte) (*http.Response, error) {
	watch, attemptCtx := newProgressWatch(ctx, c.stall)
	var reader io.Reader
	var getBody func() (io.ReadCloser, error)
	if body != nil {
		progress := newUploadProgressReader(bytes.NewReader(body), int64(len(body)), c.onUploadProgress)
		reader = &watchedUploadReader{inner: progress, watch: watch, total: int64(len(body))}
		getBody = func() (io.ReadCloser, error) {
			progress := newUploadProgressReader(bytes.NewReader(body), int64(len(body)), c.onUploadProgress)
			return io.NopCloser(&watchedUploadReader{inner: progress, watch: watch, total: int64(len(body))}), nil
		}
	} else {
		watch.setPhase("response headers")
	}
	req, err := http.NewRequestWithContext(attemptCtx, method, endpoint.String(), reader)
	if err != nil {
		watch.stop()
		return nil, fmt.Errorf("create request: %w", err)
	}
	if body != nil {
		req.Body = io.NopCloser(reader)
		req.GetBody = getBody
		if c.onUploadProgress != nil {
			c.onUploadProgress(0, int64(len(body)))
		}
		req.ContentLength = int64(len(body))
	}
	headers, err := c.headerSource(attemptCtx)
	if err != nil {
		cause := context.Cause(attemptCtx)
		watch.stop()
		var stalled *ProgressStallError
		if errors.As(cause, &stalled) {
			return nil, stalled
		}
		return nil, fmt.Errorf("resolve request headers: %w", err)
	}
	for key, values := range headers {
		for _, value := range values {
			req.Header.Set(key, value)
		}
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "text/event-stream, application/x-ndjson, application/json")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		cause := context.Cause(attemptCtx)
		watch.stop()
		var stalled *ProgressStallError
		if errors.As(cause, &stalled) {
			return nil, stalled
		}
		return nil, err
	}
	if body != nil {
		// The request was handed to the transport; report completion.
		reportUploadComplete(int64(len(body)), c.onUploadProgress)
	}
	watch.setPhase("download")
	resp.Body = &watchedReadCloser{inner: resp.Body, watch: watch, ctx: attemptCtx}
	return resp, nil
}

// backoff waits before the next attempt, firing the retry callback.
func (c *Client) backoff(ctx context.Context, attempt int, resp *http.Response, cause error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	delay := serverHintedDelay(resp, attempt, c.dial)
	if delay < 0 {
		return fmt.Errorf("provider requested a retry delay longer than %s; try again later: %w", maxServerRetryDelay, cause)
	}
	logIf(func(lg *logger.Logger) {
		lg.Warn("RETRY", "attempt", "protocol", c.proto.Info().ID, "attempt", attempt,
			"backoff_ms", delay.Milliseconds(), "error", cause.Error())
	})
	if c.onRetry != nil {
		c.onRetry(attempt, delay, cause)
	}
	return waitForRetry(ctx, delay)
}

// ExhaustedError reports that every attempt failed.
type ExhaustedError struct {
	Attempts int
	Cause    error
}

func (e *ExhaustedError) Error() string {
	return fmt.Sprintf("request failed after %d attempts: %v", e.Attempts, e.Cause)
}
func (e *ExhaustedError) Unwrap() error { return e.Cause }

// StatusError is a non-2xx HTTP response with its parsed message.
type StatusError struct {
	Param      string
	APIType    string
	APICode    string
	StatusCode int
	Message    string
	RequestID  string
	Code       string
	RetryHint  string
}

func (e *StatusError) Error() string {
	message := e.Message
	if message == "" {
		message = http.StatusText(e.StatusCode)
	}
	if e.RequestID != "" {
		message += " (request " + e.RequestID + ")"
	}
	return fmt.Sprintf("API %d: %s", e.StatusCode, message)
}

// Retryable reports whether the status classically warrants a retry.
func (e *StatusError) Retryable() bool {
	if permanentAPIError(e.Code) || e.RetryHint == "false" {
		return false
	}
	if e.StatusCode == 400 || e.StatusCode == 401 || e.StatusCode == 403 || e.StatusCode == 404 || e.StatusCode == 413 || e.StatusCode == 422 {
		return false
	}
	return e.RetryHint == "true" || isRetryableStatus(e.StatusCode)
}

// readStatusError consumes a non-2xx response into a StatusError, parsing
// the provider's error envelope when present.
func readStatusError(resp *http.Response, maxBody int64) *StatusError {
	defer resp.Body.Close()
	body, readErr := readBounded(resp.Body, min64(maxBody, 1<<20))
	message := strings.TrimSpace(string(body))
	if readErr != nil {
		message = readErr.Error()
	}

	var envelope struct {
		Error *struct {
			Param   string `json:"param"`
			Type    string `json:"type"`
			Code    any    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &envelope) == nil {
		if envelope.Error != nil && envelope.Error.Message != "" {
			message = envelope.Error.Message
			if envelope.Error.Type != "" {
				message = envelope.Error.Type + ": " + message
			}
			if envelope.Error.Code != nil && fmt.Sprint(envelope.Error.Code) != "" {
				message = fmt.Sprintf("%v: %s", envelope.Error.Code, message)
			}
		} else if envelope.Message != "" {
			message = envelope.Message
		}
	}
	if message == "" {
		message = http.StatusText(resp.StatusCode)
	}
	code := ""
	param := ""
	apiType := ""
	apiCode := ""
	if envelope.Error != nil {
		code = envelope.Error.Type + " " + fmt.Sprint(envelope.Error.Code)
		param = envelope.Error.Param
		apiType = envelope.Error.Type
		apiCode = fmt.Sprint(envelope.Error.Code)
	}
	return &StatusError{
		Param:      param,
		APIType:    apiType,
		APICode:    apiCode,
		StatusCode: resp.StatusCode,
		Message:    message,
		RequestID:  firstNonEmptyString(resp.Header.Get("request-id"), resp.Header.Get("x-request-id")),
		Code:       code,
		RetryHint:  strings.ToLower(strings.TrimSpace(resp.Header.Get("x-should-retry"))),
	}
}

func readBounded(reader io.Reader, limit int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("response exceeds %d bytes", limit)
	}
	return body, nil
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// --- response framing ---

// streamEventReader yields protocol-agnostic stream events from a response
// body of a given framing.
type streamEventReader interface {
	next() (StreamEvent, error)
}

// newStreamEventReader picks the framing for a 2xx response body. Content
// type wins when it is explicit; otherwise the protocol's declared framing
// is the default, with a JSON sniff protecting against servers that ignore
// the stream flag and answer with one JSON document.
// newStreamEventReader picks the framing reader for a response. The returned
// boolean reports a single-JSON payload, which must be decoded through the
// protocol's non-streaming response schema rather than its event schema.
func newStreamEventReader(resp *http.Response, declared StreamFraming) (streamEventReader, bool, error) {
	mediaType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	body := bufio.NewReader(resp.Body)
	switch mediaType {
	case "application/x-ndjson", "application/json-seq":
		return &ndjsonEventReader{reader: body}, false, nil
	case "application/json":
		// Some gateways label NDJSON streams "application/json". A second
		// top-level value in the body wins over the header.
		if peeked, _ := body.Peek(ndjsonSniffBytes); looksLikeNDJSON(peeked) {
			return &ndjsonEventReader{reader: body}, false, nil
		}
		reader, err := newSingleJSONEventReader(body)
		return reader, true, err
	case "text/event-stream":
		return &sseEventReader{reader: NewSSEReader(body)}, false, nil
	}
	if declared == FramingNDJSON {
		// A server that ignored stream=true answers with one (possibly
		// pretty-printed) document, which line framing would shred. Sniff:
		// a single top-level value routes through the non-streaming schema.
		peeked, _ := body.Peek(ndjsonSniffBytes)
		if trimmed := bytes.TrimSpace(peeked); len(trimmed) > 0 && !looksLikeNDJSON(peeked) {
			reader, err := newSingleJSONEventReader(body)
			return reader, true, err
		}
		return &ndjsonEventReader{reader: body}, false, nil
	}
	peeked, _ := body.Peek(512)
	trimmed := bytes.TrimSpace(peeked)
	if len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[') {
		reader, err := newSingleJSONEventReader(body)
		return reader, true, err
	}
	return &sseEventReader{reader: NewSSEReader(body)}, false, nil
}

// ndjsonSniffBytes bounds the body prefix inspected for framing detection.
const ndjsonSniffBytes = 4096

// looksLikeNDJSON reports whether the inspected prefix holds more than one
// top-level JSON value. A first value that runs past the inspected prefix
// (unexpected EOF) is assumed to be a single document; misrouted payloads
// still recover through the decoders' own tolerance.
func looksLikeNDJSON(peeked []byte) bool {
	dec := json.NewDecoder(bytes.NewReader(peeked))
	var value json.RawMessage
	if err := dec.Decode(&value); err != nil {
		return false
	}
	return len(bytes.TrimSpace(peeked[dec.InputOffset():])) > 0
}

// chatResponseDecoder adapts a protocol's non-streaming response decoder to
// the stream decoder interface. It is selected when a provider answers a
// streaming request with one JSON document (Content-Type application/json or
// a JSON-sniffed body), whose schema follows the non-streaming response —
// which differs from the event schema for several protocols.
type chatResponseDecoder struct {
	proto Protocol
}

func (d chatResponseDecoder) Decode(event StreamEvent) (StreamUpdate, error) {
	resp, err := d.proto.DecodeResponse(event.Data)
	if err != nil {
		return StreamUpdate{}, err
	}
	update := chatResponseToUpdate(resp)
	update.Done = true
	return update, nil
}

// chatResponseToUpdate flattens a non-streaming response into a single
// terminal update.
func chatResponseToUpdate(resp *ChatResponse) StreamUpdate {
	var update StreamUpdate
	if resp == nil {
		return update
	}
	update.ContinuationID = resp.ContinuationID
	update.ContinuationReason = resp.ContinuationReason
	if len(resp.Choices) > 0 {
		choice := resp.Choices[0]
		update.FinishReason = choice.FinishReason
		message := choice.Message
		update.ContentDelta = message.ContentString()
		trace := firstNonEmpty(message.ReasoningContent, message.Reasoning)
		display := message.ReasoningDisplay
		if display.Start > 0 && display.Start < len(trace) {
			update.Reasoning = append(update.Reasoning, reasoning.Delta{Text: strings.TrimSuffix(trace[:display.Start], "\n\n"), Kind: display.Kind, BlockID: "initial"})
			trace = trace[display.Start:]
		}
		update.Reasoning = append(update.Reasoning, reasoning.Delta{Text: trace, Kind: display.Kind, BlockID: firstNonEmpty(display.BlockID, "latest")})
		for _, call := range message.ToolCalls {
			update.ToolCalls = append(update.ToolCalls, ToolCallDelta{
				Index: call.Index, ID: call.ID, Type: call.Type,
				Name: call.Function.Name, Arguments: call.Function.Arguments,
				ReplaceArguments: true,
			})
		}
		update.ProviderItems = message.ProviderItems
	}
	if resp.Usage != nil {
		usage := resp.Usage.normalized()
		update.Usage = &usage
	}
	return update
}

// sseEventReader adapts SSEReader to the streamEventReader interface,
// dropping comment-only events.
type sseEventReader struct {
	reader *SSEReader
}

func (r *sseEventReader) next() (StreamEvent, error) {
	for {
		event, err := r.reader.Next()
		if err != nil {
			return StreamEvent{}, err
		}
		if len(event.Data) == 0 && event.Event == "" {
			continue
		}
		return event, nil
	}
}

// ndjsonEventReader yields one event per non-empty line of a
// newline-delimited JSON body.
type ndjsonEventReader struct {
	reader *bufio.Reader
}

func (r *ndjsonEventReader) next() (StreamEvent, error) {
	for {
		line, err := r.reader.ReadBytes('\n')
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) > 0 {
			return StreamEvent{Data: trimmed}, nil
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return StreamEvent{}, io.EOF
			}
			return StreamEvent{}, err
		}
	}
}

// singleJSONEventReader presents a full non-streaming JSON body as exactly
// one event, so protocols whose server ignored stream=true still flow
// through the stream decoder pipeline.
type singleJSONEventReader struct {
	body     []byte
	consumed bool
}

func newSingleJSONEventReader(reader *bufio.Reader) (*singleJSONEventReader, error) {
	body, err := readBounded(reader, DefaultMaxResponseBytes)
	if err != nil {
		return nil, err
	}
	return &singleJSONEventReader{body: body}, nil
}

func (r *singleJSONEventReader) next() (StreamEvent, error) {
	if r.consumed {
		return StreamEvent{}, io.EOF
	}
	r.consumed = true
	return StreamEvent{Data: r.body}, nil
}

func isEmptyUpdate(update StreamUpdate) bool {
	return update.ContentDelta == "" && update.ReasoningText() == "" &&
		len(update.ToolCalls) == 0 && len(update.ProviderItems) == 0 &&
		update.FinishReason == "" && update.Usage == nil && update.ContinuationID == "" && update.ContinuationReason == ""
}
