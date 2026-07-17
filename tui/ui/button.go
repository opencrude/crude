package ui

import (
	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/surface"
)

// Button is a focusable, clickable control that emits an Action on activate.
type Button struct {
	Base
	label    string
	actionID string
	disabled bool
	tokens   *TokenSet
	// fallback colors when tokens is nil
	fg      cell.Color
	bg      cell.Color
	focusFG cell.Color
	focusBG cell.Color
	hoverBG cell.Color
}

// NewButton creates a button.
func NewButton(id ComponentID, label, actionID string) *Button {
	return &Button{
		Base:     NewBase(id),
		label:    label,
		actionID: actionID,
		fg:       cell.NewTrueColor(230, 237, 243),
		bg:       cell.NewTrueColor(33, 38, 45),
		focusFG:  cell.NewTrueColor(13, 17, 23),
		focusBG:  cell.NewTrueColor(88, 166, 255),
		hoverBG:  cell.NewTrueColor(48, 54, 61),
	}
}

// SetTokens wires theme tokens for state colors.
func (b *Button) SetTokens(ts *TokenSet) { b.tokens = ts }

// SetDisabled enables/disables the button.
func (b *Button) SetDisabled(d bool) { b.disabled = d }

// SetLabel updates the visible label.
func (b *Button) SetLabel(s string) { b.label = s }

// Measure: "[ label ]" width.
func (b *Button) Measure(_ LayoutContext, cs Constraints) Size {
	w := len([]rune(b.label)) + 4 // "[ " + label + " ]"
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
	fg, bg := b.fg, b.bg
	attr := cell.Attrs(0)
	if b.tokens != nil {
		fg, bg = b.tokens.ButtonColors(st)
	} else {
		switch st {
		case StateDisabled:
			fg = cell.NewTrueColor(110, 118, 129)
		case StateFocused, StatePressed:
			fg, bg = b.focusFG, b.focusBG
			attr = cell.AttrBold
		case StateHovered:
			bg = b.hoverBG
		}
	}
	if st == StateFocused || st == StatePressed {
		attr = cell.AttrBold
	}
	FillRect(buf, originX, originY, r, cell.BlankCell(bg))
	text := "[ " + b.label + " ]"
	runes := []rune(text)
	if len(runes) > r.W && r.W > 0 {
		runes = runes[:r.W]
	}
	buf.WriteString(originX+r.X, originY+r.Y, string(runes), fg, bg, attr)
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
