package viewmodel

import (
	"context"
	"reflect"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/shellport"
	"crude/tui/editor"
	"crude/tui/screens/codeactions"
	"crude/tui/ui"
	"crude/tui/wm"
	"crude/types"
	"crude/workspace"
)

const codeActionsOverlay ui.OverlayID = "lsp-code-actions"

// Each invocation is a new owner, even at an identical source/selection. Raw
// actions stay in the core. The chooser owns only its detached display evidence.
type codeActionState struct {
	workspace            shellport.Workspace
	editor               *editor.Editor
	path                 string
	revision, generation uint64
	source               types.InsightSource
	selection            codeActionSelection
	start, end           int
	port                 shellport.CodeActions
	reviews              shellport.ReviewedEdits
	screen               *codeactions.Screen
	ctx                  context.Context
	cancel               context.CancelFunc
	deadline             time.Time
	result               *codeActionListResultMsg
	set                  workspace.CodeActionSet
	sequence             uint64
	preparing            bool
}

// Comparing the private view endpoints is constant-time. Converting source byte
// offsets happens once per explicit invocation, never on every Update or frame.
type codeActionSelection struct {
	line, col, sl, sc, el, ec int
	selected                  bool
}

func currentCodeActionSelection(ed *editor.Editor) codeActionSelection {
	s := codeActionSelection{line: ed.CursorLine(), col: ed.CursorCol(), selected: ed.HasSelection()}
	if s.selected {
		s.sl, s.sc, s.el, s.ec = ed.SelectionRange()
	}
	return s
}

type codeActionListResultMsg struct {
	owner *codeActionState
	set   workspace.CodeActionSet
	err   error
}

type codeActionPreparedMsg struct {
	owner    *codeActionState
	sequence uint64
	preview  workspace.ReviewedEditPreview
	err      error
}

type codeActionReleasedMsg struct {
	owner      *codeActionState
	id, digest string
	err        error
}

func (s *codeActionState) retainedSet() workspace.CodeActionSet {
	if s.set.ID != "" || s.result == nil {
		return s.set
	}
	return s.result.set
}

func (m *App) codeActionsVisible(s *codeActionState) bool {
	if s == nil || m.codeActions != s || m.ws != s.workspace || m.overlayHost == nil ||
		m.asyncIOStopped.Load() || !m.acceptsLSPGeneration(s.generation) {
		return false
	}
	fw := m.overlayHost.Window(codeActionsOverlay)
	if fw == nil {
		return false
	}
	frame, ok := fw.Content.(*ui.WindowFrameWidget)
	return ok && frame.Body() == s.screen
}

func (m *App) closeCodeActions() {
	if m.overlayHost != nil && m.overlayHost.IsOpen(codeActionsOverlay) {
		m.overlayHost.Close(codeActionsOverlay)
	}
	if s := m.codeActions; s != nil {
		m.codeActions = nil
		s.cancel()
		m.runDetachedAsyncCmd(releaseCodeActionSet(s, s.retainedSet()))
	}
}

func releaseCodeActionSet(s *codeActionState, set workspace.CodeActionSet) tea.Cmd {
	if s == nil || s.port == nil || set.ID == "" {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		// This only revokes a retained capability. It cannot repeat a mutation.
		// Detach and the domain expiry also revoke it if transport is unavailable.
		err := s.port.ReleaseLSPCodeActions(ctx, set.ID, set.Digest)
		return codeActionReleasedMsg{owner: s, id: set.ID, digest: set.Digest, err: err}
	}
}

func (m *App) requestCodeActionsCmd() tea.Cmd {
	m.closeCodeActions()
	m.closeSignatureHelp()
	ed := m.EditorState.Editor
	port, supported := m.ws.(shellport.CodeActions)
	reviews, recoverable := m.ws.(shellport.ReviewedEdits)
	if ed == nil || !ed.IsOpen() || !supported || !recoverable || m.ws.LSPMgr() == nil ||
		!m.ws.LSPMgr().ServerReady() {
		m.Feedback.NotifyInfo("Code actions need a connected shared document and language server")
		return nil
	}
	source, current := ed.CurrentInsightSource()
	state := ed.DocumentState()
	if !current || source.Kind != "canonical" || ed.Modified() || state.PendingEdits != 0 ||
		state.InFlight || state.Conflict != nil {
		m.Feedback.NotifyInfo("Save and synchronize the shared document before requesting code actions")
		return nil
	}
	start, end, exact := ed.SourceByteRange()
	if !exact {
		m.Feedback.NotifyInfo("Code actions require exact source boundaries at the caret or selection")
		return nil
	}
	base, generation := m.lspRequestContext()
	ctx, cancel := context.WithCancel(base)
	s := &codeActionState{
		workspace: m.ws, editor: ed, path: ed.FilePath(), revision: ed.Revision(),
		generation: generation, source: source, selection: currentCodeActionSelection(ed),
		start: start, end: end, port: port, reviews: reviews,
		screen: codeactions.New(m.Theme), ctx: ctx, cancel: cancel, deadline: time.Now().Add(10 * time.Second),
	}
	m.codeActions = s
	w, h := min(104, max(1, m.width-4)), min(28, max(1, m.height-4))
	frame := ui.NewWindowFrameWidget(m.Theme, "code-actions/frame", ui.FrameDialog, "Code actions", s.screen, nil, false, w, h)
	frame.Focus(s.screen.DefaultFocus())
	fw := wm.NewFloatingWindow(string(codeActionsOverlay), "", frame, w, h,
		wm.WithDecoration(true), wm.WithMovable(true), wm.WithResizable(true), wm.WithModal(true),
		wm.WithFocusable(true), wm.WithClosable(false), wm.WithClickOutside(wm.ClickOutsideIgnore))
	wm.CenterOnScreen(fw, m.width, m.height)
	m.overlayHost.Open(codeActionsOverlay, fw, ui.ModalDialogPolicy(), func() {
		if m.codeActions == s {
			m.codeActions = nil
			s.cancel()
			m.runDetachedAsyncCmd(releaseCodeActionSet(s, s.retainedSet()))
		}
	})
	return func() tea.Msg {
		requestCtx, stop := context.WithTimeout(s.ctx, 10*time.Second)
		defer stop()
		set, err := port.RequestLSPCodeActions(requestCtx, source, start, end)
		return codeActionListResultMsg{owner: s, set: set, err: err}
	}
}

func (m *App) markCodeActionsDirty(s *codeActionState) {
	if !m.codeActionsVisible(s) {
		return
	}
	fw := m.overlayHost.Window(codeActionsOverlay)
	frame := fw.Content.(*ui.WindowFrameWidget)
	frame.Host().MarkLayout()
	// SetSet changes the mounted child tree from reader to list. Register and
	// lay out that tree before focusing its newly present control; otherwise
	// Enter would still activate the loading view's Close button.
	frame.Host().Layout()
	frame.Focus(s.screen.DefaultFocus())
	fw.MarkContentDirty()
	m.forceFrame()
}

func (m *App) handleCodeActionListResult(msg codeActionListResultMsg) tea.Cmd {
	m.syncCodeActions()
	s := msg.owner
	if !m.codeActionsVisible(s) || s.ctx.Err() != nil {
		return releaseCodeActionSet(s, msg.set)
	}
	if msg.err != nil {
		s.deadline = time.Time{}
		s.screen.SetError(msg.err.Error())
		m.markCodeActionsDirty(s)
		return releaseCodeActionSet(s, msg.set)
	}
	if msg.set.Start != s.start || msg.set.End != s.end {
		s.deadline = time.Time{}
		s.screen.SetError("The result does not identify the original source selection.")
		m.markCodeActionsDirty(s)
		return releaseCodeActionSet(s, msg.set)
	}
	// Empty/null lists obey the very same boundary barrier as positive answers.
	s.result = &msg
	m.syncCodeActions()
	return nil
}

func (m *App) syncCodeActions() {
	s := m.codeActions
	if s == nil {
		return
	}
	if !m.codeActionsVisible(s) ||
		!m.acceptsLSPDocument(s.generation, s.path, s.editor, s.revision) ||
		!s.editor.MatchesInsightSource(s.source) || s.editor.Modified() ||
		currentCodeActionSelection(s.editor) != s.selection ||
		m.ws.LSPMgr() == nil || !m.ws.LSPMgr().ServerReady() {
		m.closeCodeActions()
		return
	}
	if !s.deadline.IsZero() && time.Now().After(s.deadline) {
		s.cancel()
		m.runDetachedAsyncCmd(releaseCodeActionSet(s, s.retainedSet()))
		s.result = nil
		s.deadline = time.Time{}
		s.screen.SetError("The action request timed out or its exact project context could not be observed.")
		m.markCodeActionsDirty(s)
		return
	}
	if s.result == nil {
		return
	}
	b := s.result.set.Boundary
	owner, generation := m.ws.LSPMgr().ContextCursor()
	if owner == "" || owner != b.Source.WorkspaceGeneration || generation > b.Context.Generation || b.Source != s.source {
		// It is not stored in s.set until the replica barrier accepts it.
		m.runDetachedAsyncCmd(releaseCodeActionSet(s, s.result.set))
		s.result = nil
		m.closeCodeActions()
		return
	}
	if generation < b.Context.Generation {
		return
	}
	if s.set.ID != "" {
		return
	}
	s.set = s.result.set
	s.deadline = time.Time{}
	s.screen.SetSet(s.set)
	m.markCodeActionsDirty(s)
}

func (m *App) prepareCodeAction(msg codeactions.PrepareMsg) tea.Cmd {
	m.syncCodeActions()
	s := m.codeActions
	if !m.codeActionsVisible(s) || msg.Target != s.screen || s.set.ID == "" ||
		s.preparing || s.ctx.Err() != nil || msg.Index < 0 || msg.Index >= len(s.set.Items) || !s.set.Items[msg.Index].CanReview {
		return nil
	}
	s.preparing = true
	s.sequence++
	sequence := s.sequence
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(s.ctx, 30*time.Second)
		defer cancel()
		preview, err := s.port.PrepareLSPCodeAction(ctx, s.set.ID, s.set.Digest, msg.Index)
		return codeActionPreparedMsg{owner: s, sequence: sequence, preview: preview, err: err}
	}
}

func (m *App) handleCodeActionPrepared(msg codeActionPreparedMsg) tea.Cmd {
	m.syncCodeActions()
	s := msg.owner
	if s == nil {
		return nil
	}
	r := reviewedEditDialogRequest{preview: msg.preview, port: s.reviews}
	if !m.codeActionsVisible(s) || msg.sequence != s.sequence {
		return releaseReviewedEditPreview(r)
	}
	s.preparing = false
	if msg.err != nil || msg.preview.ID == "" || msg.preview.Digest == "" || len(msg.preview.Files) != 1 ||
		!reflect.DeepEqual(msg.preview.Boundary, s.set.Boundary) {
		detail := "The provider did not return an exact, recoverable current-document review."
		if msg.err != nil {
			detail = msg.err.Error()
		}
		s.screen.SetError(detail)
		m.markCodeActionsDirty(s)
		return releaseReviewedEditPreview(r)
	}
	r.owner, r.revision, r.generation, r.filePath = s.editor, s.revision, s.generation, s.path
	r.title, r.intro = "Review code action", "Review the exact action and literal changed spans below."
	r.applyLabel, r.committedLabel, r.operationLabel = "Apply changes", "Applied changes", "Code action"
	set := s.set
	r.retire = func(ctx context.Context) error {
		return s.port.ReleaseLSPCodeActions(ctx, set.ID, set.Digest)
	}
	// Transfer the original list's lifetime to the one shared mutation dialog.
	// Closing the chooser alone would revoke its review's Apply capability.
	m.codeActions = nil
	s.cancel()
	m.overlayHost.Close(codeActionsOverlay)
	m.showReviewedEditDialog(r)
	return nil
}

func (m *App) closeCodeActionScreen(msg codeactions.CloseMsg) tea.Cmd {
	if s := m.codeActions; m.codeActionsVisible(s) && msg.Target == s.screen {
		// Explicit Close awaits this original release for its semantic receipt;
		// the modal may retire itself, but cannot acknowledge unknown cleanup.
		m.codeActions = nil
		s.cancel()
		m.overlayHost.Close(codeActionsOverlay)
		return releaseCodeActionSet(s, s.retainedSet())
	}
	return nil
}

func (m *App) handleCodeActionReleased(msg codeActionReleasedMsg) {
	if msg.err != nil && msg.owner != nil && msg.owner.workspace == m.ws && m.acceptsLSPGeneration(msg.owner.generation) {
		m.Feedback.NotifyInfo("Code-action list release is unconfirmed. Its original capability expires automatically; no source edit was replayed.")
	}
}
