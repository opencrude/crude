package editor

import (
	"strings"
	"testing"
)

func TestSourceRowsKeepGraphemesWhitespaceAndZeroHeightLines(t *testing.T) {
	for _, tc := range []struct {
		text       string
		tab, width int
		starts     []int
	}{
		{"abcdefghijklmno", 4, 9, []int{0, 9}},
		{"hello world", 4, 8, []int{0, 6}},
		{"abc\t界éXYZq", 4, 5, []int{0, 4, 9}},
		{"界界界界界界", 4, 5, []int{0, 4, 8}},
		{"\t\t\t", 4, 5, []int{0, 5, 10}},
		{"", 4, 5, []int{0}},
	} {
		m := measureRowMetrics(tc.text, tc.tab)
		line := wrapRowMetrics(m, tc.width, nil)
		if line.count != len(tc.starts) {
			t.Fatalf("%q count=%d want %d", tc.text, line.count, len(tc.starts))
		}
		for i, start := range tc.starts {
			if line.start(i) != start || line.atColumn(start) != i {
				t.Fatalf("%q row %d start=%d inverse=%d", tc.text, i, line.start(i), line.atColumn(start))
			}
		}
		if line.end(line.count-1) != m.width {
			t.Fatal("source tail omitted")
		}
	}
	var rows sourceRows
	rows.reset(4)
	rows.set(0, rowLine{count: 2})
	rows.set(1, rowLine{})
	rows.set(2, rowLine{count: 3})
	rows.set(3, rowLine{count: 1})
	for row, want := range [][2]int{{0, 0}, {0, 1}, {2, 0}, {2, 1}, {2, 2}, {3, 0}} {
		line, local := rows.at(row)
		if line != want[0] || local != want[1] {
			t.Fatal("hidden/wrapped row disagrees", row, line, local)
		}
	}
	rows.set(0, rowLine{count: 1})
	if rows.total() != 5 || rows.before(3) != 4 {
		t.Fatal("prefix retained changed line height")
	}
}

func TestSourceRowsCompressLongNarrowAndWideRuns(t *testing.T) {
	narrow := wrapRowMetrics(measureRowMetrics(strings.Repeat("x", 2<<20), 4), 1, nil)
	if len(narrow.runs) != 0 || narrow.count != 2<<20 {
		t.Fatal("one allocation per narrow row", len(narrow.runs), narrow.count)
	}
	wide := wrapRowMetrics(measureRowMetrics(strings.Repeat("界", 10000), 4), 5, nil)
	if len(wide.runs) > 2 || wide.count != 5000 {
		t.Fatal("repeated wide rows not compressed", len(wide.runs), wide.count)
	}
}
