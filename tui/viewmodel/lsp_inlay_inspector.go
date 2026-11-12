package viewmodel

import (
	"context"
	"errors"
	"reflect"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/screens/inlay"
	"crude/tui/ui"
	"crude/types"
)

type inlayInspectorState struct {
	request             *inlayRequest
	screen              *inlay.Screen
	ctx                 context.Context
	cancel              context.CancelFunc
	focus               FocusTarget
	shown               bool
	requestFailureShown bool // One error transition for this retained request.
	reading             bool
	sequence            uint64
	index               int
	readCancel          context.CancelFunc
	deadline            time.Time
}

type inlayInspectionMsg struct {
	owner      *inlayInspectorState
	sequence   uint64
	index      int
	inspection types.LSPInlayInspection
	err        error
}

func (m *App) closeInlayInspector() {
	if s := m.inlayInspector; s != nil {
		m.inlayInspector = nil
		s.cancel()
		if s.readCancel != nil {
			s.readCancel()
		}
	}
	if m.overlayHost != nil {
		m.overlayHost.Close(ui.OverlayInlayHints)
	}
}

func (m *App) openInlayInspectorCmd() tea.Cmd {
	m.closeInlayInspector()
	m.closeNavigation()
	m.closeSignatureHelp()
	m.closeCodeActions()
	if m.EditorState == nil || m.ws == nil || m.ws.LSPMgr() == nil {
		return nil
	}
	if enabled, _ := m.ws.LSPMgr().InlayHintsState(); !enabled {
		m.Feedback.NotifyInfo("Inlay hints are off or blocked; enable the read-only feature in Code Intelligence settings")
		return nil
	}
	work := m.syncInlayHints()
	view := m.inlayViews[m.EditorState.Editor]
	if view == nil || view.hidden || view.request == nil {
		m.Feedback.NotifyInfo("Inlay hints are not ready. Show them in the editor, then try again.")
		return work
	}
	base, _ := m.lspRequestContext()
	ctx, cancel := context.WithCancel(base)
	s := &inlayInspectorState{request: view.request, screen: inlay.New(m.Theme), ctx: ctx, cancel: cancel, index: -1}
	if m.FocusStack != nil {
		s.focus = m.FocusStack.Top()
	}
	s.screen.SetLoading()
	m.inlayInspector = s
	w, h := min(104, max(1, m.width-4)), min(28, max(1, m.height-4))
	frame, fw := m.newEditorPopover(ui.OverlayInlayHints, "Inlay hints", s.screen, s.screen.DefaultFocus(), ui.Rect{W: w, H: h})
	if !m.placeEditorPickerHeight(fw, frame, view.editor, 18) {
		m.closeInlayInspector()
		return work
	}
	m.overlayHost.Open(ui.OverlayInlayHints, fw, ui.PopoverPolicy(true), func() {
		if m.inlayInspector == s {
			m.inlayInspector = nil
			s.cancel()
			if s.readCancel != nil {
				s.readCancel()
			}
		}
	})
	m.updateInlayInspector(view.request)
	return work
}

func (m *App) inlayInspectorCurrent(s *inlayInspectorState) bool {
	if s == nil || m.inlayInspector != s || s.ctx.Err() != nil || m.overlayHost == nil ||
		m.asyncIOStopped.Load() || m.termDockFocused || m.sessionDeckCapturesInput() {
		return false
	}
	r := s.request
	if r == nil || r.view == nil || m.inlayViews[r.view.editor] != r.view || r.view.request != r ||
		r.view.hidden || !r.view.visible || r.view.workspace != m.ws || !m.acceptsLSPGeneration(r.view.generation) ||
		!r.view.editor.IsOpen() || r.view.editor.Revision() != r.revision || !r.view.editor.MatchesInsightSource(r.source) ||
		m.ws.LSPMgr() == nil || m.ws.LSPMgr().FileProducer(r.source.Path) != r.producer {
		return false
	}
	enabled, epoch := m.ws.LSPMgr().InlayHintsState()
	owner, generation := m.ws.LSPMgr().ContextCursor()
	if !enabled || epoch != r.settingEpoch || owner != r.contextOwner || generation > r.contextGeneration ||
		m.FocusStack != nil && m.FocusStack.Top() != s.focus {
		return false
	}
	fw := m.overlayHost.Window(ui.OverlayInlayHints)
	if fw == nil || m.focusedPopup() != fw {
		return false
	}
	frame, ok := fw.Content.(*ui.WindowFrameWidget)
	return ok && frame.Body() == s.screen
}

func (m *App) markInlayInspectorDirty(s *inlayInspectorState) {
	if s == nil || m.inlayInspector != s || m.overlayHost == nil {
		return
	}
	if fw := m.overlayHost.Window(ui.OverlayInlayHints); fw != nil {
		frame := fw.Content.(*ui.WindowFrameWidget)
		frame.Host().MarkLayout()
		frame.Host().Layout()
		frame.Focus(s.screen.DefaultFocus())
		fw.MarkContentDirty()
		m.forceFrame()
	}
}

// A shell logger may block on a flush or its backing store. Retain the exact
// logger and failure on the App's joined async owner; never do that I/O on Update.
func (m *App) logInlayFailure(operation string, err error) {
	if err == nil || m.ws == nil {
		return
	}
	logger := m.ws.Logger()
	if logger == nil {
		return
	}
	m.runDetachedAsyncCmd(func() tea.Msg {
		logger.Warn("LSP", "request_failed", "operation", operation, "error", err.Error())
		return nil
	})
}

func (m *App) updateInlayInspector(r *inlayRequest) {
	s := m.inlayInspector
	if s == nil || s.request != r {
		return
	}
	if r.err != nil {
		if s.requestFailureShown {
			return
		}
		// A later reprojection can fail after the reader was shown. Retire
		// its selected read before replacing the stale success presentation.
		s.requestFailureShown = true
		s.sequence++
		if s.readCancel != nil {
			s.readCancel()
			s.readCancel = nil
		}
		s.reading, s.deadline, s.index = false, time.Time{}, -1
		m.logInlayFailure("inlay hints", r.err)
		s.screen.SetError()
		s.shown = true
	} else if !s.shown && r.result != nil && r.installed {
		s.screen.SetResult(*r.result)
		s.shown = true
	} else {
		return
	}
	m.markInlayInspectorDirty(s)
}

func (m *App) syncInlayInspector() {
	s := m.inlayInspector
	if s == nil {
		return
	}
	if !m.inlayInspectorCurrent(s) {
		m.closeInlayInspector()
		return
	}
	if s.reading && !s.deadline.IsZero() && time.Now().After(s.deadline) {
		if s.readCancel != nil {
			s.readCancel()
			s.readCancel = nil
		}
		s.sequence++
		s.reading, s.deadline, s.index = false, time.Time{}, -1
		s.screen.SetError()
		m.markInlayInspectorDirty(s)
	}
	m.updateInlayInspector(s.request)
}

func (m *App) inspectInlayHintCmd(msg inlay.InspectMsg) tea.Cmd {
	s := m.inlayInspector
	if !m.inlayInspectorCurrent(s) || msg.Target != s.screen || !s.shown || s.reading ||
		s.request.result == nil || !s.request.installed || s.request.ctx.Err() != nil ||
		msg.Index < 0 || msg.Index >= len(s.request.result.Hints) ||
		msg.Resolve && !s.request.result.ResolveSupported {
		return nil
	}
	if selected, ok := s.screen.SelectedIndex(); !ok || selected != msg.Index {
		return nil
	}
	s.sequence++
	s.index, s.reading, s.deadline = msg.Index, true, time.Now().Add(10*time.Second)
	ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
	s.readCancel = cancel
	s.screen.SetBusy(true)
	m.markInlayInspectorDirty(s)
	sequence, result, port := s.sequence, *s.request.result, s.request.port
	return m.clientIOCommand(ctx, func(parent context.Context) tea.Msg {
		defer cancel()
		detail, err := port.InspectLSPInlayHint(parent, result.ID, result.Digest, msg.Index, msg.Resolve)
		if cancelled := parent.Err(); cancelled != nil {
			detail, err = types.LSPInlayInspection{}, errors.Join(err, cancelled)
		}
		return inlayInspectionMsg{owner: s, sequence: sequence, index: msg.Index, inspection: detail, err: err}
	})
}

func (m *App) handleInlayInspection(msg inlayInspectionMsg) bool {
	s := msg.owner
	if !m.inlayInspectorCurrent(s) || !s.reading || s.sequence != msg.sequence || s.index != msg.index {
		return false
	}
	if selected, ok := s.screen.SelectedIndex(); !ok || selected != msg.index {
		return false
	}
	s.reading, s.deadline = false, time.Time{}
	if s.readCancel != nil {
		s.readCancel()
		s.readCancel = nil
	}
	if msg.err != nil {
		m.logInlayFailure("inlay hint details", msg.err)
		s.screen.SetError()
		m.markInlayInspectorDirty(s)
		return false
	}
	detail, result := msg.inspection, s.request.result
	if result == nil || detail.SetID != result.ID || detail.Digest != result.Digest || detail.Index != msg.index ||
		detail.WireVersion != result.WireVersion || detail.Range != result.Range ||
		!reflect.DeepEqual(detail.Boundary, result.Boundary) {
		s.screen.SetError()
		m.markInlayInspectorDirty(s)
		return false
	}
	s.screen.SetInspection(detail)
	m.markInlayInspectorDirty(s)
	return s.screen.InDetail()
}

func (m *App) backFromInlayInspection(msg inlay.BackMsg) {
	s := m.inlayInspector
	if !m.inlayInspectorCurrent(s) || msg.Target != s.screen || !s.screen.InList() {
		return
	}
	s.sequence++
	if s.readCancel != nil {
		s.readCancel()
		s.readCancel = nil
	}
	s.reading, s.deadline, s.index = false, time.Time{}, -1
	s.screen.SetBusy(false)
	m.markInlayInspectorDirty(s)
}
