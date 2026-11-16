//go:build cgo

package terminalvt

import (
	"fmt"
	"strings"
	"testing"
)

// A snapshot fed into a new engine reproduces the rows, scrollback and
// colors of the original screen.
func TestSnapshotRestoresScrollbackAndStyles(t *testing.T) {
	source, err := New(Options{Cols: 40, Rows: 5, ScrollbackLines: 1000})
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	var out strings.Builder
	for i := range 30 {
		fmt.Fprintf(&out, "line-%02d\r\n", i)
	}
	out.WriteString("\x1b[31mred\x1b[0m tail")
	source.Feed([]byte(out.String()))
	snapshot, err := source.Snapshot()
	if err != nil || len(snapshot) == 0 {
		t.Fatal("snapshot failed", err)
	}
	restored, err := New(Options{Cols: 40, Rows: 5, ScrollbackLines: 1000})
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	restored.Feed(snapshot)
	page, err := restored.Rows(RowRequest{Start: 0, Count: MaxSnapshotRows})
	if err != nil {
		t.Fatal(err)
	}
	var text []string
	red := false
	for _, row := range page.Cells {
		var b strings.Builder
		for _, c := range row {
			b.WriteString(c.Text)
			if c.Text == "r" && c.FG.Explicit {
				red = true
			}
		}
		text = append(text, strings.TrimRight(b.String(), " "))
	}
	joined := strings.Join(text, "\n")
	for _, want := range []string{"line-00", "line-17", "line-29", "red tail"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("restored buffer lacks %q:\n%s", want, joined)
		}
	}
	if !red {
		t.Fatal("restored buffer lost its colors")
	}
}
