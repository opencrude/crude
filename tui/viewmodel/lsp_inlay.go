package viewmodel

import (
	"context"
	"fmt"
	"time"
	"unicode/utf16"

	tea "charm.land/bubbletea/v2"

	"crude/shellport"
	"crude/tui/editor"
	"crude/types"
)

const inlayDebounce = 180 * time.Millisecond

// A view-local hide is not a settings or Trust override. The core remains the
// source/provider/context and original-object authority; this owner retains only
// a private request and its detached presentation.
type inlayViewState struct {
	editor          *editor.Editor
	workspace       shellport.Workspace
	generation      uint64
	id              string
	sequence        uint64
	hidden, visible bool
	request         *inlayRequest
}

type inlayRequest struct {
	view                            *inlayViewState
	port                            shellport.InlayHints
	source                          types.InsightSource
	revision                        uint64
	contextOwner                    string
	contextGeneration, settingEpoch uint64
	first, last                     int
	span                            types.LSPInlayRange
	sequence                        uint64
	ctx                             context.Context
	cancel                          context.CancelFunc
	deadline                        time.Time
	started, installed              bool
	result                          *types.LSPInlaySet
	err                             error
}

type inlayDueMsg struct{ owner *inlayRequest }
type inlayResultMsg struct {
	owner  *inlayRequest
	result types.LSPInlaySet
	err    error
}
type inlayReleasedMsg struct{ err error }

func (m *App) inlayView(ed *editor.Editor) *inlayViewState {
	if ed == nil || !ed.IsOpen() {
		return nil
	}
	if state := m.inlayViews[ed]; state != nil {
		return state
	}
	m.inlayViewSequence++
	state := &inlayViewState{editor: ed, workspace: m.ws, generation: m.workspaceSwitchGen.Load(),
		id: fmt.Sprintf("editor-inlays-%d-%d", m.workspaceSwitchGen.Load(), m.inlayViewSequence)}
	if m.inlayViews == nil {
		m.inlayViews = make(map[*editor.Editor]*inlayViewState)
	}
	m.inlayViews[ed] = state
	return state
}

func (m *App) retireInlayRequest(view *inlayViewState) {
	request := view.request
	if request == nil {
		return
	}
	view.request = nil
	request.cancel()
	view.editor.ClearInlayHints()
	if s := m.inlayInspector; s != nil && s.request == request {
		m.closeInlayInspector()
	}
	m.runDetachedAsyncCmd(func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return inlayReleasedMsg{err: request.port.ReleaseLSPInlayHints(ctx, view.id, request.sequence)}
	})
}

func (m *App) resetInlayHints() {
	m.closeInlayInspector()
	for _, view := range m.inlayViews {
		m.retireInlayRequest(view)
		view.editor.ClearInlayHints()
	}
	m.inlayViews = nil
}

// syncInlayHints observes only constant-size source/geometry/cursor metadata per
// visible editor. It neither exports text nor rebuilds projections on a tick.
// Admission captures just the requested end line once; work is debounced without
// ever delaying canonical text mutation or presentation.
func (m *App) syncInlayHints() tea.Cmd {
	var commands []tea.Cmd
	for ed, view := range m.inlayViews {
		view.visible = false
		if !ed.IsOpen() || view.workspace != m.ws || !m.acceptsLSPGeneration(view.generation) {
			m.retireInlayRequest(view)
			delete(m.inlayViews, ed)
		}
	}
	if m.ws == nil || m.ws.LSPMgr() == nil || m.asyncIOStopped.Load() {
		for _, view := range m.inlayViews {
			m.retireInlayRequest(view)
		}
		return nil
	}
	service := m.ws.LSPMgr()
	enabled, epoch := service.InlayHintsState()
	enabled = enabled && service.ServerReady()
	if enabled && m.lspRequestCtx == nil {
		// Initialize the workspace owner before admitting any view. Its reset
		// also retires the preceding generation's ephemeral inlay map.
		m.lspRequestContext()
	}
	mark := func(ed *editor.Editor) {
		if ed == nil || !ed.IsOpen() {
			return
		}
		state := m.inlayViews[ed]
		if state == nil && enabled {
			state = m.inlayView(ed)
		}
		if state != nil {
			state.visible = true
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
	port, connected := m.ws.(shellport.InlayHints)
	for _, view := range m.inlayViews {
		source, acknowledged := view.editor.CurrentInsightSource()
		first, last, painted := view.editor.VisibleSourceLines()
		if !enabled || !connected || !view.visible || view.hidden || !acknowledged || !painted ||
			contextOwner == "" || contextOwner != source.WorkspaceGeneration {
			m.retireInlayRequest(view)
			continue
		}
		request := view.request
		if request != nil && (request.source != source || request.revision != view.editor.Revision() ||
			request.settingEpoch != epoch || request.contextOwner != contextOwner ||
			contextGeneration > request.contextGeneration || first < request.first || last > request.last) {
			m.retireInlayRequest(view)
			request = nil
		}
		if request != nil && request.result != nil && time.Now().After(request.result.ExpiresAt) {
			m.retireInlayRequest(view)
			request = nil
		}
		if request == nil {
			first, last = max(0, first-8), min(view.editor.LineCount()-1, last+8)
			endCol := 0
			for _, r := range view.editor.LineText(last) {
				endCol += utf16.RuneLen(r)
			}
			base, _ := m.lspRequestContext()
			ctx, cancel := context.WithCancel(base)
			view.sequence++
			request = &inlayRequest{view: view, port: port, source: source, revision: view.editor.Revision(),
				contextOwner: contextOwner, contextGeneration: contextGeneration, settingEpoch: epoch,
				first: first, last: last, sequence: view.sequence, ctx: ctx, cancel: cancel,
				span: types.LSPInlayRange{Start: types.LSPInlayPosition{Line: first},
					End: types.LSPInlayPosition{Line: last, Character: endCol}}}
			view.request = request
			commands = append(commands, tea.Tick(inlayDebounce, func(time.Time) tea.Msg { return inlayDueMsg{owner: request} }))
		}
		if !request.deadline.IsZero() && time.Now().After(request.deadline) {
			request.cancel()
			request.deadline = time.Time{}
			request.err = fmt.Errorf("inlay request or source-context observation timed out; refresh to try again")
			m.updateInlayInspector(request)
		}
		if request.installed && request.err == nil && !view.editor.HasInlayProjection() {
			// Width-profile invalidation drops only the cell projection. Exact
			// retained source evidence can prepare it again without another RPC.
			request.installed = false
		}
		if request.result != nil && !request.installed && request.err == nil &&
			contextGeneration == request.result.Boundary.Context.Generation {
			request.installed = true
			if err := view.editor.SetInlayHints(*request.result); err != nil {
				// Retain read-only original evidence for the inspector, but paint
				// none of a set whose exact anchors cannot be represented.
				view.editor.ClearInlayHints()
				request.err = fmt.Errorf("inline labels were not rendered: %w", err)
			}
			request.deadline = time.Time{}
			m.updateInlayInspector(request)
		}
	}
	m.syncInlayInspector()
	return tea.Batch(commands...)
}

func (m *App) inlayRequestCurrent(r *inlayRequest) bool {
	if r == nil || r.view == nil || m.inlayViews[r.view.editor] != r.view || r.view.request != r ||
		r.view.hidden || !r.view.visible || r.view.workspace != m.ws || m.asyncIOStopped.Load() ||
		!m.acceptsLSPGeneration(r.view.generation) || r.ctx.Err() != nil ||
		!r.view.editor.IsOpen() || r.view.editor.Revision() != r.revision || !r.view.editor.MatchesInsightSource(r.source) ||
		m.ws.LSPMgr() == nil || !m.ws.LSPMgr().ServerReady() {
		return false
	}
	enabled, epoch := m.ws.LSPMgr().InlayHintsState()
	owner, generation := m.ws.LSPMgr().ContextCursor()
	return enabled && epoch == r.settingEpoch && owner == r.contextOwner && generation <= r.contextGeneration
}

func (m *App) startInlayRequest(msg inlayDueMsg) tea.Cmd {
	r := msg.owner
	if !m.inlayRequestCurrent(r) || r.started {
		return nil
	}
	r.started, r.deadline = true, time.Now().Add(10*time.Second)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(r.ctx, 10*time.Second)
		defer cancel()
		result, err := r.port.RequestLSPInlayHints(ctx, r.source, r.view.id, r.sequence, r.span)
		return inlayResultMsg{owner: r, result: result, err: err}
	}
}

func (m *App) handleInlayResult(msg inlayResultMsg) {
	r := msg.owner
	if !m.inlayRequestCurrent(r) {
		return
	}
	if msg.err != nil {
		r.deadline, r.err = time.Time{}, msg.err
		m.updateInlayInspector(r)
		return
	}
	result := msg.result
	if result.ID == "" || len(result.Digest) != 64 || result.Boundary.Source != r.source ||
		result.ViewID != r.view.id || result.Sequence != r.sequence || result.Range != r.span ||
		result.WireVersion <= 0 || result.Boundary.Context.Generation < r.contextGeneration ||
		result.ExpiresAt.IsZero() || !time.Now().Before(result.ExpiresAt) {
		r.deadline = time.Time{}
		r.err = fmt.Errorf("inlay result lost its original source, view, range or context identity")
		m.updateInlayInspector(r)
		return
	}
	r.contextGeneration = result.Boundary.Context.Generation
	r.result = &result
	// The independent context replica may still lag. syncInlayHints installs
	// only after its exact cursor reaches this boundary.
}

func (m *App) setInlayViewHidden(hidden bool) {
	if m.EditorState == nil {
		return
	}
	view := m.inlayView(m.EditorState.Editor)
	if view == nil {
		return
	}
	view.hidden = hidden
	m.retireInlayRequest(view)
	if hidden {
		m.Feedback.NotifyInfo("Inlay hints hidden in this editor view")
		return
	}
	if m.ws.LSPMgr() == nil {
		m.Feedback.NotifyInfo("Inlay hints need a connected language service")
		return
	}
	if enabled, _ := m.ws.LSPMgr().InlayHintsState(); !enabled {
		m.Feedback.NotifyInfo("Enable Inlay hints in Code Intelligence settings first; Show does not change settings or workspace Trust")
	}
}
