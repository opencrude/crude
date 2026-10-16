package ui

import (
	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/surface"
)

// SplitDivider is the hit-tested resize handle for a SplitModel. Host pointer
// capture keeps motion routed here after the pointer leaves the 1-cell (or
// N-cell) handle, matching ScrollbarControl.
type SplitDivider struct {
	Base
	model        *SplitModel
	tokens       *TokenSet
	colorRole    SemanticColor
	fgOverride   cell.Color
	fill         bool
	transparent  bool
	surfaceRole  SemanticColor
	surfaceSet   bool
	currentFirst int // last laid-out first-pane size; drag deltas start here
}

// NewSplitDivider creates a divider bound to model. model must be non-nil.
func NewSplitDivider(id ComponentID, model *SplitModel) *SplitDivider {
	if model == nil {
		model = NewSplitModel(Horizontal)
	}
	return &SplitDivider{
		Base:      NewBase(id),
		model:     model,
		colorRole: ColorBorder,
	}
}

// SetTokens updates theme tokens.
func (d *SplitDivider) SetTokens(ts *TokenSet) { d.tokens = ts }

// SetColorRole selects the idle glyph color. Hover and drag use ColorAccent.
func (d *SplitDivider) SetColorRole(role SemanticColor) { d.colorRole = role }

// SetForeground overrides the idle glyph color (e.g. tree BorderFg). NoColor
// falls back to ColorRole.
func (d *SplitDivider) SetForeground(c cell.Color) { d.fgOverride = c }

// SetFill paints blank cells across the handle so a 3-cell gap can look like
// " │ " instead of a single centered glyph.
func (d *SplitDivider) SetFill(fill bool) { d.fill = fill }

// SetTransparent uses a default (inherit) background so the handle sits on
// the parent fill.
func (d *SplitDivider) SetTransparent(v bool) { d.transparent = v }

// SetSurfaceBackground selects the token role that fills the handle's cells,
// mirroring ScrollbarControl. Use it when the divider sits on a floating
// window body: its rule then carries the popup fill explicitly instead of
// relying on inherited-cell transparency, which a direct paint path may not
// resolve. NoColor falls back to the popup fill when tokens are present.
func (d *SplitDivider) SetSurfaceBackground(role SemanticColor) {
	d.surfaceRole = role
	d.surfaceSet = true
}

// SetCurrentFirst records the first-pane size that Layout assigned this
// frame. BeginDrag snapshots it so the handle can live in a parent that is
// not itself a SplitPane (sidebar last column, settings rail gap).
func (d *SplitDivider) SetCurrentFirst(n int) { d.currentFirst = n }

// Model returns the bound split model.
func (d *SplitDivider) Model() *SplitModel { return d.model }

// Measure is the divider thickness along the main axis, stretching on the
// cross axis.
func (d *SplitDivider) Measure(_ LayoutContext, cs Constraints) Size {
	thick := 1
	if d.model != nil {
		thick = d.model.DividerSize()
		if thick < 1 {
			thick = 1
		}
	}
	if d.model != nil && d.model.Dir() == Vertical {
		return cs.ClampSize(Size{W: cs.MaxW, H: thick})
	}
	return cs.ClampSize(Size{W: thick, H: cs.MaxH})
}

// Layout stores bounds.
func (d *SplitDivider) Layout(_ LayoutContext, r Rect) { d.SetBounds(r) }

// Paint draws the resize glyph. Horizontal splits use BorderV; vertical
// splits use BorderH. A multi-cell handle centers the glyph.
func (d *SplitDivider) Paint(buf *surface.CellBuffer, originX, originY int, ctx PaintContext) {
	r := d.Bounds()
	if r.Empty() || buf == nil {
		return
	}
	ox, oy := originX+r.X, originY+r.Y
	fg, bg := d.paintColors(ctx)
	glyph := d.glyph()
	vertical := d.model == nil || d.model.Dir() == Horizontal
	defer d.paintCollapseControls(buf, ox, oy, r, bg)
	if vertical {
		mid := r.W / 2
		for y := 0; y < r.H; y++ {
			for x := 0; x < r.W; x++ {
				ch := glyph
				if x != mid {
					if !d.fill {
						continue
					}
					ch = ' '
				}
				buf.Set(ox+x, oy+y, cell.NewCell(ch, fg, bg, 0))
			}
		}
		return
	}
	mid := r.H / 2
	for y := 0; y < r.H; y++ {
		for x := 0; x < r.W; x++ {
			ch := glyph
			if y != mid {
				if !d.fill {
					continue
				}
				ch = ' '
			}
			buf.Set(ox+x, oy+y, cell.NewCell(ch, fg, bg, 0))
		}
	}
}

// collapseControls lists the clickable collapse glyphs at the start of the
// handle: which pane each hides (or restores) and its offset on the cross axis.
func (d *SplitDivider) collapseControls() []struct {
	side   SplitSide
	glyph  rune
	offset int
} {
	if d.model == nil || !d.model.Collapsible() {
		return nil
	}
	horizontal := d.model.Dir() == Horizontal
	pick := func(h, v rune) rune {
		if horizontal {
			return h
		}
		return v
	}
	type control = struct {
		side   SplitSide
		glyph  rune
		offset int
	}
	switch d.model.Collapsed() {
	case SplitFirst:
		return []control{{SplitFirst, pick('▸', '▾'), 0}}
	case SplitSecond:
		return []control{{SplitSecond, pick('◂', '▴'), 0}}
	}
	return []control{{SplitFirst, pick('◂', '▴'), 0}, {SplitSecond, pick('▸', '▾'), 1}}
}

func (d *SplitDivider) paintCollapseControls(buf *surface.CellBuffer, ox, oy int, r Rect, bg cell.Color) {
	fg := cell.NoColor
	if d.tokens != nil {
		fg = d.tokens.Color(ColorAccent)
	}
	for _, control := range d.collapseControls() {
		x, y := ox+r.W/2, oy+control.offset
		if d.model.Dir() == Vertical {
			x, y = ox+control.offset, oy+r.H/2
		}
		if control.offset < r.H || d.model.Dir() == Vertical && control.offset < r.W {
			buf.Set(x, y, cell.NewCell(control.glyph, fg, bg, 0))
		}
	}
}

// collapseAt returns the pane the control at a local position toggles.
func (d *SplitDivider) collapseAt(localX, localY int) SplitSide {
	offset := localY
	if d.model.Dir() == Vertical {
		offset = localX
	}
	for _, control := range d.collapseControls() {
		if control.offset == offset {
			return control.side
		}
	}
	return SplitNone
}

func (d *SplitDivider) paintColors(ctx PaintContext) (fg, bg cell.Color) {
	hovered := ctx.Hovered || (d.model != nil && d.model.Dragging())
	if d.tokens != nil {
		if d.surfaceSet {
			bg = d.tokens.Color(d.surfaceRole)
		} else if !d.transparent {
			bg = d.tokens.Color(ColorBg)
		}
		if hovered {
			fg = d.tokens.Color(ColorAccent)
		} else if !d.fgOverride.IsDefault() {
			fg = d.fgOverride
		} else {
			fg = d.tokens.Color(d.colorRole)
		}
		return fg, bg
	}
	if hovered {
		return cell.NoColor, bg
	}
	if !d.fgOverride.IsDefault() {
		return d.fgOverride, bg
	}
	return cell.NoColor, bg
}

func (d *SplitDivider) glyph() rune {
	g := DefaultGlyphsFromTokens(d.tokens)
	if d.model != nil && d.model.Dir() == Vertical {
		return FirstRune(g.BorderH, '─')
	}
	return FirstRune(g.BorderV, '│')
}

// CancelPointerCapture ends an in-progress drag without reverting the size.
func (d *SplitDivider) CancelPointerCapture() {
	if d.model != nil {
		d.model.CancelDrag()
	}
}

// Handle starts, updates, and ends a resize drag. Keyboard arrows nudge the
// first pane when the divider is focused.
func (d *SplitDivider) Handle(ctx EventContext, evt Event) EventResult {
	if d.model == nil {
		return EventResult{}
	}
	switch evt.Kind {
	case EventMouseClick:
		if evt.MouseClick.Button != tea.MouseLeft {
			return EventResult{}
		}
		if side := d.collapseAt(ctx.LocalX, ctx.LocalY); side != SplitNone {
			d.model.ToggleCollapsed(side)
			return EventResult{Handled: true, Invalidate: InvalidateLayout | InvalidatePaint}
		}
		if d.model.Collapsed() != SplitNone {
			// A collapsed split has nothing to drag; its control restores it.
			return EventResult{Handled: true}
		}
		d.model.BeginDrag(d.model.PointerMain(ctx.ScreenX, ctx.ScreenY), d.currentFirst)
		return EventResult{Handled: true, Invalidate: InvalidatePaint}
	case EventMouseMotion:
		if !d.model.Dragging() {
			return EventResult{}
		}
		d.model.DragTo(d.model.PointerMain(ctx.ScreenX, ctx.ScreenY))
		return d.resizeResult()
	case EventMouseRelease:
		if !d.model.Dragging() {
			return EventResult{}
		}
		d.model.EndDrag()
		return EventResult{Handled: true, Invalidate: InvalidatePaint}
	case EventKey:
		delta := 0
		if d.model.Dir() == Vertical {
			switch evt.Key.Code {
			case tea.KeyUp:
				delta = -1
			case tea.KeyDown:
				delta = 1
			}
		} else {
			switch evt.Key.Code {
			case tea.KeyLeft:
				delta = -1
			case tea.KeyRight:
				delta = 1
			}
		}
		if delta == 0 {
			return EventResult{}
		}
		d.model.NudgeFirst(delta, d.currentFirst)
		return d.resizeResult()
	}
	return EventResult{}
}

func (d *SplitDivider) resizeResult() EventResult {
	first := d.model.FirstSize()
	d.currentFirst = first
	return EventResult{
		Handled:    true,
		Invalidate: InvalidateLayout | InvalidatePaint,
		Action:     &Action{ID: ActionSplitResize, Payload: SplitResize{ID: d.ID(), First: first}},
	}
}

// Semantics reports a separator role.
func (d *SplitDivider) Semantics() Semantics {
	return Semantics{Role: RoleSeparator, Label: "resize"}
}

var _ Component = (*SplitDivider)(nil)
var _ PointerCaptureCanceller = (*SplitDivider)(nil)
