package viewmodel

import (
	"context"
	"reflect"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/fileio"
	"crude/shellport"
	"crude/tui/editor"
	"crude/tui/screens/codeactions"
	"crude/tui/ui"
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

type codeActionURLResultMsg struct {
	url string
	err error
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
	m.closeNavigation()
	m.closeSignatureHelp()
	ed := m.EditorState.Editor
	port, supported := m.ws.(shellport.CodeActions)
	reviews, recoverable := m.ws.(shellport.ReviewedEdits)
	if ed == nil || !ed.IsOpen() || !supported || !recoverable || !m.manualLSPAvailable() {
		m.Feedback.NotifyInfo("The language server is not running for this file. Open Language servers to start it.")
		return nil
	}
	source, current := ed.CurrentInsightSource()
	state := ed.DocumentState()
	if !current || source.Kind != "canonical" || ed.Modified() || state.PendingEdits != 0 ||
		state.InFlight || state.Conflict != nil {
		if ed.Modified() {
			m.Feedback.NotifyInfo("Save the file, then open Code actions again.")
		} else {
			m.Feedback.NotifyInfo("The file is still syncing. Try again in a moment.")
		}
		return nil
	}
	start, end, exact := ed.SourceByteRange()
	if !exact {
		m.Feedback.NotifyInfo("Code actions are not available at this position.")
		return nil
	}
	base, generation := m.lspRequestContext()
	ctx, cancel := context.WithCancel(base)
	s := &codeActionState{
		workspace: m.ws, editor: ed, path: ed.FilePath(), revision: ed.Revision(),
		generation: generation, source: source, selection: currentCodeActionSelection(ed),
		start: start, end: end, port: port, reviews: reviews,
		screen: codeactions.New(m.Theme), ctx: ctx, cancel: cancel, deadline: time.Now().Add(workspace.ManualLSPRequestTimeout + 5*time.Second),
	}
	m.codeActions = s
	w, h := min(104, max(1, m.width-4)), min(28, max(1, m.height-4))
	frame, fw := m.newEditorPopover(codeActionsOverlay, "Code actions", s.screen, s.screen.DefaultFocus(), ui.Rect{W: w, H: h})
	if !m.placeEditorPicker(fw, frame, ed) {
		m.closeCodeActions()
		return nil
	}
	m.overlayHost.Open(codeActionsOverlay, fw, ui.PopoverPolicy(true), func() {
		if m.codeActions == s {
			m.codeActions = nil
			s.cancel()
			m.runDetachedAsyncCmd(releaseCodeActionSet(s, s.retainedSet()))
		}
	})
	return func() tea.Msg {
		requestCtx, stop := context.WithTimeout(s.ctx, workspace.ManualLSPRequestTimeout)
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
	frame.Frame().SetTitle(s.screen.Title())
	m.placeEditorPicker(fw, frame, s.editor)
	frame.Host().MarkLayout()
	// SetSet changes the mounted child tree from reader to list. Register and
	// lay out that tree before focusing its newly present control; otherwise
	// Enter must target the delivered list, not the previous loading reader.
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
		m.logLSPFailure("code-actions", msg.err)
		s.screen.SetError("Could not get code actions. Try again.")
		m.markCodeActionsDirty(s)
		return releaseCodeActionSet(s, msg.set)
	}
	if msg.set.Start != s.start || msg.set.End != s.end {
		s.deadline = time.Time{}
		s.screen.SetError("The file changed during the request. Try Code actions again.")
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
		m.ws.LSPMgr() == nil || s.set.ID != "" && !m.lspProducerConnected(s.set.Boundary.Producer.InstanceID) {
		m.closeCodeActions()
		return
	}
	if !s.deadline.IsZero() && time.Now().After(s.deadline) {
		s.cancel()
		m.runDetachedAsyncCmd(releaseCodeActionSet(s, s.retainedSet()))
		s.result = nil
		s.deadline = time.Time{}
		s.screen.SetError("The language server did not answer in time. Try Code actions again.")
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
	if !m.lspProducerConnected(b.Producer.InstanceID) {
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
		ctx, cancel := context.WithTimeout(s.ctx, workspace.ManualLSPRequestTimeout)
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
	if msg.err != nil {
		m.logLSPFailure("code-action-review", msg.err)
		s.screen.SetError("Crude cannot run this action. Choose another action.")
		m.markCodeActionsDirty(s)
		return releaseReviewedEditPreview(r)
	}
	if msg.preview.ID == "" {
		// A command action with no source edit has already completed exactly
		// once. Its message or browser destination is the visible outcome.
		message, url := msg.preview.Message, msg.preview.URL
		if message == "" && url == "" {
			s.screen.SetError("The action completed without a visible result.")
			m.markCodeActionsDirty(s)
			return nil
		}
		m.codeActions = nil
		s.cancel()
		m.overlayHost.Close(codeActionsOverlay)
		if message != "" && m.Feedback != nil {
			m.Feedback.NotifyInfo(message)
		}
		release := releaseCodeActionSet(s, s.retainedSet())
		if url == "" {
			return release
		}
		return tea.Batch(release, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			return codeActionURLResultMsg{url: url, err: fileio.OpenBrowserURL(ctx, url)}
		})
	}
	if msg.preview.Digest == "" || len(msg.preview.Files) == 0 ||
		!reflect.DeepEqual(msg.preview.Boundary, s.set.Boundary) {
		s.screen.SetError("Crude cannot preview this action. Choose another action.")
		m.markCodeActionsDirty(s)
		return releaseReviewedEditPreview(r)
	}
	r.owner, r.revision, r.generation, r.filePath = s.editor, s.revision, s.generation, s.path
	r.title, r.intro = "Review code action", "Review the changes before applying them."
	r.applyLabel, r.committedLabel = "Apply changes", "Applied changes"
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
		// the picker may retire itself, but cannot acknowledge unknown cleanup.
		m.codeActions = nil
		s.cancel()
		m.overlayHost.Close(codeActionsOverlay)
		return releaseCodeActionSet(s, s.retainedSet())
	}
	return nil
}

func (m *App) handleCodeActionReleased(msg codeActionReleasedMsg) {
	if msg.err != nil && msg.owner != nil && msg.owner.workspace == m.ws && m.acceptsLSPGeneration(msg.owner.generation) {
		m.logLSPFailure("code-action-release", msg.err)
	}
}

func (m *App) handleCodeActionURLResult(msg codeActionURLResultMsg) {
	if msg.err != nil && m.Feedback != nil {
		m.Feedback.NotifyError("Could not open the action's page: " + msg.err.Error())
	}
}
