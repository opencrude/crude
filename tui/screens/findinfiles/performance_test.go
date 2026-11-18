package findinfiles

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/theme"
	"crude/workspace/fileio"
)

func TestResultNavigationKeepsListStructureAndUpdatesPreview(t *testing.T) {
	screen, host := groupedHost(t)
	items := screen.list.list.Items()
	host.Focus(ListID)
	host.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
	host.Layout()
	buffer := surface.NewCellBuffer(80, 24)
	host.Paint(buffer)
	if screen.Selected() != 1 || screen.SelectedRow() != 2 || !strings.Contains(bufferText(buffer), "needle second") {
		t.Fatal("navigation did not select and paint the next match")
	}
	if next := screen.list.list.Items(); len(next) != len(items) || &next[0] != &items[0] {
		t.Fatal("moving the selection rebuilt unchanged result rows")
	}
}

func denseSearchResults(n int) []fileio.ProjectSearchMatch {
	results := make([]fileio.ProjectSearchMatch, n)
	for i := range results {
		results[i] = fileio.ProjectSearchMatch{
			Path: fmt.Sprintf("/project/source%04d.go", i/10), RelativePath: fmt.Sprintf("source%04d.go", i/10),
			Line: i%10 + 1, EndLine: i%10 + 1, Column: 0, EndColumn: 6, LineText: "needle",
			ContextStart: 1, Context: strings.Repeat("needle\n", 10),
		}
	}
	return results
}

func BenchmarkProjectSearchSelection(b *testing.B) {
	for _, count := range []int{10, 1000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			screen := New(theme.DefaultDark())
			screen.Open("/project")
			screen.ApplySearchResults(screen.Generation(), denseSearchResults(count), nil, 0)
			b.Cleanup(screen.Close)
			b.ReportAllocs()
			row := 1
			for b.Loop() {
				row++
				if row >= len(screen.rows) {
					row = 1
				}
				screen.selectRow(row)
				if screen.SelectedRow() != row {
					b.Fatal("selection did not move")
				}
			}
		})
	}
}
