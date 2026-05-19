package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strings"
	"time"

	"crude/logger"
	"crude/provider"
)

type apiCallError struct {
	Message          string
	RetriesExhausted bool
}

func (e *apiCallError) Error() string { return e.Message }

func (e *apiCallError) IsCancellation() bool {
	return strings.Contains(e.Message, "context canceled") || strings.Contains(e.Message, "context cancelled")
}

const maxRetries = 10

func isRetryable(code int) bool {
	return code == http.StatusTooManyRequests || code >= 500
}

func retryBackoff(attempt int) time.Duration {
	if attempt <= 1 {
		return time.Duration(500+rand.Intn(1500)) * time.Millisecond
	}
	base := time.Duration(1<<min(attempt-1, 6)) * time.Second
	jitter := time.Duration(rand.Intn(int(base))) / 4
	return base + jitter
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
) (content string, reasoningContent string, finishReason string, toolCalls []provider.ToolCall, promptTokens int, completionTokens int, promptTokensCached int, err error) {
	req := provider.ChatRequest{
		Model:    a.Model,
		Messages: msgs,
		Tools:    tools,
		Stream:   true,
		StreamOptions: &provider.StreamOptions{IncludeUsage: true},
	}

	std := a.Provider.Standard()

	body, err := std.EncodeRequest(&req)
	if err != nil {
		return "", "", "", nil, 0, 0, 0, fmt.Errorf("encode request: %w", err)
	}

	if extra := a.Provider.BuildExtraBody(); len(extra) > 0 {
		a.logger.Debug("LLM", "extra_body", "keys", len(extra))
		var merged map[string]any
		if err := json.Unmarshal(body, &merged); err != nil {
			return "", "", "", nil, 0, 0, 0, fmt.Errorf("merge extras: %w", err)
		}
		for k, v := range extra {
			merged[k] = v
		}
		body, err = json.Marshal(merged)
		if err != nil {
			return "", "", "", nil, 0, 0, 0, fmt.Errorf("marshal merged: %w", err)
		}
	}

	streamStart := time.Now()
	a.logger.Info("LLM", "prompt", "model", a.Model, "msgs", len(msgs), "tools", len(tools))

	client := &http.Client{Timeout: 300 * time.Second}
	endpoint := a.Provider.ChatEndpoint()

	firstTokenLogged := false

	for attempt := 1; ; attempt++ {
		select {
		case <-ctx.Done():
			a.logger.Warn("CANCEL", "stream_cancelled", "reason", ctx.Err().Error())
			return "", "", "", nil, 0, 0, 0, &apiCallError{Message: fmt.Sprintf("Stream request cancelled: %v", ctx.Err())}
		default:
		}

		httpReq, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(body))
		if err != nil {
			return "", "", "", nil, 0, 0, 0, fmt.Errorf("create request: %w", err)
		}
		hdr, val := a.Provider.Auth()
		httpReq.Header.Set(hdr, val)
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Accept", "text/event-stream")

		resp, err := client.Do(httpReq)
		if err != nil {
			if attempt >= maxRetries {
				return "", "", "", nil, 0, 0, 0, &apiCallError{Message: fmt.Sprintf("Stream request failed after %d attempts: %v", attempt, err), RetriesExhausted: true}
			}
			bo := retryBackoff(attempt)
			a.logger.Warn("RETRY", "attempt", "model", a.Model, "attempt", attempt, "backoff_ms", int(bo.Milliseconds()), "error", err.Error())
			if onRetry != nil {
				onRetry(attempt, bo.Seconds())
			}
			select {
			case <-time.After(bo):
			case <-ctx.Done():
				a.logger.Warn("CANCEL", "stream_retry_cancelled", "reason", ctx.Err().Error())
				return "", "", "", nil, 0, 0, 0, &apiCallError{Message: fmt.Sprintf("Stream request cancelled during retry: %v", ctx.Err())}
			}
			continue
		}

		if resp.StatusCode != 200 {
			respBody, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			snippet := string(respBody)
			if isRetryable(resp.StatusCode) {
				if attempt >= maxRetries {
					return "", "", "", nil, 0, 0, 0, &apiCallError{Message: fmt.Sprintf("API %d after %d attempts: %s", resp.StatusCode, attempt, snippet), RetriesExhausted: true}
				}
				bo := retryBackoff(attempt)
				a.logger.Warn("RETRY", "attempt", "model", a.Model, "attempt", attempt, "backoff_ms", int(bo.Milliseconds()), "error", fmt.Errorf("status %d: %s", resp.StatusCode, snippet).Error())
				if onRetry != nil {
					onRetry(attempt, bo.Seconds())
				}
				select {
				case <-time.After(bo):
				case <-ctx.Done():
					a.logger.Warn("CANCEL", "stream_retry_cancelled", "reason", ctx.Err().Error())
					return "", "", "", nil, 0, 0, 0, &apiCallError{Message: fmt.Sprintf("Stream request cancelled during retry: %v", ctx.Err())}
				}
				continue
			}
			a.logger.Error("LLM", "error", "model", a.Model, "error", fmt.Errorf("status %d: %s", resp.StatusCode, snippet).Error(), "latency_ms", time.Since(streamStart).Milliseconds())
			return "", "", "", nil, 0, 0, 0, fmt.Errorf("API %d: %s", resp.StatusCode, snippet)
		}

		var contentBuf strings.Builder
		var reasoningBuf strings.Builder
		finishReason := ""
		promptTokens := 0
		completionTokens := 0
		promptTokensCached := 0
		toolCallsByIndex := make(map[int]*provider.ToolCall)
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 1024*64), 1024*64)

		for scanner.Scan() {
			if ctx.Err() != nil {
				break
			}
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimPrefix(line, "data: ")

			chunk, err := std.DecodeStreamChunk(data)
			if err != nil {
				continue
			}
			if chunk == nil {
				break
			}
			if len(chunk.Choices) == 0 {
				continue
			}

			choice := chunk.Choices[0]
			if choice.FinishReason != "" {
				finishReason = choice.FinishReason
			}

			if chunk.Usage != nil {
				promptTokens = chunk.Usage.PromptTokens
				completionTokens = chunk.Usage.CompletionTokens
				if chunk.Usage.PromptTokensDetails != nil {
					promptTokensCached = chunk.Usage.PromptTokensDetails.CachedTokens
				}
				a.logger.Debug("STREAM", "usage_received", "prompt", promptTokens, "completion", completionTokens, "cached", promptTokensCached)
			}

			reasoningText := choice.Delta.ReasoningContent
			if reasoningText == "" {
				reasoningText = choice.Delta.Reasoning
			}
			if reasoningText != "" {
				reasoningBuf.WriteString(reasoningText)
				if emitThinking != nil {
					emitThinking(reasoningText)
				}
			}

			delta := choice.Delta.Content
			if delta != "" {
				if !firstTokenLogged {
					latencyMs := time.Since(streamStart).Milliseconds()
					a.logger.Debug("STREAM", "first_token", "model", a.Model, "latency_ms", latencyMs)
					firstTokenLogged = true
				}
				contentBuf.WriteString(delta)
				emit(delta)
			}

			for _, tc := range choice.Delta.ToolCalls {
				existing, ok := toolCallsByIndex[tc.Index]
				if !ok {
					a.toolCallSeq++
					internalID := fmt.Sprintf("_tc%d", a.toolCallSeq)
					toolCallsByIndex[tc.Index] = &provider.ToolCall{
						Index:      tc.Index,
						ID:         tc.ID,
						Type:       tc.Type,
						InternalID: internalID,
						Function: provider.FunctionCall{
							Name:      tc.Function.Name,
							Arguments: tc.Function.Arguments,
						},
					}
					if onToolCallStart != nil && tc.Function.Name != "" {
						a.logger.Debug("STREAM", "tool_call_start", "id", tc.ID, "internal_id", internalID, "name", tc.Function.Name)
						onToolCallStart(internalID, tc.ID, tc.Function.Name)
					}
					if onToolCallDelta != nil && tc.Function.Name != "" {
						a.logger.Debug("STREAM", "tool_call_delta", "id", tc.ID, "internal_id", internalID, "name", tc.Function.Name, "args_len", len(tc.Function.Arguments))
						onToolCallDelta(internalID, tc.ID, tc.Function.Name, tc.Function.Arguments)
					}
				} else {
					if tc.ID != "" && existing.ID == "" {
						existing.ID = tc.ID
					}
					if tc.Type != "" && existing.Type == "" {
						existing.Type = tc.Type
					}
					if tc.Function.Name != "" && existing.Function.Name == "" {
						existing.Function.Name = tc.Function.Name
						a.logger.Debug("STREAM", "tool_call_start", "id", existing.ID, "internal_id", existing.InternalID, "name", existing.Function.Name)
						if onToolCallStart != nil {
							onToolCallStart(existing.InternalID, existing.ID, existing.Function.Name)
						}
					}
					existing.Function.Arguments += tc.Function.Arguments
					if onToolCallDelta != nil && existing.Function.Name != "" {
						onToolCallDelta(existing.InternalID, existing.ID, existing.Function.Name, existing.Function.Arguments)
					}
				}
			}
		}
		resp.Body.Close()

		if err := scanner.Err(); err != nil {
			partialContent := contentBuf.String()
			if partialContent != "" {
				a.logger.Warn("STREAM", "scan_error_partial", "model", a.Model, "content_len", len(partialContent), "error", err.Error())
				return partialContent, reasoningBuf.String(), finishReason, nil, promptTokens, completionTokens, promptTokensCached, nil
			}
			if attempt >= maxRetries {
				return "", "", "", nil, 0, 0, 0, &apiCallError{Message: fmt.Sprintf("Stream scan error after %d attempts: %v", attempt, err), RetriesExhausted: true}
			}
			bo := retryBackoff(attempt)
			a.logger.Warn("RETRY", "attempt", "model", a.Model, "attempt", attempt, "backoff_ms", int(bo.Milliseconds()), "error", fmt.Errorf("stream scan error: %w", err).Error())
			if onRetry != nil {
				onRetry(attempt, bo.Seconds())
			}
			select {
			case <-time.After(bo):
			case <-ctx.Done():
				return "", "", "", nil, 0, 0, 0, &apiCallError{Message: fmt.Sprintf("Stream request cancelled during retry: %v", ctx.Err())}
			}
			continue
		}

		var resultToolCalls []provider.ToolCall
		if len(toolCallsByIndex) > 0 {
			maxIdx := 0
			for idx := range toolCallsByIndex {
				if idx > maxIdx {
					maxIdx = idx
				}
			}
			for i := 0; i <= maxIdx; i++ {
				if tc, ok := toolCallsByIndex[i]; ok && tc.Function.Name != "" {
					resultToolCalls = append(resultToolCalls, *tc)
				}
			}
		}

		streamDur := time.Since(streamStart).Milliseconds()
		a.logger.Info("LLM", "response", "model", a.Model, "tool_calls", len(resultToolCalls), "prompt_tokens", promptTokens, "completion_tokens", completionTokens, "cached_tokens", promptTokensCached, "latency_ms", streamDur)

		if a.turn != nil {
			turnToolCalls := make([]logger.TurnToolCall, len(resultToolCalls))
			for i, tc := range resultToolCalls {
				turnToolCalls[i] = logger.TurnToolCall{
					ID:        tc.ID,
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				}
			}
			if turnFile := a.turn.Write(logger.TurnRecord{
				Time:             time.Now(),
				Model:            a.Model,
				LatencyMS:        streamDur,
				PromptTokens:     promptTokens,
				CompletionTokens: completionTokens,
				CachedTokens:     promptTokensCached,
				FinishReason:     finishReason,
				RequestBody:      body,
				ResponseContent:  contentBuf.String(),
				ReasoningContent: reasoningBuf.String(),
				ToolCalls:        turnToolCalls,
			}); turnFile != "" {
				a.logger.Debug("LLM", "turn_dump", "file", turnFile)
			}
		}

		return contentBuf.String(), reasoningBuf.String(), finishReason, resultToolCalls, promptTokens, completionTokens, promptTokensCached, nil
	}
}

func (a *Agent) emitError(emit func(AgentEvent), err error) {
	errMsg := err.Error()
	originalMsg := errMsg
	if strings.Contains(errMsg, "context canceled") || strings.Contains(errMsg, "context cancelled") {
		errMsg = "Request was cancelled"
	} else if strings.Contains(errMsg, "429") {
		errMsg = "Rate limited — please wait a moment and try again"
	} else if strings.Contains(errMsg, " 5") && (strings.Contains(errMsg, "API 5") || strings.Contains(errMsg, "50") || strings.Contains(errMsg, "529")) {
		errMsg = "API server error — please try again"
	} else if idx := strings.Index(errMsg, "API error: "); idx >= 0 {
		errMsg = errMsg[idx+len("API error: "):]
		if len([]rune(errMsg)) > 120 {
			errMsg = string([]rune(errMsg)[:120]) + "…"
		}
	}
	a.logger.Error("LLM", "emit_error", "original", originalMsg, "display", errMsg)
	emit(AgentEvent{Type: "error", Data: errMsg})
}
