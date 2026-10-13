package settings

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
)

func diffContentRows(bufA, bufB *surface.CellBuffer, x0, x1, w, h int) int {
	diff := 0
	for y := 0; y < h; y++ {
		for x := x0; x < x1 && x < w; x++ {
			if bufA.Get(x, y).Char != bufB.Get(x, y).Char {
				diff++
				break
			}
		}
	}
	return diff
}

// TestDragScrollbarMovesForm does a real click+drag on the form scrollbar
// through the Host and verifies the form field content actually moves.
func TestDragScrollbarMovesForm(t *testing.T) {
	th := theme.DefaultDark()
	model := New(th, types.AppSettings{}, nil)
	model.SelectCategory(catLSP)
	surfaceRoot := NewSurface(th, model)
	surfaceRoot.refreshForm()
	host := ui.NewHost(th, surfaceRoot)
	const w, h = 90, 24
	host.Resize(w, h)
	host.Layout()
	bar := surfaceRoot.formScroll.Bounds()
	if bar.Empty() {
		t.Fatal("no form scrollbar")
	}
	bufA := surface.NewCellBuffer(w, h)
	host.Paint(bufA)

	top, _ := surfaceRoot.form.Scroll().ThumbBounds(bar.H)
	grab := bar.Y + top
	cx := bar.X + bar.W/2

	host.Handle(tea.MouseClickMsg{X: cx, Y: grab, Button: tea.MouseLeft})
	host.Handle(tea.MouseMotionMsg{X: cx, Y: grab + 5, Button: tea.MouseLeft})

	bufB := surface.NewCellBuffer(w, h)
	host.Paint(bufB)

	off := surfaceRoot.form.Scroll().Offset()
	textDiff := diffContentRows(bufA, bufB, 40, 80, w, h)
	t.Logf("drag offset=%d contentDiffRows=%d", off, textDiff)
	if surfaceRoot.form.Scroll().MaxOffset() <= 0 {
		t.Fatal("harness: form must overflow")
	}
	if off == 0 {
		t.Fatal("dragging scrollbar did not change form ScrollModel offset")
	}
	if textDiff == 0 {
		t.Fatal("form ScrollModel moved but painted field content did not change")
	}
}

// TestWheelScrollbarMovesForm drives a wheel over the form scrollbar.
func TestWheelScrollbarMovesForm(t *testing.T) {
	th := theme.DefaultDark()
	model := New(th, types.AppSettings{}, nil)
	model.SelectCategory(catLSP)
	surfaceRoot := NewSurface(th, model)
	surfaceRoot.refreshForm()
	host := ui.NewHost(th, surfaceRoot)
	const w, h = 90, 24
	host.Resize(w, h)
	host.Layout()
	bar := surfaceRoot.formScroll.Bounds()
	if bar.Empty() {
		t.Fatal("no form scrollbar")
	}
	bufA := surface.NewCellBuffer(w, h)
	host.Paint(bufA)
	cx := bar.X + bar.W/2
	cy := bar.Y + 5

	host.Handle(tea.MouseWheelMsg{X: cx, Y: cy, Button: tea.MouseWheelDown})
	host.Handle(tea.MouseWheelMsg{X: cx, Y: cy, Button: tea.MouseWheelDown})

	bufB := surface.NewCellBuffer(w, h)
	host.Paint(bufB)
	off := surfaceRoot.form.Scroll().Offset()
	textDiff := diffContentRows(bufA, bufB, 40, 80, w, h)
	t.Logf("wheel offset=%d contentDiffRows=%d", off, textDiff)
	if off == 0 {
		t.Fatal("wheel on scrollbar did not change form offset")
	}
	if textDiff == 0 {
		t.Fatal("wheel changed form offset but painted content did not move")
	}
}

func TestTrackClickPagesFormAndMovesPaintedContent(t *testing.T) {
	th := theme.DefaultDark()
	model := New(th, types.AppSettings{}, nil)
	model.SelectCategory(catLSP)
	surfaceRoot := NewSurface(th, model)
	surfaceRoot.refreshForm()
	host := ui.NewHost(th, surfaceRoot)
	const w, h = 90, 24
	host.Resize(w, h)
	host.Layout()
	bar := surfaceRoot.formScroll.Bounds()
	if bar.Empty() {
		t.Fatal("no form scrollbar")
	}
	bufA := surface.NewCellBuffer(w, h)
	host.Paint(bufA)
	top, thumbH := surfaceRoot.form.Scroll().ThumbBounds(bar.H)
	clickY := bar.Y + top + thumbH
	if clickY >= bar.Y+bar.H {
		clickY = bar.Y
	}

	host.Handle(tea.MouseClickMsg{X: bar.X, Y: clickY, Button: tea.MouseLeft})
	bufB := surface.NewCellBuffer(w, h)
	host.Paint(bufB)
	if off := surfaceRoot.form.Scroll().Offset(); off == 0 {
		t.Fatal("track click did not page the form")
	}
	if diff := diffContentRows(bufA, bufB, 40, 80, w, h); diff == 0 {
		t.Fatal("track click moved offset but not painted form content")
	}
	if surfaceRoot.formScroll.IsDragging() {
		t.Fatal("track click must not enter thumb-drag state")
	}
}

// TestDragScrollbarMovesList drives click+drag on the category-list
// scrollbar and verifies row content scrolls.
func TestDragScrollbarMovesList(t *testing.T) {
	th := theme.DefaultDark()
	model := New(th, types.AppSettings{}, nil)
	model.SelectCategory(catLSP)
	surfaceRoot := NewSurface(th, model)
	surfaceRoot.refreshForm()
	host := ui.NewHost(th, surfaceRoot)
	const w, h = 90, 24
	host.Resize(w, h)
	host.Layout()
	bar := surfaceRoot.listScroll.Bounds()
	if bar.Empty() {
		t.Skip("category list does not overflow at this size")
	}
	bufA := surface.NewCellBuffer(w, h)
	host.Paint(bufA)
	top, _ := surfaceRoot.categories.list.Scroll().ThumbBounds(bar.H)
	grab := bar.Y + top
	cx := bar.X + bar.W/2

	hit := host.HitTest(cx, grab)
	t.Logf("list hit=%q bar=%+v", hit, bar)
	host.Handle(tea.MouseClickMsg{X: cx, Y: grab, Button: tea.MouseLeft})
	host.Handle(tea.MouseMotionMsg{X: cx, Y: grab + 6, Button: tea.MouseLeft})

	bufB := surface.NewCellBuffer(w, h)
	host.Paint(bufB)
	off := surfaceRoot.categories.list.Scroll().Offset()
	textDiff := diffContentRows(bufA, bufB, 3, 28, w, h)
	t.Logf("list offset=%d contentDiffRows=%d", off, textDiff)
	if surfaceRoot.categories.list.Scroll().MaxOffset() <= 0 {
		t.Skip("category list does not overflow; nothing to assert")
	}
	if off == 0 {
		t.Fatal("dragging list scrollbar did not change list offset")
	}
	if textDiff == 0 {
		t.Fatal("list offset moved but painted rows did not change")
	}
}
