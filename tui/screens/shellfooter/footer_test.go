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
	screen := New(theme.DefaultDark(), ui.DefaultRegistry())
	screen.SetProjection(projection)
	screen.Layout(ui.LayoutContext{}, ui.Rect{X: 0, Y: 0, W: width, H: 2})
	buf := surface.NewCellBuffer(width, 2)
	screen.Paint(buf, 0, 0, ui.PaintContext{})
	return screen, strings.TrimRight(rowText(buf, 1), " ")
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
