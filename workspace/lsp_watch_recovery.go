package workspace

import "crude/types"

// SetProjectWatchReliable records whether project file observation is complete.
// It only changes in-memory authority; the existing worker joins and starts
// producers outside the caller. Repeated identical observations are harmless.
func (m *LSPManager) SetProjectWatchReliable(reliable bool) {
	m.editStages.setReliable(reliable)
	m.reviewWatch.setReliable(reliable)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.projectWatchUnreliable == !reliable {
		return
	}
	m.projectWatchUnreliable = !reliable
	if !reliable && m.sources != nil && m.sources.Insight != nil {
		m.sources.Insight.diagnostics.InvalidateMatching(func(b types.InsightBoundary) bool {
			_, project := m.projects[b.Context.ID]
			return project
		}, "Project file observation is incomplete; this result may be out of date.")
	}
	// Retire on both edges: a shared document server may have started during
	// the gap, and only a new process after recovery can re-index the project.
	for key := range m.projects {
		if state := m.states[key]; state != nil && !state.stopped {
			m.retireLocked(state)
		}
	}
	m.bumpVersionLocked()
	if len(m.projects) > 0 || len(m.states) > 0 {
		m.signalLocked()
	}
}
