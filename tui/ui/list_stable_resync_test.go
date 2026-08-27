package ui

import (
	"fmt"
	"testing"
)

func stableRows(n int) []ListItem {
	rows := make([]ListItem, n)
	for i := range rows {
		rows[i] = ListItem{ID: fmt.Sprintf("row-%02d", i), Label: fmt.Sprintf("row %d", i)}
	}
	return rows
}

// SetItemsStable exists so live resyncs (spinner ticks, status refreshes)
// never clobber the reader's view: selection is matched by ID and the scroll
// offset is re-anchored instead of snapping toward the first row.
func TestListSetItemsStablePreservesSelectionAndOffset(t *testing.T) {
	l := NewList("stable-test", nil)
	l.SetItems(stableRows(30)) // selects row-00
	if !l.SelectIndex(25) {
		t.Fatal("could not select row-25")
	}
	l.Scroll().SetViewport(8)
	l.Scroll().ScrollTo(l.Scroll().MaxOffset()) // bottom
	bottom := l.Scroll().Offset()
	if bottom == 0 {
		t.Fatal("precondition: expected a scrolled-down viewport")
	}

	updated := stableRows(30)
	updated[25].Detail = "refreshed meta"
	l.SetItemsStable(updated)

	it, ok := l.Selected()
	if !ok || it.ID != "row-25" {
		t.Fatalf("selection lost during resync: id=%q ok=%v", it.ID, ok)
	}
	if l.Scroll().Offset() != bottom {
		t.Fatalf("scroll offset snapped during resync: %d → %d", bottom, l.Scroll().Offset())
	}
}

func TestListSetItemsStableFallsBackWhenSelectionVanishes(t *testing.T) {
	l := NewList("stable-fallback", nil)
	l.SetItems(stableRows(30))
	if !l.SelectIndex(25) {
		t.Fatal("could not select row-25")
	}
	l.Scroll().SetViewport(8)
	l.Scroll().ScrollTo(l.Scroll().MaxOffset())

	l.SetItemsStable(stableRows(5)) // row-25 removed

	it, ok := l.Selected()
	if !ok || it.ID != "row-00" {
		t.Fatalf("fallback selection = id=%q ok=%v, want first selectable row-00", it.ID, ok)
	}
	if off := l.Scroll().Offset(); off != 0 {
		t.Fatalf("fallback offset = %d, want 0 (EnsureVisible(first))", off)
	}
}

func TestListSetItemsStableSkipsHeadersOnRestore(t *testing.T) {
	l := NewList("stable-header", nil)
	rows := stableRows(10)
	l.SetItems(rows)
	if !l.SelectIndex(3) {
		t.Fatal("could not select row-03")
	}
	l.Scroll().SetViewport(4)
	l.Scroll().ScrollTo(l.Scroll().MaxOffset())
	prev := l.Scroll().Offset()

	next := []ListItem{{ID: "header:x", Label: "Today", Header: true}}
	next = append(next, stableRows(10)...)
	l.SetItemsStable(next)

	it, ok := l.Selected()
	if !ok || it.ID != "row-03" {
		t.Fatalf("selection restored onto id=%q ok=%v, want row-03", it.ID, ok)
	}
	if l.Scroll().Offset() != prev {
		t.Fatalf("offset changed: %d → %d", prev, l.Scroll().Offset())
	}
	if _, sel := l.Selected(); sel && !l.items[l.SelectedIndex()].Selectable() {
		t.Fatal("restored selection landed on a non-selectable row")
	}
}
