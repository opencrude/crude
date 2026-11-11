package viewmodel

import (
	tea "charm.land/bubbletea/v2"
	"crude/tui/editor"
	"crude/tui/ui"
)

type editorSyntaxSurface interface{ SyntaxEditor() *editor.Editor }

func (m *App) visitSyntaxEditors(visit func(*editor.Editor)) {
	if m.EditorState != nil {
		for _, view := range m.EditorState.Views {
			visit(view.Editor)
		}
		visit(m.EditorState.Editor)
	}
	if m.windowManager != nil {
		for _, fw := range m.windowManager.Windows() {
			if frame, ok := fw.Content.(*ui.WindowFrameWidget); ok {
				if surface, ok := frame.Body().(editorSyntaxSurface); ok {
					visit(surface.SyntaxEditor())
				}
			}
		}
	}
}

// Every retained main editor gets its exact completion, independent of selected
// tab/focus. Closed editors and older source generations reject their results.
func (m *App) drainEditorSyntaxCommands() tea.Cmd {
	for done := range m.editorSyntaxJobs {
		select {
		case <-done:
			delete(m.editorSyntaxJobs, done)
		default:
		}
	}
	for ed, r := range m.editorConfigRequests {
		if !ed.IsOpen() || r.owner != m.ws {
			r.invalid = true
			r.cancel()
			if !r.loading {
				delete(m.editorConfigRequests, ed)
			}
		}
	}
	var commands []tea.Cmd
	add := func(ed *editor.Editor) {
		if ed == nil || !ed.IsOpen() {
			return
		}
		if cmd := m.editorConfigurationCommand(ed); cmd != nil {
			commands = append(commands, cmd)
		}
		if command := ed.SyntaxCommand(); command != nil {
			commands = append(commands, command)
		}
		if command := ed.RowsCommand(); command != nil {
			commands = append(commands, command)
		}
		for _, done := range []<-chan struct{}{ed.SyntaxDone(), ed.RowsDone()} {
			if done != nil {
				if m.editorSyntaxJobs == nil {
					m.editorSyntaxJobs = make(map[<-chan struct{}]*editor.Editor)
				}
				m.editorSyntaxJobs[done] = ed
			}
		}
	}
	m.visitSyntaxEditors(add)
	return tea.Batch(commands...)
}

func (m *App) applyEditorSyntax(msg editor.SyntaxReadyMsg) {
	msg.Apply()
	if failure := msg.Failure(); failure != "" && m.Feedback != nil {
		m.Feedback.NotifyError(failure)
	}
	m.invalidatePreparedEditor(msg.Editor())
}

func (m *App) invalidatePreparedEditor(ed *editor.Editor) {
	if m.windowManager == nil {
		return
	}
	for _, fw := range m.windowManager.Windows() {
		if frame, ok := fw.Content.(*ui.WindowFrameWidget); ok {
			if surface, ok := frame.Body().(editorSyntaxSurface); ok && surface.SyntaxEditor() == ed {
				fw.MarkContentDirty()
			}
		}
	}
}

func (m *App) stopEditorSyntax() {
	// A final paint/open may have admitted a job after Update's last drain.
	// Capture every retained surface before cancellation and the join barrier.
	m.visitSyntaxEditors(func(ed *editor.Editor) {
		if ed == nil {
			return
		}
		for _, done := range []<-chan struct{}{ed.SyntaxDone(), ed.RowsDone()} {
			if done != nil {
				if m.editorSyntaxJobs == nil {
					m.editorSyntaxJobs = make(map[<-chan struct{}]*editor.Editor)
				}
				m.editorSyntaxJobs[done] = ed
			}
		}
		ed.StopRows()
		ed.StopSyntax()
	})
	for _, ed := range m.editorSyntaxJobs {
		ed.StopRows()
		ed.StopSyntax()
	}
	for done := range m.editorSyntaxJobs {
		<-done
	}
	m.editorSyntaxJobs = nil
}
