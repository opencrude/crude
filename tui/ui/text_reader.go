package ui

import "crude/tui/surface"

// TextReader composes the native read-only text control with an on-demand
// scrollbar. Both controls use TextArea's one scroll model; selection, wrapping,
// keyboard movement and pointer capture remain owned by the native controls.
type TextReader struct {
	Base
	text      *TextArea
	scrollbar *ScrollbarControl
	measured  bool
	measureAt Constraints
	revision  uint64
	size      Size
}

func NewTextReader(id ComponentID, tokens *TokenSet) *TextReader {
	r := &TextReader{Base: NewBase(id)}
	r.text = NewTextArea(NewID(id, "text"), tokens, true)
	r.text.SetReadOnly(true)
	r.scrollbar = NewScrollbarControl(NewID(id, "scrollbar"), r.text.Scroll(), tokens)
	r.scrollbar.SetPointerTargetSize(1)
	r.scrollbar.SetSurfaceBackground(ColorPopupBg)
	r.SetTokens(tokens)
	return r
}

func (r *TextReader) TextArea() *TextArea { return r.text }

func (r *TextReader) SetTokens(tokens *TokenSet) {
	r.text.SetTokens(tokens)
	r.scrollbar.SetTokens(tokens)
	if tokens != nil {
		r.text.SetBackgroundColor(tokens.Color(ColorPopupBg))
	}
}

func (r *TextReader) Children() []Component { return []Component{r.text, r.scrollbar} }

func (r *TextReader) Measure(ctx LayoutContext, cs Constraints) Size {
	// Measurement uses the width without a scrollbar. Actual overflow layout
	// can use one less column; do not bounce the text wrap cache every frame.
	revision := r.text.edit.contentRevision
	if !r.measured || r.measureAt != cs || r.revision != revision {
		r.size = r.text.Measure(ctx, cs)
		r.measured, r.measureAt, r.revision = true, cs, revision
	}
	return r.size
}

func (r *TextReader) Layout(ctx LayoutContext, bounds Rect) {
	r.SetBounds(bounds)
	w, h := max(0, bounds.W), max(0, bounds.H)
	barW := 0
	if w > 1 && h > 0 && r.text.VisualLineCount(w) > h {
		barW = ScrollbarWidth
	}
	r.text.Layout(ctx, Rect{W: w - barW, H: h})
	r.scrollbar.Layout(ctx, Rect{X: w - barW, W: barW, H: h})
}

func (r *TextReader) Paint(*surface.CellBuffer, int, int, PaintContext) {}
