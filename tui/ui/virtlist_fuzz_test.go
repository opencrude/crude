package ui

import "testing"

// FuzzVirtualListItemAtRow verifies prefix rebuild and row→item lookup stay
// consistent for variable-height items and bottom padding.
func FuzzVirtualListItemAtRow(f *testing.F) {
	f.Add(0, 1, 1, 1, 0)
	f.Add(3, 2, 1, 3, 2)
	f.Add(5, 1, 2, 4, 1)

	f.Fuzz(func(t *testing.T, h0, h1, h2, pad, queryRow int) {
		normalize := func(h int) int {
			if h < 1 {
				return 1
			}
			if h > 20 {
				return 20
			}
			return h
		}
		h0, h1, h2 = normalize(h0), normalize(h1), normalize(h2)
		if pad < 0 {
			pad = 0
		}
		if pad > 10 {
			pad = 10
		}

		items := []VirtItem{
			{ID: "a", Height: h0, Label: "a"},
			{ID: "b", Height: h1, Label: "b"},
			{ID: "c", Height: h2, Label: "c"},
		}

		list := NewVirtualList("fuzz", nil)
		list.SetItems(items)
		list.SetBottomPadding(pad)

		total := h0 + h1 + h2
		if list.Scroll().Content() != total+pad {
			t.Fatalf("content=%d want %d", list.Scroll().Content(), total+pad)
		}

		// Every content row maps to exactly one item index.
		for row := 0; row < total; row++ {
			idx := list.ItemIndexAtRow(row)
			if idx < 0 || idx >= len(items) {
				t.Fatalf("row %d → idx=%d out of range", row, idx)
			}
			start := list.prefix[idx]
			end := list.prefix[idx+1]
			if row < start || row >= end {
				t.Fatalf("row %d mapped to item %d but prefix=[%d,%d)", row, idx, start, end)
			}
		}

		// Padding rows return -1.
		for row := total; row < total+pad; row++ {
			if got := list.ItemIndexAtRow(row); got != -1 {
				t.Fatalf("padding row %d → idx=%d want -1", row, got)
			}
		}

		// Fuzz query: clamp to extended range including OOB.
		if queryRow < -5 {
			queryRow = -5
		}
		if queryRow > total+pad+5 {
			queryRow = total + pad + 5
		}
		idx := list.ItemIndexAtRow(queryRow)
		if queryRow < 0 || queryRow >= total+pad {
			if idx != -1 {
				t.Fatalf("OOB row %d → idx=%d want -1", queryRow, idx)
			}
			return
		}
		if queryRow < total {
			if idx < 0 {
				t.Fatalf("in-range row %d → idx=%d", queryRow, idx)
			}
		}
	})
}
