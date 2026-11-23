package panelchrome

import "crude/tui/ui"

// ViewerButton is the image and document toolbar's compact icon action. It
// shares the shell's font preference and Button's mouse, focus and state paint.
type ViewerButton struct {
	*ui.IconButton
	name, tip, nerd, plain string
	owner                  ui.ComponentID
}

// NewViewerButton gives an icon an accessible name and a tooltip with its
// shortcut, if any. The caller sets theme tokens before mounting it.
func NewViewerButton(id, owner ui.ComponentID, action, name, shortcut, nerd, plain string) *ViewerButton {
	b := &ViewerButton{IconButton: ui.NewIconButton(id, plain, name, action, nil), name: name, tip: name, nerd: nerd, plain: plain, owner: owner}
	if shortcut != "" {
		b.tip += " (" + shortcut + ")"
	}
	b.SetVariant(ui.VariantGhost)
	return b
}

// PointerFocusTarget returns keys to the pane after a click. A selected
// mode is persistent; zoom or Fit is only an action, not a selected mode.
func (b *ViewerButton) PointerFocusTarget() ui.ComponentID { return b.owner }

// SetTokens applies the shared color and font preferences.
func (b *ViewerButton) SetTokens(tokens *ui.TokenSet) {
	b.IconButton.SetTokens(tokens)
	glyph := b.plain
	if NerdFont(tokens.Theme()) {
		glyph = b.nerd
	}
	b.SetGlyph(glyph)
}

// Semantics keeps the action name independent of the chosen glyph.
func (b *ViewerButton) Semantics() ui.Semantics {
	sem := b.IconButton.Semantics()
	sem.Label, sem.Description = b.name, b.tip
	return sem
}

// Tooltip returns the full name and shortcut for the icon.
func (b *ViewerButton) Tooltip() (string, ui.Rect, bool) {
	r := b.Bounds()
	return b.tip, ui.Rect{W: r.W, H: r.H}, !r.Empty()
}

// ViewerActionWidth is the hit width of each compact viewer action. Equal
// slots and centered icons leave room for Nerd Font ink outside its cell.
const ViewerActionWidth = 5

// Measure gives all viewer actions an equal hit width, independent of glyph.
func (b *ViewerButton) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
	return cs.ClampSize(ui.Size{W: ViewerActionWidth, H: 1})
}

// LayoutViewerActions packs equal slots against the content rail. At narrow
// widths they share the available space without overlapping.
func LayoutViewerActions(ctx ui.LayoutContext, row ui.Rect, buttons []ui.Component) {
	if len(buttons) == 0 {
		return
	}
	total := min(row.W, len(buttons)*ViewerActionWidth)
	x := row.X + row.W - total
	for i, b := range buttons {
		w := total / len(buttons)
		if i >= len(buttons)-total%len(buttons) {
			w++
		}
		b.Layout(ctx, ui.Rect{X: x, Y: row.Y, W: w, H: min(1, row.H)})
		x += w
	}
}
