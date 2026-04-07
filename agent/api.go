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

// apiCallError is returned when the API call fails after all retries.
type apiCallError struct {
	Message          string
	RetriesExhausted bool
}

func (e *apiCallError) Error() string { return e.Message }

// complete sends one request to OpenRouter and returns the first choice.
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
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	logAgent("calling %s model=%s msgs=%d tools=%d", a.Provider.Name, a.Model, len(msgs), len(tools))
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
				logAgent("request failed attempt=%d/%d err=%v retrying in %s", attempt, maxAPIAttempts, err, bo)
				time.Sleep(bo)
				continue
			}
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
			snippet := truncate(string(respBody), 240)
			if isRetryable(resp.StatusCode) && attempt < maxAPIAttempts {
				bo := retryBackoff(attempt)
				logAgent("retryable status=%d attempt=%d/%d retrying in %s body=%s",
					resp.StatusCode, attempt, maxAPIAttempts, bo, snippet)
				time.Sleep(bo)
				continue
			}
			if isRetryable(resp.StatusCode) {
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
			return nil, fmt.Errorf("API error: %s", chatResp.Error.Message)
		}
		if len(chatResp.Choices) == 0 {
			return nil, fmt.Errorf("empty response from model")
		}

		choice := chatResp.Choices[0]
		logAgent("response  status=%d took=%s finish=%s tools=%d content=%s",
			resp.StatusCode, time.Since(apiStart).Round(time.Millisecond),
			choice.FinishReason, len(choice.Message.ToolCalls),
			truncate(choice.Message.Content, 80))

		var usage *struct{ PromptTokens, CompletionTokens int }
		if chatResp.Usage != nil {
			usage = &struct{ PromptTokens, CompletionTokens int }{
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
			Usage:        usage,
		}, nil
	}

	return nil, &apiCallError{Message: "API request retries exhausted", RetriesExhausted: true}
}

// completeStream sends a streaming request to the API and calls emit for each token chunk.
// Returns the full accumulated content, finish reason, and any error.
func (a *Agent) completeStream(
	msgs []APIMessage,
	tools []ToolDef,
	emit func(token string),
	emitThinking func(text string),
) (string, string, error) {
	req := ChatRequest{
		Model:    a.Model,
		Messages: msgs,
		Tools:    tools,
		Stream:   true,
	}
	body, err := json.Marshal(req)
	if err != nil {
		return "", "", fmt.Errorf("marshal request: %w", err)
	}

	logAgent("calling %s model=%s (streaming) msgs=%d tools=%d", a.Provider.Name, a.Model, len(msgs), len(tools))
	client := &http.Client{Timeout: 300 * time.Second}

	for attempt := 1; attempt <= maxAPIAttempts; attempt++ {
		httpReq, err := http.NewRequest("POST", a.Provider.ChatEndpoint(), bytes.NewReader(body))
		if err != nil {
			return "", "", fmt.Errorf("create request: %w", err)
		}
		httpReq.Header.Set(a.Provider.AuthHeader, a.Provider.AuthValue())
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Accept", "text/event-stream")

		resp, err := client.Do(httpReq)
		if err != nil {
			if attempt < maxAPIAttempts {
				bo := retryBackoff(attempt)
				logAgent("stream request failed attempt=%d/%d err=%v retrying in %s", attempt, maxAPIAttempts, err, bo)
				time.Sleep(bo)
				continue
			}
			return "", "", &apiCallError{
				Message:          fmt.Sprintf("Stream request failed after %d attempts: %v", maxAPIAttempts, err),
				RetriesExhausted: true,
			}
		}

		if resp.StatusCode != 200 {
			respBody, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			snippet := truncate(string(respBody), 240)
			if isRetryable(resp.StatusCode) && attempt < maxAPIAttempts {
				bo := retryBackoff(attempt)
				logAgent("retryable status=%d attempt=%d/%d retrying in %s body=%s",
					resp.StatusCode, attempt, maxAPIAttempts, bo, snippet)
				time.Sleep(bo)
				continue
			}
			return "", "", fmt.Errorf("OpenRouter %d: %s", resp.StatusCode, snippet)
		}

		var content strings.Builder
		finishReason := ""
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

			reasoning := choice.Delta.ReasoningContent
			if reasoning != "" && emitThinking != nil {
				content.WriteString(reasoning)
				emitThinking(reasoning)
			}

			delta := choice.Delta.Content
			if delta != "" {
				content.WriteString(delta)
				emit(delta)
			}
		}
		resp.Body.Close()

		if err := scanner.Err(); err != nil {
			if attempt < maxAPIAttempts {
				bo := retryBackoff(attempt)
				logAgent("stream scan failed attempt=%d/%d err=%v retrying in %s", attempt, maxAPIAttempts, err, bo)
				time.Sleep(bo)
				continue
			}
			return "", "", fmt.Errorf("stream read error: %w", err)
		}

		logAgent("stream response finish=%s content_len=%d", finishReason, content.Len())
		return content.String(), finishReason, nil
	}

	return "", "", &apiCallError{Message: "Stream request retries exhausted", RetriesExhausted: true}
}

// emitError emits a user-friendly error event.
func (a *Agent) emitError(emit func(AgentEvent), err error) {
	errMsg := err.Error()
	// Clean up API errors — extract short readable message
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
