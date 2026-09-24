package workspace

// AgentNameFor resolves the durable identity of a root session. Presentation
// never allocates names: reopening a tab must retain its original identity.
func (w *Workspace) AgentNameFor(agentID string) string {
	if owner := w.sessionForAgent(agentID); owner != nil && owner.backend != nil {
		return owner.backend.AgentName()
	}
	return ""
}
