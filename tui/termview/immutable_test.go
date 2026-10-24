package termview

import (
	"reflect"
	"testing"

	"crude/terminalvt"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

func terminalInstallPage() terminalvt.Projection {
	g := terminalvt.Projection{Cols: 200, GridRows: 50, Total: 50}
	g.Cells = make([][]terminalvt.Cell, g.GridRows)
	g.Wrapped = make([]bool, g.GridRows)
	for y := range g.Cells {
		g.Cells[y] = make([]terminalvt.Cell, g.Cols)
		for x := range g.Cells[y] {
			g.Cells[y][x] = terminalvt.Cell{Text: "x", Width: 1, FG: terminalvt.Color{Explicit: true, R: byte(x), G: byte(y)}}
		}
	}
	return g
}

func BenchmarkTerminalViewInstall200x50(b *testing.B) {
	g := terminalInstallPage()
	var v View
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		if !v.Install(g, uint64(i+1)) {
			b.Fatal("install rejected")
		}
	}
}

func BenchmarkTerminalViewInstallImmutable200x50(b *testing.B) {
	g := terminalInstallPage()
	var v View
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		if !v.InstallImmutable(g, uint64(i+1)) {
			b.Fatal("install rejected")
		}
	}
}

func TestInstallImmutableRetainsRowsAndDetachesPageHeaders(t *testing.T) {
	g := terminalvt.Projection{Cols: 8, GridRows: 2, Total: 2, Cells: [][]terminalvt.Cell{{{Text: "hello ", Width: 1}}, {{Text: "world", Width: 1}}}, Wrapped: []bool{true, false}, ReadSequence: 2}
	rows := append([][]terminalvt.Cell(nil), g.Cells...)
	want := [][]terminalvt.Cell{append([]terminalvt.Cell(nil), rows[0]...), append([]terminalvt.Cell(nil), rows[1]...)}
	var a, b View
	if !a.InstallImmutable(g, 2) || !b.InstallImmutable(g, 2) {
		t.Fatal("immutable page rejected")
	}
	if &a.Grid.Cells[0][0] != &rows[0][0] || &b.Grid.Cells[0][0] != &rows[0][0] {
		t.Fatal("immutable cells were redundantly cloned")
	}
	// Page containers remain caller-owned, unlike the immutable row contents.
	g.Cells[0], g.Wrapped[0] = nil, false
	a.SelectAll()
	if text, ok := a.SelectionText(); !ok || text != "hello world" {
		t.Fatalf("copy lost retained rows/wraps: %q %v", text, ok)
	}
	buf := surface.NewCellBuffer(8, 2)
	a.Paint(buf, 0, 0, 8, 2, ui.NewTokenSet(theme.DefaultDark()), true, true)
	if buf.Get(0, 0).Content != "hello " || b.HasSelection() {
		t.Fatal("selection/paint lost rows or changed a peer view")
	}
	if !reflect.DeepEqual(rows, want) {
		t.Fatal("selection/copy/paint mutated immutable source cells")
	}
	stale := b.Grid
	stale.ReadSequence--
	if b.InstallImmutable(stale, 2) || b.InstallImmutable(b.Grid, 1) {
		t.Fatal("immutable install bypassed read/generation fences")
	}
}

func TestInstallImmutableAllocationBound(t *testing.T) {
	g := terminalInstallPage()
	var v View
	v.InstallImmutable(g, 1)
	seq := uint64(1)
	allocs := testing.AllocsPerRun(100, func() {
		seq++
		v.InstallImmutable(g, seq)
	})
	if allocs > 3 {
		t.Fatalf("immutable 200x50 page allocated %.0f times; want <=3 page-header allocations", allocs)
	}
}
