package tui

import (
	ttypes "crude/tui/types"
)

type (
	Settings       = ttypes.Settings
	ModelEntry     = ttypes.ModelEntry
	ProviderPreset = ttypes.ProviderPreset
	ConfirmResult  = ttypes.ConfirmResult
)

type ChatMessage = ttypes.ChatMessage

type AgentEventMsg = ttypes.AgentEventMsg

const (
	ToolPreviewPreparing = ttypes.ToolPreviewPreparing
	ToolPreviewStreaming = ttypes.ToolPreviewStreaming
	ToolPreviewDone      = ttypes.ToolPreviewDone
	ToolPreviewError     = ttypes.ToolPreviewError
)

const (
	RoleUser          = ttypes.RoleUser
	RoleAssistant     = ttypes.RoleAssistant
	RoleThinking      = ttypes.RoleThinking
	RoleTool          = ttypes.RoleTool
	RoleError         = ttypes.RoleError
	RoleStatus        = ttypes.RoleStatus
	RoleSessionPicker = ttypes.RoleSessionPicker
)

const (
	ConfirmAllow  = ttypes.ConfirmAllow
	ConfirmAlways = ttypes.ConfirmAlways
	ConfirmDeny   = ttypes.ConfirmDeny
)

type (
	ModelsListMsg  = ttypes.ModelsListMsg
	ModelsErrorMsg = ttypes.ModelsErrorMsg
)
