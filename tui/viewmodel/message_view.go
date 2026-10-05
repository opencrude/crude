package viewmodel

import (
	"crude/provider"
	ttypes "crude/tui/types"
	"crude/types/recovery"
	"crude/workspace"
)

// ChatMessagesFromViews projects workspace hydration data into frontend-owned
// message objects. Rendering caches intentionally start empty.
func ChatMessagesFromViews(views []workspace.ChatMessageView) []ttypes.ChatMessage {
	messages := make([]ttypes.ChatMessage, 0, len(views))
	for _, view := range views {
		messages = append(messages, chatMessageFromCanonical(view))
	}
	return messages
}

func chatMessageFromCanonical(view workspace.ChatMessageView) ttypes.ChatMessage {
	return ttypes.ChatMessage{
		InternalID:             view.InternalID,
		Role:                   view.Role,
		Origin:                 view.Origin,
		Content:                view.Content,
		Collaboration:          cloneCollaboration(view.Collaboration),
		Recovery:               cloneRecovery(view.Recovery),
		ReferenceLabels:        append([]string(nil), view.ReferenceLabels...),
		Time:                   view.Time,
		TurnGeneration:         view.TurnGeneration,
		ProviderID:             view.ProviderID,
		AccountID:              view.AccountID,
		ModelID:                view.ModelID,
		ToolName:               view.ToolName,
		ToolArgs:               view.ToolArgs,
		ToolCommand:            view.ToolCommand,
		ToolInput:              view.ToolInput,
		ToolScope:              view.ToolScope,
		ToolID:                 view.ToolID,
		ToolRoundID:            view.ToolRoundID,
		ToolStatus:             view.ToolStatus,
		ToolIndex:              view.ToolIndex,
		ToolProgressPhase:      view.ToolProgressPhase,
		ToolProgressMessage:    view.ToolProgressMessage,
		ToolProgressLabel:      view.ToolProgressLabel,
		ToolPreviewPhase:       view.ToolPreviewPhase,
		ToolEstTokens:          view.ToolEstTokens,
		ToolLivedAdded:         view.ToolLinesAdded,
		ToolLivedRemoved:       view.ToolLinesRemoved,
		ToolStartTime:          view.ToolStartTime,
		ToolDuration:           view.ToolDuration,
		ThinkingActive:         view.ThinkingActive,
		ThinkingInterrupted:    view.ThinkingInterrupted,
		ThinkingDuration:       view.ThinkingDuration,
		ThinkingDisplay:        view.ThinkingDisplay,
		LifecyclePhase:         view.LifecyclePhase,
		LifecycleProgress:      view.LifecycleProgress,
		LifecycleProvider:      view.LifecycleProvider,
		LifecycleUploadedBytes: view.LifecycleUploadedBytes,
		LifecycleTotalBytes:    view.LifecycleTotalBytes,
		LifecycleBeforeTokens:  view.LifecycleBeforeTokens,
		LifecycleAfterTokens:   view.LifecycleAfterTokens,
		LifecycleOperationID:   view.LifecycleOperationID,
		LifecycleTrigger:       view.LifecycleTrigger,
		LifecycleStageName:     view.LifecycleStageName,
		LifecycleStageIndex:    view.LifecycleStageIndex,
		LifecycleCompleted:     view.LifecycleCompleted,
		LifecycleStageCount:    view.LifecycleStageCount,
		LifecycleRetryAttempt:  view.LifecycleRetryAttempt,
		LifecycleRetryAt:       view.LifecycleRetryAt,
		LifecycleRetryLimit:    view.LifecycleRetryLimit,
		LifecycleContextWindow: view.LifecycleContextWindow,
		HistoryPathCount:       view.HistoryPathCount,
		HistoryTargetSnapshot:  view.HistoryTargetSnapshot,
		HistorySafetySnapshot:  view.HistorySafetySnapshot,
		Incomplete:             view.Incomplete,
		IncompleteReason:       view.IncompleteReason,
		SubPaneID:              view.SubPaneID,
		PermTag:                view.PermTag,
		PendingSteering:        view.PendingSteering,
	}
}

func chatMessagesFromConversation(conversation *workspace.Conversation) ([]ttypes.ChatMessage, uint64) {
	if conversation == nil {
		return nil, 0
	}
	views := conversation.Messages()
	messages := make([]ttypes.ChatMessage, 0, len(views))
	for _, view := range views {
		messages = append(messages, chatMessageFromCanonical(view))
	}
	return messages, conversation.Version()
}

func workspaceMessageFromChat(message ttypes.ChatMessage) workspace.Message {
	return workspace.Message{
		InternalID:             message.InternalID,
		Role:                   message.Role,
		Origin:                 message.Origin,
		Content:                message.Content,
		Collaboration:          cloneCollaboration(message.Collaboration),
		Recovery:               cloneRecovery(message.Recovery),
		ReferenceLabels:        append([]string(nil), message.ReferenceLabels...),
		Time:                   message.Time,
		TurnGeneration:         message.TurnGeneration,
		ProviderID:             message.ProviderID,
		AccountID:              message.AccountID,
		ModelID:                message.ModelID,
		ToolName:               message.ToolName,
		ToolArgs:               message.ToolArgs,
		ToolCommand:            message.ToolCommand,
		ToolInput:              message.ToolInput,
		ToolScope:              message.ToolScope,
		ToolID:                 message.ToolID,
		ToolRoundID:            message.ToolRoundID,
		ToolStatus:             message.ToolStatus,
		ToolIndex:              message.ToolIndex,
		ToolProgressPhase:      message.ToolProgressPhase,
		ToolProgressMessage:    message.ToolProgressMessage,
		ToolProgressLabel:      message.ToolProgressLabel,
		ToolPreviewPhase:       message.ToolPreviewPhase,
		ToolEstTokens:          message.ToolEstTokens,
		ToolLinesAdded:         message.ToolLivedAdded,
		ToolLinesRemoved:       message.ToolLivedRemoved,
		ToolStartTime:          message.ToolStartTime,
		ToolDuration:           message.ToolDuration,
		ThinkingActive:         message.ThinkingActive,
		ThinkingInterrupted:    message.ThinkingInterrupted,
		ThinkingDuration:       message.ThinkingDuration,
		ThinkingDisplay:        message.ThinkingDisplay,
		LifecyclePhase:         message.LifecyclePhase,
		LifecycleProgress:      message.LifecycleProgress,
		LifecycleProvider:      message.LifecycleProvider,
		LifecycleUploadedBytes: message.LifecycleUploadedBytes,
		LifecycleTotalBytes:    message.LifecycleTotalBytes,
		LifecycleBeforeTokens:  message.LifecycleBeforeTokens,
		LifecycleAfterTokens:   message.LifecycleAfterTokens,
		LifecycleOperationID:   message.LifecycleOperationID,
		LifecycleTrigger:       message.LifecycleTrigger,
		LifecycleStageName:     message.LifecycleStageName,
		LifecycleStageIndex:    message.LifecycleStageIndex,
		LifecycleCompleted:     message.LifecycleCompleted,
		LifecycleStageCount:    message.LifecycleStageCount,
		LifecycleRetryAttempt:  message.LifecycleRetryAttempt,
		LifecycleRetryAt:       message.LifecycleRetryAt,
		LifecycleRetryLimit:    message.LifecycleRetryLimit,
		LifecycleContextWindow: message.LifecycleContextWindow,
		HistoryPathCount:       message.HistoryPathCount,
		HistoryTargetSnapshot:  message.HistoryTargetSnapshot,
		HistorySafetySnapshot:  message.HistorySafetySnapshot,
		Incomplete:             message.Incomplete,
		IncompleteReason:       message.IncompleteReason,
		SubPaneID:              message.SubPaneID,
		PermTag:                message.PermTag,
		PendingSteering:        message.PendingSteering,
	}
}

func cloneCollaboration(input *provider.CollaborationAttribution) *provider.CollaborationAttribution {
	if input == nil {
		return nil
	}
	result := *input
	result.RecipientIDs = append([]string(nil), input.RecipientIDs...)
	result.RecipientNames = append([]string(nil), input.RecipientNames...)
	return &result
}

func cloneRecovery(status *recovery.Status) *recovery.Status {
	if status == nil {
		return nil
	}
	copy := *status
	return &copy
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
	if node.State.Messages == nil || proj.renderer.SourceVersion() == node.State.Messages.Version() {
		return
	}
	messages, version := chatMessagesFromConversation(node.State.Messages)
	proj.renderer.SyncMessages(messages, version)
	if scroll {
		proj.renderer.ScrollToBottom()
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
	if sp.Messages == nil || proj.renderer.SourceVersion() == sp.Messages.Version() {
		return
	}
	messages, version := chatMessagesFromConversation(sp.Messages)
	proj.renderer.SyncMessages(messages, version)
	if scroll {
		proj.renderer.ScrollToBottom()
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

func (m *App) hydrateActiveMessages(messages []ttypes.ChatMessage) {
	agentID := m.AgentMgr.ActiveID()
	node := m.AgentMgr.Get(agentID)
	if node == nil || node.State == nil || node.State.Messages == nil {
		return
	}
	for _, message := range messages {
		node.State.Messages.AppendMessage(workspaceMessageFromChat(message))
	}
	// Hydrated streams are stopped. Normalize before publishing, and export
	// the transcript once rather than copying every growing prefix.
	node.State.Messages.MarkStreamingToolsCancelled()
	node.State.Messages.MarkAllThinkingInterrupted()
	m.syncAgentMessages(agentID, true)
}
