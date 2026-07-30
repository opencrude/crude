package ui

import (
	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/theme"
)

// Button is a focusable, clickable control that emits an Action on activate.
type Button struct {
	Base
	label      string
	actionID   string
	disabled   bool
	tokens     *TokenSet
	variant    ButtonVariant
	variantSet bool
}

// NewButton creates a button.
func NewButton(id ComponentID, label, actionID string) *Button {
	return &Button{
		Base:     NewBase(id),
		label:    label,
		actionID: actionID,
	}
}

// SetTokens wires theme tokens for state colors.
func (b *Button) SetTokens(ts *TokenSet) { b.tokens = ts }

// SetDisabled enables/disables the button.
func (b *Button) SetDisabled(d bool) { b.disabled = d }

// SetVariant selects the semantic treatment used by ActionBar buttons.
func (b *Button) SetVariant(v ButtonVariant) { b.variant, b.variantSet = v, true }

// SetLabel updates the visible label.
func (b *Button) SetLabel(s string) { b.label = s }

func (b *Button) tokenSet() *TokenSet {
	if b.tokens != nil {
		return b.tokens
	}
	return NewTokenSet(theme.DefaultDark())
}

// Measure: "[ label ]" width.
func (b *Button) Measure(_ LayoutContext, cs Constraints) Size {
	w := rendering.VisibleWidth(b.label) + 4 // "[ " + label + " ]"
	return cs.ClampSize(Size{W: w, H: 1})
}

func (b *Button) Layout(_ LayoutContext, r Rect) {
	b.SetBounds(r)
}

func (b *Button) Paint(buf *surface.CellBuffer, originX, originY int, ctx PaintContext) {
	r := b.Bounds()
	if r.Empty() {
		return
	}
	st := ResolveControlState(b.disabled, ctx.Focused, false, false, ctx.Hovered)
	tokens := b.tokenSet()
	fg, bg := tokens.ButtonColors(st)
	attr := cell.Attrs(0)
	if st != StateDisabled && b.variantSet {
		switch b.variant {
		case VariantPrimary:
			fg, bg = tokens.Color(ColorBg), tokens.Color(ColorAccent)
		case VariantDanger:
			fg, bg = tokens.Color(ColorBg), tokens.Color(ColorError)
		case VariantGhost:
			fg, bg = tokens.Color(ColorAccent), tokens.Color(ColorPopupBg)
		case VariantSecondary:
			fg, bg = tokens.Color(ColorPopupButtonFg), tokens.Color(ColorPopupButtonBg)
		}
		if st == StateHovered {
			bg = tokens.Color(ColorHover)
			fg = tokens.Color(ColorFg)
		}
		if st == StateFocused || st == StatePressed {
			// Keyboard focus: Accent fill + bold. Distinct from unfocused primary
			// via bold, and from secondary via Accent vs PopupButtonBg.
			fg, bg = tokens.Color(ColorBg), tokens.Color(ColorAccent)
			if b.variant == VariantDanger {
				bg = tokens.Color(ColorError)
			}
			attr = cell.AttrBold
		}
	}
	if st == StateFocused || st == StatePressed {
		attr = cell.AttrBold
	}
	FillRect(buf, originX, originY, r, cell.BlankCell(bg))
	text := "[ " + b.label + " ]"
	buf.WriteString(originX+r.X, originY+r.Y, rendering.TruncateVisual(text, r.W, ""), fg, bg, attr)
}

func (b *Button) Handle(ctx EventContext, msg tea.Msg) EventResult {
	if b.disabled {
		return EventResult{}
	}
	switch m := msg.(type) {
	case tea.KeyPressMsg:
		if m.Code == tea.KeyEnter || m.Code == tea.KeySpace {
			return EventResult{
				Handled:    true,
				Action:     &Action{ID: b.actionID, Payload: b.label},
				Invalidate: InvalidatePaint,
			}
		}
	case tea.MouseClickMsg:
		if m.Button == tea.MouseLeft {
			return EventResult{
				Handled:    true,
				Action:     &Action{ID: b.actionID, Payload: b.label},
				Invalidate: InvalidatePaint,
			}
		}
	}
	return EventResult{}
}

func (b *Button) Semantics() Semantics {
	return Semantics{
		Role:     RoleButton,
		Label:    b.label,
		Disabled: b.disabled,
	}
}
