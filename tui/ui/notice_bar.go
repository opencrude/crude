package ui

import (
	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
)

// NoticeBar is one row of state above the content it belongs to, with the
// actions that settle it: for example an editor whose file changed on disk.
// It is state, not a notification: it stays while the state lasts and goes
// when the state ends. Its buttons are the shared ActionBar; an action leaves
// the bar with the owner's payload, so one handler serves every view.
type NoticeBar struct {
	Base
	tokens  *TokenSet
	message string
	role    SemanticColor
	payload any
	actions *ActionBar
	textW   int
}

func NewNoticeBar(id ComponentID, tokens *TokenSet) *NoticeBar {
	b := &NoticeBar{Base: NewBase(id), tokens: tokens, role: ColorWarning, actions: NewActionBar(NewID(id, "actions"), tokens)}
	b.actions.SetAlignEnd(false)
	b.actions.SetSurfaceBackground(ColorPopupBg)
	return b
}

// Set shows message in role ink with buttons; payload goes with every action.
func (b *NoticeBar) Set(message string, role SemanticColor, payload any, buttons ...ActionBarButton) {
	b.message, b.role, b.payload = message, role, payload
	b.actions.SetButtons(buttons...)
}

func (b *NoticeBar) Message() string { return b.message }

func (b *NoticeBar) SetTokens(t *TokenSet) {
	if t == nil {
		return
	}
	b.tokens = t
	b.actions.SetTokens(t)
}

func (b *NoticeBar) Children() []Component { return []Component{b.actions} }

func (b *NoticeBar) Measure(_ LayoutContext, cs Constraints) Size {
	return cs.ClampSize(Size{W: cs.MaxW, H: 1})
}

// Layout gives the buttons their room first: a decision never disappears for
// text. The message takes what is left and is cut with an ellipsis.
func (b *NoticeBar) Layout(ctx LayoutContext, r Rect) {
	b.SetBounds(r)
	lead := 2 // "! "
	buttons := b.actions.Measure(ctx, Constraints{MaxW: max(0, r.W-lead), MaxH: 1}).W
	text := rendering.VisibleWidth(b.message)
	b.textW = max(0, min(text, r.W-lead-buttons-2))
	x := min(r.W, lead+b.textW+2)
	if b.textW == 0 {
		x = min(r.W, lead)
	}
	b.actions.Layout(ctx, Rect{X: x, W: max(0, r.W-x), H: min(1, r.H)})
}

func (b *NoticeBar) Paint(buf *surface.CellBuffer, ox, oy int, _ PaintContext) {
	r := b.Bounds()
	if r.Empty() || b.tokens == nil {
		return
	}
	bg := b.tokens.Color(ColorPopupBg)
	x, y := ox+r.X, oy+r.Y
	FillRect(buf, ox, oy, r, cell.BlankCell(bg))
	buf.WriteString(x, y, "!", b.tokens.Color(b.role), bg, cell.AttrBold)
	if b.textW > 0 {
		buf.WriteString(x+2, y, rendering.TruncateVisual(b.message, b.textW, "…"), b.tokens.Color(ColorFg), bg, 0)
	}
}

// HandleAction sends a button's action on with the owner's payload. A
// button's own payload is its label, which names nothing to the owner.
func (b *NoticeBar) HandleAction(_ EventContext, a Action) EventResult {
	a.Payload = b.payload
	return EventResult{Handled: true, Action: &a, Invalidate: InvalidatePaint}
}

func (b *NoticeBar) Semantics() Semantics {
	return Semantics{Role: RoleGroup, Label: "Notice", Value: b.message}
}
