package termview

import (
	"crude/terminalvt"
	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"errors"
	"strings"
	"testing"
)

func TestWideSelectionAndCursorPreserveGrapheme(t *testing.T) {
	var v View
	g := terminalvt.Projection{Cols: 8, GridRows: 2, Total: 2, Cells: [][]terminalvt.Cell{{{Text: "👩‍💻", Width: 2}, {Width: 0}, {Text: "x", Width: 1}}, {}}, Cursor: terminalvt.Cursor{X: 1, Y: 0, Visible: true, Blink: true}}
	v.Install(g, 1)
	v.SelectionPress(1, 0, 1)
	v.SelectionDrag(1, 0)
	text, ok := v.SelectionText()
	if !ok || text != "👩‍💻" {
		t.Fatalf("selected=%q ok=%v", text, ok)
	}
	v.ClearSelection()
	buf := surface.NewCellBuffer(8, 2)
	v.Paint(buf, 0, 0, 8, 2, ui.NewTokenSet(theme.DefaultDark()), true, true)
	if c := buf.Get(0, 0); c.Content != "👩‍💻" || c.Width != 2 || c.Attr&cell.AttrReverse == 0 {
		t.Fatalf("cursor grapheme=%+v", c)
	}
	if c := buf.Get(1, 0); c.Width != 0 {
		t.Fatalf("continuation=%+v", c)
	}
}

func TestEngineWrappedCopyPreservesSource(t *testing.T) {
	for _, source := range []string{"abcdef  ijklmnopq", "abcdefg界ijklmnop", "abcdefghijklmnop\r\nhard"} {
		t.Run(source, func(t *testing.T) {
			e, err := terminalvt.New(terminalvt.Options{Cols: 8, Rows: 6})
			if errors.Is(err, terminalvt.ErrNoCGO) {
				t.Skip(err)
			}
			if err != nil {
				t.Fatal(err)
			}
			defer e.Close()
			e.Feed([]byte(source))
			g, err := e.Rows(terminalvt.RowRequest{Start: -1, Count: 0})
			if err != nil {
				t.Fatal(err)
			}
			var v View
			v.Install(g, 1)
			v.SelectionPress(0, 0, 1)
			v.SelectionDrag(g.Cursor.X-1, g.Cursor.Y)
			got, ok := v.SelectionText()
			want := strings.ReplaceAll(source, "\r\n", "\n")
			if !ok || got != want {
				t.Fatalf("copy=%q want=%q ok=%v wraps=%v", got, want, ok, g.Wrapped)
			}
			g.Wrapped[0] = false
			if got, _ := v.SelectionText(); got != want {
				t.Fatal("wrap snapshot aliases caller")
			}
		})
	}
}

func TestEngineOutputPreservesScrolledSelectionAndInvalidatesEviction(t *testing.T) {
	e, err := terminalvt.New(terminalvt.Options{Cols: 8, Rows: 2, ScrollbackLines: 5})
	if errors.Is(err, terminalvt.ErrNoCGO) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	e.Feed([]byte("selected\r\nother\r\nnew\r\n"))
	g, err := e.Rows(terminalvt.RowRequest{Start: 0, Count: 5})
	if err != nil {
		t.Fatal(err)
	}
	var a, b View
	a.Install(g, 1)
	b.Install(g, 1)
	a.ScrollTop()
	a.SelectionPress(0, 0, 1)
	a.SelectionDrag(7, 0)
	e.Feed([]byte("more\r\n"))
	g, err = e.Rows(terminalvt.RowRequest{Start: -1, Count: 0})
	if err != nil {
		t.Fatal(err)
	}
	a.Install(g, 2)
	b.Install(g, 2)
	if !a.HasSelection() || a.Offset != 0 || !b.Pinned() || b.HasSelection() {
		t.Fatal("output changed independent scroll/selection state")
	}
	if got, ok := a.SelectionText(); !ok || got != "selected" {
		t.Fatal("immutable selected history was discarded on ingress", got, ok)
	}
	g, err = e.Rows(terminalvt.RowRequest{Start: 0, Count: 2})
	if err != nil {
		t.Fatal(err)
	}
	a.Install(g, 2)
	a.SelectionDrag(7, 0)
	if got, ok := a.SelectionText(); !ok || got != "selected" {
		t.Fatalf("continued drag=%q ok=%v", got, ok)
	}
	tokens := ui.NewTokenSet(theme.DefaultDark())
	buf := surface.NewCellBuffer(8, 2)
	a.Paint(buf, 0, 0, 8, 2, tokens, true, true)
	if c := buf.Get(0, 0); c.Char != 's' || c.BG != tokens.Color(ui.ColorTextSelection) {
		t.Fatalf("selected history not painted after output: %+v", c)
	}
	for i := 0; i < 800; i++ {
		e.Feed([]byte("more\r\n"))
	}
	g, err = e.Rows(terminalvt.RowRequest{Start: -1, Count: 0})
	if err != nil {
		t.Fatal(err)
	}
	a.Install(g, 3)
	a.SelectionDrag(7, 0)
	if a.HasSelection() {
		t.Fatal("evicted drag resurrected stale anchor")
	}
	a.SelectionPress(0, 0, 1_000_000_000)
	a.SelectionDrag(3, 0)
	if err := e.Resize(12, 2); err != nil {
		t.Fatal(err)
	}
	g, err = e.Rows(terminalvt.RowRequest{Start: -1, Count: 0})
	if err != nil {
		t.Fatal(err)
	}
	a.Install(g, 4)
	if a.HasSelection() {
		t.Fatal("reflow kept stale selection")
	}
}
func TestIndependentViewsAndSourceGeneration(t *testing.T) {
	g := terminalvt.Projection{Cols: 8, GridRows: 2, Total: 20, Start: 18, Cells: [][]terminalvt.Cell{{{Text: "last", Width: 1}}, {}}}
	var a, b View
	a.Install(g, 2)
	b.Install(g, 2)
	a.ScrollTop()
	if !b.Pinned() || a.Pinned() {
		t.Fatal("views share scroll")
	}
	g.Cells[0][0].Text = "mutated"
	row, _ := b.Row(18)
	if row[0].Text != "last" {
		t.Fatal("install aliases rows")
	}
	if b.Install(g, 1) {
		t.Fatal("older generation applied")
	}
	a.SelectAll()
	if text, ok := a.SelectionText(); ok || text != "" {
		t.Fatal("missing history copied")
	}
}

func TestSynchronizedHistoryRetainsUsableRowsAndRejectsLateRead(t *testing.T) {
	var v View
	rows := make([][]terminalvt.Cell, 6)
	for i := range rows {
		rows[i] = []terminalvt.Cell{{Text: string(rune('a' + i)), Width: 1}}
	}
	g := terminalvt.Projection{Cols: 8, GridRows: 2, Total: 6, Cells: rows, HistoryEpoch: 1, ReadSequence: 1}
	v.Install(g, 1)
	v.ScrollTop()
	v.SelectionPress(0, 0, 1)
	v.SelectionDrag(0, 0)
	g.Start, g.Cells, g.Synchronized, g.ReadSequence = 4, rows[4:], true, 2
	v.Install(g, 2)
	if v.Offset != 0 || !v.HasSelection() {
		t.Fatal("pending synchronized update displaced scroll/selection")
	}
	buf := surface.NewCellBuffer(8, 2)
	v.Paint(buf, 0, 0, 8, 2, ui.NewTokenSet(theme.DefaultDark()), true, true)
	if buf.Get(0, 0).Char != 'a' || buf.Get(0, 1).Char != 'b' {
		t.Fatal("synchronized invalidation blanked cached history")
	}
	var viewer View
	viewer.Install(g, 2)
	viewer.ScrollTop()
	if !viewer.Pinned() {
		t.Fatal("new viewer scrolled from usable active grid into unavailable rows")
	}
	// Timeout releases without new PTY ingress, so only read ordering separates
	// the completed response from a delayed pre-timeout page of the same gen.
	completed := g
	completed.Synchronized, completed.ReadSequence = false, 3
	completed.Cells = [][]terminalvt.Cell{{{Text: "new", Width: 1}}, {}}
	v.ScrollBottom()
	v.Install(completed, 2)
	if v.Install(g, 2) || v.Grid.Synchronized {
		t.Fatal("late same-generation synchronized page replaced completed grid")
	}
	completed.HistoryEpoch++
	completed.ReadSequence++
	v.Install(completed, 2)
	if v.HasSelection() {
		t.Fatal("identity-changing commit retained stale selection")
	}
}
