package ui

import (
	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
)

// DragGhost is the lifted copy of a dragged item that follows the pointer,
// like the drag image of a desktop file manager. Its first row names the item;
// its second row states what a release does at the pointer. Every drag source
// (file tree rows, tabs) describes itself with one DragGhost so all drags look
// and read the same.
type DragGhost struct {
	Icon      string
	IconColor cell.Color
	Label     string
	// Intent states what releasing does here ("Move to src", "Split right").
	Intent string
	// Blocked marks a pointer position where releasing does nothing; Intent
	// then tells the user where to drop instead.
	Blocked bool
	// X and Y are the pointer cell in screen coordinates.
	X, Y int
}

// Active reports whether there is a ghost to paint.
func (g DragGhost) Active() bool { return g.Label != "" }

const (
	dragGhostMaxWidth = 36
	dragGhostShadow   = 0.5
)

// Rect is the ghost card in screen cells, excluding its shadow. The card sits
// below and right of the pointer so it never hides the drop target, and flips
// to the other side near the screen edges.
func (g DragGhost) Rect(screenW, screenH int) Rect {
	label := g.Label
	if g.Icon != "" {
		label = g.Icon + " " + label
	}
	w := min(dragGhostMaxWidth, max(rendering.VisibleWidth(label), rendering.VisibleWidth(g.Intent))+4)
	w = min(w, max(1, screenW-1))
	h := 2
	if g.Intent == "" {
		h = 1
	}
	x, y := g.X+2, g.Y+1
	if x+w+1 > screenW {
		x = g.X - w - 1
	}
	if y+h+1 > screenH {
		y = g.Y - h - 1
	}
	return Rect{X: max(0, x), Y: max(0, y), W: w, H: h}
}

// PaintDragGhost draws the ghost over a completed frame. The card uses popup
// colors with an accent edge; a soft shadow darkens the cells under its right
// and bottom edges without hiding them.
func PaintDragGhost(buf *surface.CellBuffer, g DragGhost, tokens *TokenSet) {
	if buf == nil || !g.Active() {
		return
	}
	r := g.Rect(buf.Width(), buf.Height())
	if r.Empty() {
		return
	}
	shadow := tokens.Color(ColorShadow)
	darken := func(x, y int) {
		if x < 0 || y < 0 || x >= buf.Width() || y >= buf.Height() {
			return
		}
		c := buf.Get(x, y)
		if c.Width == 0 {
			return
		}
		c.BG = c.BG.Blend(shadow, dragGhostShadow)
		c.FG = c.FG.Blend(shadow, dragGhostShadow)
		buf.Set(x, y, c)
	}
	for y := r.Y + 1; y <= r.Y+r.H; y++ {
		darken(r.X+r.W, y)
	}
	for x := r.X + 1; x < r.X+r.W; x++ {
		darken(x, r.Y+r.H)
	}

	bg, fg := tokens.Color(ColorPopupBg), tokens.Color(ColorFg)
	edge := tokens.Color(ColorAccent)
	if g.Blocked {
		edge = tokens.Color(ColorMuted)
	}
	for y := r.Y; y < r.Y+r.H; y++ {
		buf.FillRect(surface.Rect{X: r.X, Y: y, W: r.W, H: 1}, cell.BlankCell(bg))
		buf.Set(r.X, y, cell.NewCell('▌', edge, bg, 0))
	}
	x := r.X + 2
	room := r.W - 3
	if g.Icon != "" && room > 2 {
		iconColor := g.IconColor
		if iconColor.IsDefault() {
			iconColor = fg
		}
		x = buf.WriteString(x, r.Y, g.Icon, iconColor, bg, 0) + 1
		room = r.X + r.W - 1 - x
	}
	buf.WriteString(x, r.Y, rendering.TruncateVisual(g.Label, max(1, room), "…"), fg, bg, cell.AttrBold)
	if g.Intent != "" {
		intent := tokens.Color(ColorAccent)
		if g.Blocked {
			intent = tokens.Color(ColorMuted)
		}
		buf.WriteString(r.X+2, r.Y+1, rendering.TruncateVisual(g.Intent, max(1, r.W-3), "…"), intent, bg, 0)
	}
}
