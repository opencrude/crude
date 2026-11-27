package ui

import (
	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
)

// ProgressBar is a one-row bar for work with a known size, such as a data
// update or a download. It fills in half-cell steps in the accent color over a
// quiet track. Without a total it shows a moving segment instead: the work has
// started but its size is not known yet. ProgressRule stays the loading
// indicator of lists and pages.
type ProgressBar struct {
	Base
	tokens      *TokenSet
	done, total int64
	frame       int
	bg          SemanticColor
	bgSet       bool
}

// NewProgressBar returns an empty bar.
func NewProgressBar(id ComponentID, tokens *TokenSet) *ProgressBar {
	return &ProgressBar{Base: NewBase(id), tokens: tokens}
}

// SetTokens replaces the theme tokens.
func (p *ProgressBar) SetTokens(tokens *TokenSet) { p.tokens = tokens }

// SetSurfaceBackground paints the bar on an explicit surface fill.
func (p *ProgressBar) SetSurfaceBackground(role SemanticColor) { p.bg, p.bgSet = role, true }

// SetMeasurement sets the finished and total work. A total of zero or less
// shows the moving segment.
func (p *ProgressBar) SetMeasurement(done, total int64) { p.done, p.total = done, total }

// SetFrame advances the moving segment; the host owns the frame clock.
func (p *ProgressBar) SetFrame(frame int) { p.frame = frame }

// NeedsFrame reports whether the bar animates.
func (p *ProgressBar) NeedsFrame() bool { return p.total <= 0 }

// Fraction is the finished part of the work, from 0 to 1.
func (p *ProgressBar) Fraction() float64 {
	if p.total <= 0 {
		return 0
	}
	switch fraction := float64(p.done) / float64(p.total); {
	case fraction < 0:
		return 0
	case fraction > 1:
		return 1
	default:
		return fraction
	}
}

// Paint draws the bar on its first row.
func (p *ProgressBar) Paint(buf *surface.CellBuffer, ox, oy int, ctx PaintContext) {
	b := p.Bounds()
	if b.Empty() || p.tokens == nil {
		return
	}
	glyphs := p.tokens.Glyphs()
	full, half := FirstRune(glyphs.ProgressBar, '━'), FirstRune(glyphs.ProgressHalf, '╸')
	if rendering.VisibleWidth(string(full)) != 1 || rendering.VisibleWidth(string(half)) != 1 {
		return
	}
	fill, track := p.tokens.Color(ColorAccent), p.tokens.Color(ColorBorder)
	bg := cell.NoColor
	if p.bgSet {
		bg = p.tokens.Color(p.bg)
	}
	put := func(column int, glyph rune, fg cell.Color) {
		c := cell.NewCell(glyph, fg, bg, 0)
		c.Width = 1
		if !p.bgSet {
			c.BG = buf.Get(ox+b.X+column, oy+b.Y).BG
		}
		buf.Set(ox+b.X+column, oy+b.Y, c)
	}
	if p.total <= 0 {
		frame := p.frame
		if frame == 0 {
			frame = ctx.Frame
		}
		// A segment of a fifth of the bar crosses it about once a second.
		segment := max(1, b.W/5)
		start := (frame/16)%(b.W+segment) - segment
		for column := 0; column < b.W; column++ {
			fg := track
			if column >= start && column < start+segment {
				fg = fill
			}
			put(column, full, fg)
		}
		return
	}
	halves := int(p.Fraction() * float64(2*b.W))
	for column := 0; column < b.W; column++ {
		switch {
		case 2*column+2 <= halves:
			put(column, full, fill)
		case 2*column+1 == halves:
			put(column, half, fill)
		default:
			put(column, full, track)
		}
	}
}
