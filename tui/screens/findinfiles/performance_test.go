package findinfiles

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/workspace/fileio"
)

func TestResultNavigationKeepsListStructureAndUpdatesPreview(t *testing.T) {
	screen, host := groupedHost(t)
	items := screen.list.list.Items()
	source := screen.SyntaxEditor()
	host.Focus(ListID)
	host.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
	host.Layout()
	buffer := surface.NewCellBuffer(80, 24)
	host.Paint(buffer)
	if screen.Selected() != 1 || screen.SelectedRow() != 2 || !strings.Contains(bufferText(buffer), "needle second") {
		t.Fatal("navigation did not select and paint the next match")
	}
	if source != screen.SyntaxEditor() {
		t.Fatal("another hit in the same file replaced its full source")
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

// Measure the mounted source, not an empty list or a result snippet.
func BenchmarkWholeFilePreviewViewport(b *testing.B) {
	screen, host := searchHostForBench(b)
	defer screen.Close()
	source := strings.Repeat("needle and source bytes\n", 100000)
	hits := make([]fileio.ProjectSearchMatch, 1000)
	for i := range hits {
		hits[i] = fileio.ProjectSearchMatch{Path: "large.txt", Line: i*100 + 1, EndColumn: 6, LineText: "needle and source bytes"}
	}
	screen.ApplySearchResults(screen.Generation(), hits, nil, 0)
	mountPreview(b, screen, source)
	host.Layout()
	frame := surface.NewCellBuffer(120, 40)
	host.Paint(frame)
	b.ReportAllocs()
	for b.Loop() {
		host.Paint(frame)
	}
}
func searchHostForBench(b *testing.B) (*Screen, *ui.Host) {
	th := theme.DefaultDark()
	s := New(th)
	s.Open(".")
	s.SetQuery("needle")
	host := ui.NewHost(th, s)
	host.Resize(120, 40)
	host.Layout()
	return s, host
}
