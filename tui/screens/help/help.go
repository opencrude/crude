// Package help provides the native Help overlay: usage topics, slash
// commands, and keyboard shortcuts in a scrollable WindowFrame body.
package help

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

const (
	ScreenID    ui.ComponentID = "help"
	SearchID    ui.ComponentID = "help/search"
	ListID      ui.ComponentID = "help/list"
	ScrollbarID ui.ComponentID = "help/scrollbar"
	EmptyID     ui.ComponentID = "help/empty"
	HintsID     ui.ComponentID = "help/hints"
)

// CancelMsg is emitted when the overlay is dismissed.
type CancelMsg struct{ Target *Screen }

// Screen is the Host-ready Help body: filter + VirtualList + scrollbar.
type Screen struct {
	ui.Base
	th        *theme.Theme
	tokens    *ui.TokenSet
	cmds      []Command
	shortcuts []Shortcut
	query     string
	bodyW     int
	active    bool
	search    *helpSearch
	list      *helpList
	scrollbar *ui.ScrollbarControl
	empty     *ui.Label
	hints     *ui.Label
	focus     func(ui.ComponentID)
}

func New(th *theme.Theme) *Screen {
	if th == nil {
		th = theme.DefaultDark()
	}
	tokens := ui.NewTokenSet(th)
	s := &Screen{Base: ui.NewBase(ScreenID), th: th, tokens: tokens}

	field := ui.NewFilterField(SearchID, tokens)
	field.SetPrompt(ui.SearchGlyphEnd(tokens))
	field.SetPromptPlacement(ui.PromptEnd)
	field.SetPlaceholder("Search help")
	field.SetSubmitAction("")
	field.OnChanged(s.applyFilter)
	s.search = &helpSearch{Base: ui.NewBase(SearchID), field: field, owner: s}

	list := ui.NewVirtualList(ListID, tokens)
	list.SetActivateAction("help.noop")
	list.SetRowPainter(paintHelpRow)
	s.list = &helpList{Base: ui.NewBase(ListID), list: list, owner: s}
	s.scrollbar = ui.NewScrollbarControl(ScrollbarID, list.Scroll(), tokens)

	s.empty = ui.NewLabel(EmptyID, "No matching help topics.", th)
	s.empty.SetColorRole(ui.ColorMuted)
	s.hints = ui.NewLabel(HintsID, "↑↓ PgUp/PgDn  Tab search  Esc close", th)
	s.hints.SetColorRole(ui.ColorMuted)
	return s
}

// Open loads a live catalog and marks the overlay active.
func (s *Screen) Open(cmds []Command, shortcuts []Shortcut) {
	s.cmds = append(s.cmds[:0], cmds...)
	s.shortcuts = append(s.shortcuts[:0], shortcuts...)
	s.query = ""
	s.bodyW = 0
	s.active = true
	if s.search != nil && s.search.field != nil {
		s.search.field.SetValue("")
	}
	s.rebuild()
}

func (s *Screen) Close() { s.active = false }

func (s *Screen) IsOpen() bool { return s.active }

func (s *Screen) Query() string { return s.query }

func (s *Screen) SetFocusHandler(fn func(ui.ComponentID)) { s.focus = fn }

func (s *Screen) FocusSearch() { s.focusID(SearchID) }

func (s *Screen) FocusList() { s.focusID(ListID) }

func (s *Screen) focusID(id ui.ComponentID) {
	if s.focus != nil {
		s.focus(id)
	}
}

func (s *Screen) Children() []ui.Component {
	if s.emptyVisible() {
		return []ui.Component{s.search, s.empty, s.hints}
	}
	return []ui.Component{s.search, s.list, s.scrollbar, s.hints}
}

func (s *Screen) SetTokens(tokens *ui.TokenSet) {
	s.tokens = tokens
	s.search.SetTokens(tokens)
	s.list.SetTokens(tokens)
	s.scrollbar.SetTokens(tokens)
	s.empty.SetTokens(tokens)
	s.hints.SetTokens(tokens)
	if s.search != nil && s.search.field != nil {
		s.search.field.SetPrompt(ui.SearchGlyphEnd(tokens))
		s.search.field.SetPromptPlacement(ui.PromptEnd)
	}
}

func (s *Screen) SetTheme(th *theme.Theme) {
	if th == nil {
		return
	}
	s.th = th
	s.SetTokens(ui.NewTokenSet(th))
}

func (s *Screen) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
	return cs.ClampSize(ui.Size{W: cs.MaxW, H: cs.MaxH})
}

func (s *Screen) Layout(ctx ui.LayoutContext, r ui.Rect) {
	s.SetBounds(r)
	s.search.Layout(ctx, ui.Rect{W: r.W, H: 1})
	listY := 1
	listH := max(1, r.H-listY-1)
	listW := max(1, r.W-1)
	if listW != s.bodyW {
		s.bodyW = listW
		s.rebuild()
	}
	if s.emptyVisible() {
		s.empty.Layout(ctx, ui.Rect{Y: listY, W: r.W, H: listH})
	} else {
		s.list.Layout(ctx, ui.Rect{X: 0, Y: listY, W: listW, H: listH})
		s.scrollbar.Layout(ctx, ui.Rect{X: listW, Y: listY, W: 1, H: listH})
	}
	s.hints.Layout(ctx, ui.Rect{Y: max(0, r.H-1), W: r.W, H: 1})
}

func (s *Screen) Paint(_ *surface.CellBuffer, _, _ int, _ ui.PaintContext) {}

func (s *Screen) Handle(_ ui.EventContext, evt ui.Event) ui.EventResult {
	if evt.IsKey() && evt.Key.Code == tea.KeyEsc {
		return s.cancel()
	}
	return ui.EventResult{}
}

func (s *Screen) HandleAction(_ ui.EventContext, action ui.Action) ui.EventResult {
	switch action.ID {
	case "help.noop":
		return ui.EventResult{Handled: true}
	case "help.cancel":
		return s.cancel()
	}
	return ui.EventResult{Action: &action}
}

func (s *Screen) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleDialog, Label: "Help"}
}

func (s *Screen) emptyVisible() bool {
	return s.list == nil || s.list.list == nil || len(s.list.list.Items()) == 0
}

func (s *Screen) applyFilter(v string) {
	s.query = v
	s.rebuild()
}

func (s *Screen) rebuild() {
	width := s.bodyW
	if width < 8 {
		width = 56
	}
	prev := ""
	if s.list != nil && s.list.list != nil {
		if item, ok := s.list.list.Selected(); ok {
			prev = item.ID
		}
	}
	items := BuildItems(width, s.query, s.cmds, s.shortcuts)
	s.list.list.SetItems(items)
	if prev != "" {
		for i, item := range items {
			if item.ID == prev {
				s.list.list.SelectIndex(i)
				break
			}
		}
	}
	if s.empty != nil {
		q := strings.TrimSpace(s.query)
		if q == "" {
			s.empty.SetText("No help topics.")
		} else {
			s.empty.SetText("No help matching \"" + q + "\".")
		}
	}
}

func (s *Screen) cancel() ui.EventResult {
	return ui.EventResult{
		Handled: true,
		Cmd:     func() tea.Msg { return CancelMsg{Target: s} },
	}
}

func paintHelpRow(buf *surface.CellBuffer, x, y, width int, item ui.VirtItem, localRow int, state ui.ControlState, _ ui.PaintContext, tokens *ui.TokenSet) {
	if width < 1 {
		return
	}
	data, _ := item.Payload.(rowData)
	line := ""
	if localRow >= 0 && localRow < len(data.Lines) {
		line = data.Lines[localRow]
	}
	fg, bg := cell.NoColor, cell.NoColor
	if tokens != nil {
		fg, bg = tokens.ListRowColors(state)
	}
	attr := cell.Attrs(0)
	switch data.Kind {
	case rowSection:
		attr = cell.AttrBold
		if state == ui.StateDefault || state == ui.StateHovered {
			if tokens != nil {
				fg = tokens.Color(ui.ColorAccent)
				bg = tokens.Color(ui.ColorPopupBg)
			}
		}
	case rowTopic:
		if localRow == 0 {
			attr = cell.AttrBold
		} else if state == ui.StateDefault || state == ui.StateHovered {
			if tokens != nil {
				fg = tokens.Color(ui.ColorMuted)
			}
		}
	case rowCommand, rowShortcut:
		if state == ui.StateDefault || state == ui.StateHovered {
			if tokens != nil {
				fg = tokens.Color(ui.ColorFg)
			}
			paintKeyedLine(buf, x, y, width, item.Label, item.Detail, fg, tokens.Color(ui.ColorMuted), bg, tokens.Color(ui.ColorAccent), state)
			return
		}
	}
	if !bg.IsDefault() {
		ui.FillRect(buf, 0, 0, ui.Rect{X: x, Y: y, W: width, H: 1}, cell.BlankCell(bg))
	}
	if data.Kind == rowTopic && localRow > 0 {
		line = "  " + line
	}
	buf.WriteString(x, y, rendering.TruncateVisual(line, width, "…"), fg, bg, attr)
}

func paintKeyedLine(buf *surface.CellBuffer, x, y, width int, key, detail string, fg, muted, bg, accent cell.Color, state ui.ControlState) {
	if !bg.IsDefault() {
		ui.FillRect(buf, 0, 0, ui.Rect{X: x, Y: y, W: width, H: 1}, cell.BlankCell(bg))
	}
	keyFG := accent
	detailFG := muted
	if state == ui.StateFocused || state == ui.StateSelected {
		keyFG = fg
		detailFG = fg
	}
	key = rendering.TruncateVisual(key, max(1, width), "…")
	buf.WriteString(x, y, key, keyFG, bg, cell.AttrBold)
	used := rendering.VisibleWidth(key)
	if detail == "" || used >= width-2 {
		return
	}
	pad := 2
	if used < 16 {
		pad = 16 - used
	}
	if used+pad >= width {
		return
	}
	rest := rendering.TruncateVisual(detail, width-used-pad, "…")
	buf.WriteString(x+used+pad, y, rest, detailFG, bg, 0)
}

type helpSearch struct {
	ui.Base
	field *ui.FilterField
	owner *Screen
}

func (f *helpSearch) Measure(ctx ui.LayoutContext, cs ui.Constraints) ui.Size {
	return f.field.Measure(ctx, cs)
}
func (f *helpSearch) Layout(ctx ui.LayoutContext, r ui.Rect) {
	f.SetBounds(r)
	f.field.Layout(ctx, ui.Rect{W: r.W, H: r.H})
}
func (f *helpSearch) Paint(buf *surface.CellBuffer, ox, oy int, ctx ui.PaintContext) {
	f.field.Paint(buf, ox+f.Bounds().X, oy+f.Bounds().Y, ctx)
}
func (f *helpSearch) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	if evt.IsKey() {
		switch evt.Key.Code {
		case tea.KeyEsc:
			result := f.field.Handle(ctx, evt)
			if result.Handled {
				return result
			}
			return f.owner.cancel()
		case tea.KeyTab, tea.KeyDown:
			f.owner.FocusList()
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
		}
	}
	return f.field.Handle(ctx, evt)
}
func (f *helpSearch) Semantics() ui.Semantics       { return f.field.Semantics() }
func (f *helpSearch) SetTokens(tokens *ui.TokenSet) { f.field.SetTokens(tokens) }

type helpList struct {
	ui.Base
	list  *ui.VirtualList
	owner *Screen
}

func (l *helpList) Measure(ctx ui.LayoutContext, cs ui.Constraints) ui.Size {
	return l.list.Measure(ctx, cs)
}
func (l *helpList) Layout(ctx ui.LayoutContext, r ui.Rect) {
	l.SetBounds(r)
	l.list.Layout(ctx, ui.Rect{W: r.W, H: r.H})
}
func (l *helpList) Paint(buf *surface.CellBuffer, ox, oy int, ctx ui.PaintContext) {
	l.list.Paint(buf, ox+l.Bounds().X, oy+l.Bounds().Y, ctx)
}
func (l *helpList) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	if evt.IsKey() {
		key := evt.Key
		switch {
		case key.Code == tea.KeyEsc:
			return l.owner.cancel()
		case key.Code == tea.KeyTab:
			l.owner.FocusSearch()
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
		case key.String() == "k":
			return l.list.Handle(ctx, ui.KeyEvent(tea.KeyPressMsg{Code: tea.KeyUp}))
		case key.String() == "j":
			return l.list.Handle(ctx, ui.KeyEvent(tea.KeyPressMsg{Code: tea.KeyDown}))
		default:
			if key.Text != "" && !key.Mod.Contains(tea.ModCtrl) && !key.Mod.Contains(tea.ModAlt) {
				l.owner.FocusSearch()
				return l.owner.search.Handle(ui.EventContext{Focused: true}, evt)
			}
		}
	}
	result := l.list.Handle(ctx, evt)
	if result.Action != nil && result.Action.ID == "help.noop" {
		result.Action = nil
	}
	return result
}
func (l *helpList) Semantics() ui.Semantics { return l.list.Semantics() }
func (l *helpList) SetTokens(tokens *ui.TokenSet) {
	l.list.SetTokens(tokens)
}

var _ ui.Component = (*Screen)(nil)
var _ ui.Parent = (*Screen)(nil)
var _ ui.ActionHandler = (*Screen)(nil)
