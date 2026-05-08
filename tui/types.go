package tui

import "time"

const (
	ToolPreviewPreparing = "preparing"
	ToolPreviewStreaming = "streaming"
	ToolPreviewDone      = "done"
	ToolPreviewError     = "error"
)

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
	ToolStatus string // "streaming", "calling", "success", "error", "skipped"
	ToolIndex  int    // index in messages slice for in-place updates

	// Tool progress for live updates during streaming/execution
	ToolProgressPhase   string
	ToolProgressMessage string
	ToolProgressLabel   string // display target (path, command, url, etc.)

	// Live streaming preview fields
	ToolPreviewPhase string // "", "preparing", "streaming", "done", "error"
	ToolEstTokens    int    // estimated tokens for this tool call (updated live)
	ToolLivedAdded   int    // live added lines (write_file, edit_file)
	ToolLivedRemoved int    // live removed lines (edit_file)
	ToolStartTime    time.Time
	ToolDuration     time.Duration

	// Thinking metadata
	ThinkingActive bool // true while agent is still thinking, false when done

	// Render cache
	cachedLines []string // rendered ANSI lines, nil = needs re-render
	cursorLine  int      // line index within cachedLines for blinking cursor, -1 = none
}

// AgentEventMsg carries an agent lifecycle event to the TUI.
// Type is one of: "phase", "thinking", "stream_token", "tool_call_streaming", "tool_call", "tool_progress", "tool_result", "done", "error", "usage".
type AgentEventMsg struct {
	Type string
	Data any
}

// Settings holds all user-configurable options, passed between TUI and main.
type Settings struct {
	APIKey                   string
	Model                    string
	ModelContextWindow       int
	OracleModel              string
	OracleModelContextWindow int
	ProviderID               string
	ProviderURL              string
	ProviderAPIKey           string
	ThinkingEffort           string
}

// ModelEntry represents a model available on a provider.
type ModelEntry struct {
	ID            string
	Name          string
	ContextWindow int
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
