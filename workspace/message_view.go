package workspace

import (
	"crude/agent/sessiondb"
	"crude/types/conversation"
)

func messageFromView(view conversation.Message) conversation.Message {
	return conversation.CloneMessage(view)
}

// statusRowView is the transcript row of a session status change, identical
// whether it arrives live or is hydrated from the session.
func statusRowView(row sessiondb.StatusRow) conversation.Message {
	return conversation.Message{InternalID: row.ID, Role: conversation.RoleStatus, LifecyclePhase: row.Phase,
		LifecycleOperationID: row.ID, Content: row.Content, Time: row.CreatedAt}
}
