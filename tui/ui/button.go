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
	surface    cell.Color
	surfaceSet bool
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

// SetSurfaceBackground is the idle fill so unfocused buttons sit on the
// surrounding form or dock instead of a raised chip.
func (b *Button) SetSurfaceBackground(c cell.Color) {
	b.surface = c
	b.surfaceSet = true
}

// SetLabel updates the visible label.
func (b *Button) SetLabel(s string) { b.label = s }

func (b *Button) SetActionID(actionID string) { b.actionID = actionID }

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
	// Idle surface is the surrounding surface (dialog/form bg), with the
	// variant-colored label carrying identity; the accent/variant FILL is
	// reserved for keyboard focus.
	bg := tokens.Color(ColorPopupBg)
	if b.surfaceSet {
		bg = b.surface
	}
	fg := b.idleForeground(tokens)
	attr := cell.Attrs(0)
	switch st {
	case StateDisabled:
		fg = tokens.Color(ColorMuted)
	case StateHovered:
		// Hover language: subtle tinted fill, never the accent/variant fill
		// (reserved for keyboard focus, as in ButtonColors()).
		bg = tokens.Color(ColorHover)
	case StateFocused, StatePressed:
		bg = b.variantFill(tokens)
		fg = cell.ContrastColor(bg)
		attr = cell.AttrBold
	}
	FillRect(buf, originX, originY, r, cell.BlankCell(bg))
	text := "[ " + rendering.TruncateVisual(b.label, max(0, r.W-4), "…") + " ]"
	buf.WriteString(originX+r.X, originY+r.Y, rendering.TruncateVisual(text, r.W, ""), fg, bg, attr)
}

func (b *Button) idleForeground(tokens *TokenSet) cell.Color {
	if !b.variantSet {
		return tokens.Color(ColorPopupButtonFg)
	}
	switch b.variant {
	case VariantPrimary, VariantGhost:
		return tokens.Color(ColorAccent)
	case VariantDanger:
		return tokens.Color(ColorError)
	case VariantSecondary:
		return tokens.Color(ColorAccent2)
	default:
		return tokens.Color(ColorPopupButtonFg)
	}
}

func (b *Button) variantFill(tokens *TokenSet) cell.Color {
	if !b.variantSet {
		return tokens.Color(ColorAccent)
	}
	switch b.variant {
	case VariantPrimary, VariantGhost:
		return tokens.Color(ColorAccent)
	case VariantDanger:
		return tokens.Color(ColorError)
	case VariantSecondary:
		return tokens.Color(ColorAccent2)
	default:
		return tokens.Color(ColorAccent)
	}
}

func (b *Button) Handle(ctx EventContext, evt Event) EventResult {
	if b.disabled {
		return EventResult{}
	}
	switch evt.Kind {
	case EventKey:
		if evt.Key.Code == tea.KeyEnter || evt.Key.Code == tea.KeySpace {
			return b.activateControl()
		}
	case EventMouseClick:
		if evt.MouseClick.Button == tea.MouseLeft {
			return b.activateControl()
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
