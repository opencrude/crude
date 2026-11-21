package core

// The existing resource lane owns cleanup after the session transition. An
// archive or refusal cannot delay or change the user's approval receipt.
func (a *actor) enqueuePlanInvestigationCleanup(agentID string) {
	if a.resourceLane == nil || agentID == "" {
		return
	}
	if job := a.preparePlanInvestigationCleanup(agentID); job != nil && !a.resourceLane.submit(job) && a.ws.Logger != nil {
		a.ws.Logger.Warn("PLAN", "investigation_retained", "agent_id", agentID, "reason", "workspace cleanup worker unavailable")
	}
}
