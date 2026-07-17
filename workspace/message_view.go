package workspace

import "time"

// ChatMessageView is the presentation-neutral snapshot used to hydrate a
// persisted conversation. Runtime rendering caches and selection state belong
// to the frontend and are created when this view crosses into the TUI.
type ChatMessageView struct {
	Role    string
	Content string
	Time    time.Time

	ToolName          string
	ToolArgs          string
	ToolID            string
	ToolStatus        string
	ToolProgressLabel string
	ToolPreviewPhase  string
	ToolEstTokens     int
	ToolLinesAdded    int
	ToolLinesRemoved  int
	ToolDuration      time.Duration

	SubPaneID string
	PermTag   string
}
