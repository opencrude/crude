package connectionpopover

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

func TestConnectionPopoverNativeLayoutThemeAndReading(t *testing.T) {
	for _, th := range []*theme.Theme{theme.DefaultDark(), theme.DefaultLight()} {
		for _, size := range []ui.Size{{W: 58, H: 18}, {W: 30, H: 14}, {W: 22, H: 8}} {
			p := Projection{Summary: "Core link is slow", Role: ui.ColorWarning, Detail: "Connection   Development\nTransport    QUIC\nRound trip   520 ms\n\n" + strings.Repeat("Readable complete evidence. ", 20) + "final observation"}
			s := New(th, p)
			widget := ui.NewWindowFrameWidget(th, "test/frame", ui.FramePopover, "Connection to core", s, nil, false, size.W, size.H)
			host := widget.Host()
			host.Resize(size.W, size.H)
			host.Layout()
			buf := surface.NewCellBuffer(size.W, size.H)
			host.Paint(buf)
			if !buf.Get(3, 1).FG.Equal(ui.NewTokenSet(th).Color(ui.ColorWarning)) {
				t.Fatal("summary omitted semantic theme ink")
			}
			for _, c := range s.Children() {
				b := c.(interface{ Bounds() ui.Rect }).Bounds()
				if b.X < 0 || b.Y < 0 || b.X+b.W > s.Bounds().W || b.Y+b.H > s.Bounds().H {
					t.Fatalf("child exceeds parent: %+v in %+v", b, s.Bounds())
				}
			}
			if b := s.detail.Bounds(); b.Y+b.H > s.actions.Bounds().Y {
				t.Fatal("reader overlaps pinned actions")
			}
			host.Focus(DetailID)
			host.Handle(tea.KeyPressMsg{Code: tea.KeyEnd, Mod: tea.ModCtrl})
			// Use ordinary page navigation to reach the complete evidence even in
			// the constrained frame; never replace the body with truncated text.
			for range 20 {
				host.Handle(tea.KeyPressMsg{Code: tea.KeyPgDown})
			}
			host.Paint(buf)
			if s.detail.ScrollOffset() == 0 || s.detail.Value() != p.Detail {
				t.Fatal("long detail is not scrollable/complete")
			}
			var text strings.Builder
			for y := range size.H {
				for x := range size.W {
					c := buf.Get(x, y)
					if c.Width > 0 && c.Char != 0 {
						text.WriteRune(c.Char)
					}
				}
			}
			if !strings.Contains(text.String(), "observation") {
				t.Fatal("last evidence is not reachable in rendered output")
			}
			host.Handle(tea.KeyPressMsg{Code: tea.KeyTab})
			if host.Focused() != ActionsID {
				t.Fatal("Tab did not reach actions")
			}
			host.Handle(tea.KeyPressMsg{Code: tea.KeyUp})
			if host.Focused() != DetailID {
				t.Fatal("actions cannot return to the reader")
			}
			host.Handle(tea.KeyPressMsg{Code: 'x', Text: "x"})
			if s.detail.Value() != p.Detail {
				t.Fatal("reading changed the evidence")
			}
		}
	}
}

func TestConnectionPopoverActionsAndLiveRefresh(t *testing.T) {
	s := New(theme.DefaultDark(), Projection{Summary: "Connected", Role: ui.ColorSuccess, Detail: "Ready"})
	host := ui.NewHost(theme.DefaultDark(), s)
	host.Resize(50, 12)
	host.Layout()
	host.Focus(DetailID)
	if s.SetProjection(s.projection) {
		t.Fatal("unchanged projection invalidates reading")
	}
	s.SetProjection(Projection{Summary: "Unavailable", Role: ui.ColorError, Detail: "The core link was interrupted."})
	if s.detail.CursorFlat() != 0 {
		t.Fatal("refresh moved the reading cursor")
	}
	for _, action := range []string{"close", "connections"} {
		res := s.HandleAction(ui.EventContext{}, ui.Action{ID: action})
		msg, ok := res.Cmd().(ActionMsg)
		if !ok || msg.Screen != s || msg.Action != action {
			t.Fatalf("wrong action: %+v", msg)
		}
	}
}
