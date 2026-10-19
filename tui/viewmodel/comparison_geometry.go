package viewmodel

import (
	tea "charm.land/bubbletea/v2"
	"crude/tui/screens/diffview"
)

type comparisonGeometryMsg struct {
	workspace, generation uint64
	results               []diffview.SourceGeometryReadyMsg
}

// Calibration changes only prepared geometry. Never redo Git I/O, diffing or
// source-size work on Update, and never publish another workspace's result.
func (m *App) drainComparisonGeometry() tea.Cmd {
	if !m.comparisonReflow || m.asyncIOStopped.Load() {
		return nil
	}
	m.comparisonReflow = false
	if m.comparisonReflowCancel != nil {
		m.comparisonReflowCancel()
	}
	ctx, cancel := m.sourceControlContext()
	m.comparisonReflowCancel = cancel
	m.comparisonGeometryGen++
	msg := comparisonGeometryMsg{workspace: m.workspaceSwitchGen.Load(), generation: m.comparisonGeometryGen}
	var commands []tea.Cmd
	if m.gitPanel != nil {
		for _, doc := range m.gitPanel.ComparisonDocuments() {
			doc.BeginGeometryReflow()
		}
	}
	for _, view := range m.gitReviews {
		view.screen.Document().BeginGeometryReflow()
	}
	for _, view := range m.localDiffs {
		view.screen.ComparisonReader().BeginGeometryReflow()
	}
	for source := range m.comparisonGeometrySources() {
		if cmd := source.ReflowCommand(ctx); cmd != nil {
			commands = append(commands, cmd)
		}
	}
	return m.comparisonCommand(cancel, func() tea.Msg {
		defer cancel()
		for _, cmd := range commands {
			msg.results = append(msg.results, cmd().(diffview.SourceGeometryReadyMsg))
		}
		return msg
	})
}

func (m *App) applyComparisonGeometry(msg comparisonGeometryMsg) {
	if m.asyncIOStopped.Load() || msg.workspace != m.workspaceSwitchGen.Load() || msg.generation != m.comparisonGeometryGen {
		return
	}
	m.comparisonGeometryGen++
	m.comparisonReflowCancel = nil
	// A closed/replaced reader must not publish into an orphaned source. Shared
	// sources still retained by another review remain eligible.
	active := m.comparisonGeometrySources()
	for _, result := range msg.results {
		if !active[result.Source()] {
			continue
		}
		if _, err := result.Apply(); err != nil && m.Feedback != nil {
			m.Feedback.NotifyError("Comparison geometry: " + err.Error())
		}
	}
	if m.gitPanel != nil {
		for _, doc := range m.gitPanel.ComparisonDocuments() {
			doc.InvalidateWidthProfile()
		}
	}
	for _, view := range m.gitReviews {
		view.screen.InvalidateWidthProfile()
	}
	for _, view := range m.localDiffs {
		view.screen.InvalidateWidthProfile()
	}
	if m.rootHost != nil {
		m.rootHost.MarkLayout()
	}
	m.forceFrame()
}

func (m *App) cancelComparisonGeometry() {
	if m.comparisonReflowCancel != nil {
		m.comparisonReflowCancel()
		m.comparisonReflowCancel = nil
	}
	m.comparisonGeometryGen++
	m.comparisonReflow = false
}

func (m *App) comparisonGeometrySources() map[diffview.SourceLines]bool {
	retained := make(map[diffview.SourceLines]bool)
	add := func(current []diffview.SourceLines) {
		for _, source := range current {
			retained[source] = true
		}
	}
	if m.gitPanel != nil {
		for _, file := range m.gitPanel.PreparedList() {
			add(file.SourceGeometry())
		}
		for _, doc := range m.gitPanel.ComparisonDocuments() {
			add(doc.SourceGeometry())
		}
	}
	for _, view := range m.gitReviews {
		add(view.screen.Document().SourceGeometry())
	}
	for _, view := range m.localDiffs {
		add(view.screen.ComparisonReader().SourceGeometry())
	}
	return retained
}

func (m *App) cancelOrphanedComparisonGeometry(sources []diffview.SourceLines) {
	retained := m.comparisonGeometrySources()
	for _, source := range sources {
		if !retained[source] {
			source.CancelReflow()
		}
	}
}
