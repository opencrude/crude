package session

// Emitted only after the owning input/preparation commits. The exact excluded
// reference and reason also live in its immutable context enrichment.
func notifyAttachmentIssues(sender EventSender, agentID string, issues []string) {
	if sender == nil {
		return
	}
	for _, reason := range issues {
		sender.SendAgentEventWithID("attachment_excluded", map[string]any{"reason": reason}, agentID)
	}
}
