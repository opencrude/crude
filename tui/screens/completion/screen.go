// Package completion is an inert completion chooser body. Its parent owns the
// invocation, requests, source/context validation, placement and actual edits.
package completion

import (
	"strconv"
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types/control"
)

const (
	ScreenID ui.ComponentID = "completion"
	ListID   ui.ComponentID = "completion/list"
	TextID   ui.ComponentID = "completion/documentation"

	ActionSelection = "completion.selection"
	ActionAccept    = "completion.accept"
	ActionClose     = "completion.close"

	rowSelection = "completion.row.selection"
	rowActivate  = "completion.row.activate"
)

// Entry is display data, not an edit or provider capability. IDs must be
// nonempty and unique; ambiguous rows are displayed but cannot be selected.
type Entry struct {
	ID, Label, Detail, Documentation, DocumentationState string
	Kind                                                 int
}

// Action carries original identities only. The parent must validate its current
// invocation and source before starting a request or applying any mutation.
type Action struct{ Invocation, ItemID string }

type Screen struct {
	ui.Base
	tokens     *ui.TokenSet
	invocation string
	entries    []Entry
	byID       map[string]int
	list       *chooserList
	scrollbar  *ui.ScrollbarControl
	text       *documentation
	docScroll  *ui.ScrollbarControl
	caption    *ui.Label
	renderedID string // documentation projection key, never selection authority
	acceptance string
	measureAt  ui.Constraints
	measureRev uint64
	contentRev uint64
	measure    ui.Size
	measured   bool
	hasDetails bool
	listW      int // measured list column width; documentation takes the rest
}

// Compact chooser bounds: rows show at most this many suggestions, and labels
// and details are clipped so the list stays close to the typed text.
const (
	maxRows        = 8
	maxLabelWidth  = 40
	maxDetailWidth = 28
	docWidth       = 44
	stackedDocRows = 5
)

// New retains a detached display snapshot. It never dispatches initial requests.
// The containing Host should focus DefaultFocus after mounting, and invalidate
// paint/layout after delivering SetDocumentation.
func New(th *theme.Theme, invocation string, entries []Entry, status string) *Screen {
	if th == nil {
		th = theme.DefaultDark()
	}
	s := &Screen{Base: ui.NewBase(ScreenID), invocation: strings.Clone(invocation),
		entries: make([]Entry, len(entries)), byID: make(map[string]int, len(entries))}
	for i, e := range entries {
		s.entries[i] = Entry{ID: strings.Clone(e.ID), Label: strings.Clone(e.Label), Detail: strings.Clone(e.Detail),
			Documentation: strings.Clone(e.Documentation), DocumentationState: strings.Clone(e.DocumentationState), Kind: e.Kind}
		if _, exists := s.byID[e.ID]; exists || e.ID == "" {
			s.byID[e.ID] = -1
		} else {
			s.byID[e.ID] = i
		}
	}
	ts := ui.NewTokenSet(th)
	s.list = &chooserList{List: ui.NewList(ListID, ts), screen: s}
	s.list.SetActivateAction(rowActivate)
	s.list.SetDefaultRowBackground(ui.ColorPopupBg)
	s.list.SetItems(s.rows())
	s.scrollbar = ui.NewScrollbarControl("completion/scrollbar", s.list.Scroll(), ts)
	s.text = &documentation{TextArea: ui.NewTextArea(TextID, ts, true), screen: s}
	s.text.SetReadOnly(true)
	s.docScroll = ui.NewScrollbarControl("completion/documentation-scrollbar", s.text.Scroll(), ts)
	s.docScroll.SetPointerTargetSize(1)
	s.docScroll.SetSurfaceBackground(ui.ColorPopupBg)
	s.caption = ui.NewLabel("completion/status", singleLine(status), th)
	s.caption.SetColorRole(ui.ColorMuted)
	s.SetAcceptance("tab-enter")
	s.SetTokens(ts)
	s.prepareDocumentation()
	return s
}

func (s *Screen) DefaultFocus() ui.ComponentID { return ListID }

// SetAcceptance updates which keys the native chooser accepts with. The
// containing editor routes a non-accepting key to normal source editing.
func (s *Screen) SetAcceptance(policy string) bool {
	if s.acceptance == policy {
		return false
	}
	s.acceptance = policy
	return true
}

func (s *Screen) rows() []ui.ListItem {
	rows := make([]ui.ListItem, len(s.entries))
	for i, e := range s.entries {
		rows[i] = ui.ListItem{ID: e.ID, Label: kindGlyph(e.Kind) + " " + clip(singleLine(e.Label), maxLabelWidth),
			Detail: clip(singleLine(e.Detail), maxDetailWidth), Disabled: s.byID[e.ID] < 0, Payload: Action{s.invocation, e.ID}}
	}
	return rows
}

// Invocation names the original result set these rows display.
func (s *Screen) Invocation() string { return s.invocation }

// ClearSelection shows the rows without choosing one. The parent offers the
// top row inline; Up or Down chooses a row and shows its documentation.
func (s *Screen) ClearSelection() {
	s.list.Selection().ClearSelection()
	s.list.Scroll().ScrollTo(0)
	s.prepareDocumentation()
}

// TopID is the first selectable row, the one offered without a choice.
func (s *Screen) TopID() string {
	for _, e := range s.entries {
		if s.byID[e.ID] >= 0 {
			return e.ID
		}
	}
	return ""
}

// SoleID identifies one visible, selectable row. Duplicate identities cannot
// become an implicit choice, even when their labels look equal.
func (s *Screen) SoleID() string {
	if len(s.entries) != 1 || s.byID[s.entries[0].ID] < 0 {
		return ""
	}
	return s.entries[0].ID
}

// Select chooses one row, as Down or a click would.
func (s *Screen) Select(id string) bool {
	i, ok := s.byID[id]
	if !ok || i < 0 || !s.list.SelectIndex(i) {
		return false
	}
	s.list.Scroll().EnsureVisible(i)
	s.prepareDocumentation()
	return true
}

// SetStatus replaces the one-line status shown above the rows.
func (s *Screen) SetStatus(status string) {
	s.caption.SetText(singleLine(status))
	s.contentRev++
}

// Entry returns the display data of one row.
func (s *Screen) Entry(id string) (Entry, bool) {
	i, ok := s.byID[id]
	if !ok || i < 0 {
		return Entry{}, false
	}
	return s.entries[i], true
}

// SetEntries replaces the rows with those of a newer result set while the
// chooser stays open. A chosen row stays chosen when the new set has a row with
// the same label and kind; otherwise the chooser returns to no choice.
func (s *Screen) SetEntries(invocation string, entries []Entry) {
	var keep *Entry
	if e, ok := s.Entry(s.SelectedID()); ok {
		keep = &e
	}
	s.invocation = strings.Clone(invocation)
	s.entries = make([]Entry, len(entries))
	s.byID = make(map[string]int, len(entries))
	for i, e := range entries {
		s.entries[i] = Entry{ID: strings.Clone(e.ID), Label: strings.Clone(e.Label), Detail: strings.Clone(e.Detail),
			Documentation: strings.Clone(e.Documentation), DocumentationState: strings.Clone(e.DocumentationState), Kind: e.Kind}
		if _, exists := s.byID[e.ID]; exists || e.ID == "" {
			s.byID[e.ID] = -1
		} else {
			s.byID[e.ID] = i
		}
	}
	s.list.SetItems(s.rows())
	s.list.Selection().ClearSelection()
	s.list.Scroll().ScrollTo(0)
	if keep != nil {
		for i, e := range s.entries {
			if e.Label == keep.Label && e.Kind == keep.Kind && s.byID[e.ID] == i {
				s.list.SelectIndex(i)
				break
			}
		}
	}
	s.prepareDocumentation()
}

func clip(value string, width int) string {
	return rendering.TruncateVisual(value, width, "…")
}

func (s *Screen) SelectedID() string {
	if row, ok := s.list.Selected(); ok && row.Selectable() {
		return row.ID
	}
	return ""
}

// SetDocumentation updates only a row belonging to this original invocation.
// An offscreen/nonselected result is cached without replacing the visible row's
// documentation. State is plain parent-supplied text, never authorization.
func (s *Screen) SetDocumentation(invocation, itemID, detail, documentation, status string) bool {
	i, ok := s.byID[itemID]
	if invocation != s.invocation || !ok || i < 0 {
		return false
	}
	e := &s.entries[i]
	if e.Detail == detail && e.Documentation == documentation && e.DocumentationState == status {
		return true
	}
	e.Detail, e.Documentation, e.DocumentationState =
		strings.Clone(detail), strings.Clone(documentation), strings.Clone(status)
	// Keep the native rows and selection/scroll model intact; only display detail
	// changes. No SetItems reset on an asynchronous documentation delivery.
	s.list.Items()[i].Detail = singleLine(detail)
	if itemID == s.SelectedID() {
		s.prepareDocumentation()
	}
	return true
}

func (s *Screen) prepareDocumentation() {
	s.renderedID = s.SelectedID()
	s.contentRev++
	s.hasDetails = false
	value := "No completion selected."
	if i, ok := s.byID[s.SelectedID()]; ok && i >= 0 {
		e := s.entries[i]
		s.hasDetails = e.Detail != "" || e.Documentation != ""
		var b strings.Builder
		b.WriteString(literal(e.Label))
		if e.Detail != "" {
			b.WriteString("\n" + literal(e.Detail))
		}
		if e.DocumentationState != "" {
			b.WriteString("\n" + singleLine(e.DocumentationState))
		}
		if e.Documentation != "" {
			b.WriteString("\n\n" + literal(e.Documentation))
		} else {
			b.WriteString("\n\nNo documentation available.")
		}
		value = b.String()
	}
	s.text.CancelPointerCapture()
	s.text.ClearSelection()
	s.text.SetValue(value)
	s.text.SetCursorFlat(0)
	s.text.SetScrollOffset(0)
}

func (s *Screen) SetTokens(ts *ui.TokenSet) {
	if ts == nil {
		return
	}
	s.tokens = ts
	s.list.SetTokens(ts)
	s.scrollbar.SetTokens(ts)
	s.text.SetTokens(ts)
	s.text.SetBackgroundColor(ts.Color(ui.ColorPopupBg))
	s.docScroll.SetTokens(ts)
	s.caption.SetTokens(ts)
}

func (s *Screen) SetTheme(th *theme.Theme) {
	if th != nil {
		s.SetTokens(ui.NewTokenSet(th))
	}
}

func (s *Screen) Children() []ui.Component {
	return []ui.Component{s.list, s.scrollbar, s.text, s.docScroll, s.caption}
}

// showsDocumentation: documentation appears only for a chosen row that has
// something to read, so unchosen suggestions cost no screen space.
func (s *Screen) showsDocumentation() bool {
	if s.SelectedID() == "" {
		return false
	}
	e, _ := s.Entry(s.SelectedID())
	return s.hasDetails || e.DocumentationState != ""
}

func (s *Screen) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
	if s.measured && s.measureAt == cs && s.measureRev == s.contentRev {
		return s.measure
	}
	indent := 1
	if s.tokens != nil {
		indent = max(0, s.tokens.FormIndent())
	}
	labelW, detailW := 0, 0
	for _, row := range s.list.Items() {
		labelW = max(labelW, rendering.VisibleWidth(row.Label))
		detailW = max(detailW, rendering.VisibleWidth(row.Detail))
	}
	listW := indent + labelW + 1
	if detailW > 0 {
		listW += 2 + detailW
	}
	listRows := min(max(1, len(s.entries)), maxRows)
	if len(s.entries) > listRows {
		listW++ // scrollbar
	}
	listW = max(16, listW)
	captionH := 0
	if s.caption.Text() != "" {
		captionH = 1
		listW = max(listW, rendering.VisibleWidth(s.caption.Text()))
	}
	size := ui.Size{W: listW, H: listRows + captionH}
	if s.showsDocumentation() {
		if cs.MaxW <= 0 || listW+1+docWidth <= cs.MaxW {
			docW := docWidth
			docRows := s.text.VisualLineCount(max(1, docW-ui.ScrollbarWidth))
			size = ui.Size{W: listW + 1 + docW, H: max(listRows, min(docRows, 10)) + captionH}
		} else {
			w := listW
			if cs.MaxW > 0 {
				w = max(listW, min(cs.MaxW, 60))
			}
			docRows := s.text.VisualLineCount(max(1, w-ui.ScrollbarWidth))
			size = ui.Size{W: w, H: listRows + 1 + min(docRows, stackedDocRows) + captionH}
		}
	}
	s.listW = listW
	s.measure = cs.ClampSize(size)
	s.measureAt, s.measureRev, s.measured = cs, s.contentRev, true
	return s.measure
}

func (s *Screen) Layout(ctx ui.LayoutContext, r ui.Rect) {
	s.SetBounds(r)
	w, h := max(0, r.W), max(0, r.H)
	captionH := 0
	if s.caption.Text() != "" {
		captionH = min(1, h)
	}
	bodyH := h - captionH
	listW := min(w, max(1, s.listW))
	listRect := ui.Rect{Y: captionH, W: w, H: bodyH}
	docRect := ui.Rect{Y: captionH + bodyH}
	if s.showsDocumentation() {
		if w >= listW+1+docWidth {
			listRect.W = listW
			docRect = ui.Rect{X: listW + 1, Y: captionH, W: w - listW - 1, H: bodyH}
		} else if bodyH > 2 {
			listRect.H = min(min(max(1, len(s.entries)), maxRows), bodyH-2)
			docRect = ui.Rect{Y: captionH + listRect.H + 1, W: w, H: bodyH - listRect.H - 1}
		}
	}
	barW := 0
	if listRect.W > 1 && s.scrollbar.NeededForViewport(listRect.H) {
		barW = 1
	}
	listRect.W -= barW
	s.list.Layout(ctx, listRect)
	s.scrollbar.Layout(ctx, ui.Rect{X: listRect.X + listRect.W, Y: listRect.Y, W: barW, H: listRect.H})
	docBarW := 0
	if docRect.W > 1 && docRect.H > 0 && s.text.VisualLineCount(docRect.W) > docRect.H {
		docBarW = ui.ScrollbarWidth
	}
	docRect.W -= docBarW
	s.text.Layout(ctx, docRect)
	s.docScroll.Layout(ctx, ui.Rect{X: docRect.X + docRect.W, Y: docRect.Y, W: docBarW, H: docRect.H})
	s.caption.Layout(ctx, ui.Rect{W: w, H: captionH})
}

func (s *Screen) Paint(buf *surface.CellBuffer, ox, oy int, _ ui.PaintContext) {
	ui.FillRect(buf, ox, oy, s.Bounds(), cell.BlankCell(s.tokens.Color(ui.ColorPopupBg)))
}

func (s *Screen) Handle(ctx ui.EventContext, e ui.Event) ui.EventResult {
	if e.IsKey() && e.Key.Code == tea.KeyEsc && e.Key.Mod == 0 {
		return s.emit(ActionClose, s.SelectedID())
	}
	if e.IsKey() {
		r := s.list.Handle(ctx, e)
		if r.Action != nil && (r.Action.ID == rowSelection || r.Action.ID == rowActivate) {
			r = r.Merge(s.HandleAction(ctx, *r.Action))
		}
		return r
	}
	return ui.EventResult{}
}

// HandleAction is the sole translation from native row intents to parent
// intents. Labels/ordinals are never used to reconstruct an item identity.
func (s *Screen) HandleAction(_ ui.EventContext, a ui.Action) ui.EventResult {
	if a.ID != rowSelection && a.ID != rowActivate {
		return ui.EventResult{}
	}
	row, ok := a.Payload.(ui.ListItem)
	if !ok || row.ID != s.SelectedID() {
		return ui.EventResult{Handled: true}
	}
	i, exists := s.byID[row.ID]
	if !exists || i < 0 || row.Payload != (Action{s.invocation, row.ID}) {
		return ui.EventResult{Handled: true}
	}
	if a.ID == rowSelection {
		s.prepareDocumentation()
		return s.emit(ActionSelection, row.ID).Merge(ui.EventResult{Invalidate: ui.InvalidateLayout})
	} else if s.renderedID != row.ID {
		// Native pointer/semantic activation may select a different row as part
		// of its single activation; it still emits only one parent intent.
		s.prepareDocumentation()
	}
	if a.ID == rowActivate {
		return s.emit(ActionAccept, row.ID)
	}
	return s.emit(ActionSelection, row.ID)
}

func (s *Screen) emit(id, itemID string) ui.EventResult {
	return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint,
		Action: &ui.Action{ID: id, Payload: Action{s.invocation, itemID}}}
}

func (s *Screen) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleGroup, Label: "Completion chooser",
		Description: s.caption.Text(), Value: s.SelectedID()}
}

// chooserList observes native selection after dispatch; it never computes a
// row, pointer index, page size, wheel offset or activation of its own.
type chooserList struct {
	*ui.List
	screen *Screen
}

func (l *chooserList) Handle(ctx ui.EventContext, e ui.Event) ui.EventResult {
	if e.IsKey() {
		if e.Key.Code == tea.KeyEsc || plainCharacter(e.Key) {
			return ui.EventResult{} // parent closes + forwards typing exactly once
		}
		if e.Key.Mod == tea.ModCtrl && (e.Key.Code == tea.KeyPgUp || e.Key.Code == tea.KeyPgDown) {
			e.Key.Mod = 0
			return l.screen.text.TextArea.Handle(ctx, e)
		}
		if e.Key.Mod != 0 {
			return ui.EventResult{}
		}
		if e.Key.Code == tea.KeyTab && l.screen.acceptance == "enter" ||
			e.Key.Code == tea.KeyEnter && l.screen.acceptance == "tab" {
			return ui.EventResult{}
		}
		if e.Key.Code == tea.KeyTab {
			e.Key.Code = tea.KeyEnter
		}
	}
	before := l.screen.SelectedID()
	return l.observe(before, l.List.Handle(ctx, e))
}

func (l *chooserList) observe(before string, r ui.EventResult) ui.EventResult {
	if before != l.screen.SelectedID() && r.Action == nil {
		if row, ok := l.Selected(); ok {
			r.Action = &ui.Action{ID: rowSelection, Payload: row}
		}
	}
	return r
}

func (l *chooserList) ControlActions() []control.Descriptor {
	out := l.List.ControlActions()
	for i := range out {
		if out[i].ID == rowActivate {
			out[i].ID = "activate"
		}
	}
	return out
}

func (l *chooserList) HandleControl(ctx ui.EventContext, op ui.ControlOperation) (ui.EventResult, error) {
	before := l.screen.SelectedID()
	r, err := l.List.HandleControl(ctx, op)
	if err != nil {
		return r, err
	}
	return l.observe(before, r), nil
}

func (l *chooserList) Semantics() ui.Semantics {
	v := l.List.Semantics()
	v.Value = l.screen.SelectedID() // equal labels must not hide selection revisions
	return v
}

// documentation changes only key routing. Native TextArea owns read-only text,
// pointer capture, selection, copy, wrapping and wheel/page scrolling.
type documentation struct {
	*ui.TextArea
	screen *Screen
}

func (d *documentation) Handle(ctx ui.EventContext, e ui.Event) ui.EventResult {
	if e.IsKey() {
		if e.Key.Code == tea.KeyEsc {
			return ui.EventResult{}
		}
		if plainCharacter(e.Key) {
			return ui.EventResult{}
		}
		if e.Key.Mod == tea.ModCtrl && (e.Key.Code == tea.KeyPgUp || e.Key.Code == tea.KeyPgDown) {
			e.Key.Mod = 0
			return d.TextArea.Handle(ctx, e)
		}
		if e.Key.Mod == 0 {
			switch e.Key.Code {
			case tea.KeyUp, tea.KeyDown, tea.KeyPgUp, tea.KeyPgDown, tea.KeyHome, tea.KeyEnd,
				tea.KeyEnter, tea.KeyTab:
				return d.screen.list.Handle(ctx, e)
			}
		}
	}
	return d.TextArea.Handle(ctx, e)
}

func plainCharacter(k tea.KeyPressMsg) bool {
	return k.Mod & ^tea.ModShift == 0 && (k.Text != "" || k.Code > 0 && unicode.IsPrint(k.Code))
}

// Text is painted literally, never parsed as Markdown, ANSI or executable links.
// Preserve line breaks/tabs and Unicode; make terminal control bytes visible.
func literal(value string) string {
	var b strings.Builder
	for _, r := range value {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			q := strconv.QuoteRune(r)
			b.WriteString(q[1 : len(q)-1])
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func singleLine(value string) string {
	return strings.NewReplacer("\n", "\\n", "\t", "\\t").Replace(literal(value))
}

// Only this native row projection paints the standard LSP kind. The glyph is
// display data, never an item identity or a second list interaction path.
func kindGlyph(kind int) string {
	switch kind {
	case 1:
		return "T"
	case 2, 3, 4, 5:
		return "ƒ"
	case 6:
		return "v"
	case 7:
		return "C"
	case 8:
		return "I"
	case 9:
		return "M"
	case 10:
		return "P"
	case 14:
		return "K"
	default:
		return "·"
	}
}
