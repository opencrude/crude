package agent

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"crude/logger"
	"crude/provider"
	"crude/safety"
	"crude/statemachine"
	"crude/tools"
)

type APIMessage struct {
	Role             string     `json:"role"`
	Content          string     `json:"content,omitempty"`
	ReasoningContent string     `json:"reasoning_content,omitempty"`
	ToolCalls        []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string     `json:"tool_call_id,omitempty"`
}

type ToolCall struct {
	Index    int          `json:"index"`
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type ToolDef struct {
	Type     string      `json:"type"`
	Function ToolFuncDef `json:"function"`
}

type ToolFuncDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type ChatRequest struct {
	Model      string       `json:"model"`
	Messages   []APIMessage `json:"messages"`
	Tools      []ToolDef    `json:"tools,omitempty"`
	ToolChoice any          `json:"tool_choice,omitempty"`
	Stream     bool         `json:"stream"`
}

type ChatResponse struct {
	Choices []struct {
		Message      APIMessage `json:"message"`
		FinishReason string     `json:"finish_reason"`
		Delta        struct {
			Content          string     `json:"content"`
			ReasoningContent string     `json:"reasoning_content"`
			ToolCalls        []ToolCall `json:"tool_calls,omitempty"`
		} `json:"delta"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage,omitempty"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type AgentEvent struct {
	Type string
	Data any
}

const (
	maxSteps       = 50
	maxAPIAttempts = 3
)

type Agent struct {
	Provider       *provider.Provider
	Model          string
	Policy         safety.Policy
	ToolFn         func(tool string, params map[string]any) (string, error)
	logger         *logger.Logger
	ExtraBody      map[string]any // provider-specific request fields (e.g. thinking params)
	OracleProvider *provider.Provider
	OracleModel    string
}

// marshalRequest marshals a ChatRequest, merging any ExtraBody fields into the top-level JSON.
func (a *Agent) marshalRequest(req ChatRequest) ([]byte, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	if len(a.ExtraBody) == 0 {
		return body, nil
	}
	var merged map[string]any
	if err := json.Unmarshal(body, &merged); err != nil {
		return nil, err
	}
	for k, v := range a.ExtraBody {
		merged[k] = v
	}
	return json.Marshal(merged)
}

func NewAgent(p *provider.Provider, model string, lg *logger.Logger) *Agent {
	if model == "" {
		model = "google/gemini-2.0-flash-exp:free"
	}
	return &Agent{
		Provider: p,
		Model:    model,
		ToolFn:   func(tool string, params map[string]any) (string, error) { return "", nil },
		logger:   lg,
	}
}

func (a *Agent) Run(
	sessionMessages []APIMessage,
	systemPrompt string,
	emit func(AgentEvent),
	blockResolveCh <-chan string,
	sm *statemachine.ConversationStateMachine,
	sessionID string,
) {
	allTools := allToolDefs()
	userText := extractUserMessage(sessionMessages)

	lastReply := ""
	msgs := []APIMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userText},
	}

	for step := 0; step < maxSteps; step++ {
		var accumulatedText string
		var toolCalls []ToolCall

		content, reasoningContent, finishReason, streamToolCalls, promptTokens, completionTokens, err := a.completeStream(msgs, allTools,
			func(token string) {
				emit(AgentEvent{Type: "stream_token", Data: token})
			},
			func(text string) {
				emit(AgentEvent{Type: "thinking", Data: text})
			},
		)
		if err != nil {
			a.emitError(emit, err)
			return
		}

		if promptTokens > 0 || completionTokens > 0 {
			emit(AgentEvent{Type: "usage", Data: map[string]int{
				"prompt":     promptTokens,
				"completion": completionTokens,
			}})
		}

		accumulatedText = content
		toolCalls = streamToolCalls

		a.logger.Info("llm.step_done", "text_len", len(accumulatedText), "tool_calls", len(toolCalls), "finish_reason", finishReason)

		if accumulatedText == "" {
			accumulatedText = lastReply
		}

		if len(toolCalls) == 0 {
			if accumulatedText != "" {
				emit(AgentEvent{Type: "done", Data: accumulatedText})
			} else {
				emit(AgentEvent{Type: "done", Data: "Done."})
			}
			return
		}

		// Build the assistant message for the history
		assistantMsg := APIMessage{Role: "assistant", Content: accumulatedText, ReasoningContent: reasoningContent, ToolCalls: toolCalls}
		msgs = append(msgs, assistantMsg)

		type execResult struct {
			id     string
			result string
		}
		resultCh := make(chan execResult, len(toolCalls))

		for _, tc := range toolCalls {
			go func(tc ToolCall) {
				var params map[string]any
				if err := json.Unmarshal([]byte(tc.Function.Arguments), &params); err != nil {
					resultCh <- execResult{tc.ID, "Error: invalid arguments: " + err.Error()}
					return
				}

				if tc.Function.Name == "reply" {
					content := getStr(params, "content")
					lastReply = content
					resultCh <- execResult{tc.ID, content}
					return
				}

				emit(AgentEvent{Type: "tool_call", Data: map[string]string{
					"name": tc.Function.Name,
					"args": fmt.Sprintf("%v", params),
				}})

				a.logger.Info("tool.call", "tool", tc.Function.Name, "params", fmt.Sprintf("%v", params))
				toolStart := time.Now()
				result, _ := a.ToolFn(tc.Function.Name, params)
				toolDur := time.Since(toolStart).Milliseconds()

				if strings.HasPrefix(result, "Error:") {
					a.logger.Error("tool.error", "tool", tc.Function.Name, "error", result, "latency_ms", toolDur)
				} else {
					a.logger.Info("tool.result", "tool", tc.Function.Name, "latency_ms", toolDur, "content", result)
				}

				emit(AgentEvent{Type: "tool_result", Data: map[string]string{
					"name":   tc.Function.Name,
					"result": result,
				}})

				resultCh <- execResult{tc.ID, result}
			}(tc)
		}

		for i := 0; i < len(toolCalls); i++ {
			r := <-resultCh
			msgs = append(msgs, APIMessage{
				Role:       "tool",
				ToolCallID: r.id,
				Content:    r.result,
			})
		}
	}

	emit(AgentEvent{Type: "done", Data: "Reached maximum " + fmt.Sprint(maxSteps) + " steps. Task may be incomplete."})
}

func getStr(params map[string]any, key string) string {
	if v, ok := params[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
		return fmt.Sprintf("%v", v)
	}
	return ""
}

func isRetryable(code int) bool {
	return code == http.StatusTooManyRequests || code >= 500
}

func retryBackoff(attempt int) time.Duration {
	if attempt <= 1 {
		return time.Second
	}
	return time.Duration(1<<(attempt-1)) * time.Second
}

func extractUserMessage(msgs []APIMessage) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "user" {
			return msgs[i].Content
		}
	}
	return ""
}

func allToolDefs() []ToolDef {
	result := make([]ToolDef, len(tools.AgentTools))
	for i, td := range tools.AgentTools {
		result[i] = ToolDef{
			Type: td.Type,
			Function: ToolFuncDef{
				Name:        td.Function.Name,
				Description: td.Function.Description,
				Parameters:  td.Function.Parameters,
			},
		}
	}
	return result
}
