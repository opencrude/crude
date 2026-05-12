package chat

import (
	"time"

	ttypes "crude/tui/types"
)

const (
	RoleUser      = ttypes.RoleUser
	RoleAssistant = ttypes.RoleAssistant
	RoleThinking  = ttypes.RoleThinking
	RoleTool      = ttypes.RoleTool
	RoleError     = ttypes.RoleError
)

type ChatMessage struct {
	Role    string
	Content string
	Time    time.Time

	ToolName   string
	ToolArgs   string
	ToolID     string
	ToolStatus string
	ToolIndex  int

	ToolProgressPhase   string
	ToolProgressMessage string
	ToolProgressLabel   string

	ToolPreviewPhase string
	ToolEstTokens    int
	ToolLivedAdded   int
	ToolLivedRemoved int
	ToolStartTime    time.Time
	ToolDuration     time.Duration

	ThinkingActive bool

	CachedLines []string
	CursorLine  int
}
