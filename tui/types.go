package tui

import "time"

// ChatRole constants for ChatMessage.
const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleThinking  = "thinking"
	RoleTool      = "tool"
	RoleError     = "error"
)

// ChatMessage is a single entry in the conversation display.
type ChatMessage struct {
	Role    string
	Content string
	Time    time.Time

	// Tool metadata for RoleTool messages
	ToolName   string
	ToolArgs   string
	ToolStatus string // "calling", "success", "error", "skipped"
	ToolIndex  int    // index in messages slice for in-place updates

	// Thinking metadata
	ThinkingActive bool // true while agent is still thinking, false when done

	// Render cache
	cachedLines []string // rendered ANSI lines, nil = needs re-render
	cursorLine  int      // line index within cachedLines for blinking cursor, -1 = none
}

// AgentEventMsg carries an agent lifecycle event to the TUI.
// Type is one of: "phase", "thinking", "tool_call", "tool_result", "done", "error", "usage".
type AgentEventMsg struct {
	Type string
	Data any
}

// Settings holds all user-configurable options, passed between TUI and main.
type Settings struct {
	APIKey         string
	Model          string
	OracleModel    string
	ProviderID     string
	ProviderURL    string
	ProviderAPIKey string
	ThinkingEffort string
}

// ModelEntry represents a model available on OpenRouter.
type ModelEntry struct {
	ID   string
	Name string
}

// Internal Bubble Tea messages

type tickMsg time.Time
type statusMsg string
type errorMsg string

// agentMsg wraps an agent event arriving from an external goroutine.
type agentMsg AgentEventMsg

// openSettingsMsg triggers inline settings mode.
type openSettingsMsg struct{}

// fetchModelsMsg asks the model layer to fetch models for a specific provider.
type fetchModelsMsg struct {
	ProviderID string
	BaseURL    string
	APIKey     string
}

// saveSettingsMsg carries new settings back from the inline form.
type saveSettingsMsg Settings

// updateModelMsg updates the model name displayed in the header.
type updateModelMsg string

// ModelsListMsg delivers fetched models from OpenRouter.
type ModelsListMsg []ModelEntry

// ModelsErrorMsg reports a model fetch failure.
type ModelsErrorMsg string

// OracleModelsListMsg delivers fetched oracle models.
type OracleModelsListMsg []ModelEntry

// OracleModelsErrorMsg reports an oracle model fetch failure.
type OracleModelsErrorMsg string

// selectionTickMsg is sent by the auto-scroll timer during text selection.
type selectionTickMsg time.Time

// clipboardCopiedMsg confirms clipboard copy succeeded.
type clipboardCopiedMsg struct{}

// clipboardErrorMsg reports a clipboard copy failure.
type clipboardErrorMsg string

// UndoResultMsg carries the result of an undo operation back to the TUI.
type UndoResultMsg struct {
	Desc  string
	Error string
}
