package shelltree_test

import (
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/filetree"
	"crude/tui/screens/shelltree"
	"crude/tui/theme"
	"crude/tui/ui"
)

// flatFileFixture builds a workspace with the given file names directly under
// the root (no nested directories), so clicking a row never toggles directory
// expansion and the visible-node list stays stable across clicks.
func flatFileFixture(t *testing.T, names ...string) *filetree.FileTreeModel {
	t.Helper()
	dir := t.TempDir()
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("package p\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return filetree.NewFileTreeModel(dir)
}

// treeMouseHost mounts the shelltree screen as the Host root, mirroring
// setupTreeHost in tree_contract_test.go.
func treeMouseHost(t *testing.T, model *filetree.FileTreeModel) *ui.Host {
	t.Helper()
	screen := shelltree.New(theme.DefaultDark(), model)
	host := ui.NewHost(theme.DefaultDark(), screen)
	host.Resize(40, 10)
	host.Layout()
	host.Focus(shelltree.TreeID)
	return host
}

func cursorBase(model *filetree.FileTreeModel) string {
	idx := model.CursorIndex()
	nodes := model.VisibleNodes()
	if idx < 0 || idx >= len(nodes) {
		return ""
	}
	return filepath.Base(nodes[idx].Path)
}

// TestTreeMouseClickRowMapping pins the click->row mapping for every painted
// row, with and without a non-empty filter. Title and search are persistent:
// screen rows 0/1 are chrome and tree items start at row 2.
func TestTreeMouseClickRowMapping(t *testing.T) {
	names := []string{"a.go", "b.go", "c.go", "d.go", "e.go"}
	cases := []struct {
		name    string
		filter  bool
		screenY int
	}{
		{"no_filter_row0", false, 2},
		{"no_filter_row1", false, 3},
		{"no_filter_row2", false, 4},
		{"no_filter_row3", false, 5},
		{"no_filter_row4", false, 6},
		{"filter_row0_first_tree_row", true, 2},
		{"filter_row1", true, 3},
		{"filter_row2", true, 4},
		{"filter_row3", true, 5},
		{"filter_row4", true, 6},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			model := flatFileFixture(t, names...)
			if tc.filter {
				model.EnterFilterMode()
			}
			host := treeMouseHost(t, model)

			nodes := model.VisibleNodes()
			if len(nodes) < len(names)+1 {
				t.Fatalf("need %d visible nodes, got %d", len(names)+1, len(nodes))
			}

			// The painted row a user sees at screenY, below title + search.
			wantIdx := tc.screenY - 2
			if wantIdx < 0 || wantIdx >= len(nodes) {
				t.Fatalf("wantIdx %d out of range", wantIdx)
			}
			want := filepath.Base(nodes[wantIdx].Path)

			// Start selection at the last node so a swallowed click (one that
			// never reaches the model) is detectable.
			model.SetCursorIndex(len(nodes) - 1)
			host.Layout()

			host.Handle(tea.MouseClickMsg{X: 2, Y: tc.screenY, Button: tea.MouseLeft})

			if got := cursorBase(model); got != want {
				t.Errorf("click at screenY=%d (filter=%v): selected %q, want %q (visible[%d])",
					tc.screenY, tc.filter, got, want, wantIdx)
			}
		})
	}
}

// slotWrapper places a child screen at a slot rect, mirroring how
// shellRoot.Layout drives the shelltree Screen's slot rect in the real shell
// (configureShell: shellSidebarSlot starts at shell row 1).
type slotWrapper struct {
	ui.Base
	child ui.Component
	slot  ui.Rect
}

func (w *slotWrapper) Children() []ui.Component { return []ui.Component{w.child} }

func (w *slotWrapper) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
	return cs.ClampSize(ui.Size{W: cs.MaxW, H: cs.MaxH})
}

func (w *slotWrapper) Layout(_ ui.LayoutContext, r ui.Rect) {
	w.SetBounds(r)
	w.child.Layout(ui.LayoutContext{}, w.slot)
}

// TestTreeMouseClickRealShellGeometry mounts the shelltree Screen at the
// sidebar slot rect the real shell uses ({0, 1, W, H}) and clicks with
// absolute screen coordinates, proving the row mapping holds end-to-end. Each
// assertion uses a fresh model/host because clicking the root directory
// toggles its expansion and changes the visible list.
func TestTreeMouseClickRealShellGeometry(t *testing.T) {
	names := []string{"a.go", "b.go", "c.go", "d.go"}
	clickAt := func(screenY int) (*filetree.FileTreeModel, string) {
		model := flatFileFixture(t, names...)
		model.EnterFilterMode()
		screen := shelltree.New(theme.DefaultDark(), model)
		th := theme.DefaultDark()
		host := ui.NewHost(th, &slotWrapper{Base: ui.NewBase("slot"), child: screen, slot: ui.Rect{X: 0, Y: 1, W: 30, H: 21}})
		host.Resize(80, 24)
		host.Layout()
		host.Focus(shelltree.TreeID)
		nodes := model.VisibleNodes()
		if len(nodes) != len(names)+1 {
			t.Fatalf("need %d visible nodes, got %d", len(names)+1, len(nodes))
		}
		model.SetCursorIndex(len(nodes) - 1)
		host.Layout()
		host.Handle(tea.MouseClickMsg{X: 2, Y: screenY, Button: tea.MouseLeft})
		return model, cursorBase(model)
	}

	// Sidebar slot starts at screen Y=1: title/search paint at Y=1/2 and the
	// first tree row at Y=3, the second at Y=4. Expected rows come from the same
	// fixture that received the click (temp dir names are random per fixture).
	for _, tc := range []struct {
		screenY int
		wantIdx int
	}{
		{3, 0},
		{4, 1},
	} {
		model, got := clickAt(tc.screenY)
		want := filepath.Base(model.VisibleNodes()[tc.wantIdx].Path)
		if got != want {
			t.Errorf("click tree row at screenY=%d: selected %q, want %q", tc.screenY, got, want)
		}
	}
}

// TestTreeViewScreenRect_BelowFilterRow grounds the LocalY origin claim: the
// Host dispatches a click over the tree to the tree view (TreeID) with LocalY
// relative to the TREE VIEW's screen rect, which starts below the filter row.
func TestTreeViewScreenRect_BelowFilterRow(t *testing.T) {
	model := flatFileFixture(t, "a.go")
	model.EnterFilterMode()
	screen := shelltree.New(theme.DefaultDark(), model)
	th := theme.DefaultDark()
	host := ui.NewHost(th, &slotWrapper{Base: ui.NewBase("slot"), child: screen, slot: ui.Rect{X: 0, Y: 1, W: 30, H: 21}})
	host.Resize(80, 24)
	host.Layout()

	treeRect, ok := host.ScreenRect(shelltree.TreeID)
	if !ok {
		t.Fatal("tree view not mounted")
	}
	// Title/search occupy slot rows 0/1; the tree view starts at slot row 2.
	if treeRect.Y != 3 {
		t.Fatalf("tree view screen rect Y=%d, want 3 (slot.Y=1 + treeY=2)", treeRect.Y)
	}
	if treeRect.X != 1 || treeRect.W != 26 {
		t.Fatalf("tree view screen rect X/W=%d/%d, want 1/26 (left inset + sidebar content)", treeRect.X, treeRect.W)
	}
}

// TestTreeMouseClickBreadcrumbOffset verifies the mapping when a breadcrumb
// header row is active (scroll offset > 0). The breadcrumb is painted INSIDE
// the ui.Tree's own bounds as its first row, so a click on the row below it
// must select the first item.
func TestTreeMouseClickBreadcrumbOffset(t *testing.T) {
	names := make([]string, 0, 24)
	for i := 0; i < 24; i++ {
		names = append(names, "file"+string(rune('a'+i))+".go")
	}
	model := flatFileFixture(t, names...)
	model.SetScrollOffset(3)
	host := treeMouseHost(t, model)

	if model.ScrollOffset() != 3 {
		t.Fatalf("precondition: scroll offset 3, got %d", model.ScrollOffset())
	}
	nodes := model.VisibleNodes()
	want := filepath.Base(nodes[3].Path)

	model.SetCursorIndex(len(nodes) - 1)
	host.Layout()

	// Breadcrumb paints at tree row 0. Title/search occupy rows 0/1, so the
	// first item paints at screen row 3.
	host.Handle(tea.MouseClickMsg{X: 2, Y: 3, Button: tea.MouseLeft})

	if got := cursorBase(model); got != want {
		t.Errorf("breadcrumb active: click at screenY=3 selected %q, want %q (visible[3])", got, want)
	}
}

// TestTreeMouseClickBreadcrumbAndFilter verifies the combined chrome case:
// both the persistent search row and the breadcrumb are present. The first
// item sits below title + search + breadcrumb at screen row 3.
func TestTreeMouseClickBreadcrumbAndFilter(t *testing.T) {
	names := make([]string, 0, 24)
	for i := 0; i < 24; i++ {
		names = append(names, "file"+string(rune('a'+i))+".go")
	}
	model := flatFileFixture(t, names...)
	model.EnterFilterMode()
	model.SetScrollOffset(3)
	host := treeMouseHost(t, model)

	if model.ScrollOffset() != 3 {
		t.Fatalf("precondition: scroll offset 3, got %d", model.ScrollOffset())
	}
	nodes := model.VisibleNodes()
	want := filepath.Base(nodes[3].Path)

	model.SetCursorIndex(len(nodes) - 1)
	host.Layout()

	// Title/search at rows 0/1, breadcrumb at row 2, first item at row 3.
	host.Handle(tea.MouseClickMsg{X: 2, Y: 3, Button: tea.MouseLeft})

	if got := cursorBase(model); got != want {
		t.Errorf("filter+breadcrumb active: click at screenY=3 selected %q, want %q (visible[3])", got, want)
	}
}
