package viewmodel

import (
	ttypes "crude/tui/types"
	"crude/workspace"
)

// ChatMessagesFromViews projects workspace hydration data into frontend-owned
// message objects. Rendering caches intentionally start empty.
func ChatMessagesFromViews(views []workspace.ChatMessageView) []ttypes.ChatMessage {
	messages := make([]ttypes.ChatMessage, 0, len(views))
	for _, view := range views {
		messages = append(messages, ttypes.ChatMessage{
			Role:              view.Role,
			Content:           view.Content,
			Time:              view.Time,
			ToolName:          view.ToolName,
			ToolArgs:          view.ToolArgs,
			ToolID:            view.ToolID,
			ToolStatus:        view.ToolStatus,
			ToolProgressLabel: view.ToolProgressLabel,
			ToolPreviewPhase:  view.ToolPreviewPhase,
			ToolEstTokens:     view.ToolEstTokens,
			ToolLivedAdded:    view.ToolLinesAdded,
			ToolLivedRemoved:  view.ToolLinesRemoved,
			ToolDuration:      view.ToolDuration,
			SubPaneID:         view.SubPaneID,
			PermTag:           view.PermTag,
		})
	}
	return messages
}
