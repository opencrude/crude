package viewmodel

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/shellport"
	"crude/tui/editor"
	"crude/tui/screens/completion"
	"crude/tui/ui"
	"crude/tui/wm"
	"crude/types"
)

const completionPopupID = "completion-popup"
const completionViewID = "editor-completion"

// The native List owns selection and scrolling. This state retains only the
// exact invocation and asynchronous observations; labels/cursors are not IDs.
type completionState struct {
	workspace                               shellport.Workspace
	editor                                  *editor.Editor
	source                                  types.InsightSource
	selection                               codeActionSelection
	focus                                   FocusTarget
	revision, generation, feature, sequence uint64
	contextGeneration                       uint64
	line, col                               int
	automatic, released                     bool
	port                                    shellport.Completions
	ctx                                     context.Context
	cancel                                  context.CancelFunc
	deadline                                time.Time
	set                                     *types.LSPCompletionSet
	screen                                  *completion.Screen
	pending                                 *completionRead
	observed                                map[string]bool
	requestErr                              error
}

type completionRead struct {
	owner  *completionState
	itemID string
	accept bool
	cancel context.CancelFunc
}

type completionResultMsg struct {
	owner *completionState
	set   types.LSPCompletionSet
	err   error
}

type completionReadMsg struct {
	read   *completionRead
	result types.LSPCompletionInspection
	err    error
}

type completionReleasedMsg struct {
	owner *completionState
	err   error
}

func (m *App) retireCompletion(s *completionState) {
	if s == nil || s.released {
		return
	}
	s.released = true
	s.cancel()
	if s.pending != nil {
		s.pending.cancel()
	}
	m.runDetachedAsyncCmd(func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return completionReleasedMsg{owner: s, err: s.port.ReleaseLSPCompletions(ctx, completionViewID, s.sequence)}
	})
}

func (m *App) closeCompletionPopup() {
	s := m.completion
	m.completion = nil
	m.retireCompletion(s)
	if m.overlayHost != nil {
		m.overlayHost.Close(ui.OverlayCompletion)
	}
}

func (m *App) requestCompletionCmd() tea.Cmd {
	if m.openSnippetChoices() {
		return nil
	}
	return m.requestCompletion(false)
}

func (m *App) requestCompletion(automatic bool) tea.Cmd {
	m.closeCompletionPopup()
	ed := m.EditorState.Editor
	port, connected := m.ws.(shellport.Completions)
	if ed == nil || !ed.IsOpen() || !connected || m.ws.LSPMgr() == nil || !m.ws.LSPMgr().ServerReady() {
		if !automatic {
			m.Feedback.NotifyInfo("Completion needs a connected shared document and language server")
		}
		return nil
	}
	enabled, feature := m.ws.LSPMgr().CompletionsState()
	source, current := ed.CurrentInsightSource()
	if !enabled || !current || source.Kind != "canonical" || automatic && ed.SnippetActive() {
		if !automatic {
			m.Feedback.NotifyInfo("Completion waits for acknowledged source text and enabled language intelligence")
		}
		return nil
	}
	m.closeSignatureHelp()
	base, generation := m.lspRequestContext()
	ctx, cancel := context.WithCancel(base)
	line, col := ed.LSPCursorPosition()
	m.completionSequence++
	s := &completionState{workspace: m.ws, editor: ed, source: source, selection: currentCodeActionSelection(ed),
		revision: ed.Revision(), generation: generation, feature: feature, sequence: m.completionSequence,
		line: line, col: col, automatic: automatic, port: port, ctx: ctx, cancel: cancel,
		deadline: time.Now().Add(10 * time.Second), observed: make(map[string]bool)}
	if m.FocusStack != nil {
		s.focus = m.FocusStack.Top()
	}
	contextOwner, contextGeneration := m.ws.LSPMgr().ContextCursor()
	if contextOwner == source.WorkspaceGeneration {
		s.contextGeneration = contextGeneration
	}
	m.completion = s
	return func() tea.Msg {
		request, stop := context.WithTimeout(ctx, 10*time.Second)
		defer stop()
		set, err := port.RequestLSPCompletions(request, source, completionViewID, s.sequence, line, col)
		if cancelled := request.Err(); cancelled != nil {
			set, err = types.LSPCompletionSet{}, errors.Join(err, cancelled)
		}
		return completionResultMsg{owner: s, set: set, err: err}
	}
}

func (m *App) completionCurrent(s *completionState) bool {
	if s == nil || m.completion != s || s.released || s.ctx.Err() != nil || m.ws != s.workspace ||
		m.asyncIOStopped.Load() || !m.acceptsLSPDocument(s.generation, s.source.Path, s.editor, s.revision) ||
		!s.editor.MatchesInsightSource(s.source) || currentCodeActionSelection(s.editor) != s.selection ||
		m.ws.LSPMgr() == nil || !m.ws.LSPMgr().ServerReady() || m.sessionDeckModal() ||
		m.sessionDeckCapturesInput() || m.termDockFocused || m.FocusStack != nil && m.FocusStack.Top() != s.focus {
		return false
	}
	enabled, feature := m.ws.LSPMgr().CompletionsState()
	if !enabled || feature != s.feature || s.automatic && (!m.ws.LSPMgr().Settings().AutoComplete || s.editor.SnippetActive()) {
		return false
	}
	line, col := s.editor.LSPCursorPosition()
	if line != s.line || col != s.col {
		return false
	}
	if s.screen == nil {
		return m.focusTarget() == FocusEditor && m.focusedPopup() == nil
	}
	owner, generation := m.ws.LSPMgr().ContextCursor()
	if s.set == nil || owner != s.source.WorkspaceGeneration || generation != s.set.Boundary.Context.Generation ||
		time.Now().After(s.set.ExpiresAt) {
		return false
	}
	fw := m.overlayHost.Window(ui.OverlayCompletion)
	if fw == nil || m.focusedPopup() != fw {
		return false
	}
	frame, ok := fw.Content.(*ui.WindowFrameWidget)
	return ok && frame.Body() == s.screen
}

func (m *App) handleCompletionResult(msg completionResultMsg) {
	s := msg.owner
	if !m.completionCurrent(s) {
		return
	}
	if msg.err != nil {
		s.requestErr = msg.err
		return
	}
	set := msg.set
	if set.ID == "" || len(set.Digest) != 64 || set.ViewID != completionViewID || set.Sequence != s.sequence ||
		set.Line != s.line || set.Col != s.col || set.Boundary.Source != s.source || set.FeatureGeneration != s.feature {
		m.closeCompletionPopup()
		return
	}
	s.set = &set
}

// Only memory clocks and mounted identities are observed on Update. Publication
// must reach the original context before even a negative answer is presented.
func (m *App) syncCompletion() tea.Cmd {
	s := m.completion
	if s == nil {
		return nil
	}
	if !m.completionCurrent(s) {
		m.closeCompletionPopup()
		return nil
	}
	if !s.deadline.IsZero() && time.Now().After(s.deadline) {
		if !s.automatic {
			m.Feedback.NotifyInfo("Completion timed out or its original context was not observed")
		}
		m.closeCompletionPopup()
		return nil
	}
	if s.requestErr != nil {
		owner, generation := m.ws.LSPMgr().ContextCursor()
		if owner != "" && owner != s.source.WorkspaceGeneration || s.contextGeneration > 0 && generation > s.contextGeneration {
			m.closeCompletionPopup()
			return nil
		}
		if owner == "" || s.contextGeneration == 0 || generation < s.contextGeneration {
			return nil
		}
		if !s.automatic {
			m.Feedback.NotifyError("Completion: " + s.requestErr.Error())
		}
		m.closeCompletionPopup()
		return nil
	}
	if s.set == nil {
		return nil
	}
	set := s.set
	owner, generation := m.ws.LSPMgr().ContextCursor()
	if owner != "" && owner != s.source.WorkspaceGeneration || generation > set.Boundary.Context.Generation || time.Now().After(set.ExpiresAt) {
		m.closeCompletionPopup()
		return nil
	}
	if owner == "" || generation < set.Boundary.Context.Generation || s.screen != nil {
		return nil
	}
	if len(set.Entries) == 0 {
		if !s.automatic {
			switch {
			case set.Answer == types.LSPCompletionUnsupported:
				m.Feedback.NotifyInfo("This provider does not advertise completions")
			case set.UnsupportedItems > 0:
				m.Feedback.NotifyInfo(fmt.Sprintf("No supported completions (%d unsupported items)", set.UnsupportedItems))
			case set.Answer == types.LSPCompletionNull:
				m.Feedback.NotifyInfo("Completion provider returned null")
			default:
				m.Feedback.NotifyInfo("Completion provider returned an empty list")
			}
		}
		m.closeCompletionPopup()
		return nil
	}
	entries := make([]completion.Entry, len(set.Entries))
	for i, row := range set.Entries {
		entries[i] = completion.Entry{ID: row.ID, Label: row.Insertion.Label, Kind: row.Insertion.Kind, Detail: row.Documentation.Detail,
			Documentation: completionDocumentationText(set, row.Documentation), DocumentationState: completionDocumentationState(row.Documentation, "initial")}
	}
	qualifier := "unversioned"
	if len(set.Boundary.Context.UnknownInputs) > 0 {
		qualifier += "; context partial"
	}
	caption := fmt.Sprintf("r%d · %s · context %d · %s", s.source.Revision, set.Boundary.Producer.Provider, set.Boundary.Context.Generation, qualifier)
	s.screen = completion.New(m.Theme, set.ID, entries, caption)
	title := fmt.Sprintf("Completions · %d", len(entries))
	if set.Incomplete {
		title += " · incomplete"
	}
	if set.UnsupportedItems > 0 {
		title += fmt.Sprintf(" · %d unsupported", set.UnsupportedItems)
	}
	bounds, visible := m.editorHelpBounds(s.editor, min(112, max(1, m.width-4)), min(22, max(1, m.height-4)))
	if !visible || bounds.H < 6 {
		m.closeCompletionPopup()
		return nil
	}
	frame := ui.NewWindowFrameWidget(m.Theme, "completion/frame", ui.FramePopover, title, s.screen, nil, false, bounds.W, bounds.H)
	frame.Focus(s.screen.DefaultFocus())
	fw := wm.NewFloatingWindow(completionPopupID, "", frame, bounds.W, bounds.H, wm.WithDecoration(false),
		wm.WithMovable(false), wm.WithResizable(false), wm.WithClosable(false), wm.WithFocusable(true),
		wm.WithClickOutside(wm.ClickOutsideCloseRoute))
	fw.SetPosition(bounds.X, bounds.Y)
	m.overlayHost.Open(ui.OverlayCompletion, fw, ui.PopoverPolicy(true), func() {
		if m.completion == s {
			m.completion = nil
			m.retireCompletion(s)
		}
	})
	s.deadline = time.Time{}
	return m.readCompletion(s, s.screen.SelectedID(), false)
}

func (m *App) positionCompletion() {
	s := m.completion
	if s == nil || s.screen == nil || m.overlayHost == nil {
		return
	}
	b, ok := m.editorHelpBounds(s.editor, min(112, max(1, m.width-4)), min(22, max(1, m.height-4)))
	if !ok || b.H < 6 {
		m.closeCompletionPopup()
		return
	}
	if fw := m.overlayHost.Window(ui.OverlayCompletion); fw != nil {
		fw.SetPosition(b.X, b.Y)
		fw.SetSize(b.W, b.H)
	}
}

func completionDocumentationState(doc types.LSPCompletionDocumentation, status string) string {
	if !doc.Present {
		return status + "; not supplied"
	}
	kind := doc.Form + " " + doc.Kind
	if doc.Value == "" {
		return status + "; observed empty " + kind
	}
	return status + "; " + kind
}

func completionDocumentationText(set *types.LSPCompletionSet, doc types.LSPCompletionDocumentation) string {
	value := doc.Value
	if !doc.Present {
		value = "No documentation supplied."
	} else if value == "" {
		value = "The provider supplied empty documentation."
	}
	b := set.Boundary
	return fmt.Sprintf("%s\n\nSource revision: %d (synchronized wire %d; unversioned response)\nProvider: %s\nInstance: %s\nContext: %s (generation %d)\nUnknown inputs: %s",
		value, b.Source.Revision, set.WireVersion, b.Producer.Provider, b.Producer.InstanceID, b.Context.ID, b.Context.Generation,
		strings.Join(b.Context.UnknownInputs, "; "))
}

func (m *App) markCompletionDirty(s *completionState) {
	if s == nil || m.completion != s {
		return
	}
	if fw := m.overlayHost.Window(ui.OverlayCompletion); fw != nil {
		if frame, ok := fw.Content.(*ui.WindowFrameWidget); ok {
			frame.Host().MarkLayout()
		}
		fw.MarkContentDirty()
		m.forceFrame()
	}
}

func (m *App) readCompletion(s *completionState, itemID string, accept bool) tea.Cmd {
	if !m.completionCurrent(s) || s.set == nil || s.screen == nil || itemID != s.screen.SelectedID() {
		return nil
	}
	if s.pending != nil {
		s.pending.cancel()
		s.pending = nil
	}
	var row *types.LSPCompletionEntry
	for i := range s.set.Entries {
		if s.set.Entries[i].ID == itemID {
			row = &s.set.Entries[i]
			break
		}
	}
	if row == nil {
		return nil
	}
	if !accept && (s.observed[itemID] || !s.set.ResolveSupported) {
		return nil
	}
	ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
	read := &completionRead{owner: s, itemID: itemID, accept: accept, cancel: cancel}
	s.pending = read
	status := "loading"
	if accept {
		status = "checking original insertion"
	}
	s.screen.SetDocumentation(s.set.ID, itemID, row.Documentation.Detail, completionDocumentationText(s.set, row.Documentation),
		completionDocumentationState(row.Documentation, status))
	m.markCompletionDirty(s)
	return func() tea.Msg {
		defer cancel()
		result, err := s.port.InspectLSPCompletion(ctx, s.set.ID, s.set.Digest, itemID, !accept)
		if cancelled := ctx.Err(); cancelled != nil {
			result, err = types.LSPCompletionInspection{}, errors.Join(err, cancelled)
		}
		return completionReadMsg{read: read, result: result, err: err}
	}
}

func (m *App) handleCompletionRead(msg completionReadMsg) bool {
	read := msg.read
	if read == nil || !m.completionCurrent(read.owner) || read.owner.pending != read {
		return false
	}
	s := read.owner
	s.pending = nil
	if s.screen.SelectedID() != read.itemID || s.set == nil {
		return false
	}
	var row *types.LSPCompletionEntry
	for i := range s.set.Entries {
		if s.set.Entries[i].ID == read.itemID {
			row = &s.set.Entries[i]
			break
		}
	}
	if row == nil {
		return false
	}
	if msg.err != nil {
		s.screen.SetDocumentation(s.set.ID, read.itemID, row.Documentation.Detail, completionDocumentationText(s.set, row.Documentation),
			completionDocumentationState(row.Documentation, "unavailable: "+msg.err.Error()))
		m.markCompletionDirty(s)
		return false
	}
	result := msg.result
	if result.SetID != s.set.ID || result.Digest != s.set.Digest || result.ItemID != read.itemID ||
		result.WireVersion != s.set.WireVersion || result.FeatureGeneration != s.set.FeatureGeneration ||
		!reflect.DeepEqual(result.Boundary, s.set.Boundary) {
		m.closeCompletionPopup()
		return false
	}
	if read.accept {
		if err := s.editor.ApplyCompletion(row.Insertion); err != nil {
			m.Feedback.NotifyError("Completion: " + err.Error())
			return false
		}
		m.adoptEditorSnippet(s.editor)
		m.closeCompletionPopup()
		layout := m.computeLayout()
		s.editor.RefreshCursorHover(layout.SidebarW, layout.ChatAreaTop)
		return true
	}
	s.observed[read.itemID] = true
	s.screen.SetDocumentation(s.set.ID, read.itemID, result.Documentation.Detail, completionDocumentationText(s.set, result.Documentation),
		completionDocumentationState(result.Documentation, result.ResolveState))
	m.markCompletionDirty(s)
	return true
}

func (m *App) handleCompletionAction(action *ui.Action) (tea.Cmd, bool) {
	switch action.ID {
	case completion.ActionSelection, completion.ActionAccept, completion.ActionClose:
	default:
		return nil, false
	}
	payload, ok := action.Payload.(completion.Action)
	s := m.completion
	if !ok || !m.completionCurrent(s) || s.set == nil || payload.Invocation != s.set.ID || payload.ItemID != s.screen.SelectedID() {
		return nil, true
	}
	if action.ID == completion.ActionClose {
		m.closeCompletionPopup()
		return nil, true
	}
	return m.readCompletion(s, payload.ItemID, action.ID == completion.ActionAccept), true
}

func (m *App) handleCompletionKey(fw *wm.FloatingWindow, msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	native := false
	if msg.Mod == 0 {
		switch msg.Code {
		case tea.KeyUp, tea.KeyDown, tea.KeyPgUp, tea.KeyPgDown, tea.KeyHome, tea.KeyEnd, tea.KeyEnter, tea.KeyTab, tea.KeyEsc:
			native = true
		}
	}
	if msg.Mod == tea.ModCtrl && (msg.Code == tea.KeyPgUp || msg.Code == tea.KeyPgDown || msg.Code == 'a') || ui.IsCopyChord(msg) {
		native = true
	}
	if native {
		return m, m.forwardEventToWindowContent(fw, msg)
	}
	m.closeCompletionPopup()
	return m.dispatchEditorKey(msg)
}
