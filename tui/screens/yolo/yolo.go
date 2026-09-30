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
		{"approvals", "Approval prompts", "Skipped"},
		{"execution", "Commands / edits", "No confirmation"},
		{"blocks", "Hard blocks", "Enforced"},
		{"history", "Restore safeguards", "Enforced"},
	} {
		value := ui.StringValue(field.value)
		model.AddField(&ui.FieldState{ID: field.id, Label: field.label, Type: ui.FieldText, Value: value, Initial: value, ReadOnly: true})
	}
	s.form = ui.NewDockedForm(FormID, model, "Enable YOLO for this session?", "Use /yolo to restore approvals.", s.tokens)
	s.actions = ui.NewDockedActionBar(ActionsID, s.tokens,
		ui.ActionBarButton{ID: ui.NewID(ActionsID, "enable"), Label: "Enable YOLO", Action: "yolo.enable", Shortcut: "y", Variant: ui.VariantDanger, Enabled: true},
		ui.ActionBarButton{ID: ui.NewID(ActionsID, "cancel"), Label: "Cancel", Action: "yolo.cancel", Shortcut: "n", Variant: ui.VariantSecondary, Enabled: true},
	)
	s.actions.SetCancelAction("yolo.cancel")
	s.actions.FocusLast()
}

func (s *Screen) Children() []ui.Component { return []ui.Component{s.form, s.actions} }
func (s *Screen) SetTokens(tokens *ui.TokenSet) {
	s.tokens = tokens
	s.form.SetTokens(tokens)
	s.actions.SetTokens(tokens)
}

func (s *Screen) SetSurfaceBackground(role ui.SemanticColor) {
	if s.form != nil {
		s.form.SetSurfaceBackground(role)
	}
}
func (s *Screen) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
	return cs.ClampSize(ui.Size{W: cs.MaxW, H: s.PreferredHeight(cs.MaxH)})
}
func (s *Screen) PreferredHeight(maxHeight int) int {
	chrome := s.actions.PreferredHeight()
	return min(maxHeight, s.form.PreferredHeight(max(0, maxHeight-chrome))+chrome)
}
func (s *Screen) Layout(ctx ui.LayoutContext, r ui.Rect) {
	s.SetBounds(r)
	chrome := s.actions.PreferredHeight()
	s.form.Layout(ctx, ui.Rect{W: r.W, H: max(1, r.H-chrome)})
	s.actions.Layout(ctx, ui.Rect{Y: max(0, r.H-chrome), W: r.W, H: chrome})
}
func (s *Screen) Paint(_ *surface.CellBuffer, _, _ int, _ ui.PaintContext) {}
func (s *Screen) Handle(_ ui.EventContext, evt ui.Event) ui.EventResult {
	if !evt.IsKey() {
		return ui.EventResult{}
	}
	key := evt.Key
	switch {
	case key.Code == tea.KeyEsc || key.String() == "n":
		return s.result(false)
	case key.String() == "y":
		return s.result(true)
	case key.Code == tea.KeyEnter:
		return s.result(false)
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
	if action.ID == "form.exit.forward" || action.ID == "form.exit.backward" {
		s.actions.FocusLast()
		return ui.EventResult{Handled: true, Action: &ui.Action{ID: "ui.focus", Payload: ActionsID}, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	}
	if action.ID == "form.cancel" {
		return s.result(false)
	}
	if action.ID == "actionbar.exit.forward" || action.ID == "actionbar.exit.backward" {
		if action.ID == "actionbar.exit.backward" {
			s.form.FocusLast()
		} else {
			s.form.FocusFirst()
		}
		return ui.EventResult{Handled: true, Action: &ui.Action{ID: "ui.focus", Payload: FormID}, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
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
