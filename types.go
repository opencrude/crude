package main

import "encoding/json"

// ──────────────────────────── API types ────────────────────────────

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

// ──────────────────────────── Agent event ────────────────────────────

// AgentEvent is emitted by Agent.Run during a conversation turn.
// Type is one of: "phase", "thinking", "tool_call", "tool_result", "done", "error", "usage".
type AgentEvent struct {
	Type string
	Data any
}
