// Package toolsetup presents a native project-tool preview. It owns selection,
// not downloads, project trust or installation authority.
package toolsetup

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
)

const (
	ScreenID      ui.ComponentID = "project-tools"
	FormID        ui.ComponentID = "project-tools/form"
	ActionsID     ui.ComponentID = "project-tools/actions"
	ActionInstall                = "project-tools.install"
	ActionDecline                = "project-tools.decline"
	ActionClose                  = "project-tools.close"
)

type DecisionMsg struct {
	Target   *Screen
	Selected []string
}

type CloseMsg struct{ Target *Screen }

type Screen struct {
	ui.Base
	model     *ui.FormModel
	form      *ui.Form
	scrollbar *ui.ScrollbarControl
	actions   *ui.ActionBar
	items     []types.ProjectToolCandidate
}

func New(th *theme.Theme, review types.ProjectToolReview) *Screen {
	if th == nil {
		th = theme.DefaultDark()
	}
	tokens := ui.NewTokenSet(th)
	s := &Screen{Base: ui.NewBase(ScreenID), model: ui.NewFormModel()}
	intro := ui.TextAreaFieldState("about", "Project", review.Project+
		"\nAeron sets up the tools you leave on. It works in the background; open it in the Agents panel to watch. Editing works without these tools.", "")
	intro.ReadOnly, intro.Rows = true, 4
	s.model.AddField(intro)
	for _, item := range review.Items {
		s.items = append(s.items, types.CloneProjectToolCandidate(item))
		label := item.Package
		if label == "" {
			label = item.Command
		}
		selected := ui.BoolFieldState(item.ID, label, true, itemDescription(item))
		s.model.AddField(selected)
	}
	s.form = ui.NewForm(FormID, s.model, "", "", tokens)
	s.form.SetFieldGap(1)
	s.form.SetArrowExitsForm(true)
	s.scrollbar = ui.NewScrollbarControl("project-tools/scroll", s.form.Scroll(), tokens)
	s.scrollbar.SetSurfaceBackground(ui.ColorPopupBg)
	s.actions = ui.NewActionBar(ActionsID, tokens,
		ui.ActionBarButton{Label: "Set up with Aeron", Action: ActionInstall, Enabled: len(s.SelectedIDs()) > 0, Variant: ui.VariantPrimary},
		ui.ActionBarButton{Label: "Not now", Action: ActionDecline, Enabled: true, Variant: ui.VariantSecondary})
	s.actions.SetAlignEnd(false)
	s.actions.SetShowRule(true)
	s.actions.SetCancelAction(ActionClose)
	s.actions.FocusLast()
	s.model.OnChanged(func(string) {
		s.actions.SetEnabled(ActionInstall, len(s.SelectedIDs()) > 0)
	})
	return s
}

// itemDescription is the one line under a switch: what the tool serves and
// where it comes from, or why Crude cannot fetch it directly.
func itemDescription(item types.ProjectToolCandidate) string {
	parts := []string{}
	if len(item.Languages) > 0 {
		parts = append(parts, strings.Join(item.Languages, ", "))
	}
	if item.Version != "" {
		parts = append(parts, "version "+item.Version)
	}
	switch {
	case item.Reason != "":
		parts = append(parts, item.Reason)
	case item.Source != "":
		parts = append(parts, "from "+item.Source)
	}
	return strings.Join(parts, " · ")
}

// SelectedIDs lists the tools whose switch is on. Aeron decides how to get
// each one, so every tool is a real choice, whether or not Crude can download
// it itself.
func (s *Screen) SelectedIDs() []string {
	var selected []string
	for _, item := range s.items {
		if ui.BoolFieldValue(s.model.Field(item.ID)) {
			selected = append(selected, item.ID)
		}
	}
	return selected
}

// Items returns the tools this screen offers.
func (s *Screen) Items() []types.ProjectToolCandidate {
	items := make([]types.ProjectToolCandidate, len(s.items))
	for i, item := range s.items {
		items[i] = types.CloneProjectToolCandidate(item)
	}
	return items
}

func (s *Screen) Children() []ui.Component {
	return []ui.Component{s.form, s.scrollbar, s.actions}
}

func (s *Screen) SetTokens(tokens *ui.TokenSet) {
	s.form.SetTokens(tokens)
	s.scrollbar.SetTokens(tokens)
	s.actions.SetTokens(tokens)
}

func (s *Screen) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
	return cs.ClampSize(ui.Size{W: 92, H: 24})
}

func (s *Screen) Layout(ctx ui.LayoutContext, r ui.Rect) {
	s.SetBounds(r)
	w, h := max(0, r.W), max(0, r.H)
	barH := min(h, s.actions.PreferredHeight())
	bodyH := h - barH
	scrollW := 0
	if w > 0 && s.scrollbar.NeededForViewport(bodyH) {
		scrollW = 1
	}
	s.form.Layout(ctx, ui.Rect{W: w - scrollW, H: bodyH})
	s.scrollbar.Layout(ctx, ui.Rect{X: w - scrollW, W: scrollW, H: bodyH})
	s.actions.Layout(ctx, ui.Rect{Y: bodyH, W: w, H: barH})
}

func (*Screen) Paint(*surface.CellBuffer, int, int, ui.PaintContext) {}

func (s *Screen) Handle(ctx ui.EventContext, event ui.Event) ui.EventResult {
	if event.IsKey() && event.Key.Code == tea.KeyEsc {
		return s.HandleAction(ctx, ui.Action{ID: ActionClose})
	}
	return ui.EventResult{}
}

func (s *Screen) HandleAction(_ ui.EventContext, action ui.Action) ui.EventResult {
	focus := func(id ui.ComponentID) ui.EventResult {
		return ui.EventResult{Handled: true, Action: &ui.Action{ID: "ui.focus", Payload: id},
			Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
	}
	switch action.ID {
	case ActionInstall:
		if selected := s.SelectedIDs(); len(selected) > 0 {
			return ui.EventResult{Handled: true, Cmd: func() tea.Msg { return DecisionMsg{s, selected} }}
		}
		return ui.EventResult{Handled: true}
	case ActionDecline:
		return ui.EventResult{Handled: true, Cmd: func() tea.Msg { return DecisionMsg{Target: s} }}
	case ActionClose, "form.cancel":
		return ui.EventResult{Handled: true, Cmd: func() tea.Msg { return CloseMsg{s} }}
	case "form.exit.forward", "form.exit.backward":
		return focus(ActionsID)
	case "actionbar.exit.forward", "actionbar.exit.backward":
		s.form.FocusFirst()
		return focus(FormID)
	}
	return ui.EventResult{}
}
