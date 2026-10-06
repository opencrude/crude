// Package operationissue renders actionable operational failures without
// replacing the underlying launcher, editor, or saved conversation.
package operationissue

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"crude/tui/rendering"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
)

const ScreenID ui.ComponentID = "operation-issue/body"
const ActionsID ui.ComponentID = "operation-issue/actions"
const DetailID ui.ComponentID = "operation-issue/detail"

type ActionMsg struct {
	Screen *Screen
	Action string
}

type Screen struct {
	ui.Base
	issue   types.OperationIssue
	summary *ui.Label
	detail  *ui.TextArea
	actions *ui.ActionBar
}

func New(th *theme.Theme, issue types.OperationIssue, repairLabel string) *Screen {
	tokens := ui.NewTokenSet(th)
	s := &Screen{Base: ui.NewBase(ScreenID), issue: issue}
	s.summary = ui.NewLabel("operation-issue/summary", issue.Summary, th)
	s.summary.SetColorRole(ui.ColorFg)
	s.detail = ui.NewTextArea(DetailID, tokens, true)
	s.detail.SetReadOnly(true)
	parts := []string{issue.Detail, issue.Guidance}
	if issue.Path != "" {
		parts = append(parts, "Location: "+issue.Path)
	}
	s.detail.SetValue(strings.Join(parts, "\n\n"))
	s.detail.SetCursorFlat(0)
	s.actions = ui.NewActionBar(ActionsID, tokens,
		ui.ActionBarButton{Label: repairLabel, Action: "repair", Enabled: true, Variant: ui.VariantPrimary},
		ui.ActionBarButton{Label: "Close", Action: "close", Enabled: true, Variant: ui.VariantSecondary},
	)
	s.actions.SetCancelAction("close")
	s.actions.FocusLast()
	return s
}

func (s *Screen) Issue() types.OperationIssue { return s.issue }

// PreferredHeight keeps ordinary failures compact; long evidence remains
// complete in the retained scrollable detail area on constrained terminals.
func (s *Screen) PreferredHeight(contentWidth int) int {
	w := max(1, contentWidth)
	return len(rendering.WrapText(s.issue.Summary, w)) + len(rendering.WrapText(s.detail.Value(), w)) + 5
}
func (s *Screen) Children() []ui.Component { return []ui.Component{s.summary, s.detail, s.actions} }
func (s *Screen) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
	return cs.ClampSize(ui.Size{W: cs.MaxW, H: cs.MaxH})
}
func (s *Screen) Layout(ctx ui.LayoutContext, r ui.Rect) {
	s.SetBounds(r)
	rows := rendering.WrapText(s.issue.Summary, max(1, r.W))
	s.summary.SetText(strings.Join(rows, "\n"))
	sh := min(len(rows), max(0, r.H-3))
	s.summary.Layout(ctx, ui.Rect{W: r.W, H: sh})
	y := min(sh+1, r.H)
	s.detail.Layout(ctx, ui.Rect{Y: y, W: r.W, H: max(0, r.H-y-2)})
	s.actions.Layout(ctx, ui.Rect{Y: max(0, r.H-1), W: r.W, H: min(1, r.H)})
}
func (s *Screen) Handle(ctx ui.EventContext, event ui.Event) ui.EventResult {
	if event.IsKey() {
		if event.Key.Code == tea.KeyEsc {
			return s.HandleAction(ctx, ui.Action{ID: "close"})
		}
		return s.actions.Handle(ctx, event)
	}
	return ui.EventResult{}
}
func (s *Screen) HandleAction(_ ui.EventContext, action ui.Action) ui.EventResult {
	if action.ID == "repair" || action.ID == "close" {
		return ui.EventResult{Handled: true, Cmd: func() tea.Msg { return ActionMsg{Screen: s, Action: action.ID} }}
	}
	return ui.EventResult{}
}
func (s *Screen) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleDialog, Label: s.issue.Title}
}
