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
	selection textSelection
}

// textSelection is a pointer drag over painted plan rows. Like chat, the text
// is copied on release and the highlight never outlives the gesture.
type textSelection struct {
	active                                 bool
	anchorLine, anchorCol, endLine, endCol int
}

func (t textSelection) ordered() (startLine, startCol, endLine, endCol int) {
	if t.anchorLine < t.endLine || t.anchorLine == t.endLine && t.anchorCol <= t.endCol {
		return t.anchorLine, t.anchorCol, t.endLine, t.endCol
	}
	return t.endLine, t.endCol, t.anchorLine, t.anchorCol
}

// columns returns the highlighted column span of one row, or 0,0.
func (t textSelection) columns(line int) (start, end int) {
	if !t.active {
		return 0, 0
	}
	startLine, startCol, endLine, endCol := t.ordered()
	if line < startLine || line > endLine {
		return 0, 0
	}
	start, end = 0, 1<<30
	if line == startLine {
		start = startCol
	}
	if line == endLine {
		end = endCol
	}
	return start, end
}

func New(th *theme.Theme, plan *activity.Plan) *Screen {
	tokens := ui.NewTokenSet(th)
	s := &Screen{Base: ui.NewBase(ScreenID), plan: plan, width: -1}
	s.list = ui.NewVirtualList(ui.NewID(ScreenID, "rows"), tokens)
	s.list.SetOpaqueRowFill(false)
	s.list.SetSelectionAnchorsScroll(false)
	s.list.SetPointerPolicy(ui.PointerPolicyPassthrough)
	s.list.SetRowPainter(func(buf *surface.CellBuffer, x, y, width int, item ui.VirtItem, _ int, _ ui.ControlState, _ ui.PaintContext, tokens *ui.TokenSet) {
		row, _ := item.Payload.(planRow)
		start, end := s.selection.columns(row.index)
		chatprojection.PaintLineAtBackground(buf, x, y, width, row.line, tokens, start, end, tokens.Color(ui.ColorPopupBg))
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
			items[i] = ui.VirtItem{ID: fmt.Sprint(i), Height: 1, Payload: planRow{index: i, line: row}}
		}
		// Rewrapping changes row coordinates; a drag cannot span the change.
		s.selection = textSelection{}
		offset := s.list.Scroll().Offset()
		s.list.SetItems(items)
		s.list.Scroll().ScrollTo(offset)
	}
	s.list.Layout(ctx, ui.Rect{W: w, H: r.H})
	s.scrollbar.Layout(ctx, ui.Rect{X: max(0, r.W-1), W: min(1, r.W), H: r.H})
}

type planRow struct {
	index int
	line  chatprojection.PaintLine
}

// lineAt maps a popover-local row to a painted plan row, clamping drags that
// leave the viewport by scrolling one row toward the pointer.
func (s *Screen) lineAt(localX, localY int) (line, col int, ok bool) {
	scroll := s.list.Scroll()
	if len(s.rows) == 0 {
		return 0, 0, false
	}
	col = max(0, localX)
	switch {
	case localY < 0:
		scroll.ScrollBy(-1)
		return scroll.Offset(), 0, true
	case localY >= scroll.Viewport():
		scroll.ScrollBy(1)
		return min(len(s.rows)-1, scroll.Offset()+scroll.Viewport()-1), 1 << 30, true
	}
	line = scroll.Offset() + localY
	if line >= len(s.rows) {
		return len(s.rows) - 1, 1 << 30, true
	}
	return line, col, true
}

func (s *Screen) selectedText() string {
	lines := make([]string, len(s.rows))
	for i, row := range s.rows {
		lines[i] = row.PlainText()
	}
	startLine, startCol, endLine, endCol := s.selection.ordered()
	return strings.TrimRight(rendering.ExtractSelectedText(lines, startLine, startCol, endLine, endCol), " \t\r\n")
}

// CancelPointerCapture drops a drag that lost the pointer without a release.
func (s *Screen) CancelPointerCapture() { s.selection = textSelection{} }

func (s *Screen) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	scroll := s.list.Scroll()
	switch evt.Kind {
	case ui.EventMouseClick:
		if evt.MouseClick.Button != tea.MouseLeft || ctx.LocalX >= s.width {
			return ui.EventResult{Handled: true}
		}
		line, col, ok := s.lineAt(ctx.LocalX, ctx.LocalY)
		if !ok {
			return ui.EventResult{Handled: true}
		}
		s.selection = textSelection{active: true, anchorLine: line, anchorCol: col, endLine: line, endCol: col}
		// Capture keeps the drag alive when it crosses the popover edge.
		return ui.EventResult{Handled: true, Action: &ui.Action{ID: "ui.capture"}, Invalidate: ui.InvalidatePaint}
	case ui.EventMouseMotion:
		if !s.selection.active {
			return ui.EventResult{}
		}
		if line, col, ok := s.lineAt(ctx.LocalX, ctx.LocalY); ok {
			s.selection.endLine, s.selection.endCol = line, col
		}
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
	case ui.EventMouseRelease:
		if !s.selection.active {
			return ui.EventResult{}
		}
		if line, col, ok := s.lineAt(ctx.LocalX, ctx.LocalY); ok && ctx.LocalY >= 0 && ctx.LocalY < scroll.Viewport() {
			s.selection.endLine, s.selection.endCol = line, col
		}
		text := ""
		if s.selection.anchorLine != s.selection.endLine || s.selection.anchorCol != s.selection.endCol {
			text = s.selectedText()
		}
		s.selection = textSelection{}
		result := ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
		if text != "" {
			result.Cmd = rendering.CopyToClipboardCmd(text)
		}
		return result
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
		return ui.EventResult{}
	}
	return ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
}

func (s *Screen) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleDialog, Label: "Current plan"}
}
