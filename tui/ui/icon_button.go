package ui

import (
	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
)

// IconButton uses native Button activation, focus and hover for compact chrome.
// The accessible label is also the shell-owned tooltip.
type IconButton struct {
	*Button
	glyph, name string
	tokens      *TokenSet
	tabAction   bool
}

func NewIconButton(id ComponentID, glyph, name, action string, tokens *TokenSet) *IconButton {
	b := &IconButton{Button: NewButton(id, name, action), glyph: glyph, name: name}
	b.SetTokens(tokens)
	return b
}

// NewTabButton is the content-adjacent plus used by every tab-strip host.
func NewTabButton(id ComponentID, name, action string, tokens *TokenSet) *IconButton {
	b := NewIconButton(id, "+", name, action, tokens)
	b.tabAction = true
	return b
}

func (b *IconButton) SetTokens(tokens *TokenSet) {
	b.tokens = tokens
	b.Button.SetTokens(tokens)
}

func (b *IconButton) Measure(_ LayoutContext, cs Constraints) Size {
	width := rendering.VisibleWidth(b.glyph) + 2
	if b.tabAction {
		width = 5
	}
	return cs.ClampSize(Size{W: width, H: 1})
}

func (b *IconButton) Paint(buf *surface.CellBuffer, ox, oy int, ctx PaintContext) {
	r := b.Bounds()
	if r.Empty() || b.tokens == nil {
		return
	}
	fg, bg, attr := b.tokens.Color(ColorMuted), b.tokens.Color(ColorBg), cell.Attrs(0)
	if b.tabAction {
		fg, attr = b.tokens.Color(ColorAccent), cell.AttrBold
	}
	if ctx.Focused {
		bg = b.tokens.Color(ColorAccent)
		fg = cell.ContrastInk(bg)
	} else if ctx.Hovered && !b.tabAction {
		fg, bg = b.tokens.Color(ColorFg), b.tokens.Color(ColorHover)
	}
	FillRect(buf, ox, oy, r, cell.BlankCell(bg))
	width := rendering.VisibleWidth(b.glyph)
	if r.W >= width {
		buf.WriteString(ox+r.X+(r.W-width)/2, oy+r.Y, b.glyph, fg, bg, attr)
	}
}

func (b *IconButton) Tooltip() (string, Rect, bool) {
	return b.name, Rect{W: b.Bounds().W, H: b.Bounds().H}, !b.Bounds().Empty()
}
