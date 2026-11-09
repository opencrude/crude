// Package completion is an inert completion chooser body. Its parent owns the
// invocation, requests, source/context validation, placement and actual edits.
package completion

import (
	"strconv"
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
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
	hint       *ui.Label
	renderedID string // documentation projection key, never selection authority
	acceptance string
	measureAt  ui.Constraints
	measureRev uint64
	contentRev uint64
	measure    ui.Size
	measured   bool
	hasDetails bool
}

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
	rows := make([]ui.ListItem, len(s.entries))
	for i, e := range s.entries {
		rows[i] = ui.ListItem{ID: e.ID, Label: kindGlyph(e.Kind) + " " + singleLine(e.Label), Detail: singleLine(e.Detail),
			Disabled: s.byID[e.ID] < 0, Payload: Action{s.invocation, e.ID}}
	}
	s.list.SetItems(rows)
	s.scrollbar = ui.NewScrollbarControl("completion/scrollbar", s.list.Scroll(), ts)
	s.text = &documentation{TextArea: ui.NewTextArea(TextID, ts, true), screen: s}
	s.text.SetReadOnly(true)
	s.docScroll = ui.NewScrollbarControl("completion/documentation-scrollbar", s.text.Scroll(), ts)
	s.docScroll.SetPointerTargetSize(1)
	s.docScroll.SetSurfaceBackground(ui.ColorPopupBg)
	s.caption = ui.NewLabel("completion/status", singleLine(status), th)
	s.caption.SetColorRole(ui.ColorMuted)
	s.hint = ui.NewLabel("completion/hint", "Ctrl+PgUp/PgDn: docs · Enter/Tab: accept · Esc: close", th)
	s.hint.SetColorRole(ui.ColorMuted)
	s.SetAcceptance("tab-enter")
	s.SetTokens(ts)
	s.prepareDocumentation()
	return s
}

func (s *Screen) DefaultFocus() ui.ComponentID { return ListID }

// SetAcceptance updates the native chooser and its displayed key hint together.
// The containing editor routes a non-accepting key to normal source editing.
func (s *Screen) SetAcceptance(policy string) bool {
	if s.acceptance == policy {
		return false
	}
	s.acceptance = policy
	keys := "Enter/Tab"
	switch policy {
	case "tab":
		keys = "Tab"
	case "enter":
		keys = "Enter"
	}
	s.hint.SetText("Ctrl+PgUp/PgDn: docs · " + keys + ": accept · Esc: close")
	return true
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
	s.hint.SetTokens(ts)
}

func (s *Screen) SetTheme(th *theme.Theme) {
	if th != nil {
		s.SetTokens(ui.NewTokenSet(th))
	}
}

func (s *Screen) Children() []ui.Component {
	return []ui.Component{s.list, s.scrollbar, s.text, s.docScroll, s.caption, s.hint}
}

func (s *Screen) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
	if s.measured && s.measureAt == cs && s.measureRev == s.contentRev {
		return s.measure
	}
	preferredW := 70
	if s.hasDetails {
		preferredW = 106
	}
	w := cs.ClampSize(ui.Size{W: preferredW}).W
	listRows := max(1, len(s.entries))
	docW := w
	if w >= 80 {
		docW = w - w*2/5 - 1
	}
	docRows := s.text.VisualLineCount(max(1, docW))
	bodyH := max(min(listRows, 8), min(docRows, 8))
	if w < 80 {
		bodyH = min(listRows, 5) + 1 + min(docRows, 6)
	}
	h := bodyH + 1 // keyboard hint
	if s.caption.Text() != "" {
		h++
	}
	s.measure = cs.ClampSize(ui.Size{W: w, H: h})
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
	hintH := min(1, h-captionH)
	bodyH := h - captionH - hintH
	listRect := ui.Rect{Y: captionH, W: w, H: bodyH}
	docRect := ui.Rect{Y: captionH + bodyH, W: w}
	if w >= 80 {
		listRect.W = w * 2 / 5
		docRect = ui.Rect{X: listRect.W + 1, Y: captionH, W: w - listRect.W - 1, H: bodyH}
	} else if bodyH > 1 {
		gap := min(1, bodyH-2)
		listRect.H = min(max(1, len(s.entries)), (bodyH-gap)/2)
		docRect = ui.Rect{Y: captionH + listRect.H + gap, W: w, H: bodyH - listRect.H - gap}
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
	s.hint.Layout(ctx, ui.Rect{Y: h - hintH, W: w, H: hintH})
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
