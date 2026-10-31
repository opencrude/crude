// Package codeactions owns the read-only manual action chooser. The canonical
// workspace owns raw provider evidence, lazy resolution and mutation permits.
package codeactions

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
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
	ActionInspect                = "code-actions.inspect"
	ActionPrepare                = "code-actions.prepare"
	ActionBack                   = "code-actions.back"
	ActionClose                  = "code-actions.close"
)

type PrepareMsg struct {
	Target *Screen
	Index  int
}

type CloseMsg struct{ Target *Screen }

// Screen is a focused body, not an RPC or file owner. Opening a row reads its
// complete literal title and eligibility. It never silently selects a preferred
// row for Apply, drops unsupported pieces, or calls a provider on paint/input.
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
	s.list.SetActivateAction(ActionInspect)
	s.scrollbar = ui.NewScrollbarControl("code-actions/scrollbar", s.list.Scroll(), ts)
	s.text = &actionText{ui.NewTextArea(TextID, ts, true)}
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
	s.setText("Requesting actions for the exact saved shared source and selection…\n\n" +
		"Only current-document text edits can be reviewed. No command or resource operation will run.")
	s.rebuildActions()
}

func (s *Screen) SetError(message string) {
	s.busy, s.failure = false, message
	if !s.hasSet {
		s.setText("Code actions unavailable\n\n" + message + "\n\nNo source changes were made.")
	} else if s.selected >= 0 {
		s.setText(s.detailText())
	}
	s.rebuildActions()
}

func (s *Screen) SetSet(set workspace.CodeActionSet) {
	total := 0
	for i, row := range set.Items {
		total += len(row.Title) + len(row.Kind) + len(row.DisabledReason) + len(row.UnsupportedReason)
		if i != row.Index || len(row.Title) > 4<<10 || len(row.Kind) > 256 ||
			len(row.DisabledReason) > 4<<10 || len(row.UnsupportedReason) > 4<<10 {
			s.SetError("The action list contains invalid or oversized display evidence.")
			return
		}
	}
	if set.ID == "" || set.Digest == "" || len(set.Items) > 64 || total > 64<<10 {
		s.SetError("The action list is missing its original identity or exceeds its display bound.")
		return
	}
	s.set = set
	s.set.Items = slices.Clone(set.Items)
	s.set.Boundary.Context.UnknownInputs = slices.Clone(set.Boundary.Context.UnknownInputs)
	s.hasSet, s.selected, s.busy, s.failure = true, -1, false, ""
	if len(set.Items) == 0 {
		s.setText("No actions returned for this source and selection.\n\n" + s.provenanceText() +
			"\n\nAn empty or null response is not proof that this file or project has no problems.")
	} else {
		items := make([]ui.ListItem, 0, len(set.Items))
		for _, row := range set.Items {
			label := strconv.Quote(row.Title)
			switch {
			case row.DisabledReason != "":
				label = "Disabled · " + label
			case !row.CanReview:
				label = "Unsupported · " + label
			case row.HasPreferred && row.Preferred:
				label = "Preferred · " + label
			}
			// Disabled/unsupported actions stay selectable for complete detail.
			items = append(items, ui.ListItem{
				ID: fmt.Sprint(row.Index), Label: label,
				Detail: strconv.Quote(row.Kind), Payload: row.Index,
			})
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
	return s.hasSet && len(s.set.Items) != 0 && s.selected < 0
}

func (s *Screen) DefaultFocus() ui.ComponentID {
	if s.InList() {
		return ListID
	}
	return ActionsID // Close, never Preview or Apply, is the safe default.
}

func (s *Screen) setText(value string) {
	s.text.CancelPointerCapture()
	s.text.SetValue(value)
	s.text.SetCursorFlat(0)
	s.text.SetScrollOffset(0)
}

func (s *Screen) provenanceText() string {
	boundary, _ := json.MarshalIndent(s.set.Boundary, "", "  ")
	return fmt.Sprintf("Original action set: %s\nSet digest: %s\nSource byte selection: [%d,%d)\n"+
		"Original diagnostics included: %d\nDiagnostic context: %s\n\nSource / provider / project-context boundary:\n%s",
		s.set.ID, s.set.Digest, s.set.Start, s.set.End, s.set.DiagnosticsIncluded, s.set.DiagnosticsDetail, boundary)
}

func (s *Screen) detailText() string {
	row := s.set.Items[s.selected]
	var out strings.Builder
	fmt.Fprintf(&out, "Action title (literal, complete): %q\nKind (literal): %q\nPreferred: %t (reported: %t)\n",
		row.Title, row.Kind, row.Preferred, row.HasPreferred)
	if s.failure != "" {
		fmt.Fprintf(&out, "\nReview unavailable: %s\n", s.failure)
	}
	if row.DisabledReason != "" {
		fmt.Fprintf(&out, "\nDisabled by provider (literal): %q\n", row.DisabledReason)
	}
	if row.UnsupportedReason != "" {
		fmt.Fprintf(&out, "\nUnsupported: %s\n", row.UnsupportedReason)
	}
	if row.NeedsResolve && row.CanReview {
		out.WriteString("\nPreview requests only the original row's edit from the same provider and source. " +
			"Resolution cannot execute a command or approve any edit.\n")
	}
	out.WriteString("\nPreview never writes source. Applying requires a separate immutable, Cancel-default review " +
		"and retained Local History safety. Only ordinary text edits to this saved document are supported.\n\n")
	out.WriteString(s.provenanceText())
	return out.String()
}

func (s *Screen) rebuildActions() {
	s.actions.SetCancelAction(ActionClose)
	var buttons []ui.ActionBarButton
	if s.hasSet && s.selected >= 0 {
		if !s.busy {
			s.actions.SetCancelAction(ActionBack)
		}
		if s.set.Items[s.selected].CanReview {
			buttons = append(buttons, ui.ActionBarButton{
				Label: "Preview changes", Action: ActionPrepare, Enabled: !s.busy, Variant: ui.VariantPrimary,
			})
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
}

func (s *Screen) Children() []ui.Component {
	if s.InList() {
		return []ui.Component{s.list, s.scrollbar, s.actions}
	}
	return []ui.Component{s.text, s.actions}
}

func (s *Screen) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
	return cs.ClampSize(ui.Size{W: 100, H: 26})
}

func (s *Screen) Layout(ctx ui.LayoutContext, r ui.Rect) {
	s.SetBounds(r)
	w, h := max(0, r.W), max(0, r.H)
	barH := min(h, s.actions.PreferredHeight())
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
	if e.IsKey() && e.Key.Code == tea.KeyEsc {
		if s.selected >= 0 && !s.busy {
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
	case ActionInspect:
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
		s.selected, s.failure = index, ""
		s.setText(s.detailText())
		s.rebuildActions()
		return actionFocus(ActionsID)
	case ActionBack:
		if s.busy || s.selected < 0 {
			return ui.EventResult{Handled: true}
		}
		s.selected, s.failure = -1, ""
		s.rebuildActions()
		return actionFocus(ListID)
	case ActionPrepare:
		if s.busy || s.selected < 0 || !s.set.Items[s.selected].CanReview {
			return ui.EventResult{Handled: true}
		}
		index := s.set.Items[s.selected].Index
		s.SetBusy(true)
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint,
			Cmd: func() tea.Msg { return PrepareMsg{s, index} }}
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

type actionText struct{ *ui.TextArea }

func (t *actionText) Handle(ctx ui.EventContext, e ui.Event) ui.EventResult {
	if e.IsKey() && e.Key.Code == tea.KeyTab {
		return actionFocus(ActionsID)
	}
	return t.TextArea.Handle(ctx, e)
}
