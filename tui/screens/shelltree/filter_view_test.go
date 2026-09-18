package shelltree

import (
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/third_party/pathwalk"
	"crude/tui/cell"
	"crude/tui/filetree"
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

func TestAsyncSearchFilterArrowKeysNavigateProjection(t *testing.T) {
	model, paths := treeFixture(t)
	screen := New(theme.DefaultDark(), model)
	matches := []filetree.PathMatch{
		{Entry: pathwalk.Entry{Path: paths.main, Relative: filepath.ToSlash("main.go"), Name: "main.go"}},
		{Entry: pathwalk.Entry{Path: paths.util, Relative: filepath.ToSlash("pkg/util.go"), Name: "util.go"}},
	}
	screen.filter.field.SetValue("go")
	if !screen.ApplyPathSearch("go", matches) || screen.searchModel == nil {
		t.Fatal("async search projection was not installed")
	}
	host := ui.NewHost(theme.DefaultDark(), screen)
	host.Resize(40, 10)
	host.Layout()
	host.Focus(FilterID)
	before := screen.searchModel.CursorIndex()
	result := host.Handle(tea.KeyPressMsg{Code: tea.KeyUp})
	if !result.Handled || screen.searchModel.CursorIndex() != before-1 {
		t.Fatalf("Up in async search did not navigate: before=%d after=%d result=%#v", before, screen.searchModel.CursorIndex(), result)
	}
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
	searchGlyph := ui.SearchGlyphEnd(ui.NewTokenSet(theme.DefaultDark()))
	if searchRow := PlainRow(buf, 0, 40); !strings.Contains(searchRow, "u") || !strings.Contains(searchRow, searchGlyph) {
		t.Fatalf("header search should paint prompt and pattern, got %q", searchRow)
	}
	findRowContaining(t, buf, "util.go")
	for y := 0; y < buf.Height(); y++ {
		if strings.Contains(PlainRow(buf, y, 40), "main.go") {
			t.Fatal("main.go must be filtered out by pattern 'u'")
		}
	}
	_ = paths
}

func TestClickPersistentSearchOwnsTyping(t *testing.T) {
	model, _ := treeFixture(t)
	host := setupTreeHost(t, model)

	result := host.Handle(tea.MouseClickMsg{X: 5, Y: 0, Button: tea.MouseLeft})
	if !result.Handled || host.Focused() != FilterID {
		t.Fatalf("search click result=%#v focus=%q", result, host.Focused())
	}
	pressKey(host, tea.KeyPressMsg{Code: 'u', Text: "u"})
	if model.Filter() != "u" || !model.FilterActive() {
		t.Fatalf("filter=%q active=%v", model.Filter(), model.FilterActive())
	}
}

func TestFilterLeftRightPreservesCaretMovement(t *testing.T) {
	model, _ := treeFixture(t)
	host := setupTreeHost(t, model)
	pressKey(host, tea.KeyPressMsg{Code: 'm', Text: "m"})
	pressKey(host, tea.KeyPressMsg{Code: 'a', Text: "a"})
	pressKey(host, tea.KeyPressMsg{Code: 'i', Text: "i"})
	pressKey(host, tea.KeyPressMsg{Code: 'n', Text: "n"})
	screen := host.Root().(*Screen)
	if got := screen.filter.field.Edit().Cursor(); got != 4 {
		t.Fatalf("initial cursor=%d want 4", got)
	}
	host.Handle(tea.KeyPressMsg{Code: tea.KeyLeft})
	if got := screen.filter.field.Edit().Cursor(); got != 3 {
		t.Fatalf("Left cursor=%d want 3", got)
	}
	host.Handle(tea.KeyPressMsg{Code: tea.KeyRight})
	if got := screen.filter.field.Edit().Cursor(); got != 4 {
		t.Fatalf("Right cursor=%d want 4", got)
	}
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
