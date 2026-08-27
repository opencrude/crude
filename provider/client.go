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

	// sessionID is a stable identifier for protocols that require
	// per-conversation headers (see SessionScope).
	sessionID string

	onRetry          func(attempt int, delay time.Duration, err error)
	onUploadProgress func(uploaded, total int64)
	onRequestEncoded func(body []byte)
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

// WithRequestExtras sets fields merged into every chat request body. Keys
// overwrite what the protocol encoded (shallow merge).
func WithRequestExtras(extra map[string]any) ClientOption {
	return func(c *Client) { c.requestExtras = extra }
}

// OnRequestEncoded registers a callback receiving the fully encoded request
// body (after extras merge) for each chat request — used for turn logging.
func OnRequestEncoded(fn func(body []byte)) ClientOption {
	return func(c *Client) { c.onRequestEncoded = fn }
}

// OnRetry registers a callback fired before each retry wait.
func OnRetry(fn func(attempt int, delay time.Duration, err error)) ClientOption {
	return func(c *Client) { c.onRetry = fn }
}

// OnUploadProgress registers a callback for upload progress during request
// streaming. It is called with percentage-deduplicated (uploaded, total)
// byte counts.
func OnUploadProgress(fn func(uploaded, total int64)) ClientOption {
	return func(c *Client) { c.onUploadProgress = fn }
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
	c := &Client{
		proto:     proto,
		base:      base,
		hc:        newHTTPClient(dial),
		dial:      dial,
		sessionID: newSessionUUID(),
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
			DialContext:           dialContext(dial.ConnectTimeout),
			MaxIdleConns:          10,
			MaxIdleConnsPerHost:   10,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: dial.ConnectTimeout,
		},
	}
}

// --- chat ---

// Complete performs a non-streaming chat request.
func (c *Client) Complete(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	req = withStreamFlag(req, false)
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
		defer resp.Body.Close()
		payload, err := readBounded(resp.Body, c.dial.MaxResponseBytes)
		if err != nil {
			return nil, fmt.Errorf("read response: %w", err)
		}
		decoded, err := c.proto.DecodeResponse(payload)
		if err != nil {
			return nil, err
		}
		return decoded, nil
	}
	return nil, wrapExhausted(lastErr, c.dial.MaxAttempts)
}

// Stream performs a streaming chat request. Retry semantics: attempts are
// made transparently until the response is established, and again for
// retryable failures before the first update is yielded — callers never
// observe a retry after they have seen output. Next returns io.EOF after
// the terminal update.
func (c *Client) Stream(ctx context.Context, req *ChatRequest) (*ChatStream, error) {
	req = withStreamFlag(req, true)
	body, err := c.encodeChatRequest(req)
	if err != nil {
		return nil, err
	}
	return &ChatStream{
		client: c,
		ctx:    ctx,
		body:   body,
	}, nil
}

// ChatStream is a decoded, ordered view of one streaming response.
type ChatStream struct {
	client   *Client
	ctx      context.Context
	body     []byte
	resp     *http.Response
	reader   streamEventReader
	decoder  StreamDecoder
	attempt  int
	yielded  bool
	sawDone  bool
	closed   bool
	accepted bool
}

// Next returns the next decoded stream update. It returns io.EOF after the
// terminal update. Errors that occur before any update has been yielded are
// retried transparently when retryable; errors after output has been seen
// are surfaced as-is so callers can preserve the partial result.
func (s *ChatStream) Next() (StreamUpdate, error) {
	if s.closed {
		return StreamUpdate{}, io.EOF
	}
	for {
		if s.resp == nil {
			if err := s.establish(); err != nil {
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
			if !s.yielded && s.client.retryableStreamError(err) && s.attempt < s.client.dial.MaxAttempts {
				if waitErr := s.client.backoff(s.ctx, s.attempt, nil, err); waitErr != nil {
					return StreamUpdate{}, waitErr
				}
				continue
			}
			return StreamUpdate{}, err
		}
		update, err := s.decoder.Decode(event)
		if err != nil {
			s.closeResp()
			if !s.yielded && s.client.retryableStreamError(err) && s.attempt < s.client.dial.MaxAttempts {
				if waitErr := s.client.backoff(s.ctx, s.attempt, nil, err); waitErr != nil {
					return StreamUpdate{}, waitErr
				}
				continue
			}
			return StreamUpdate{}, err
		}
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
		s.yielded = true
		if update.Done {
			s.done()
		}
		return update, nil
	}
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
			if !statusErr.Retryable() || s.attempt >= c.dial.MaxAttempts {
				return statusErr
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
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		timeout := c.dial.RequestTimeout
		if timeout <= 0 || timeout > listingRequestTimeout {
			timeout = listingRequestTimeout
		}
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

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
		if _, hasDeadline := ctx.Deadline(); !hasDeadline {
			return context.WithTimeout(ctx, c.dial.RequestTimeout)
		}
	}
	return context.WithCancel(ctx)
}

// chatURL resolves the chat endpoint.
func (c *Client) chatURL() *url.URL {
	return joinPath(c.base, c.proto.Info().ChatPath)
}

// encodeChatRequest serializes the request and merges request extras.
func (c *Client) encodeChatRequest(req *ChatRequest) ([]byte, error) {
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
	var reader io.Reader
	var getBody func() (io.ReadCloser, error)
	if body != nil {
		reader = bytes.NewReader(body)
		getBody = func() (io.ReadCloser, error) {
			return io.NopCloser(newUploadProgressReader(bytes.NewReader(body), int64(len(body)), c.onUploadProgress)), nil
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint.String(), reader)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	if body != nil {
		progress := newUploadProgressReader(bytes.NewReader(body), int64(len(body)), c.onUploadProgress)
		req.Body = io.NopCloser(progress)
		req.GetBody = getBody
		progress.report(0)
		req.ContentLength = int64(len(body))
	}
	headers, err := c.headerSource(ctx)
	if err != nil {
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
		return nil, err
	}
	if body != nil {
		// The request was handed to the transport; report completion.
		reportUploadComplete(int64(len(body)), c.onUploadProgress)
	}
	return resp, nil
}

// backoff waits before the next attempt, firing the retry callback.
func (c *Client) backoff(ctx context.Context, attempt int, resp *http.Response, cause error) error {
	delay := serverHintedDelay(resp, attempt, c.dial)
	logIf(func(lg *logger.Logger) {
		lg.Warn("RETRY", "attempt", "protocol", c.proto.Info().ID, "attempt", attempt,
			"backoff_ms", delay.Milliseconds(), "error", cause.Error())
	})
	if c.onRetry != nil {
		c.onRetry(attempt, delay, cause)
	}
	return waitForRetry(ctx, delay)
}

// retryableStreamError reports whether a mid-stream error warrants
// re-establishing the stream (only valid before any output was yielded).
func (c *Client) retryableStreamError(err error) bool {
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		typ := strings.ToLower(apiErr.Type + " " + apiErr.Code)
		return strings.Contains(typ, "overload") || strings.Contains(typ, "rate_limit") ||
			strings.Contains(typ, "temporar") || strings.Contains(typ, "api_error") ||
			strings.Contains(typ, "server_error") || strings.Contains(typ, "internal_error") ||
			strings.Contains(typ, "timeout") || strings.Contains(typ, "unavailable")
	}
	return false
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
	StatusCode int
	Message    string
	RequestID  string
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
func (e *StatusError) Retryable() bool { return isRetryableStatus(e.StatusCode) }

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
	return &StatusError{
		StatusCode: resp.StatusCode,
		Message:    message,
		RequestID:  firstNonEmptyString(resp.Header.Get("request-id"), resp.Header.Get("x-request-id")),
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
		reader, err := newSingleJSONEventReader(body)
		return reader, true, err
	case "text/event-stream":
		return &sseEventReader{reader: NewSSEReader(body)}, false, nil
	}
	if declared == FramingNDJSON {
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
	if len(resp.Choices) > 0 {
		choice := resp.Choices[0]
		update.FinishReason = choice.FinishReason
		message := choice.Message
		update.ContentDelta = message.ContentString()
		update.ReasoningDelta = message.ReasoningContent
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
		usage := &TokenUsage{
			PromptTokens:     resp.Usage.PromptTokens,
			CompletionTokens: resp.Usage.CompletionTokens,
		}
		if resp.Usage.PromptTokensDetails != nil {
			usage.PromptTokensCached = resp.Usage.PromptTokensDetails.CachedTokens
			usage.PromptTokensWritten = resp.Usage.PromptTokensDetails.CacheWriteTokens
		}
		update.Usage = usage
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
	return update.ContentDelta == "" && update.ReasoningDelta == "" &&
		len(update.ToolCalls) == 0 && len(update.ProviderItems) == 0 &&
		update.FinishReason == "" && update.Usage == nil
}
