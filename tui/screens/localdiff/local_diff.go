// Package localdiff composes a comparison snapshot and exact-owner local actions.
// The application owns snapshot preparation, freshness, source buffers and tabs.
//
// The page shows only what the tab and the editor cannot: the two versions and
// the decisions between them. The tab names the file (its tooltip the full
// path); Find, Save and Close keep their editor keys (Ctrl+F, Ctrl+S, Esc); the
// tab's menu holds the rare choices (presentation, open the file). A comparison
// that resolves has one bar: its state, Keep buttons, change navigation and
// Save. A read-only comparison has one status row.
package localdiff

import (
	tea "charm.land/bubbletea/v2"
	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/screens/diffview"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

const (
	ActionRefresh    = "localdiff.refresh"
	ActionOpenSource = "localdiff.open-source"
	ActionClose      = "localdiff.close"
	// ActionSave writes the chosen changes when the comparison resolves.
	ActionSave = "localdiff.save"
)

type Screen struct {
	ui.Base
	tokens              *ui.TokenSet
	viewer              *diffview.Screen
	search              *ui.FilterField
	focus               func(ui.ComponentID) bool
	searchOpen, loading bool
	refreshDisabled     bool
	resolving           bool
	notice, errText     string
}

func New(th *theme.Theme) *Screen { return NewWithID(th, "localdiff") }
func NewWithID(th *theme.Theme, id ui.ComponentID) *Screen {
	if th == nil {
		th = theme.DefaultDark()
	}
	t := ui.NewTokenSet(th)
	s := &Screen{Base: ui.NewBase(id), tokens: t, viewer: diffview.NewWithID(th, ui.NewID(id, "comparison")), search: ui.NewFilterField(ui.NewID(id, "search"), t)}
	s.search.SetPlaceholder("Find in comparison")
	s.search.SetSubmitAction("localdiff.match")
	s.search.OnChanged(s.viewer.SetQuery)
	return s
}
func (s *Screen) SetPrepared(p diffview.Prepared) {
	s.viewer.SetPrepared(p)
	s.loading = false
	s.errText = ""
	s.syncState()
}
func (s *Screen) SetLoading(b bool) {
	s.loading = b
	if b {
		s.errText = ""
	}
	s.syncState()
}
func (s *Screen) SetError(err error) {
	s.loading = false
	s.errText = ""
	if err != nil {
		s.errText = err.Error()
	}
	s.syncState()
}
func (s *Screen) SetNotice(n string)                 { s.notice = n; s.syncState() }
func (s *Screen) Loading() bool                      { return s.loading }
func (s *Screen) Comparison() diffview.Comparison    { return s.viewer.Comparison() }
func (s *Screen) DocumentID() ui.ComponentID         { return s.viewer.DocumentID() }
func (s *Screen) InvalidateWidthProfile()            { s.viewer.InvalidateWidthProfile() }
func (s *Screen) ComparisonReader() *diffview.Screen { return s.viewer }
func (s *Screen) SetFocusHandler(fn func(ui.ComponentID) bool) {
	s.focus = fn
	s.viewer.SetFocusHandler(fn)
}
func (s *Screen) SetTokens(t *ui.TokenSet) {
	if t == nil {
		return
	}
	s.tokens = t
	s.viewer.SetTokens(t)
	s.search.SetTokens(t)
}
func (s *Screen) SetTheme(th *theme.Theme) {
	if th != nil {
		s.SetTokens(ui.NewTokenSet(th))
	}
}
func (s *Screen) Children() []ui.Component {
	return []ui.Component{s.viewer, s.search}
}

// SetRefreshEnabled distinguishes immutable evidence from refreshable sources.
func (s *Screen) SetRefreshEnabled(enabled bool) { s.refreshDisabled = !enabled }

// SetResolvable lets the user keep each change from the left or the right and
// save the result. initial is the choice every change starts with.
func (s *Screen) SetResolvable(enabled bool, initial diffview.Choice) {
	s.resolving = enabled
	s.viewer.SetResolvable(enabled, initial)
	s.syncState()
}

// Resolvable reports whether the page saves chosen changes.
func (s *Screen) Resolvable() bool { return s.resolving }

// status is the page state for the one status line: an error, preparing, the
// search result, the owner's notice, then the changes.
func (s *Screen) status() (string, ui.SemanticColor) {
	switch {
	case s.errText != "":
		return s.errText, ui.ColorError
	case s.loading:
		return "Updating the comparison…", ui.ColorMuted
	case s.searchOpen && s.viewer.SearchStatus() != "":
		return s.viewer.SearchStatus(), ui.ColorMuted
	case s.notice != "":
		return s.notice, ui.ColorMuted
	}
	return "", ui.ColorMuted
}

// syncState gives the resolution bar the page state and the Save button.
func (s *Screen) syncState() {
	if !s.resolving {
		return
	}
	text, role := s.status()
	s.viewer.SetMessage(text, role)
	s.viewer.SetCommit("Save", "ctrl+s", ActionSave, !s.loading && s.errText == "")
}

func (s *Screen) Layout(ctx ui.LayoutContext, r ui.Rect) {
	s.SetBounds(r)
	s.syncState()
	y := 0
	s.search.Layout(ctx, ui.Rect{})
	if s.searchOpen {
		h := min(1, r.H)
		s.search.Layout(ctx, ui.Rect{Y: y, W: r.W, H: h})
		y += h
	}
	if !s.resolving {
		y += min(1, max(0, r.H-y)) // the status row
	}
	s.viewer.Layout(ctx, ui.Rect{Y: y, W: r.W, H: max(0, r.H-y)})
}

func (s *Screen) Paint(buf *surface.CellBuffer, ox, oy int, _ ui.PaintContext) {
	r := s.Bounds()
	ui.FillRect(buf, ox, oy, r, cell.BlankCell(s.tokens.Color(ui.ColorBg)))
	if s.resolving {
		return // the comparison's own bar shows the state
	}
	row := s.viewer.Bounds().Y - 1
	if row < 0 || row >= r.H {
		return
	}
	status, role := s.status()
	switch {
	case status == "" && s.viewer.HunkCount() == 0:
		status = "No differences"
	case status == "":
		status = "Read-only comparison · Alt+↑/↓ moves between changes"
	}
	buf.WriteString(ox+r.X, oy+r.Y+row, rendering.TruncateVisual(status, r.W, "…"), s.tokens.Color(role), s.tokens.Color(ui.ColorBg), 0)
}
func (s *Screen) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleGroup, Label: "Local comparison snapshot", Value: s.Comparison().Path}
}
func (s *Screen) Handle(_ ui.EventContext, e ui.Event) ui.EventResult {
	if !e.IsKey() {
		return ui.EventResult{}
	}
	switch e.Key.String() {
	case "esc":
		if s.searchOpen {
			s.searchOpen = false
			s.syncState()
			if s.focus != nil {
				s.focus(s.DocumentID())
			}
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
		}
		return s.action(ActionClose)
	case "ctrl+r":
		return s.action(ActionRefresh)
	case "ctrl+s":
		if s.resolving {
			if !s.viewer.CommitAllowed() {
				return ui.EventResult{Handled: true}
			}
			return s.action(ActionSave)
		}
	case "f3", "shift+f3":
		s.viewer.NextMatch(e.Key.Mod.Contains(tea.ModShift))
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
	case "ctrl+f":
		return s.OpenFind()
	case "tab":
		if s.resolving && s.focus != nil {
			s.focus(s.viewer.BarID())
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus}
		}
	case "enter":
		if s.searchOpen {
			s.viewer.NextMatch(e.Key.Mod.Contains(tea.ModShift))
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
		}
	}
	return ui.EventResult{}
}
func (s *Screen) action(id string) ui.EventResult {
	return ui.EventResult{Handled: true, Action: &ui.Action{ID: id, Payload: s}}
}

// OpenFind shows the find field and focuses it (Ctrl+F, the tab menu).
func (s *Screen) OpenFind() ui.EventResult {
	s.searchOpen = true
	s.syncState()
	if s.focus != nil {
		s.focus(s.search.ID())
	}
	return ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidateFocus | ui.InvalidatePaint}
}

// TogglePresentation switches between side by side and unified (tab menu).
func (s *Screen) TogglePresentation() {
	p := diffview.SideBySide
	if s.viewer.PreferredPresentation() == p {
		p = diffview.Unified
	}
	s.viewer.SetPresentation(p)
}

func (s *Screen) HandleAction(_ ui.EventContext, a ui.Action) ui.EventResult {
	result := ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
	switch a.ID {
	case ActionRefresh, ActionOpenSource, ActionClose, ActionSave:
		if a.ID == ActionRefresh && s.refreshDisabled || a.ID == ActionSave && !s.viewer.CommitAllowed() {
			return ui.EventResult{Handled: true}
		}
		return s.action(a.ID)
	case "localdiff.match":
		s.viewer.NextMatch(false)
	case "actionbar.exit.forward", "actionbar.exit.backward", "actionbar.exit.down", "actionbar.exit.up":
		if s.focus != nil {
			s.focus(s.DocumentID())
		}
	default:
		return ui.EventResult{Action: &a}
	}
	return result
}
