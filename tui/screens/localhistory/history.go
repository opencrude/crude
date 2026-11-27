package localhistory

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/workspace/localhistory"
)

const (
	ScreenID         ui.ComponentID = "local-history"
	ListID           ui.ComponentID = "local-history/list"
	ScrollbarID      ui.ComponentID = "local-history/scrollbar"
	DiffID           ui.ComponentID = "local-history/diff"
	ActionsID        ui.ComponentID = "local-history/actions"
	ActionPreview                   = "history.preview"
	ActionApply                     = "history.apply"
	ActionCheck                     = "history.check_result"
	ActionSafetyOpen                = "history.safety.open"

	barRestore = "history.restore"
	barCancel  = "history.cancel"
	barUndo    = "history.undo"
	barCheck   = "history.check"
	toggleRow  = "history.toggle"
)

// PlanMsg delivers the immutable restore plan a preview reads.
type PlanMsg struct {
	Plan  localhistory.RestorePlan
	Error string
	// Generation is the preview this plan answers. A plan for an earlier
	// preview is dropped, so it can never be restored under a later heading.
	Generation uint64
}

// AppliedMsg is the outcome of Restore.
type AppliedMsg struct {
	Result     localhistory.RestoreResult
	Error      string
	Warning    string
	RequestID  string // unresolved original mutation; only observation is allowed
	Incomplete bool   // retained safety evidence, not a fully completed restore
}

// PreviewRequest names what to restore. Heading is the dialog's question in
// the user's words, such as "Undo Bisi's changes?".
type PreviewRequest struct {
	SnapshotID string
	Paths      []string
	SessionID  string
	MessageID  string
	AgentID    string
	Heading    string
	// Generation is set by Begin; the plan read for it carries it back.
	Generation uint64
}

// ApplyRequest restores the reviewed plan with the chosen files.
type ApplyRequest struct {
	PlanID  string
	Choices []localhistory.RestoreChoice
}

// Screen is the body of the Restore preview dialog. It lists the files a
// restore changes with a check box each, shows the selected file's diff, and
// restores only with Restore. Its state grants no authority: the owner plans,
// applies, keeps receipts and checks generations.
type Screen struct {
	ui.Base
	tokens      *ui.TokenSet
	list        *changeList
	listBar     *ui.ScrollbarControl
	diff        *diffText
	bar         *ui.ActionBar
	request     PreviewRequest
	plan        *localhistory.RestorePlan
	selected    map[string]bool
	exact       map[string]bool
	loading     bool
	applying    bool
	errorText   string
	status      string
	lastRestore *localhistory.RestoreResult
	incomplete  bool
	requestID   string
	generation  uint64
	wide        bool
	divider     int
}

// New builds an empty dialog body that shows it is reading the plan.
func New(th *theme.Theme) *Screen {
	if th == nil {
		th = theme.DefaultDark()
	}
	tokens := ui.NewTokenSet(th)
	s := &Screen{Base: ui.NewBase(ScreenID), tokens: tokens, selected: map[string]bool{}, exact: map[string]bool{}, loading: true, divider: -1}
	s.list = &changeList{List: ui.NewList(ListID, tokens), owner: s}
	s.list.SetWrap(false)
	s.list.SetActivateAction(toggleRow)
	s.list.SetDefaultRowBackground(ui.ColorPopupBg)
	s.listBar = ui.NewScrollbarControl(ScrollbarID, s.list.Scroll(), tokens)
	s.diff = newDiffText(DiffID, tokens)
	s.bar = ui.NewActionBar(ActionsID, tokens)
	s.bar.SetShowRule(true)
	s.bar.SetSurfaceBackground(ui.ColorPopupBg)
	s.bar.SetCancelAction(barCancel)
	s.SetTokens(tokens)
	s.sync()
	return s
}

// SetTokens applies a theme.
func (s *Screen) SetTokens(tokens *ui.TokenSet) {
	s.tokens = tokens
	s.list.SetTokens(tokens)
	s.listBar.SetTokens(tokens)
	s.listBar.SetSurfaceBackground(ui.ColorPopupBg)
	s.diff.SetTokens(tokens)
	s.bar.SetTokens(tokens)
}

// Begin shows that request's plan is being read.
func (s *Screen) Begin(request PreviewRequest) {
	if s.requestID != "" {
		return // An unknown outcome allows only checking that outcome.
	}
	s.generation++
	request.Generation = s.generation
	s.request = request
	s.plan, s.lastRestore, s.errorText, s.status = nil, nil, "", ""
	s.loading, s.applying = true, false
	s.sync()
}

// FocusedAction is the action of the focused button: Restore, Cancel, Undo
// this restore or Check the result.
func (s *Screen) FocusedAction() string { return s.bar.FocusedAction() }

// Restoring reports whether the focused button applies the reviewed plan.
func (s *Screen) Restoring() bool { return s.bar.FocusedAction() == barRestore }

// Request is what the dialog previews.
func (s *Screen) Request() PreviewRequest { return s.request }

// PreferredFocus is the file list, so the review starts with the files;
// without files to review it is the buttons.
func (s *Screen) PreferredFocus() ui.ComponentID {
	if s.plan == nil || len(s.plan.Changes) == 0 {
		return ActionsID
	}
	return ListID
}

// Children are the list, the diff and the actions.
func (s *Screen) Children() []ui.Component {
	var children []ui.Component
	for _, child := range []ui.Component{s.list, s.listBar, s.diff, s.diff.bar, s.bar} {
		if !child.Bounds().Empty() {
			children = append(children, child)
		}
	}
	return children
}

// Measure prefers room for the list and a readable diff.
func (s *Screen) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
	return cs.ClampSize(ui.Size{W: 100, H: 30})
}

const reviewHeader = 3 // question, detail, gap

// Layout places the question, the list beside or above the diff, and the
// action row at the bottom.
func (s *Screen) Layout(ctx ui.LayoutContext, r ui.Rect) {
	s.SetBounds(r)
	for _, child := range []ui.Component{s.list, s.listBar, s.diff, s.diff.bar, s.bar} {
		child.Layout(ctx, ui.Rect{})
	}
	barH := min(r.H, s.bar.PreferredHeightAtWidth(r.W))
	s.bar.Layout(ctx, ui.Rect{Y: r.H - barH, W: r.W, H: barH})
	top := min(reviewHeader, r.H-barH)
	bodyH := max(0, r.H-barH-top)
	s.divider = -1
	if s.plan == nil || len(s.plan.Changes) == 0 || bodyH < 3 {
		return
	}
	s.wide = r.W >= 90
	listRect := ui.Rect{Y: top, W: r.W, H: bodyH}
	var diffRect ui.Rect
	if s.wide {
		listW := max(30, min(r.W*2/5, 56))
		listRect.W = listW
		s.divider = listW
		diffRect = ui.Rect{X: listW + 1, Y: top, W: r.W - listW - 1, H: bodyH}
	} else {
		listH := min(len(s.plan.Changes), max(2, bodyH/3))
		listRect.H = listH
		diffRect = ui.Rect{Y: top + listH + 1, W: r.W, H: max(0, bodyH-listH-1)}
	}
	barW := 0
	if s.listBar.NeededForViewport(listRect.H) {
		barW = ui.ScrollbarWidth
	}
	s.list.Layout(ctx, ui.Rect{X: listRect.X, Y: listRect.Y, W: max(0, listRect.W-barW), H: listRect.H})
	s.listBar.Layout(ctx, ui.Rect{X: listRect.X + listRect.W - barW, Y: listRect.Y, W: barW, H: listRect.H})
	s.diff.Layout(ctx, diffRect)
}

// Paint draws the question, the detail line and the divider; children paint
// themselves.
func (s *Screen) Paint(buf *surface.CellBuffer, ox, oy int, _ ui.PaintContext) {
	r := s.Bounds()
	bg := s.tokens.Color(ui.ColorPopupBg)
	ui.FillRect(buf, ox, oy, r, cell.BlankCell(bg))
	x, y := ox+r.X, oy+r.Y
	question, detail, role := s.text()
	buf.WriteString(x, y, truncate(question, r.W), s.tokens.Color(ui.ColorFg), bg, cell.AttrBold)
	if r.H > 1 {
		buf.WriteString(x, y+1, truncate(detail, r.W), s.tokens.Color(role), bg, 0)
	}
	line := s.tokens.Color(ui.ColorPopupDivider)
	if s.divider >= 0 {
		list := s.list.Bounds()
		for row := list.Y; row < list.Y+list.H; row++ {
			buf.Set(x+s.divider, y+row, cell.NewCell('│', line, bg, 0))
		}
	} else if diff := s.diff.Bounds(); !diff.Empty() && diff.Y > 0 {
		for col := 0; col < r.W; col++ {
			buf.Set(x+col, y+diff.Y-1, cell.NewCell('─', line, bg, 0))
		}
	}
}

// text is the question, the detail line and its color for the state.
func (s *Screen) text() (string, string, ui.SemanticColor) {
	question := s.request.Heading
	if question == "" {
		question = "Restore files?"
	}
	switch {
	case s.requestID != "":
		return "Check the restore result", s.errorText, ui.ColorWarning
	case s.lastRestore != nil:
		return s.status, "Undo this restore puts back the files as they were before it.", ui.ColorMuted
	case s.applying:
		return question, "Restoring the files…", ui.ColorMuted
	case s.loading:
		return question, "Preparing the restore preview…", ui.ColorMuted
	case s.errorText != "":
		return question, s.errorText, ui.ColorError
	case s.plan == nil:
		return question, "", ui.ColorMuted
	case len(s.plan.Changes) == 0:
		if s.plan.MessageID != "" {
			return question, "These changes are already undone. No file needs to change.", ui.ColorMuted
		}
		return question, "The files already match this point. No file needs to change.", ui.ColorMuted
	case s.status != "":
		return question, s.status, ui.ColorWarning
	}
	if exact := s.exactCount(); exact > 0 {
		return question, fmt.Sprintf("Later edits in %s will be lost. Crude keeps a copy of the current files first.", fileCount(exact)), ui.ColorWarning
	}
	return question, "Choose the files to restore. Crude keeps a copy of the current files first.", ui.ColorMuted
}

func (s *Screen) exactCount() int {
	n := 0
	for path, exact := range s.exact {
		if exact && s.selected[path] {
			n++
		}
	}
	return n
}

func (s *Screen) choices() []localhistory.RestoreChoice {
	if s.plan == nil {
		return nil
	}
	choices := make([]localhistory.RestoreChoice, 0, len(s.plan.Changes))
	for _, c := range s.plan.Changes {
		if s.selected[c.Path] {
			choices = append(choices, localhistory.RestoreChoice{Path: c.Path, Exact: s.exact[c.Path]})
		}
	}
	return choices
}

// sync rebuilds the action row for the state. Cancel stays focused while a
// restore is possible, so a stray Enter never changes files.
func (s *Screen) sync() {
	button := func(label, action string, variant ui.ButtonVariant, enabled bool) ui.ActionBarButton {
		return ui.ActionBarButton{Label: label, Action: action, Variant: variant, Enabled: enabled}
	}
	switch {
	case s.requestID != "":
		s.bar.SetButtons(button("Check the result", barCheck, ui.VariantPrimary, !s.loading), button("Close", barCancel, ui.VariantSecondary, true))
		s.bar.FocusFirst()
	case s.lastRestore != nil:
		s.bar.SetButtons(button("Undo this restore", barUndo, ui.VariantSecondary, s.lastRestore.SafetySnapshotID != ""), button("Close", barCancel, ui.VariantSecondary, true))
		s.bar.FocusLast()
	case s.applying:
		s.bar.SetButtons(button("Restoring…", barRestore, ui.VariantPrimary, false))
	case s.plan != nil && len(s.plan.Changes) > 0:
		count := len(s.choices())
		label, variant := "Restore "+fileCount(count), ui.VariantPrimary
		if count == 0 {
			label = "Restore files"
		}
		if s.exactCount() > 0 {
			variant = ui.VariantDanger
		}
		s.bar.SetButtons(button(label, barRestore, variant, count > 0), button("Cancel", barCancel, ui.VariantSecondary, true))
		s.bar.FocusLast()
	case s.loading:
		s.bar.SetButtons(button("Cancel", barCancel, ui.VariantSecondary, true))
	default:
		s.bar.SetButtons(button("Close", barCancel, ui.VariantSecondary, true))
	}
	s.bar.SetHint(s.hint())
}

func (s *Screen) hint() string {
	if s.plan == nil || len(s.plan.Changes) == 0 || s.applying || s.lastRestore != nil || s.requestID != "" {
		return ""
	}
	for _, c := range s.plan.Changes {
		if c.ConflictReason != "" && c.ExactAllowed {
			return "space select · e replace later edits · esc cancel"
		}
	}
	return "space select · esc cancel"
}

// Handle owns the dialog keys; the list, diff and actions handle the rest.
func (s *Screen) Handle(_ ui.EventContext, evt ui.Event) ui.EventResult {
	if evt.Kind == ui.EventCustom {
		switch msg := evt.Custom.(type) {
		case PlanMsg:
			s.setPlan(msg)
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
		case AppliedMsg:
			s.setApplied(msg)
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint | ui.InvalidateFocus}
		}
		return ui.EventResult{}
	}
	if !evt.IsKey() {
		return ui.EventResult{}
	}
	switch evt.Key.String() {
	case "esc":
		return ui.EventResult{Handled: true, Action: &ui.Action{ID: "window.close"}}
	case "u", "U":
		if s.lastRestore != nil && s.lastRestore.SafetySnapshotID != "" {
			return s.HandleAction(ui.EventContext{}, ui.Action{ID: barUndo})
		}
	}
	return ui.EventResult{}
}

// focusTo moves keyboard focus inside the dialog: list, diff, then actions.
func focusTo(id ui.ComponentID) ui.EventResult {
	return ui.EventResult{Handled: true, Action: &ui.Action{ID: "ui.focus", Payload: id}, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
}

// HandleAction turns the action row, list rows and the diff into intents.
func (s *Screen) HandleAction(_ ui.EventContext, action ui.Action) ui.EventResult {
	switch action.ID {
	case barCancel:
		return ui.EventResult{Handled: true, Action: &ui.Action{ID: "window.close"}}
	case barRestore:
		if s.plan == nil || s.loading || s.applying || s.requestID != "" {
			return ui.EventResult{Handled: true}
		}
		choices := s.choices()
		if len(choices) == 0 {
			s.status = "Select a file to restore. Press Space to select it."
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
		}
		s.applying = true
		s.sync()
		return ui.EventResult{Handled: true, Action: &ui.Action{ID: ActionApply, Payload: ApplyRequest{PlanID: s.plan.ID, Choices: choices}}, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
	case barUndo:
		if s.lastRestore == nil || s.lastRestore.SafetySnapshotID == "" {
			return ui.EventResult{Handled: true}
		}
		s.Begin(PreviewRequest{SnapshotID: s.lastRestore.SafetySnapshotID, Heading: "Undo this restore?"})
		return ui.EventResult{Handled: true, Action: &ui.Action{ID: ActionPreview, Payload: s.request}, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
	case barCheck:
		if s.requestID == "" || s.loading {
			return ui.EventResult{Handled: true}
		}
		s.loading = true
		s.sync()
		return ui.EventResult{Handled: true, Action: &ui.Action{ID: ActionCheck, Payload: s.requestID}, Invalidate: ui.InvalidatePaint}
	case toggleRow:
		s.toggleSelected()
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
	case "actionbar.exit.forward":
		if s.list.Bounds().Empty() {
			return ui.EventResult{Handled: true}
		}
		return focusTo(ListID)
	case "actionbar.exit.backward":
		if s.diff.Bounds().Empty() {
			return ui.EventResult{Handled: true}
		}
		return focusTo(DiffID)
	}
	return ui.EventResult{Action: &action}
}

// CancelPointerCapture ends a scrollbar drag when the dialog closes.
func (s *Screen) CancelPointerCapture() {
	s.listBar.CancelPointerCapture()
	s.diff.bar.CancelPointerCapture()
}

func (s *Screen) setPlan(msg PlanMsg) {
	if s.requestID != "" || msg.Generation != s.request.Generation {
		return
	}
	s.loading, s.applying, s.status = false, false, ""
	s.errorText = msg.Error
	if msg.Error != "" {
		s.plan = nil
		s.sync()
		return
	}
	plan := msg.Plan
	s.plan = &plan
	s.selected, s.exact = map[string]bool{}, map[string]bool{}
	for _, c := range plan.Changes {
		if c.UnsafeReason == "" && c.ConflictReason == "" {
			s.selected[c.Path] = true
		}
	}
	s.rebuildPlan()
	s.list.SelectIndex(0)
	s.showSelected()
	s.sync()
}

func (s *Screen) setApplied(msg AppliedMsg) {
	s.loading, s.applying = false, false
	switch {
	case msg.RequestID != "":
		s.requestID, s.errorText = msg.RequestID, msg.Error
		s.plan, s.lastRestore = nil, nil
	case msg.Error != "":
		// A refusal invalidates the reviewed plan; restoring needs a new
		// preview of the current files.
		s.requestID, s.plan, s.lastRestore = "", nil, nil
		s.errorText = msg.Error + " Close this preview and open it again to review the current files."
	default:
		s.errorText, s.requestID = "", ""
		result := msg.Result
		s.lastRestore, s.plan, s.incomplete = &result, nil, msg.Incomplete
		s.status = "Restored " + fileCount(len(result.Paths)) + "."
		if msg.Incomplete {
			s.status = "The restore did not complete. Undo this restore puts back the files as they were."
		}
		if msg.Warning != "" {
			s.status += " " + msg.Warning
		}
	}
	s.list.SetItems(nil)
	s.diff.SetText("", "")
	s.sync()
}

func (s *Screen) rebuildPlan() {
	if s.plan == nil {
		return
	}
	items := make([]ui.ListItem, 0, len(s.plan.Changes))
	for _, c := range s.plan.Changes {
		mark := "[ ]"
		if s.selected[c.Path] {
			mark = "[x]"
		}
		items = append(items, ui.ListItem{ID: c.Path, Label: mark + " " + c.Path, Detail: changeDetail(c, s.exact[c.Path]), Disabled: c.UnsafeReason != "", Payload: c})
	}
	s.list.SetItemsStable(items)
}

func changeDetail(c localhistory.Change, exact bool) string {
	switch {
	case c.UnsafeReason != "":
		return "cannot restore: " + c.UnsafeReason
	case c.ConflictReason != "" && exact:
		return "replace · later edits are lost"
	case c.ConflictReason != "" && c.ExactAllowed:
		return "changed later · e to replace"
	case c.ConflictReason != "":
		return "changed later"
	case c.AutoMerged:
		return "keeps later edits"
	case c.MovePeer != "":
		return "moves with " + c.MovePeer
	}
	switch c.Action {
	case localhistory.ChangeCreate:
		return "comes back"
	case localhistory.ChangeDelete:
		return "is removed"
	}
	return "restored"
}

func (s *Screen) selectedChange() (localhistory.Change, bool) {
	item, ok := s.list.Selected()
	if !ok {
		return localhistory.Change{}, false
	}
	c, ok := item.Payload.(localhistory.Change)
	return c, ok
}

// showSelected shows the selected file's diff.
func (s *Screen) showSelected() {
	c, ok := s.selectedChange()
	if !ok {
		s.diff.SetText("", "Select a file to see what changes.")
		return
	}
	empty := "Only the file's type or permissions change."
	if c.Action == localhistory.ChangeDelete {
		empty = "This file is removed."
	}
	s.diff.SetText(c.TextDiff, empty)
}

func (s *Screen) toggleSelected() {
	c, ok := s.selectedChange()
	if !ok || c.UnsafeReason != "" || s.applying {
		return
	}
	if c.ConflictReason != "" && !s.exact[c.Path] {
		s.status = "This file changed later. Press E to replace it, or leave it as it is."
		if !c.ExactAllowed {
			s.status = "This file changed later and cannot be replaced safely."
		}
		return
	}
	selected := !s.selected[c.Path]
	paths := c.AtomicPaths
	if len(paths) == 0 {
		paths = []string{c.Path}
	}
	for _, path := range paths {
		s.selected[path] = selected
	}
	s.status = ""
	s.rebuildPlan()
	s.sync()
}

func (s *Screen) toggleExact() {
	c, ok := s.selectedChange()
	if !ok || c.ConflictReason == "" || !c.ExactAllowed || s.applying {
		return
	}
	s.exact[c.Path] = !s.exact[c.Path]
	s.selected[c.Path] = s.exact[c.Path]
	s.status = ""
	s.rebuildPlan()
	s.sync()
}

// Semantics names the dialog body.
func (s *Screen) Semantics() ui.Semantics {
	question, detail, _ := s.text()
	return ui.Semantics{Role: ui.RoleGroup, Label: question, Description: detail}
}

// changeList is the file list. Space and Enter tick a file; E replaces a file
// that changed later; moving the selection shows that file's diff.
type changeList struct {
	*ui.List
	owner *Screen
}

func (l *changeList) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	if evt.IsKey() {
		switch evt.Key.String() {
		case "tab":
			return focusTo(DiffID)
		case "shift+tab":
			return focusTo(ActionsID)
		case "space", " ":
			l.owner.toggleSelected()
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
		case "e", "E":
			l.owner.toggleExact()
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
		}
	}
	before := l.SelectedIndex()
	result := l.List.Handle(ctx, evt)
	if l.SelectedIndex() != before {
		l.owner.showSelected()
		result.Handled = true
		result.Invalidate |= ui.InvalidatePaint
	}
	return result
}

// diffText is a read-only, scrolling unified diff with added lines, removed
// lines and hunk headings in the diff colors.
type diffText struct {
	ui.Base
	tokens *ui.TokenSet
	lines  []string
	empty  string
	scroll *ui.ScrollModel
	bar    *ui.ScrollbarControl
}

func newDiffText(id ui.ComponentID, tokens *ui.TokenSet) *diffText {
	d := &diffText{Base: ui.NewBase(id), tokens: tokens, scroll: ui.NewScrollModel()}
	d.bar = ui.NewScrollbarControl(ui.NewID(id, "scrollbar"), d.scroll, tokens)
	return d
}

func (d *diffText) SetTokens(tokens *ui.TokenSet) {
	d.tokens = tokens
	d.bar.SetTokens(tokens)
	d.bar.SetSurfaceBackground(ui.ColorPopupBg)
}

// SetText shows a unified diff from its first line; empty explains a change
// without text.
func (d *diffText) SetText(diff, empty string) {
	d.lines, d.empty = nil, empty
	for _, line := range strings.Split(strings.TrimRight(diff, "\n"), "\n") {
		if strings.HasPrefix(line, "--- ") || strings.HasPrefix(line, "+++ ") || line == "" && len(d.lines) == 0 {
			continue
		}
		d.lines = append(d.lines, strings.ReplaceAll(line, "\t", "    "))
	}
	d.scroll.ScrollTo(0)
}

func (d *diffText) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
	return cs.ClampSize(ui.Size{W: cs.MaxW, H: cs.MaxH})
}

func (d *diffText) Layout(ctx ui.LayoutContext, r ui.Rect) {
	d.SetBounds(r)
	d.scroll.SetViewport(r.H)
	d.scroll.SetContent(len(d.lines))
	d.bar.Layout(ctx, ui.Rect{})
	if !r.Empty() && d.bar.NeededForViewport(r.H) {
		d.bar.Layout(ctx, ui.Rect{X: r.X + r.W - ui.ScrollbarWidth, Y: r.Y, W: ui.ScrollbarWidth, H: r.H})
	}
}

func (d *diffText) Paint(buf *surface.CellBuffer, ox, oy int, ctx ui.PaintContext) {
	r := d.Bounds()
	bg := d.tokens.Color(ui.ColorPopupBg)
	ui.FillRect(buf, ox, oy, r, cell.BlankCell(bg))
	width := r.W - 1
	if !d.bar.Bounds().Empty() {
		width -= ui.ScrollbarWidth
	}
	x, y := ox+r.X+1, oy+r.Y
	if len(d.lines) == 0 {
		buf.WriteString(x, y, truncate(d.empty, width), d.tokens.Color(ui.ColorMuted), bg, 0)
		return
	}
	for row := 0; row < r.H; row++ {
		index := d.scroll.Offset() + row
		if index >= len(d.lines) {
			break
		}
		line := d.lines[index]
		fg, lineBg, attr := d.tokens.Color(ui.ColorDiffContext), bg, cell.Attrs(0)
		switch {
		case strings.HasPrefix(line, "@@"):
			fg, attr = d.tokens.Color(ui.ColorDiffHunk), cell.AttrBold
		case strings.HasPrefix(line, "+"):
			fg, lineBg = d.tokens.Color(ui.ColorDiffAddition), d.tokens.Color(ui.ColorDiffAdditionBg)
		case strings.HasPrefix(line, "-"):
			fg, lineBg = d.tokens.Color(ui.ColorDiffDeletion), d.tokens.Color(ui.ColorDiffDeletionBg)
		}
		if lineBg != bg {
			ui.FillRect(buf, ox+r.X, y+row, ui.Rect{W: width + 1, H: 1}, cell.BlankCell(lineBg))
		}
		buf.WriteString(x, y+row, rendering.TruncateVisual(line, width, "…"), fg, lineBg, attr)
	}
	if ctx.Focused {
		buf.Set(ox+r.X, y, cell.NewCell('▎', d.tokens.Color(ui.ColorAccent), bg, 0))
	}
}

func (d *diffText) Handle(_ ui.EventContext, evt ui.Event) ui.EventResult {
	before := d.scroll.Offset()
	switch {
	case evt.Kind == ui.EventMouseWheel && evt.MouseWheel.Button == tea.MouseWheelUp:
		d.scroll.ScrollBy(-3)
	case evt.Kind == ui.EventMouseWheel && evt.MouseWheel.Button == tea.MouseWheelDown:
		d.scroll.ScrollBy(3)
	case evt.IsKey():
		switch evt.Key.String() {
		case "tab":
			return focusTo(ActionsID)
		case "shift+tab":
			return focusTo(ListID)
		case "up":
			d.scroll.ScrollBy(-1)
		case "down":
			d.scroll.ScrollBy(1)
		case "pgup":
			d.scroll.ScrollBy(-max(1, d.scroll.Viewport()-1))
		case "pgdown", "space":
			d.scroll.ScrollBy(max(1, d.scroll.Viewport()-1))
		case "home":
			d.scroll.ScrollTo(0)
		case "end":
			d.scroll.ScrollTo(d.scroll.MaxOffset())
		default:
			return ui.EventResult{}
		}
	default:
		return ui.EventResult{}
	}
	if d.scroll.Offset() == before && !evt.IsKey() {
		return ui.EventResult{}
	}
	return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
}

func (d *diffText) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleGroup, Label: "Changes in the selected file"}
}

func truncate(text string, width int) string { return rendering.TruncateVisual(text, width, "…") }

// PointTime names when a history point was recorded: today and yesterday by
// name, other days by date.
func PointTime(at, now time.Time) string {
	return strings.Join(strings.Fields(snapshotTime(at, now)), " ")
}

func snapshotTime(at, now time.Time) string {
	const day = "2006-01-02"
	switch at.Format(day) {
	case now.Format(day):
		return "Today     " + at.Format("15:04:05")
	case now.AddDate(0, 0, -1).Format(day):
		return "Yesterday " + at.Format("15:04:05")
	}
	if at.Year() == now.Year() {
		return at.Format("Jan 02    15:04:05")
	}
	return at.Format("2006-01-02 15:04")
}

func countLabel(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func fileCount(n int) string { return countLabel(n, "file") }

var _ ui.Parent = (*Screen)(nil)
var _ ui.ActionHandler = (*Screen)(nil)
