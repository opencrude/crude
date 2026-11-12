package viewmodel

import (
	"context"
	"errors"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"
	"crude/coreclient"
	"crude/coreprotocol"
	"crude/retry"
	"crude/shellport"
	"crude/tui/editor"
	"crude/types"
)

// One private view owns only current presentation and its cancellable request.
// Source, provider, token validation and reusable delta bases belong to core.
type semanticViewState struct {
	editor     *editor.Editor
	workspace  shellport.Workspace
	generation uint64
	id         string
	sequence   uint64
	retained   bool
	port       shellport.SemanticTokens
	visible    bool
	request    *semanticRequest
}
type semanticRequest struct {
	view               *semanticViewState
	port               shellport.SemanticTokens
	source             types.InsightSource
	revision           uint64
	producer           types.InsightProducer
	contextOwner       string
	contextGeneration  uint64
	first, last        int
	span               types.LSPSemanticRange
	sequence           uint64
	ctx                context.Context
	cancel             context.CancelFunc
	started, installed bool
	busyAttempts       int
	result             *types.LSPSemanticSet
	err                error
}
type semanticDueMsg struct{ owner *semanticRequest }
type semanticResultMsg struct {
	owner  *semanticRequest
	result types.LSPSemanticSet
	err    error
}
type semanticReleasedMsg struct{ err error }

func (m *App) semanticView(ed *editor.Editor) *semanticViewState {
	if ed == nil || !ed.IsOpen() {
		return nil
	}
	if view := m.semanticViews[ed]; view != nil {
		return view
	}
	m.semanticViewSequence++
	view := &semanticViewState{editor: ed, workspace: m.ws, generation: m.workspaceSwitchGen.Load(), id: fmt.Sprintf("editor-semantic-%d-%d", m.workspaceSwitchGen.Load(), m.semanticViewSequence)}
	if m.semanticViews == nil {
		m.semanticViews = make(map[*editor.Editor]*semanticViewState)
	}
	m.semanticViews[ed] = view
	return view
}
func (m *App) retireSemantic(view *semanticViewState, release bool) {
	if r := view.request; r != nil {
		view.request = nil
		r.cancel()
		view.editor.ClearSemanticTokens()
	}
	if release && view.retained {
		port, sequence, id := view.port, view.sequence, view.id
		view.retained = false
		m.runDetachedAsyncCmd(func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			return semanticReleasedMsg{port.ReleaseLSPSemantic(ctx, id, sequence)}
		})
	}
}

func (m *App) resetSemanticTokens() {
	for _, view := range m.semanticViews {
		m.retireSemantic(view, true)
		view.editor.ClearSemanticTokens()
	}
	m.semanticViews = nil
}

// syncSemanticTokens reads only constant-size view metadata on Update. Core
// resolves the end-of-line UTF-16 coordinate and parses source off this thread.
func (m *App) syncSemanticTokens() tea.Cmd {
	for ed, view := range m.semanticViews {
		view.visible = false
		if !ed.IsOpen() || view.workspace != m.ws || !m.acceptsLSPGeneration(view.generation) {
			m.retireSemantic(view, true)
			delete(m.semanticViews, ed)
		}
	}
	if m.ws == nil || m.ws.LSPMgr() == nil || m.asyncIOStopped.Load() {
		for _, view := range m.semanticViews {
			m.retireSemantic(view, true)
		}
		return nil
	}
	service := m.ws.LSPMgr()
	port, connected := m.ws.(shellport.SemanticTokens)
	if connected && m.lspRequestCtx == nil {
		m.lspRequestContext()
	}
	mark := func(ed *editor.Editor) {
		if ed == nil || !ed.IsOpen() {
			return
		}
		view := m.semanticViews[ed]
		if view == nil && connected && service.FileProducer(ed.FilePath()).InstanceID != "" {
			view = m.semanticView(ed)
		}
		if view != nil {
			view.visible = true
		}
	}
	if m.mainPanes != nil {
		for id, group := range m.mainPanes.groups {
			_, docked := m.mainPanes.placements[id]
			floating := m.mainPanes.windows[id] != nil && m.overlayHost != nil && m.overlayHost.IsOpen(paneWindowID(id))
			if (docked || floating) && group.view != nil && group.view.editor != nil && !group.editor.Empty() {
				mark(group.view.editor.SyntaxEditor())
			}
		}
	}
	if m.EditorState != nil && m.EditorState.ActiveView != "" {
		mark(m.EditorState.Editor)
	}
	contextOwner, contextGeneration := service.ContextCursor()
	var commands []tea.Cmd
	for _, view := range m.semanticViews {
		source, ready := view.editor.CurrentInsightSource()
		first, last, painted := view.editor.VisibleSourceLines()
		producer := service.FileProducer(view.editor.FilePath())
		if !connected || !view.visible || !painted || producer.InstanceID == "" || contextOwner == "" || contextOwner != source.WorkspaceGeneration {
			m.retireSemantic(view, true)
			continue
		}
		r := view.request
		if r != nil && r.producer != producer {
			m.retireSemantic(view, true)
			r = nil
		}
		if !ready {
			// Pending local text hides old colors immediately but preserves the
			// core's private full base for a later source-qualified delta.
			m.retireSemantic(view, false)
			continue
		}
		if r != nil && (r.source != source || r.revision != view.editor.Revision() || r.contextOwner != contextOwner || contextGeneration > r.contextGeneration || r.result != nil && !r.result.Full && !r.result.Unsupported && (first < r.first || last > r.last)) {
			// Preserve core's old full result as a possible delta base for this view.
			m.retireSemantic(view, false)
			r = nil
		}
		if r == nil {
			// Wait for this file's acknowledged source/context publication.
			// Automatic reads must not occupy a core job during server startup.
			if !service.FileSynchronized(source, contextGeneration) {
				continue
			}
			first, last = max(0, first-8), min(view.editor.LineCount()-1, last+8)
			base, _ := m.lspRequestContext()
			ctx, cancel := context.WithCancel(base)
			view.sequence++
			r = &semanticRequest{view: view, port: port, source: source, revision: view.editor.Revision(), producer: producer, contextOwner: contextOwner, contextGeneration: contextGeneration, first: first, last: last, sequence: view.sequence, ctx: ctx, cancel: cancel,
				span: types.LSPSemanticRange{Start: types.LSPSemanticPosition{Line: first}, End: types.LSPSemanticPosition{Line: last, Character: -1}}}
			view.request = r
			view.retained, view.port = true, port
			commands = append(commands, tea.Tick(180*time.Millisecond, func(time.Time) tea.Msg { return semanticDueMsg{r} }))
		}
		if r.result != nil && !r.installed && r.err == nil && contextGeneration == r.result.Boundary.Context.Generation {
			r.installed = true
			if r.result.Unsupported || r.result.Null {
				view.editor.ClearSemanticTokens()
			} else if err := view.editor.SetSemanticTokens(r.result); err != nil {
				view.editor.ClearSemanticTokens()
				r.err = err
			}
		}
	}
	return tea.Batch(commands...)
}
func (m *App) semanticCurrent(r *semanticRequest) bool {
	if r == nil || r.view == nil || m.semanticViews[r.view.editor] != r.view || r.view.request != r || !r.view.visible || r.view.workspace != m.ws || m.asyncIOStopped.Load() || !m.acceptsLSPGeneration(r.view.generation) || r.ctx.Err() != nil || !r.view.editor.IsOpen() || r.view.editor.Revision() != r.revision || !r.view.editor.MatchesInsightSource(r.source) || m.ws.LSPMgr() == nil || m.ws.LSPMgr().FileProducer(r.source.Path) != r.producer {
		return false
	}
	owner, generation := m.ws.LSPMgr().ContextCursor()
	return owner == r.contextOwner && generation <= r.contextGeneration
}
func (m *App) startSemanticRequest(msg semanticDueMsg) tea.Cmd {
	r := msg.owner
	if !m.semanticCurrent(r) || r.started {
		return nil
	}
	if !m.ws.LSPMgr().FileSynchronized(r.source, r.contextGeneration) {
		m.retireSemantic(r.view, false)
		return nil
	}
	r.started = true
	return m.clientIOCommand(r.ctx, func(parent context.Context) tea.Msg {
		ctx, cancel := context.WithTimeout(parent, 10*time.Second)
		defer cancel()
		result, err := r.port.RequestLSPSemantic(ctx, r.source, r.view.id, r.sequence, r.span)
		return semanticResultMsg{r, result, err}
	})
}
func (m *App) handleSemanticResult(msg semanticResultMsg) tea.Cmd {
	r := msg.owner
	if !m.semanticCurrent(r) {
		return nil
	}
	if msg.err != nil {
		// A busy fault means the core explicitly refused admission. An
		// uncertain cleanup can wrap that fault; never replay that case.
		var uncertain *coreclient.UncertainError
		var fault *coreprotocol.Fault
		if r.started && !errors.As(msg.err, &uncertain) && errors.As(msg.err, &fault) && fault.Code == "busy" {
			r.started = false
			r.busyAttempts++
			delay := retry.Jitter(r.busyAttempts, 250*time.Millisecond, 5*time.Second)
			return tea.Tick(delay, func(time.Time) tea.Msg { return semanticDueMsg{r} })
		}
		r.err = msg.err
		return nil
	}
	result := msg.result
	if result.Boundary.Source != r.source || result.ViewID != r.view.id || result.Sequence != r.sequence || result.Range != r.span || result.WireVersion <= 0 || result.Boundary.Context.Generation < r.contextGeneration || result.Boundary.Producer != r.producer {
		r.err = fmt.Errorf("semantic result lost source, view or provider identity")
		return nil
	}
	r.contextGeneration = result.Boundary.Context.Generation
	r.result = &result
	return nil
}
