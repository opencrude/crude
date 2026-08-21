// Package composer provides the native Composer floating-window body.
package composer

import (
	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

const (
	ScreenID  ui.ComponentID = "composer"
	InputID   ui.ComponentID = "composer/input"
	ActionsID ui.ComponentID = "composer/actions"
)

// SubmitMsg is emitted when the composer submits its text.
type SubmitMsg struct {
	Target *Screen
	Text   string
}

// CancelMsg is emitted when the composer is dismissed.
type CancelMsg struct {
	Target *Screen
}

// Screen is the retained native Composer body: TextArea + ActionBar inside
// the existing OverlayHost / WindowFrame shell.
type Screen struct {
	ui.Base
	th      *theme.Theme
	tokens  *ui.TokenSet
	input   *composerInput
	actions *ui.ActionBar
	open    bool
	focus   func(ui.ComponentID)
}

// New creates a closed composer screen.
func New(th *theme.Theme) *Screen {
	if th == nil {
		th = theme.DefaultDark()
	}
	tokens := ui.NewTokenSet(th)
	s := &Screen{Base: ui.NewBase(ScreenID), th: th, tokens: tokens}
	area := ui.NewTextArea(InputID, tokens, true)
	area.SetSubmitAction("composer.submit")
	s.input = &composerInput{Base: ui.NewBase(InputID), area: area, owner: s}
	s.rebuildActions()
	return s
}

func (s *Screen) Open(initial string) {
	s.open = true
	if initial != "" {
		s.input.area.SetValue(initial)
	} else {
		s.input.area.Clear()
	}
}

func (s *Screen) Close() {
	s.open = false
	s.input.area.Clear()
}

func (s *Screen) IsOpen() bool  { return s.open }
func (s *Screen) Value() string { return s.input.area.Value() }
func (s *Screen) SetValue(v string) {
	s.input.area.SetValue(v)
}

// Selecting reports an in-progress mouse-drag selection.
func (s *Screen) Selecting() bool { return s.input.area.IsSelecting() }

// HasActiveSelection reports a non-empty selection range.
func (s *Screen) HasActiveSelection() bool { return s.input.area.HasActiveSelection() }

// SelectedText returns the current selection, or "".
func (s *Screen) SelectedText() string { return s.input.area.SelectedText() }

// SelectionRange returns flat rune [start, end) of the selection.
func (s *Screen) SelectionRange() (int, int) { return s.input.area.SelectionRange() }

func (s *Screen) SetFocusHandler(fn func(ui.ComponentID)) { s.focus = fn }
func (s *Screen) FocusInput()                             { s.focusID(InputID) }
func (s *Screen) focusID(id ui.ComponentID) {
	if s.focus != nil {
		s.focus(id)
	}
}

func (s *Screen) rebuildActions() {
	s.actions = ui.NewActionBar(ActionsID, s.tokens,
		ui.ActionBarButton{
			ID: ui.NewID(ActionsID, "submit"), Label: "Submit", Action: "composer.submit",
			Shortcut: "ctrl+enter", Variant: ui.VariantPrimary, Enabled: true,
		},
		ui.ActionBarButton{
			ID: ui.NewID(ActionsID, "cancel"), Label: "Cancel", Action: "composer.cancel",
			Variant: ui.VariantSecondary, Enabled: true,
		},
	)
	s.actions.SetCancelAction("composer.cancel")
}

func (s *Screen) Children() []ui.Component {
	return []ui.Component{s.input, s.actions}
}

func (s *Screen) SetTokens(tokens *ui.TokenSet) {
	s.tokens = tokens
	s.input.SetTokens(tokens)
	s.actions.SetTokens(tokens)
}

func (s *Screen) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
	return cs.ClampSize(ui.Size{W: cs.MaxW, H: cs.MaxH})
}

func (s *Screen) Layout(ctx ui.LayoutContext, r ui.Rect) {
	s.SetBounds(r)
	inputH := max(1, r.H-1)
	s.input.Layout(ctx, ui.Rect{X: 0, Y: 0, W: r.W, H: inputH})
	s.actions.Layout(ctx, ui.Rect{X: 0, Y: max(0, r.H-1), W: r.W, H: 1})
}

func (s *Screen) Paint(_ *surface.CellBuffer, _, _ int, _ ui.PaintContext) {}

func (s *Screen) Handle(_ ui.EventContext, _ ui.Event) ui.EventResult { return ui.EventResult{} }

func (s *Screen) HandleAction(_ ui.EventContext, action ui.Action) ui.EventResult {
	switch action.ID {
	case "composer.submit":
		return s.submit()
	case "composer.cancel":
		return s.cancel()
	case "actionbar.exit.backward", "actionbar.exit.forward":
		s.focusID(InputID)
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	}
	return ui.EventResult{Action: &action}
}

func (s *Screen) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleDialog, Label: "Composer", Value: s.Value()}
}

func (s *Screen) submit() ui.EventResult {
	text := s.Value()
	return ui.EventResult{
		Handled: true,
		Cmd:     func() tea.Msg { return SubmitMsg{Target: s, Text: text} },
	}
}

func (s *Screen) cancel() ui.EventResult {
	return ui.EventResult{
		Handled: true,
		Cmd:     func() tea.Msg { return CancelMsg{Target: s} },
	}
}

// composerInput wraps TextArea so Esc without a selection cancels and Tab
// moves focus to the ActionBar, matching other native floating screens.
type composerInput struct {
	ui.Base
	area  *ui.TextArea
	owner *Screen
}

func (c *composerInput) Measure(ctx ui.LayoutContext, cs ui.Constraints) ui.Size {
	return c.area.Measure(ctx, cs)
}

func (c *composerInput) Layout(ctx ui.LayoutContext, r ui.Rect) {
	c.SetBounds(r)
	c.area.Layout(ctx, ui.Rect{W: r.W, H: r.H})
}

func (c *composerInput) Paint(buf *surface.CellBuffer, ox, oy int, ctx ui.PaintContext) {
	c.area.Paint(buf, ox+c.Bounds().X, oy+c.Bounds().Y, ctx)
}

func (c *composerInput) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	if c.owner == nil {
		return c.area.Handle(ctx, evt)
	}
	if evt.IsKey() {
		key := evt.Key
		switch {
		case key.Code == tea.KeyEsc:
			if c.area.HasActiveSelection() {
				return c.area.Handle(ctx, evt)
			}
			return c.owner.cancel()
		case key.Code == tea.KeyTab:
			c.owner.focusID(ActionsID)
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
		case key.String() == "ctrl+c" && c.area.HasActiveSelection():
			// Viewmodel owns clipboard copy; leave unhandled so the host
			// does not swallow the key before the owner can copy.
			return ui.EventResult{}
		}
	}
	return c.area.Handle(ctx, evt)
}

func (c *composerInput) Semantics() ui.Semantics       { return c.area.Semantics() }
func (c *composerInput) SetTokens(tokens *ui.TokenSet) { c.area.SetTokens(tokens) }

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
