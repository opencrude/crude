package agent

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"crude/dag"
	"crude/logging"
	"crude/provider"
	"crude/rag"
	"crude/safety"
	"crude/statemachine"
)

// API types

// APIMessage is a single message in the OpenRouter chat format.
type APIMessage struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

// ToolCall is a function invocation requested by the model.
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

// FunctionCall holds the name and JSON-encoded arguments of a tool call.
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ToolDef describes a tool available to the model.
type ToolDef struct {
	Type     string      `json:"type"`
	Function ToolFuncDef `json:"function"`
}

// ToolFuncDef is the function schema within a tool definition.
type ToolFuncDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// ChatRequest is the request body for the OpenRouter completions endpoint.
type ChatRequest struct {
	Model    string       `json:"model"`
	Messages []APIMessage `json:"messages"`
	Tools    []ToolDef    `json:"tools,omitempty"`
	Stream   bool         `json:"stream"`
}

// ChatResponse is the response body from the OpenRouter completions endpoint.
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

// Agent event

// AgentEvent is emitted by Agent.Run during a conversation turn.
// Type is one of: "phase", "thinking", "tool_call", "tool_result", "done", "error", "usage", "blocking", "stream_token".
type AgentEvent struct {
	Type string
	Data any
}

// Agent

const (
	maxIterations    = 30
	maxGroundingIter = 10
	maxAPIAttempts   = 3
)

// Agent calls an LLM provider with a three-phase loop: Ground → Plan → Execute.
type Agent struct {
	Provider        *provider.Provider
	Model           string
	Policy          safety.Policy
	AuditLogger     *safety.AuditLogger
	RollbackHandler *safety.RollbackHandler
	ToolFn          func(tool string, params map[string]any) (string, error)
	Logger          *logging.InteractionLogger
}

// NewAgent creates an Agent.
func NewAgent(p *provider.Provider, model string) *Agent {
	if model == "" {
		model = "google/gemini-2.0-flash-exp:free"
	}
	return &Agent{
		Provider: p,
		Model:    model,
		Policy:   safety.DefaultPolicy(),
	}
}

// Run executes the three-phase agentic loop.
func (a *Agent) Run(
	sessionMessages []APIMessage,
	systemPrompt string,
	emit func(AgentEvent),
	ragEngine *rag.Engine,
	blockResolveCh <-chan string,
	sm *statemachine.ConversationStateMachine,
	sessionID string,
) {
	// Check for chitchat
	lastUserMsg := ""
	for i := len(sessionMessages) - 1; i >= 0; i-- {
		if sessionMessages[i].Role == "user" {
			lastUserMsg = sessionMessages[i].Content
			break
		}
	}

	if isChitchat(lastUserMsg) {
		if a.Logger != nil {
			a.Logger.LogPhaseStart("chitchat", map[string]any{"session_id": sessionID})
		}
		emit(AgentEvent{Type: "phase", Data: "replying"})
		reply := a.directReply(sessionMessages, systemPrompt, emit)
		if reply != "" {
			emit(AgentEvent{Type: "done", Data: reply})
			if a.Logger != nil {
				a.Logger.LogPhaseEnd("chitchat", 0)
			}
			return
		}
		// Fallback to full pipeline if direct reply fails.
	}

	// Phase 1: GROUNDING
	groundingStart := time.Now()
	if a.Logger != nil {
		a.Logger.LogPhaseStart("grounding", map[string]any{"session_id": sessionID})
	}
	emit(AgentEvent{Type: "phase", Data: "grounding"})
	observations := a.ground(sessionMessages, systemPrompt, emit, ragEngine)
	if a.Logger != nil {
		a.Logger.LogPhaseEnd("grounding", time.Since(groundingStart).Milliseconds())
	}

	// Phase 2: PLANNING
	planningStart := time.Now()
	if a.Logger != nil {
		a.Logger.LogPhaseStart("planning", map[string]any{"session_id": sessionID})
	}
	emit(AgentEvent{Type: "phase", Data: "planning"})
	execDAG := a.plan(sessionMessages, systemPrompt, observations, emit, ragEngine)
	if execDAG == nil {
		return
	}
	if a.Logger != nil {
		a.Logger.LogPhaseEnd("planning", time.Since(planningStart).Milliseconds())
	}

	// Phase 3: EXECUTION
	execStart := time.Now()
	if a.Logger != nil {
		a.Logger.LogPhaseStart("executing", map[string]any{"session_id": sessionID, "dag_nodes": len(execDAG.Nodes)})
	}
	emit(AgentEvent{Type: "phase", Data: "executing"})
	a.execute(execDAG, emit, blockResolveCh, sm, sessionID)
	if a.Logger != nil {
		a.Logger.LogPhaseEnd("executing", time.Since(execStart).Milliseconds())
	}

	// Check for blocking node response
	var blockingResponse string
	for _, n := range execDAG.Nodes {
		if n.Tool == "blocking" && n.Status == dag.Successful && n.Result != "" {
			blockingResponse = n.Result
			break
		}
	}

	if blockingResponse != "" {
		emit(AgentEvent{Type: "done", Data: blockingResponse})
		return
	}

	// Check for reply node
	for _, n := range execDAG.Nodes {
		if n.Tool == "reply" && n.Status == dag.Successful && n.Result != "" {
			emit(AgentEvent{Type: "done", Data: n.Result})
			return
		}
	}

	// Phase 4: SUMMARY
	summaryStart := time.Now()
	if a.Logger != nil {
		a.Logger.LogPhaseStart("summarizing", map[string]any{"session_id": sessionID})
	}
	summary := a.summarize(sessionMessages, execDAG, systemPrompt, emit)
	if a.Logger != nil {
		a.Logger.LogPhaseEnd("summarizing", time.Since(summaryStart).Milliseconds())
	}
	emit(AgentEvent{Type: "done", Data: summary})
}

// Helpers

// isChitchat returns true if the message is a simple greeting or casual remark
// that doesn't require system grounding or tool execution.
func isChitchat(msg string) bool {
	lower := strings.ToLower(strings.TrimSpace(msg))
	if len(lower) == 0 {
		return false
	}
	// Exact short matches
	if len(lower) <= 4 {
		switch lower {
		case "hi", "hey", "yo", "sup", "ok", "ty", "yes", "no", "nope", "yep", "bye", "cya":
			return true
		}
	}
	for _, prefix := range chitchatPrefixes {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

// chitchatPrefixes are greeting/casual message prefixes that don't need grounding.
var chitchatPrefixes = []string{
	"hello", "hi ", "hey", "greetings", "good morning", "good afternoon",
	"good evening", "howdy", "sup", "yo ", "what's up", "whats up",
	"how are you", "how r u", "hru", "thanks", "thank you", "ty ",
	"ok", "okay", "sure", "yes", "no ", "nope", "yep", "yeah",
	"bye", "goodbye", "see you", "cya", "later",
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

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…[truncated]"
}

func logAgent(format string, args ...any) {
	log.Printf("[agent] "+format, args...)
}
