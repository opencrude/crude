package tui

import ttypes "crude/tui/types"

// Public API types — type aliases so consumers use tui.Settings etc.
type (
	Settings       = ttypes.Settings
	ModelEntry     = ttypes.ModelEntry
	ProviderConfig = ttypes.ProviderConfig
	ProviderPreset = ttypes.ProviderPreset
	ConfirmResult  = ttypes.ConfirmResult
	UndoResultMsg  = ttypes.UndoResultMsg
)

// ChatMessage is aliased for internal use by old code during migration.
type ChatMessage = ttypes.ChatMessage

// AgentEventMsg is aliased for internal use by old code during migration.
type AgentEventMsg = ttypes.AgentEventMsg

// Const redeclarations (Go has no const alias)
const (
	ToolPreviewPreparing = ttypes.ToolPreviewPreparing
	ToolPreviewStreaming = ttypes.ToolPreviewStreaming
	ToolPreviewDone      = ttypes.ToolPreviewDone
	ToolPreviewError     = ttypes.ToolPreviewError
)

const (
	RoleUser      = ttypes.RoleUser
	RoleAssistant = ttypes.RoleAssistant
	RoleThinking  = ttypes.RoleThinking
	RoleTool      = ttypes.RoleTool
	RoleError     = ttypes.RoleError
)

const (
	ConfirmAllow  = ttypes.ConfirmAllow
	ConfirmAlways = ttypes.ConfirmAlways
	ConfirmDeny   = ttypes.ConfirmDeny
)

// ModelsListMsg, ModelsErrorMsg etc are message types exported for external use.
type (
	ModelsListMsg        = ttypes.ModelsListMsg
	ModelsErrorMsg       = ttypes.ModelsErrorMsg
	OracleModelsListMsg  = ttypes.OracleModelsListMsg
	OracleModelsErrorMsg = ttypes.OracleModelsErrorMsg
)
