package viewmodel

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"crude/indentation"
	"crude/shellport"
	"crude/tui/editor"
	"crude/types"
	"slices"
	"time"
)

type selectionFormatState struct {
	ed                   *editor.Editor
	workspace            shellport.Workspace
	generation, revision uint64
	path                 string
	selections           []editor.SourceSelection
	policy               indentation.Choices
	ctx                  context.Context
	cancel               context.CancelFunc
	loading              bool
	result               *selectionFormatResult
	source               types.InsightSource
}
type selectionFormatResult struct {
	owner  *selectionFormatState
	result types.LSPFormattingResult
	err    error
}

func (m *App) requestSelectionFormat(ed *editor.Editor) tea.Cmd {
	if ed == nil || !ed.IsOpen() || ed.ReadOnly() {
		m.Feedback.NotifyInfo("Open an editable file first.")
		return nil
	}
	if !ed.HasSelection() {
		m.Feedback.NotifyInfo("Select text to format.")
		return nil
	}
	if _, ok := m.ws.(shellport.SelectionFormatter); !ok {
		m.Feedback.NotifyInfo("Selection formatting needs a connected language server.")
		return nil
	}
	if m.selectionFormatting != nil {
		m.Feedback.NotifyInfo("Selection formatting is already running.")
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	m.selectionFormatting = &selectionFormatState{ed: ed, workspace: m.ws, generation: m.workspaceSwitchGen.Load(), revision: ed.Revision(), path: ed.FilePath(), selections: ed.SourceSelections(), policy: ed.Indentation(), ctx: ctx, cancel: cancel}
	m.Feedback.NotifyInfo("Formatting selection…")
	return m.syncSelectionFormatting()
}
func (m *App) selectionFormatCurrent(s *selectionFormatState) bool {
	return s != nil && m.selectionFormatting == s && s.workspace == m.ws && s.generation == m.workspaceSwitchGen.Load() && m.EditorState != nil && m.EditorState.Editor == s.ed && s.ed.IsOpen() && !s.ed.ReadOnly() && s.ed.FilePath() == s.path && s.ed.Revision() == s.revision && s.ed.Indentation() == s.policy && slices.Equal(s.ed.SourceSelections(), s.selections)
}
func (m *App) cancelSelectionFormatting() {
	if s := m.selectionFormatting; s != nil {
		s.cancel()
		m.selectionFormatting = nil
	}
}
func (m *App) syncSelectionFormatting() tea.Cmd {
	s := m.selectionFormatting
	if s == nil {
		return nil
	}
	if !m.selectionFormatCurrent(s) {
		m.cancelSelectionFormatting()
		return nil
	}
	if s.ctx.Err() != nil {
		m.cancelSelectionFormatting()
		m.Feedback.NotifyInfo("Selection formatting timed out. Try again.")
		return nil
	}
	if s.result != nil {
		m.finishSelectionFormatting(*s.result)
		return nil
	}
	if s.loading {
		return nil
	}
	source, ready := s.ed.CurrentInsightSource()
	if !ready {
		return nil
	} // ordinary edit queue flushes first
	s.source = source
	s.loading = true
	in := types.LSPFormattingRequest{Source: source, Options: types.LSPFormattingOptions{TabSize: s.policy.Size, InsertSpaces: s.policy.Style != indentation.Tabs}}
	for _, r := range s.selections {
		a, b := min(r.Anchor, r.Cursor), max(r.Anchor, r.Cursor)
		if a != b {
			in.Ranges = append(in.Ranges, types.LSPFormattingSpan{Start: a, End: b})
		}
	}
	port := s.workspace.(shellport.SelectionFormatter)
	return m.clientIOCommand(s.ctx, func(ctx context.Context) tea.Msg {
		result, err := port.RequestSelectionFormatting(ctx, in)
		return selectionFormatResult{s, result, err}
	})
}
func (m *App) applySelectionFormatting(msg selectionFormatResult) {
	if !m.selectionFormatCurrent(msg.owner) {
		return
	}
	msg.owner.result = &msg
	m.finishSelectionFormatting(msg)
}

func (m *App) finishSelectionFormatting(msg selectionFormatResult) {
	s := msg.owner
	if !m.selectionFormatCurrent(s) {
		return
	}
	if msg.err == nil {
		generation, version := m.ws.LSPMgr().ContextCursor()
		// A fast reply may precede its context topic. Retain it until that exact
		// publication arrives; do not reject valid work or apply it speculatively.
		if generation == "" || generation == msg.result.Boundary.Source.WorkspaceGeneration && version < msg.result.Boundary.Context.Generation {
			return
		}
		if generation != msg.result.Boundary.Source.WorkspaceGeneration || version > msg.result.Boundary.Context.Generation {
			m.cancelSelectionFormatting()
			m.Feedback.NotifyInfo("The language server changed. Format the selection again.")
			return
		}
	}
	defer m.cancelSelectionFormatting()
	if msg.err != nil {
		m.Feedback.NotifyErrorDetail("Could not format the selection. Check the language server and try again.", msg.err.Error())
		return
	}
	if s.ctx.Err() != nil || msg.result.Boundary.Source != s.source || !s.ed.MatchesInsightSource(s.source) {
		return
	}
	if msg.result.Unsupported {
		m.Feedback.NotifyInfo("This language server cannot format selections. Use Format Document if needed.")
		return
	}
	if len(msg.result.Edits) == 0 {
		m.Feedback.NotifyInfo("No formatting changes were needed.")
		return
	}
	if err := s.ed.ApplySourceEdits(s.revision, msg.result.Edits); err != nil {
		m.Feedback.NotifyErrorDetail("Could not apply formatting. Select the text and try again.", err.Error())
		return
	}
	m.Feedback.NotifyInfo("Formatted selection.")
}
