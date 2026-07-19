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

func chatMessagesFromConversation(conversation *workspace.Conversation) ([]ttypes.ChatMessage, uint64) {
	if conversation == nil {
		return nil, 0
	}
	views := conversation.Messages()
	messages := make([]ttypes.ChatMessage, 0, len(views))
	for _, view := range views {
		messages = append(messages, ttypes.ChatMessage{
			InternalID:          view.InternalID,
			Role:                view.Role,
			Content:             view.Content,
			Time:                view.Time,
			ToolName:            view.ToolName,
			ToolArgs:            view.ToolArgs,
			ToolID:              view.ToolID,
			ToolStatus:          view.ToolStatus,
			ToolIndex:           view.ToolIndex,
			ToolProgressPhase:   view.ToolProgressPhase,
			ToolProgressMessage: view.ToolProgressMessage,
			ToolProgressLabel:   view.ToolProgressLabel,
			ToolPreviewPhase:    view.ToolPreviewPhase,
			ToolEstTokens:       view.ToolEstTokens,
			ToolLivedAdded:      view.ToolLinesAdded,
			ToolLivedRemoved:    view.ToolLinesRemoved,
			ToolStartTime:       view.ToolStartTime,
			ToolDuration:        view.ToolDuration,
			ThinkingActive:      view.ThinkingActive,
			ThinkingInterrupted: view.ThinkingInterrupted,
			Incomplete:          view.Incomplete,
			IncompleteReason:    view.IncompleteReason,
			SubPaneID:           view.SubPaneID,
			PermTag:             view.PermTag,
		})
	}
	return messages, conversation.Version()
}

func workspaceMessageFromChat(message ttypes.ChatMessage) workspace.Message {
	return workspace.Message{
		InternalID:          message.InternalID,
		Role:                message.Role,
		Content:             message.Content,
		Time:                message.Time,
		ToolName:            message.ToolName,
		ToolArgs:            message.ToolArgs,
		ToolID:              message.ToolID,
		ToolStatus:          message.ToolStatus,
		ToolIndex:           message.ToolIndex,
		ToolProgressPhase:   message.ToolProgressPhase,
		ToolProgressMessage: message.ToolProgressMessage,
		ToolProgressLabel:   message.ToolProgressLabel,
		ToolPreviewPhase:    message.ToolPreviewPhase,
		ToolEstTokens:       message.ToolEstTokens,
		ToolLinesAdded:      message.ToolLivedAdded,
		ToolLinesRemoved:    message.ToolLivedRemoved,
		ToolStartTime:       message.ToolStartTime,
		ToolDuration:        message.ToolDuration,
		ThinkingActive:      message.ThinkingActive,
		ThinkingInterrupted: message.ThinkingInterrupted,
		Incomplete:          message.Incomplete,
		IncompleteReason:    message.IncompleteReason,
		SubPaneID:           message.SubPaneID,
		PermTag:             message.PermTag,
	}
}

func (m *App) syncAgentMessages(agentID string, scroll bool) {
	node := m.AgentMgr.Get(agentID)
	if node == nil || node.State == nil {
		return
	}
	sp := m.PaneMgr.Get(agentID)
	if sp == nil {
		return
	}
	proj := m.subPaneProjections.GetExisting(agentID)
	if proj == nil {
		return
	}
	if node.State.Messages == nil || proj.msgList.SourceVersion() == node.State.Messages.Version() {
		return
	}
	messages, version := chatMessagesFromConversation(node.State.Messages)
	proj.msgList.SyncMessages(messages, version)
	if scroll {
		proj.msgList.ScrollToBottom()
	}
}

func (m *App) syncPaneMessages(paneID string, scroll bool) {
	sp := m.PaneMgr.Get(paneID)
	if sp == nil {
		return
	}
	proj := m.subPaneProjections.GetExisting(paneID)
	if proj == nil {
		return
	}
	if sp.Messages == nil || proj.msgList.SourceVersion() == sp.Messages.Version() {
		return
	}
	messages, version := chatMessagesFromConversation(sp.Messages)
	proj.msgList.SyncMessages(messages, version)
	if scroll {
		proj.msgList.ScrollToBottom()
	}
}

func (m *App) syncConversationMessages(conversation *workspace.Conversation, scroll bool) {
	if conversation == nil {
		return
	}
	for id, pane := range m.PaneMgr.Panes() {
		if pane != nil && pane.Messages == conversation {
			m.syncPaneMessages(id, scroll)
			return
		}
	}
}

func (m *App) appendAgentMessage(agentID string, message ttypes.ChatMessage) {
	node := m.AgentMgr.Get(agentID)
	if node == nil || node.State == nil || node.State.Messages == nil {
		return
	}
	node.State.Messages.AppendMessage(workspaceMessageFromChat(message))
	m.syncAgentMessages(agentID, true)
}

func (m *App) appendActiveMessage(message ttypes.ChatMessage) {
	m.appendAgentMessage(m.AgentMgr.ActiveID(), message)
}
