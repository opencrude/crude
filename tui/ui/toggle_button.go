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
	label    string
	actionID string
	cancelID string
	checked  bool
	disabled bool
	tokens   *TokenSet
}

// NewToggleButton creates a compact checkable action control.
func NewToggleButton(id ComponentID, label, actionID string, tokens *TokenSet) *ToggleButton {
	return &ToggleButton{Base: NewBase(id), label: label, actionID: actionID, tokens: tokens}
}

// SetTokens updates the theme token set.
func (b *ToggleButton) SetTokens(tokens *TokenSet) { b.tokens = tokens }

// SetChecked updates the displayed state.
func (b *ToggleButton) SetChecked(checked bool) { b.checked = checked }

// Checked reports the displayed state.
func (b *ToggleButton) Checked() bool { return b.checked }

// SetDisabled enables or disables activation.
func (b *ToggleButton) SetDisabled(disabled bool) { b.disabled = disabled }

// SetCancelAction sets the optional action emitted when Escape is pressed.
func (b *ToggleButton) SetCancelAction(actionID string) { b.cancelID = actionID }

func (b *ToggleButton) Measure(_ LayoutContext, cs Constraints) Size {
	return cs.ClampSize(Size{W: rendering.VisibleWidth(b.label), H: 1})
}

func (b *ToggleButton) Layout(_ LayoutContext, r Rect) { b.SetBounds(r) }

func (b *ToggleButton) Paint(buf *surface.CellBuffer, originX, originY int, ctx PaintContext) {
	r := b.Bounds()
	if r.Empty() {
		return
	}
	st := ResolveControlState(b.disabled, ctx.Focused, false, false, ctx.Hovered)
	fg, bg := cell.NoColor, cell.NoColor
	if b.tokens != nil {
		fg, bg = b.tokens.ButtonColors(st)
		if b.checked && st != StateDisabled {
			fg = b.tokens.Color(ColorAccent)
			bg = b.tokens.Color(ColorPopupSubtleBg)
		}
	}
	FillRect(buf, originX, originY, r, cell.BlankCell(bg))
	buf.WriteString(originX+r.X, originY+r.Y, rendering.TruncateVisual(b.label, r.W, ""), fg, bg, 0)
}

func (b *ToggleButton) Handle(_ EventContext, msg tea.Msg) EventResult {
	if b.disabled {
		return EventResult{}
	}
	switch m := msg.(type) {
	case tea.KeyPressMsg:
		if m.Code == tea.KeyEsc && b.cancelID != "" {
			return EventResult{Handled: true, Action: &Action{ID: b.cancelID}, Invalidate: InvalidatePaint}
		}
		if m.Code == tea.KeyEnter || m.Code == tea.KeySpace {
			return EventResult{Handled: true, Action: &Action{ID: b.actionID}, Invalidate: InvalidatePaint}
		}
	case tea.MouseClickMsg:
		if m.Button == tea.MouseLeft {
			return EventResult{Handled: true, Action: &Action{ID: b.actionID}, Invalidate: InvalidatePaint}
		}
	}
	return EventResult{}
}

func (b *ToggleButton) Semantics() Semantics {
	return Semantics{Role: RoleButton, Label: b.label, Selected: b.checked, Disabled: b.disabled}
}
