package workspace

import (
	"context"

	"crude/types/insight"
)

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
	m.cancelAssemblyInvalidationLocked()
	m.invalidateAssemblyDiscoveryLocked()
	if !reliable {
		m.cancelProjectCheckLocked()
		m.invalidateCheckedResultsLocked(func(string, string, string) bool { return true }, "Project file observation is incomplete; this result may be out of date.")
	}
	if reliable {
		m.invalidateCheckedResultsLocked(func(string, string, string) bool { return true },
			"File observation resumed. Crude will check this file again.")
		m.requestProjectCheckLocked()
		// The lost watch generation may have missed ignores or an entire source
		// subtree. Refresh even if there are no active project producers.
		m.scheduleAssemblyInvalidationLocked(context.Background())
	}
	if !reliable && m.sources != nil && m.sources.Insight != nil {
		m.sources.Insight.diagnostics.InvalidateMatching(func(b insight.Boundary) bool {
			return isProjectFact(b) || m.reportsProjectLocked(b.Context.ID, m.states[b.Context.ID])
		}, "Project file observation is incomplete; this result may be out of date.")
	}
	// Retire on both edges: a shared document server may have started during
	// the gap, and only a new process after recovery can re-index the project.
	for key, state := range m.states {
		if !state.stopped && m.reportsProjectLocked(key, state) {
			m.retireLocked(state)
		}
	}
	m.bumpVersionLocked()
	if len(m.projects) > 0 || len(m.states) > 0 {
		m.signalLocked()
	}
}
