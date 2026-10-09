package types

import "slices"

// CloneConversationMessage detaches all mutable evidence in a message. Strings,
// reasoning metadata and timestamps are value data; the nested attribution,
// reference labels and recovery record must not alias a producer or subscriber.
func CloneConversationMessage(message ConversationMessage) ConversationMessage {
	message.ReferenceLabels = slices.Clone(message.ReferenceLabels)
	if message.Collaboration != nil {
		attribution := *message.Collaboration
		attribution.RecipientIDs = slices.Clone(attribution.RecipientIDs)
		attribution.RecipientNames = slices.Clone(attribution.RecipientNames)
		message.Collaboration = &attribution
	}
	if message.Recovery != nil {
		status := *message.Recovery
		message.Recovery = &status
	}
	return message
}
