package shellheader

import (
	"strings"
	"testing"

	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

func TestWordmarkTideChangesOnlyConnectingInk(t *testing.T) {
	for _, th := range []*theme.Theme{theme.DefaultDark(), theme.DefaultLight()} {
		s := New(th)
		s.SetProjection(Projection{LeftSegments: []Segment{Wordmark(ConnectionConnecting)}, Connection: ConnectionConnecting})
		host := ui.NewHost(th, s)
		host.Resize(60, 2)
		host.Layout()
		a, b := surface.NewCellBuffer(60, 2), surface.NewCellBuffer(60, 2)
		host.SetFrame(1000)
		host.Paint(a)
		bounds := s.ActionBounds(ConnectionAction)
		host.SetFrame(2200)
		host.Paint(b)
		if bounds != s.ActionBounds(ConnectionAction) || readRow(a, 0, 60) != readRow(b, 0, 60) {
			t.Fatal("connecting motion moved text or hit geometry")
		}
		changed := 0
		for y := range 2 {
			for x := range 60 {
				first, next := a.Get(x, y), b.Get(x, y)
				if first != next {
					if !bounds.Contains(x, y) || first.Char != next.Char || first.Width != next.Width || first.BG != next.BG {
						t.Fatal("tide changed geometry, background or other header content")
					}
					changed++
				}
			}
		}
		if changed == 0 {
			t.Fatal("connecting wordmark did not animate")
		}
		s.SetProjection(Projection{LeftSegments: []Segment{Wordmark(ConnectionReady)}})
		host.Paint(a)
		host.SetFrame(3400)
		host.Paint(b)
		for x := range 60 {
			if a.Get(x, 0) != b.Get(x, 0) {
				t.Fatal("healthy brand kept animating")
			}
		}
	}
}

func TestWordmarkAttentionUsesSemanticInkAndFitsTinyHeaders(t *testing.T) {
	for _, state := range []ConnectionState{ConnectionReady, ConnectionConnecting, ConnectionReconnecting, ConnectionSlow, ConnectionOffline} {
		for width := 1; width <= 60; width++ {
			s := New(theme.DefaultDark())
			s.SetProjection(Projection{LeftSegments: []Segment{Wordmark(state)}, Connection: state, RightSegments: []Segment{{Text: "model"}}})
			s.Layout(ui.LayoutContext{}, ui.Rect{W: width, H: 2})
			buf := surface.NewCellBuffer(width+12, 3)
			s.Paint(buf, 0, 0, ui.PaintContext{})
			for x := width; x < width+12; x++ {
				if c := buf.Get(x, 0); c.Char != 0 && c.Char != ' ' {
					t.Fatalf("width %d leaked outside bounds", width)
				}
			}
			if width < 24 {
				continue
			}
			r := s.ActionBounds(ConnectionAction)
			line := readRow(buf, 0, width)
			role, mark := ui.ColorWarning, "⚠"
			if state == ConnectionOffline {
				role, mark = ui.ColorError, "✕"
			}
			if state == ConnectionReady || state == ConnectionConnecting {
				if strings.Contains(line, "⚠") || strings.Contains(line, "✕") {
					t.Fatal("healthy/connecting wordmark got an issue mark")
				}
				continue
			}
			if !strings.Contains(line, "Crude "+mark) || !buf.Get(r.X+6, 0).FG.Equal(s.tokens.Color(role)) || buf.Get(r.X+6, 0).Attr&cell.AttrBlink != 0 {
				t.Fatalf("attention is not static semantic ink: %q", line)
			}
		}
	}
}
