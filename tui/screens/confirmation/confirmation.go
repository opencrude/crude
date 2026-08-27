// Package confirmation provides the retained tool-confirmation screen.
package confirmation

import (
	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

const (
	ScreenID  ui.ComponentID = "session-confirmation"
	FormID    ui.ComponentID = "session-confirmation/form"
	ActionsID ui.ComponentID = "session-confirmation/actions"
)

type Details struct {
	Question, Operation, Tool, Target, Risk, Reason, Suggestion string
}

type ResultMsg struct {
	Target *Screen
	Choice string
}

// CancelMsg is distinct from ResultMsg: Escape stops the active turn without
// representing the stop as an explicit permission denial.
type CancelMsg struct{ Target *Screen }

type Screen struct {
	ui.Base
	tokens  *ui.TokenSet
	details Details
	form    *ui.Form
	actions *ui.ActionBar
}

func New(th *theme.Theme) *Screen {
	if th == nil {
		th = theme.DefaultDark()
	}
	s := &Screen{Base: ui.NewBase(ScreenID), tokens: ui.NewTokenSet(th)}
	s.Reset(Details{})
	return s
}

func (s *Screen) Reset(details Details) {
	s.details = details
	model := ui.NewFormModel()
	for _, field := range []struct{ id, label, value string }{
		{"risk", "Risk", riskLabel(details.Risk)}, {"operation", "Operation", details.Operation},
		{"tool", "Tool", details.Tool}, {"target", "Target", details.Target},
		{"reason", "Reason", details.Reason}, {"suggestion", "Suggestion", details.Suggestion},
	} {
		if field.value == "" {
			continue
		}
		value := ui.StringValue(field.value)
		model.AddField(&ui.FieldState{ID: field.id, Label: field.label, Type: ui.FieldText, Value: value, Initial: value, ReadOnly: true})
	}
	s.form = ui.NewDockedForm(FormID, model, "Confirmation required", details.Question, s.tokens)
	s.actions = ui.NewDockedActionBar(ActionsID, s.tokens,
		ui.ActionBarButton{ID: ui.NewID(ActionsID, "allow"), Label: "Allow", Action: "confirmation.allow", Shortcut: "y", Variant: ui.VariantPrimary, Enabled: true},
		ui.ActionBarButton{ID: ui.NewID(ActionsID, "always"), Label: "Always allow", Action: "confirmation.always", Shortcut: "a", Variant: ui.VariantSecondary, Enabled: true},
		ui.ActionBarButton{ID: ui.NewID(ActionsID, "deny"), Label: "Deny", Action: "confirmation.deny", Shortcut: "n", Variant: ui.VariantDanger, Enabled: true},
	)
	s.actions.SetCancelAction("confirmation.cancel")
}

func (s *Screen) Dismiss()                 { s.Reset(Details{}) }
func (s *Screen) Details() Details         { return s.details }
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
	case key.Code == tea.KeyEsc:
		return s.cancel()
	case key.String() == "n":
		return s.result("deny")
	case key.String() == "y":
		return s.result("allow")
	case key.String() == "a":
		return s.result("always")
	case key.Code == tea.KeyEnter:
		return s.result("allow")
	case key.Code == tea.KeyLeft || key.Code == tea.KeyRight || key.Code == tea.KeyTab:
		return ui.EventResult{Handled: true, Action: &ui.Action{ID: "ui.focus", Payload: ActionsID}, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	}
	return ui.EventResult{}
}
func (s *Screen) HandleAction(_ ui.EventContext, action ui.Action) ui.EventResult {
	if action.ID == "confirmation.cancel" || action.ID == "form.cancel" {
		return s.cancel()
	}
	choice := map[string]string{"confirmation.allow": "allow", "confirmation.always": "always", "confirmation.deny": "deny"}[action.ID]
	if choice != "" {
		return s.result(choice)
	}
	if action.ID == "form.exit.forward" || action.ID == "form.exit.backward" {
		if action.ID == "form.exit.backward" {
			s.actions.FocusLast()
		} else {
			s.actions.FocusFirst()
		}
		return ui.EventResult{Handled: true, Action: &ui.Action{ID: "ui.focus", Payload: ActionsID}, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
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
func (s *Screen) result(choice string) ui.EventResult {
	return ui.EventResult{Handled: true, Action: &ui.Action{ID: "panel.close"}, Cmd: func() tea.Msg {
		return ResultMsg{Target: s, Choice: choice}
	}, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
}
func (s *Screen) cancel() ui.EventResult {
	return ui.EventResult{Handled: true, Action: &ui.Action{ID: "panel.close"}, Cmd: func() tea.Msg {
		return CancelMsg{Target: s}
	}, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
}
func (s *Screen) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleDialog, Label: "Tool confirmation"}
}
func riskLabel(risk string) string {
	if risk == "blocked" {
		return "BLOCKED"
	}
	if risk == "destructive" {
		return "DANGEROUS"
	}
	if risk == "project-write" {
		return "WRITE"
	}
	return risk
}

var _ ui.Component = (*Screen)(nil)
var _ ui.Parent = (*Screen)(nil)
var _ ui.ActionHandler = (*Screen)(nil)
