package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/theme"
)

func newStrip(t *testing.T, width int) (*Tabs, *surface.CellBuffer) {
	t.Helper()
	tokens := NewTokenSet(theme.DefaultDark())
	tabs := NewTabs("test/tabs", tokens)
	tabs.SetItems([]TabItem{
		{ID: "one", Label: "One"},
		{ID: "two", Label: "Two"},
		{ID: "three", Label: "Three"},
	}, "one")
	tabs.Layout(LayoutContext{}, Rect{X: 0, Y: 0, W: width, H: StripHeight})
	buf := surface.NewCellBuffer(width, StripHeight)
	tabs.Paint(buf, 0, 0, PaintContext{})
	return tabs, buf
}

func TestTabsStripOwnsLabelsAndBaselineRow(t *testing.T) {
	tabs, buf := newStrip(t, 40)
	if got := tabs.Measure(LayoutContext{}, Loose(40, 10)).H; got != StripHeight {
		t.Fatalf("strip height = %d want %d", got, StripHeight)
	}
	labels := stripRow(buf, 0, 40)
	if !strings.Contains(labels, "One") || !strings.Contains(labels, "Two") || !strings.Contains(labels, "Three") {
		t.Fatalf("labels row missing modes: %q", labels)
	}
	baseline := stripRow(buf, 1, 40)
	if strings.Trim(baseline, "─") != "" {
		t.Fatalf("baseline row must be a plain rule, got %q", baseline)
	}
}

func TestTabsActiveTabIsAccentUnderlined(t *testing.T) {
	tokens := NewTokenSet(theme.DefaultDark())
	tabs, buf := newStrip(t, 40)
	accent := tokens.Color(ColorAccent)
	active := tabs.ranges[0]

	// Underline sits on the baseline row, exactly under the active label, in
	// accent ink.
	for x := active.start; x < active.end; x++ {
		cell := buf.Get(x, 1)
		if !cell.FG.Equal(accent) {
			t.Fatalf("baseline x=%d under active tab is not accent: %+v", x, cell)
		}
	}
	if after := buf.Get(active.end, 1); after.FG.Equal(accent) {
		t.Fatalf("underline bleeds past the active tab end at x=%d", active.end)
	}
	// Ink on the label row is accent for the active tab and muted elsewhere.
	if fg := buf.Get(active.start, 0).FG; !fg.Equal(accent) {
		t.Fatalf("active label ink = %v want accent %v", fg, accent)
	}
	muted := tokens.Color(ColorMuted)
	if fg := buf.Get(tabs.ranges[1].start, 0).FG; !fg.Equal(muted) {
		t.Fatalf("inactive label ink = %v want muted %v", fg, muted)
	}
}

func TestTabsFocusIsDistinctFromHoverAndActive(t *testing.T) {
	tokens := NewTokenSet(theme.DefaultDark())
	accent := tokens.Color(ColorAccent)
	hover := tokens.Color(ColorHover)

	tabs := NewTabs("test/tabs", tokens)
	tabs.SetItems([]TabItem{{ID: "one", Label: "One"}, {ID: "two", Label: "Two"}}, "one")
	tabs.Layout(LayoutContext{}, Rect{W: 24, H: StripHeight})

	// Hover: background only, on the labels row; baseline stays border ink.
	tabs.SyncHover(1)
	buf := surface.NewCellBuffer(24, StripHeight)
	tabs.Paint(buf, 0, 0, PaintContext{})
	if bg := buf.Get(tabs.ranges[1].start, 0).BG; !bg.Equal(hover) {
		t.Fatalf("hover fill = %v want %v", bg, hover)
	}
	if fg := buf.Get(tabs.ranges[1].start, 1).FG; fg.Equal(accent) {
		t.Fatal("hover must not turn the baseline accent")
	}

	// Focus: whole baseline turns accent-heavy, hover fill stays put.
	tabs.SetBarFocused(true)
	buf = surface.NewCellBuffer(24, StripHeight)
	tabs.Paint(buf, 0, 0, PaintContext{})
	baseline := stripRow(buf, 1, 24)
	if strings.Trim(baseline, "━") != "" {
		t.Fatalf("focused baseline must be the heavy accent rule, got %q", baseline)
	}
	if fg := buf.Get(tabs.ranges[0].start, 0).FG; !fg.Equal(accent) {
		t.Fatal("focused strip lost the accent active label")
	}
}

func TestTabsHostFocusPaintsHeavyBaseline(t *testing.T) {
	tokens := NewTokenSet(theme.DefaultDark())
	tabs := NewTabs("test/tabs", tokens)
	tabs.SetItems([]TabItem{{ID: "one", Label: "One"}}, "one")
	tabs.Layout(LayoutContext{}, Rect{W: 10, H: StripHeight})
	buf := surface.NewCellBuffer(10, StripHeight)
	tabs.Paint(buf, 0, 0, PaintContext{Focused: true})
	if got := stripRow(buf, 1, 10); strings.Trim(got, "━") != "" {
		t.Fatalf("host-focused strip baseline = %q want heavy rule", got)
	}
}

func TestTabsContentSizedAndCloseAdjacentToLabel(t *testing.T) {
	th := theme.DefaultDark()
	tokens := NewTokenSet(th)
	tabs := NewTabs("test/tabs", tokens)
	tabs.SetChrome(TabsShell)
	tabs.SetItems([]TabItem{
		{ID: "1", Label: "Main Agent", Closable: true},
		{ID: "2", Label: "main.go", Closable: true},
	}, "1")
	tabs.Layout(LayoutContext{}, Rect{W: 80, H: StripHeight})
	buf := surface.NewCellBuffer(80, StripHeight)
	tabs.Paint(buf, 0, 0, PaintContext{})

	// Content-sized: an ordinary strip never pads short labels into fixed
	// slots, so the tab width tracks the label + close affordance.
	if got, want := tabs.ranges[1].end-tabs.ranges[1].start, len("main.go")+2; got != want {
		t.Fatalf("tab width = %d want %d", got, want)
	}
	for tabIdx := 0; tabIdx < 2; tabIdx++ {
		closeX := tabs.CloseLocalX(tabIdx)
		if closeX < 0 {
			t.Fatalf("tab %d missing close geometry", tabIdx)
		}
		found := false
		for x := closeX; x < closeX+3 && x < 80; x++ {
			if buf.Get(x, 0).Char == '×' {
				found = true
				idx, hit := tabs.HitTest(x)
				if idx != tabIdx || !hit {
					t.Fatalf("painted × at %d: HitTest=(%d,%v) want (%d,true)", x, idx, hit, tabIdx)
				}
				break
			}
		}
		if !found {
			t.Fatalf("tab %d: no × near closeStart=%d; row=%q", tabIdx, closeX, stripRow(buf, 0, 80))
		}
	}
	if strings.Contains(stripRow(buf, 0, 80), "Main Agent     ") {
		t.Fatalf("active tab still padded like fixed-width slots: %q", stripRow(buf, 0, 80))
	}
}

func TestTabsNarrowShrinksThenScrollsToActive(t *testing.T) {
	tokens := NewTokenSet(theme.DefaultDark())
	tabs := NewTabs("test/tabs", tokens)
	tabs.SetItems([]TabItem{
		{ID: "a", Label: "AlphaLongName", Closable: true},
		{ID: "b", Label: "BetaLongName", Closable: true},
		{ID: "c", Label: "GammaLongName", Closable: true},
	}, "a")
	tabs.Layout(LayoutContext{}, Rect{W: 24, H: StripHeight})
	buf := surface.NewCellBuffer(24, StripHeight)
	tabs.Paint(buf, 0, 0, PaintContext{})
	if strings.Contains(stripRow(buf, 0, 24), "GammaLongName") {
		t.Fatalf("narrow strip must shrink labels, got %q", stripRow(buf, 0, 24))
	}

	// Activate the last tab: the strip must scroll it into view at min widths.
	tabs.SetItems([]TabItem{
		{ID: "a", Label: "AlphaLongName", Closable: true},
		{ID: "b", Label: "BetaLongName", Closable: true},
		{ID: "c", Label: "GammaLongName", Closable: true},
	}, "c")
	tabs.Layout(LayoutContext{}, Rect{W: 24, H: StripHeight})
	if tabs.ScrollOffset() == 0 {
		t.Fatal("strip did not scroll to keep the active tab visible")
	}
	active := tabs.ranges[2]
	if active.start < 0 || active.end > 24 {
		t.Fatalf("active tab range %+v outside the 24-column strip", active)
	}
}

func TestTabsSingleRowBoundsAreTolerated(t *testing.T) {
	tokens := NewTokenSet(theme.DefaultDark())
	tabs := NewTabs("test/tabs", tokens)
	tabs.SetItems([]TabItem{{ID: "one", Label: "One"}}, "one")
	tabs.Layout(LayoutContext{}, Rect{W: 12, H: 1})
	buf := surface.NewCellBuffer(12, 1)
	tabs.Paint(buf, 0, 0, PaintContext{})
	if !strings.Contains(stripRow(buf, 0, 12), "One") {
		t.Fatalf("one-row strip must still paint labels: %q", stripRow(buf, 0, 12))
	}
}

func TestTabsRuleBleedSpansParentRow(t *testing.T) {
	tokens := NewTokenSet(theme.DefaultDark())
	tabs := NewTabs("test/tabs", tokens)
	tabs.SetRuleFullBleed(true)
	tabs.SetItems([]TabItem{{ID: "one", Label: "One"}}, "one")
	// The strip's own bounds are inset by 4 columns; the parent row spans the
	// whole 30 columns.
	tabs.SetRuleBleed(4, 30)
	tabs.Layout(LayoutContext{}, Rect{X: 4, Y: 0, W: 22, H: StripHeight})
	buf := surface.NewCellBuffer(30, StripHeight)
	tabs.Paint(buf, 0, 0, PaintContext{})
	baseline := stripRow(buf, 1, 30)
	if strings.Trim(baseline, "─") != "" {
		t.Fatalf("bled baseline must span the parent row: %q", baseline)
	}
	if got := stripRow(buf, 0, 4); strings.TrimSpace(got) != "" {
		t.Fatalf("labels must stay on their own rail, bleed leaked text: %q", got)
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
	tabs.Layout(LayoutContext{}, Rect{X: 0, Y: 0, W: 30, H: StripHeight})

	buf := surface.NewCellBuffer(30, StripHeight)
	tabs.Paint(buf, 0, 0, PaintContext{})
	// The attention marker is a prefix so it can never collide with the label
	// (and so a dirty filename can never grow a second dot).
	if line := stripRow(buf, 0, 30); !strings.Contains(line, "● One") {
		t.Fatalf("expected the attention marker in front of the label, got %q", line)
	}
	if line := stripRow(buf, 0, 30); strings.Contains(strings.ReplaceAll(line, "● One", ""), "●") {
		t.Fatalf("attention marker repeated in the strip: %q", line)
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

	act := tabs.Handle(EventContext{}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyRight}))
	if act.Action == nil || act.Action.ID != "tabs.activate" || act.Action.Payload != "two" {
		t.Fatalf("expected tabs.activate two, got %+v", act.Action)
	}
}

// A dirty tab's marker must not push its own content: the cell stays
// content-sized, so the close glyph follows the label with its single
// separating space and no stray padding columns. The marker used to be counted
// twice (once in the composed label, once in the width), which left the
// filename looking detached from its close glyph.
func TestTabsAttentionMarkerDoesNotPadTheLabel(t *testing.T) {
	tokens := NewTokenSet(theme.DefaultDark())
	tabs := NewTabs("test/tabs", tokens)
	tabs.SetItems([]TabItem{
		{ID: "dirty", Label: "README.md", Closable: true, Attention: true},
		{ID: "clean", Label: "README.md", Closable: true},
	}, "dirty")
	tabs.Layout(LayoutContext{}, Rect{X: 0, Y: 0, W: 40, H: StripHeight})

	buf := surface.NewCellBuffer(40, StripHeight)
	tabs.Paint(buf, 0, 0, PaintContext{})

	ranges := tabs.ranges
	dirtyW := ranges[0].end - ranges[0].start
	cleanW := ranges[1].end - ranges[1].start
	if dirtyW != cleanW+2 {
		t.Fatalf("dirty cell=%d clean cell=%d, want exactly the 2-column marker", dirtyW, cleanW)
	}
	row := stripRow(buf, 0, 40)
	if !strings.Contains(row, "● README.md ×") {
		t.Fatalf("close glyph is not adjacent to the label: %q", row)
	}
	if strings.Contains(row, "README.md  ×") {
		t.Fatalf("attention marker padded the label: %q", row)
	}
}

func TestTabsChromeSelectsBackgroundOnly(t *testing.T) {
	th := theme.DefaultDark()
	tokens := NewTokenSet(th)
	tabs := NewTabs("test/tabs", tokens)
	tabs.SetItems([]TabItem{{ID: "a", Label: "Tab"}}, "a")
	tabs.Layout(LayoutContext{}, Rect{X: 0, Y: 0, W: 12, H: StripHeight})

	buf := surface.NewCellBuffer(12, StripHeight)
	tabs.Paint(buf, 0, 0, PaintContext{})
	popup := tokens.Color(ColorPopupBg)
	if got := buf.Get(11, 0).BG; !got.Equal(popup) {
		t.Fatalf("popup strip fill = %v want %v", got, popup)
	}

	tabs.SetChrome(TabsShell)
	buf = surface.NewCellBuffer(12, StripHeight)
	tabs.Paint(buf, 0, 0, PaintContext{})
	shell := tokens.Color(ColorBg)
	if got := buf.Get(11, 0).BG; !got.Equal(shell) {
		t.Fatalf("shell strip fill = %v want %v", got, shell)
	}
}

func stripRow(buf *surface.CellBuffer, y, w int) string {
	var b strings.Builder
	for x := 0; x < w; x++ {
		c := buf.Get(x, y)
		if c.Char != 0 && c.Width != 0 {
			b.WriteRune(c.Char)
		} else {
			b.WriteByte(' ')
		}
	}
	return b.String()
}

func TestTabsLabelInsetKeepsUnderlineOnTheRowEdge(t *testing.T) {
	tokens := NewTokenSet(theme.DefaultDark())
	accent := tokens.Color(ColorAccent)
	hover := tokens.Color(ColorHover)

	build := func(active string) (*Tabs, *surface.CellBuffer) {
		t.Helper()
		tabs := NewTabs("test/tabs", tokens)
		tabs.SetLabelInset(1)
		tabs.SetItems([]TabItem{
			{ID: "one", Label: "One"},
			{ID: "two", Label: "Two"},
		}, active)
		tabs.Layout(LayoutContext{}, Rect{W: 24, H: StripHeight})
		buf := surface.NewCellBuffer(24, StripHeight)
		tabs.Paint(buf, 0, 0, PaintContext{})
		return tabs, buf
	}

	// First tab active: the label is inset, the underline is not — it starts
	// on the bar's own left edge and runs through the tab's cell.
	_, buf := build("one")
	if c := buf.Get(0, 0); c.Char != 0 && c.Char != ' ' {
		t.Fatalf("label row must keep the inset column empty, got %q at x=0", c.Char)
	}
	if got := buf.Get(1, 0).Char; got != 'O' {
		t.Fatalf("first label char=%q want 'O' at x=1", got)
	}
	if got := buf.Get(0, 1); !got.FG.Equal(accent) || (got.Char != '─' && got.Char != '━') {
		t.Fatalf("underline must start on the bar's left edge, got %+v at x=0", got)
	}

	// The first tab's cell owns that edge column: hovering the inactive first
	// tab fills it and a click there selects that tab.
	tabs, _ := build("two")
	tabs.SyncHover(0)
	hoverBuf := surface.NewCellBuffer(24, StripHeight)
	tabs.Paint(hoverBuf, 0, 0, PaintContext{})
	if bg := hoverBuf.Get(0, 0).BG; !bg.Equal(hover) {
		t.Fatalf("hover fill = %v want %v on the bar's edge column", bg, hover)
	}
	if idx, closeHover := tabs.HitTest(0); idx != 0 || closeHover {
		t.Fatalf("hit test at the edge column = (%d,%v) want (0,false)", idx, closeHover)
	}

	// Later tabs are unaffected: with the second tab active the edge column
	// carries the plain rule, not the indicator.
	tabs, buf = build("two")
	if got := buf.Get(0, 1).FG; got.Equal(accent) {
		t.Fatal("a non-first active tab must not tint the bar's edge column")
	}
	if got := buf.Get(tabs.ranges[1].start, 1).FG; !got.Equal(accent) {
		t.Fatal("the active tab's own cell must carry the underline")
	}
}
