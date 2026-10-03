package ui

import (
	"strings"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
)

// ProgressRule is a one-row separator with optional indeterminate ink motion.
// It never changes geometry or implies measurable completion. A settled rule
// has no animation; its host owns the frame clock and scheduling.
type ProgressRule struct {
	Base
	tokens *TokenSet
	active bool
	frame  int
	motion TextTideMotion
}

func NewProgressRule(id ComponentID, tokens *TokenSet) *ProgressRule {
	return &ProgressRule{Base: NewBase(id), tokens: tokens}
}
func (r *ProgressRule) SetTokens(tokens *TokenSet) { r.tokens = tokens }
func (r *ProgressRule) SetFrame(frame int)         { r.frame = frame }
func (r *ProgressRule) SetActive(active bool) {
	if r.active != active {
		r.motion = TextTideMotion{}
	}
	r.active = active
}
func (r *ProgressRule) NeedsFrame() bool { return r.active }
func (r *ProgressRule) Paint(buf *surface.CellBuffer, ox, oy int, _ PaintContext) {
	b := r.Bounds()
	if b.Empty() || r.tokens == nil {
		return
	}
	glyph := string(FirstRune(r.tokens.Glyphs().BorderH, '─'))
	width := rendering.VisibleWidth(glyph)
	if width < 1 {
		return
	}
	ink := r.tokens.Color(ColorBorder)
	buf.WriteString(ox+b.X, oy+b.Y, strings.Repeat(glyph, b.W/width), ink, cell.NoColor, 0)
	if !r.active {
		return
	}
	center := r.motion.Advance(r.frame, b.W, true)
	for column := 0; column+width <= b.W; column += width {
		fg, _ := TextTide(ink, 0, r.tokens, center, column)
		buf.WriteString(ox+b.X+column, oy+b.Y, glyph, fg, cell.NoColor, 0)
	}
}
