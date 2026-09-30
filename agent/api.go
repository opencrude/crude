package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"crude/logger"
	"crude/provider"
	"crude/types/reasoning"
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
	return errors.Is(e, context.Canceled)
}

type completionResult struct {
	Content           string
	ReasoningContent  string
	ReasoningDuration time.Duration
	ReasoningDisplay  reasoning.Display
	FinishReason      string
	ToolCalls         []provider.ToolCall
	ProviderItems     []provider.ProviderItem
	Usage             provider.TokenUsage
	LastPromptTokens  int
	ReplayScope       string
	ReplayPrefixHash  string
	ReplaySourceModel string
}

func (r completionResult) hasOutput() bool {
	return r.Content != "" || r.ReasoningContent != "" || len(r.ToolCalls) > 0 || len(r.ProviderItems) > 0
}

// CompleteText performs a single completion outside the agent loop: no
// tools, no streaming callbacks, no session state. Background jobs (memory
// extraction, session summaries, consolidation) use it so they never touch
// the interactive turn path.
func CompleteText(
	ctx context.Context, p *provider.Provider, model, system, user string,
) (string, error) {
	return CompleteTextWithMaxOutput(ctx, p, model, system, user, 0)
}

// CompleteTextWithMaxOutput caps one-shot jobs by the configured agent limit.
func CompleteTextWithMaxOutput(
	ctx context.Context, p *provider.Provider, model, system, user string, maxOutput int,
) (string, error) {
	a := NewAgent(p, model, nil)
	a.MaxOutputTokens = maxOutput
	result, err := a.completeStream(ctx, []provider.ChatMessage{
		provider.NewTextMessage("system", system),
		provider.NewTextMessage("user", user),
	}, nil, nil, nil, nil, nil, nil, nil, nil)
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
	emitThinking func(reasoning.Delta),
	onToolCallStart func(internalID, id, name string),
	onToolCallDelta func(internalID, id, name, accumulatedArgs string),
	onRetry func(attempt int, backoffSeconds float64, reason string),
	onRequestAccepted func(),
	onRequestProgress func(uploaded, total int64),
) (completionResult, error) {
	return a.completeStreamWithOptions(ctx, msgs, tools, emit, emitThinking,
		onToolCallStart, onToolCallDelta, onRetry, onRequestAccepted,
		onRequestProgress, completionRequestOptions{})
}

type completionRequestOptions struct {
	toolChoice      any
	maxOutputTokens int
	maxAttempts     int
	inputTokens     int
	compaction      bool
}

func (a *Agent) completeStreamWithOptions(
	ctx context.Context,
	msgs []provider.ChatMessage,
	tools []provider.ToolDef,
	emit func(token string),
	emitThinking func(reasoning.Delta),
	onToolCallStart func(internalID, id, name string),
	onToolCallDelta func(internalID, id, name, accumulatedArgs string),
	onRetry func(attempt int, backoffSeconds float64, reason string),
	onRequestAccepted func(),
	onRequestProgress func(uploaded, total int64),
	requestOptions completionRequestOptions,
) (completionResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if a.Provider == nil {
		return completionResult{}, fmt.Errorf("agent provider is nil")
	}
	msgs = provider.FilterReplayMessages(msgs, tools, a.Provider)
	req := provider.ChatRequest{
		Model:                a.Model,
		ReasoningContentKind: a.ReasoningContentKind,
		Messages:             msgs,
		Tools:                tools,
		Stream:               true,
		StreamOptions:        &provider.StreamOptions{IncludeUsage: true},
	}
	if requestOptions.toolChoice != nil {
		req.ToolChoice = requestOptions.toolChoice
	}
	if a.Provider != nil {
		sampling := a.Provider.Sampling()
		req.Temperature = sampling.Temperature
		req.TopP = sampling.TopP
	}

	ctxWindow := a.Provider.ContextWindow(a.Model)
	if requestOptions.compaction {
		req.MaxOutputTokens = a.compactionOutputLimit(ctxWindow, requestOptions.maxOutputTokens)
	} else {
		req.MaxOutputTokens = a.outputTokenLimit(ctxWindow, requestOptions.maxOutputTokens)
	}
	if ctxWindow > 0 {
		estimatedInput := requestOptions.inputTokens
		if estimatedInput <= 0 {
			estimatedInput = estimateCanonicalRequest(msgs, tools, a.Model)
		}
		available := ctxWindow - estimatedInput - contextSafetyReserve(ctxWindow)
		if available <= 0 {
			return completionResult{}, &provider.ContextWindowError{InputTokens: estimatedInput, ContextWindow: ctxWindow}
		}
		req.MaxOutputTokens = min(req.MaxOutputTokens, available)
	}

	if err := a.Provider.Validate(); err != nil {
		return completionResult{}, err
	}

	var requestBody []byte
	maxAttempts := a.MaxRetries
	if requestOptions.maxAttempts > 0 {
		maxAttempts = requestOptions.maxAttempts
	}
	client, err := a.newClient(onRetry, onRequestAccepted, onRequestProgress, func(body []byte) { requestBody = body }, maxAttempts)
	if err != nil {
		return completionResult{}, err
	}

	streamStart := time.Now()
	a.logger.Info("LLM", "prompt", "model", a.Model, "protocol", a.Provider.ProtocolID(), "msgs", len(msgs), "tools", len(tools))

	result, consumeErr := a.consumeStream(
		ctx, client, &req, streamStart,
		emit, emitThinking, onToolCallStart, onToolCallDelta,
	)
	result.ReplayScope = a.Provider.ReplayScope()
	result.ReplayPrefixHash = provider.RequestPrefixHash(req.Messages, req.Tools)
	result.ReplaySourceModel = a.Model
	result.LastPromptTokens = result.Usage.PromptTokens
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
		var exhausted *provider.ExhaustedError
		if errors.As(consumeErr, &exhausted) {
			return completionResult{Usage: result.Usage}, &apiCallError{
				Message:          consumeErr.Error(),
				RetriesExhausted: true,
				Cause:            exhausted.Cause,
			}
		}
		return result, consumeErr
	}

	a.logCompletedTurn(requestBody, result, streamStart)
	return result, nil
}

func maxOutputTokensForContext(ctxWindow int) int {
	if ctxWindow <= 0 {
		return 0
	}
	maxOut := ctxWindow / 4
	if maxOut < 8192 {
		maxOut = 8192
	}
	if maxOut > 65536 {
		maxOut = 65536
	}
	return maxOut
}

// newClient builds the provider transport client wired to this agent's
// callbacks and test injection.
func (a *Agent) newClient(
	onRetry func(attempt int, backoffSeconds float64, reason string),
	onRequestAccepted func(),
	onRequestProgress func(uploaded, total int64),
	onRequestEncoded func(body []byte),
	maxAttempts int,
) (*provider.Client, error) {
	opts := []provider.ClientOption{
		provider.OnRetry(func(attempt int, delay time.Duration, err error) {
			a.logger.Warn("RETRY", "attempt", "model", a.Model, "attempt", attempt,
				"backoff_ms", delay.Milliseconds(), "error", err.Error())
			if onRetry != nil {
				onRetry(attempt, delay.Seconds(), provider.ErrorSummary(err))
			}
		}),
	}
	if onRequestEncoded != nil {
		opts = append(opts, provider.OnRequestEncoded(onRequestEncoded))
	}
	if onRequestAccepted != nil {
		var once sync.Once
		opts = append(opts, provider.OnRequestAccepted(func() { once.Do(onRequestAccepted) }))
	}
	if onRequestProgress != nil {
		opts = append(opts, provider.OnUploadProgress(onRequestProgress))
	}
	if a.testHTTPClient != nil {
		opts = append(opts, provider.WithHTTPClient(a.testHTTPClient))
	}
	if maxAttempts > 0 {
		opts = append(opts, provider.WithMaxAttempts(maxAttempts))
	}
	return a.Provider.Client(opts...)
}

type toolCallAccumulator struct {
	call    provider.ToolCall
	started bool
}

// consumeStream drives a client stream to completion, accumulating the
// normalized updates into a completion result and fanning deltas out to the
// UI callbacks.
func (a *Agent) consumeStream(
	ctx context.Context,
	client *provider.Client,
	req *provider.ChatRequest,
	streamStart time.Time,
	emit func(string),
	emitThinking func(reasoning.Delta),
	onToolCallStart func(string, string, string),
	onToolCallDelta func(string, string, string, string),
) (completionResult, error) {
	stream, err := client.Stream(ctx, req)
	if err != nil {
		return completionResult{}, err
	}
	defer stream.Close()

	var content strings.Builder
	var trace string
	var reasoningStarted time.Time
	var reasoningEnded time.Time
	result := completionResult{}
	toolCalls := make(map[int]*toolCallAccumulator)
	firstTokenLogged := false
	finish := func() {
		result.Content = content.String()
		result.ReasoningContent = trace
		result.ProviderItems = provider.StampProviderItems(provider.CanonicalizeProviderItems(result.ProviderItems), a.Provider, a.Model, provider.RequestPrefixHash(req.Messages, req.Tools))
		result.ToolCalls = finalizeToolCalls(toolCalls)
		if a.Provider.ProtocolID() == "openai" {
			for i := range result.ToolCalls {
				call := &result.ToolCalls[i]
				if call.Type != "function" {
					continue
				}
				for _, def := range req.Tools {
					if def.Type != "custom" || def.Function.Name != call.Function.Name {
						continue
					}
					params, err := provider.ToolCallParams(*call)
					if err == nil {
						if input, ok := params["input"].(string); ok {
							call.Type = "custom"
							call.Function.Arguments = input
						}
					}
					break
				}
			}
		}
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
		update, err := stream.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			finish()
			return result, err
		}
		if update.Usage != nil {
			result.Usage = *update.Usage
		}
		if update.FinishReason != "" {
			result.FinishReason = update.FinishReason
		}
		if len(update.ProviderItems) > 0 {
			result.ProviderItems = append(result.ProviderItems, update.ProviderItems...)
		}
		for _, delta := range update.Reasoning {
			if delta.Text == "" || delta.Kind == reasoning.Encrypted {
				continue
			}
			if reasoningStarted.IsZero() {
				reasoningStarted = time.Now()
			}
			trace = result.ReasoningDisplay.Append(trace, delta)
			if emitThinking != nil {
				emitThinking(delta)
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
	}

	finish()
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

func cancellationError(prefix string, cause error) *apiCallError {
	return &apiCallError{Message: fmt.Sprintf("%s: %v", prefix, cause), Cause: cause}
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

func (a *Agent) emitError(emit func(AgentEvent), err error) {
	if err == nil {
		return
	}
	if errors.Is(err, context.Canceled) {
		a.logger.Warn("LLM", "cancel_intercepted", "original", err.Error())
		emit(AgentEvent{Type: "done", Data: map[string]any{"content": "Cancelled.", "reason": "cancelled"}})
		return
	}

	display := err.Error()
	var apiErr *apiCallError
	if errors.As(err, &apiErr) {
		display = provider.ErrorSummary(apiErr.Cause)
		switch apiErr.StatusCode {
		case http.StatusTooManyRequests:
			var status *provider.StatusError
			if errors.As(apiErr.Cause, &status) && !status.Retryable() {
				display += ". Check this account's quota or billing before continuing."
			} else {
				display += ". Automatic recovery stopped; your work is kept. Send a message to retry when the provider is available."
			}
		case http.StatusUnauthorized:
			if a.Provider != nil && a.Provider.OAuth() != nil {
				display = "Authentication failed — re-sign in with ChatGPT in Settings → Providers"
			} else {
				display = "Authentication failed — check the provider API key in Settings → Providers"
			}
		case http.StatusForbidden:
			display = "Provider access denied. Check this account's permissions for the configured endpoint and model."
		default:
			switch apiErr.StatusCode {
			case 400, 422:
				var status *provider.StatusError
				if errors.As(apiErr.Cause, &status) {
					detail := status.Message
					if len([]rune(detail)) > 140 {
						detail = string([]rune(detail)[:140]) + "..."
					}
					display += ". " + detail
				}
			case 404:
				display += ". Check the configured model and endpoint in Settings."
			default:
				display += ". Automatic recovery stopped; completed work is kept. Send a message to continue."
			}
		}
	}
	if len([]rune(display)) > 240 {
		display = string([]rune(display)[:240]) + "…"
	}
	a.logger.Error("LLM", "emit_error", "original", err.Error(), "display", display)
	emit(AgentEvent{Type: "error", Data: display})
}
