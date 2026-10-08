// Package planpopover provides a read-only, bounded view of the current plan.
package planpopover

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/tui/chatprojection"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types/activity"
)

const ScreenID ui.ComponentID = "plan-hover/body"

type Screen struct {
	ui.Base
	plan      *activity.Plan
	list      *ui.VirtualList
	scrollbar *ui.ScrollbarControl
	width     int
	rows      []chatprojection.PaintLine
}

func New(th *theme.Theme, plan *activity.Plan) *Screen {
	tokens := ui.NewTokenSet(th)
	s := &Screen{Base: ui.NewBase(ScreenID), plan: plan, width: -1}
	s.list = ui.NewVirtualList(ui.NewID(ScreenID, "rows"), tokens)
	s.list.SetOpaqueRowFill(false)
	s.list.SetSelectionAnchorsScroll(false)
	s.list.SetPointerPolicy(ui.PointerPolicyPassthrough)
	s.list.SetRowPainter(func(buf *surface.CellBuffer, x, y, width int, item ui.VirtItem, _ int, _ ui.ControlState, _ ui.PaintContext, tokens *ui.TokenSet) {
		line, _ := item.Payload.(chatprojection.PaintLine)
		chatprojection.PaintLineAtBackground(buf, x, y, width, line, tokens, 0, 0, tokens.Color(ui.ColorPopupBg))
	})
	s.scrollbar = ui.NewScrollbarControl(ui.NewID(ScreenID, "scrollbar"), s.list.Scroll(), tokens)
	s.scrollbar.SetPointerTargetSize(1)
	s.scrollbar.SetSurfaceBackground(ui.ColorPopupBg)
	return s
}

func (s *Screen) SetPlan(p *activity.Plan) {
	if s.plan != p {
		s.plan, s.width = p, -1
	}
}

// PreferredSize measures wrapped rows, not the count of unwrapped steps. All
// dimensions remain within supplied terminal space; overflow is scrollable.
func (s *Screen) PreferredSize(maxW, maxH int) ui.Size {
	w := min(maxW, 80)
	if s.plan != nil {
		preferred := rendering.VisibleWidth(s.plan.Objective)
		for _, step := range s.plan.Steps {
			preferred = max(preferred, rendering.VisibleWidth(step.Text)+8)
		}
		w = min(w, max(32, preferred+8))
	}
	w = max(0, w)
	rows := chatprojection.PlanActivityRows(s.plan, max(1, w-8))
	return ui.Size{W: w, H: min(maxH, len(rows)+2)}
}

func (s *Screen) Children() []ui.Component {
	if s.list.Scroll().MaxOffset() > 0 {
		return []ui.Component{s.list, s.scrollbar}
	}
	return []ui.Component{s.list}
}

func (s *Screen) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
	return cs.ClampSize(ui.Size{W: cs.MaxW, H: cs.MaxH})
}

func (s *Screen) Layout(ctx ui.LayoutContext, r ui.Rect) {
	s.SetBounds(r)
	// A stable gutter keeps wrap/height and scrollbar visibility independent.
	w := max(0, r.W-2)
	if s.width != w {
		s.width = w
		s.rows = chatprojection.PlanActivityRows(s.plan, w)
		items := make([]ui.VirtItem, len(s.rows))
		for i, row := range s.rows {
			items[i] = ui.VirtItem{ID: fmt.Sprint(i), Height: 1, Payload: row}
		}
		offset := s.list.Scroll().Offset()
		s.list.SetItems(items)
		s.list.Scroll().ScrollTo(offset)
	}
	s.list.Layout(ctx, ui.Rect{W: w, H: r.H})
	s.scrollbar.Layout(ctx, ui.Rect{X: max(0, r.W-1), W: min(1, r.W), H: r.H})
}

func (s *Screen) Handle(_ ui.EventContext, evt ui.Event) ui.EventResult {
	scroll := s.list.Scroll()
	switch evt.Kind {
	case ui.EventMouseWheel:
		switch evt.MouseWheel.Button {
		case tea.MouseWheelUp:
			scroll.ScrollBy(-3)
		case tea.MouseWheelDown:
			scroll.ScrollBy(3)
		}
	case ui.EventKey:
		if ui.IsCopyChord(evt.Key) {
			var lines []string
			for _, line := range chatprojection.PlanActivityRows(s.plan, 10000) {
				lines = append(lines, line.PlainText())
			}
			return ui.EventResult{Handled: true, Cmd: rendering.CopyToClipboardCmd(strings.Join(lines, "\n"))}
		}
		switch evt.Key.Code {
		case tea.KeyUp:
			scroll.ScrollBy(-1)
		case tea.KeyDown:
			scroll.ScrollBy(1)
		case tea.KeyPgUp:
			scroll.ScrollBy(-max(1, scroll.Viewport()-1))
		case tea.KeyPgDown:
			scroll.ScrollBy(max(1, scroll.Viewport()-1))
		case tea.KeyHome:
			scroll.ScrollTo(0)
		case tea.KeyEnd:
			scroll.ScrollTo(scroll.MaxOffset())
		default:
			return ui.EventResult{}
		}
	default:
		return ui.EventResult{Handled: evt.Kind == ui.EventMouseClick}
	}
	return ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
}

func (s *Screen) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleDialog, Label: "Current plan"}
}
