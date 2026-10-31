// Package navigation presents source-qualified LSP locations. It owns no file,
// provider, or editor I/O; OpenMsg is only an explicit, selected-row intent.
package navigation

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
)

const (
	ScreenID      ui.ComponentID = "navigation"
	ListID        ui.ComponentID = "navigation/list"
	TextID        ui.ComponentID = "navigation/detail"
	ActionsID     ui.ComponentID = "navigation/actions"
	ActionInspect                = "navigation.inspect"
	ActionOpen                   = "navigation.open"
	ActionBack                   = "navigation.back"
	ActionClose                  = "navigation.close"

	maxTargets      = 1024
	maxFieldBytes   = 4096
	maxDisplayBytes = 128 << 10 // aggregate input; quoting expansion is also bounded
)

type OpenMsg struct {
	Target *Screen
	Index  int
}

type CloseMsg struct{ Target *Screen }

// Screen retains a detached display snapshot, never target file contents.
// Owners delivering SetLoading/SetResult/SetError/SetBusy must invalidate their
// Host's layout/content and restore DefaultFocus when replacing the body.
type Screen struct {
	ui.Base
	tokens    *ui.TokenSet
	list      *ui.List
	scrollbar *ui.ScrollbarControl
	text      *reader
	actions   *ui.ActionBar
	result    types.LSPNavigation
	hasResult bool
	selected  int
	busy      bool
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
	s.scrollbar = ui.NewScrollbarControl("navigation/scrollbar", s.list.Scroll(), ts)
	s.text = &reader{ui.NewTextArea(TextID, ts, true)}
	s.text.SetReadOnly(true)
	s.actions = ui.NewActionBar(ActionsID, ts)
	s.actions.SetShowRule(true)
	s.actions.SetSurfaceBackground(ui.ColorPopupBg)
	s.SetTokens(ts)
	s.SetLoading("")
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

func (s *Screen) reset() {
	s.result = types.LSPNavigation{}
	s.hasResult, s.selected, s.busy = false, -1, false
	s.list.SetItems(nil)
}

func (s *Screen) SetLoading(kind string) {
	s.reset()
	if !validString(kind) {
		s.SetError("Invalid navigation kind display data.")
		return
	}
	s.busy = true
	s.setText(fmt.Sprintf("Requesting navigation (%q)…\n\n"+
		"Waiting for source/provider/context-qualified evidence.\n"+
		"No target file contents have been read yet.", kind))
	s.rebuildActions()
}

func (s *Screen) SetError(message string) {
	provenance := ""
	if s.hasResult {
		provenance = "\n\n" + s.provenanceText()
	}
	s.reset() // failure never leaves an old Open action active
	if !validString(message) {
		message = "Invalid or oversized failure display data."
	}
	s.setText("Navigation failed\n\nFailure (literal): " + strconv.Quote(message) +
		"\n\nNo target was opened by this screen." + provenance)
	s.rebuildActions()
}

func (s *Screen) SetResult(result types.LSPNavigation) {
	if !validResult(result) {
		// Do not retain old targets or misleading old provenance for rejected data.
		s.reset()
		s.SetError("Invalid or oversized navigation display data; the answer was refused.")
		return
	}
	s.reset()
	s.result = result
	s.result.Targets = slices.Clone(result.Targets)
	s.result.Boundary.Context.UnknownInputs = slices.Clone(result.Boundary.Context.UnknownInputs)
	for i := range s.result.Targets {
		if origin := s.result.Targets[i].Origin; origin != nil {
			copy := *origin
			s.result.Targets[i].Origin = &copy
		}
	}
	s.hasResult = true
	if len(result.Targets) > 0 {
		items := make([]ui.ListItem, 0, len(result.Targets))
		for index, target := range result.Targets {
			status := "Inspect"
			if target.UnavailableReason != "" || target.Path == "" {
				status = "Unavailable · inspect"
			} else if target.External {
				status = "Outside workspace · inspect"
			}
			// Full strings and metadata remain in the scrollable/copyable detail.
			label := fmt.Sprintf("%s · %q", status, target.URI)
			if target.Path != "" {
				// Keep destinations distinguishable at narrow widths, rather
				// than spending every visible column on a shared URI prefix.
				label = fmt.Sprintf("%q · %s · %q", filepath.Base(target.Path), status, target.URI)
			}
			items = append(items, ui.ListItem{
				ID: strconv.Itoa(index), Payload: index,
				Label:  label,
				Detail: fmt.Sprintf("%d:%d", target.Selection.Start.Line, target.Selection.Start.Character),
			})
		}
		s.list.SetItems(items)
	} else {
		description := map[string]string{
			types.LSPNavigationNull:        "Provider returned null.",
			types.LSPNavigationEmpty:       "Provider returned an empty array [].",
			types.LSPNavigationUnsupported: "Navigation is unsupported for this source/provider.",
		}[result.Answer]
		s.setText(description + "\n\nThis is not proof that no symbols exist, or that the file or project is clean.\n\n" +
			s.provenanceText())
	}
	s.rebuildActions()
}

func (s *Screen) SetBusy(busy bool) {
	s.busy = busy
	s.rebuildActions()
}

func (s *Screen) InList() bool {
	return s.hasResult && len(s.result.Targets) != 0 && s.selected < 0
}

func (s *Screen) DefaultFocus() ui.ComponentID {
	if s.InList() {
		return ListID
	}
	return ActionsID // Close is always the safe initial detail action.
}

func (s *Screen) setText(value string) {
	s.text.CancelPointerCapture()
	s.text.SetValue(value)
	s.text.SetCursorFlat(0)
	s.text.SetScrollOffset(0)
}

func (s *Screen) provenanceText() string {
	boundary, _ := json.MarshalIndent(s.result.Boundary, "", "  ")
	return fmt.Sprintf("Navigation kind (literal): %q\nAnswer (literal): %q\n"+
		"Source wire version: %d\nInvocation (zero-based UTF-16): %d:%d\n"+
		"References includeDeclaration: %t\n\nSource / provider / context / unknown inputs:\n%s",
		s.result.Kind, s.result.Answer, s.result.WireVersion, s.result.Line, s.result.Col,
		s.result.IncludeDeclaration, boundary)
}

func formatRange(r types.LSPNavigationRange) string {
	return fmt.Sprintf("[%d:%d, %d:%d)", r.Start.Line, r.Start.Character, r.End.Line, r.End.Character)
}

func (s *Screen) detailText() string {
	target := s.result.Targets[s.selected]
	var out strings.Builder
	fmt.Fprintf(&out, "Target %d of %d\nURI (literal, complete): %q\nPath (literal, complete): %q\n"+
		"LocationLink: %t\nRanges are zero-based UTF-16, end exclusive.\n"+
		"targetRange: %s\ntargetSelectionRange: %s\n",
		s.selected+1, len(s.result.Targets), target.URI, target.Path, target.Link,
		formatRange(target.Range), formatRange(target.Selection))
	if target.Origin != nil {
		fmt.Fprintf(&out, "originSelectionRange: %s\n", formatRange(*target.Origin))
	} else {
		out.WriteString("originSelectionRange: not reported\n")
	}
	if target.External {
		out.WriteString("\nWarning: outside workspace. Opening requires explicit Open external file.\n")
	}
	if target.UnavailableReason != "" {
		fmt.Fprintf(&out, "\nUnavailable (literal): %q\n", target.UnavailableReason)
	} else if target.Path == "" {
		out.WriteString("\nUnavailable: no local file path was supplied.\n")
	}
	out.WriteString("\nNo target file contents have been read yet.\n" +
		"Standard LSP targets are unversioned. Open requests structural validation against observed target bytes; " +
		"it cannot guarantee semantic freshness or that the provider analyzed those bytes.\n\n")
	out.WriteString(s.provenanceText())
	return out.String()
}

func (s *Screen) canOpen() bool {
	if !s.hasResult || s.selected < 0 || s.selected >= len(s.result.Targets) {
		return false
	}
	target := s.result.Targets[s.selected]
	return !s.busy && target.Path != "" && target.UnavailableReason == ""
}

func (s *Screen) rebuildActions() {
	s.actions.SetCancelAction(ActionClose)
	var buttons []ui.ActionBarButton
	if s.hasResult && s.selected >= 0 {
		s.actions.SetCancelAction(ActionBack)
		label := "Open"
		if s.result.Targets[s.selected].External {
			label = "Open external file"
		}
		buttons = append(buttons,
			ui.ActionBarButton{ID: "navigation/actions/open", Label: label, Action: ActionOpen,
				Enabled: s.canOpen(), Variant: ui.VariantPrimary},
			ui.ActionBarButton{ID: "navigation/actions/back", Label: "Back", Action: ActionBack,
				Enabled: true, Variant: ui.VariantSecondary})
	}
	buttons = append(buttons, ui.ActionBarButton{ID: "navigation/actions/close",
		Label: "Close", Action: ActionClose, Enabled: true, Variant: ui.VariantSecondary})
	s.actions.SetButtons(buttons...)
	s.actions.FocusLast()
	// SetButtons itself does not invalidate Host layout. Event paths below also
	// mark layout/content; relayout retained geometry immediately for external
	// deliveries, so the next painted footer cannot have stale button bounds.
	s.actions.Layout(ui.LayoutContext{}, s.actions.Bounds())
}

func (s *Screen) Children() []ui.Component {
	if s.InList() {
		return []ui.Component{s.list, s.scrollbar, s.actions}
	}
	return []ui.Component{s.text, s.actions}
}

func (s *Screen) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
	return cs.ClampSize(ui.Size{W: 100, H: 28})
}

func (s *Screen) Layout(ctx ui.LayoutContext, r ui.Rect) {
	s.SetBounds(r)
	w, h := max(0, r.W), max(0, r.H)
	// Even a one-row body retains a decision row instead of only its rule.
	s.actions.SetShowRule(h > 1)
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
	if e.IsKey() && e.Key.Code == tea.KeyTab {
		return focus(ActionsID)
	}
	if e.IsKey() && e.Key.Code == tea.KeyEsc {
		action := ActionClose
		if s.hasResult && s.selected >= 0 {
			action = ActionBack
		}
		return s.HandleAction(ctx, ui.Action{ID: action})
	}
	return ui.EventResult{}
}

func (s *Screen) HandleAction(_ ui.EventContext, action ui.Action) ui.EventResult {
	switch action.ID {
	case ActionClose:
		return ui.EventResult{Handled: true, Cmd: func() tea.Msg { return CloseMsg{s} }}
	case ActionInspect:
		if s.busy || !s.InList() {
			return ui.EventResult{Handled: true}
		}
		row, ok := action.Payload.(ui.ListItem)
		if !ok {
			return ui.EventResult{Handled: true}
		}
		index, ok := row.Payload.(int)
		if !ok || index < 0 || index >= len(s.result.Targets) {
			return ui.EventResult{Handled: true}
		}
		s.selected = index
		s.setText(s.detailText())
		s.rebuildActions()
		return focus(ActionsID)
	case ActionBack:
		if !s.hasResult || s.selected < 0 {
			return ui.EventResult{Handled: true}
		}
		s.selected = -1
		s.rebuildActions()
		return focus(ListID)
	case ActionOpen:
		if !s.canOpen() {
			return ui.EventResult{Handled: true}
		}
		index := s.selected
		s.SetBusy(true)
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint,
			Cmd: func() tea.Msg { return OpenMsg{s, index} }}
	case "actionbar.exit.backward", "actionbar.exit.forward":
		if s.InList() {
			return focus(ListID)
		}
		return focus(TextID)
	}
	return ui.EventResult{}
}

func focus(id ui.ComponentID) ui.EventResult {
	return ui.EventResult{Handled: true, Action: &ui.Action{ID: "ui.focus", Payload: id},
		Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
}

func (s *Screen) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleGroup, Label: "Source-qualified navigation"}
}

// reader changes only Tab ownership; scrolling, selection, copy and Escape's
// selection-first lifecycle remain the shared TextArea's responsibility.
type reader struct{ *ui.TextArea }

func (r *reader) Handle(ctx ui.EventContext, event ui.Event) ui.EventResult {
	if event.IsKey() && event.Key.Code == tea.KeyTab {
		return focus(ActionsID)
	}
	return r.TextArea.Handle(ctx, event)
}

func validString(value string) bool {
	return len(value) <= maxFieldBytes && utf8.ValidString(value)
}

func validRange(r types.LSPNavigationRange) bool {
	a, b := r.Start, r.End
	return a.Line >= 0 && a.Character >= 0 && b.Line >= 0 && b.Character >= 0 &&
		(a.Line < b.Line || a.Line == b.Line && a.Character <= b.Character)
}

// Bounds are checked before serialization/cloning. Display validation neither
// reads files nor confers authenticity or semantic freshness on the answer.
func validResult(result types.LSPNavigation) bool {
	switch result.Kind {
	case types.LSPNavigationDefinition, types.LSPNavigationTypeDefinition,
		types.LSPNavigationImplementation, types.LSPNavigationReferences:
	default:
		return false
	}
	switch result.Answer {
	case types.LSPNavigationTargets:
		if len(result.Targets) == 0 {
			return false
		}
	case types.LSPNavigationEmpty, types.LSPNavigationNull, types.LSPNavigationUnsupported:
		if len(result.Targets) != 0 {
			return false
		}
	default:
		return false
	}
	if len(result.Targets) > maxTargets || result.Line < 0 || result.Col < 0 ||
		result.WireVersion < 0 || len(result.Boundary.Context.UnknownInputs) > maxTargets {
		return false
	}
	total := 0
	check := func(value string) bool {
		total += len(value)
		return validString(value) && total <= maxDisplayBytes
	}
	b := result.Boundary
	for _, value := range []string{b.Source.WorkspaceGeneration, b.Source.DocumentID, b.Source.Path,
		b.Source.Lifetime, b.Source.Kind, b.Context.ID, b.Context.Root,
		b.Producer.InstanceID, b.Producer.Provider, b.Producer.ToolIdentity} {
		if !check(value) {
			return false
		}
	}
	for _, value := range b.Context.UnknownInputs {
		if !check(value) {
			return false
		}
	}
	for _, target := range result.Targets {
		if !check(target.URI) || !check(target.Path) || !check(target.UnavailableReason) ||
			!validRange(target.Range) || !validRange(target.Selection) ||
			target.Origin != nil && !validRange(*target.Origin) {
			return false
		}
	}
	return true
}
