package viewmodel

import (
	tea "charm.land/bubbletea/v2"
	"crude/tui/screens/diffview"
)

type comparisonSearchSurface interface {
	SearchCommand() tea.Cmd
	SearchDone() <-chan struct{}
	StopSearch()
	WaitSearch()
}

func (m *App) visitComparisonSearch(visit func(comparisonSearchSurface)) {
	if m.gitPanel != nil {
		for _, doc := range m.gitPanel.ComparisonDocuments() {
			visit(doc)
		}
	}
	for _, view := range m.gitReviews {
		visit(view.screen.Document())
	}
	for _, view := range m.localDiffs {
		visit(view.screen.ComparisonReader())
	}
}

func (m *App) drainComparisonSearch() tea.Cmd {
	for done := range m.comparisonSearchJobs {
		select {
		case <-done:
			delete(m.comparisonSearchJobs, done)
		default:
		}
	}
	var commands []tea.Cmd
	m.visitComparisonSearch(func(s comparisonSearchSurface) {
		if command := s.SearchCommand(); command != nil {
			commands = append(commands, command)
		}
		if done := s.SearchDone(); done != nil {
			if m.comparisonSearchJobs == nil {
				m.comparisonSearchJobs = make(map[<-chan struct{}]comparisonSearchSurface)
			}
			m.comparisonSearchJobs[done] = s
		}
	})
	return tea.Batch(commands...)
}

func (m *App) applyComparisonSearch(msg diffview.SearchReadyMsg) {
	if !msg.Apply() {
		return
	}
	m.forceFrame()
}

func (m *App) cancelComparisonSearch() {
	m.visitComparisonSearch(func(s comparisonSearchSurface) { s.StopSearch() })
}
func (m *App) stopComparisonSearch() {
	m.visitComparisonSearch(func(s comparisonSearchSurface) {
		if done := s.SearchDone(); done != nil {
			if m.comparisonSearchJobs == nil {
				m.comparisonSearchJobs = make(map[<-chan struct{}]comparisonSearchSurface)
			}
			m.comparisonSearchJobs[done] = s
		}
	})
	m.cancelComparisonSearch()
	for _, s := range m.comparisonSearchJobs {
		s.WaitSearch()
	}
	for done := range m.comparisonSearchJobs {
		<-done
	}
	m.comparisonSearchJobs = nil
}
