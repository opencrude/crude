package shelltree

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

func paintHost(t *testing.T, host *ui.Host, w, h int) *surface.CellBuffer {
	t.Helper()
	host.Layout()
	buf := surface.NewCellBuffer(w, h)
	host.Paint(buf)
	return buf
}

func findRowContaining(t *testing.T, buf *surface.CellBuffer, needle string) int {
	t.Helper()
	for y := 0; y < buf.Height(); y++ {
		if strings.Contains(PlainRow(buf, y, buf.Width()), needle) {
			return y
		}
	}
	t.Fatalf("no painted row contains %q", needle)
	return -1
}

// TestTypeToFilterOpensFilterRowAtHostLevel proves a bare printable key
// starts the filter: the filter row paints the typed pattern and the tree
// narrows to fuzzy matches.
func TestTypeToFilterOpensFilterRowAtHostLevel(t *testing.T) {
	model, paths := treeFixture(t)
	host := setupTreeHost(t, model)

	pressKey(host, tea.KeyPressMsg{Code: 'u', Text: "u"})
	if !model.FilterActive() {
		t.Fatal("typing must activate the tree filter")
	}
	if model.Filter() != "u" {
		t.Fatalf("filter=%q want u", model.Filter())
	}

	buf := paintHost(t, host, 40, 10)
	if row0 := PlainRow(buf, 0, 40); !strings.Contains(row0, "u") || !strings.Contains(row0, "/") {
		t.Fatalf("filter row should paint prompt and pattern, got %q", row0)
	}
	findRowContaining(t, buf, "util.go")
	for y := 0; y < buf.Height(); y++ {
		if strings.Contains(PlainRow(buf, y, 40), "main.go") {
			t.Fatal("main.go must be filtered out by pattern 'u'")
		}
	}
	_ = paths
}

// TestTreeFilterHighlightsMatchedRunes verifies the fuzzy-matched runes of a
// row are painted with the theme accent foreground (and bold), while
// unmatched runes keep the regular file foreground.
func TestTreeFilterHighlightsMatchedRunes(t *testing.T) {
	model, _ := treeFixture(t)
	th := theme.DefaultDark()
	screen := New(th, model)
	host := ui.NewHost(th, screen)
	host.Resize(40, 10)
	host.Layout()
	host.Focus(TreeID)

	pressKey(host, tea.KeyPressMsg{Code: 'm', Text: "m"})
	pressKey(host, tea.KeyPressMsg{Code: 'n', Text: "n"})
	if model.Filter() != "mn" {
		t.Fatalf("filter=%q want mn", model.Filter())
	}

	buf := paintHost(t, host, 40, 10)
	y := findRowContaining(t, buf, "main.go")
	accent := ui.NewTokenSet(th).Color(ui.ColorAccent)

	nameX := -1
	for x := 0; x < buf.Width(); x++ {
		if buf.Get(x, y).Char == 'm' && buf.Get(x+1, y).Char == 'a' && buf.Get(x+2, y).Char == 'i' {
			nameX = x
			break
		}
	}
	if nameX < 0 {
		t.Fatalf("main.go not found in row %d: %q", y, PlainRow(buf, y, 40))
	}

	mCell := buf.Get(nameX, y)
	if !mCell.FG.Equal(accent) || !mCell.Attr.Has(cell.AttrBold) {
		t.Fatalf("matched 'm' should use accent+bold, got fg=%v attr=%v", mCell.FG, mCell.Attr)
	}
	nCell := buf.Get(nameX+3, y)
	if !nCell.FG.Equal(accent) {
		t.Fatalf("matched 'n' should use accent, got fg=%v", nCell.FG)
	}
	aCell := buf.Get(nameX+1, y)
	if aCell.FG.Equal(accent) {
		t.Fatal("unmatched 'a' must not use the accent foreground")
	}
}

// TestTreeFilterNoMatchesPaintsEmptyState verifies the sidebar shows a
// "No matches" hint instead of a blank pane when the filter excludes
// everything.
func TestTreeFilterNoMatchesPaintsEmptyState(t *testing.T) {
	model, _ := treeFixture(t)
	host := setupTreeHost(t, model)

	for _, r := range []rune{'z', 'q', 'x'} {
		pressKey(host, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	if n := len(model.VisibleNodes()); n != 0 {
		t.Fatalf("filter should match nothing, visible=%d", n)
	}

	buf := paintHost(t, host, 40, 10)
	findRowContaining(t, buf, "No matches")

	pressKey(host, tea.KeyPressMsg{Code: tea.KeyEsc})
	buf = paintHost(t, host, 40, 10)
	for y := 0; y < buf.Height(); y++ {
		if strings.Contains(PlainRow(buf, y, 40), "No matches") {
			t.Fatal("empty state must disappear when the filter clears")
		}
	}
	findRowContaining(t, buf, "main.go")
}
