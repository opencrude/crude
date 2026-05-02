package agent

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type usageInfo struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

type apiCallError struct {
	Message          string
	RetriesExhausted bool
}

func (e *apiCallError) Error() string { return e.Message }

func (a *Agent) complete(msgs []APIMessage, tools []ToolDef) (*struct {
	Message      APIMessage
	FinishReason string
	Usage        *struct{ PromptTokens, CompletionTokens int }
}, error,
) {
	req := ChatRequest{
		Model:    a.Model,
		Messages: msgs,
		Tools:    tools,
		Stream:   false,
	}
	body, err := a.marshalRequest(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	msgLog := make([]map[string]any, len(msgs))
	for i, m := range msgs {
		msgLog[i] = map[string]any{"role": m.Role, "content": m.Content}
	}
	toolDefs := make([]map[string]any, len(tools))
	for i, t := range tools {
		toolDefs[i] = map[string]any{"name": t.Function.Name}
	}
	a.logger.Info("llm.prompt", "model", a.Provider.Name, "msg_count", len(msgs), "tool_count", len(toolDefs))

	client := &http.Client{Timeout: 120 * time.Second}

	for attempt := 1; attempt <= maxAPIAttempts; attempt++ {
		apiStart := time.Now()

		httpReq, err := http.NewRequest("POST", a.Provider.ChatEndpoint(), bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}
		httpReq.Header.Set(a.Provider.AuthHeader, a.Provider.AuthValue())
		httpReq.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(httpReq)
		if err != nil {
			if attempt < maxAPIAttempts {
				bo := retryBackoff(attempt)
				a.logger.Warn("llm.retry", "model", a.Provider.Name, "attempt", attempt, "error", err.Error())
				time.Sleep(bo)
				continue
			}
			a.logger.Error("llm.error", "model", a.Provider.Name, "error", err.Error(), "latency_ms", time.Since(apiStart).Milliseconds())
			return nil, &apiCallError{
				Message:          fmt.Sprintf("API request failed after %d attempts: %v", maxAPIAttempts, err),
				RetriesExhausted: true,
			}
		}

		respBody, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read response: %w", readErr)
		}

		if resp.StatusCode != 200 {
			snippet := string(respBody)
			if len(snippet) > 240 {
				snippet = snippet[:240]
			}
			if isRetryable(resp.StatusCode) && attempt < maxAPIAttempts {
				bo := retryBackoff(attempt)
				a.logger.Warn("llm.retry", "model", a.Provider.Name, "attempt", attempt, "error", fmt.Errorf("status %d: %s", resp.StatusCode, snippet).Error())
				time.Sleep(bo)
				continue
			}
			if isRetryable(resp.StatusCode) {
				a.logger.Error("llm.error", "model", a.Provider.Name, "error", fmt.Errorf("status %d: %s", resp.StatusCode, snippet).Error(), "latency_ms", time.Since(apiStart).Milliseconds())
				return nil, &apiCallError{
					Message:          fmt.Sprintf("OpenRouter %d after %d attempts: %s", resp.StatusCode, maxAPIAttempts, snippet),
					RetriesExhausted: true,
				}
			}
			return nil, fmt.Errorf("OpenRouter %d: %s", resp.StatusCode, string(respBody))
		}

		var chatResp ChatResponse
		if err := json.Unmarshal(respBody, &chatResp); err != nil {
			return nil, fmt.Errorf("unmarshal response: %w", err)
		}
		if chatResp.Error != nil {
			a.logger.Error("llm.error", "model", a.Provider.Name, "error", fmt.Errorf("API error: %s", chatResp.Error.Message).Error(), "latency_ms", time.Since(apiStart).Milliseconds())
			return nil, fmt.Errorf("API error: %s", chatResp.Error.Message)
		}
		if len(chatResp.Choices) == 0 {
			return nil, fmt.Errorf("empty response from model")
		}

		choice := chatResp.Choices[0]
		apiDur := time.Since(apiStart).Milliseconds()
		var usage *usageInfo
		if chatResp.Usage != nil {
			usage = &usageInfo{
				PromptTokens:     chatResp.Usage.PromptTokens,
				CompletionTokens: chatResp.Usage.CompletionTokens,
			}
		}
		usageAttrs := []any{}
		if usage != nil {
			usageAttrs = append(usageAttrs, "prompt_tokens", usage.PromptTokens, "completion_tokens", usage.CompletionTokens)
		}
		respArgs := []any{"model", a.Provider.Name, "content", choice.Message.Content, "latency_ms", apiDur}
		respArgs = append(respArgs, usageAttrs...)
		a.logger.Info("llm.response", respArgs...)

		var resultUsage *struct{ PromptTokens, CompletionTokens int }
		if chatResp.Usage != nil {
			resultUsage = &struct{ PromptTokens, CompletionTokens int }{
				PromptTokens:     chatResp.Usage.PromptTokens,
				CompletionTokens: chatResp.Usage.CompletionTokens,
			}
		}

		return &struct {
			Message      APIMessage
			FinishReason string
			Usage        *struct{ PromptTokens, CompletionTokens int }
		}{
			Message:      choice.Message,
			FinishReason: choice.FinishReason,
			Usage:        resultUsage,
		}, nil
	}

	return nil, &apiCallError{Message: "API request retries exhausted", RetriesExhausted: true}
}

func (a *Agent) completeStream(
	msgs []APIMessage,
	tools []ToolDef,
	emit func(token string),
	emitThinking func(text string),
) (content string, reasoningContent string, finishReason string, toolCalls []ToolCall, promptTokens int, completionTokens int, err error) {
	req := ChatRequest{
		Model:    a.Model,
		Messages: msgs,
		Tools:    tools,
		Stream:   true,
	}
	body, err := a.marshalRequest(req)
	if err != nil {
		return "", "", "", nil, 0, 0, fmt.Errorf("marshal request: %w", err)
	}

	streamStart := time.Now()
	msgLog := make([]map[string]any, len(msgs))
	for i, m := range msgs {
		msgLog[i] = map[string]any{"role": m.Role, "content": m.Content}
	}
	toolDefs := make([]map[string]any, len(tools))
	for i, t := range tools {
		toolDefs[i] = map[string]any{"name": t.Function.Name}
	}
	a.logger.Info("llm.prompt", "model", a.Provider.Name, "msg_count", len(msgs), "tool_count", len(toolDefs))
	client := &http.Client{Timeout: 300 * time.Second}

	for attempt := 1; attempt <= maxAPIAttempts; attempt++ {
		httpReq, err := http.NewRequest("POST", a.Provider.ChatEndpoint(), bytes.NewReader(body))
		if err != nil {
			return "", "", "", nil, 0, 0, fmt.Errorf("create request: %w", err)
		}
		httpReq.Header.Set(a.Provider.AuthHeader, a.Provider.AuthValue())
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Accept", "text/event-stream")

		resp, err := client.Do(httpReq)
		if err != nil {
			if attempt < maxAPIAttempts {
				bo := retryBackoff(attempt)
				a.logger.Warn("llm.retry", "model", a.Provider.Name, "attempt", attempt, "error", err.Error())
				time.Sleep(bo)
				continue
			}
			a.logger.Error("llm.error", "model", a.Provider.Name, "error", err.Error(), "latency_ms", time.Since(streamStart).Milliseconds())
			return "", "", "", nil, 0, 0, &apiCallError{
				Message:          fmt.Sprintf("Stream request failed after %d attempts: %v", maxAPIAttempts, err),
				RetriesExhausted: true,
			}
		}

		if resp.StatusCode != 200 {
			respBody, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			snippet := string(respBody)
			if len(snippet) > 240 {
				snippet = snippet[:240]
			}
			if isRetryable(resp.StatusCode) && attempt < maxAPIAttempts {
				bo := retryBackoff(attempt)
				a.logger.Warn("llm.retry", "model", a.Provider.Name, "attempt", attempt, "error", fmt.Errorf("status %d: %s", resp.StatusCode, snippet).Error())
				time.Sleep(bo)
				continue
			}
			a.logger.Error("llm.error", "model", a.Provider.Name, "error", fmt.Errorf("status %d: %s", resp.StatusCode, snippet).Error(), "latency_ms", time.Since(streamStart).Milliseconds())
			return "", "", "", nil, 0, 0, fmt.Errorf("OpenRouter %d: %s", resp.StatusCode, snippet)
		}

		var content strings.Builder
		var reasoningBuilder strings.Builder
		finishReason := ""
		promptTokens := 0
		completionTokens := 0
		toolCallsByIndex := make(map[int]*ToolCall)
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 1024*64), 1024*64)

		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimPrefix(line, "data: ")
			if data == "[DONE]" {
				break
			}

			var chunk ChatResponse
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				continue
			}
			if len(chunk.Choices) == 0 {
				continue
			}

			choice := chunk.Choices[0]
			if choice.FinishReason != "" {
				finishReason = choice.FinishReason
			}

			// Track usage from streaming chunks
			if chunk.Usage != nil {
				promptTokens = chunk.Usage.PromptTokens
				completionTokens = chunk.Usage.CompletionTokens
			}

			reasoning := choice.Delta.ReasoningContent
			if reasoning != "" {
				reasoningBuilder.WriteString(reasoning)
				if emitThinking != nil {
					emitThinking(reasoning)
				}
			}

			delta := choice.Delta.Content
			if delta != "" {
				content.WriteString(delta)
				emit(delta)
			}

			// Accumulate tool calls from delta
			for _, tc := range choice.Delta.ToolCalls {
				existing, ok := toolCallsByIndex[tc.Index]
				if !ok {
					toolCallsByIndex[tc.Index] = &ToolCall{
						Index: tc.Index,
						ID:    tc.ID,
						Type:  tc.Type,
						Function: FunctionCall{
							Name:      tc.Function.Name,
							Arguments: tc.Function.Arguments,
						},
					}
				} else {
					existing.Function.Arguments += tc.Function.Arguments
				}
			}
		}
		resp.Body.Close()

		if err := scanner.Err(); err != nil {
			if attempt < maxAPIAttempts {
				bo := retryBackoff(attempt)
				a.logger.Warn("llm.retry", "model", a.Provider.Name, "attempt", attempt, "error", fmt.Errorf("stream scan error: %w", err).Error())
				time.Sleep(bo)
				continue
			}
			a.logger.Error("llm.error", "model", a.Provider.Name, "error", fmt.Errorf("stream read error: %w", err).Error(), "latency_ms", time.Since(streamStart).Milliseconds())
			return "", "", "", nil, 0, 0, fmt.Errorf("stream read error: %w", err)
		}

		// Collect accumulated tool calls (sorted by index)
		var resultToolCalls []ToolCall
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
		a.logger.Info("llm.response", "model", a.Provider.Name, "content", content.String(), "tool_calls", len(resultToolCalls), "prompt_tokens", promptTokens, "completion_tokens", completionTokens, "latency_ms", streamDur)
		return content.String(), reasoningBuilder.String(), finishReason, resultToolCalls, promptTokens, completionTokens, nil
	}

	return "", "", "", nil, 0, 0, &apiCallError{Message: "Stream request retries exhausted", RetriesExhausted: true}
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
