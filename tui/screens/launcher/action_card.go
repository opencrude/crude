package launcher

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/ui"
)

// launcherAction preserves the original four-row outlined launcher buttons.
type launcherAction struct {
	ui.Base
	owner         *Screen
	kind          ItemKind
	title, detail string
	tokens        *ui.TokenSet
}

func newLauncherAction(s *Screen, id ui.ComponentID, kind ItemKind, title, detail string) *launcherAction {
	return &launcherAction{Base: ui.NewBase(id), owner: s, kind: kind, title: title, detail: detail}
}

func (a *launcherAction) SetTokens(tokens *ui.TokenSet) { a.tokens = tokens }

func (a *launcherAction) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
	return cs.ClampSize(ui.Size{W: cs.MaxW, H: 4})
}

func (a *launcherAction) Layout(_ ui.LayoutContext, r ui.Rect) { a.SetBounds(r) }

func (a *launcherAction) Paint(buf *surface.CellBuffer, ox, oy int, ctx ui.PaintContext) {
	r := a.Bounds()
	if r.W < 8 || r.H < 4 || a.tokens == nil {
		return
	}
	selected := a.owner.selected == int(a.kind)
	border := a.tokens.Color(ui.ColorBorder)
	title := a.tokens.Color(ui.ColorFg)
	if selected {
		border = a.tokens.Color(ui.ColorAccent)
		title = a.tokens.Color(ui.ColorAccent)
	} else if a.kind == ItemNewSession || ctx.Hovered {
		border = a.tokens.Color(ui.ColorMuted)
	}
	g := a.tokens.Glyphs()
	h := string(ui.FirstRune(g.BorderH, '─'))
	v := string(ui.FirstRune(g.BorderV, '│'))
	tl := string(ui.FirstRune(g.CornerTL, '╭'))
	tr := string(ui.FirstRune(g.CornerTR, '╮'))
	bl := string(ui.FirstRune(g.CornerBL, '╰'))
	br := string(ui.FirstRune(g.CornerBR, '╯'))
	x, y := ox+r.X, oy+r.Y
	buf.WriteString(x, y, tl+strings.Repeat(h, r.W-2)+tr, border, cell.NoColor, 0)
	for row := 1; row < 3; row++ {
		buf.WriteString(x, y+row, v, border, cell.NoColor, 0)
		buf.WriteString(x+1, y+row, strings.Repeat(" ", r.W-2), title, cell.NoColor, 0)
		buf.WriteString(x+r.W-1, y+row, v, border, cell.NoColor, 0)
	}
	buf.WriteString(x, y+3, bl+strings.Repeat(h, r.W-2)+br, border, cell.NoColor, 0)
	innerW := r.W - 4
	label, detail := a.title, a.detail
	if a.kind == ItemNewSession {
		label = sparkleFrame(a.owner.frame) + label
		detail = a.owner.cwd
	}
	buf.WriteString(x+2, y+1, rendering.TruncateLine(label, innerW), title, cell.NoColor, cell.AttrBold)
	hint := rendering.TruncateLine("enter ↵", innerW)
	hintW := rendering.VisibleWidth(hint)
	detail = rendering.TruncateLine(detail, max(0, innerW-hintW-1))
	spacerW := max(0, innerW-rendering.VisibleWidth(detail)-hintW)
	buf.WriteString(x+2, y+2, detail+strings.Repeat(" ", spacerW)+hint, a.tokens.Color(ui.ColorMuted), cell.NoColor, 0)
}

func sparkleFrame(frame int) string {
	frames := [...]string{"✨", "🌟", "✨", "💫", "✨", "⭐"}
	return frames[(frame/10000)%len(frames)]
}

func (a *launcherAction) Handle(_ ui.EventContext, event ui.Event) ui.EventResult {
	if event.IsKey() {
		if event.Key.Code != tea.KeyEnter && event.Key.Code != tea.KeySpace {
			return a.owner.handleKey(event.Key)
		}
	}
	if event.IsKey() || event.Kind == ui.EventMouseClick && event.MouseClick.Button == tea.MouseLeft {
		a.owner.selected = int(a.kind)
		a.owner.focused = a.ID()
		return ui.EventResult{Handled: true, Cmd: func() tea.Msg { return ActivateMsg{Screen: a.owner, Kind: a.kind} }, Invalidate: ui.InvalidatePaint | ui.InvalidateFocus}
	}
	return ui.EventResult{}
}

func (a *launcherAction) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleButton, Label: a.title, Selected: a.owner.selected == int(a.kind)}
}
