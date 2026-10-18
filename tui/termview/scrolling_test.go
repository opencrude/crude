package termview

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"crude/terminalvt"
	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

func TestStreamingViewportRetainsHistoryButNeverPromotesStaleActiveRows(t *testing.T) {
	e, err := terminalvt.New(terminalvt.Options{Cols: 20, Rows: 3})
	if errors.Is(err, terminalvt.ErrNoCGO) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	e.Feed([]byte("history\r\nactive0\r\nactive1\r\nmutable"))
	g, err := e.Rows(terminalvt.RowRequest{Start: 0, Count: 5})
	if err != nil {
		t.Fatal(err)
	}
	var v View
	v.Install(g, 1)
	v.ScrollTop()
	v.SelectionPress(0, 0, 1)
	v.SelectionDrag(6, 0)
	// The formerly active bottom line is rewritten before it enters history.
	e.Feed([]byte("\rCHANGED\r\nnext\r\nlast\r\nend"))
	g, err = e.Rows(terminalvt.RowRequest{Start: -1, Count: 0})
	if err != nil {
		t.Fatal(err)
	}
	v.Install(g, 2)
	if copied, ok := v.SelectionText(); !ok || copied != "history" {
		t.Fatalf("stable history selection=%q complete=%v", copied, ok)
	}
	if _, ok := v.Row(3); ok {
		t.Fatal("former mutable active row was silently promoted into immutable cache")
	}
	v.ClearSelection()
	v.ScrollTo(3)
	if v.Offset != 0 || v.DesiredOffset() != 3 {
		t.Fatal("unread viewport was committed instead of retaining last usable rows")
	}
	g, err = e.Rows(terminalvt.RowRequest{Start: 3, Count: 0})
	if err != nil {
		t.Fatal(err)
	}
	v.Install(g, 2)
	buf := surface.NewCellBuffer(20, 3)
	v.Paint(buf, 0, 0, 20, 3, ui.NewTokenSet(theme.DefaultDark()), true, true)
	if buf.Get(0, 0).Char != 'C' || v.Offset != 3 {
		t.Fatal("refreshed historical viewport did not atomically present rewritten row")
	}
}

func TestStreamingCacheStaysBoundedAndEvictionNeverCopiesWrongRows(t *testing.T) {
	e, err := terminalvt.New(terminalvt.Options{Cols: 20, Rows: 3, ScrollbackLines: 1200})
	if errors.Is(err, terminalvt.ErrNoCGO) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	e.Feed([]byte(strings.Repeat("history\r\n", 1200)))
	var v View
	g, _ := e.Rows(terminalvt.RowRequest{Start: -1, Count: 0})
	v.Install(g, 1)
	for offset := 0; offset < v.Bottom(); offset += 3 {
		v.ScrollTo(offset)
		g, err = e.Rows(terminalvt.RowRequest{Start: offset, Count: 0})
		if err != nil {
			t.Fatal(err)
		}
		v.Install(g, 1)
		if len(v.rows) > terminalvt.MaxSnapshotRows+2*v.Grid.GridRows {
			t.Fatalf("navigation cache grew to %d rows", len(v.rows))
		}
	}
	v.SelectionPress(0, 0, 1)
	v.SelectionDrag(6, 0)
	e.Feed([]byte(strings.Repeat("replacement\r\n", 2400)))
	g, err = e.Rows(terminalvt.RowRequest{Start: v.DesiredOffset(), HistoryEpoch: v.Grid.HistoryEpoch, HistoryBase: v.Grid.HistoryBase})
	if err != nil {
		t.Fatal(err)
	}
	v.Install(g, 2)
	if v.HasSelection() || len(v.rows) != 3 || len(v.presented) != 3 {
		t.Fatalf("eviction kept stale selection/cache or blanked page: selection=%v cache=%d presented=%d", v.HasSelection(), len(v.rows), len(v.presented))
	}
	// Same-generation page completion ordering is independent of ingress.
	older := g
	g.ReadSequence++
	if !v.Install(g, 2) || v.Install(older, 2) {
		t.Fatal("late same-generation page replaced newer read")
	}
}

func TestPartialSelectionPageCannotChangePresentedTailCursor(t *testing.T) {
	var v View
	row := []terminalvt.Cell{{Text: "A", Width: 1}, {Text: "B", Width: 1}, {Text: "C", Width: 1}}
	g := terminalvt.Projection{Cols: 8, GridRows: 2, Total: 6, Start: 4, HistoryEpoch: 1, ReadSequence: 1, Cells: [][]terminalvt.Cell{row, row}, Cursor: terminalvt.Cursor{X: 0, Y: 0, Visible: true}}
	v.Install(g, 1)
	// An auxiliary copy page of a newer generation contains no active-grid
	// cells. Until the desired viewport arrives, its old cells AND cursor are
	// one complete publication, not old cells plus a new cursor position.
	g.Start, g.Cells, g.Cursor.X, g.ReadSequence = 0, [][]terminalvt.Cell{row}, 2, 2
	v.Install(g, 2)
	buf := surface.NewCellBuffer(8, 2)
	v.Paint(buf, 0, 0, 8, 2, ui.NewTokenSet(theme.DefaultDark()), true, true)
	if buf.Get(0, 0).Attr&cell.AttrReverse == 0 || buf.Get(2, 0).Attr&cell.AttrReverse != 0 {
		t.Fatal("partial page moved cursor over retained cells")
	}
}

func BenchmarkStreamingScrollViewport(b *testing.B) {
	for _, history := range []int{100, 10000} {
		b.Run(fmt.Sprint(history), func(b *testing.B) {
			e, err := terminalvt.New(terminalvt.Options{Cols: 80, Rows: 24})
			if err != nil {
				b.Fatal(err)
			}
			defer e.Close()
			e.Feed([]byte(strings.Repeat("dense scroll output abcdefghijklmnopqrstuvwxyz\r\n", history)))
			g, err := e.Rows(terminalvt.RowRequest{Start: -1, Count: 0})
			if err != nil {
				b.Fatal(err)
			}
			var v View
			v.Install(g, 1)
			g, _ = e.Rows(terminalvt.RowRequest{Start: 0, Count: 0})
			v.Install(g, 1)
			v.ScrollTop()
			buf := surface.NewCellBuffer(80, 24)
			tokens := ui.NewTokenSet(theme.DefaultDark())
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				e.Feed([]byte("new output\r\n"))
				g, err = e.Rows(terminalvt.RowRequest{Start: 0, Count: 0})
				if err != nil {
					b.Fatal(err)
				}
				v.Install(g, uint64(i+2))
				v.Paint(buf, 0, 0, 80, 24, tokens, false, true)
			}
		})
	}
}
