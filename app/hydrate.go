package app

import (
	"time"

	"crude/sessiondb"
	"crude/tui/tools"
	ttypes "crude/tui/types"
)

func convertMessagesForDisplay(msgs []sessiondb.MessageWithMeta) []ttypes.ChatMessage {
	var result []ttypes.ChatMessage

	toolNameByCallID := buildToolNameIndex(msgs)

	for _, mm := range msgs {
		role := mm.Meta.DisplayRole
		if role == "" {
			role = mm.Msg.Role
		}

		msgTime := time.Now()
		if mm.CreatedAt > 0 {
			msgTime = time.Unix(0, mm.CreatedAt)
		}

		switch role {
		case "user":
			result = append(result, ttypes.ChatMessage{
				Role:    ttypes.RoleUser,
				Content: mm.Msg.Content.ContentDescription(),
				Time:    msgTime,
			})

		case "assistant":
			result = append(result, ttypes.ChatMessage{
				Role:    ttypes.RoleAssistant,
				Content: mm.Msg.Content.String(),
				Time:    msgTime,
			})

		case "thinking":
			result = append(result, ttypes.ChatMessage{
				Role:    ttypes.RoleThinking,
				Content: mm.Msg.ReasoningContent,
				Time:    msgTime,
			})
			if mm.Msg.Content.String() != "" {
				result = append(result, ttypes.ChatMessage{
					Role:    ttypes.RoleAssistant,
					Content: mm.Msg.Content.String(),
					Time:    msgTime,
				})
			}

		case "tool":
			toolName := mm.Meta.ToolName
			toolArgs := mm.Meta.ToolArgs
			toolStatus := mm.Meta.ToolStatus
			if toolStatus == "" {
				toolStatus = "success"
			}
			if toolName == "" && mm.Msg.ToolCallID != "" {
				if name, ok := toolNameByCallID[mm.Msg.ToolCallID]; ok {
					toolName = name
				}
			}

			argsCompact := tools.SummarizeToolArgs(toolName, toolArgs)
			content := toolName
			if argsCompact != "" {
				content = toolName + " " + argsCompact
			}

			chatMsg := ttypes.ChatMessage{
				Role:              ttypes.RoleTool,
				Content:           content,
				Time:              msgTime,
				ToolName:          toolName,
				ToolID:            mm.Msg.ToolCallID,
				ToolArgs:          argsCompact,
				ToolStatus:        toolStatus,
				ToolProgressLabel: mm.Meta.ProgressLabel,
				PermTag:           mm.Meta.PermTag,
				ToolPreviewPhase:  ttypes.ToolPreviewDone,
			}

			if mm.Meta.DurationMs > 0 {
				chatMsg.ToolDuration = time.Duration(mm.Meta.DurationMs) * time.Millisecond
			}
			if mm.Meta.EstTokens > 0 {
				chatMsg.ToolEstTokens = mm.Meta.EstTokens
			}
			if mm.Meta.LinesAdded > 0 || mm.Meta.LinesRemoved > 0 {
				chatMsg.ToolLivedAdded = mm.Meta.LinesAdded
				chatMsg.ToolLivedRemoved = mm.Meta.LinesRemoved
			}
			if mm.Meta.SubPaneID != "" {
				chatMsg.SubPaneID = mm.Meta.SubPaneID
			}

			result = append(result, chatMsg)

		default:
			if mm.Msg.Content.String() != "" {
				result = append(result, ttypes.ChatMessage{
					Role:    role,
					Content: mm.Msg.Content.String(),
					Time:    msgTime,
				})
			}
		}
	}

	return result
}

func buildToolNameIndex(msgs []sessiondb.MessageWithMeta) map[string]string {
	index := make(map[string]string)
	for _, mm := range msgs {
		if mm.Msg.Role == "assistant" && len(mm.Msg.ToolCalls) > 0 {
			for _, tc := range mm.Msg.ToolCalls {
				if tc.ID != "" && tc.Function.Name != "" {
					index[tc.ID] = tc.Function.Name
				}
			}
		}
	}
	return index
}
