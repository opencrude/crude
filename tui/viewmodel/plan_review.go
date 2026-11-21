package viewmodel

import (
	"crypto/rand"
	"fmt"
	"hash/maphash"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	shellport "crude/core/port"
	"crude/text/anchor"
	"crude/tui/editor"
	"crude/tui/rendering"
	"crude/tui/screens/decision"
	"crude/tui/ui"
	"crude/tui/window"
	"crude/types"
	"crude/workspace"
)

// Plan review: every plan a session proposes in Plan mode is a revision with
// its own Markdown file in the session's directory. The user opens it in the
// editor beside the chat, edits it, comments on its text, and then sends the
// comments (the agent revises the plan), implements it in this session, or
// implements it in a new session. The session owns the record and composes
// what the agent receives; this file only shows the record and sends the
// user's choices. Comments are anchored by quote and position
// (crude/text/anchor), so they follow the text as the user edits it.

// Plan review bar actions. The bar's payload is the editor it belongs to.
const (
	actionPlanSend       = "plan-review.send"
	actionPlanImplement  = "plan-review.implement"
	actionPlanNewSession = "plan-review.new-session"
	actionPlanRevise     = "plan-review.revise"
	actionPlanComment    = "plan-review.comment"
	actionPlanEvidence   = "plan-review.evidence"
	actionPlanOpenLatest = "plan-review.open-latest"
)

// Plan decision answers, encoded in the dock's decision data with the
// revision they act on.
const (
	planDecisionImplement = "implement"
	planDecisionSend      = "send"
	planDecisionCheck     = "check"
	planChoiceNewSession  = "new_session"
	planChoiceReview      = "review"
)

// planDocument is the plan revision an editor shows. print fingerprints its
// comments, so an editor rebuilds its cards only when they change.
type planDocument struct {
	agentID  string
	revision types.PlanRevision
	latest   int
	print    uint64
}

// planDocCache remembers, per file, the revision found at one plan review
// epoch. The epoch advances when the core's state reaches this shell, so a
// frame never copies the record or hashes its comments again.
type planDocCache struct {
	epoch uint64
	doc   planDocument
	ok    bool
}

// planDocumentOf finds the revision whose plan file the editor shows. It
// reads only the mirrored records the shell already holds.
func (m *App) planDocumentOf(ed *editor.Editor) (planDocument, bool) {
	if ed == nil || m.ws == nil || !planFilePath(ed.FilePath()) {
		return planDocument{}, false
	}
	path := ed.FilePath()
	if cached, ok := m.planDocs[path]; ok && cached.epoch == m.planReviewEpoch {
		return cached.doc, cached.ok
	}
	doc, found := planDocument{}, false
	for agentID := range m.ws.SessionsByAgent() {
		review, ok := m.ws.PlanReviewForAgent(agentID)
		if !ok {
			continue
		}
		if revision, ok := review.RevisionAtPath(path); ok {
			latest, _ := review.Latest()
			doc, found = planDocument{agentID: agentID, revision: revision, latest: latest.Number}, true
			doc.print = planCommentsFingerprint(doc)
			break
		}
	}
	if m.planDocs == nil {
		m.planDocs = make(map[string]planDocCache)
	}
	m.planDocs[path] = planDocCache{epoch: m.planReviewEpoch, doc: doc, ok: found}
	return doc, found
}

// planFilePath reports, without allocating, whether path can be a session's
// plan file: the session keeps them in a directory named plans. Only such
// paths are looked up in the records.
func planFilePath(path string) bool {
	dir := path[:max(0, strings.LastIndexAny(path, "/\\"))]
	return strings.HasSuffix(dir, "/plans") || strings.HasSuffix(dir, "\\plans")
}

// planReviewChanged starts a new plan review epoch: the next frame reads the
// mirrored records again.
func (m *App) planReviewChanged() {
	m.planReviewEpoch++
}

// latestPlanRevision is the newest revision of the session whose main agent
// is agentID.
func (m *App) latestPlanRevision(agentID string) (types.PlanRevision, bool) {
	if m.ws == nil || agentID == "" {
		return types.PlanRevision{}, false
	}
	review, ok := m.ws.PlanReviewForAgent(agentID)
	if !ok {
		return types.PlanRevision{}, false
	}
	return review.Latest()
}

// planBarFullWidth fits the revision label and all four buttons of an open
// revision (about 70 columns); a narrower pane leaves out New session, which
// the plan decision and the palette also offer. Without it the bar fits in a
// 60-column pane.
const planBarFullWidth = 72

// planReviewChrome keeps an editor's comment cards in step with its plan
// revision and returns the review bar shown above it, or nil for a file that
// is not a plan.
func (m *App) planReviewChrome(v *mainPaneView, tab window.TabID, ed *editor.Editor, width int) *ui.NoticeBar {
	doc, ok := m.planDocumentOf(ed)
	m.syncPlanComments(ed, doc, ok)
	if !ok || v == nil {
		return nil
	}
	if v.planBar == nil {
		v.planBar = ui.NewNoticeBar(ui.ComponentID(fmt.Sprintf("main-panes/view-%d/plan", tab)), m.rootHost.Tokens())
		v.planBar.SetMark("✎")
	}
	revision := doc.revision
	pending := revision.PendingComments()
	extraWidth := 0
	if len(revision.Evidence) > 0 {
		extraWidth = 13
	}
	setBar := func(label string, role ui.SemanticColor, buttons ...ui.ActionBarButton) {
		if len(revision.Evidence) > 0 {
			buttons = append(buttons, ui.ActionBarButton{Label: "Evidence", Action: actionPlanEvidence, Enabled: true, Variant: ui.VariantSecondary})
		}
		v.planBar.Set(label, role, ed, buttons...)
	}
	switch {
	// The buttons keep their room first; the message is short so that it
	// still fits beside all four in an 80-column pane.
	case revision.Outcome == types.PlanOutcomeReplaced:
		setBar(fmt.Sprintf("Revision %d. Revision %d replaced it.", revision.Number, doc.latest), ui.ColorMuted,
			ui.ActionBarButton{Label: fmt.Sprintf("Open revision %d", doc.latest), Action: actionPlanOpenLatest, Enabled: true, Variant: ui.VariantSecondary})
	case revision.Outcome == types.PlanOutcomeImplemented:
		setBar(fmt.Sprintf("Revision %d. This session implements it.", revision.Number), ui.ColorMuted)
	case revision.Outcome == types.PlanOutcomeNewSession:
		setBar(fmt.Sprintf("Revision %d. A new session implements it.", revision.Number), ui.ColorMuted)
	case revision.NewSessionID != "":
		setBar("Check that the new session received the plan.", ui.ColorAccent,
			ui.ActionBarButton{Label: "Check", Action: actionPlanNewSession, Enabled: true, Variant: ui.VariantPrimary})
	case pending > 0:
		setBar(fmt.Sprintf("Revision %d · %s", revision.Number, countNoun(pending, "comment")), ui.ColorAccent,
			ui.ActionBarButton{Label: "Send comments", Action: actionPlanSend, Enabled: true, Variant: ui.VariantPrimary},
			ui.ActionBarButton{Label: "Comment", Action: actionPlanComment, Enabled: !ed.ReadOnly(), Variant: ui.VariantSecondary})
	default:
		buttons := []ui.ActionBarButton{{Label: "Implement", Action: actionPlanImplement, Enabled: true, Variant: ui.VariantPrimary}}
		if width >= planBarFullWidth+extraWidth {
			buttons = append(buttons, ui.ActionBarButton{Label: "New session", Action: actionPlanNewSession, Enabled: true, Variant: ui.VariantSecondary})
		}
		buttons = append(buttons,
			ui.ActionBarButton{Label: "Revise", Action: actionPlanRevise, Enabled: true, Variant: ui.VariantSecondary},
			ui.ActionBarButton{Label: "Comment", Action: actionPlanComment, Enabled: !ed.ReadOnly(), Variant: ui.VariantSecondary})
		setBar(fmt.Sprintf("Revision %d", revision.Number), ui.ColorAccent, buttons...)
	}
	return v.planBar
}

// syncPlanComments gives an editor the comments of the revision it shows.
// The fingerprint keeps an unchanged record from rebuilding the list.
func (m *App) syncPlanComments(ed *editor.Editor, doc planDocument, ok bool) {
	if ed == nil {
		return
	}
	previous, seen := m.planCommentPrints[ed]
	if !ok {
		if seen {
			delete(m.planCommentPrints, ed)
			ed.SetReviewComments(nil)
		}
		return
	}
	if seen && previous == doc.print {
		return
	}
	if m.planCommentPrints == nil {
		m.planCommentPrints = make(map[*editor.Editor]uint64)
	}
	m.planCommentPrints[ed] = doc.print
	comments := make([]editor.ReviewComment, 0, len(doc.revision.Comments))
	for _, comment := range doc.revision.Comments {
		status := ""
		if comment.Sent {
			status = "Sent"
		}
		comments = append(comments, editor.ReviewComment{ID: comment.ID, Anchor: comment.Anchor, Body: comment.Body,
			Status: status, Muted: comment.Sent || !doc.revision.Open()})
	}
	ed.SetReviewComments(comments)
}

func planCommentsFingerprint(doc planDocument) uint64 {
	var h maphash.Hash
	h.SetSeed(planPrintSeed)
	write := func(values ...string) {
		for _, value := range values {
			h.WriteString(value)
			h.WriteByte(0)
		}
	}
	write(doc.agentID, strconv.Itoa(doc.revision.Number), string(doc.revision.Outcome))
	for _, c := range doc.revision.Comments {
		write(c.ID, c.Body, strconv.FormatBool(c.Sent), strconv.Itoa(c.Anchor.Start), strconv.Itoa(c.Anchor.End),
			c.Anchor.Exact, c.Anchor.Prefix, c.Anchor.Suffix)
	}
	return h.Sum64() | 1 // zero means "no comments shown"
}

var planPrintSeed = maphash.MakeSeed()

// forgetPlanEditor drops a closed editor's fingerprint and cached record.
func (m *App) forgetPlanEditor(ed *editor.Editor) {
	delete(m.planCommentPrints, ed)
	if ed != nil {
		delete(m.planDocs, ed.FilePath())
	}
}

// handlePlanReviewAction carries out a review bar button.
func (m *App) handlePlanReviewAction(action ui.Action) (tea.Cmd, bool) {
	switch action.ID {
	case actionPlanSend, actionPlanImplement, actionPlanNewSession, actionPlanRevise, actionPlanComment, actionPlanEvidence, actionPlanOpenLatest:
	default:
		return nil, false
	}
	ed, _ := action.Payload.(*editor.Editor)
	doc, ok := m.planDocumentOf(ed)
	if !ok {
		return nil, true // the bar is gone with its plan
	}
	switch action.ID {
	case actionPlanEvidence:
		return m.planEvidenceMenu(doc.agentID, []types.PlanRevision{doc.revision}), true
	case actionPlanOpenLatest:
		return m.openPlanRevision(doc.agentID, doc.latest), true
	case actionPlanComment:
		m.focusEditorFor(ed)
		return m.addPlanComment(ed), true
	case actionPlanSend, actionPlanRevise:
		return m.settlePlanReview(doc.agentID, doc.revision.Number, types.PlanReviewFeedback), true
	case actionPlanImplement:
		return m.settlePlanReview(doc.agentID, doc.revision.Number, types.PlanReviewImplement), true
	case actionPlanNewSession:
		return m.settlePlanReview(doc.agentID, doc.revision.Number, types.PlanReviewNewSession), true
	}
	return nil, true
}

// focusEditorFor makes ed's tab the active one, so comment commands act on
// the editor whose bar was clicked.
func (m *App) focusEditorFor(ed *editor.Editor) {
	if m.EditorState == nil || ed == nil {
		return
	}
	for id, view := range m.EditorState.Views {
		if view.Editor == ed {
			m.showDocument(view.Path, m.Win.GroupOf(id), true)
			return
		}
	}
}

// ── Opening a revision ──────────────────────────────────────────────────────

type planRevisionOpenedMsg struct {
	owner      shellport.Workspace
	generation uint64
	agentID    string
	revision   int
	path       string
	err        error
}

// openPlanRevision makes sure the revision's file exists, then opens it in
// the editor beside the session's chat.
func (m *App) openPlanRevision(agentID string, revision int) tea.Cmd {
	if agentID == "" || revision <= 0 {
		return nil
	}
	owner, generation := m.ws, m.workspaceSwitchGen.Load()
	return func() tea.Msg {
		path, err := owner.OpenPlanRevision(agentID, revision)
		return planRevisionOpenedMsg{owner: owner, generation: generation, agentID: agentID, revision: revision, path: path, err: err}
	}
}

func (m *App) applyPlanRevisionOpened(msg planRevisionOpenedMsg) tea.Cmd {
	if msg.owner != m.ws || msg.generation != m.workspaceSwitchGen.Load() {
		return nil
	}
	m.planReviewChanged()
	if msg.err != nil {
		m.Feedback.NotifyError("The plan did not open. " + sentence(msg.err.Error()))
		return nil
	}
	if views := m.documentViews(msg.path); len(views) > 0 {
		m.showDocument(msg.path, m.Win.GroupOf(views[0].Tab), true)
		return nil
	}
	group, split, dir := m.planReviewPlacement(msg.agentID)
	cmd := m.openPaneFiles([]string{msg.path}, group)
	if split {
		m.planSplit = &planSplitRequest{seq: m.paneFileOpenSeq, path: msg.path, dir: dir}
	}
	return cmd
}

// planSplitRequest moves a plan that is opening into a new pane beside the
// chat once its tab exists.
type planSplitRequest struct {
	seq  uint64
	path string
	dir  ui.Direction
}

// placeOpenedPlan finishes a planSplitRequest for the view that opened.
func (m *App) placeOpenedPlan(seq uint64, view *EditorView, source window.GroupID) {
	request := m.planSplit
	if request == nil || request.seq != seq || view == nil || view.Path != request.path {
		return
	}
	m.planSplit = nil
	if group := m.askSplit(view.Tab, source, request.dir); group != 0 {
		m.showDocument(view.Path, group, true)
	}
}

// planReviewPlacement chooses where a plan opens: the pane right of the
// session's chat, a new pane split from the chat when there is room, else
// the chat's own pane. The chat stays visible beside its plan, as with Ask
// Crude.
func (m *App) planReviewPlacement(agentID string) (group window.GroupID, split bool, dir ui.Direction) {
	chat := window.GroupID(0)
	for _, tab := range m.Win.Tabs() {
		if m.isAgentTabTitle(tab.Title) && m.agentIDFromTabTitle(tab.Title) == agentID {
			chat = tab.Group
			break
		}
	}
	if chat == 0 {
		return m.Win.ActiveGroup(), false, ui.Horizontal
	}
	if right := m.Win.Neighbor(chat, ui.Horizontal, false); right != 0 {
		return right, false, ui.Horizontal
	}
	if rect, known := m.askGroupRect(chat); known {
		if rect.W >= askCodeMinColumns+askChatMinColumns+1 {
			return chat, true, ui.Horizontal
		}
		if rect.H >= askCodeMinRows+askChatMinRows+1 {
			return chat, true, ui.Vertical
		}
	}
	return chat, false, ui.Horizontal
}

// reviewLatestPlan opens the newest plan of the active session.
func (m *App) reviewLatestPlan() tea.Cmd {
	agentID := m.activeRootAgent()
	revision, ok := m.latestPlanRevision(agentID)
	if !ok {
		m.Feedback.NotifyInfo("This session has no proposed plan. Use /plan to plan first.")
		return nil
	}
	return m.openPlanRevision(agentID, revision.Number)
}

// settleActivePlan settles the plan in the focused editor, else the newest
// plan of the active session.
func (m *App) settleActivePlan(action types.PlanReviewAction) tea.Cmd {
	if doc, ok := m.planDocumentOf(m.EditorState.Editor); ok {
		return m.settlePlanReview(doc.agentID, doc.revision.Number, action)
	}
	agentID := m.activeRootAgent()
	revision, ok := m.latestPlanRevision(agentID)
	if !ok {
		m.Feedback.NotifyInfo("This session has no proposed plan. Use /plan to plan first.")
		return nil
	}
	return m.settlePlanReview(agentID, revision.Number, action)
}

// activeRootAgent is the main agent of the session the user looks at.
func (m *App) activeRootAgent() string {
	agentID := m.AgentView.ActiveID()
	if node := m.ws.AgentMgr().OrchestratorFor(agentID); node != nil {
		return node.ID
	}
	return agentID
}

// ── Comments ────────────────────────────────────────────────────────────────

type planCommentEditedMsg struct {
	owner      shellport.Workspace
	generation uint64
	agentID    string
	edit       types.PlanCommentEdit
	err        error
	editor     *editor.Editor
}

// addPlanComment asks for a comment on the selection, or on the caret's line
// when nothing is selected.
func (m *App) addPlanComment(ed *editor.Editor) tea.Cmd {
	if ed == nil || !ed.IsOpen() {
		return nil
	}
	doc, ok := m.planDocumentOf(ed)
	if !ok {
		m.Feedback.NotifyInfo("You can comment on a proposed plan. Choose Review on the plan to open it.")
		return nil
	}
	if !doc.revision.Open() {
		m.Feedback.NotifyInfo(fmt.Sprintf("Plan revision %d is closed. Comment on revision %d.", doc.revision.Number, doc.latest))
		return nil
	}
	text := ed.Value()
	start, end := ed.SelectionOffsets()
	if start == end {
		start = strings.LastIndexByte(text[:min(start, len(text))], '\n') + 1
		if next := strings.IndexByte(text[start:], '\n'); next >= 0 {
			end = start + next
		} else {
			end = len(text)
		}
	}
	if end-start > types.PlanCommentMaxQuote {
		m.Feedback.NotifyInfo("The selection is too long for a comment. Select less text.")
		return nil
	}
	quote := anchor.Capture(text, start, end)
	m.openPlanCommentPrompt(ed, doc, types.PlanComment{ID: newPlanCommentID(), Anchor: quote}, types.PlanCommentAdd)
	return nil
}

// editPlanComment opens an existing comment for change. An empty comment is
// deleted.
func (m *App) editPlanComment(ed *editor.Editor, id string) tea.Cmd {
	doc, ok := m.planDocumentOf(ed)
	if !ok {
		return nil
	}
	for _, comment := range doc.revision.Comments {
		if comment.ID != id {
			continue
		}
		if comment.Sent {
			m.Feedback.NotifyInfo("This comment went to the agent. You cannot change it.")
			return nil
		}
		if !doc.revision.Open() {
			m.Feedback.NotifyInfo(fmt.Sprintf("Plan revision %d is closed. Comment on revision %d.", doc.revision.Number, doc.latest))
			return nil
		}
		m.openPlanCommentPrompt(ed, doc, comment, types.PlanCommentUpdate)
		return nil
	}
	return nil
}

// openPlanCommentPrompt edits one comment in the shared editor text prompt,
// anchored at the caret. A failed save reopens it with the typed text.
func (m *App) openPlanCommentPrompt(ed *editor.Editor, doc planDocument, comment types.PlanComment, op types.PlanCommentOp) {
	title, prompt := "Add comment", "On "+quotePreview(comment.Anchor.Exact)+"."
	if op == types.PlanCommentUpdate {
		title, prompt = "Edit comment", "On "+quotePreview(comment.Anchor.Exact)+". Leave it empty to delete it."
	}
	if comment.Anchor.Exact == "" {
		prompt = strings.TrimPrefix(prompt, "On "+quotePreview("")+". ")
		if op == types.PlanCommentAdd {
			prompt = "On an empty line."
		}
	}
	if ed != m.EditorState.Editor {
		m.focusEditorFor(ed)
	}
	agentID, revision := doc.agentID, doc.revision.Number
	m.openEditorTextPrompt(ui.TextPromptComment, title, prompt, comment.Body, func(value string) error {
		body := strings.TrimSpace(value)
		edit := types.PlanCommentEdit{Revision: revision, Op: op, Comment: comment}
		switch {
		case body == "" && op == types.PlanCommentAdd:
			return fmt.Errorf("type a comment, or press Esc to cancel")
		case body == "":
			edit.Op = types.PlanCommentDelete
		case len(body) > types.PlanCommentMaxBody:
			return fmt.Errorf("the comment is too long. Make it shorter")
		}
		edit.Comment.Body = body
		if edit.Comment.CreatedAt.IsZero() {
			edit.Comment.CreatedAt = time.Now().UTC()
		}
		m.runDetachedAsyncCmd(m.sendPlanCommentEdit(agentID, edit, ed))
		return nil
	})
}

func (m *App) sendPlanCommentEdit(agentID string, edit types.PlanCommentEdit, ed *editor.Editor) tea.Cmd {
	owner, generation := m.ws, m.workspaceSwitchGen.Load()
	return func() tea.Msg {
		return planCommentEditedMsg{owner: owner, generation: generation, agentID: agentID, edit: edit, editor: ed,
			err: owner.EditPlanComment(agentID, edit)}
	}
}

func (m *App) applyPlanCommentEdited(msg planCommentEditedMsg) tea.Cmd {
	if msg.owner != m.ws || msg.generation != m.workspaceSwitchGen.Load() {
		return nil
	}
	m.planReviewChanged()
	if msg.err == nil {
		switch msg.edit.Op {
		case types.PlanCommentAdd:
			m.Feedback.NotifyInfo("Comment added")
		case types.PlanCommentDelete:
			m.Feedback.NotifyInfo("Comment deleted")
		default:
			m.Feedback.NotifyInfo("Comment changed")
		}
		return nil
	}
	m.Feedback.NotifyError("The comment was not saved. " + sentence(msg.err.Error()))
	// Keep the typed text: reopen the prompt with it.
	if doc, ok := m.planDocumentOf(msg.editor); ok && msg.edit.Op != types.PlanCommentDelete && doc.revision.Open() {
		op := msg.edit.Op
		m.openPlanCommentPrompt(msg.editor, doc, msg.edit.Comment, op)
	}
	return nil
}

// stepPlanComment moves the caret to the next or previous comment.
func (m *App) stepPlanComment(forward bool) {
	ed := m.EditorState.Editor
	if _, ok := m.planDocumentOf(ed); !ok {
		m.Feedback.NotifyInfo("You can comment on a proposed plan. Choose Review on the plan to open it.")
		return
	}
	if _, ok := ed.NextReviewComment(forward); !ok {
		m.Feedback.NotifyInfo("This plan has no comments. Press Ctrl+Alt+C to add one.")
	}
}

// editPlanCommentAtCaret edits the comment at the caret, or adds one there.
func (m *App) editPlanCommentAtCaret() tea.Cmd {
	ed := m.EditorState.Editor
	if id, ok := ed.ReviewCommentAtCaret(); ok {
		return m.editPlanComment(ed, id)
	}
	return m.addPlanComment(ed)
}

func newPlanCommentID() string {
	return "c-" + rand.Text()[:20]
}

// quotePreview is a short quoted excerpt of commented text for prompts.
func quotePreview(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	if rendering.VisibleWidth(text) > 40 {
		text = rendering.TruncateVisual(text, 40, "…")
	}
	return "\"" + text + "\""
}

// sentence makes an error message a sentence that ends with a period.
func sentence(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	text = strings.ToUpper(text[:1]) + text[1:]
	if !strings.HasSuffix(text, ".") {
		text += "."
	}
	return text
}

func countNoun(count int, noun string) string {
	if count == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(count) + " " + noun + "s"
}

// ── Settling a revision ─────────────────────────────────────────────────────

type planSettledMsg struct {
	owner              shellport.Workspace
	generation         uint64
	sequence           uint64
	agentID            string
	destinationAgentID string
	settle             types.PlanReviewSettle
	err                error
}

func runPlanReviewSettlement(msg planSettledMsg) tea.Cmd {
	return func() tea.Msg {
		_, msg.err = msg.owner.SettlePlanReview(msg.agentID, msg.settle)
		return msg
	}
}

// settlePlanReview sends the user's choice for a revision. The plan text is
// what the user sees in an open editor; without one the core uses the
// document or file it owns. A changed plan is also saved, so the file holds
// the text the user approved.
func (m *App) settlePlanReview(agentID string, number int, action types.PlanReviewAction) tea.Cmd {
	settle := types.PlanReviewSettle{Revision: number, Action: action}
	var save tea.Cmd
	if review, ok := m.ws.PlanReviewForAgent(agentID); ok {
		if revision, ok := review.Revision(number); ok {
			if action == types.PlanReviewNewSession {
				settle.DestinationSessionID = revision.NewSessionID
			}
			if views := m.documentViews(revision.Path); settle.DestinationSessionID == "" && len(views) > 0 && views[0].Editor.IsOpen() {
				ed := views[0].Editor
				settle.Text = ed.Value()
				if ed.Modified() && !ed.ReadOnly() {
					save = m.requestEditorSave(ed, workspace.DocumentSaveManual, false)
				}
			}
		}
	}
	owner, generation := m.ws, m.workspaceSwitchGen.Load()
	msg := planSettledMsg{owner: owner, generation: generation, agentID: agentID, settle: settle}
	if action == types.PlanReviewNewSession {
		return tea.Batch(save, m.openPlanDestination(msg))
	}
	return tea.Batch(save, runPlanReviewSettlement(msg))
}

func (m *App) applyPlanSettled(msg planSettledMsg) tea.Cmd {
	if msg.owner != m.ws || msg.generation != m.workspaceSwitchGen.Load() {
		return nil
	}
	if msg.settle.Action == types.PlanReviewNewSession {
		if !m.sessionLifecyclePending || msg.sequence != m.sessionLifecycleSequence {
			return nil
		}
		m.sessionLifecyclePending = false
	}
	m.planReviewChanged()
	if msg.err != nil {
		m.Feedback.NotifyError(sentence(msg.err.Error()))
		if msg.settle.Action == types.PlanReviewNewSession {
			m.reofferPlanDecision(msg)
		}
		return nil
	}
	m.closePlanDecision(msg.agentID)
	switch msg.settle.Action {
	case types.PlanReviewNewSession:
		m.AddOrchestrator(msg.destinationAgentID)
	case types.PlanReviewImplement:
		// The core sent the message with the settlement.
		m.Feedback.NotifyInfo("Plan approved. Plan mode is off.")
	default:
		m.Feedback.NotifyInfo("Plan feedback sent")
	}
	return nil
}

// The failed choice closed the chat question, not the review.
func (m *App) reofferPlanDecision(msg planSettledMsg) {
	if m.ActiveSession != nil && m.ws.SessionsByAgent()[msg.agentID] == m.ActiveSession {
		if latest, ok := m.latestPlanRevision(msg.agentID); ok && latest.Open() && latest.Number == msg.settle.Revision {
			m.showPlanDecision(msg.agentID, latest)
		}
	}
}

// ── The plan decision in the chat ───────────────────────────────────────────

// offerPlanReview follows a completed Plan mode turn whose reply proposed a
// plan: the session recorded it as the newest revision. It asks only in the
// session the user is looking at, and only for that turn's proposal.
func (m *App) offerPlanReview(agentID string, generation uint64) {
	m.planReviewChanged()
	if !m.ws.PlanModeForAgent(agentID) || m.ActiveSession == nil || m.ws.SessionsByAgent()[agentID] != m.ActiveSession {
		return
	}
	revision, ok := m.latestPlanRevision(agentID)
	if !ok || !revision.Open() || generation == 0 || revision.Generation != generation {
		return
	}
	m.showPlanDecision(agentID, revision)
}

// showPlanDecision opens or refreshes the docked plan decision.
func (m *App) showPlanDecision(agentID string, revision types.PlanRevision) {
	spec, data := planDecisionSpec(revision)
	projection := m.activeSessionProjection()
	if projection == nil {
		return
	}
	if projection.decisionAgentID == agentID && projection.decision != nil && projection.decision.Purpose() == "implement_plan" {
		if projection.decisionData != data {
			projection.decision.Show(spec)
			projection.decisionData = data
			projection.deckHost.MarkLayout()
		}
		return
	}
	m.openSessionDecision(agentID, "Plan", spec)
	if projection.decisionAgentID == agentID {
		projection.decisionData = data
	}
}

// planDecisionSpec builds the decision for an open revision. Comments that
// are not sent must go to the agent before the plan can be implemented.
func planDecisionSpec(revision types.PlanRevision) (decision.Spec, string) {
	pending := revision.PendingComments()
	spec := decision.Spec{
		Purpose: "implement_plan", Title: "Implement this plan?", ReadingLayout: true,
		Facts:          []decision.Fact{{Label: "Plan", Value: fmt.Sprintf("%s · revision %d", revision.Title, revision.Number)}},
		Cancel:         decision.Answer{Label: "Keep planning", Shortcut: "n"},
		SemanticsLabel: "Implement the proposed plan",
	}
	review := decision.Answer{ID: planChoiceReview, Label: "Review", Shortcut: "r", Stay: true}
	mode := planDecisionImplement
	if revision.NewSessionID != "" {
		mode = planDecisionCheck
		spec.Title = "Check the new session?"
		spec.SemanticsLabel = "Check that the new session received the plan"
		spec.Hint = "Check that the new session received the plan. This does not send it again."
		spec.Confirm = decision.Answer{Label: "Check", Shortcut: "c"}
		spec.Alternates = []decision.Answer{review}
	} else if pending > 0 {
		mode = planDecisionSend
		spec.Title = "Send your comments?"
		spec.Facts = append(spec.Facts, decision.Fact{Label: "Comments", Value: countNoun(pending, "comment") + " not sent. The agent revises the plan."})
		spec.Confirm = decision.Answer{Label: "Send comments", Shortcut: "s"}
		spec.Alternates = []decision.Answer{review}
	} else {
		spec.Facts = append(spec.Facts,
			decision.Fact{Label: "Implement", Value: "Turn off Plan mode and implement this revision."},
			decision.Fact{Label: "New session", Value: "Implement this revision in a new session."},
			decision.Fact{Label: "Review", Value: "Open the plan to edit or add comments."})
		spec.Confirm = decision.Answer{Label: "Implement", Shortcut: "y"}
		spec.Alternates = []decision.Answer{{ID: planChoiceNewSession, Label: "New session", Shortcut: "c"}, review}
	}
	return spec, fmt.Sprintf("%d:%s:%d", revision.Number, mode, pending)
}

// syncPlanDecision keeps an open plan decision true to the record: a new
// comment changes its answers, and a revision settled elsewhere closes it.
func (m *App) syncPlanDecision() {
	if m.planDecisionEpoch == m.planReviewEpoch {
		return
	}
	m.planDecisionEpoch = m.planReviewEpoch
	projection := m.activeSessionProjection()
	if projection == nil || projection.decision == nil || projection.decisionAgentID == "" || projection.decision.Purpose() != "implement_plan" {
		return
	}
	agentID := projection.decisionAgentID
	number, _, _ := strings.Cut(projection.decisionData, ":")
	revision, ok := m.latestPlanRevision(agentID)
	if !ok || !revision.Open() || strconv.Itoa(revision.Number) != number {
		m.closePlanDecision(agentID)
		return
	}
	m.showPlanDecision(agentID, revision)
}

// closePlanDecision closes the plan decision of agentID's session, if open.
func (m *App) closePlanDecision(agentID string) {
	if m.sessionProjections == nil {
		return
	}
	for _, projection := range m.sessionProjections.items {
		if projection.decisionAgentID == agentID && projection.decision != nil && projection.decision.Purpose() == "implement_plan" {
			projection.decisionAgentID, projection.decisionData = "", ""
			m.closeProjectionPanel(projection, sessionDecisionPanelID)
		}
	}
}

// resolvePlanDecision carries out an answer to the plan decision.
func (m *App) resolvePlanDecision(agentID, data string, msg decision.ResultMsg) tea.Cmd {
	numberText, mode, _ := strings.Cut(data, ":")
	mode, _, _ = strings.Cut(mode, ":")
	number, err := strconv.Atoi(numberText)
	if err != nil || !msg.Confirmed {
		return nil
	}
	switch msg.Choice {
	case planChoiceReview:
		return m.openPlanRevision(agentID, number)
	case planChoiceNewSession:
		return m.settlePlanReview(agentID, number, types.PlanReviewNewSession)
	}
	if mode == planDecisionSend {
		return m.settlePlanReview(agentID, number, types.PlanReviewFeedback)
	}
	if mode == planDecisionCheck {
		return m.settlePlanReview(agentID, number, types.PlanReviewNewSession)
	}
	return m.settlePlanReview(agentID, number, types.PlanReviewImplement)
}

// planReviewMenuSection is the editor menu's plan section: comment commands
// first, then the revision's actions, as in the review bar.
func (m *App) planReviewMenuSection(ed *editor.Editor, doc planDocument) ui.ContextMenuSection {
	open := doc.revision.Open() && doc.revision.NewSessionID == ""
	label := "Add comment"
	if _, ok := ed.ReviewCommentAtCaret(); ok {
		label = "Edit comment"
	}
	items := []ui.ContextMenuItem{
		{Label: label, Detail: "Ctrl+Alt+C", Disabled: !open || ed.ReadOnly(), Action: func() { m.runDetachedAsyncCmd(m.editPlanCommentAtCaret()) }},
		{Label: "Next comment", Detail: "Alt+F9", Disabled: len(doc.revision.Comments) == 0, Action: func() { m.stepPlanComment(true) }},
		{Label: "Previous comment", Detail: "Shift+Alt+F9", Disabled: len(doc.revision.Comments) == 0, Action: func() { m.stepPlanComment(false) }},
	}
	agentID, number := doc.agentID, doc.revision.Number
	if len(doc.revision.Evidence) > 0 {
		items = append(items, ui.ContextMenuItem{Label: "Review investigation evidence", Action: func() { m.runDetachedAsyncCmd(m.planEvidenceMenu(agentID, []types.PlanRevision{doc.revision})) }})
	}
	if open && doc.revision.PendingComments() > 0 {
		items = append(items, ui.ContextMenuItem{Label: "Send comments", Action: func() {
			m.runDetachedAsyncCmd(m.settlePlanReview(agentID, number, types.PlanReviewFeedback))
		}})
	} else if open {
		items = append(items, ui.ContextMenuItem{Label: "Implement this plan", Action: func() {
			m.runDetachedAsyncCmd(m.settlePlanReview(agentID, number, types.PlanReviewImplement))
		}})
	}
	return ui.ContextMenuSection{SectionLabel: "Plan review", Items: items}
}
