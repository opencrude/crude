package shellchat

import (
	"strings"
	"testing"

	"crude/tui/cell"
	"crude/tui/chatprojection"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

func TestPaintGoldenSetMessagesVirtItems(t *testing.T) {
	th := theme.DefaultDark()
	s := New(th)
	padLR := th.Spacing.PadLR
	innerW := 48
	rows := []chatprojection.MessageRow{
		{
			ID:    "u1",
			Lines: []string{"user message body"},
			Native: []chatprojection.PaintLine{
				{Spans: []chatprojection.RowSpan{{Text: "user message body", Role: ui.ColorAccent}}},
			},
		},
		{
			ID:    "a1",
			Lines: []string{"agent reply line"},
			Native: []chatprojection.PaintLine{
				{Spans: []chatprojection.RowSpan{{Text: "agent reply line", Role: ui.ColorAccent2}}},
			},
		},
	}
	s.SetMessages(rows, PaintParams{
		Compose: ComposeParams{
			InnerW: innerW,
			PadLR:  padLR,
			Theme:  th,
		},
	})

	w := innerW + padLR*2
	h := len(rows[0].Lines) + len(rows[1].Lines)
	buf := s.PaintTestBuffer(w, h)
	if NonEmptyCellCount(buf) == 0 {
		t.Fatal("SetMessages/VirtItems paint produced no cells")
	}
	for _, want := range []string{"user message", "agent reply"} {
		found := false
		for y := 0; y < buf.Height(); y++ {
			if strings.Contains(PlainRow(buf, y, w), want) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("missing substring %q in painted buffer", want)
		}
	}
	if !bufferHasThemedFG(buf) {
		t.Fatal("message paint missing themed foreground colors")
	}
}

func TestShellChatPadColumnsMatchShellBackground(t *testing.T) {
	th := theme.DefaultDark()
	s := New(th)
	padLR := th.Spacing.PadLR
	if padLR < 1 {
		t.Fatal("theme PadLR must be at least 1 to observe side columns")
	}
	innerW := 24
	rows := []chatprojection.MessageRow{
		{
			ID:    "u1",
			Lines: []string{"hello"},
			Native: []chatprojection.PaintLine{
				{Spans: []chatprojection.RowSpan{{Text: "hello", Role: ui.ColorFg}}},
			},
		},
		{
			ID:    "a1",
			Lines: []string{"reply"},
			Native: []chatprojection.PaintLine{
				{Spans: []chatprojection.RowSpan{{Text: "reply", Role: ui.ColorFg}}},
			},
		},
	}
	s.SetMessages(rows, PaintParams{
		Compose: ComposeParams{InnerW: innerW, PadLR: padLR, Theme: th},
	})
	w := innerW + padLR*2
	buf := s.PaintTestBuffer(w, 2)
	tokens := ui.NewTokenSet(th)
	popup := tokens.Color(ui.ColorPopupBg)
	selection := tokens.Color(ui.ColorSelection)
	for y := 0; y < 2; y++ {
		left := buf.Get(0, y)
		right := buf.Get(w-1, y)
		if left.BG.Equal(popup) || left.BG.Equal(selection) {
			t.Fatalf("left pad y=%d uses list/popup fill %v", y, left.BG)
		}
		if right.BG.Equal(popup) || right.BG.Equal(selection) {
			t.Fatalf("right pad y=%d uses list/popup fill %v", y, right.BG)
		}
		if !left.BG.Equal(buf.Get(0, 0).BG) {
			t.Fatalf("left pad color changed across messages: y0=%v y%d=%v", buf.Get(0, 0).BG, y, left.BG)
		}
	}
}

func bufferHasThemedFG(buf *surface.CellBuffer) bool {
	for y := 0; y < buf.Height(); y++ {
		for x := 0; x < buf.Width(); x++ {
			fg := buf.Get(x, y).FG
			if !fg.IsDefault() && fg != cell.NoColor {
				return true
			}
		}
	}
	return false
}

func TestShellChatPaintsComposedMessageLines(t *testing.T) {
	th := theme.DefaultDark()
	s := New(th)

	raw := []string{"Hello from agent", "second line of reply"}
	composed := Compose(ComposeParams{
		RawLines: raw,
		InnerW:   40,
		PadLR:    th.Spacing.PadLR,
		Theme:    th,
	})
	if len(composed) != 2 {
		t.Fatalf("expected 2 composed lines, got %d", len(composed))
	}
	rows := []chatprojection.MessageRow{
		{
			ID:    "m1",
			Lines: raw,
			Native: []chatprojection.PaintLine{
				{Spans: []chatprojection.RowSpan{{Text: "Hello from agent", Role: ui.ColorFg}}},
				{Spans: []chatprojection.RowSpan{{Text: "second line of reply", Role: ui.ColorFg}}},
			},
		},
	}
	s.SetMessages(rows, PaintParams{
		Compose: ComposeParams{
			InnerW: 40,
			PadLR:  th.Spacing.PadLR,
			Theme:  th,
		},
	})

	w := 40 + th.Spacing.PadLR*2
	h := 2
	buf := s.PaintTestBuffer(w, h)
	if NonEmptyCellCount(buf) == 0 {
		t.Fatal("expected non-empty message content in painted buffer")
	}
	row0 := PlainRow(buf, 0, w)
	if !strings.Contains(row0, "Hello from agent") {
		t.Fatalf("expected message text in row 0, got %q", row0)
	}
	row1 := PlainRow(buf, 1, w)
	if !strings.Contains(row1, "second line") {
		t.Fatalf("expected second line in row 1, got %q", row1)
	}
}

func TestShellChatHostPaintSurface(t *testing.T) {
	th := theme.DefaultDark()
	s := New(th)
	rows := []chatprojection.MessageRow{
		{
			ID:    "m1",
			Lines: []string{"stream token chunk"},
			Native: []chatprojection.PaintLine{
				{Spans: []chatprojection.RowSpan{{Text: "stream token chunk", Role: ui.ColorFg}}},
			},
		},
	}
	s.SetMessages(rows, PaintParams{
		Compose: ComposeParams{
			InnerW: 48,
			PadLR:  1,
			Theme:  th,
		},
	})
	host := ui.NewHost(th, s)
	host.Resize(50, 1)
	host.Layout()
	buf := surface.NewCellBuffer(50, 1)
	host.Paint(buf)
	if NonEmptyCellCount(buf) == 0 {
		t.Fatal("expected painted cells for chat host surface")
	}
	plain := PlainRow(buf, 0, 50)
	if !strings.Contains(plain, "stream token") {
		t.Fatalf("expected chat content, got %q", plain)
	}
}

func TestShellChatVirtualListMessageItems(t *testing.T) {
	th := theme.DefaultDark()
	s := New(th)
	rows := []chatprojection.MessageRow{
		{ID: "m1", Lines: []string{"line A", "line B"}},
		{ID: "m2", Lines: []string{"line C"}},
	}
	s.SetMessages(rows, PaintParams{
		Compose: ComposeParams{
			InnerW: 30,
			PadLR:  th.Spacing.PadLR,
			Theme:  th,
		},
	})

	list := s.VirtualList()
	items := list.Items()
	if len(items) != 2 {
		t.Fatalf("virt items=%d want 2", len(items))
	}
	if items[0].Height != 2 || items[1].Height != 1 {
		t.Fatalf("heights=%d,%d want 2,1", items[0].Height, items[1].Height)
	}
	if list.Scroll().Content() != 3 {
		t.Fatalf("content rows=%d want 3", list.Scroll().Content())
	}

	w := 30 + th.Spacing.PadLR*2
	buf := s.PaintTestBuffer(w, 3)
	for y, want := range []string{"line A", "line B", "line C"} {
		row := PlainRow(buf, y, w)
		if !strings.Contains(row, want) {
			t.Fatalf("row %d=%q want substring %q", y, row, want)
		}
	}
}

func TestShellChatVirtualListRowPainter(t *testing.T) {
	th := theme.DefaultDark()
	s := New(th)
	raw := []string{"line A", "line B", "line C"}
	rows := []chatprojection.MessageRow{
		{
			ID:    "m1",
			Lines: []string{"line A"},
			Native: []chatprojection.PaintLine{
				{Spans: []chatprojection.RowSpan{{Text: "line A", Role: ui.ColorFg}}},
			},
		},
		{
			ID:    "m2",
			Lines: []string{"line B"},
			Native: []chatprojection.PaintLine{
				{Spans: []chatprojection.RowSpan{{Text: "line B", Role: ui.ColorFg}}},
			},
		},
		{
			ID:    "m3",
			Lines: []string{"line C"},
			Native: []chatprojection.PaintLine{
				{Spans: []chatprojection.RowSpan{{Text: "line C", Role: ui.ColorFg}}},
			},
		},
	}
	s.SetMessages(rows, PaintParams{
		Compose: ComposeParams{
			InnerW: 30,
			PadLR:  th.Spacing.PadLR,
			Theme:  th,
		},
	})

	list := s.VirtualList()
	if got := len(list.Items()); got != 3 {
		t.Fatalf("virt items=%d want 3", got)
	}
	for i, it := range list.Items() {
		if it.Height != 1 {
			t.Fatalf("item[%d] height=%d want 1", i, it.Height)
		}
		if _, ok := it.Payload.(chatprojection.MessageRow); !ok {
			t.Fatalf("item[%d] payload not MessageRow", i)
		}
	}
	if list.Scroll().Content() != len(raw) {
		t.Fatalf("content rows=%d want %d", list.Scroll().Content(), len(raw))
	}

	w := 30 + th.Spacing.PadLR*2
	buf := s.PaintTestBuffer(w, 3)
	for y, want := range []string{"line A", "line B", "line C"} {
		row := PlainRow(buf, y, w)
		if !strings.Contains(row, want) {
			t.Fatalf("row %d=%q want substring %q", y, row, want)
		}
	}
}

func TestComposePreservesLeftPadAndContent(t *testing.T) {
	th := theme.DefaultDark()
	padLR := 2
	innerW := 30
	lines := Compose(ComposeParams{
		RawLines: []string{"hi"},
		InnerW:   innerW,
		PadLR:    padLR,
		Theme:    th,
	})
	if len(lines) != 1 {
		t.Fatalf("want 1 line, got %d", len(lines))
	}
	plain := stripANSI(lines[0])
	if !strings.HasPrefix(plain, strings.Repeat(" ", padLR)) {
		t.Fatalf("expected left pad, got %q", plain)
	}
	if !strings.Contains(plain, "hi") {
		t.Fatalf("expected content, got %q", plain)
	}
}

func TestShellChatMainScrollbarSinglePaintPath(t *testing.T) {
	th := theme.DefaultDark()
	s := New(th)
	padLR := th.Spacing.PadLR
	innerW := 30
	lines := make([]string, 12)
	for i := range lines {
		lines[i] = "scrollable line"
	}
	rows := []chatprojection.MessageRow{{ID: "m1", Lines: lines}}
	s.SetMessages(rows, PaintParams{
		Compose: ComposeParams{
			InnerW:        innerW,
			PadLR:         padLR,
			Theme:         th,
			MainSBVisible: true,
		},
	})
	viewportH := 5
	s.SyncScrollMetrics(viewportH, len(lines))
	w := innerW + padLR*2 + 1
	h := viewportH
	buf := s.PaintTestBuffer(w, h)

	sbX := w - padLR - 1
	trackCols := 0
	thumbCols := 0
	for y := 0; y < h; y++ {
		c := buf.Get(sbX, y)
		if c.Char == '░' {
			trackCols++
		}
		if c.Char == '█' {
			thumbCols++
		}
	}
	if trackCols+thumbCols == 0 {
		t.Fatal("expected ScrollbarControl to paint track/thumb in gutter column")
	}
	if thumbCols == 0 {
		t.Fatal("expected thumb glyph in main gutter")
	}

	// Row text must not embed legacy main scrollbar glyphs (check content area only).
	for y := 0; y < h; y++ {
		for x := 0; x < sbX; x++ {
			ch := buf.Get(x, y).Char
			if ch == '░' || ch == '█' {
				t.Fatalf("legacy main gutter glyph %q at (%d,%d) inside row text", ch, x, y)
			}
		}
	}
}

func TestShellChatMainScrollbarThemedNoScrollFill(t *testing.T) {
	th := theme.DefaultDark()
	s := New(th)
	padLR := th.Spacing.PadLR
	innerW := 30
	s.SetMessages([]chatprojection.MessageRow{
		{ID: "m1", Lines: []string{"fits in viewport"}},
	}, PaintParams{
		Compose: ComposeParams{
			InnerW:        innerW,
			PadLR:         padLR,
			Theme:         th,
			MainSBVisible: true,
		},
	})
	s.SyncScrollMetrics(3, 1)
	w := innerW + padLR*2 + 1
	buf := s.PaintTestBuffer(w, 3)

	sbX := w - padLR - 1
	filled := 0
	for y := 0; y < 3; y++ {
		c := buf.Get(sbX, y)
		if !c.Transparent && !c.BG.IsDefault() {
			filled++
		}
	}
	if filled != 3 {
		t.Fatalf("expected themed gutter fill on all rows when not scrollable, got %d/3", filled)
	}
	for y := 0; y < 3; y++ {
		if ch := buf.Get(sbX, y).Char; ch == '░' || ch == '█' {
			t.Fatalf("no-scroll gutter should not paint track/thumb at row %d", y)
		}
	}
}

func TestShellChatMainScrollbarThumbAlignsAcrossRows(t *testing.T) {
	th := theme.DefaultDark()
	s := New(th)
	padLR := th.Spacing.PadLR
	innerW := 24
	lines := make([]string, 20)
	for i := range lines {
		lines[i] = "row content"
	}
	s.SetMessages([]chatprojection.MessageRow{{ID: "m1", Lines: lines}}, PaintParams{
		Compose: ComposeParams{
			InnerW:        innerW,
			PadLR:         padLR,
			Theme:         th,
			MainSBVisible: true,
		},
	})
	viewportH := 6
	s.SyncScrollMetrics(viewportH, len(lines))
	w := innerW + padLR*2 + 1
	buf := s.PaintTestBuffer(w, viewportH)

	sbX := w - padLR - 1
	thumbRows := 0
	for y := 0; y < viewportH; y++ {
		if buf.Get(sbX, y).Char == '█' {
			thumbRows++
		}
	}
	if thumbRows < 1 {
		t.Fatal("expected contiguous thumb over gutter column")
	}
	top, thH := s.ScrollModel().ThumbBounds(viewportH)
	if thumbRows != thH {
		t.Fatalf("painted thumb rows=%d want ThumbBounds height=%d (top=%d)", thumbRows, thH, top)
	}
}

func TestComposeOmitsMainScrollbarGlyphs(t *testing.T) {
	th := theme.DefaultDark()
	lines := Compose(ComposeParams{
		RawLines:      []string{"hello"},
		InnerW:        20,
		PadLR:         th.Spacing.PadLR,
		Theme:         th,
		MainSBVisible: true,
	})
	if len(lines) != 1 {
		t.Fatalf("want 1 line, got %d", len(lines))
	}
	plain := stripANSI(lines[0])
	if strings.Contains(plain, "█") || strings.Contains(plain, "░") {
		t.Fatalf("composed row must not embed main scrollbar glyphs: %q", plain)
	}
}

func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && s[j] != 'm' {
				j++
			}
			if j < len(s) {
				i = j + 1
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}
