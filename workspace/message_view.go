package workspace

import (
	"crude/sessiondb"
	ttypes "crude/types"
)

// ChatMessageView is the canonical presentation-neutral conversation message.
// Live state and hydrated state therefore cross into every frontend through
// the exact same contract.
type ChatMessageView = ttypes.ConversationMessage

func messageFromView(view ChatMessageView) Message {
	return ttypes.CloneConversationMessage(view)
}

// statusRowView is the transcript row of a session status change, identical
// whether it arrives live or is hydrated from the session.
func statusRowView(row sessiondb.StatusRow) ChatMessageView {
	return ChatMessageView{InternalID: row.ID, Role: ttypes.RoleStatus, LifecyclePhase: row.Phase,
		LifecycleOperationID: row.ID, Content: row.Content, Time: row.CreatedAt}
}
