package viewmodel

import (
	"context"
	"sync"

	tea "charm.land/bubbletea/v2"
)

type comparisonWork struct {
	once   sync.Once
	done   chan struct{}
	cancel context.CancelFunc
	run    tea.Cmd
	result tea.Msg
}

func (w *comparisonWork) command() tea.Msg {
	w.once.Do(func() { defer close(w.done); w.result = w.run() })
	return w.result
}

// A command may still be queued when its view closes or the program stops.
// Keep its cancellation and join ownership independent of message delivery.
func (m *App) comparisonCommand(cancel context.CancelFunc, run tea.Cmd) tea.Cmd {
	for work := range m.comparisonWork {
		select {
		case <-work.done:
			delete(m.comparisonWork, work)
		default:
		}
	}
	if m.comparisonWork == nil {
		m.comparisonWork = make(map[*comparisonWork]struct{})
	}
	work := &comparisonWork{done: make(chan struct{}), cancel: cancel, run: run}
	m.comparisonWork[work] = struct{}{}
	return work.command
}

func (m *App) stopComparisonWork() {
	for work := range m.comparisonWork {
		work.cancel()
	}
	for work := range m.comparisonWork {
		work.command()
		delete(m.comparisonWork, work)
	}
}
