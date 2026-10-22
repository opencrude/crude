package shellfooter

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

// hintSegments is a realistic compact guidance group (editor focus): three
// ranked hints plus the Help entry, 61 columns wide.
func hintSegments() []Segment {
	return []Segment{
		{Text: "Ctrl+F find", Priority: 3},
		{Text: "Ctrl+] next tab", Priority: 3},
		{Text: "Ctrl+1 focus file tree", Priority: 3},
		{Text: "Help", Action: "help", Priority: 1},
	}
}

func paintFooter(t *testing.T, projection Projection, width int) (*Screen, string) {
	t.Helper()
	screen, buf := paintFooterBuffer(t, theme.DefaultDark(), projection, width)
	return screen, strings.TrimRight(rowText(buf, 1), " ")
}

// paintFooterBuffer paints the footer on its own buffer so tests can inspect the
// painted fills, not just the text.
func paintFooterBuffer(t *testing.T, th *theme.Theme, projection Projection, width int) (*Screen, *surface.CellBuffer) {
	t.Helper()
	screen := New(th, ui.DefaultRegistry())
	screen.SetProjection(projection)
	screen.Layout(ui.LayoutContext{}, ui.Rect{X: 0, Y: 0, W: width, H: 2})
	buf := surface.NewCellBuffer(width, 2)
	screen.Paint(buf, 0, 0, ui.PaintContext{})
	return screen, buf
}

func rowText(buf *surface.CellBuffer, y int) string {
	var b strings.Builder
	for x := 0; x < buf.Width(); x++ {
		if c := buf.Get(x, y); c.Char != 0 {
			b.WriteRune(c.Char)
		} else {
			b.WriteRune(' ')
		}
	}
	return b.String()
}

func railColumns(width int) (int, int) {
	return ui.ShellContentRail(ui.NewTokenSet(theme.DefaultDark()), width)
}

func firstTextColumn(row string) int {
	for index, r := range []rune(row) {
		if r != ' ' {
			return index
		}
	}
	return -1
}

func lastTextColumn(row string) int {
	runes := []rune(row)
	for index := len(runes) - 1; index >= 0; index-- {
		if runes[index] != ' ' {
			return index
		}
	}
	return -1
}

// The guidance group (contextual hints, then Help) owns the left rail and the
// quieter status group is right-aligned on the shared content rails.
func TestGuidanceGroupLeadsAndStatusGroupTrails(t *testing.T) {
	const width = 100
	projection := Projection{
		Left:  []Segment{{Text: "Ctrl+O attach file", Priority: 3}, {Text: "Help", Action: "help", Priority: 1}},
		Right: []Segment{{Text: "LSP 3", Action: "workspace", Priority: 4}, {Text: "Status", Action: "workspace", Priority: 5}},
	}
	_, row := paintFooter(t, projection, width)
	left, right := railColumns(width)
	if got := firstTextColumn(row); got != left {
		t.Fatalf("guidance group starts at column %d, want shared left rail %d: %q", got, left, row)
	}
	if got := lastTextColumn(row); got != right-1 {
		t.Fatalf("status group ends at column %d, want shared right rail %d: %q", got, right-1, row)
	}
	if !strings.HasPrefix(row[left:], "Ctrl+O attach file · Help") {
		t.Fatalf("left rail does not lead with the guidance group: %q", row)
	}
	if !strings.HasSuffix(row, "LSP 3 · Status") {
		t.Fatalf("right rail does not carry the status group: %q", row)
	}
}

// The status group owns at most half the row, so it can never truncate the
// guidance group: a full compact hint set stays readable beside a short
// status group even when that hint set is wider than half the row.
func TestStatusGroupNeverTruncatesTheGuidanceGroup(t *testing.T) {
	const width = 100 // inner row 96, half 48 < the 61-column hint set
	projection := Projection{
		Left:  hintSegments(),
		Right: []Segment{{Text: "Status", Action: "workspace", Priority: 5}},
	}
	_, row := paintFooter(t, projection, width)
	for _, text := range []string{"Ctrl+F find", "Ctrl+] next tab", "Ctrl+1 focus file tree", "Help"} {
		if !strings.Contains(row, text) {
			t.Fatalf("guidance hint %q dropped by the status group: %q", text, row)
		}
	}
	if !strings.HasSuffix(row, "Status") {
		t.Fatalf("status group not right-aligned: %q", row)
	}
}

// A demanding status group is capped at half the row and is right-aligned: it
// still ends on the shared right rail and never reaches into the guidance
// group's half.
func TestStatusGroupIsCappedAtHalfTheRow(t *testing.T) {
	const width = 100
	projection := Projection{
		Left: hintSegments(),
		Right: []Segment{
			{Text: "LSP 3 failed", Action: "workspace", Priority: 1},
			{Text: "Tests failing: 12", Action: "workspace", Priority: 1},
			{Text: "9 background agents", Action: "activity", Priority: 1},
			{Text: "Status", Action: "workspace", Priority: 5},
		},
	}
	_, row := paintFooter(t, projection, width)
	left, right := railColumns(width)
	if got := firstTextColumn(row); got != left {
		t.Fatalf("guidance group starts at column %d, want shared left rail %d: %q", got, left, row)
	}
	if got := lastTextColumn(row); got != right-1 {
		t.Fatalf("status group ends at column %d, want shared right rail %d: %q", got, right-1, row)
	}
	status := "LSP 3 failed · Tests failing: 12 · Status"
	if !strings.HasSuffix(row, status) {
		t.Fatalf("status group lost its own capacity (or order): %q", row)
	}
	if start := strings.Index(row, status); start < left+(right-left)/2 {
		t.Fatalf("status group starts at %d, inside the guidance group's half of the row: %q", start, row)
	}
}

// Both groups keep their hit targets after the move: the guidance group is
// hit-tested from the left rail, the status group from its right-aligned
// origin, and non-actionable hints stay inert.
func TestMovedGroupsKeepTheirHitTargets(t *testing.T) {
	const width = 100
	projection := Projection{
		Left:  []Segment{{Text: "Ctrl+O attach file", Priority: 3}, {Text: "Help", Action: "help", Priority: 1}},
		Right: []Segment{{Text: "Status", Action: "workspace", Priority: 5}},
	}
	screen, row := paintFooter(t, projection, width)
	click := func(column int) ui.EventResult {
		return screen.Handle(ui.EventContext{LocalX: column, LocalY: 1}, ui.MouseClickEvent(tea.MouseClickMsg{Button: tea.MouseLeft}))
	}
	for _, target := range []struct{ text, action string }{{"Help", "help"}, {"Status", "workspace"}} {
		res := click(strings.Index(row, target.text))
		if !res.Handled || res.Cmd == nil {
			t.Fatalf("click on %q (column %d) unresolved: %+v", target.text, strings.Index(row, target.text), res)
		}
		msg, ok := res.Cmd().(ActionMsg)
		if !ok || msg.Action != target.action {
			t.Fatalf("click on %q emitted %#v, want action %q", target.text, res.Cmd(), target.action)
		}
	}
	if res := click(strings.Index(row, "Ctrl+O attach file")); res.Handled || res.Cmd != nil {
		t.Fatalf("non-actionable hint became clickable: %+v", res)
	}
}

func TestPillNavigationPreservesGuidanceAndFeedback(t *testing.T) {
	right := []Segment{
		{Text: "Problems", Pill: true, Action: "problems", Priority: 1},
		{Text: "Notices", Pill: true, Action: "notices", Priority: 1},
		{Text: "Terminals", Pill: true, Action: "terminals", Priority: 1},
		{Text: "Git", Pill: true, Action: "git", Priority: 1},
		{Text: "Board", Pill: true, Action: "board"},
		{Text: "Memory", Pill: true, Action: "memory"},
		{Text: "Performance", Pill: true, Action: "performance"},
		{Text: "Workspace", Pill: true, Action: "workspace"},
	}
	for _, test := range []struct {
		name string
		left []Segment
	}{
		{"guidance", []Segment{{Text: "Alt+Enter new line", Priority: 3}, {Text: "Ctrl+O attach file", Priority: 3}, {Text: "Help", Action: "help", Priority: 1}}},
		{"feedback", []Segment{{Text: "Samuel sub Rebecca · Researcher completed"}, {Text: "Help", Action: "help", Priority: 1}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			screen, row := paintFooter(t, Projection{Left: test.left, Right: right}, 120)
			for _, segment := range test.left {
				if !strings.Contains(row, segment.Text) {
					t.Fatalf("navigation crowded out %q: %q", segment.Text, row)
				}
			}
			if !strings.Contains(row, "Performance") || !strings.Contains(row, "More") || len(screen.Overflow()) == 0 {
				t.Fatalf("navigation did not retain Performance and explicit overflow: %q", row)
			}
		})
	}
}

func BenchmarkFooterOverflowPaint(b *testing.B) {
	screen := New(theme.DefaultDark(), ui.DefaultRegistry())
	screen.SetProjection(Projection{
		Left: []Segment{{Text: "Alt+Enter new line", Priority: 3}, {Text: "Ctrl+O attach file", Priority: 3}, {Text: "Help", Action: "help", Priority: 1}},
		Right: []Segment{
			{Text: "Problems", Pill: true, Action: "problems", Priority: 1},
			{Text: "Notices", Pill: true, Action: "notices", Priority: 1},
			{Text: "Terminals", Pill: true, Action: "terminals", Priority: 1},
			{Text: "Git", Pill: true, Action: "git", Priority: 1},
			{Text: "Board", Pill: true, Action: "board"},
			{Text: "Memory", Pill: true, Action: "memory"},
			{Text: "Performance", Pill: true, Action: "performance"},
			{Text: "Workspace", Pill: true, Action: "workspace"},
		},
	})
	screen.Layout(ui.LayoutContext{}, ui.Rect{W: 120, H: 2})
	buf := surface.NewCellBuffer(120, 2)
	screen.Paint(buf, 0, 0, ui.PaintContext{})
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		screen.Paint(buf, 0, 0, ui.PaintContext{})
	}
}

func TestFooterRepaintClearsReplacedNavigation(t *testing.T) {
	screen, buf := paintFooterBuffer(t, theme.DefaultDark(), Projection{Right: stripSegments()}, 36)
	if len(screen.Overflow()) == 0 {
		t.Fatal("fixture did not exercise retained overflow")
	}
	screen.SetProjection(Projection{Right: []Segment{{Text: "Performance", Action: "performance", Pill: true}}})
	screen.Paint(buf, 0, 0, ui.PaintContext{})
	if len(screen.Overflow()) != 0 || len(screen.hits) != 1 || screen.hits[0].action != "performance" {
		t.Fatalf("repaint retained old destinations: overflow=%+v hits=%+v", screen.Overflow(), screen.hits)
	}
	screen.SetProjection(Projection{})
	screen.Paint(buf, 0, 0, ui.PaintContext{})
	if len(screen.Overflow()) != 0 || len(screen.hits) != 0 || strings.TrimSpace(rowText(buf, 1)) != "" {
		t.Fatal("empty projection retained footer ink or actions")
	}
}
