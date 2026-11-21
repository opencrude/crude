// Package confirmation provides the retained tool-confirmation screen.
package confirmation

import (
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
)

// choiceActionPrefix names a Choice as an action-bar action.
const choiceActionPrefix = "confirmation."

const (
	ScreenID  ui.ComponentID = "session-confirmation"
	FormID    ui.ComponentID = "session-confirmation/form"
	ActionsID ui.ComponentID = "session-confirmation/actions"
)

type Details struct {
	Question, Operation, Tool, Target, Risk, Reason, Suggestion string
	// AutoReview tells the user why Auto Review left this request to them.
	AutoReview string
	// OfferAutoReview adds the choice that opens the Auto Review Agent. The
	// owner sets it while Auto Review is off and the request can be reviewed.
	OfferAutoReview   bool
	InvestigationPath bool
	Directory         string
}

// Choice is one answer a tool confirmation offers. The docked screen and the
// modal frame both build their buttons from Choices, so they cannot drift.
type Choice struct {
	ID, Label, Shortcut string
	Variant             ui.ButtonVariant
}

const (
	ChoiceAllow  = "allow"
	ChoiceAlways = "always"
	ChoiceDeny   = "deny"
	// ChoiceAutoReview does not answer the request. It opens the Auto Review
	// Agent, which checks later requests for the user. It is not YOLO: the
	// reviewer still asks or denies when it is not sure.
	ChoiceAutoReview = "auto-review"
)

// Choices lists the answers in display order. Auto Review comes last so the
// three answers keep their positions.
func Choices(offerAutoReview bool) []Choice {
	choices := []Choice{
		{ID: ChoiceAllow, Label: "Allow", Shortcut: "y", Variant: ui.VariantPrimary},
		{ID: ChoiceAlways, Label: "Always allow", Shortcut: "a", Variant: ui.VariantSecondary},
		{ID: ChoiceDeny, Label: "Deny", Shortcut: "n", Variant: ui.VariantDanger},
	}
	if offerAutoReview {
		choices = append(choices, Choice{ID: ChoiceAutoReview, Label: "Auto Review…", Shortcut: "r", Variant: ui.VariantSecondary})
	}
	return choices
}

// OffersAutoReview reports whether a request can use the Auto Review choice:
// Auto Review is off and the request is not hard-blocked, which no reviewer
// may approve.
func OffersAutoReview(autoReviewOn bool, risk string) bool {
	return !autoReviewOn && risk != "blocked"
}

// Choices for host execution do not offer durable workspace permission or auto review.
func (d Details) Choices() []Choice {
	if d.Operation == types.PlanInvestigationHostOperation {
		return []Choice{{ID: types.PlanInvestigationHostAllow, Label: "Allow for this plan", Shortcut: "y", Variant: ui.VariantPrimary}, {ID: ChoiceDeny, Label: "Do not run", Shortcut: "n", Variant: ui.VariantSecondary}}
	}
	choices := Choices(d.OfferAutoReview)
	if d.InvestigationPath {
		return slices.DeleteFunc(choices, func(c Choice) bool { return c.ID == ChoiceAlways })
	}
	return choices
}

func (d Details) operationLabel() string {
	if d.Operation == types.PlanInvestigationHostOperation {
		return ""
	}
	return d.Operation
}

// HostDetails puts the permission warning before the command and folder. The
// modal and dock show the same scope and decision-critical details.
func (d Details) HostDetails() string {
	var lines []string
	for _, field := range []struct{ label, value string }{
		{"", d.Reason}, {"Command: ", d.Target}, {"Folder: ", d.Directory}, {"", d.Suggestion},
	} {
		if field.value != "" {
			lines = append(lines, field.label+field.value)
		}
	}
	return strings.Join(lines, "\n")
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
	hostConsent := details.Operation == types.PlanInvestigationHostOperation
	if hostConsent {
		s.form = ui.NewDockedReadingForm(FormID, "Confirmation required", details.Question+"\n"+details.HostDetails(), s.tokens)
	} else {
		model := ui.NewFormModel()
		for _, field := range []struct{ id, label, value string }{
			{"risk", "Risk", riskLabel(details.Risk)}, {"operation", "Operation", details.operationLabel()},
			{"tool", "Tool", details.Tool}, {"target", "Target", details.Target},
			{"folder", "Folder", details.Directory}, {"reason", "Reason", details.Reason}, {"suggestion", "Suggestion", details.Suggestion},
			{"auto-review", "Auto Review", details.AutoReview},
		} {
			if field.value == "" {
				continue
			}
			value := ui.StringValue(field.value)
			state := &ui.FieldState{ID: field.id, Label: field.label, Type: ui.FieldText, Value: value, Initial: value, ReadOnly: true}
			if lines := strings.Count(field.value, "\n") + 1; lines > 1 {
				// Exact operation arguments arrive one "path: value" per line.
				state = ui.TextAreaFieldState(field.id, field.label, field.value, "")
				state.ReadOnly, state.Rows = true, lines
			}
			model.AddField(state)
		}
		s.form = ui.NewDockedForm(FormID, model, "Confirmation required", details.Question, s.tokens)
	}
	var buttons []ui.ActionBarButton
	for _, choice := range details.Choices() {
		action := choiceActionPrefix + choice.ID
		buttons = append(buttons, ui.ActionBarButton{ID: ui.NewID(ActionsID, choice.ID), Label: choice.Label,
			Action: action, Shortcut: choice.Shortcut, Variant: choice.Variant, Enabled: true})
		if hostConsent {
			s.form.SetChordAction(choice.Shortcut, action)
		}
	}
	s.actions = ui.NewDockedActionBar(ActionsID, s.tokens, buttons...)
	s.actions.SetCancelAction("confirmation.cancel")
	if hostConsent {
		s.actions.SetWrap(true)
		s.actions.FocusLast()
	}
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
	switch key.Code {
	case tea.KeyEsc:
		return s.cancel()
	case tea.KeyEnter:
		if s.details.Operation == types.PlanInvestigationHostOperation {
			return s.result(ChoiceDeny)
		}
		return s.result(ChoiceAllow)
	case tea.KeyLeft, tea.KeyRight, tea.KeyTab:
		return ui.EventResult{Handled: true, Action: &ui.Action{ID: "ui.focus", Payload: ActionsID}, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	}
	for _, choice := range s.details.Choices() {
		if key.String() == choice.Shortcut {
			return s.result(choice.ID)
		}
	}
	return ui.EventResult{}
}
func (s *Screen) HandleAction(_ ui.EventContext, action ui.Action) ui.EventResult {
	if action.ID == "confirmation.cancel" || action.ID == "form.cancel" {
		return s.cancel()
	}
	if choice, ok := strings.CutPrefix(action.ID, choiceActionPrefix); ok {
		for _, offered := range s.details.Choices() {
			if offered.ID == choice {
				return s.result(choice)
			}
		}
	}
	if action.ID == "form.exit.forward" || action.ID == "form.exit.backward" {
		if action.ID == "form.exit.backward" || s.details.Operation == types.PlanInvestigationHostOperation {
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
	result := ui.EventResult{Handled: true, Cmd: func() tea.Msg {
		return ResultMsg{Target: s, Choice: choice}
	}, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
	if choice != ChoiceAutoReview {
		// Auto Review answers nothing, so the request stays on screen.
		result.Action = &ui.Action{ID: "panel.close"}
	}
	return result
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
