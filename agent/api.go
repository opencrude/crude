package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"mime"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"crude/logger"
	"crude/provider"
)

type apiCallError struct {
	Message          string
	StatusCode       int
	RetriesExhausted bool
	Partial          bool
	Cause            error
}

func (e *apiCallError) Error() string { return e.Message }
func (e *apiCallError) Unwrap() error { return e.Cause }

func (e *apiCallError) IsCancellation() bool {
	return errors.Is(e, context.Canceled) || errors.Is(e, context.DeadlineExceeded)
}

type completionResult struct {
	Content           string
	ReasoningContent  string
	ReasoningDuration time.Duration
	FinishReason      string
	ToolCalls         []provider.ToolCall
	ProviderItems     []provider.ProviderItem
	Usage             provider.TokenUsage
	LastPromptTokens  int
}

func (r completionResult) hasOutput() bool {
	return r.Content != "" || r.ReasoningContent != "" || len(r.ToolCalls) > 0 || len(r.ProviderItems) > 0
}

func addTokenUsage(left, right provider.TokenUsage) provider.TokenUsage {
	return provider.TokenUsage{
		PromptTokens:        left.PromptTokens + right.PromptTokens,
		CompletionTokens:    left.CompletionTokens + right.CompletionTokens,
		PromptTokensCached:  left.PromptTokensCached + right.PromptTokensCached,
		PromptTokensWritten: left.PromptTokensWritten + right.PromptTokensWritten,
	}
}

const (
	maxRetries            = 10
	maxErrorResponseBytes = 1 << 20
	maxJSONResponseBytes  = 64 << 20
	maxRetryDelay         = 2 * time.Minute
)

func isRetryable(code int) bool {
	switch code {
	case http.StatusRequestTimeout, http.StatusConflict, http.StatusTooEarly, http.StatusTooManyRequests:
		return true
	default:
		return code >= 500
	}
}

func retryBackoff(attempt int) time.Duration {
	if attempt <= 1 {
		return time.Duration(500+rand.Intn(1500)) * time.Millisecond
	}
	base := time.Duration(1<<min(attempt-1, 6)) * time.Second
	jitter := time.Duration(rand.Intn(max(1, int(base/4))))
	return min(base+jitter, maxRetryDelay)
}

func retryDelay(resp *http.Response, attempt int) time.Duration {
	if resp != nil {
		if value := strings.TrimSpace(resp.Header.Get("retry-after-ms")); value != "" {
			if milliseconds, err := strconv.ParseFloat(value, 64); err == nil && milliseconds >= 0 {
				return min(time.Duration(milliseconds*float64(time.Millisecond)), maxRetryDelay)
			}
		}
		if value := strings.TrimSpace(resp.Header.Get("Retry-After")); value != "" {
			if seconds, err := strconv.ParseFloat(value, 64); err == nil && seconds >= 0 {
				return min(time.Duration(seconds*float64(time.Second)), maxRetryDelay)
			}
			if when, err := http.ParseTime(value); err == nil {
				return min(max(0, time.Until(when)), maxRetryDelay)
			}
		}
	}
	return retryBackoff(attempt)
}

// CompleteText performs a single completion outside the agent loop: no
// tools, no streaming callbacks, no session state. Background jobs (memory
// extraction, session summaries, consolidation) use it so they never touch
// the interactive turn path.
func CompleteText(
	ctx context.Context, p *provider.Provider, model, system, user string,
) (string, error) {
	a := NewAgent(p, model, nil)
	result, err := a.completeStream(ctx, []provider.ChatMessage{
		provider.NewTextMessage("system", system),
		provider.NewTextMessage("user", user),
	}, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		return "", err
	}
	return result.Content, nil
}

func (a *Agent) completeStream(
	ctx context.Context,
	msgs []provider.ChatMessage,
	tools []provider.ToolDef,
	emit func(token string),
	emitThinking func(text string),
	onToolCallStart func(internalID, id, name string),
	onToolCallDelta func(internalID, id, name, accumulatedArgs string),
	onRetry func(attempt int, backoffSeconds float64),
	onRequestProgress func(uploaded, total int64),
) (completionResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if a.Provider == nil {
		return completionResult{}, fmt.Errorf("agent provider is nil")
	}
	req := provider.ChatRequest{
		Model:         a.Model,
		Messages:      msgs,
		Tools:         tools,
		Stream:        true,
		StreamOptions: &provider.StreamOptions{IncludeUsage: true},
	}

	std := a.Provider.Standard()
	ctxWindow := std.ContextWindow(a.Model)
	if ctxWindow > 0 {
		estimatedInput := provider.EstimateMessagesTotal(msgs, a.Model)
		if len(tools) > 0 {
			if encodedTools, marshalErr := json.Marshal(tools); marshalErr == nil {
				estimatedInput += provider.EstimateTokens(string(encodedTools), a.Model)
			}
		}
		available := ctxWindow - estimatedInput
		if available <= 0 {
			return completionResult{}, fmt.Errorf(
				"estimated input exceeds the model context window (%d/%d tokens)",
				estimatedInput, ctxWindow,
			)
		}
		maxOut := ctxWindow / 4
		if maxOut < 8192 {
			maxOut = 8192
		}
		if maxOut > 65536 {
			maxOut = 65536
		}
		if maxOut > available {
			maxOut = available
		}
		req.MaxOutputTokens = maxOut
	}

	body, err := std.EncodeRequest(&req)
	if err != nil {
		return completionResult{}, fmt.Errorf("encode request: %w", err)
	}
	body, err = mergeRequestExtras(body, a.Provider.BuildExtraBody())
	if err != nil {
		return completionResult{}, err
	}
	if err := a.Provider.Validate(); err != nil {
		return completionResult{}, err
	}

	// Refresh OAuth access tokens (e.g. ChatGPT subscription) before building
	// the request so the bearer is current for every attempt.
	if err := a.Provider.EnsureAuth(ctx); err != nil {
		return completionResult{}, fmt.Errorf("refresh oauth token: %w", err)
	}

	endpoint := a.Provider.ChatEndpoint()
	attemptLimit := maxRetries
	if a.MaxRetries > 0 {
		attemptLimit = a.MaxRetries
	}
	client := a.client
	if client == nil {
		client = http.DefaultClient
	}

	streamStart := time.Now()
	a.logger.Info("LLM", "prompt", "model", a.Model, "standard", std.ID(), "msgs", len(msgs), "tools", len(tools))

	var accumulatedUsage provider.TokenUsage
	for attempt := 1; attempt <= attemptLimit; attempt++ {
		if err := ctx.Err(); err != nil {
			return completionResult{Usage: accumulatedUsage}, cancellationError("Stream request cancelled", err)
		}

		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			return completionResult{Usage: accumulatedUsage}, fmt.Errorf("create request: %w", err)
		}
		progress := newUploadProgressReader(bytes.NewReader(body), int64(len(body)), onRequestProgress)
		httpReq.Body = io.NopCloser(progress)
		httpReq.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(newUploadProgressReader(bytes.NewReader(body), int64(len(body)), onRequestProgress)), nil
		}
		progress.report(0)
		for header, value := range a.Provider.Headers() {
			httpReq.Header.Set(header, value)
		}
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Accept", "text/event-stream, application/json")

		resp, err := client.Do(httpReq)
		if err == nil {
			progress.complete()
		}
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return completionResult{Usage: accumulatedUsage}, cancellationError("Stream request cancelled", ctxErr)
			}
			if attempt == attemptLimit {
				return completionResult{Usage: accumulatedUsage}, &apiCallError{
					Message:          fmt.Sprintf("stream request failed after %d attempts: %v", attempt, err),
					RetriesExhausted: true,
					Cause:            err,
				}
			}
			delay := retryBackoff(attempt)
			a.logRetry(attempt, delay, err, onRetry)
			if err := waitForRetry(ctx, delay); err != nil {
				return completionResult{Usage: accumulatedUsage}, cancellationError("Stream request cancelled during retry", err)
			}
			continue
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			statusErr := readHTTPStatusError(resp)
			if !isRetryable(resp.StatusCode) || attempt == attemptLimit {
				statusErr.RetriesExhausted = isRetryable(resp.StatusCode) && attempt == attemptLimit
				return completionResult{Usage: accumulatedUsage}, statusErr
			}
			delay := retryDelay(resp, attempt)
			a.logRetry(attempt, delay, statusErr, onRetry)
			if err := waitForRetry(ctx, delay); err != nil {
				return completionResult{Usage: accumulatedUsage}, cancellationError("Stream request cancelled during retry", err)
			}
			continue
		}

		result, consumeErr := a.consumeResponse(
			ctx, resp, std, emit, emitThinking, onToolCallStart, onToolCallDelta, streamStart,
		)
		result.LastPromptTokens = result.Usage.PromptTokens
		result.Usage = addTokenUsage(accumulatedUsage, result.Usage)
		if consumeErr != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return result, cancellationError("Stream request cancelled", ctxErr)
			}
			if result.hasOutput() {
				return result, &apiCallError{
					Message: fmt.Sprintf("stream interrupted after partial response: %v", consumeErr),
					Partial: true,
					Cause:   consumeErr,
				}
			}
			if attempt < attemptLimit && retryableStreamError(consumeErr) {
				accumulatedUsage = result.Usage
				delay := retryBackoff(attempt)
				a.logRetry(attempt, delay, consumeErr, onRetry)
				if err := waitForRetry(ctx, delay); err != nil {
					return completionResult{Usage: accumulatedUsage}, cancellationError("Stream request cancelled during retry", err)
				}
				continue
			}
			return result, consumeErr
		}

		a.logCompletedTurn(body, result, streamStart)
		return result, nil
	}

	return completionResult{Usage: accumulatedUsage}, &apiCallError{
		Message:          "stream request failed: retry limit reached",
		RetriesExhausted: true,
	}
}

type uploadProgressReader struct {
	reader     io.Reader
	total      int64
	sent       int64
	lastReport int
	onProgress func(uploaded, total int64)
}

func newUploadProgressReader(reader io.Reader, total int64, onProgress func(uploaded, total int64)) *uploadProgressReader {
	return &uploadProgressReader{reader: reader, total: total, lastReport: -1, onProgress: onProgress}
}

func (r *uploadProgressReader) Read(buffer []byte) (int, error) {
	n, err := r.reader.Read(buffer)
	if n > 0 {
		r.sent += int64(n)
		r.report(r.sent)
	}
	return n, err
}

func (r *uploadProgressReader) complete() {
	r.report(r.total)
}

func (r *uploadProgressReader) report(uploaded int64) {
	if r == nil || r.onProgress == nil {
		return
	}
	uploaded = max(0, min(r.total, uploaded))
	percent := 0
	if r.total > 0 {
		percent = int(uploaded * 100 / r.total)
	}
	if percent == r.lastReport {
		return
	}
	r.lastReport = percent
	r.onProgress(uploaded, r.total)
}

func mergeRequestExtras(body []byte, extra map[string]any) ([]byte, error) {
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

func (a *Agent) consumeResponse(
	ctx context.Context,
	resp *http.Response,
	std provider.APIStandard,
	emit func(string),
	emitThinking func(string),
	onToolCallStart func(string, string, string),
	onToolCallDelta func(string, string, string, string),
	streamStart time.Time,
) (completionResult, error) {
	defer resp.Body.Close()

	mediaType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	bodyReader := bufio.NewReader(resp.Body)

	// Ollama's native protocol streams newline-delimited JSON (application/
	// x-ndjson) rather than Server-Sent Events. It must be routed before the
	// JSON sniff below: NDJSON starts with '{' and would otherwise be mistaken
	// for a single non-streaming JSON response. Re-emit each line as an SSE
	// "data:" event so the existing SSE consumer and stream decoders handle it.
	if mediaType == "application/x-ndjson" || mediaType == "application/json-seq" {
		return a.consumeSSE(ctx, newNDJSONToSSEReader(bodyReader), std.NewStreamDecoder(), emit, emitThinking,
			onToolCallStart, onToolCallDelta, streamStart)
	}

	isJSON := mediaType == "application/json" || strings.HasSuffix(mediaType, "+json")
	if !isJSON && mediaType != "text/event-stream" {
		isJSON = readerLooksLikeJSON(bodyReader)
	}
	if isJSON {
		body, err := readBounded(bodyReader, maxJSONResponseBytes)
		if err != nil {
			return completionResult{}, err
		}
		decoded, err := std.DecodeResponse(body)
		if err != nil {
			return completionResult{}, err
		}
		result, resultErr := a.resultFromChatResponse(decoded, emit, emitThinking, onToolCallStart, onToolCallDelta)
		if result.ReasoningContent != "" {
			result.ReasoningDuration = time.Since(streamStart)
		}
		return result, resultErr
	}

	return a.consumeSSE(ctx, bodyReader, std.NewStreamDecoder(), emit, emitThinking,
		onToolCallStart, onToolCallDelta, streamStart)
}

func readerLooksLikeJSON(reader *bufio.Reader) bool {
	peeked, _ := reader.Peek(512)
	trimmed := bytes.TrimSpace(peeked)
	return len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[')
}

// ndjsonToSSEReader wraps a newline-delimited JSON stream (Ollama's native
// streaming format) and re-emits each non-empty line as an SSE "data:" event
// terminated by a blank line, so the existing SSE consumer and stream decoders
// process it unchanged.
type ndjsonToSSEReader struct {
	r   *bufio.Reader
	buf []byte
	eof bool
}

func newNDJSONToSSEReader(r *bufio.Reader) *ndjsonToSSEReader {
	return &ndjsonToSSEReader{r: r}
}

func (n *ndjsonToSSEReader) Read(p []byte) (int, error) {
	for {
		if len(n.buf) > 0 {
			copied := copy(p, n.buf)
			n.buf = n.buf[copied:]
			return copied, nil
		}
		if n.eof {
			return 0, io.EOF
		}
		line, err := n.r.ReadBytes('\n')
		line = bytes.TrimSpace(line)
		if len(line) > 0 {
			n.buf = append(n.buf, "data: "...)
			n.buf = append(n.buf, line...)
			n.buf = append(n.buf, '\n', '\n')
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				n.eof = true
				continue // flush any buffered event before reporting EOF
			}
			return 0, err
		}
	}
}

type toolCallAccumulator struct {
	call    provider.ToolCall
	started bool
}

func (a *Agent) consumeSSE(
	ctx context.Context,
	body io.Reader,
	decoder provider.StreamDecoder,
	emit func(string),
	emitThinking func(string),
	onToolCallStart func(string, string, string),
	onToolCallDelta func(string, string, string, string),
	streamStart time.Time,
) (completionResult, error) {
	reader := provider.NewSSEReader(body)
	var content strings.Builder
	var reasoning strings.Builder
	var reasoningStarted time.Time
	var reasoningEnded time.Time
	result := completionResult{}
	toolCalls := make(map[int]*toolCallAccumulator)
	sawEvent := false
	sawTerminal := false
	firstTokenLogged := false
	finish := func() {
		result.Content = content.String()
		result.ReasoningContent = reasoning.String()
		result.ToolCalls = finalizeToolCalls(toolCalls)
		if !reasoningStarted.IsZero() {
			if reasoningEnded.IsZero() {
				reasoningEnded = time.Now()
			}
			result.ReasoningDuration = reasoningEnded.Sub(reasoningStarted)
		}
	}

	for {
		if err := ctx.Err(); err != nil {
			finish()
			return result, err
		}
		event, err := reader.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			finish()
			return result, err
		}
		sawEvent = true

		update, err := decoder.Decode(event)
		if err != nil {
			finish()
			return result, err
		}
		if update.Usage != nil {
			result.Usage = *update.Usage
		}
		if update.FinishReason != "" {
			result.FinishReason = update.FinishReason
			sawTerminal = true
		}
		if len(update.ProviderItems) > 0 {
			result.ProviderItems = append(result.ProviderItems, update.ProviderItems...)
		}
		if update.ReasoningDelta != "" {
			if reasoningStarted.IsZero() {
				reasoningStarted = time.Now()
			}
			reasoning.WriteString(update.ReasoningDelta)
			if emitThinking != nil {
				emitThinking(update.ReasoningDelta)
			}
		}
		if update.ContentDelta != "" {
			if !reasoningStarted.IsZero() && reasoningEnded.IsZero() {
				reasoningEnded = time.Now()
			}
			if !firstTokenLogged {
				a.logger.Debug("STREAM", "first_token", "model", a.Model, "latency_ms", time.Since(streamStart).Milliseconds())
				firstTokenLogged = true
			}
			content.WriteString(update.ContentDelta)
			if emit != nil {
				emit(update.ContentDelta)
			}
		}
		for _, delta := range update.ToolCalls {
			if !reasoningStarted.IsZero() && reasoningEnded.IsZero() {
				reasoningEnded = time.Now()
			}
			a.accumulateToolCall(toolCalls, delta, onToolCallStart, onToolCallDelta)
		}
		if update.Done {
			sawTerminal = true
			break
		}
	}

	finish()
	if !sawEvent {
		return result, io.ErrUnexpectedEOF
	}
	if !sawTerminal {
		return result, fmt.Errorf("%w: provider stream ended without terminal event", io.ErrUnexpectedEOF)
	}
	if result.FinishReason == "" {
		if len(result.ToolCalls) > 0 {
			result.FinishReason = "tool_calls"
		} else {
			result.FinishReason = "stop"
		}
	}
	return result, nil
}

func (a *Agent) accumulateToolCall(
	toolCalls map[int]*toolCallAccumulator,
	delta provider.ToolCallDelta,
	onToolCallStart func(string, string, string),
	onToolCallDelta func(string, string, string, string),
) {
	index := delta.Index
	if existing := toolCalls[index]; existing != nil && delta.ID != "" &&
		existing.call.ID != "" && existing.call.ID != delta.ID {
		for index = 0; toolCalls[index] != nil; index++ {
		}
	}

	acc := toolCalls[index]
	if acc == nil {
		a.toolCallSeq++
		acc = &toolCallAccumulator{call: provider.ToolCall{
			Index:      index,
			InternalID: fmt.Sprintf("_tc%d", a.toolCallSeq),
			Type:       "function",
		}}
		toolCalls[index] = acc
	}
	if delta.ID != "" {
		acc.call.ID = delta.ID
	}
	if delta.Type != "" {
		acc.call.Type = delta.Type
	}
	if delta.Name != "" {
		acc.call.Function.Name = delta.Name
	}
	if delta.ReplaceArguments {
		acc.call.Function.Arguments = delta.Arguments
	} else {
		acc.call.Function.Arguments += delta.Arguments
	}
	if !acc.started && acc.call.Function.Name != "" {
		acc.started = true
		if onToolCallStart != nil {
			onToolCallStart(acc.call.InternalID, acc.call.ID, acc.call.Function.Name)
		}
	}
	if onToolCallDelta != nil && acc.call.Function.Name != "" {
		onToolCallDelta(acc.call.InternalID, acc.call.ID, acc.call.Function.Name, acc.call.Function.Arguments)
	}
}

func finalizeToolCalls(accumulators map[int]*toolCallAccumulator) []provider.ToolCall {
	if len(accumulators) == 0 {
		return nil
	}
	indexes := make([]int, 0, len(accumulators))
	for index := range accumulators {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)

	result := make([]provider.ToolCall, 0, len(indexes))
	for _, index := range indexes {
		call := accumulators[index].call
		if call.Function.Name == "" {
			continue
		}
		if call.ID == "" {
			call.ID = "call" + call.InternalID
		}
		if call.Type == "" {
			call.Type = "function"
		}
		result = append(result, call)
	}
	return result
}

func (a *Agent) resultFromChatResponse(
	response *provider.ChatResponse,
	emit func(string),
	emitThinking func(string),
	onToolCallStart func(string, string, string),
	onToolCallDelta func(string, string, string, string),
) (completionResult, error) {
	if response == nil {
		return completionResult{}, fmt.Errorf("provider returned an empty response")
	}
	if response.Error != nil {
		return completionResult{}, &provider.APIError{
			Type: response.Error.Type, Code: fmt.Sprint(response.Error.Code), Message: response.Error.Message,
		}
	}
	if len(response.Choices) == 0 {
		return completionResult{}, fmt.Errorf("provider response contains no choices")
	}

	choice := response.Choices[0]
	message := choice.Message
	content := message.ContentString()
	if content == "" {
		content = message.Refusal
	}
	reasoning := message.ReasoningContent
	if reasoning == "" {
		reasoning = message.Reasoning
	}
	if content == "" {
		content = choice.Delta.Content
	}
	if content == "" {
		content = choice.Delta.Refusal
	}
	if reasoning == "" {
		reasoning = choice.Delta.ReasoningContent
	}
	if reasoning == "" {
		reasoning = choice.Delta.Reasoning
	}
	toolCalls := message.ToolCalls
	if len(toolCalls) == 0 {
		toolCalls = choice.Delta.ToolCalls
	}
	for i := range toolCalls {
		a.toolCallSeq++
		toolCalls[i].Index = i
		toolCalls[i].InternalID = fmt.Sprintf("_tc%d", a.toolCallSeq)
		if toolCalls[i].ID == "" {
			toolCalls[i].ID = "call" + toolCalls[i].InternalID
		}
		if toolCalls[i].Type == "" {
			toolCalls[i].Type = "function"
		}
		if onToolCallStart != nil {
			onToolCallStart(toolCalls[i].InternalID, toolCalls[i].ID, toolCalls[i].Function.Name)
		}
		if onToolCallDelta != nil {
			onToolCallDelta(toolCalls[i].InternalID, toolCalls[i].ID,
				toolCalls[i].Function.Name, toolCalls[i].Function.Arguments)
		}
	}
	if reasoning != "" && emitThinking != nil {
		emitThinking(reasoning)
	}
	if content != "" && emit != nil {
		emit(content)
	}

	result := completionResult{
		Content:          content,
		ReasoningContent: reasoning,
		FinishReason:     choice.FinishReason,
		ToolCalls:        toolCalls,
		ProviderItems:    message.ProviderItems,
	}
	if response.Usage != nil {
		result.Usage.PromptTokens = response.Usage.PromptTokens
		result.Usage.CompletionTokens = response.Usage.CompletionTokens
		if response.Usage.PromptTokensDetails != nil {
			result.Usage.PromptTokensCached = response.Usage.PromptTokensDetails.CachedTokens
			result.Usage.PromptTokensWritten = response.Usage.PromptTokensDetails.CacheWriteTokens
		}
	}
	if result.FinishReason == "" {
		if len(result.ToolCalls) > 0 {
			result.FinishReason = "tool_calls"
		} else {
			result.FinishReason = "stop"
		}
	}
	return result, nil
}

func readHTTPStatusError(resp *http.Response) *apiCallError {
	defer resp.Body.Close()
	body, readErr := readBounded(resp.Body, maxErrorResponseBytes)
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
	if requestID := firstNonEmpty(resp.Header.Get("request-id"), resp.Header.Get("x-request-id")); requestID != "" {
		message += " (request " + requestID + ")"
	}
	return &apiCallError{
		Message:    fmt.Sprintf("API %d: %s", resp.StatusCode, message),
		StatusCode: resp.StatusCode,
		Cause:      fmt.Errorf("HTTP %d", resp.StatusCode),
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

func retryableStreamError(err error) bool {
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var apiErr *provider.APIError
	if errors.As(err, &apiErr) {
		typ := strings.ToLower(apiErr.Type + " " + apiErr.Code)
		return strings.Contains(typ, "overload") || strings.Contains(typ, "rate_limit") ||
			strings.Contains(typ, "temporar") || strings.Contains(typ, "api_error") ||
			strings.Contains(typ, "server_error") || strings.Contains(typ, "internal_error") ||
			strings.Contains(typ, "timeout") || strings.Contains(typ, "unavailable")
	}
	return false
}

func waitForRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func cancellationError(prefix string, cause error) *apiCallError {
	return &apiCallError{Message: fmt.Sprintf("%s: %v", prefix, cause), Cause: cause}
}

func (a *Agent) logRetry(attempt int, delay time.Duration, err error, onRetry func(int, float64)) {
	a.logger.Warn("RETRY", "attempt", "model", a.Model, "attempt", attempt,
		"backoff_ms", delay.Milliseconds(), "error", err.Error())
	if onRetry != nil {
		onRetry(attempt, delay.Seconds())
	}
}

func (a *Agent) logCompletedTurn(body []byte, result completionResult, streamStart time.Time) {
	streamDuration := time.Since(streamStart).Milliseconds()
	a.logger.Info("LLM", "response", "model", a.Model, "tool_calls", len(result.ToolCalls),
		"prompt_tokens", result.Usage.PromptTokens, "completion_tokens", result.Usage.CompletionTokens,
		"cached_tokens", result.Usage.PromptTokensCached, "latency_ms", streamDuration)

	if a.turn == nil {
		return
	}
	turnToolCalls := make([]logger.TurnToolCall, len(result.ToolCalls))
	for i, call := range result.ToolCalls {
		turnToolCalls[i] = logger.TurnToolCall{
			ID:        call.ID,
			Name:      call.Function.Name,
			Arguments: call.Function.Arguments,
		}
	}
	if turnFile := a.turn.Write(logger.TurnRecord{
		Time:             time.Now(),
		Model:            a.Model,
		LatencyMS:        streamDuration,
		PromptTokens:     result.Usage.PromptTokens,
		CompletionTokens: result.Usage.CompletionTokens,
		CachedTokens:     result.Usage.PromptTokensCached,
		FinishReason:     result.FinishReason,
		RequestBody:      body,
		ResponseContent:  result.Content,
		ReasoningContent: result.ReasoningContent,
		ToolCalls:        turnToolCalls,
	}); turnFile != "" {
		a.logger.Debug("LLM", "turn_dump", "file", turnFile)
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func (a *Agent) emitError(emit func(AgentEvent), err error) {
	if err == nil {
		return
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		a.logger.Warn("LLM", "cancel_intercepted", "original", err.Error())
		emit(AgentEvent{Type: "done", Data: map[string]any{"content": "Cancelled.", "reason": "cancelled"}})
		return
	}

	display := err.Error()
	var apiErr *apiCallError
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case http.StatusTooManyRequests:
			display = "Rate limited — please wait a moment and try again"
		case http.StatusUnauthorized, http.StatusForbidden:
			if a.Provider != nil && a.Provider.OAuth() != nil {
				display = "Authentication failed — re-sign in with ChatGPT in Settings → Providers"
			} else {
				display = "Authentication failed — check the provider API key in Settings → Providers"
			}
		default:
			if apiErr.StatusCode >= 500 {
				display = "API server error — please try again"
			}
		}
	}
	if len([]rune(display)) > 240 {
		display = string([]rune(display)[:240]) + "…"
	}
	a.logger.Error("LLM", "emit_error", "original", err.Error(), "display", display)
	emit(AgentEvent{Type: "error", Data: display})
}
