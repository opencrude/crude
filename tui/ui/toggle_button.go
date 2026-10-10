package ui

import (
	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
)

// ToggleButton is a compact checkable action control.
type ToggleButton struct {
	Base
	label        string
	actionID     string
	cancelID     string
	checked      bool
	disabled     bool
	switchChrome bool
	chip         bool
	tokens       *TokenSet
}

// SetChip paints the toggle as a filter chip: a switch marker ("●"/"○")
// before the label instead of a bracketed button, so a filter state never
// reads as a command.
func (b *ToggleButton) SetChip(enabled bool) { b.chip = enabled }

// NewToggleButton creates a compact checkable action control.
func NewToggleButton(id ComponentID, label, actionID string, tokens *TokenSet) *ToggleButton {
	return &ToggleButton{Base: NewBase(id), label: label, actionID: actionID, tokens: tokens}
}

// SetTokens updates the theme token set.
func (b *ToggleButton) SetTokens(tokens *TokenSet) { b.tokens = tokens }

func (b *ToggleButton) SetLabel(label string) { b.label = label }

// SetSwitchChrome uses a quiet unselected track and a persistent selected
// background, rather than the standalone action-button background.
func (b *ToggleButton) SetSwitchChrome(enabled bool) { b.switchChrome = enabled }

// SetChecked updates the displayed state.
func (b *ToggleButton) SetChecked(checked bool) { b.checked = checked }

// Checked reports the displayed state.
func (b *ToggleButton) Checked() bool { return b.checked }

// SetDisabled enables or disables activation.
func (b *ToggleButton) SetDisabled(disabled bool) { b.disabled = disabled }

// SetCancelAction sets the optional action emitted when Escape is pressed.
func (b *ToggleButton) SetCancelAction(actionID string) { b.cancelID = actionID }

func (b *ToggleButton) Measure(_ LayoutContext, cs Constraints) Size {
	w := rendering.VisibleWidth(b.label)
	if b.chip {
		w += rendering.VisibleWidth(b.chipMarker()) + 3
	}
	return cs.ClampSize(Size{W: w, H: 1})
}

func (b *ToggleButton) chipMarker() string {
	g := DefaultGlyphsFromTokens(b.tokens)
	if b.checked {
		return g.SwitchOn
	}
	return g.SwitchOff
}

func (b *ToggleButton) Layout(_ LayoutContext, r Rect) { b.SetBounds(r) }

func (b *ToggleButton) Paint(buf *surface.CellBuffer, originX, originY int, ctx PaintContext) {
	r := b.Bounds()
	if r.Empty() {
		return
	}
	st := ResolveControlState(b.disabled, ctx.Focused, false, false, ctx.Hovered)
	if b.chip && b.tokens != nil {
		b.paintChip(buf, originX, originY, r, st)
		return
	}
	fg, bg := cell.NoColor, cell.NoColor
	if b.tokens != nil {
		fg, bg = b.tokens.ButtonColors(st)
		if b.switchChrome {
			fg, bg = b.tokens.Color(ColorMuted), b.tokens.Color(ColorPopupBg)
			if b.checked {
				fg, bg = b.tokens.Color(ColorAccent), b.tokens.Color(ColorSelection)
			} else if ctx.Hovered || ctx.Focused {
				fg, bg = b.tokens.Color(ColorFg), b.tokens.Color(ColorHover)
			}
		} else if b.checked && st != StateDisabled {
			fg = b.tokens.Color(ColorAccent)
			bg = b.tokens.Color(ColorPopupSubtleBg)
		}
	}
	FillRect(buf, originX, originY, r, cell.BlankCell(bg))
	attr := cell.Attrs(0)
	if b.switchChrome && b.checked {
		attr = cell.AttrBold
	}
	buf.WriteString(originX+r.X, originY+r.Y, rendering.TruncateVisual(b.label, r.W, ""), fg, bg, attr)
}

func (b *ToggleButton) paintChip(buf *surface.CellBuffer, originX, originY int, r Rect, st ControlState) {
	bg := b.tokens.Color(ColorPopupBg)
	labelFG, markerFG := b.tokens.Color(ColorMuted), b.tokens.Color(ColorMuted)
	attr := cell.Attrs(0)
	if b.checked {
		labelFG, markerFG = b.tokens.Color(ColorFg), b.tokens.Color(ColorAccent)
	}
	switch st {
	case StateFocused:
		bg, labelFG, attr = b.tokens.Color(ColorHover), b.tokens.Color(ColorAccent), cell.AttrBold
	case StateHovered:
		bg = b.tokens.Color(ColorHover)
	case StateDisabled:
		labelFG, markerFG = b.tokens.Color(ColorMuted), b.tokens.Color(ColorMuted)
	}
	FillRect(buf, originX, originY, r, cell.BlankCell(bg))
	marker := b.chipMarker()
	x := originX + r.X + 1
	buf.WriteString(x, originY+r.Y, marker, markerFG, bg, 0)
	x += rendering.VisibleWidth(marker) + 1
	buf.WriteString(x, originY+r.Y, rendering.TruncateVisual(b.label, max(0, r.W-(x-originX-r.X)-1), "…"), labelFG, bg, attr)
}

func (b *ToggleButton) Handle(_ EventContext, evt Event) EventResult {
	if b.disabled {
		return EventResult{}
	}
	switch evt.Kind {
	case EventKey:
		if evt.Key.Code == tea.KeyEsc && b.cancelID != "" {
			return EventResult{Handled: true, Action: &Action{ID: b.cancelID}, Invalidate: InvalidatePaint}
		}
		if evt.Key.Code == tea.KeyEnter || evt.Key.Code == tea.KeySpace {
			return EventResult{Handled: true, Action: &Action{ID: b.actionID}, Invalidate: InvalidatePaint}
		}
	case EventMouseClick:
		if evt.MouseClick.Button == tea.MouseLeft {
			return EventResult{Handled: true, Action: &Action{ID: b.actionID}, Invalidate: InvalidatePaint}
		}
	}
	return EventResult{}
}

func (b *ToggleButton) Semantics() Semantics {
	return Semantics{Role: RoleButton, Label: b.label, Selected: b.checked, Disabled: b.disabled}
}
