// Package navigation lists the places a language server found (references,
// definitions, implementations) and lets the user jump to one. It owns no file,
// provider, or editor I/O; OpenMsg is only the user's choice of a row.
package navigation

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types/insight"
)

const (
	ScreenID    ui.ComponentID = "navigation"
	ListID      ui.ComponentID = "navigation/list"
	TextID      ui.ComponentID = "navigation/detail"
	ActionOpen                 = "navigation.open"
	ActionClose                = "navigation.close"

	maxTargets      = 1024
	maxFieldBytes   = 4096
	maxDisplayBytes = 128 << 10 // aggregate input; quoting expansion is also bounded
)

// OpenMsg asks the owner to open the chosen row.
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
	result    insight.LSPNavigation
	hasResult bool
	busy      bool
}

func New(th *theme.Theme) *Screen {
	if th == nil {
		th = theme.DefaultDark()
	}
	ts := ui.NewTokenSet(th)
	s := &Screen{Base: ui.NewBase(ScreenID), tokens: ts}
	s.list = ui.NewList(ListID, ts)
	s.list.SetDefaultRowBackground(ui.ColorPopupBg)
	s.list.SetActivateAction(ActionOpen)
	s.list.SetFollowPointer(true)
	s.scrollbar = ui.NewScrollbarControl("navigation/scrollbar", s.list.Scroll(), ts)
	s.text = &reader{ui.NewTextArea(TextID, ts, true)}
	s.text.SetReadOnly(true)
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
}

func (s *Screen) SetTheme(th *theme.Theme) {
	if th != nil {
		s.SetTokens(ui.NewTokenSet(th))
	}
}

func (s *Screen) reset() {
	s.result = insight.LSPNavigation{}
	s.hasResult, s.busy = false, false
	s.list.SetItems(nil)
}

func (s *Screen) SetLoading(kind string) {
	s.reset()
	if !validString(kind) {
		s.SetError("This search cannot be shown.")
		return
	}
	s.busy = true
	s.setText("Searching…\nThe first search in a large project can take a minute.")
}

// SetError shows why nothing was found, in plain words. Nothing here is a
// provenance dump: the user needs the state and the next step.
func (s *Screen) SetError(message string) {
	s.reset()
	if !validString(message) || strings.TrimSpace(message) == "" {
		message = "The search could not be completed."
	}
	s.setText(message)
}

// SetResult shows the targets of a result, or an error when the result is invalid.
func (s *Screen) SetResult(result insight.LSPNavigation) {
	if !validResult(result) {
		s.reset()
		s.SetError("The language server returned an answer that cannot be shown.")
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
			items = append(items, ui.ListItem{
				ID: fmt.Sprint(index), Payload: index,
				Label: rowLabel(target), Detail: rowDetail(result.Boundary.Context.Root, target),
				Disabled: !canOpen(target),
			})
		}
		s.list.SetItems(items)
	} else {
		s.setText(emptyText(result))
	}
}

// Keep file and position in primary ink; the native List owns muted detail.
func rowLabel(target insight.LSPNavigationTarget) string {
	place := fmt.Sprintf("%d:%d", target.Selection.Start.Line+1, target.Selection.Start.Character+1)
	name := target.Path
	if name == "" {
		return target.URI + ":" + place
	}
	return filepath.Base(name) + ":" + place
}

func rowDetail(root string, target insight.LSPNavigationTarget) string {
	dir := filepath.Dir(target.Path)
	if root != "" {
		if rel, err := filepath.Rel(root, dir); err == nil && !strings.HasPrefix(rel, "..") {
			dir = rel
		}
	}
	detail := rowNote(target)
	if target.Path != "" && dir != "." && dir != "" {
		if detail != "" {
			detail += " · "
		}
		detail += filepath.ToSlash(dir)
	}
	return detail
}

// rowNote says why a row is special, before the folder, so a long path cannot
// clip it away.
func rowNote(target insight.LSPNavigationTarget) string {
	switch {
	case target.UnavailableReason != "" || target.Path == "":
		return "cannot be opened"
	case target.External:
		return "outside the project"
	}
	return ""
}

func emptyText(result insight.LSPNavigation) string {
	if result.Answer == insight.LSPNavigationUnsupported {
		return "This language server cannot do that for this file."
	}
	switch result.Kind {
	case insight.LSPNavigationReferences:
		return "No references found."
	case insight.LSPNavigationImplementation:
		return "No implementation found."
	case insight.LSPNavigationTypeDefinition:
		return "No type definition found."
	}
	return "No definition found."
}

// SetBusy marks that the chosen row is being opened. The list stays visible.
func (s *Screen) SetBusy(busy bool) {
	s.busy = busy
}

// InList reports that the screen shows places to choose from.
func (s *Screen) InList() bool {
	return s.hasResult && len(s.result.Targets) != 0
}

func (s *Screen) DefaultFocus() ui.ComponentID {
	if s.InList() {
		return ListID
	}
	return TextID
}

func (s *Screen) setText(value string) {
	s.text.CancelPointerCapture()
	s.text.SetValue(value)
	s.text.SetCursorFlat(0)
	s.text.SetScrollOffset(0)
}

func (s *Screen) Children() []ui.Component {
	if s.InList() {
		return []ui.Component{s.list, s.scrollbar}
	}
	return []ui.Component{s.text}
}

func (s *Screen) Measure(ctx ui.LayoutContext, cs ui.Constraints) ui.Size {
	if s.InList() {
		return s.list.ContentSize(cs, 12)
	}
	cs.MaxW, cs.MaxH = min(cs.MaxW, 58), min(cs.MaxH, 6)
	return s.text.Measure(ctx, cs)
}

func (s *Screen) Layout(ctx ui.LayoutContext, r ui.Rect) {
	s.SetBounds(r)
	w, h := max(0, r.W), max(0, r.H)
	bodyH := h
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
}

func (s *Screen) Paint(*surface.CellBuffer, int, int, ui.PaintContext) {}

func (s *Screen) Handle(ctx ui.EventContext, e ui.Event) ui.EventResult {
	if e.IsKey() && e.Key.Code == tea.KeyTab {
		return ui.EventResult{Handled: true}
	}
	if e.IsKey() && e.Key.Code == tea.KeyEsc {
		return s.HandleAction(ctx, ui.Action{ID: ActionClose})
	}
	return ui.EventResult{}
}

func (s *Screen) HandleAction(_ ui.EventContext, action ui.Action) ui.EventResult {
	switch action.ID {
	case ActionClose:
		return ui.EventResult{Handled: true, Cmd: func() tea.Msg { return CloseMsg{s} }}
	case ActionOpen:
		// Choosing a row is the whole decision: it goes straight to the code.
		if s.busy || !s.InList() {
			return ui.EventResult{Handled: true}
		}
		row, ok := action.Payload.(ui.ListItem)
		if !ok {
			return ui.EventResult{Handled: true}
		}
		index, ok := row.Payload.(int)
		if !ok || index < 0 || index >= len(s.result.Targets) || !canOpen(s.result.Targets[index]) {
			return ui.EventResult{Handled: true}
		}
		s.SetBusy(true)
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint,
			Cmd: func() tea.Msg { return OpenMsg{s, index} }}
	}
	return ui.EventResult{}
}

func canOpen(target insight.LSPNavigationTarget) bool {
	return target.Path != "" && target.UnavailableReason == ""
}

func (s *Screen) Title() string {
	if !s.InList() {
		return ""
	}
	name := "Definitions"
	switch s.result.Kind {
	case insight.LSPNavigationReferences:
		name = "References"
	case insight.LSPNavigationImplementation:
		name = "Implementations"
	case insight.LSPNavigationTypeDefinition:
		name = "Type definitions"
	}
	return fmt.Sprintf("%s · %d", name, len(s.result.Targets))
}

func (s *Screen) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleGroup, Label: "Search results"}
}

// reader changes only Tab ownership; scrolling, selection, copy and Escape's
// selection-first lifecycle remain the shared TextArea's responsibility.
type reader struct{ *ui.TextArea }

func (r *reader) Handle(ctx ui.EventContext, event ui.Event) ui.EventResult {
	if event.IsKey() && event.Key.Code == tea.KeyTab {
		return ui.EventResult{Handled: true}
	}
	return r.TextArea.Handle(ctx, event)
}

func validString(value string) bool {
	return len(value) <= maxFieldBytes && utf8.ValidString(value)
}

func validRange(r insight.LSPNavigationRange) bool {
	a, b := r.Start, r.End
	return a.Line >= 0 && a.Character >= 0 && b.Line >= 0 && b.Character >= 0 &&
		(a.Line < b.Line || a.Line == b.Line && a.Character <= b.Character)
}

// Bounds are checked before serialization/cloning. Display validation neither
// reads files nor confers authenticity or semantic freshness on the answer.
func validResult(result insight.LSPNavigation) bool {
	switch result.Kind {
	case insight.LSPNavigationDefinition, insight.LSPNavigationTypeDefinition,
		insight.LSPNavigationImplementation, insight.LSPNavigationReferences:
	default:
		return false
	}
	switch result.Answer {
	case insight.LSPNavigationTargets:
		if len(result.Targets) == 0 {
			return false
		}
	case insight.LSPNavigationEmpty, insight.LSPNavigationNull, insight.LSPNavigationUnsupported:
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
