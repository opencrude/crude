// Package codeactions owns the read-only manual action chooser. The canonical
// workspace owns raw provider evidence, lazy resolution and mutation permits.
package codeactions

import (
	"fmt"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/workspace"
)

const (
	ScreenID      ui.ComponentID = "code-actions"
	ListID        ui.ComponentID = "code-actions/list"
	TextID        ui.ComponentID = "code-actions/detail"
	ActionsID     ui.ComponentID = "code-actions/actions"
	ActionPrepare                = "code-actions.prepare"
	ActionBack                   = "code-actions.back"
	ActionClose                  = "code-actions.close"
)

type PrepareMsg struct {
	Target *Screen
	Index  int
}

type CloseMsg struct{ Target *Screen }

// Screen is a focused body, not an RPC or file owner. Choosing an available
// action requests its review directly. Unavailable rows explain why; they never
// drop unsupported pieces or acquire edit authority.
type Screen struct {
	ui.Base
	tokens    *ui.TokenSet
	list      *ui.List
	scrollbar *ui.ScrollbarControl
	text      *actionText
	actions   *ui.ActionBar
	set       workspace.CodeActionSet
	hasSet    bool
	selected  int
	busy      bool
	failure   string
}

func New(th *theme.Theme) *Screen {
	if th == nil {
		th = theme.DefaultDark()
	}
	ts := ui.NewTokenSet(th)
	s := &Screen{Base: ui.NewBase(ScreenID), tokens: ts, selected: -1}
	s.list = ui.NewList(ListID, ts)
	s.list.SetDefaultRowBackground(ui.ColorPopupBg)
	s.list.SetActivateAction(ActionPrepare)
	s.scrollbar = ui.NewScrollbarControl("code-actions/scrollbar", s.list.Scroll(), ts)
	s.text = &actionText{TextArea: ui.NewTextArea(TextID, ts, true), owner: s}
	s.text.SetReadOnly(true)
	s.actions = ui.NewActionBar(ActionsID, ts)
	s.actions.SetShowRule(true)
	s.actions.SetSurfaceBackground(ui.ColorPopupBg)
	s.actions.SetCancelAction(ActionClose)
	s.SetTokens(ts)
	s.SetLoading()
	return s
}

func (s *Screen) SetTokens(ts *ui.TokenSet) {
	if ts == nil {
		return
	}
	s.tokens = ts
	s.list.SetTokens(ts)
	s.scrollbar.SetTokens(ts)
	s.text.SetTokens(ts)
	s.text.SetBackgroundColor(ts.Color(ui.ColorPopupBg))
	s.actions.SetTokens(ts)
}

func (s *Screen) SetTheme(th *theme.Theme) {
	if th != nil {
		s.SetTokens(ui.NewTokenSet(th))
	}
}

func (s *Screen) SetLoading() {
	s.hasSet, s.selected, s.busy, s.failure = false, -1, true, ""
	s.set = workspace.CodeActionSet{}
	s.setText("Finding code actions…")
	s.rebuildActions()
}

func (s *Screen) SetError(message string) {
	s.busy, s.failure = false, message
	s.setText(message)
	s.rebuildActions()
}

func (s *Screen) SetSet(set workspace.CodeActionSet) {
	total := 0
	for i, row := range set.Items {
		total += len(row.Title) + len(row.Kind) + len(row.DisabledReason) + len(row.UnsupportedReason)
		if i != row.Index || len(row.Title) > 4<<10 || len(row.Kind) > 256 ||
			len(row.DisabledReason) > 4<<10 || len(row.UnsupportedReason) > 4<<10 {
			s.SetError("The language server returned an action list that cannot be shown. Try again.")
			return
		}
	}
	if set.ID == "" || set.Digest == "" || len(set.Items) > 64 || total > 64<<10 {
		s.SetError("The language server returned an action list that cannot be shown. Try again.")
		return
	}
	s.set = set
	s.set.Items = slices.Clone(set.Items)
	s.set.Boundary.Context.UnknownInputs = slices.Clone(set.Boundary.Context.UnknownInputs)
	s.hasSet, s.selected, s.busy, s.failure = true, -1, false, ""
	if len(set.Items) == 0 {
		s.setText("No code actions available here.")
	} else {
		items := make([]ui.ListItem, 0, len(set.Items))
		available := 0
		for _, row := range set.Items {
			label := row.Title
			detail := ""
			switch {
			case !row.CanReview:
				detail = "Not available"
			case row.HasPreferred && row.Preferred:
				detail = "Recommended"
			}
			if row.CanReview {
				available++
			}
			// Disabled/unsupported actions stay selectable for complete detail.
			items = append(items, ui.ListItem{
				ID: fmt.Sprint(row.Index), Label: label,
				Detail: detail, Payload: row.Index,
			})
		}
		rank := func(row ui.ListItem) int {
			item := s.set.Items[row.Payload.(int)]
			if !item.CanReview {
				return 2
			}
			if item.HasPreferred && item.Preferred {
				return 0
			}
			return 1
		}
		slices.SortStableFunc(items, func(a, b ui.ListItem) int { return rank(a) - rank(b) })
		if available == 0 {
			for i := range items {
				items[i].Detail = ""
			}
			items = append([]ui.ListItem{{ID: "unavailable", Label: "Crude cannot apply these actions yet.", Header: true}}, items...)
		}
		s.list.SetItems(items)
	}
	s.rebuildActions()
}

func (s *Screen) SetBusy(busy bool) {
	s.busy = busy
	s.rebuildActions()
}

func (s *Screen) InList() bool {
	return s.hasSet && len(s.set.Items) != 0 && s.selected < 0 && s.failure == ""
}

func (s *Screen) DefaultFocus() ui.ComponentID {
	if s.InList() {
		return ListID
	}
	if s.hasActions() {
		return ActionsID
	}
	return TextID
}

func (s *Screen) hasActions() bool {
	return s.hasSet && (s.selected >= 0 || s.failure != "")
}

func (s *Screen) Title() string {
	if !s.InList() {
		return "Code actions"
	}
	return fmt.Sprintf("Code actions · %d", len(s.set.Items))
}
func (s *Screen) setText(value string) {
	s.text.CancelPointerCapture()
	s.text.SetValue(value)
	s.text.SetCursorFlat(0)
	s.text.SetScrollOffset(0)
}

func (s *Screen) detailText() string {
	row := s.set.Items[s.selected]
	var out strings.Builder
	out.WriteString(row.Title + "\n\n")
	if row.DisabledReason != "" {
		out.WriteString(row.DisabledReason)
	} else if row.UnsupportedReason != "" {
		out.WriteString(row.UnsupportedReason)
	} else {
		out.WriteString("This action is not available here.")
	}
	return out.String()
}

func (s *Screen) rebuildActions() {
	s.actions.SetCancelAction(ActionClose)
	var buttons []ui.ActionBarButton
	if s.hasSet && (s.selected >= 0 || s.failure != "") {
		if !s.busy {
			s.actions.SetCancelAction(ActionBack)
		}
		buttons = append(buttons, ui.ActionBarButton{
			Label: "Back", Action: ActionBack, Enabled: !s.busy, Variant: ui.VariantSecondary,
		})
	}
	buttons = append(buttons, ui.ActionBarButton{
		Label: "Close", Action: ActionClose, Enabled: true, Variant: ui.VariantSecondary,
	})
	s.actions.SetButtons(buttons...)
	s.actions.FocusLast()
	s.actions.Layout(ui.LayoutContext{}, s.actions.Bounds())
}

func (s *Screen) Children() []ui.Component {
	if s.InList() {
		return []ui.Component{s.list, s.scrollbar}
	}
	if !s.hasActions() {
		return []ui.Component{s.text}
	}
	return []ui.Component{s.text, s.actions}
}

func (s *Screen) Measure(ctx ui.LayoutContext, cs ui.Constraints) ui.Size {
	if s.InList() {
		return s.list.ContentSize(cs, 12)
	}
	cs.MaxW = min(cs.MaxW, 58)
	size := s.text.Measure(ctx, cs)
	if s.hasActions() {
		size.H = min(cs.MaxH, size.H+s.actions.PreferredHeight())
	}
	return cs.ClampSize(size)
}

func (s *Screen) Layout(ctx ui.LayoutContext, r ui.Rect) {
	s.SetBounds(r)
	w, h := max(0, r.W), max(0, r.H)
	s.actions.SetShowRule(h > 1)
	barH := 0
	if s.hasActions() {
		barH = min(h, s.actions.PreferredHeight())
	}
	bodyH := h - barH
	if s.InList() {
		barW := 0
		if w > 0 && s.scrollbar.NeededForViewport(bodyH) {
			barW = 1
		}
		s.list.Layout(ctx, ui.Rect{W: w - barW, H: bodyH})
		s.scrollbar.Layout(ctx, ui.Rect{X: w - barW, W: barW, H: bodyH})
	} else {
		s.text.Layout(ctx, ui.Rect{W: w, H: bodyH})
	}
	s.actions.Layout(ctx, ui.Rect{Y: bodyH, W: w, H: barH})
}

func (s *Screen) Paint(*surface.CellBuffer, int, int, ui.PaintContext) {}

func (s *Screen) Handle(ctx ui.EventContext, e ui.Event) ui.EventResult {
	if e.IsKey() && e.Key.Code == tea.KeyTab {
		if s.hasActions() {
			return actionFocus(ActionsID)
		}
		return ui.EventResult{Handled: true}
	}
	if e.IsKey() && e.Key.Code == tea.KeyEsc {
		if (s.selected >= 0 || s.failure != "" && s.hasSet) && !s.busy {
			return s.HandleAction(ctx, ui.Action{ID: ActionBack})
		}
		return s.HandleAction(ctx, ui.Action{ID: ActionClose})
	}
	return ui.EventResult{}
}

func (s *Screen) HandleAction(_ ui.EventContext, a ui.Action) ui.EventResult {
	switch a.ID {
	case ActionClose:
		return ui.EventResult{Handled: true, Cmd: func() tea.Msg { return CloseMsg{s} }}
	case ActionPrepare:
		if s.busy || !s.InList() {
			return ui.EventResult{Handled: true}
		}
		row, ok := a.Payload.(ui.ListItem)
		if !ok {
			return ui.EventResult{Handled: true}
		}
		index, ok := row.Payload.(int)
		if !ok || index < 0 || index >= len(s.set.Items) {
			return ui.EventResult{Handled: true}
		}
		if !s.set.Items[index].CanReview {
			s.selected, s.failure = index, ""
			s.setText(s.detailText())
			s.rebuildActions()
			return actionFocus(ActionsID)
		}
		s.SetBusy(true)
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint,
			Cmd: func() tea.Msg { return PrepareMsg{s, index} }}
	case ActionBack:
		if s.busy || !s.hasSet {
			return ui.EventResult{Handled: true}
		}
		s.selected, s.failure = -1, ""
		s.rebuildActions()
		return actionFocus(ListID)
	case "actionbar.exit.backward", "actionbar.exit.forward":
		if s.InList() {
			return actionFocus(ListID)
		}
		return actionFocus(TextID)
	}
	return ui.EventResult{}
}

func actionFocus(id ui.ComponentID) ui.EventResult {
	return ui.EventResult{Handled: true, Action: &ui.Action{ID: "ui.focus", Payload: id},
		Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
}

func (s *Screen) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleGroup, Label: "Manual code actions"}
}

type actionText struct {
	*ui.TextArea
	owner *Screen
}

func (t *actionText) Handle(ctx ui.EventContext, e ui.Event) ui.EventResult {
	if e.IsKey() && e.Key.Code == tea.KeyTab {
		if t.owner.hasActions() {
			return actionFocus(ActionsID)
		}
		return ui.EventResult{Handled: true}
	}
	return t.TextArea.Handle(ctx, e)
}
