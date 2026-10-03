package ui

import (
	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
)

// ActionLink is a compact, text-led command. It uses Button's activation and
// disabled semantics without a bracket, border, or permanent filled surface.
type ActionLink struct {
	Button
}

func NewActionLink(id ComponentID, label, action string) *ActionLink {
	return &ActionLink{Button: *NewButton(id, label, action)}
}

func (l *ActionLink) Measure(_ LayoutContext, cs Constraints) Size {
	return cs.ClampSize(Size{W: rendering.VisibleWidth(l.label) + 2, H: 1})
}

func (l *ActionLink) Paint(buf *surface.CellBuffer, ox, oy int, ctx PaintContext) {
	r := l.Bounds()
	if r.Empty() {
		return
	}
	tokens := l.tokenSet()
	fg, bg, attr := l.idleForeground(tokens), tokens.Color(ColorBg), cell.Attrs(0)
	switch ResolveControlState(l.disabled, ctx.Focused, false, false, ctx.Hovered) {
	case StateDisabled:
		fg = tokens.Color(ColorMuted)
	case StateFocused:
		bg, attr = tokens.Color(ColorSelection), cell.AttrBold|cell.AttrUnderline
	case StateHovered:
		bg = tokens.Color(ColorHover)
	}
	FillRect(buf, ox, oy, r, cell.BlankCell(bg))
	pad := min(1, r.W/3)
	buf.WriteString(ox+r.X+pad, oy+r.Y, rendering.TruncateVisual(l.label, max(0, r.W-2*pad), "…"), fg, bg, attr)
}
