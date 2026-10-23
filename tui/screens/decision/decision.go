// Package decision provides the retained docked decision a session asks
// before a consequential mode change (enable YOLO, implement a proposed plan).
package decision

import (
	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

const (
	ScreenID  ui.ComponentID = "session-decision"
	FormID    ui.ComponentID = "session-decision/form"
	ActionsID ui.ComponentID = "session-decision/actions"
)

// Spec is one decision: what is asked, the facts that inform it and its
// answers. Confirm, and the optional Alternate way to confirm, run only on an
// explicit choice; Enter on the default focus, Esc and the cancel shortcut all
// decline.
type Spec struct {
	Purpose        string
	Title, Hint    string
	Facts          []Fact
	Confirm        Answer
	Alternate      *Answer
	Cancel         Answer
	ConfirmDanger  bool
	SemanticsLabel string
}

type Fact struct{ Label, Value string }

type Answer struct{ Label, Shortcut string }

// ResultMsg is the answer. Alternate is set with Confirmed when the user chose
// the alternate way to confirm.
type ResultMsg struct {
	Target    *Screen
	Purpose   string
	Confirmed bool
	Alternate bool
}

type Screen struct {
	ui.Base
	tokens  *ui.TokenSet
	spec    Spec
	form    *ui.Form
	actions *ui.ActionBar
}

func New(th *theme.Theme) *Screen {
	if th == nil {
		th = theme.DefaultDark()
	}
	s := &Screen{Base: ui.NewBase(ScreenID), tokens: ui.NewTokenSet(th)}
	s.Show(Spec{Confirm: Answer{Label: "Yes", Shortcut: "y"}, Cancel: Answer{Label: "No", Shortcut: "n"}})
	return s
}

// Show replaces the decision; focus starts on the declining answer.
func (s *Screen) Show(spec Spec) {
	s.spec = spec
	model := ui.NewFormModel()
	for index, fact := range spec.Facts {
		value := ui.StringValue(fact.Value)
		model.AddField(&ui.FieldState{ID: string(rune('a' + index)), Label: fact.Label, Type: ui.FieldText, Value: value, Initial: value, ReadOnly: true})
	}
	s.form = ui.NewDockedForm(FormID, model, spec.Title, spec.Hint, s.tokens)
	variant := ui.VariantPrimary
	if spec.ConfirmDanger {
		variant = ui.VariantDanger
	}
	buttons := []ui.ActionBarButton{{ID: ui.NewID(ActionsID, "confirm"), Label: spec.Confirm.Label, Action: "decision.confirm", Shortcut: spec.Confirm.Shortcut, Variant: variant, Enabled: true}}
	if spec.Alternate != nil {
		buttons = append(buttons, ui.ActionBarButton{ID: ui.NewID(ActionsID, "alternate"), Label: spec.Alternate.Label, Action: "decision.alternate", Shortcut: spec.Alternate.Shortcut, Variant: ui.VariantSecondary, Enabled: true})
	}
	buttons = append(buttons, ui.ActionBarButton{ID: ui.NewID(ActionsID, "cancel"), Label: spec.Cancel.Label, Action: "decision.cancel", Shortcut: spec.Cancel.Shortcut, Variant: ui.VariantSecondary, Enabled: true})
	s.actions = ui.NewDockedActionBar(ActionsID, s.tokens, buttons...)
	s.actions.SetCancelAction("decision.cancel")
	s.actions.FocusLast()
}

func (s *Screen) Purpose() string { return s.spec.Purpose }

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
	case key.Code == tea.KeyEsc || key.String() == s.spec.Cancel.Shortcut:
		return s.result(false)
	case key.String() == s.spec.Confirm.Shortcut:
		return s.result(true)
	case s.spec.Alternate != nil && key.String() == s.spec.Alternate.Shortcut:
		return s.resultAlternate()
	case key.Code == tea.KeyEnter:
		return s.result(false)
	case key.Code == tea.KeyLeft || key.Code == tea.KeyRight || key.Code == tea.KeyTab:
		return ui.EventResult{Handled: true, Action: &ui.Action{ID: "ui.focus", Payload: ActionsID}, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	}
	return ui.EventResult{}
}
func (s *Screen) HandleAction(_ ui.EventContext, action ui.Action) ui.EventResult {
	switch action.ID {
	case "decision.confirm":
		return s.result(true)
	case "decision.alternate":
		return s.resultAlternate()
	case "decision.cancel", "form.cancel":
		return s.result(false)
	case "form.exit.forward", "form.exit.backward":
		s.actions.FocusLast()
		return ui.EventResult{Handled: true, Action: &ui.Action{ID: "ui.focus", Payload: ActionsID}, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	case "actionbar.exit.forward", "actionbar.exit.backward":
		if action.ID == "actionbar.exit.backward" {
			s.form.FocusLast()
		} else {
			s.form.FocusFirst()
		}
		return ui.EventResult{Handled: true, Action: &ui.Action{ID: "ui.focus", Payload: FormID}, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	}
	return ui.EventResult{Action: &action}
}
func (s *Screen) result(confirmed bool) ui.EventResult {
	return s.answer(ResultMsg{Target: s, Purpose: s.spec.Purpose, Confirmed: confirmed})
}

func (s *Screen) resultAlternate() ui.EventResult {
	return s.answer(ResultMsg{Target: s, Purpose: s.spec.Purpose, Confirmed: true, Alternate: true})
}

func (s *Screen) answer(msg ResultMsg) ui.EventResult {
	return ui.EventResult{Handled: true, Action: &ui.Action{ID: "panel.close"}, Cmd: func() tea.Msg { return msg },
		Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
}
func (s *Screen) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleDialog, Label: s.spec.SemanticsLabel}
}

var _ ui.Component = (*Screen)(nil)
var _ ui.Parent = (*Screen)(nil)
var _ ui.ActionHandler = (*Screen)(nil)
