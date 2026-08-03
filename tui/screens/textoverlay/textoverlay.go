// Package textoverlay provides the native text/reference editor floating body.
package textoverlay

import (
	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

const (
	ScreenID  ui.ComponentID = "textoverlay"
	InputID   ui.ComponentID = "textoverlay/input"
	ActionsID ui.ComponentID = "textoverlay/actions"
)

// SaveMsg is emitted when the overlay saves its content.
type SaveMsg struct {
	Target *Screen
	RefID  string
	Text   string
}

// CancelMsg is emitted when the overlay is dismissed without saving.
type CancelMsg struct {
	Target *Screen
}

// Screen is the retained native text/reference editor body: TextArea +
// ActionBar inside the existing OverlayHost / WindowFrame shell.
type Screen struct {
	ui.Base
	th      *theme.Theme
	tokens  *ui.TokenSet
	input   *overlayInput
	actions *ui.ActionBar
	title   string
	refID   string
	focus   func(ui.ComponentID)
}

// New creates a closed text-overlay screen.
func New(th *theme.Theme) *Screen {
	if th == nil {
		th = theme.DefaultDark()
	}
	tokens := ui.NewTokenSet(th)
	s := &Screen{Base: ui.NewBase(ScreenID), th: th, tokens: tokens}
	area := ui.NewTextArea(InputID, tokens, true)
	area.SetSubmitAction("")
	s.input = &overlayInput{Base: ui.NewBase(InputID), area: area, owner: s}
	s.rebuildActions()
	return s
}

func (s *Screen) Open(refID, title, content string) {
	s.refID = refID
	s.title = title
	s.input.area.SetValue(content)
}

func (s *Screen) Close() {
	s.refID = ""
	s.title = ""
	s.input.area.Clear()
}

func (s *Screen) RefID() string { return s.refID }
func (s *Screen) Title() string { return s.title }
func (s *Screen) Value() string { return s.input.area.Value() }
func (s *Screen) IsOpen() bool  { return s.refID != "" }

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
			ID: ui.NewID(ActionsID, "save"), Label: "Save", Action: "textoverlay.save",
			Shortcut: "ctrl+s", Variant: ui.VariantPrimary, Enabled: true,
		},
		ui.ActionBarButton{
			ID: ui.NewID(ActionsID, "cancel"), Label: "Cancel", Action: "textoverlay.cancel",
			Variant: ui.VariantSecondary, Enabled: true,
		},
	)
	s.actions.SetCancelAction("textoverlay.cancel")
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
	case "textoverlay.save":
		return s.save()
	case "textoverlay.cancel":
		return s.cancel()
	case "actionbar.exit.backward", "actionbar.exit.forward":
		s.focusID(InputID)
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	}
	return ui.EventResult{Action: &action}
}

func (s *Screen) Semantics() ui.Semantics {
	label := s.title
	if label == "" {
		label = "Text editor"
	}
	return ui.Semantics{Role: ui.RoleDialog, Label: label, Value: s.Value()}
}

func (s *Screen) save() ui.EventResult {
	return ui.EventResult{
		Handled: true,
		Cmd: func() tea.Msg {
			return SaveMsg{Target: s, RefID: s.refID, Text: s.Value()}
		},
	}
}

func (s *Screen) cancel() ui.EventResult {
	return ui.EventResult{
		Handled: true,
		Cmd:     func() tea.Msg { return CancelMsg{Target: s} },
	}
}

type overlayInput struct {
	ui.Base
	area  *ui.TextArea
	owner *Screen
}

func (o *overlayInput) Measure(ctx ui.LayoutContext, cs ui.Constraints) ui.Size {
	return o.area.Measure(ctx, cs)
}

func (o *overlayInput) Layout(ctx ui.LayoutContext, r ui.Rect) {
	o.SetBounds(r)
	o.area.Layout(ctx, ui.Rect{W: r.W, H: r.H})
}

func (o *overlayInput) Paint(buf *surface.CellBuffer, ox, oy int, ctx ui.PaintContext) {
	o.area.Paint(buf, ox+o.Bounds().X, oy+o.Bounds().Y, ctx)
}

func (o *overlayInput) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	if o.owner == nil {
		return o.area.Handle(ctx, evt)
	}
	if evt.IsKey() {
		key := evt.Key
		switch {
		case key.Code == tea.KeyEsc:
			if o.area.HasActiveSelection() {
				return o.area.Handle(ctx, evt)
			}
			return o.owner.cancel()
		case key.String() == "ctrl+s":
			return o.owner.save()
		case key.String() == "ctrl+c":
			if o.area.HasActiveSelection() {
				// Viewmodel owns clipboard copy.
				return ui.EventResult{}
			}
			return o.owner.cancel()
		case key.Code == tea.KeyTab:
			o.owner.focusID(ActionsID)
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
		}
	}
	return o.area.Handle(ctx, evt)
}

func (o *overlayInput) Semantics() ui.Semantics       { return o.area.Semantics() }
func (o *overlayInput) SetTokens(tokens *ui.TokenSet) { o.area.SetTokens(tokens) }

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
