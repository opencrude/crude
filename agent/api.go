package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"crude/provider"
)

type apiCallError struct {
	Message          string
	RetriesExhausted bool
}

func (e *apiCallError) Error() string { return e.Message }

func (a *Agent) complete(ctx context.Context, msgs []provider.ChatMessage, tools []provider.ToolDef) (*provider.ChatResponse, *provider.ChatResponse, string, int, int, int, error) {
	req := provider.ChatRequest{
		Model:    a.Model,
		Messages: msgs,
		Tools:    tools,
		Stream:   false,
	}

	std := a.Provider.Standard()

	body, err := std.EncodeRequest(&req)
	if err != nil {
		return nil, nil, "", 0, 0, 0, fmt.Errorf("encode request: %w", err)
	}

	if extra := a.Provider.BuildExtraBody(); len(extra) > 0 {
		var merged map[string]any
		if err := json.Unmarshal(body, &merged); err != nil {
			return nil, nil, "", 0, 0, 0, fmt.Errorf("merge extras: %w", err)
		}
		for k, v := range extra {
			merged[k] = v
		}
		body, err = json.Marshal(merged)
		if err != nil {
			return nil, nil, "", 0, 0, 0, fmt.Errorf("marshal merged: %w", err)
		}
	}

	a.logger.Info("llm.prompt", "model", a.Provider.Name, "msg_count", len(msgs), "tool_count", len(tools))

	client := &http.Client{Timeout: 120 * time.Second}
	endpoint := a.Provider.ChatEndpoint()

	for attempt := 1; ; attempt++ {
		select {
		case <-ctx.Done():
			return nil, nil, "", 0, 0, 0, &apiCallError{Message: fmt.Sprintf("API request cancelled: %v", ctx.Err())}
		default:
		}

		apiStart := time.Now()

		httpReq, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(body))
		if err != nil {
			return nil, nil, "", 0, 0, 0, fmt.Errorf("create request: %w", err)
		}
		hdr, val := a.Provider.Auth()
		httpReq.Header.Set(hdr, val)
		httpReq.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(httpReq)
		if err != nil {
			bo := retryBackoff(attempt)
			a.logger.Warn("llm.retry", "model", a.Provider.Name, "attempt", attempt, "error", err.Error())
			select {
			case <-time.After(bo):
			case <-ctx.Done():
				return nil, nil, "", 0, 0, 0, &apiCallError{Message: fmt.Sprintf("API request cancelled during retry: %v", ctx.Err())}
			}
			continue
		}

		respBody, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			return nil, nil, "", 0, 0, 0, fmt.Errorf("read response: %w", readErr)
		}

		if resp.StatusCode != 200 {
			snippet := string(respBody)
			if len(snippet) > 240 {
				snippet = snippet[:240]
			}
			if isRetryable(resp.StatusCode) {
				bo := retryBackoff(attempt)
				a.logger.Warn("llm.retry", "model", a.Provider.Name, "attempt", attempt, "error", fmt.Errorf("status %d: %s", resp.StatusCode, snippet).Error())
				select {
				case <-time.After(bo):
				case <-ctx.Done():
					return nil, nil, "", 0, 0, 0, &apiCallError{Message: fmt.Sprintf("API request cancelled during retry: %v", ctx.Err())}
				}
				continue
			}
			return nil, nil, "", 0, 0, 0, fmt.Errorf("API %d: %s", resp.StatusCode, string(respBody))
		}

		chatResp, err := std.DecodeResponse(respBody)
		if err != nil {
			return nil, nil, "", 0, 0, 0, fmt.Errorf("decode response: %w", err)
		}
		if chatResp.Error != nil {
			a.logger.Error("llm.error", "model", a.Provider.Name, "error", fmt.Errorf("API error: %s", chatResp.Error.Message).Error(), "latency_ms", time.Since(apiStart).Milliseconds())
			return nil, nil, "", 0, 0, 0, fmt.Errorf("API error: %s", chatResp.Error.Message)
		}
		if len(chatResp.Choices) == 0 {
			return nil, nil, "", 0, 0, 0, fmt.Errorf("empty response from model")
		}

		choice := chatResp.Choices[0]
		apiDur := time.Since(apiStart).Milliseconds()
		promptTokens, completionTokens, promptTokensCached := std.UsageFromResponse(chatResp)
		usageAttrs := []any{}
		if promptTokens > 0 || completionTokens > 0 {
			usageAttrs = append(usageAttrs, "prompt_tokens", promptTokens, "completion_tokens", completionTokens)
		}
		respArgs := []any{"model", a.Provider.Name, "content", choice.Message.Content, "latency_ms", apiDur}
		respArgs = append(respArgs, usageAttrs...)
		a.logger.Info("llm.response", respArgs...)

		return chatResp, nil, choice.FinishReason, promptTokens, completionTokens, promptTokensCached, nil
	}
}

func (a *Agent) completeStream(
	ctx context.Context,
	msgs []provider.ChatMessage,
	tools []provider.ToolDef,
	emit func(token string),
	emitThinking func(text string),
	onToolCallStart func(id, name string),
	onToolCallDelta func(id, name, accumulatedArgs string),
) (content string, reasoningContent string, finishReason string, toolCalls []provider.ToolCall, promptTokens int, completionTokens int, promptTokensCached int, err error) {
	req := provider.ChatRequest{
		Model:    a.Model,
		Messages: msgs,
		Tools:    tools,
		Stream:   true,
	}

	std := a.Provider.Standard()

	body, err := std.EncodeRequest(&req)
	if err != nil {
		return "", "", "", nil, 0, 0, 0, fmt.Errorf("encode request: %w", err)
	}

	if extra := a.Provider.BuildExtraBody(); len(extra) > 0 {
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
	a.logger.Info("llm.prompt", "model", a.Provider.Name, "msg_count", len(msgs), "tool_count", len(tools))

	client := &http.Client{Timeout: 300 * time.Second}
	endpoint := a.Provider.ChatEndpoint()

	for attempt := 1; ; attempt++ {
		select {
		case <-ctx.Done():
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
			bo := retryBackoff(attempt)
			a.logger.Warn("llm.retry", "model", a.Provider.Name, "attempt", attempt, "error", err.Error())
			select {
			case <-time.After(bo):
			case <-ctx.Done():
				return "", "", "", nil, 0, 0, 0, &apiCallError{Message: fmt.Sprintf("Stream request cancelled during retry: %v", ctx.Err())}
			}
			continue
		}

		if resp.StatusCode != 200 {
			respBody, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			snippet := string(respBody)
			if len(snippet) > 240 {
				snippet = snippet[:240]
			}
			if isRetryable(resp.StatusCode) {
				bo := retryBackoff(attempt)
				a.logger.Warn("llm.retry", "model", a.Provider.Name, "attempt", attempt, "error", fmt.Errorf("status %d: %s", resp.StatusCode, snippet).Error())
				select {
				case <-time.After(bo):
				case <-ctx.Done():
					return "", "", "", nil, 0, 0, 0, &apiCallError{Message: fmt.Sprintf("Stream request cancelled during retry: %v", ctx.Err())}
				}
				continue
			}
			a.logger.Error("llm.error", "model", a.Provider.Name, "error", fmt.Errorf("status %d: %s", resp.StatusCode, snippet).Error(), "latency_ms", time.Since(streamStart).Milliseconds())
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
				contentBuf.WriteString(delta)
				emit(delta)
			}

			for _, tc := range choice.Delta.ToolCalls {
				existing, ok := toolCallsByIndex[tc.Index]
				if !ok {
					toolCallsByIndex[tc.Index] = &provider.ToolCall{
						Index: tc.Index,
						ID:    tc.ID,
						Type:  tc.Type,
						Function: provider.FunctionCall{
							Name:      tc.Function.Name,
							Arguments: tc.Function.Arguments,
						},
					}
					if onToolCallStart != nil && tc.Function.Name != "" {
						onToolCallStart(tc.ID, tc.Function.Name)
					}
					if onToolCallDelta != nil && tc.Function.Name != "" {
						onToolCallDelta(tc.ID, tc.Function.Name, tc.Function.Arguments)
					}
			} else {
				existing.Function.Arguments += tc.Function.Arguments
				if onToolCallDelta != nil && existing.Function.Name != "" {
					onToolCallDelta(tc.ID, existing.Function.Name, existing.Function.Arguments)
				}
			}
			}
		}
		resp.Body.Close()

		if err := scanner.Err(); err != nil {
			bo := retryBackoff(attempt)
			a.logger.Warn("llm.retry", "model", a.Provider.Name, "attempt", attempt, "error", fmt.Errorf("stream scan error: %w", err).Error())
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
				if tc, ok := toolCallsByIndex[i]; ok && tc.ID != "" {
					resultToolCalls = append(resultToolCalls, *tc)
				}
			}
		}

		streamDur := time.Since(streamStart).Milliseconds()
		a.logger.Info("llm.response", "model", a.Provider.Name, "content", contentBuf.String(), "tool_calls", len(resultToolCalls), "prompt_tokens", promptTokens, "completion_tokens", completionTokens, "prompt_tokens_cached", promptTokensCached, "latency_ms", streamDur)
		return contentBuf.String(), reasoningBuf.String(), finishReason, resultToolCalls, promptTokens, completionTokens, promptTokensCached, nil
	}
}

func (a *Agent) emitError(emit func(AgentEvent), err error) {
	errMsg := err.Error()
	if strings.Contains(errMsg, "429") {
		errMsg = "Rate limited — please wait a moment and try again"
	} else if strings.Contains(errMsg, "OpenRouter 5") {
		errMsg = "API server error — please try again"
	} else if idx := strings.Index(errMsg, "API error: "); idx >= 0 {
		errMsg = errMsg[idx+len("API error: "):]
		if len(errMsg) > 120 {
			errMsg = errMsg[:120] + "…"
		}
	}
	emit(AgentEvent{Type: "error", Data: errMsg})
}
