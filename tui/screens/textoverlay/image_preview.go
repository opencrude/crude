package textoverlay

import (
	"image"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi/kitty"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/ui"
)

// imagePreview is the read-only image body of the existing attachment viewer.
// Supported terminals paint original pixels through composited placeholder
// cells. Other terminals retain the two-sample half-block approximation.
type imagePreview struct {
	ui.Base
	owner                 *Screen
	tokens                *ui.TokenSet
	image                 image.Image
	resource              *tea.KittyImage
	graphics              bool
	cellWidth, cellHeight int
	placeholderSize       ui.Size
	placeholderID         uint32
	placeholders          []string
}

func (p *imagePreview) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
	return cs.ClampSize(ui.Size{W: cs.MaxW, H: cs.MaxH})
}

func (p *imagePreview) Layout(_ ui.LayoutContext, r ui.Rect) { p.SetBounds(r) }

func (p *imagePreview) Paint(buf *surface.CellBuffer, ox, oy int, _ ui.PaintContext) {
	r := p.Bounds()
	if r.Empty() || p.tokens == nil {
		return
	}
	bg := p.tokens.Color(ui.ColorPopupBg)
	ui.FillRect(buf, ox, oy, r, cell.BlankCell(bg))
	if p.image == nil || p.image.Bounds().Empty() {
		return
	}
	source := p.image.Bounds()
	if p.graphics && p.resource != nil && !p.imageRect().Empty() {
		p.paintGraphics(buf, ox, oy, bg)
		return
	}
	w, h := r.W, max(1, int(int64(source.Dy())*int64(r.W)/int64(source.Dx())))
	if h > r.H*2 {
		h = r.H * 2
		w = max(1, int(int64(source.Dx())*int64(h)/int64(source.Dy())))
	}
	x0, y0 := ox+r.X+(r.W-w)/2, oy+r.Y+(r.H-(h+1)/2)/2
	sample := func(x, y int) cell.Color {
		if y >= h {
			return bg
		}
		return rendering.ImagePixelToCell(p.image.At(source.Min.X+x*source.Dx()/w, source.Min.Y+y*source.Dy()/h), bg)
	}
	for y := 0; y < h; y += 2 {
		for x := 0; x < w; x++ {
			buf.Set(x0+x, y0+y/2, cell.NewCell('▀', sample(x, y), sample(x, y+1), 0))
		}
	}
}

// imageRect uses the terminal's measured cell aspect, never a font heuristic,
// for high-resolution placement. The protocol preserves the source aspect in
// the final fractional-cell letterbox as well.
func (p *imagePreview) imageRect() ui.Rect {
	r := p.Bounds()
	if p.image == nil || r.Empty() {
		return ui.Rect{}
	}
	source := p.image.Bounds()
	if source.Empty() {
		return ui.Rect{}
	}
	cw, ch := max(1, p.cellWidth), max(1, p.cellHeight)
	w, h := r.W, r.H
	if int64(source.Dx())*int64(h)*int64(ch) >= int64(source.Dy())*int64(w)*int64(cw) {
		h = max(1, int((int64(source.Dy())*int64(w)*int64(cw)+int64(source.Dx())*int64(ch)-1)/(int64(source.Dx())*int64(ch))))
	} else {
		w = max(1, int((int64(source.Dx())*int64(h)*int64(ch)+int64(source.Dy())*int64(cw)-1)/(int64(source.Dy())*int64(cw))))
	}
	// Kitty defines 297 diacritics for row/column indices. Full coordinates on
	// every cell preserve correct crops under horizontal clipping and occlusion.
	if w > 297 || h > 297 {
		return ui.Rect{}
	}
	return ui.Rect{X: r.X + (r.W-w)/2, Y: r.Y + (r.H-h)/2, W: w, H: h}
}

func (p *imagePreview) paintGraphics(buf *surface.CellBuffer, ox, oy int, bg cell.Color) {
	r := p.imageRect()
	if r.Empty() {
		return
	}
	if size := (ui.Size{W: r.W, H: r.H}); p.placeholderSize != size || p.placeholderID != p.resource.ID() {
		p.placeholderSize = size
		p.placeholderID = p.resource.ID()
		p.placeholders = make([]string, r.W*r.H)
		for y := 0; y < r.H; y++ {
			for x := 0; x < r.W; x++ {
				p.placeholders[y*r.W+x] = string([]rune{kitty.Placeholder, kitty.Diacritic(y), kitty.Diacritic(x), kitty.Diacritic(int(p.resource.ID() >> 24))})
			}
		}
	}
	id := p.resource.ID()
	fg := rendering.KittyImageColor(id)
	for y := 0; y < r.H; y++ {
		for x := 0; x < r.W; x++ {
			buf.Set(ox+r.X+x, oy+r.Y+y, cell.NewClusterCell(p.placeholders[y*r.W+x], 1, fg, bg, 0))
		}
	}
}

func (p *imagePreview) Handle(_ ui.EventContext, evt ui.Event) ui.EventResult {
	if !evt.IsKey() {
		return ui.EventResult{}
	}
	switch evt.Key.Code {
	case tea.KeyEsc:
		return p.owner.cancel()
	case tea.KeyTab:
		p.owner.focusID(ActionsID)
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	}
	// Image previews cannot edit or save, including stale text-editor chords.
	return ui.EventResult{Handled: true}
}

func (p *imagePreview) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleLabel, Label: p.owner.Title(), Description: "Read-only image preview"}
}
