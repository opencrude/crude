package viewmodel

import (
	"path/filepath"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/prompt"
	"crude/provider"
	"crude/sessiondb"
	"crude/tui/editor"
	"crude/tui/ui"
	"crude/tui/window"
	"crude/types"
	"crude/workspace"
)

// Ask Crude puts code from the editor into the agent chat beside it. The chat
// is the ordinary agent tab; this file only decides which chat, where its tab
// goes and what is attached. Every decision reads the current layout, so it
// never disagrees with the pane owner (window.Window).

const (
	// A chat beside code needs room for both; below these sizes the pane is
	// not split in that direction.
	askCodeMinColumns = 60
	askChatMinColumns = 48
	askCodeMinRows    = 16
	askChatMinRows    = 12
	// Without a selection, Ask Crude attaches the enclosing fold when it is
	// at most this many lines, else the caret line.
	askFoldMaxLines = 120
)

type askKind int

const (
	askAttach  askKind = iota // attach the code and give the composer the caret
	askExplain                // send the prepared Explain prompt
	askFix                    // send the prepared Fix prompt
	askFinish                 // send the prepared Finish this change prompt
)

// askRequest is one Ask Crude invocation, captured from the editor that asked.
type askRequest struct {
	generation uint64
	group      window.GroupID // the editor's pane when the user asked
	path, name string
	start, end int    // one-based source lines
	text       string // the lines as the editor showed them; "" when too large
	kind       askKind
	newChat    bool
	problem    string // the diagnostic for askFix
}

// askReferenceMsg returns a created code-selection reference to Update.
type askReferenceMsg struct {
	request *askRequest
	session *workspace.Session
	agentID string
	ref     sessiondb.Reference
	err     error
}

// askCrude starts Ask Crude from the focused editor. The returned commands
// save the file first when it has unsaved text, then attach the code.
func (m *App) askCrude(kind askKind, newChat bool, problem string) []tea.Cmd {
	ed := m.EditorState.Editor
	if ed == nil || !ed.IsOpen() || m.Win == nil {
		return nil
	}
	if ed.FilePath() == "" {
		m.Feedback.NotifyInfo("Save this file first. Then ask Crude about it.")
		return nil
	}
	start, end := askLines(ed)
	req := &askRequest{
		generation: m.workspaceSwitchGen.Load(), group: m.Win.ActiveGroup(),
		path: ed.FilePath(), name: filepath.Base(ed.FilePath()),
		start: start + 1, end: end + 1, text: askText(ed, start, end),
		kind: kind, newChat: newChat, problem: problem,
	}
	var cmds []tea.Cmd
	// Agent tools read the saved file. The chip keeps the exact text either way.
	if ed.Modified() && !ed.ReadOnly() {
		cmds = append(cmds, m.requestEditorSave(ed, workspace.DocumentSaveFocusOut, false))
	}
	if !newChat {
		if group, agentID := m.askVisibleChat(req.group, kind != askAttach); agentID != "" {
			m.focusMainGroup(group)
			m.setFocusInput()
			return append(cmds, m.askAttachTo(agentID, req))
		}
	}
	return append(cmds, m.askNewChat(req))
}

// runAskCrude is Ask Crude from a menu action, outside Update's return path.
func (m *App) runAskCrude(kind askKind, newChat bool, problem string) {
	for _, cmd := range m.askCrude(kind, newChat, problem) {
		m.runDetachedAsyncCmd(cmd)
	}
}

// askLines returns zero-based source lines: the selection extended to whole
// lines, else the innermost small fold around the caret, else the caret line.
func askLines(ed *editor.Editor) (int, int) {
	if ed.HasSelection() {
		start, _, end, endCol := ed.SelectionRange()
		if end > start && endCol == 0 {
			end-- // a selection that ends at a line start does not include that line
		}
		return start, end
	}
	line := ed.CursorLine()
	if fold, ok := ed.EnclosingFold(line); ok && fold.End-fold.Start < askFoldMaxLines {
		// An indentation fold stops before a block's closing line; include a
		// following line that only closes brackets, so the block is whole.
		if next := fold.End + 1; next < ed.LineCount() && closesBrackets(ed.LineText(next)) {
			return fold.Start, next
		}
		return fold.Start, fold.End
	}
	return line, line
}

// closesBrackets reports whether a line holds only closing brackets and
// separators, such as "}", "});" or "],".
func closesBrackets(line string) bool {
	line = strings.TrimSpace(line)
	return line != "" && strings.Trim(line, ")]};,") == ""
}

// askText joins the lines as the editor shows them, or returns "" when they
// exceed the bound that a reference may carry.
func askText(ed *editor.Editor, start, end int) string {
	var b strings.Builder
	for line := start; line <= end && line < ed.LineCount(); line++ {
		if line > start {
			b.WriteByte('\n')
		}
		b.WriteString(ed.LineText(line))
		if b.Len() > sessiondb.CodeSelectionMaxBytes {
			return ""
		}
	}
	return b.String()
}

// askProblem describes the most severe diagnostic for a prepared Fix prompt.
func askProblem(diags []types.LSPDiagnostic) string {
	if len(diags) == 0 {
		return ""
	}
	best := diags[0]
	for _, d := range diags[1:] {
		if d.Severity > 0 && (best.Severity == 0 || d.Severity < best.Severity) {
			best = d
		}
	}
	text := strings.TrimSpace(best.Message)
	if source := strings.TrimSpace(strings.Join(slices.DeleteFunc([]string{best.Source, best.Code}, func(s string) bool { return s == "" }), " ")); source != "" {
		text += " (" + source + ")"
	}
	return text
}

// askEligible reports whether an agent's tab is a chat that Ask Crude may
// use: a root agent with a composer, never the workspace Aeron. Prepared
// prompts also need an idle agent, so they never steer a running turn.
func (m *App) askEligible(agentID string, idle bool) bool {
	node := m.ws.AgentMgr().Get(agentID)
	if node == nil || node.State == nil || node.Parent != nil || m.AgentView.IsHidden(agentID) {
		return false
	}
	if workspace.SessionIsAeron(m.ws.SessionsByAgent()[agentID]) {
		return false
	}
	return !idle || !m.ws.AgentTurnActive(agentID)
}

// askChatAgent returns the eligible chat that a group shows, if any.
func (m *App) askChatAgent(group window.GroupID, idle bool) string {
	tab := m.Win.GroupActiveTab(group)
	if tab == nil || !m.isAgentTabTitle(tab.Title) {
		return ""
	}
	if id := m.agentIDFromTabTitle(tab.Title); m.askEligible(id, idle) {
		return id
	}
	return ""
}

// askNeighbors lists the docked neighbors of a group in the order Ask Crude
// prefers them: right, below, left, above.
func (m *App) askNeighbors(group window.GroupID) []window.GroupID {
	var out []window.GroupID
	for _, side := range []struct {
		dir    ui.Direction
		before bool
	}{{ui.Horizontal, false}, {ui.Vertical, false}, {ui.Horizontal, true}, {ui.Vertical, true}} {
		if id := m.Win.Neighbor(group, side.dir, side.before); id != 0 && !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}

// askVisibleChat finds a chat that another pane shows: the agent the user
// focused last, then the nearest neighbor, then the first in layout order.
func (m *App) askVisibleChat(source window.GroupID, idle bool) (window.GroupID, string) {
	var groups []window.GroupID
	for _, id := range m.Win.AllGroupIDs() {
		if id != source && m.askChatAgent(id, idle) != "" {
			groups = append(groups, id)
		}
	}
	if len(groups) == 0 {
		return 0, ""
	}
	for _, id := range groups {
		if agentID := m.askChatAgent(id, idle); agentID == m.lastFocusedAgent {
			return id, agentID
		}
	}
	for _, id := range m.askNeighbors(source) {
		if slices.Contains(groups, id) {
			return id, m.askChatAgent(id, idle)
		}
	}
	return groups[0], m.askChatAgent(groups[0], idle)
}

// askNewChat creates a session off Update. applyRemoteSession records the
// request before the agent's tab appears, and finishAskChat places it.
func (m *App) askNewChat(req *askRequest) tea.Cmd {
	if m.sessionLifecyclePending || m.workspaceAttachPending {
		m.Feedback.NotifyInfo("A chat is still opening. Try again when it is ready.")
		return nil
	}
	m.sessionLifecycleSequence++
	sequence, owner := m.sessionLifecycleSequence, m.ws
	m.sessionLifecyclePending = true
	return func() tea.Msg {
		return remoteSessionReadyMsg{owner: owner, sequence: sequence, result: owner.NewAgentSession(), ask: req}
	}
}

// finishAskChat places a new Ask Crude chat once its agent and tab exist in
// this shell (at once on a local core, later on a connected one).
func (m *App) finishAskChat(agentID string) tea.Cmd {
	req := m.askArrivals[agentID]
	if req == nil {
		return nil
	}
	if req.generation != m.workspaceSwitchGen.Load() {
		delete(m.askArrivals, agentID)
		return nil
	}
	tab := m.Win.TabByTitle(m.agentTabTitle(agentID))
	if m.ws.AgentMgr().Get(agentID) == nil || tab == nil {
		return nil
	}
	delete(m.askArrivals, agentID)
	m.focusMainGroup(m.placeAskChat(tab.ID, req.group))
	m.setFocusInput()
	return m.askAttachTo(agentID, req)
}

// placeAskChat moves a new chat tab beside the code, in the order the plan
// names: a pane that shows a chat, the right neighbor, a split to the right,
// a split below, any neighbor, else the editor's own pane. It returns the
// chat's group.
func (m *App) placeAskChat(tab window.TabID, source window.GroupID) window.GroupID {
	current := m.Win.GroupOf(tab)
	if !slices.Contains(m.Win.AllGroupIDs(), source) {
		source = current // the editor's pane closed meanwhile
	}
	move := func(group window.GroupID) window.GroupID {
		if group == current || m.Win.MoveTabToGroup(tab, group, 0) {
			m.syncPaneWindows()
			return group
		}
		return 0
	}
	// The new tab is selected where it landed, so a pane that held a chat
	// before it is recognized by its other agent tabs.
	if current != source && m.askGroupHasChat(current, tab) {
		return current
	}
	for _, id := range m.Win.AllGroupIDs() {
		if id != source && id != current && m.askChatAgent(id, false) != "" {
			if group := move(id); group != 0 {
				return group
			}
		}
	}
	if right := m.Win.Neighbor(source, ui.Horizontal, false); right != 0 {
		if group := move(right); group != 0 {
			return group
		}
	}
	if rect, known := m.askGroupRect(source); known {
		if rect.W >= askCodeMinColumns+askChatMinColumns+1 {
			if group := m.askSplit(tab, source, ui.Horizontal); group != 0 {
				return group
			}
		}
		if rect.H >= askCodeMinRows+askChatMinRows+1 {
			if group := m.askSplit(tab, source, ui.Vertical); group != 0 {
				return group
			}
		}
	}
	for _, id := range m.askNeighbors(source) {
		if group := move(id); group != 0 {
			return group
		}
	}
	if group := move(source); group != 0 {
		return group
	}
	return current
}

// askGroupHasChat reports whether a group holds an eligible chat tab other
// than the new one.
func (m *App) askGroupHasChat(group window.GroupID, except window.TabID) bool {
	for _, tab := range m.Win.GroupTabs(group) {
		if tab.ID != except && m.isAgentTabTitle(tab.Title) && m.askEligible(m.agentIDFromTabTitle(tab.Title), false) {
			return true
		}
	}
	return false
}

// askGroupRect is a docked group's laid-out size. A compact layout shows one
// group in the whole area; a floating window's size is not known here.
func (m *App) askGroupRect(group window.GroupID) (ui.Rect, bool) {
	if m.Win.IsFloating(group) {
		return ui.Rect{}, false
	}
	panes := m.ensureMainPanes()
	if rect, ok := panes.placements[group]; ok {
		return rect, true
	}
	return panes.Bounds(), !panes.Bounds().Empty()
}

// askSplit splits the source pane and moves the chat into the new side,
// through the same placement and minimum-size owner as a dragged tab.
func (m *App) askSplit(tab window.TabID, source window.GroupID, dir ui.Direction) window.GroupID {
	scopes := m.Win.PaneScopes(source)
	if len(scopes) == 0 {
		return 0
	}
	return m.placeTabSplit(window.TabPlacement{Tab: tab, Node: scopes[0].ID, Split: true, Direction: dir})
}

// askAttachTo creates the code-selection reference in the agent's session,
// off Update.
func (m *App) askAttachTo(agentID string, req *askRequest) tea.Cmd {
	sess := m.ws.SessionsByAgent()[agentID]
	if req.kind == askFinish {
		// The prepared message carries the edits itself; nothing is attached.
		return func() tea.Msg { return askReferenceMsg{request: req, session: sess, agentID: agentID} }
	}
	if sess == nil || sess.RefStore == nil {
		m.Feedback.NotifyError("Crude could not attach the code: the chat is not ready.")
		return nil
	}
	store := sess.RefStore
	create := sessiondb.CreateRequest{
		Kind:  sessiondb.KindCodeSelection,
		Label: sessiondb.CodeSelectionLabel(req.name, req.start, req.end),
		Title: sessiondb.CodeSelectionTitle(req.name, req.start, req.end),
		Mime:  "text/plain", Content: req.text, FilePath: req.path,
	}
	return func() tea.Msg {
		// Asking twice about the same unchanged lines reuses one chip.
		for _, ref := range store.ListReferences() {
			if ref.Active() && ref.Kind == create.Kind && ref.Title == create.Title &&
				ref.FilePath == create.FilePath && ref.Content == create.Content {
				return askReferenceMsg{request: req, session: sess, agentID: agentID, ref: ref}
			}
		}
		ref, err := store.CreateReference(create)
		return askReferenceMsg{request: req, session: sess, agentID: agentID, ref: ref, err: err}
	}
}

// applyAskReference attaches the created reference, or sends the prepared
// prompt that names it.
func (m *App) applyAskReference(msg askReferenceMsg) tea.Cmd {
	if msg.request == nil || msg.request.generation != m.workspaceSwitchGen.Load() {
		return nil
	}
	if msg.err != nil {
		m.Feedback.NotifyError("Crude could not attach the code: " + msg.err.Error())
		return nil
	}
	label := msg.ref.DisplayName()
	switch msg.request.kind {
	case askFinish:
		return m.submitFirstInput(msg.agentID, provider.InputWithRefs(msg.request.text, nil))
	case askExplain:
		return m.submitFirstInput(msg.agentID, provider.InputWithRefs(prompt.AskExplain(label), []string{msg.ref.ID}))
	case askFix:
		return m.submitFirstInput(msg.agentID, provider.InputWithRefs(prompt.AskFix(label, msg.request.problem), []string{msg.ref.ID}))
	}
	if m.askChips == nil {
		m.askChips = make(map[*workspace.Session][]sessiondb.Reference)
	}
	m.askChips[msg.session] = append(m.askChips[msg.session], msg.ref)
	m.flushAskChips(msg.session, false)
	return nil
}

// flushAskChips puts waiting chips into a session's composer once its draft
// is in memory. A draft that is still loading would discard its saved text if
// it changed first, so the chips wait for applyComposerDraft. force inserts
// after a failed load, when there is nothing left to wait for.
func (m *App) flushAskChips(sess *workspace.Session, force bool) {
	chips := m.askChips[sess]
	if sess == nil || len(chips) == 0 {
		return
	}
	composer := m.paneComposer(sess)
	if composer == nil {
		return
	}
	if owner := m.composerDraftOwners[sess]; owner != nil && owner.loading && !force {
		return
	}
	delete(m.askChips, sess)
	for _, ref := range chips {
		composer.AttachReference(ref)
	}
	m.persistComposerDraftFor(sess)
	m.forceFrame()
}
