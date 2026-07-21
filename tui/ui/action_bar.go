package ui

import (
	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
)

// ButtonVariant controls the visual style of a button in an ActionBar.
type ButtonVariant int

const (
	VariantPrimary ButtonVariant = iota
	VariantSecondary
	VariantDanger
	VariantGhost
)

// ActionBarButton is a button in an ActionBar.
type ActionBarButton struct {
	Label   string
	Action  string
	Variant ButtonVariant
	Enabled bool
}

// ActionBar is a horizontal row of buttons for form save/cancel/reset actions.
type ActionBar struct {
	Base
	buttons []actionBarBtn
	tokens  *TokenSet
	gap     int
}

type actionBarBtn struct {
	label   string
	action  string
	variant ButtonVariant
	enabled bool
	button  *Button
}

// NewActionBar creates an action bar with the given buttons.
func NewActionBar(id ComponentID, tokens *TokenSet, btns ...ActionBarButton) *ActionBar {
	ab := &ActionBar{
		Base:    NewBase(id),
		tokens:  tokens,
		gap:     2,
		buttons: make([]actionBarBtn, len(btns)),
	}
	for i, b := range btns {
		bid := NewID(id, b.Label)
		btn := NewButton(bid, b.Label, b.Action)
		if tokens != nil {
			btn.SetTokens(tokens)
		}
		btn.SetDisabled(!b.Enabled)
		ab.buttons[i] = actionBarBtn{
			label:   b.Label,
			action:  b.Action,
			variant: b.Variant,
			enabled: b.Enabled,
			button:  btn,
		}
	}
	return ab
}

func (ab *ActionBar) SetTokens(ts *TokenSet) {
	ab.tokens = ts
	for i := range ab.buttons {
		ab.buttons[i].button.SetTokens(ts)
	}
}

func (ab *ActionBar) Children() []Component {
	out := make([]Component, len(ab.buttons))
	for i := range ab.buttons {
		out[i] = ab.buttons[i].button
	}
	return out
}

func (ab *ActionBar) Measure(ctx LayoutContext, cs Constraints) Size {
	totalW := 0
	for i := range ab.buttons {
		s := ab.buttons[i].button.Measure(ctx, Loose(cs.MaxW, 1))
		totalW += s.W
		if i < len(ab.buttons)-1 {
			totalW += ab.gap
		}
	}
	return cs.ClampSize(Size{W: totalW, H: 1})
}

func (ab *ActionBar) Layout(ctx LayoutContext, r Rect) {
	ab.SetBounds(r)
	x := r.X
	for i := range ab.buttons {
		b := ab.buttons[i].button
		bw := len([]rune(b.label)) + 4
		b.Layout(ctx, Rect{X: x, Y: r.Y, W: bw, H: 1})
		x += bw + ab.gap
	}
}

func (ab *ActionBar) Paint(buf *surface.CellBuffer, originX, originY int, ctx PaintContext) {
	for i := range ab.buttons {
		PaintTree(ab.buttons[i].button, buf, originX, originY, ctx)
	}
}

func (ab *ActionBar) Handle(ctx EventContext, msg tea.Msg) EventResult {
	for i := range ab.buttons {
		b := ab.buttons[i].button
		br := b.Bounds()
		childCtx := EventContext{
			Theme:   ctx.Theme,
			Focused: ctx.Focused && br.Contains(ctx.LocalX, ctx.LocalY),
			LocalX:  ctx.LocalX - br.X,
			LocalY:  ctx.LocalY - br.Y,
			ScreenX: ctx.ScreenX,
			ScreenY: ctx.ScreenY,
		}
		if r := b.Handle(childCtx, msg); r.Handled {
			return r
		}
	}
	return EventResult{}
}

