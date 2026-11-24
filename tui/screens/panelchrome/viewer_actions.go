package panelchrome

import (
	"crude/tui/rendering"
	"crude/tui/ui"
)

// ViewerButton is an icon action of the image and Markdown toolbars. It is
// the shared bracketed Button, "[ icon ]", with the icon in the middle, so it
// keeps Button's mouse, focus, selection and state paint.
type ViewerButton struct {
	*ui.Button
	name, tip, nerd, plain string
	glyph                  string // the icon for the current font preference
	inkCell                bool   // glyph is a Nerd Font icon
	owner                  ui.ComponentID
}

// NewViewerButton gives an icon an accessible name and a tooltip with its
// shortcut, if any. The caller sets theme tokens before mounting it.
func NewViewerButton(id, owner ui.ComponentID, action, name, shortcut, nerd, plain string) *ViewerButton {
	b := &ViewerButton{Button: ui.NewButton(id, plain, action), name: name, tip: name, nerd: nerd, plain: plain, owner: owner}
	if shortcut != "" {
		b.tip += " (" + shortcut + ")"
	}
	b.SetVariant(ui.VariantGhost)
	return b
}

// PointerFocusTarget returns keys to the pane after a click. A selected
// mode is persistent; zoom or Fit is only an action, not a selected mode.
func (b *ViewerButton) PointerFocusTarget() ui.ComponentID { return b.owner }

// SetTokens applies the shared color and font preferences. The idle button
// sits on the pane, not on a raised chip.
func (b *ViewerButton) SetTokens(tokens *ui.TokenSet) {
	b.Button.SetTokens(tokens)
	b.SetSurfaceBackground(tokens.Color(ui.ColorBg))
	b.glyph, b.inkCell = b.plain, false
	if NerdFont(tokens.Theme()) {
		b.glyph, b.inkCell = b.nerd, true
	}
	// A palette-only rebuild may not run Layout. Keep the label fitted to
	// the current allocation, not the width Measure would prefer.
	b.SetLabel(b.labelFor(b.Bounds().W))
}

// fullWidth is "[ icon ]" with room for the icon's ink. A Nerd Font icon
// keeps one blank cell after it: terminals draw these icons wider than one
// cell when a blank cell follows (Kitty and Ghostty use that cell, and the
// Nerd Fonts non-Mono icons are about 1.5 cells wide). The ink then has one
// blank cell on each side, "[ icon  ]", and is in the middle between the
// brackets. A plain icon is one cell wide: "[ + ]".
func (b *ViewerButton) fullWidth() int {
	w := rendering.VisibleWidth(b.glyph) + 4
	if b.inkCell {
		w++
	}
	return w
}

// labelFor keeps the ink cell only when the button has room for it, so a
// short toolbar loses blank space before it loses the icon.
func (b *ViewerButton) labelFor(width int) string {
	if b.inkCell && width >= b.fullWidth() {
		return b.glyph + " "
	}
	return b.glyph
}

// Measure asks for the full bracketed width.
func (b *ViewerButton) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
	return cs.ClampSize(ui.Size{W: b.fullWidth(), H: 1})
}

// Layout fits the label to the width the toolbar gives the button.
func (b *ViewerButton) Layout(ctx ui.LayoutContext, r ui.Rect) {
	b.SetLabel(b.labelFor(r.W))
	b.Button.Layout(ctx, r)
}

// Semantics keeps the action name independent of the chosen glyph.
func (b *ViewerButton) Semantics() ui.Semantics {
	sem := b.Button.Semantics()
	sem.Label, sem.Description = b.name, b.tip
	return sem
}

// Tooltip returns the full name and shortcut for the icon.
func (b *ViewerButton) Tooltip() (string, ui.Rect, bool) {
	r := b.Bounds()
	return b.tip, ui.Rect{W: r.W, H: r.H}, !r.Empty()
}

// viewerActionGap separates the bracketed buttons, "[ - ] [ + ]". Like
// ui.ActionBar, a short row drops the gaps before it shrinks a button.
const viewerActionGap = 1

// LayoutViewerActions packs the buttons at their measured width against the
// right of the content rail. When they do not fit, the gaps go first; then
// the buttons share the available space without overlapping.
func LayoutViewerActions(ctx ui.LayoutContext, row ui.Rect, buttons []ui.Component) {
	if len(buttons) == 0 {
		return
	}
	widths := 0
	for _, b := range buttons {
		widths += b.Measure(ctx, ui.Loose(row.W, 1)).W
	}
	gap := viewerActionGap
	if widths+gap*(len(buttons)-1) > row.W {
		gap = 0
	}
	shared := widths > row.W
	total := min(row.W, widths+gap*(len(buttons)-1))
	x := row.X + row.W - total
	for i, b := range buttons {
		w := b.Measure(ctx, ui.Loose(row.W, 1)).W
		if shared {
			w = total / len(buttons)
			if i >= len(buttons)-total%len(buttons) {
				w++
			}
		}
		b.Layout(ctx, ui.Rect{X: x, Y: row.Y, W: w, H: min(1, row.H)})
		x += w + gap
	}
}
