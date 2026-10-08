package shelltabs

import (
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"
	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/tui/window"
)

func TestNewSessionButtonNativeHostBoundsAndOverflow(t *testing.T) {
	for _, count := range []int{0, 1, 30} {
		for _, width := range []int{0, 1, 2, 3, 4, 7, 12, 30, 80, 400} {
			t.Run(fmt.Sprintf("%d_tabs_%d_columns", count, width), func(t *testing.T) {
				s := NewWithID(nil, "group/2")
				s.SetNewButton(true)
				s.SetDraggable(true)
				tabs := make([]window.Tab, count)
				for i := range tabs {
					tabs[i] = window.Tab{ID: window.TabID(i + 1), Title: fmt.Sprintf("File%d", i), Closable: true}
				}
				s.SetProjectionFromTabs(tabs, 1, false)
				h := ui.NewHost(theme.DefaultDark(), s)
				h.Resize(width, 2)
				h.Layout()
				if s.ID() != "group/2" || s.tabs.ID() != "group/2/tabs" {
					t.Fatal("custom IDs lost")
				}
				r := s.NewButtonBounds()
				if r.X < 0 || r.X+r.W > width || (width > 0 && r.H != 1) {
					t.Fatalf("button exceeds host: %+v width=%d", r, width)
				}
				if width == 0 {
					if !r.Empty() {
						t.Fatal("zero width must hide action")
					}
					return
				}
				buf := surface.NewCellBuffer(width+5, 3)
				sentinel := cell.NewCell('Z', cell.NoColor, cell.NoColor, 0)
				for x := width; x < width+5; x++ {
					buf.Set(x, 0, sentinel)
					buf.Set(x, 1, sentinel)
				}
				h.Paint(buf)
				if got := buf.Get(r.X+(r.W-1)/2, 0).Char; got != '+' {
					t.Fatalf("plus missing: %q bounds=%+v", got, r)
				}
				for x := 0; x < width; x++ {
					if c := buf.Get(x, 1).Char; c != '─' && c != '━' {
						t.Fatalf("baseline broken at %d: %q", x, c)
					}
				}
				for x := width; x < width+5; x++ {
					if buf.Get(x, 0).Char != 'Z' || buf.Get(x, 1).Char != 'Z' {
						t.Fatalf("paint escaped width at %d", x)
					}
				}
				if count == 30 && width == 30 && r.X+r.W != width {
					t.Fatal("overflow new button must remain pinned")
				}
				res := h.Handle(tea.MouseClickMsg{X: r.X, Y: 0, Button: tea.MouseLeft})
				if res.Action == nil || res.Action.ID != "tabs.new" {
					t.Fatalf("native click: %+v", res)
				}
				res = h.Handle(tea.MouseMotionMsg{X: width + 5, Y: 5, Button: tea.MouseLeft})
				if res.Action != nil || s.tabs.Dragging() {
					t.Fatal("new button must never drag a tab")
				}
				h.Handle(tea.MouseReleaseMsg{X: width + 5, Y: 5, Button: tea.MouseLeft})
				if h.HasPointerCapture() {
					t.Fatal("new button capture leaked")
				}
			})
		}
	}
}

func TestNewSessionButtonPointerHoverPaintsNoHighlight(t *testing.T) {
	for width := 1; width <= 80; width++ {
		s := New(nil)
		s.SetNewButton(true)
		s.SetProjection([]TabProjection{{ID: "1", Label: "Editor", Closable: true}}, "1", -1, false)
		h := ui.NewHost(theme.DefaultDark(), s)
		h.Resize(width, ui.StripHeight)
		h.Layout()
		r := s.NewButtonBounds()
		if width >= 5 && r.W < 5 {
			t.Fatalf("width %d: uncomfortable action width %d", width, r.W)
		}
		h.Handle(tea.MouseMotionMsg{X: r.X, Y: 0})
		buf := surface.NewCellBuffer(width, 2)
		h.Paint(buf)
		tokens := ui.NewTokenSet(theme.DefaultDark())
		// Hover carries no treatment of its own: neither row changes, so the
		// action can never read as a block hanging past the bar's bottom rule.
		for x := r.X; x < r.X+r.W; x++ {
			if c := buf.Get(x, 0); c.BG != tokens.Color(ui.ColorBg) {
				t.Fatalf("width %d: hover painted the label band at %d: %+v", width, x, c)
			}
			if c := buf.Get(x, 1); c.BG != tokens.Color(ui.ColorBg) || c.Char != '─' {
				t.Fatalf("width %d: hover touched the separator at %d: %+v", width, x, c)
			}
		}
		res := h.Handle(tea.MouseClickMsg{X: r.X + r.W - 1, Y: 0, Button: tea.MouseLeft})
		if res.Action == nil || res.Action.ID != "tabs.new" {
			t.Fatalf("width %d: label band not actionable: %+v", width, res)
		}
		h.CancelPointerCapture()
		h.Focus(s.newButton.ID())
		h.Paint(buf)
		for x := r.X; x < r.X+r.W; x++ {
			if buf.Get(x, 0).BG != tokens.Color(ui.ColorAccent) {
				t.Fatalf("width %d: focus does not fill label band at %d", width, x)
			}
			if c := buf.Get(x, 1); c.BG != tokens.Color(ui.ColorBg) || c.Char != '─' {
				t.Fatalf("width %d: focus repainted separator at %d: %+v", width, x, c)
			}
		}
		if res := h.Handle(tea.KeyPressMsg{Code: tea.KeyEnter}); res.Action == nil || res.Action.ID != "tabs.new" {
			t.Fatal("native keyboard activation lost")
		}
		res = h.Handle(tea.MouseClickMsg{X: r.X + r.W - 1, Y: 1, Button: tea.MouseLeft})
		if res.Action != nil {
			t.Fatalf("width %d: separator unexpectedly activates %+v", width, res.Action)
		}
	}
}

func TestNewSessionButtonIdleLeavesBaselineUntouched(t *testing.T) {
	for width := 1; width <= 80; width++ {
		s := New(nil)
		s.SetNewButton(true)
		s.SetProjection([]TabProjection{{ID: "1", Label: "Editor", Closable: true}}, "1", -1, false)
		h := ui.NewHost(theme.DefaultDark(), s)
		h.Resize(width, ui.StripHeight)
		h.Layout()
		r := s.NewButtonBounds()
		buf := surface.NewCellBuffer(width, 2)
		h.Paint(buf)
		tokens := ui.NewTokenSet(theme.DefaultDark())
		for x := r.X; x < r.X+r.W; x++ {
			if c := buf.Get(x, 1); c.BG != tokens.Color(ui.ColorBg) || c.Char != '─' {
				t.Fatalf("width %d: idle action repainted the separator at %d: %+v", width, x, c)
			}
		}
	}
}

func TestNewSessionButtonFitsFocusThemeAndKeyboard(t *testing.T) {
	s := New(nil)
	s.SetProjection([]TabProjection{{ID: "1", Label: "Editor", Closable: true}}, "1", -1, false)
	h := ui.NewHost(theme.DefaultDark(), s)
	h.Resize(80, 2)
	h.Layout()
	if !s.NewButtonBounds().Empty() || len(s.Children()) != 1 {
		t.Fatal("new button must default off")
	}
	s.SetNewButton(true)
	h.MarkLayout()
	h.Layout()
	r := s.NewButtonBounds()
	if r.X >= 20 {
		t.Fatal("fitting tabs must place plus after content, not far right")
	}
	h.Focus(TabsID)
	if res := h.Handle(tea.KeyPressMsg{Code: 'n', Text: "n"}); res.Action != nil {
		t.Fatal("passive tab focus must not invoke new session")
	}
	s.SetProjection([]TabProjection{{ID: "1", Label: "Editor", Closable: true}}, "1", -1, true)
	for _, key := range []string{"n", "+"} {
		res := h.Handle(tea.KeyPressMsg{Code: rune(key[0]), Text: key})
		if res.Action == nil || res.Action.ID != "tabs.new" {
			t.Fatalf("deliberate keyboard shortcut %s: %+v", key, res)
		}
	}
	h.Focus(s.newButton.ID())
	buf := surface.NewCellBuffer(80, 2)
	h.Paint(buf)
	if !buf.Get(r.X, 0).BG.Equal(ui.NewTokenSet(theme.DefaultDark()).Color(ui.ColorAccent)) {
		t.Fatal("native plus focus not painted")
	}
	s.SetTheme(theme.DefaultLight())
	h.MarkLayout()
	h.Layout()
	buf = surface.NewCellBuffer(80, 2)
	h.Paint(buf)
	if !buf.Get(r.X, 0).BG.Equal(ui.NewTokenSet(theme.DefaultLight()).Color(ui.ColorAccent)) {
		t.Fatal("theme did not reach new button")
	}
	h.Focus(TabsID)
	s.SetNewButton(false)
	h.MarkLayout()
	h.Layout()
	if res := h.Handle(tea.KeyPressMsg{Code: 'n', Text: "n"}); res.Action != nil {
		t.Fatal("disabled new button shortcut must stay absent")
	}
}

func TestShellTabsNativeHostDragAndManualOverflow(t *testing.T) {
	s := NewWithID(nil, "group/main")
	s.SetNewButton(true)
	s.SetDraggable(true)
	tabs := make([]window.Tab, 30)
	for i := range tabs {
		tabs[i] = window.Tab{ID: window.TabID(i + 1), Title: fmt.Sprintf("Document %d", i), Closable: true}
	}
	s.SetProjectionFromTabs(tabs, 30, false)
	h := ui.NewHost(theme.DefaultDark(), s)
	h.Resize(40, 2)
	h.Layout()
	h.Handle(tea.MouseWheelMsg{X: 1, Y: 0, Button: tea.MouseWheelLeft})
	offset := s.tabs.ScrollOffset()
	s.SetProjectionFromTabs(tabs, 30, false)
	h.MarkLayout()
	h.Layout()
	if s.tabs.ScrollOffset() != offset {
		t.Fatal("new button layout must preserve manual overflow browsing")
	}
	r := s.NewButtonBounds()
	if r.X+r.W != 40 {
		t.Fatal("overflow button lost trailing reservation")
	}
	res := h.Handle(tea.MouseClickMsg{X: 1, Y: 0, Button: tea.MouseLeft})
	if res.Action == nil || res.Action.ID != "tabs.activate" {
		t.Fatalf("body click: %+v", res)
	}
	id := res.Action.Payload.(string)
	res = h.Handle(tea.MouseMotionMsg{X: 60, Y: 10, Button: tea.MouseLeft})
	if res.Action == nil || res.Action.ID != "tabs.drag" || res.Action.Payload != (ui.TabDrag{ID: id, X: 60, Y: 10}) {
		t.Fatalf("shell did not forward drag: %+v", res)
	}
	h.CancelPointerCapture()
	if s.tabs.Dragging() {
		t.Fatal("shell capture cancellation did not clear gesture")
	}
	h.Handle(tea.MouseClickMsg{X: 1, Y: 0, Button: tea.MouseLeft})
	h.Handle(tea.MouseMotionMsg{X: 3, Y: 0, Button: tea.MouseLeft})
	res = h.Handle(tea.MouseReleaseMsg{X: 60, Y: 10, Button: tea.MouseLeft})
	if res.Action == nil || res.Action.ID != "tabs.drop" || h.HasPointerCapture() {
		t.Fatalf("shell did not forward outside drop: %+v", res)
	}
}
