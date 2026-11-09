package viewmodel

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/shellport"
	"crude/textsearch"
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
	m.cancelCompletionIntent()
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
	if !automatic {
		m.closeEditorGhost()
	}
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
	if !enabled || !current || source.Kind != "canonical" || automatic && editorRetainsCompletionKeys(ed) {
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
		m.ws.LSPMgr() == nil || !m.ws.LSPMgr().ServerReady() ||
		m.sessionDeckCapturesInput() || m.termDockFocused || m.FocusStack != nil && m.FocusStack.Top() != s.focus {
		return false
	}
	enabled, feature := m.ws.LSPMgr().CompletionsState()
	if !enabled || feature != s.feature || s.automatic && (!m.ws.LSPMgr().Settings().AutoComplete || editorRetainsCompletionKeys(s.editor)) {
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
	if s.screen != nil {
		if s.screen.SetAcceptance(m.ws.LSPMgr().Settings().CompletionAcceptance()) {
			m.markCompletionDirty(s)
		}
		return nil
	}
	if owner == "" || generation < set.Boundary.Context.Generation {
		return nil
	}
	if len(set.Entries) == 0 {
		if !s.automatic {
			switch {
			case set.Answer == types.LSPCompletionUnsupported:
				m.Feedback.NotifyInfo("This language server does not support completion.")
			case set.UnsupportedItems > 0:
				m.Feedback.NotifyInfo("These suggestions cannot be inserted. Continue typing to narrow the list.")
			default:
				m.Feedback.NotifyInfo("No suggestions at this position.")
			}
		}
		m.closeCompletionPopup()
		return nil
	}
	// Keep exact original row IDs. Matching and ranking are only a projection
	// of this acknowledged invocation, never a rebased insertion.
	rows := slices.Clone(set.Entries)
	slices.SortStableFunc(rows, func(a, b types.LSPCompletionEntry) int {
		return cmp.Compare(a.SortText, b.SortText)
	})
	var entries []completion.Entry
	for _, row := range rows {
		if _, _, matches := textsearch.Fuzzy(row.FilterText, row.Prefix, false); !matches {
			continue
		}
		entries = append(entries, completion.Entry{ID: row.ID, Label: row.Insertion.Label, Kind: row.Insertion.Kind, Detail: row.Documentation.Detail,
			Documentation: row.Documentation.Value})
	}
	if len(entries) == 0 {
		if !s.automatic {
			m.Feedback.NotifyInfo("No suggestions match the text at the cursor.")
		}
		m.closeCompletionPopup()
		return nil
	}
	caption := ""
	if set.Incomplete {
		caption = "Continue typing to narrow the suggestions."
	} else if set.UnsupportedItems > 0 {
		caption = "Some suggestions cannot be inserted."
	}
	s.screen = completion.New(m.Theme, set.ID, entries, caption)
	s.screen.SetAcceptance(m.ws.LSPMgr().Settings().CompletionAcceptance())
	title := fmt.Sprintf("Completions · %d", len(entries))
	frame, fw := m.newEditorPopover(ui.OverlayCompletion, title, s.screen, s.screen.DefaultFocus(), ui.Rect{W: 1, H: 1})
	bounds, visible := m.completionBounds(s, frame)
	if !visible || bounds.H < 6 {
		m.closeCompletionPopup()
		return nil
	}
	fw.SetPosition(bounds.X, bounds.Y)
	fw.SetSize(bounds.W, bounds.H)
	m.overlayHost.Open(ui.OverlayCompletion, fw, ui.PopoverPolicy(true), func() {
		if m.completion == s {
			m.completion = nil
			m.retireCompletion(s)
		}
	})
	s.deadline = time.Time{}
	return m.readCompletion(s, s.screen.SelectedID(), false)
}

func (m *App) completionBounds(s *completionState, frame *ui.WindowFrameWidget) (ui.Rect, bool) {
	size := frame.Frame().Measure(ui.LayoutContext{}, ui.Constraints{
		MaxW: min(112, max(1, m.width-4)), MaxH: min(14, max(1, m.height-4)),
	})
	return m.editorHelpBounds(s.editor, size.W, size.H)
}

func (m *App) positionCompletion() {
	s := m.completion
	if s == nil || s.screen == nil || m.overlayHost == nil {
		return
	}
	fw := m.overlayHost.Window(ui.OverlayCompletion)
	if fw == nil {
		return
	}
	frame, ok := fw.Content.(*ui.WindowFrameWidget)
	if !ok {
		m.closeCompletionPopup()
		return
	}
	b, ok := m.completionBounds(s, frame)
	if !ok || b.H < 6 {
		m.closeCompletionPopup()
		return
	}
	if x, y := fw.Position(); x != b.X || y != b.Y {
		fw.SetPosition(b.X, b.Y)
	}
	if w, h := fw.Size(); w != b.W || h != b.H {
		fw.SetSize(b.W, b.H)
	}
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
	status := "Loading documentation..."
	if accept {
		status = "Inserting suggestion..."
	}
	s.screen.SetDocumentation(s.set.ID, itemID, row.Documentation.Detail, row.Documentation.Value, status)
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
		status := "Could not load documentation. Select the suggestion again to retry."
		if read.accept {
			status = "Could not insert this suggestion. Try another suggestion."
		}
		s.screen.SetDocumentation(s.set.ID, read.itemID, row.Documentation.Detail, row.Documentation.Value, status)
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
	s.screen.SetDocumentation(s.set.ID, read.itemID, result.Documentation.Detail, result.Documentation.Value, "")
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
	accept := m.ws.LSPMgr().Settings().CompletionAcceptance()
	native := false
	if msg.Mod == 0 {
		switch msg.Code {
		case tea.KeyUp, tea.KeyDown, tea.KeyPgUp, tea.KeyPgDown, tea.KeyHome, tea.KeyEnd, tea.KeyEnter, tea.KeyTab, tea.KeyEsc:
			native = true
		}
		if msg.Code == tea.KeyTab && accept == "enter" || msg.Code == tea.KeyEnter && accept == "tab" {
			native = false
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
