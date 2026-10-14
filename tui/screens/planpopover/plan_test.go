package planpopover

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types/activity"
)

func planFixture(count int) *activity.Plan {
	p := &activity.Plan{ID: "plan", Revision: 1, Objective: "Repair the chat status", Status: "active", Total: count, Completed: 1}
	for i := range count {
		status := "pending"
		switch i {
		case 0:
			status = "completed"
		case 1:
			status = "in_progress"
		}
		p.Steps = append(p.Steps, activity.Step{Number: i + 1, Status: status, Text: fmt.Sprintf("Task %02d — inspect status and retain accepted work", i+1)})
	}
	return p
}

func bufferText(buf *surface.CellBuffer) string {
	var b strings.Builder
	for y := range buf.Height() {
		for x := range buf.Width() {
			c := buf.Get(x, y)
			if c.Width > 0 && c.Char != 0 {
				b.WriteRune(c.Char)
			}
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func TestPlanPopoverFitsWrapsAndScrollsAllSteps(t *testing.T) {
	for _, th := range []*theme.Theme{theme.DefaultDark(), theme.DefaultLight()} {
		for _, width := range []int{24, 48, 110} {
			t.Run(fmt.Sprintf("%s/%d", th.Name, width), func(t *testing.T) {
				p := planFixture(16)
				p.Steps[15].Text = "终点👩‍💻 TAIL"
				s := New(th, p)
				size := s.PreferredSize(width, 12)
				if size.W > width || size.H > 12 {
					t.Fatalf("popover exceeds supplied geometry: %+v", size)
				}
				w := ui.NewWindowFrameWidget(th, "test/frame", ui.FramePopover, "Plan", s, nil, false, size.W, size.H)
				w.Focus(s.ID())
				paint := func() string {
					buf := surface.NewCellBuffer(size.W, size.H)
					w.Host().Layout()
					w.Host().Paint(buf)
					return bufferText(buf)
				}
				first := paint()
				if !strings.Contains(first, "Repair") || strings.Contains(first, "TAIL") {
					t.Fatalf("wrong initial viewport: %q", first)
				}
				w.Dispatch(tea.KeyPressMsg{Code: tea.KeyEnd})
				if last := paint(); !strings.Contains(last, "TAIL") || last == first {
					t.Fatalf("End did not paint last step: %q", last)
				}
				before := s.list.Scroll().Offset()
				w.Dispatch(tea.MouseWheelMsg{X: 4, Y: 3, Button: tea.MouseWheelUp})
				if s.list.Scroll().Offset() >= before || strings.Contains(paint(), "TAIL") {
					t.Fatal("wheel did not move the painted reading viewport")
				}
				if !s.scrollbar.Needed() {
					t.Fatal("long plan has no native scrollbar")
				}
				w.Focus(s.ID())
				if result := w.Dispatch(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}); result.Cmd == nil {
					t.Fatal("read-only plan does not expose copy")
				}
				if p.Revision != 1 || len(p.Steps) != 16 || p.Steps[0].Status != "completed" {
					t.Fatal("inspection mutated the immutable plan")
				}
			})
		}
	}
}

func TestPlanPopoverShortPlanUsesContentHeight(t *testing.T) {
	s := New(theme.DefaultDark(), planFixture(3))
	size := s.PreferredSize(160, 50)
	if size.H > 10 || size.W > 80 {
		t.Fatalf("short plan has oversized modal: %+v", size)
	}
}

// The plan popover keeps its design and gains chat's text selection: a drag
// highlights painted rows with the text-selection colors, release copies the
// exact plain text, and the highlight never outlives the gesture.
func TestPlanPopoverDragSelectsAndCopiesText(t *testing.T) {
	th := theme.DefaultDark()
	screen := New(th, planFixture(3))
	host := ui.NewHost(th, screen)
	host.Resize(60, 8)
	host.Layout()
	paint := func() *surface.CellBuffer {
		buf := surface.NewCellBuffer(60, 8)
		host.Paint(buf)
		return buf
	}
	text := bufferText(paint())
	if !strings.Contains(text, "Repair the chat status") {
		t.Fatalf("plan text missing:\n%s", text)
	}
	row := -1
	for y, line := range strings.Split(text, "\n") {
		if strings.Contains(line, "Task 02") {
			row = y
		}
	}
	if row < 0 {
		t.Fatalf("no Task 02 row:\n%s", text)
	}
	click := host.Handle(tea.MouseClickMsg{X: 0, Y: 0, Button: tea.MouseLeft})
	if !click.Handled || !host.HasPointerCapture() {
		t.Fatalf("press did not start a captured selection: %+v", click)
	}
	host.Handle(tea.MouseMotionMsg{X: 59, Y: row, Button: tea.MouseLeft})
	selectionBG := ui.NewTokenSet(th).Color(ui.ColorTextSelection)
	if buf := paint(); buf.Get(2, 0).BG != selectionBG || buf.Get(2, row).BG != selectionBG {
		t.Fatal("drag did not paint the text-selection highlight")
	}
	selected := screen.selectedText()
	if !strings.HasPrefix(selected, "Repair the chat status") || !strings.Contains(selected, "Task 02 — inspect status") || strings.Contains(selected, "Task 03") {
		t.Fatalf("selected text = %q", selected)
	}
	release := host.Handle(tea.MouseReleaseMsg{X: 59, Y: row, Button: tea.MouseLeft})
	if release.Cmd == nil {
		t.Fatal("release did not copy the selection")
	}
	if buf := paint(); buf.Get(2, 0).BG == selectionBG || host.HasPointerCapture() {
		t.Fatal("selection or capture outlived the gesture")
	}
	// A plain click without a drag copies nothing.
	host.Handle(tea.MouseClickMsg{X: 3, Y: 1, Button: tea.MouseLeft})
	if result := host.Handle(tea.MouseReleaseMsg{X: 3, Y: 1, Button: tea.MouseLeft}); result.Cmd != nil {
		t.Fatal("a click without a drag replaced the clipboard")
	}
}
