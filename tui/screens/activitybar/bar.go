package activitybar

import (
	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/screens/panelchrome"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

// Width is the always-on left activity rail in cells:
//
//	col 0  active marker
//	col 1  gap
//	col 2  view icon
//	col 3  badge digit
//	col 4  vertical rule separating the rail from the panel
const Width = 5

const (
	markerCol = 0
	iconCol   = 2
	badgeCol  = 3
	ruleCol   = 4
)

// RowStride is the height of one rail entry. Entries start at the rail's
// first row so the rail reads as a column that begins at the top, and the
// whole band is clickable (a one-row target is too small for a mouse).
const RowStride = 2

const ScreenID ui.ComponentID = "activitybar"

const ActionSelect = "activity.select"

// View is one activity-bar destination.
type View int

const (
	ViewFiles View = iota
	ViewAgents
	ViewTerminals
	ViewGit
)

func (v View) Label() string {
	switch v {
	case ViewAgents:
		return "Agents"
	case ViewTerminals:
		return "Terminals"
	case ViewGit:
		return "Source Control"
	default:
		return "Explorer"
	}
}

// Icon returns the rail glyph for the view. Nerd-font codepoints come from
// the same coverage the file tree already relies on; the fallbacks are BMP
// symbols with neutral east-asian width so column math is unaffected.
func (v View) Icon(nerdFont bool) string {
	if nerdFont {
		switch v {
		case ViewAgents:
			return "\uf085" // nf-fa-cogs
		case ViewTerminals:
			return "\uf120" // nf-fa-terminal
		case ViewGit:
			return "\uf418" // nf-oct-git_branch
		default:
			return "\uf07b" // nf-fa-folder
		}
	}
	switch v {
	case ViewAgents:
		return "◈"
	case ViewTerminals:
		return "▸"
	case ViewGit:
		return "⑂"
	default:
		return "▤"
	}
}

// Glyph is the non-nerd-font rail symbol. Kept for callers that only need a
// stable single-cell identity for a view.
func (v View) Glyph() string { return v.Icon(false) }

var views = []View{ViewFiles, ViewAgents, ViewTerminals, ViewGit}

// Views returns the rail order.
func Views() []View { return append([]View(nil), views...) }

// Screen is the VS Code-style left activity rail.
type Screen struct {
	ui.Base
	th       *theme.Theme
	tokens   *ui.TokenSet
	active   View
	hover    int
	nerdFont bool
	badges   map[View]int
}

func New(th *theme.Theme) *Screen {
	if th == nil {
		th = theme.DefaultDark()
	}
	s := &Screen{
		Base:   ui.NewBase(ScreenID),
		th:     th,
		tokens: ui.NewTokenSet(th),
		hover:  -1,
		badges: map[View]int{},
	}
	s.applyTheme(th)
	return s
}

func (s *Screen) Active() View { return s.active }

func (s *Screen) SetActive(v View) { s.active = v }

// SetBadge sets the pending-item count shown beside a view's icon. Counts
// above 9 collapse to "+" so the rail never widens.
func (s *Screen) SetBadge(v View, n int) {
	if s.badges == nil {
		s.badges = map[View]int{}
	}
	s.badges[v] = n
}

func (s *Screen) SetTokens(tokens *ui.TokenSet) { s.tokens = tokens }

func (s *Screen) SetTheme(th *theme.Theme) {
	if th == nil {
		return
	}
	s.th = th
	s.SetTokens(ui.NewTokenSet(th))
	s.applyTheme(th)
}

func (s *Screen) applyTheme(th *theme.Theme) {
	s.nerdFont = panelchrome.NerdFont(th)
}

func (s *Screen) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
	return cs.ClampSize(ui.Size{W: Width, H: cs.MaxH})
}

func (s *Screen) Layout(_ ui.LayoutContext, r ui.Rect) { s.SetBounds(r) }

func (s *Screen) Paint(buf *surface.CellBuffer, ox, oy int, ctx ui.PaintContext) {
	r := s.Bounds()
	if buf == nil || s.tokens == nil || r.Empty() {
		return
	}
	bg := s.tokens.Color(ui.ColorBg)
	fg := s.tokens.Color(ui.ColorFg)
	accent := s.tokens.Color(ui.ColorAccent)
	muted := s.tokens.Color(ui.ColorMuted)
	ui.FillRect(buf, ox, oy, r, cell.BlankCell(bg))

	left := ox + r.X
	top := oy + r.Y
	for i, v := range views {
		y := top + i*RowStride
		if y < top || y >= top+r.H {
			continue
		}
		active := v == s.active
		hovered := i == s.hover && ctx.Hovered && !active

		rowBG := bg
		iconFG := muted
		switch {
		case active:
			iconFG = fg
		case hovered:
			rowBG = s.tokens.Color(ui.ColorHover)
			iconFG = fg
		}
		if rowBG != bg {
			// The hover band stops before the rule so the separator stays
			// a continuous line down the whole rail.
			bandH := RowStride
			if y+bandH > top+r.H {
				bandH = top + r.H - y
			}
			ui.FillRect(buf, 0, 0, ui.Rect{X: left, Y: y, W: ruleCol, H: bandH}, cell.BlankCell(rowBG))
		}
		if active && r.W > markerCol {
			buf.Set(left+markerCol, y, cell.NewCell('▌', accent, rowBG, 0))
		}
		if r.W > iconCol {
			buf.WriteString(left+iconCol, y, v.Icon(s.nerdFont), iconFG, rowBG, 0)
		}
		if r.W > badgeCol {
			if badge := badgeText(s.badges[v]); badge != "" {
				buf.WriteString(left+badgeCol, y, badge, accent, rowBG, cell.AttrBold)
			}
		}
	}

	if r.W > ruleCol {
		panelchrome.PaintVRule(buf, left+ruleCol, top, r.H, s.tokens)
	}
}

// badgeText renders a pending count into the single badge column.
func badgeText(n int) string {
	switch {
	case n <= 0:
		return ""
	case n > 9:
		return "+"
	default:
		return string(rune('0' + n))
	}
}

func (s *Screen) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	switch evt.Kind {
	case ui.EventMouseMotion:
		idx := hitIndex(ctx.LocalY)
		if idx != s.hover {
			s.hover = idx
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
		}
		return ui.EventResult{Handled: true}
	case ui.EventMouseClick:
		if evt.MouseClick.Button != tea.MouseLeft {
			return ui.EventResult{}
		}
		idx := hitIndex(ctx.LocalY)
		if idx < 0 || idx >= len(views) {
			return ui.EventResult{Handled: true}
		}
		return ui.EventResult{
			Handled:    true,
			Action:     &ui.Action{ID: ActionSelect, Payload: views[idx]},
			Invalidate: ui.InvalidatePaint | ui.InvalidateFocus,
		}
	}
	return ui.EventResult{}
}

// hitIndex maps a rail-local row to a view index. Entries own their whole
// RowStride band, so the gap row below an icon activates the same view.
func hitIndex(localY int) int {
	if localY < 0 {
		return -1
	}
	idx := localY / RowStride
	if idx < 0 || idx >= len(views) {
		return -1
	}
	return idx
}

// ViewAt reports the view under a rail-local row, for hit-test consumers
// outside the component (context menus).
func ViewAt(localY int) (View, bool) {
	idx := hitIndex(localY)
	if idx < 0 {
		return ViewFiles, false
	}
	return views[idx], true
}

func (s *Screen) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleGroup, Label: "Activity bar", Value: s.active.Label()}
}
