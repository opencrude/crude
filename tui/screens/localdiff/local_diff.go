// Package localdiff composes a comparison snapshot and exact-owner local actions.
// The application owns snapshot preparation, freshness, source buffers and tabs.
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
)

type Screen struct {
	ui.Base
	tokens              *ui.TokenSet
	viewer              *diffview.Screen
	actions             *ui.ActionBar
	more                *ui.List
	moreOpen, compact   bool
	search              *ui.FilterField
	focus               func(ui.ComponentID) bool
	searchOpen, loading bool
	notice, errText     string
}

func New(th *theme.Theme) *Screen { return NewWithID(th, "localdiff") }
func NewWithID(th *theme.Theme, id ui.ComponentID) *Screen {
	if th == nil {
		th = theme.DefaultDark()
	}
	t := ui.NewTokenSet(th)
	s := &Screen{Base: ui.NewBase(id), tokens: t, viewer: diffview.NewWithID(th, ui.NewID(id, "comparison")), actions: ui.NewActionBar(ui.NewID(id, "actions"), t), search: ui.NewFilterField(ui.NewID(id, "search"), t)}
	s.actions.SetAlignEnd(false)
	s.actions.SetSurfaceBackground(ui.ColorBg)
	s.more = ui.NewList(ui.NewID(id, "more"), t)
	s.more.SetActivateAction("localdiff.more.activate")
	var items []ui.ListItem
	for _, b := range actionButtons() {
		items = append(items, ui.ListItem{ID: b.Action, Label: b.Label, Command: true})
	}
	s.more.SetItems(items)
	s.configureActions(false)
	s.search.SetPlaceholder("Find in comparison")
	s.search.SetSubmitAction("localdiff.match")
	s.search.OnChanged(s.viewer.SetQuery)
	return s
}
func (s *Screen) SetPrepared(p diffview.Prepared) {
	s.viewer.SetPrepared(p)
	s.loading = false
	s.errText = ""
}
func (s *Screen) SetLoading(b bool) {
	s.loading = b
	if b {
		s.errText = ""
	}
}
func (s *Screen) SetError(err error) {
	s.loading = false
	s.errText = ""
	if err != nil {
		s.errText = err.Error()
	}
}
func (s *Screen) SetNotice(n string)              { s.notice = n }
func (s *Screen) Comparison() diffview.Comparison { return s.viewer.Comparison() }
func (s *Screen) DocumentID() ui.ComponentID      { return s.viewer.DocumentID() }
func (s *Screen) InvalidateWidthProfile()         { s.viewer.InvalidateWidthProfile() }
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
	s.actions.SetTokens(t)
	s.search.SetTokens(t)
	s.more.SetTokens(t)
}
func (s *Screen) SetTheme(th *theme.Theme) {
	if th != nil {
		s.SetTokens(ui.NewTokenSet(th))
	}
}
func (s *Screen) Children() []ui.Component {
	return []ui.Component{s.actions, s.viewer, s.search, s.more}
}
func actionButtons() []ui.ActionBarButton {
	return []ui.ActionBarButton{
		{Label: "Refresh", Action: ActionRefresh, Enabled: true},
		{Label: "Open Source", Action: ActionOpenSource, Enabled: true},
		{Label: "Unified / Split", Action: "localdiff.presentation", Enabled: true},
		{Label: "Find", Action: "localdiff.find", Enabled: true},
		{Label: "Previous", Action: "localdiff.previous", Enabled: true},
		{Label: "Next", Action: "localdiff.next", Enabled: true},
		{Label: "Close", Action: ActionClose, Enabled: true},
	}
}
func (s *Screen) configureActions(compact bool) {
	buttons := actionButtons()
	if compact {
		buttons = []ui.ActionBarButton{{Label: "More", Action: "localdiff.more", Enabled: true}}
	}
	s.actions.SetButtons(buttons...)
}
func (s *Screen) Layout(ctx ui.LayoutContext, r ui.Rect) {
	s.SetBounds(r)
	compact := r.W < 100
	if compact != s.compact {
		s.compact = compact
		s.configureActions(compact)
	}
	s.more.Layout(ctx, ui.Rect{})
	y := min(1, r.H)
	h := min(1, max(0, r.H-y))
	s.actions.Layout(ctx, ui.Rect{Y: y, W: r.W, H: h})
	y += h
	if s.moreOpen {
		h = min(7, max(0, r.H-y-3))
		s.more.Layout(ctx, ui.Rect{Y: y, W: r.W, H: h})
		y += h
	}
	s.search.Layout(ctx, ui.Rect{})
	if s.searchOpen {
		h = min(1, max(0, r.H-y))
		s.search.Layout(ctx, ui.Rect{Y: y, W: r.W, H: h})
		y += h
	}
	h = min(1, max(0, r.H-y))
	y += h
	s.viewer.Layout(ctx, ui.Rect{Y: y, W: r.W, H: max(0, r.H-y)})
}
func (s *Screen) Paint(buf *surface.CellBuffer, ox, oy int, _ ui.PaintContext) {
	r := s.Bounds()
	ui.FillRect(buf, ox, oy, r, cell.BlankCell(s.tokens.Color(ui.ColorBg)))
	if r.H == 0 {
		return
	}
	buf.WriteString(ox+r.X, oy+r.Y, rendering.TruncateVisual(s.Comparison().Path, r.W, "…"), s.tokens.Color(ui.ColorFg), s.tokens.Color(ui.ColorBg), cell.AttrBold)
	status, role := s.notice, ui.ColorMuted
	if s.loading {
		status = "Preparing comparison… previous snapshot remains visible"
	}
	if s.errText != "" {
		status = s.errText
		role = ui.ColorError
	}
	if search := s.viewer.SearchStatus(); search != "" && !s.loading && s.errText == "" {
		status = search
	}
	if !s.loading && s.errText == "" && s.viewer.HunkCount() == 0 && s.viewer.SearchStatus() == "" {
		status = "No textual differences · " + status
	}
	row := s.viewer.Bounds().Y - 1
	if row > 0 && row < r.H {
		buf.WriteString(ox+r.X, oy+r.Y+row, rendering.TruncateVisual(status, r.W, "…"), s.tokens.Color(role), s.tokens.Color(ui.ColorBg), 0)
	}
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
		if s.moreOpen {
			s.moreOpen = false
			if s.focus != nil {
				s.focus(s.actions.ID())
			}
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
		}
		if s.searchOpen {
			s.searchOpen = false
			if s.focus != nil {
				s.focus(s.DocumentID())
			}
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
		}
		return s.action(ActionClose)
	case "ctrl+r":
		return s.action(ActionRefresh)
	case "f3", "shift+f3":
		s.viewer.NextMatch(e.Key.Mod.Contains(tea.ModShift))
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
	case "ctrl+f":
		return s.find()
	case "tab":
		if s.focus != nil {
			s.focus(s.actions.ID())
		}
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus}
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
func (s *Screen) find() ui.EventResult {
	s.searchOpen = true
	if s.focus != nil {
		s.focus(s.search.ID())
	}
	return ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidateFocus | ui.InvalidatePaint}
}
func (s *Screen) HandleAction(ctx ui.EventContext, a ui.Action) ui.EventResult {
	result := ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
	switch a.ID {
	case "localdiff.more":
		s.moreOpen = !s.moreOpen
		if s.focus != nil {
			if s.moreOpen {
				s.focus(s.more.ID())
			} else {
				s.focus(s.actions.ID())
			}
		}
		return result
	case "localdiff.more.activate":
		if item, ok := a.Payload.(ui.ListItem); ok {
			s.moreOpen = false
			if s.focus != nil {
				s.focus(s.DocumentID())
			}
			r := s.HandleAction(ctx, ui.Action{ID: item.ID})
			r.Invalidate |= ui.InvalidateLayout | ui.InvalidatePaint
			return r
		}
	case ActionRefresh, ActionOpenSource, ActionClose:
		return s.action(a.ID)
	case "localdiff.find":
		return s.find()
	case "localdiff.presentation":
		p := diffview.SideBySide
		if s.viewer.PreferredPresentation() == p {
			p = diffview.Unified
		}
		s.viewer.SetPresentation(p)
	case "localdiff.previous":
		s.viewer.NavigateHunk(-1)
	case "localdiff.next":
		s.viewer.NavigateHunk(1)
	case "localdiff.match":
		s.viewer.NextMatch(false)
	case "actionbar.exit.down", "actionbar.exit.up":
		if s.focus != nil {
			s.focus(s.DocumentID())
		}
	default:
		return ui.EventResult{Action: &a}
	}
	return result
}
