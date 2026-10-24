package workspace

// InstallSnapshot installs metadata into a client-owned read mirror without
// replaying events, tools or lifecycle transitions. Transcript installation is a
// separate revision-checked Conversation.InstallDelta transaction.
func (a *AgentState) InstallSnapshot(snapshot AgentSnapshot) {
	a.ID = snapshot.ID
	a.Streaming, a.Thinking = snapshot.Streaming, snapshot.Thinking
	a.Running, a.Cancelling, a.Completed = snapshot.Running, snapshot.Cancelling, snapshot.Completed
	a.Status, a.TurnIdentity = snapshot.Status, snapshot.TurnIdentity
	a.runtimeIdentity = snapshot.RuntimeIdentity
	a.ReasoningMode, a.Recovery = snapshot.ReasoningMode, snapshot.Recovery
	a.LastTokenTime, a.ActiveCompaction = snapshot.LastTokenTime, snapshot.ActiveCompaction
	a.terminal = !snapshot.Running && !snapshot.Thinking && !snapshot.Streaming
	a.PendingBlockID = ""
	if snapshot.Prompt != nil {
		a.PendingBlockID = snapshot.Prompt.BlockID
	}
}
