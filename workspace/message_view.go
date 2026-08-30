package workspace

import ttypes "crude/types"

// ChatMessageView is the canonical presentation-neutral conversation message.
// Live state and hydrated state therefore cross into every frontend through
// the exact same contract.
type ChatMessageView = ttypes.ConversationMessage

func messageFromView(view ChatMessageView) Message {
	view.ReferenceLabels = append([]string(nil), view.ReferenceLabels...)
	return Message(view)
}
