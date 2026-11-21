// Package decision provides the retained docked decision a session asks
// before a consequential mode change (enable YOLO, implement a proposed plan).
package decision

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

// alternateAction prefixes the action of each alternate answer's button.
const alternateAction = "decision.alternate:"

const (
	ScreenID  ui.ComponentID = "session-decision"
	FormID    ui.ComponentID = "session-decision/form"
	ActionsID ui.ComponentID = "session-decision/actions"
)

// Spec is one decision: what is asked, the facts that inform it and its
// answers. Confirm, and the optional Alternates (other ways to accept), run
// only on an explicit choice; Enter on the default focus, Esc and the cancel
// shortcut all decline.
type Spec struct {
	Purpose        string
	Title, Hint    string
	Facts          []Fact
	Confirm        Answer
	Alternates     []Answer
	Cancel         Answer
	ConfirmDanger  bool
	ReadingLayout  bool
	SemanticsLabel string
}

type Fact struct{ Label, Value string }

// Answer is one button. ID names an alternate answer in ResultMsg.Choice. An
// alternate that Stays acts without settling the decision, which stays open
// (for example, Review opens the plan and the question remains).
type Answer struct {
	ID, Label, Shortcut string
	Stay                bool
}

// ResultMsg is the answer. Choice is the ID of the alternate answer the user
// chose, with Confirmed set; it is empty for Confirm and Cancel. Stay reports
// that the decision is still open.
type ResultMsg struct {
	Target    *Screen
	Purpose   string
	Confirmed bool
	Choice    string
	Stay      bool
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
	if spec.ReadingLayout {
		lines := make([]string, 0, len(spec.Facts)+1)
		for _, fact := range spec.Facts {
			lines = append(lines, fact.Label+": "+fact.Value)
		}
		if spec.Hint != "" {
			lines = append(lines, spec.Hint)
		}
		s.form = ui.NewDockedReadingForm(FormID, spec.Title, strings.Join(lines, "\n"), s.tokens)
	} else {
		model := ui.NewFormModel()
		for index, fact := range spec.Facts {
			value := ui.StringValue(fact.Value)
			model.AddField(&ui.FieldState{ID: string(rune('a' + index)), Label: fact.Label, Type: ui.FieldText, Value: value, Initial: value, ReadOnly: true})
		}
		s.form = ui.NewDockedForm(FormID, model, spec.Title, spec.Hint, s.tokens)
	}
	variant := ui.VariantPrimary
	if spec.ConfirmDanger {
		variant = ui.VariantDanger
	}
	buttons := []ui.ActionBarButton{{ID: ui.NewID(ActionsID, "confirm"), Label: spec.Confirm.Label, Action: "decision.confirm", Shortcut: spec.Confirm.Shortcut, Variant: variant, Enabled: true}}
	for _, alternate := range spec.Alternates {
		buttons = append(buttons, ui.ActionBarButton{ID: ui.NewID(ActionsID, "alternate-"+alternate.ID), Label: alternate.Label, Action: alternateAction + alternate.ID, Shortcut: alternate.Shortcut, Variant: ui.VariantSecondary, Enabled: true})
	}
	buttons = append(buttons, ui.ActionBarButton{ID: ui.NewID(ActionsID, "cancel"), Label: spec.Cancel.Label, Action: "decision.cancel", Shortcut: spec.Cancel.Shortcut, Variant: ui.VariantSecondary, Enabled: true})
	s.actions = ui.NewDockedActionBar(ActionsID, s.tokens, buttons...)
	s.actions.SetCancelAction("decision.cancel")
	s.actions.FocusLast()
	if spec.ReadingLayout {
		s.actions.SetWrap(true)
		for _, button := range buttons {
			if button.Shortcut != "" {
				s.form.SetChordAction(button.Shortcut, button.Action)
			}
		}
	}
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
	return cs.ClampSize(ui.Size{W: cs.MaxW, H: s.PreferredHeight(cs.MaxW, cs.MaxH)})
}

var _ ui.PreferredHeightComponent = (*Screen)(nil)

func (s *Screen) PreferredHeight(width, maxHeight int) int {
	chrome := s.actions.PreferredHeightAtWidth(width)
	return min(maxHeight, s.form.PreferredHeightAtWidth(width, max(0, maxHeight-chrome))+chrome)
}
func (s *Screen) Layout(ctx ui.LayoutContext, r ui.Rect) {
	s.SetBounds(r)
	chrome := min(r.H, s.actions.PreferredHeightAtWidth(r.W))
	s.form.Layout(ctx, ui.Rect{W: r.W, H: r.H - chrome})
	s.actions.Layout(ctx, ui.Rect{Y: r.H - chrome, W: r.W, H: chrome})
}
func (s *Screen) Paint(_ *surface.CellBuffer, _, _ int, _ ui.PaintContext) {}
func (s *Screen) Handle(_ ui.EventContext, evt ui.Event) ui.EventResult {
	if !evt.IsKey() {
		return ui.EventResult{}
	}
	key := evt.Key
	if page := s.form.ReadPage(key); page.Handled {
		return page
	}
	switch {
	case key.Code == tea.KeyEsc || key.String() == s.spec.Cancel.Shortcut:
		return s.result(false)
	case key.String() == s.spec.Confirm.Shortcut:
		return s.result(true)
	case s.alternate(key.String()) != "":
		return s.resultAlternate(s.alternate(key.String()))
	case key.Code == tea.KeyEnter:
		return s.result(false)
	case key.Code == tea.KeyLeft || key.Code == tea.KeyRight || key.Code == tea.KeyTab:
		return ui.EventResult{Handled: true, Action: &ui.Action{ID: "ui.focus", Payload: ActionsID}, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	}
	return ui.EventResult{}
}
func (s *Screen) HandleAction(_ ui.EventContext, action ui.Action) ui.EventResult {
	if id, ok := strings.CutPrefix(string(action.ID), alternateAction); ok {
		if s.hasAlternate(id) {
			return s.resultAlternate(id)
		}
		return ui.EventResult{Handled: true}
	}
	switch action.ID {
	case "decision.confirm":
		return s.result(true)
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

func (s *Screen) resultAlternate(id string) ui.EventResult {
	msg := ResultMsg{Target: s, Purpose: s.spec.Purpose, Confirmed: true, Choice: id}
	for _, alternate := range s.spec.Alternates {
		if alternate.ID == id && alternate.Stay {
			msg.Stay = true
			return ui.EventResult{Handled: true, Cmd: func() tea.Msg { return msg }, Invalidate: ui.InvalidatePaint}
		}
	}
	return s.answer(msg)
}

// alternate returns the ID of the alternate answer whose shortcut is key.
func (s *Screen) alternate(key string) string {
	for _, alternate := range s.spec.Alternates {
		if alternate.Shortcut != "" && alternate.Shortcut == key {
			return alternate.ID
		}
	}
	return ""
}

func (s *Screen) hasAlternate(id string) bool {
	for _, alternate := range s.spec.Alternates {
		if alternate.ID == id {
			return true
		}
	}
	return false
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
