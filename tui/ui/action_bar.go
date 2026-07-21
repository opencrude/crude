package ui

import (
	tea "charm.land/bubbletea/v2"

	"crude/tui/rendering"
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
	ID      ComponentID
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
	focused int
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
		bid := b.ID
		if bid == RootID {
			bid = NewID(id, b.Label)
		}
		btn := NewButton(bid, b.Label, b.Action)
		if tokens != nil {
			btn.SetTokens(tokens)
		}
		btn.SetDisabled(!b.Enabled)
		btn.SetVariant(b.Variant)
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
	// Buttons are retained by the bar so it can provide one focus scope and
	// translate actions at the screen boundary.
	return nil
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
	x := 0
	for i := range ab.buttons {
		b := ab.buttons[i].button
		bw := rendering.VisibleWidth(b.label) + 4
		b.Layout(ctx, Rect{X: x, Y: 0, W: bw, H: 1})
		x += bw + ab.gap
	}
}

func (ab *ActionBar) Paint(buf *surface.CellBuffer, originX, originY int, ctx PaintContext) {
	for i := range ab.buttons {
		buttonCtx := ctx
		buttonCtx.Focused = ctx.Focused && i == ab.focused
		ab.buttons[i].button.Paint(buf, originX+ab.Bounds().X, originY+ab.Bounds().Y, buttonCtx)
	}
}

func (ab *ActionBar) Handle(ctx EventContext, msg tea.Msg) EventResult {
	if key, ok := msg.(tea.KeyPressMsg); ok {
		switch key.Code {
		case tea.KeyTab:
			if key.Mod.Contains(tea.ModShift) {
				if ab.focused == 0 {
					return EventResult{Handled: true, Action: &Action{ID: "actionbar.exit.backward"}, Invalidate: InvalidateFocus | InvalidatePaint}
				}
				ab.focused--
				return EventResult{Handled: true, Invalidate: InvalidatePaint}
			}
			if len(ab.buttons) > 0 && ab.focused == len(ab.buttons)-1 {
				return EventResult{Handled: true, Action: &Action{ID: "actionbar.exit.forward"}, Invalidate: InvalidateFocus | InvalidatePaint}
			}
			ab.focused++
			return EventResult{Handled: true, Invalidate: InvalidatePaint}
		case tea.KeyLeft:
			if len(ab.buttons) > 0 && ab.focused > 0 {
				ab.focused--
			}
			return EventResult{Handled: true, Invalidate: InvalidatePaint}
		case tea.KeyRight:
			if len(ab.buttons) > 0 && ab.focused < len(ab.buttons)-1 {
				ab.focused++
			}
			return EventResult{Handled: true, Invalidate: InvalidatePaint}
		}
	}
	for i := range ab.buttons {
		b := ab.buttons[i].button
		br := b.Bounds()
		if click, ok := msg.(tea.MouseClickMsg); ok && click.Button == tea.MouseLeft && br.Contains(ctx.LocalX, ctx.LocalY) {
			ab.focused = i
		}
		childCtx := EventContext{
			Theme:   ctx.Theme,
			Focused: ctx.Focused && i == ab.focused,
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
