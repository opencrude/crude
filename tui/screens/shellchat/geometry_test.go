package shellchat

import (
	"strings"
	"testing"

	"crude/tui/chatprojection"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

func TestContentSurfaceWidthMatchesProduction(t *testing.T) {
	th := theme.DefaultDark()
	padLR := th.Spacing.PadLR
	innerW := 48
	if got := ContentSurfaceWidth(innerW, padLR, false); got != innerW+padLR*2 {
		t.Fatalf("no gutter width=%d want %d", got, innerW+padLR*2)
	}
	if got := ContentSurfaceWidth(innerW, padLR, true); got != innerW+padLR*2+1 {
		t.Fatalf("with gutter width=%d want %d", got, innerW+padLR*2+1)
	}
}

func TestMainChatLayoutBoundsNoOverlap(t *testing.T) {
	th := theme.DefaultDark()
	padLR := th.Spacing.PadLR
	innerW := 30
	w := ContentSurfaceWidth(innerW, padLR, true)
	h := 8

	s := New(th)
	s.SetMessages([]chatprojection.MessageRow{{ID: "m1", Lines: make([]string, 20)}}, PaintParams{
		Compose: ComposeParams{
			InnerW:        innerW,
			PadLR:         padLR,
			Theme:         th,
			MainSBVisible: true,
		},
	})
	s.SyncScrollMetrics(h, 20)
	s.Layout(ui.LayoutContext{}, ui.Rect{W: w, H: h})

	list := s.list.Bounds()
	sb := s.scrollbar.Bounds()
	if list.W+list.X > sb.X {
		t.Fatalf("list %v overlaps scrollbar %v", list, sb)
	}
	if sb.W != ui.ScrollbarWidth {
		t.Fatalf("scrollbar width=%d want 1", sb.W)
	}
	if sb.X != w-padLR-ui.ScrollbarWidth {
		t.Fatalf("scrollbar X=%d want %d", sb.X, w-padLR-1)
	}
}

func TestMainChatLayoutTinyWidthNoOverlap(t *testing.T) {
	th := theme.DefaultDark()
	s := New(th)
	s.layout.padLR = 1
	s.layout.mainSBVisible = true
	for _, w := range []int{0, 1, 2, 3} {
		s.Layout(ui.LayoutContext{}, ui.Rect{W: w, H: 3})
		list := s.list.Bounds()
		if !s.mainScrollbarVisible() {
			want := w
			if want < 0 {
				want = 0
			}
			if list.W != want {
				t.Fatalf("w=%d hidden sb list.W=%d want %d", w, list.W, want)
			}
			continue
		}
		sb := s.scrollbar.Bounds()
		if list.X+list.W > sb.X {
			t.Fatalf("w=%d list %v overlaps sb %v", w, list, sb)
		}
	}
}

func TestMainChatLayoutNoGutterWhenAbsent(t *testing.T) {
	th := theme.DefaultDark()
	padLR := th.Spacing.PadLR
	innerW := 24
	w := ContentSurfaceWidth(innerW, padLR, false)
	s := New(th)
	s.SetMessages([]chatprojection.MessageRow{{ID: "m1", Lines: []string{"fits"}}}, PaintParams{
		Compose: ComposeParams{InnerW: innerW, PadLR: padLR, Theme: th, MainSBVisible: false},
	})
	s.SyncScrollMetrics(3, 1)
	s.Layout(ui.LayoutContext{}, ui.Rect{W: w, H: 3})

	if s.mainScrollbarVisible() {
		t.Fatal("scrollbar should be absent when MainSBVisible false")
	}
	if got := s.list.Bounds().W; got != w {
		t.Fatalf("list width=%d want full surface %d", got, w)
	}
}

func TestComposeRowStopsBeforeMainGutter(t *testing.T) {
	th := theme.DefaultDark()
	padLR := th.Spacing.PadLR
	innerW := 20
	line := ComposeRow(ComposeParams{
		RawLines:      []string{"hello"},
		InnerW:        innerW,
		PadLR:         padLR,
		Theme:         th,
		MainSBVisible: true,
	}, 0, 0, "hello", chatprojection.PaneLineInfo{})
	plain := stripANSI(line)
	wantLen := innerW + padLR*2 - 1 // rowInnerW + both pads
	if len(plain) != wantLen {
		t.Fatalf("composed len=%d want %d (stops before external gutter)", len(plain), wantLen)
	}
	if strings.Contains(plain, "█") || strings.Contains(plain, "░") {
		t.Fatalf("composed row must not embed main scrollbar glyphs: %q", plain)
	}
}

func TestComposeRowNoANSIPaneGutter(t *testing.T) {
	th := theme.DefaultDark()
	padLR := th.Spacing.PadLR
	innerW := 40
	paneMap := []chatprojection.PaneLineInfo{
		{},
		{PaneID: "pane-a", LineIdx: -1},
		{PaneID: "pane-a", LineIdx: 0},
	}
	scroll := ui.NewScrollModel()
	scroll.SetContent(10)
	scroll.SetViewport(2)
	scroll.ScrollTo(1)
	line := ComposeRow(ComposeParams{
		InnerW:       innerW,
		PadLR:        padLR,
		Theme:        th,
		NestingLevel: 1,
		PaneLineMap:  paneMap,
		PaneGutters: map[string]PaneGutterState{
			"pane-a": {Scroll: scroll, TrackH: 2, Visible: true},
		},
		HasPaneBg: func(string) bool { return true },
	}, 1, 2, "pane content", paneMap[2])
	plain := stripANSI(line)
	if strings.Contains(plain, "█") || strings.Contains(plain, "░") {
		t.Fatalf("ComposeRow must not embed pane gutter glyphs: %q", plain)
	}
}

func TestNestedPaneComposeAlignment(t *testing.T) {
	th := theme.DefaultDark()
	padLR := th.Spacing.PadLR
	innerW := 40
	nesting := 1
	paneMap := []chatprojection.PaneLineInfo{
		{},
		{PaneID: "pane-a", LineIdx: 0},
		{PaneID: "pane-a", LineIdx: 1},
	}
	raw := []string{"header", "pane line one", "pane line two"}
	scroll := ui.NewScrollModel()
	scroll.SetContent(5)
	scroll.SetViewport(2)
	gutters := map[string]PaneGutterState{
		"pane-a": {Scroll: scroll, TrackH: 2, Visible: true},
	}
	lines := Compose(ComposeParams{
		RawLines:      raw,
		InnerW:        innerW,
		PadLR:         padLR,
		Theme:         th,
		NestingLevel:  nesting,
		MainSBVisible: true,
		PaneLineMap:   paneMap,
		PaneGutters:   gutters,
	})
	for i, line := range lines {
		plain := stripANSI(line)
		if strings.Contains(plain, "█") || strings.Contains(plain, "░") {
			t.Fatalf("composed row %d must not embed scrollbar glyphs: %q", i, plain)
		}
	}

	w := ContentSurfaceWidth(innerW, padLR, true)
	viewportH := 2
	s := New(th)
	s.SetMessages([]chatprojection.MessageRow{{ID: "m1", Lines: lines}}, PaintParams{
		Compose: ComposeParams{
			InnerW:        innerW,
			PadLR:         padLR,
			Theme:         th,
			NestingLevel:  nesting,
			MainSBVisible: true,
			PaneLineMap:   paneMap,
			PaneGutters:   gutters,
		},
		PaneLineMap: paneMap,
	})
	s.SyncScrollMetrics(viewportH, len(raw))
	host := ui.NewHost(th, s)
	host.Resize(w, viewportH)
	host.Layout()

	list := s.list.Bounds()
	sb := s.scrollbar.Bounds()
	if list.W+list.X > sb.X {
		t.Fatalf("nested layout list %v overlaps main sb %v", list, sb)
	}
	if sb.W != 1 {
		t.Fatalf("main gutter width=%d want 1", sb.W)
	}

	buf := surfaceFromHost(t, host)
	gx := NestedGutterX(s.paintState.compose, list.W)
	c := buf.Get(gx, 1)
	if c.Char != '█' && c.Char != '░' {
		t.Fatalf("nested pane row should have native gutter at x=%d: char=%q", gx, string(c.Char))
	}
	mainCol := PlainRow(buf, sb.X, sb.X+sb.W)
	if strings.Contains(mainCol, "pane") {
		t.Fatalf("main gutter column should not contain row text: %q", mainCol)
	}
}

func surfaceFromHost(t *testing.T, host *ui.Host) *surface.CellBuffer {
	t.Helper()
	host.Layout()
	root := host.Root()
	w, h := 1, 1
	if root != nil {
		b := root.Bounds()
		if b.W > 0 {
			w = b.W
		}
		if b.H > 0 {
			h = b.H
		}
	}
	buf := surface.NewCellBuffer(w, h)
	host.Paint(buf)
	return buf
}
