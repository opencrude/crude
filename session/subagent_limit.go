package session

// effectiveMaxSubAgents is the session override, else the workspace setting,
// else the default.
func (s *Session) effectiveMaxSubAgents() int {
	if s.maxSubAgentsOverride != nil && *s.maxSubAgentsOverride > 0 {
		return *s.maxSubAgentsOverride
	}
	if s.cfg != nil && s.cfg.Agents.MaxConcurrent > 0 {
		return s.cfg.Agents.MaxConcurrent
	}
	return DefaultAgentsConfig().MaxConcurrent
}

// applySubAgentLimitLocked publishes the effective limit to a live runner, so
// a settings change applies to the next launch without a new session.
func (s *Session) applySubAgentLimitLocked() {
	if s.subAgentSup != nil {
		s.subAgentSup.Manager().SetMaxConcurrent(s.effectiveMaxSubAgents())
	}
}
