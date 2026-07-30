package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/theme"
)

func TestTabsShellChromeOmitsIconAndPaintsActiveMarker(t *testing.T) {
	th := theme.DefaultDark()
	tokens := NewTokenSet(th)
	tabs := NewTabs("test/tabs", tokens)
	tabs.SetChrome(TabsShell)
	tabs.SetItems([]TabItem{
		{ID: "a", Label: "Alpha", Icon: "●", Closable: true},
		{ID: "b", Label: "Beta", Closable: true},
	}, "a")
	tabs.Layout(LayoutContext{}, Rect{X: 0, Y: 0, W: 40, H: 1})

	buf := surface.NewCellBuffer(40, 1)
	tabs.Paint(buf, 0, 0, PaintContext{})

	line := readTabRow(buf, 40)
	if strings.Contains(line, "●") {
		t.Fatalf("shell tabs must omit icons (tofu risk), got %q", line)
	}
	if !strings.Contains(line, "❯") {
		t.Fatalf("expected active marker in shell tab, got %q", line)
	}
	if !strings.Contains(line, "Alpha") {
		t.Fatalf("expected active label, got %q", line)
	}
}

func TestTabsShellTruncatesWhenNarrow(t *testing.T) {
	th := theme.DefaultDark()
	tokens := NewTokenSet(th)
	tabs := NewTabs("test/tabs", tokens)
	tabs.SetChrome(TabsShell)
	tabs.SetItems([]TabItem{
		{ID: "a", Label: "AlphaLongName", Icon: "●", Closable: true},
		{ID: "b", Label: "BetaLongName", Closable: true},
	}, "a")
	tabs.Layout(LayoutContext{}, Rect{X: 0, Y: 0, W: 14, H: 1})
	buf := surface.NewCellBuffer(14, 1)
	tabs.Paint(buf, 0, 0, PaintContext{})
	line := readTabRow(buf, 14)
	if strings.Contains(line, "BetaLongName") {
		t.Fatalf("narrow row should truncate second tab, got %q", line)
	}
}

func TestTabsAttentionAndCloseActions(t *testing.T) {
	th := theme.DefaultDark()
	tokens := NewTokenSet(th)
	tabs := NewTabs("test/tabs", tokens)
	tabs.SetItems([]TabItem{
		{ID: "one", Label: "One", Closable: true, Attention: true},
		{ID: "two", Label: "Two", Closable: false},
	}, "one")
	tabs.Layout(LayoutContext{}, Rect{X: 0, Y: 0, W: 30, H: 1})

	buf := surface.NewCellBuffer(30, 1)
	tabs.Paint(buf, 0, 0, PaintContext{})
	if line := readTabRow(buf, 30); !strings.Contains(line, "•") {
		t.Fatalf("expected attention marker, got %q", line)
	}

	ranges := tabs.ranges
	if len(ranges) == 0 {
		t.Fatal("expected tab ranges")
	}
	closeX := ranges[0].closeStart
	idx, closeHit := tabs.HitTest(closeX)
	if idx != 0 || !closeHit {
		t.Fatalf("expected close hit at x=%d, got index=%d close=%v ranges=%+v", closeX, idx, closeHit, ranges)
	}

	act := tabs.Handle(EventContext{}, tea.KeyPressMsg{Code: tea.KeyRight})
	if act.Action == nil || act.Action.ID != "tabs.activate" || act.Action.Payload != "two" {
		t.Fatalf("expected tabs.activate two, got %+v", act.Action)
	}
}

func TestTabsPopupChromeUsesPopupBg(t *testing.T) {
	th := theme.DefaultDark()
	tokens := NewTokenSet(th)
	tabs := NewTabs("test/tabs", tokens)
	tabs.SetItems([]TabItem{{ID: "a", Label: "Tab"}}, "a")
	tabs.Layout(LayoutContext{}, Rect{X: 0, Y: 0, W: 12, H: 1})

	buf := surface.NewCellBuffer(12, 1)
	tabs.Paint(buf, 0, 0, PaintContext{})

	subtle := tokens.Color(ColorPopupSubtleBg)
	popup := tokens.Color(ColorPopupBg)
	got := buf.Get(0, 0).BG
	if !got.Equal(subtle) && !got.Equal(popup) {
		t.Fatalf("popup tabs should use popup palette bg, got %v want subtle=%v or popup=%v", got, subtle, popup)
	}

	tabs.SetChrome(TabsShell)
	buf = surface.NewCellBuffer(12, 1)
	tabs.Paint(buf, 0, 0, PaintContext{})
	shell := tokens.Color(ColorBg)
	// Trailing blank after tab content should be shell bg from FillRect.
	if got := buf.Get(11, 0).BG; !got.Equal(shell) {
		t.Fatalf("shell tabs fill should use ColorBg, got %v want %v", got, shell)
	}
}

func readTabRow(buf *surface.CellBuffer, w int) string {
	var b strings.Builder
	for x := 0; x < w; x++ {
		c := buf.Get(x, 0)
		if c.Char != 0 && c.Width != 0 {
			b.WriteRune(c.Char)
		}
	}
	return b.String()
}
