package activitybar

import (
	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

// Width is the always-on left activity rail in cells (one glyph + padding).
const Width = 3

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
		return "Git"
	default:
		return "Files"
	}
}

func (v View) Glyph() string {
	switch v {
	case ViewAgents:
		return "A"
	case ViewTerminals:
		return "T"
	case ViewGit:
		return "G"
	default:
		return "F"
	}
}

var views = []View{ViewFiles, ViewAgents, ViewTerminals, ViewGit}

// Screen is the VS Code-style left activity rail.
type Screen struct {
	ui.Base
	th     *theme.Theme
	tokens *ui.TokenSet
	active View
	hover  int
}

func New(th *theme.Theme) *Screen {
	if th == nil {
		th = theme.DefaultDark()
	}
	return &Screen{
		Base:   ui.NewBase(ScreenID),
		th:     th,
		tokens: ui.NewTokenSet(th),
		hover:  -1,
	}
}

func (s *Screen) Active() View { return s.active }

func (s *Screen) SetActive(v View) { s.active = v }

func (s *Screen) SetTokens(tokens *ui.TokenSet) { s.tokens = tokens }

func (s *Screen) SetTheme(th *theme.Theme) {
	if th == nil {
		return
	}
	s.th = th
	s.SetTokens(ui.NewTokenSet(th))
}

func (s *Screen) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
	return cs.ClampSize(ui.Size{W: Width, H: cs.MaxH})
}

func (s *Screen) Layout(_ ui.LayoutContext, r ui.Rect) { s.SetBounds(r) }

func (s *Screen) Paint(buf *surface.CellBuffer, ox, oy int, ctx ui.PaintContext) {
	r := s.Bounds()
	bg := cell.NoColor
	fg := cell.NoColor
	accent := cell.NoColor
	muted := cell.NoColor
	if s.tokens != nil {
		bg = s.tokens.Color(ui.ColorBg)
		fg = s.tokens.Color(ui.ColorFg)
		accent = s.tokens.Color(ui.ColorAccent)
		muted = s.tokens.Color(ui.ColorMuted)
	}
	ui.FillRect(buf, ox, oy, r, cell.BlankCell(bg))
	for i, v := range views {
		y := oy + r.Y + 1 + i*2
		if y < oy+r.Y || y >= oy+r.Y+r.H {
			continue
		}
		glyph := v.Glyph()
		gfg, gbg := muted, bg
		if v == s.active {
			gfg, gbg = fg, accent
		} else if i == s.hover && ctx.Hovered && s.tokens != nil {
			// Hover paints only while the host still targets the rail, so a
			// highlight cannot stick after the pointer moved to another pane.
			gfg, gbg = fg, s.tokens.Color(ui.ColorHover)
		}
		ui.FillRect(buf, 0, 0, ui.Rect{X: ox + r.X, Y: y, W: r.W, H: 1}, cell.BlankCell(gbg))
		gx := ox + r.X + (r.W-1)/2
		buf.WriteString(gx, y, glyph, gfg, gbg, 0)
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

func hitIndex(localY int) int {
	// row 1,3,5,7 → indices 0..3 (1 + i*2)
	if localY < 1 {
		return -1
	}
	if (localY-1)%2 != 0 {
		return -1
	}
	idx := (localY - 1) / 2
	if idx < 0 || idx >= len(views) {
		return -1
	}
	return idx
}

func (s *Screen) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleGroup, Label: "Activity bar", Value: s.active.Label()}
}
