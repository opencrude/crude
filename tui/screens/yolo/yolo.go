// Package yolo provides the retained YOLO safety-confirmation screen.
package yolo

import (
	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

const (
	ScreenID  ui.ComponentID = "session-yolo"
	FormID    ui.ComponentID = "session-yolo/form"
	ActionsID ui.ComponentID = "session-yolo/actions"
)

type ResultMsg struct {
	Target  *Screen
	Enabled bool
}

type Screen struct {
	ui.Base
	tokens  *ui.TokenSet
	form    *ui.Form
	actions *ui.ActionBar
}

func New(th *theme.Theme) *Screen {
	if th == nil {
		th = theme.DefaultDark()
	}
	s := &Screen{Base: ui.NewBase(ScreenID), tokens: ui.NewTokenSet(th)}
	s.Reset()
	return s
}

func (s *Screen) Reset() {
	model := ui.NewFormModel()
	for _, field := range []struct{ id, label, value string }{
		{"effect", "Effect", "Disables all safety checks"},
		{"files", "File safety", "Accidental deletion or damage is allowed"},
		{"injection", "Prompt injection", "Untrusted instructions can execute"},
		{"commands", "Commands", "Unintended command execution is allowed"},
	} {
		value := ui.StringValue(field.value)
		model.AddField(&ui.FieldState{ID: field.id, Label: field.label, Type: ui.FieldText, Value: value, Initial: value, ReadOnly: true})
	}
	s.form = ui.NewForm(FormID, model, "YOLO mode", "Are you absolutely sure?", s.tokens)
	s.actions = ui.NewActionBar(ActionsID, s.tokens,
		ui.ActionBarButton{ID: ui.NewID(ActionsID, "enable"), Label: "Enable YOLO", Action: "yolo.enable", Shortcut: "y", Variant: ui.VariantDanger, Enabled: true},
		ui.ActionBarButton{ID: ui.NewID(ActionsID, "cancel"), Label: "Cancel", Action: "yolo.cancel", Shortcut: "n", Variant: ui.VariantSecondary, Enabled: true},
	)
	s.actions.SetCancelAction("yolo.cancel")
}

func (s *Screen) Children() []ui.Component { return []ui.Component{s.actions} }
func (s *Screen) SetTokens(tokens *ui.TokenSet) {
	s.tokens = tokens
	s.form.SetTokens(tokens)
	s.actions.SetTokens(tokens)
}
func (s *Screen) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
	return cs.ClampSize(ui.Size{W: cs.MaxW, H: s.PreferredHeight(cs.MaxH)})
}
func (s *Screen) PreferredHeight(maxHeight int) int {
	return min(maxHeight, s.form.PreferredHeight(max(0, maxHeight-2))+2)
}
func (s *Screen) Layout(ctx ui.LayoutContext, r ui.Rect) {
	s.SetBounds(r)
	s.form.Layout(ctx, ui.Rect{W: r.W, H: max(1, r.H-2)})
	s.actions.Layout(ctx, ui.Rect{Y: max(0, r.H-1), W: r.W, H: 1})
}
func (s *Screen) Paint(buf *surface.CellBuffer, originX, originY int, ctx ui.PaintContext) {
	r := s.Bounds()
	ui.PaintTree(s.form, buf, originX+r.X, originY+r.Y, ctx)
}
func (s *Screen) Handle(_ ui.EventContext, msg tea.Msg) ui.EventResult {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return ui.EventResult{}
	}
	switch {
	case key.Code == tea.KeyEsc || key.String() == "n":
		return s.result(false)
	case key.String() == "y" || key.Code == tea.KeyEnter:
		return s.result(true)
	case key.Code == tea.KeyLeft || key.Code == tea.KeyRight || key.Code == tea.KeyTab:
		return ui.EventResult{Handled: true, Action: &ui.Action{ID: "ui.focus", Payload: ActionsID}, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	}
	return ui.EventResult{}
}
func (s *Screen) HandleAction(_ ui.EventContext, action ui.Action) ui.EventResult {
	enabled, matched := false, true
	switch action.ID {
	case "yolo.enable":
		enabled = true
	case "yolo.cancel":
	default:
		matched = false
	}
	if matched {
		return s.result(enabled)
	}
	if action.ID == "actionbar.exit.forward" || action.ID == "actionbar.exit.backward" {
		return ui.EventResult{Handled: true, Action: &ui.Action{ID: "ui.focus", Payload: ActionsID}, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	}
	return ui.EventResult{Action: &action}
}
func (s *Screen) result(enabled bool) ui.EventResult {
	return ui.EventResult{Handled: true, Action: &ui.Action{ID: "panel.close"}, Cmd: func() tea.Msg {
		return ResultMsg{Target: s, Enabled: enabled}
	}, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
}
func (s *Screen) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleDialog, Label: "YOLO mode confirmation"}
}

var _ ui.Component = (*Screen)(nil)
var _ ui.Parent = (*Screen)(nil)
var _ ui.ActionHandler = (*Screen)(nil)
