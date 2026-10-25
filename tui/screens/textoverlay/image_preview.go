package textoverlay

import (
	"image"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/ui"
)

// imagePreview is the read-only image body of the existing attachment viewer,
// not another window or event path. Two source pixels per terminal cell preserve
// image proportions with the normal roughly 1:2 terminal cell aspect ratio.
type imagePreview struct {
	ui.Base
	owner  *Screen
	tokens *ui.TokenSet
	image  image.Image
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
