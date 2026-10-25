// Package connectionpopover renders the shell-to-core link's live details.
package connectionpopover

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/theme"
	"crude/tui/ui"
)

const ScreenID ui.ComponentID = "connection-health/body"
const DetailID ui.ComponentID = "connection-health/detail"
const ActionsID ui.ComponentID = "connection-health/actions"

type Projection struct {
	Summary string
	Role    ui.SemanticColor
	Detail  string
}

type ActionMsg struct {
	Screen *Screen
	Action string
}

type Screen struct {
	ui.Base
	projection Projection
	tokens     *ui.TokenSet
	summary    *ui.Label
	detail     *detailArea
	actions    *ui.ActionBar
}

func New(th *theme.Theme, p Projection) *Screen {
	tokens := ui.NewTokenSet(th)
	s := &Screen{Base: ui.NewBase(ScreenID)}
	s.summary = ui.NewLabel("connection-health/summary", "", th)
	s.detail = &detailArea{TextArea: ui.NewTextArea(DetailID, tokens, true)}
	s.detail.SetReadOnly(true)
	s.actions = ui.NewActionBar(ActionsID, tokens,
		ui.ActionBarButton{Label: "Connections", Action: "connections", Enabled: true, Variant: ui.VariantSecondary},
		ui.ActionBarButton{Label: "Close", Action: "close", Enabled: true, Variant: ui.VariantSecondary})
	s.actions.SetShowRule(true)
	s.actions.SetSurfaceBackground(ui.ColorPopupBg)
	s.actions.SetCancelAction("close")
	s.SetProjection(p)
	s.SetTokens(tokens)
	return s
}

func (s *Screen) SetTokens(tokens *ui.TokenSet) {
	s.tokens = tokens
	s.summary.SetStyle(tokens.Color(s.projection.Role), cell.NoColor, cell.AttrBold)
	s.detail.SetTokens(tokens)
	s.actions.SetTokens(tokens)
}

func (s *Screen) SetProjection(p Projection) bool {
	if s.projection == p {
		return false
	}
	s.projection = p
	s.summary.SetText(p.Summary)
	if s.tokens != nil {
		s.summary.SetStyle(s.tokens.Color(p.Role), cell.NoColor, cell.AttrBold)
	}
	if s.detail.Value() != p.Detail {
		cursor, scroll := s.detail.CursorFlat(), s.detail.ScrollOffset()
		s.detail.SetValue(p.Detail)
		s.detail.SetCursorFlat(cursor)
		s.detail.SetScrollOffset(scroll)
	}
	return true
}

// PreferredSize includes the shared frame's six horizontal chrome columns and
// two border rows. Long evidence stays in the native scrollable reader.
func (s *Screen) PreferredSize(maxW, maxH int) ui.Size {
	w := max(0, min(maxW, 58))
	rows := len(rendering.WrapText(s.projection.Detail, max(1, w-6)))
	return ui.Size{W: w, H: max(0, min(maxH, rows+7))}
}

func (s *Screen) Children() []ui.Component {
	return []ui.Component{s.summary, s.detail, s.actions}
}

func (s *Screen) Layout(ctx ui.LayoutContext, r ui.Rect) {
	s.SetBounds(r)
	barH := min(r.H, s.actions.Measure(ctx, ui.Constraints{MaxW: r.W, MaxH: r.H}).H)
	bodyH := max(0, r.H-barH)
	s.summary.Layout(ctx, ui.Rect{W: r.W, H: min(1, bodyH)})
	y := min(2, bodyH)
	s.detail.Layout(ctx, ui.Rect{Y: y, W: r.W, H: max(0, bodyH-y-1)})
	s.actions.Layout(ctx, ui.Rect{Y: bodyH, W: r.W, H: barH})
}

func (s *Screen) Handle(ctx ui.EventContext, e ui.Event) ui.EventResult {
	if e.IsKey() {
		if e.Key.Code == tea.KeyEsc {
			return s.HandleAction(ctx, ui.Action{ID: "close"})
		}
		if e.Key.Code == tea.KeyTab {
			return ui.EventResult{Handled: true, Action: &ui.Action{ID: "ui.focus", Payload: ActionsID}}
		}
		return s.actions.Handle(ctx, e)
	}
	return ui.EventResult{}
}

func (s *Screen) HandleAction(_ ui.EventContext, a ui.Action) ui.EventResult {
	switch a.ID {
	case "close", "connections":
		return ui.EventResult{Handled: true, Cmd: func() tea.Msg { return ActionMsg{Screen: s, Action: a.ID} }}
	case "actionbar.exit.backward", "actionbar.exit.forward":
		return ui.EventResult{Handled: true, Action: &ui.Action{ID: "ui.focus", Payload: DetailID}}
	}
	return ui.EventResult{}
}

type detailArea struct{ *ui.TextArea }

func (d *detailArea) Handle(ctx ui.EventContext, e ui.Event) ui.EventResult {
	if e.IsKey() && e.Key.Code == tea.KeyTab {
		return ui.EventResult{Handled: true, Action: &ui.Action{ID: "ui.focus", Payload: ActionsID}}
	}
	return d.TextArea.Handle(ctx, e)
}

func (s *Screen) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleGroup, Label: "Connection to core", Description: strings.TrimSpace(s.projection.Summary)}
}
