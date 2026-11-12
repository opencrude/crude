package viewmodel

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/shellport"
	"crude/tui/editor"
	"crude/types"
)

// occurrenceDelay lets the caret rest before asking for the occurrences of
// the symbol under it, so moving through text sends no request per key.
const occurrenceDelay = 250 * time.Millisecond

// occurrenceRequest owns the one occurrence question of the focused editor.
// After its answer it stays as the record of that caret, so a caret resting
// where there is nothing to mark asks only once. Source, provider and range
// validation belong to the core.
type occurrenceRequest struct {
	editor             *editor.Editor
	workspace          shellport.Workspace
	port               shellport.Highlights
	generation         uint64
	source             types.InsightSource
	producer           types.InsightProducer
	contextOwner       string
	contextGeneration  uint64
	revision           uint64
	caret              editor.Position
	line, col          int
	ctx                context.Context
	cancel             context.CancelFunc
	started, installed bool
	result             *types.LSPHighlights
}

type occurrenceDueMsg struct{ owner *occurrenceRequest }
type occurrenceResultMsg struct {
	owner  *occurrenceRequest
	result types.LSPHighlights
	err    error
}

func (m *App) retireOccurrences() {
	if r := m.occurrence; r != nil {
		m.occurrence = nil
		r.cancel()
	}
}

func (m *App) resetOccurrences() {
	if r := m.occurrence; r != nil {
		r.editor.ClearOccurrences()
	}
	m.retireOccurrences()
}

// syncOccurrences reconciles the focused view with its exact source, producer
// and context. A result waits for the same context in the shell's replica before
// it can paint; moving within installed marks keeps their original owner.
func (m *App) syncOccurrences() tea.Cmd {
	ed := m.logicalEditorFocus()
	if m.ws == nil || m.asyncIOStopped.Load() || ed == nil {
		m.resetOccurrences()
		return nil
	}
	port, connected := m.ws.(shellport.Highlights)
	if !connected {
		m.resetOccurrences()
		return nil
	}
	service := m.ws.LSPMgr()
	if service == nil || !service.ReferencesEnabled() || ed.HasMultipleCarets() {
		m.resetOccurrences()
		return nil
	}
	producer := service.FileProducer(ed.FilePath())
	source, current := ed.CurrentInsightSource()
	contextOwner, contextGeneration := service.ContextCursor()
	if producer.InstanceID == "" || !current || contextOwner == "" || contextOwner != source.WorkspaceGeneration {
		m.resetOccurrences()
		return nil
	}
	caret := editor.Position{Line: ed.CursorLine(), Col: ed.CursorCol()}
	r := m.occurrence
	if r != nil && (r.editor != ed || r.workspace != m.ws || !m.acceptsLSPGeneration(r.generation) ||
		r.source != source || r.revision != ed.Revision() || r.producer != producer ||
		r.contextOwner != contextOwner || contextGeneration > r.contextGeneration ||
		(r.caret != caret && (!r.installed || !ed.OccurrencesCoverCaret()))) {
		m.resetOccurrences()
		r = nil
	}
	if r == nil {
		// Do not occupy a core job while this exact source is unsynchronized.
		if !service.FileSynchronized(source, contextGeneration) {
			return nil
		}
		base, generation := m.lspRequestContext()
		ctx, cancel := context.WithCancel(base)
		line, col := ed.LSPCursorPosition()
		r = &occurrenceRequest{editor: ed, workspace: m.ws, port: port, generation: generation, source: source,
			producer: producer, contextOwner: contextOwner, contextGeneration: contextGeneration,
			revision: ed.Revision(), caret: caret, line: line, col: col, ctx: ctx, cancel: cancel}
		m.occurrence = r
		return tea.Tick(occurrenceDelay, func(time.Time) tea.Msg { return occurrenceDueMsg{r} })
	}
	if r.result != nil && !r.installed && contextGeneration == r.result.Boundary.Context.Generation {
		r.installed = true
		if err := ed.SetOccurrences(r.result); err != nil {
			ed.ClearOccurrences()
		}
	}
	return nil
}

func (m *App) occurrenceCurrent(r *occurrenceRequest) bool {
	if r == nil || m.occurrence != r || r.ctx.Err() != nil || r.workspace != m.ws || m.asyncIOStopped.Load() ||
		!m.acceptsLSPGeneration(r.generation) || m.logicalEditorFocus() != r.editor || !r.editor.IsOpen() ||
		r.editor.HasMultipleCarets() || r.editor.Revision() != r.revision ||
		r.editor.CursorLine() != r.caret.Line || r.editor.CursorCol() != r.caret.Col ||
		!r.editor.MatchesInsightSource(r.source) || m.ws.LSPMgr() == nil || m.ws.LSPMgr().FileProducer(r.source.Path) != r.producer {
		return false
	}
	service := m.ws.LSPMgr()
	owner, generation := service.ContextCursor()
	return service.ReferencesEnabled() && owner == r.contextOwner && generation <= r.contextGeneration
}

func (m *App) startOccurrenceRequest(msg occurrenceDueMsg) tea.Cmd {
	r := msg.owner
	if !m.occurrenceCurrent(r) || r.started {
		return nil
	}
	if !m.ws.LSPMgr().FileSynchronized(r.source, r.contextGeneration) {
		m.resetOccurrences()
		return nil
	}
	r.started = true
	return m.clientIOCommand(r.ctx, func(parent context.Context) tea.Msg {
		ctx, cancel := context.WithTimeout(parent, 10*time.Second)
		defer cancel()
		result, err := r.port.RequestLSPHighlights(ctx, r.source, r.line, r.col)
		return occurrenceResultMsg{r, result, err}
	})
}

// Successful and negative answers share the exact context-publication barrier.
// Failures stay quiet and are not replayed for an unchanged caret and source.
func (m *App) handleOccurrenceResult(msg occurrenceResultMsg) {
	r := msg.owner
	if !m.occurrenceCurrent(r) {
		return
	}
	r.cancel()
	result := msg.result
	if msg.err != nil || result.WireVersion <= 0 || result.Boundary.Source != r.source ||
		result.Boundary.Producer != r.producer || result.Boundary.Context.Generation < r.contextGeneration ||
		result.Line != r.line || result.Col != r.col {
		r.editor.ClearOccurrences()
		return
	}
	r.contextGeneration = result.Boundary.Context.Generation
	r.result = &result
}
