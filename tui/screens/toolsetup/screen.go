// Package toolsetup presents the project's missing code-intelligence tools.
// It owns the user's selection only. The selection becomes a work order for
// the workspace Aeron; this screen downloads and installs nothing itself.
package toolsetup

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
)

const (
	ScreenID      ui.ComponentID = "project-tools"
	ListID        ui.ComponentID = "project-tools/list"
	DetailsID     ui.ComponentID = "project-tools/details"
	ActionsID     ui.ComponentID = "project-tools/actions"
	ActionInstall                = "project-tools.install"
	ActionDecline                = "project-tools.decline"
	ActionClose                  = "project-tools.close"
	rowActivate                  = "project-tools.toggle"
)

type DecisionMsg struct {
	Target   *Screen
	Selected []string
}

type CloseMsg struct{ Target *Screen }

// Screen displays the review. Every tool with a usable identity is a real
// choice and starts selected: Aeron works out how to get a tool that Crude
// cannot download itself. Only rows without a unique identity are disabled.
type Screen struct {
	ui.Base
	project   string
	items     []types.ProjectToolCandidate
	byID      map[string]int
	selected  map[string]bool
	list      *candidateList
	scrollbar *ui.ScrollbarControl
	detail    *detailReader
	intro     *ui.Label
	caption   *ui.Label
	actions   *ui.ActionBar
}

func New(th *theme.Theme, review types.ProjectToolReview) *Screen {
	if th == nil {
		th = theme.DefaultDark()
	}
	tokens := ui.NewTokenSet(th)
	s := &Screen{Base: ui.NewBase(ScreenID), project: review.Project, byID: make(map[string]int), selected: make(map[string]bool)}
	for i, item := range review.Items {
		s.items = append(s.items, types.CloneProjectToolCandidate(item))
		if _, duplicate := s.byID[item.ID]; duplicate || item.ID == "" {
			s.byID[item.ID] = -1
		} else {
			s.byID[item.ID] = i
		}
	}
	for _, item := range s.items {
		if s.selectable(item) {
			s.selected[item.ID] = true
		}
	}
	s.list = &candidateList{List: ui.NewList(ListID, tokens), screen: s}
	s.list.SetDefaultRowBackground(ui.ColorPopupBg)
	s.list.SetActivateAction(rowActivate)
	s.rebuildRows()
	s.scrollbar = ui.NewScrollbarControl("project-tools/scroll", s.list.Scroll(), tokens)
	s.scrollbar.SetSurfaceBackground(ui.ColorPopupBg)
	s.detail = &detailReader{TextArea: ui.NewTextArea(DetailsID, tokens, true)}
	s.detail.SetReadOnly(true)
	s.detail.SetBackgroundColor(tokens.Color(ui.ColorPopupBg))
	s.intro = ui.NewLabel("project-tools/intro", "Project: "+review.Project, th)
	s.caption = ui.NewLabel("project-tools/caption",
		"Aeron sets up the tools you select. It works in the background and shows a prompt when it needs your answer. Editing works without these tools.", th)
	s.caption.SetColorRole(ui.ColorMuted)
	s.actions = ui.NewActionBar(ActionsID, tokens,
		ui.ActionBarButton{Label: "Set up with Aeron", Action: ActionInstall, Variant: ui.VariantPrimary},
		ui.ActionBarButton{Label: "Not now", Action: ActionDecline, Enabled: true, Variant: ui.VariantSecondary})
	s.actions.SetAlignEnd(false)
	s.actions.SetShowRule(true)
	s.actions.SetSurfaceBackground(ui.ColorPopupBg)
	s.actions.SetCancelAction(ActionClose)
	s.actions.SetHint("Space: select · Tab: details")
	s.actions.FocusLast()
	s.updateActions()
	s.updateDetail()
	return s
}

func (s *Screen) selectable(item types.ProjectToolCandidate) bool {
	return s.byID[item.ID] >= 0
}

func (s *Screen) rebuildRows() {
	rows := make([]ui.ListItem, len(s.items))
	for i, item := range s.items {
		name := strings.Join(strings.Fields(valueOr(item.Package, item.Command)), " ")
		prefix := "[ ]  "
		if s.selected[item.ID] {
			prefix = "[x]  "
		}
		rows[i] = ui.ListItem{ID: item.ID, Label: prefix + name, Detail: strings.Join(item.Languages, ", "), Disabled: !s.selectable(item)}
	}
	s.list.SetItemsStable(rows)
}

// SelectedIDs lists the tools the user left selected, in review order.
func (s *Screen) SelectedIDs() []string {
	var selected []string
	for _, item := range s.items {
		if s.selectable(item) && s.selected[item.ID] {
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

func (s *Screen) updateActions() {
	s.actions.SetEnabled(ActionInstall, len(s.SelectedIDs()) > 0)
}

func valueOr(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func (s *Screen) updateDetail() {
	value := "No tools to review."
	if row, ok := s.list.Selected(); ok {
		if index, exists := s.byID[row.ID]; exists && index >= 0 {
			item := s.items[index]
			if !item.Installable {
				name := valueOr(item.Package, item.Command)
				if item.Version != "" {
					name += " " + item.Version
				}
				value = fmt.Sprintf("%s\n\nCrude cannot download this tool itself. Aeron finds another way to get it, for example a package manager.\n\nSource: %s\nProgram: %s\nLanguages: %s\nLicense: %s\nProject: %s",
					name, valueOr(item.Source, "Not provided"), valueOr(item.Command, "Not provided"),
					valueOr(strings.Join(item.Languages, ", "), "Not provided"),
					valueOr(strings.Join(item.Licenses, ", "), "Not provided"), s.project)
			} else {
				size := "Not provided by the source"
				if item.SizeKnown {
					size = fmt.Sprintf("%d bytes", item.SizeBytes)
				}
				checksum := "Crude records the checksum at first download."
				if item.ChecksumPublished {
					checksum = "Crude checks the checksum provided by the source."
				}
				value = fmt.Sprintf("%s %s\n\nLanguages: %s\nSource: %s\nDownload: %s\nFile: %s\nProgram: %s\nLicense: %s\nDownload size: %s\n%s\nProject: %s",
					valueOr(item.Package, item.Command), valueOr(item.Version, "Version not provided"),
					valueOr(strings.Join(item.Languages, ", "), "Not provided"), valueOr(item.Source, "Not provided"),
					valueOr(item.URL, "Not provided"), valueOr(item.File, "Not provided"),
					valueOr(item.Command, "Not provided"), valueOr(strings.Join(item.Licenses, ", "), "Not provided"), size, checksum, s.project)
			}
		}
	}
	s.detail.CancelPointerCapture()
	s.detail.SetValue(value)
	s.detail.SetCursorFlat(0)
	s.detail.SetScrollOffset(0)
}

func (s *Screen) Children() []ui.Component {
	return []ui.Component{s.intro, s.caption, s.list, s.scrollbar, s.detail, s.actions}
}

func (s *Screen) SetTokens(tokens *ui.TokenSet) {
	if tokens == nil {
		return
	}
	s.intro.SetTokens(tokens)
	s.caption.SetTokens(tokens)
	s.list.SetTokens(tokens)
	s.scrollbar.SetTokens(tokens)
	s.detail.SetTokens(tokens)
	s.detail.SetBackgroundColor(tokens.Color(ui.ColorPopupBg))
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
	introH := min(1, bodyH)
	captionH := min(1, bodyH-introH)
	remaining := bodyH - introH - captionH
	listH := min(len(s.items), max(0, remaining/2))
	detailH := remaining - listH
	listY := introH + captionH
	scrollW := 0
	if w > 1 && s.scrollbar.NeededForViewport(listH) {
		scrollW = 1
	}
	s.intro.Layout(ctx, ui.Rect{W: w, H: introH})
	s.caption.Layout(ctx, ui.Rect{Y: introH, W: w, H: captionH})
	s.list.Layout(ctx, ui.Rect{Y: listY, W: w - scrollW, H: listH})
	s.scrollbar.Layout(ctx, ui.Rect{X: w - scrollW, Y: listY, W: scrollW, H: listH})
	s.detail.Layout(ctx, ui.Rect{Y: listY + listH, W: w, H: detailH})
	s.actions.Layout(ctx, ui.Rect{Y: bodyH, W: w, H: barH})
}

func (*Screen) Paint(*surface.CellBuffer, int, int, ui.PaintContext) {}

func (s *Screen) Handle(ctx ui.EventContext, event ui.Event) ui.EventResult {
	if event.IsKey() && event.Key.Code == tea.KeyEsc {
		return s.HandleAction(ctx, ui.Action{ID: ActionClose})
	}
	return ui.EventResult{}
}

func focus(id ui.ComponentID) ui.EventResult {
	return ui.EventResult{Handled: true, Action: &ui.Action{ID: "ui.focus", Payload: id}, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
}

func (s *Screen) HandleAction(_ ui.EventContext, action ui.Action) ui.EventResult {
	switch action.ID {
	case rowActivate:
		row, ok := action.Payload.(ui.ListItem)
		if !ok {
			return ui.EventResult{Handled: true}
		}
		if index, exists := s.byID[row.ID]; exists && index >= 0 {
			s.selected[row.ID] = !s.selected[row.ID]
			s.rebuildRows()
			s.updateActions()
		}
		s.updateDetail()
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
	case ActionInstall:
		if selected := s.SelectedIDs(); len(selected) > 0 {
			return ui.EventResult{Handled: true, Cmd: func() tea.Msg { return DecisionMsg{s, selected} }}
		}
		return ui.EventResult{Handled: true}
	case ActionDecline:
		return ui.EventResult{Handled: true, Cmd: func() tea.Msg { return DecisionMsg{Target: s} }}
	case ActionClose:
		return ui.EventResult{Handled: true, Cmd: func() tea.Msg { return CloseMsg{s} }}
	case "actionbar.exit.forward", "actionbar.exit.backward":
		return focus(ListID)
	}
	return ui.EventResult{}
}

// candidateList delegates navigation and hit testing to the shared List and
// observes its selection only to refresh the read-only details.
type candidateList struct {
	*ui.List
	screen *Screen
}

func (l *candidateList) Handle(ctx ui.EventContext, event ui.Event) ui.EventResult {
	if event.IsKey() {
		switch event.Key.Code {
		case tea.KeyTab:
			if event.Key.Mod.Contains(tea.ModShift) {
				return focus(ActionsID)
			}
			return focus(DetailsID)
		case ' ':
			if row, ok := l.Selected(); ok {
				return ui.EventResult{Handled: true, Action: &ui.Action{ID: rowActivate, Payload: row}}
			}
		}
	}
	before := l.SelectedIndex()
	result := l.List.Handle(ctx, event)
	if before != l.SelectedIndex() {
		l.screen.updateDetail()
		result.Invalidate |= ui.InvalidateLayout | ui.InvalidatePaint
	}
	return result
}

// The native reader owns wrapping, scrolling, selection and copy. Tab only
// changes the focus target; Escape bubbles to the screen's safe close action.
type detailReader struct{ *ui.TextArea }

func (d *detailReader) Handle(ctx ui.EventContext, event ui.Event) ui.EventResult {
	if event.IsKey() && event.Key.Code == tea.KeyTab {
		if event.Key.Mod.Contains(tea.ModShift) {
			return focus(ListID)
		}
		return focus(ActionsID)
	}
	return d.TextArea.Handle(ctx, event)
}
