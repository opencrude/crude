package shellchat

import (
	tea "charm.land/bubbletea/v2"
	"crude/tui/chatprojection"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
	"fmt"
	"strings"
	"testing"
)

// Fixed slots deliberately exercise parent-local paint and pointer translation.
type latestTestSlots struct {
	ui.Base
	screens []*Screen
	rects   []ui.Rect
}

func (r *latestTestSlots) Children() []ui.Component {
	out := make([]ui.Component, len(r.screens))
	for i, s := range r.screens {
		out[i] = s
	}
	return out
}
func (r *latestTestSlots) Measure(_ ui.LayoutContext, c ui.Constraints) ui.Size {
	return ui.Size{W: c.MaxW, H: c.MaxH}
}
func (r *latestTestSlots) Layout(c ui.LayoutContext, b ui.Rect) {
	r.SetBounds(b)
	for i, s := range r.screens {
		s.Layout(c, r.rects[i])
	}
}
func (r *latestTestSlots) Paint(*surface.CellBuffer, int, int, ui.PaintContext) {}
func (r *latestTestSlots) Handle(ui.EventContext, ui.Event) ui.EventResult      { return ui.EventResult{} }

func latestFixture(th *theme.Theme, id string, w, h, n int) (*Screen, PaintParams, []chatprojection.MessageRow) {
	s := NewWithID(th, ui.ComponentID(id))
	lines := make([]string, n)
	for i := range lines {
		lines[i] = fmt.Sprintf("row%03d", i)
	}
	if n > 0 {
		lines[n-1] = "TAIL"
	}
	rows := []chatprojection.MessageRow{{ID: id + "message", Lines: lines}}
	paint := PaintParams{Compose: ComposeParams{InnerW: max(0, w-2*th.Spacing.PadLR), PadLR: th.Spacing.PadLR, Theme: th, MainSBVisible: n > h-2}}
	s.SetMessages(rows, paint)
	s.SetActivity(types.AgentActivityView{Status: types.AgentStatusSnapshot{Detail: "Ready"}})
	return s, paint, rows
}
func latestPaint(host *ui.Host, w, h int) *surface.CellBuffer {
	host.MarkLayout()
	host.Layout()
	b := surface.NewCellBuffer(w, h)
	host.Paint(b)
	return b
}

func TestLatestHostPaintClickAndStreamFollow(t *testing.T) {
	for _, th := range []*theme.Theme{theme.DefaultDark(), theme.DefaultLight()} {
		for _, w := range []int{40, 9} {
			t.Run(fmt.Sprintf("%s/%d", th.Name, w), func(t *testing.T) {
				const h = 8
				const ox = 7
				const oy = 3
				s, paint, rows := latestFixture(th, "owner", w, h, 60)
				root := &latestTestSlots{Base: ui.NewBase("slots"), screens: []*Screen{s}, rects: []ui.Rect{{X: ox, Y: oy, W: w, H: h}}}
				host := ui.NewHost(th, root)
				host.Resize(ox+w+3, oy+h+2)
				latestPaint(host, ox+w+3, oy+h+2)
				originalHeight := s.ScrollModel().Content()
				viewport := s.ScrollModel().Viewport()
				host.Handle(tea.MouseWheelMsg{X: ox + 3, Y: oy + 2, Button: tea.MouseWheelUp})
				b := latestPaint(host, ox+w+3, oy+h+2)
				br := s.jumpLatest.Bounds()
				if !s.latestVisible() || br.Y != h-2 || br.X != (w-br.W)/2 {
					t.Fatalf("not centered in reserved band: %+v", br)
				}
				label := "[ ↓ Latest ]"
				if w < 12 {
					label = "[ ↓ ]"
				}
				if got := PlainRow(b, oy+h-2, b.Width()); !strings.Contains(got, label) {
					t.Fatalf("button not painted at translated origin: %q", got)
				}
				ink := b.Get(ox+br.X, oy+br.Y)
				if ink.FG != ui.NewTokenSet(th).Color(ui.ColorAccent2) || ink.FG == ink.BG {
					t.Fatal("Latest label is invisible against the chat background")
				}
				if s.ScrollModel().Content() != originalHeight || s.ScrollModel().Viewport() != viewport || viewport != h-2 {
					t.Fatal("Latest added transcript height")
				}
				host.Handle(tea.MouseClickMsg{X: ox + br.X + 2, Y: oy + br.Y, Button: tea.MouseLeft})
				host.Handle(tea.MouseReleaseMsg{X: ox + br.X + 2, Y: oy + br.Y, Button: tea.MouseLeft})
				b = latestPaint(host, ox+w+3, oy+h+2)
				if !s.ScrollModel().AutoScroll() || !strings.Contains(PlainRow(b, oy+h-3, b.Width()), "TAIL") || s.latestVisible() {
					t.Fatal("click did not paint actual tail and arm follow")
				}
				rows[0].Lines = append(rows[0].Lines, "NEWTAIL")
				s.SetMessages(rows, paint)
				b = latestPaint(host, ox+w+3, oy+h+2)
				// Very narrow surfaces clip the text, but retain the new final row.
				expected := "NEWTAIL"
				if w < 12 {
					expected = "NEWT"
				}
				if got := PlainRow(b, oy+h-3, b.Width()); !strings.Contains(got, expected) {
					t.Fatalf("append not followed: %q", got)
				}
				s.ScrollModel().SetAutoScroll(false)
				s.ScrollModel().ScrollTo(0)
				latestPaint(host, ox+w+3, oy+h+2)
				host.SetTheme(theme.DefaultLight())
				b = latestPaint(host, ox+w+3, oy+h+2)
				c := b.Get(ox+s.jumpLatest.Bounds().X, oy+h-2)
				if c.BG != ui.NewTokenSet(theme.DefaultLight()).Color(ui.ColorBg) {
					t.Fatal("theme did not propagate to Latest surface")
				}
			})
		}
	}
}

func TestLatestVisibilityAndOwnerIsolation(t *testing.T) {
	th := theme.DefaultDark()
	for _, tc := range []struct {
		w, h, n int
		up      bool
	}{{40, 8, 60, false}, {40, 8, 1, true}, {40, 2, 60, true}, {4, 8, 60, true}, {40, 1, 60, true}} {
		s, _, _ := latestFixture(th, "absence", tc.w, tc.h, tc.n)
		host := ui.NewHost(th, s)
		host.Resize(tc.w, tc.h)
		latestPaint(host, tc.w, tc.h)
		if tc.up {
			s.ScrollModel().SetAutoScroll(false)
			s.ScrollModel().ScrollTo(0)
		}
		b := latestPaint(host, tc.w, tc.h)
		if s.latestVisible() || strings.Contains(PlainRow(b, max(0, tc.h-2), tc.w), "Latest") {
			t.Fatalf("unexpected control %+v", tc)
		}
	}
	left, _, _ := latestFixture(th, "left", 40, 8, 60)
	right, _, _ := latestFixture(th, "right", 40, 8, 60)
	root := &latestTestSlots{Base: ui.NewBase("slots"), screens: []*Screen{left, right}, rects: []ui.Rect{{X: 3, Y: 2, W: 40, H: 8}, {X: 46, Y: 2, W: 40, H: 8}}}
	host := ui.NewHost(th, root)
	host.Resize(90, 12)
	latestPaint(host, 90, 12)
	for _, s := range []*Screen{left, right} {
		s.ScrollModel().SetAutoScroll(false)
		s.ScrollModel().ScrollTo(0)
	}
	latestPaint(host, 90, 12)
	host.Focus(right.ID())
	before := right.ScrollModel().Offset()
	br := left.jumpLatest.Bounds()
	host.Handle(tea.MouseClickMsg{X: 3 + br.X + 2, Y: 2 + br.Y, Button: tea.MouseLeft})
	host.Handle(tea.MouseReleaseMsg{X: 3 + br.X + 2, Y: 2 + br.Y, Button: tea.MouseLeft})
	b := latestPaint(host, 90, 12)
	if !left.ScrollModel().AutoScroll() || right.ScrollModel().AutoScroll() || right.ScrollModel().Offset() != before || !strings.Contains(PlainRow(b, 7, 43), "TAIL") {
		t.Fatal("Latest mutated wrong screen or did not paint owning tail")
	}
}

func TestLatestBandCannotStealCapturedGestures(t *testing.T) {
	for _, gesture := range []string{"selection", "scrollbar"} {
		t.Run(gesture, func(t *testing.T) {
			th := theme.DefaultDark()
			s, _, _ := latestFixture(th, "capture", 40, 12, 200)
			host := ui.NewHost(th, s)
			host.Resize(40, 12)
			latestPaint(host, 40, 12)
			s.ScrollModel().SetAutoScroll(false)
			s.ScrollModel().ScrollTo(0)
			latestPaint(host, 40, 12)
			x, y := 3, 2
			if gesture == "scrollbar" {
				br := s.scrollbar.Bounds()
				x = br.X
				y = br.Y
			}
			host.Handle(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
			if !host.HasPointerCapture() {
				t.Fatal("fixture did not capture")
			}
			br := s.jumpLatest.Bounds()
			host.Handle(tea.MouseMotionMsg{X: br.X + 2, Y: br.Y, Button: tea.MouseLeft})
			owner, _, captured := host.CapturedPointer()
			if !captured || owner == s.jumpLatest.ID() {
				t.Fatal("Latest stole capture")
			}
			beforeRelease := s.ScrollModel().Offset()
			host.Handle(tea.MouseReleaseMsg{X: br.X + 2, Y: br.Y, Button: tea.MouseLeft})
			if s.ScrollModel().Offset() != beforeRelease || host.HasPointerCapture() || (gesture == "selection" && s.ScrollModel().AutoScroll()) {
				t.Fatal("captured gesture activated Latest")
			}
		})
	}
}
