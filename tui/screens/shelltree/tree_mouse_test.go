package shelltree_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/filetree"
	"crude/tui/rendering"
	"crude/tui/screens/shelltree"
	"crude/tui/surface"
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

// nestedScrollFixture puts the files in a subdirectory so a sticky breadcrumb
// still has an ancestor to name after the workspace root row is omitted.
func nestedScrollFixture(t *testing.T, names ...string) *filetree.FileTreeModel {
	t.Helper()
	dir := t.TempDir()
	nested := filepath.Join(dir, "nested")
	if err := os.Mkdir(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(nested, n), []byte("package p\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	model := filetree.NewFileTreeModel(dir)
	model.Expand(nested)
	return model
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
// row, with and without a non-empty filter. Icon and search share row 0:
// tree items start at row 1.
func TestTreeMouseClickRowMapping(t *testing.T) {
	names := []string{"a.go", "b.go", "c.go", "d.go", "e.go"}
	cases := []struct {
		name    string
		filter  bool
		screenY int
	}{
		{"no_filter_row0", false, 1},
		{"no_filter_row1", false, 2},
		{"no_filter_row2", false, 3},
		{"no_filter_row3", false, 4},
		{"no_filter_row4", false, 5},
		{"filter_row0_first_tree_row", true, 1},
		{"filter_row1", true, 2},
		{"filter_row2", true, 3},
		{"filter_row3", true, 4},
		{"filter_row4", true, 5},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			model := flatFileFixture(t, names...)
			if tc.filter {
				model.EnterFilterMode()
			}
			host := treeMouseHost(t, model)

			nodes := model.VisibleNodes()
			if len(nodes) < len(names) {
				t.Fatalf("need %d visible nodes, got %d", len(names), len(nodes))
			}

			// The painted row a user sees at screenY, below icon + search.
			wantIdx := tc.screenY - 1
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

func TestPathSearchCanDeleteLastCharacterFromBothEntryPaths(t *testing.T) {
	for _, startInTree := range []bool{true, false} {
		name := "clicked filter"
		if startInTree {
			name = "type-to-filter"
		}
		t.Run(name, func(t *testing.T) {
			model := flatFileFixture(t, "alpha.go", "beta.go")
			screen := shelltree.New(theme.DefaultDark(), model)
			var queries []string
			screen.SetPathSearchHandler(func(query string) tea.Cmd {
				queries = append(queries, query)
				return nil
			})
			host := ui.NewHost(theme.DefaultDark(), screen)
			host.Resize(40, 10)
			host.Layout()
			if startInTree {
				host.Focus(shelltree.TreeID)
			} else {
				host.Handle(tea.MouseClickMsg{X: 9, Y: 0, Button: tea.MouseLeft})
			}
			host.Handle(tea.KeyPressMsg{Code: 'a', Text: "a"})
			if len(queries) == 0 || queries[0] != "a" || host.Focused() != shelltree.FilterID {
				t.Fatalf("first character missed the authoritative search field: queries=%q focus=%q", queries, host.Focused())
			}
			host.Handle(tea.KeyPressMsg{Code: tea.KeyBackspace})
			if len(queries) == 0 || queries[len(queries)-1] != "" || model.Filter() != "" {
				t.Fatalf("last character reappeared or async clear was skipped: queries=%q model=%q", queries, model.Filter())
			}
			if got := shelltree.PlainRow(screen.PaintTestBuffer(40, 10), 0, 40); strings.Contains(got, "a⌕") {
				t.Fatalf("painted filter retained deleted character: %q", got)
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
		if len(nodes) != len(names) {
			t.Fatalf("need %d visible nodes, got %d", len(names), len(nodes))
		}
		model.SetCursorIndex(len(nodes) - 1)
		host.Layout()
		host.Handle(tea.MouseClickMsg{X: 2, Y: screenY, Button: tea.MouseLeft})
		return model, cursorBase(model)
	}

	// Sidebar slot starts at screen Y=1: icon+search paints at Y=1 and the
	// first tree row at Y=2, the second at Y=3.
	for _, tc := range []struct {
		screenY int
		wantIdx int
	}{
		{2, 0},
		{3, 1},
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
	// Icon+search occupy slot row 0; the tree view starts at slot row 1.
	if treeRect.Y != 2 {
		t.Fatalf("tree view screen rect Y=%d, want 2 (slot.Y=1 + treeY=1)", treeRect.Y)
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
	model := nestedScrollFixture(t, names...)
	model.SetScrollOffset(3)
	host := treeMouseHost(t, model)

	if model.ScrollOffset() != 3 {
		t.Fatalf("precondition: scroll offset 3, got %d", model.ScrollOffset())
	}
	if crumb := filetree.BreadcrumbLine(model, 80); crumb == "" || strings.Contains(crumb, filepath.Base(model.RootPath())) {
		t.Fatalf("breadcrumb %q should name the nested folder, not the workspace root", crumb)
	}
	nodes := model.VisibleNodes()
	want := filepath.Base(nodes[3].Path)

	model.SetCursorIndex(len(nodes) - 1)
	host.Layout()

	// Breadcrumb paints at tree row 0. Icon+search occupy row 0, so the
	// first item paints at screen row 2.
	host.Handle(tea.MouseClickMsg{X: 2, Y: 2, Button: tea.MouseLeft})

	if got := cursorBase(model); got != want {
		t.Errorf("breadcrumb active: click at screenY=2 selected %q, want %q (visible[3])", got, want)
	}
}

// TestTreeMouseClickBreadcrumbAndFilter verifies the combined chrome case:
// both the persistent search row and the breadcrumb are present. The first
// item sits below icon+search + breadcrumb at screen row 2.
func TestTreeMouseClickBreadcrumbAndFilter(t *testing.T) {
	names := make([]string, 0, 24)
	for i := 0; i < 24; i++ {
		names = append(names, "file"+string(rune('a'+i))+".go")
	}
	model := nestedScrollFixture(t, names...)
	model.EnterFilterMode()
	model.SetScrollOffset(3)
	host := treeMouseHost(t, model)

	if model.ScrollOffset() != 3 {
		t.Fatalf("precondition: scroll offset 3, got %d", model.ScrollOffset())
	}
	if crumb := filetree.BreadcrumbLine(model, 80); crumb == "" || strings.Contains(crumb, filepath.Base(model.RootPath())) {
		t.Fatalf("breadcrumb %q should name the nested folder, not the workspace root", crumb)
	}
	nodes := model.VisibleNodes()
	want := filepath.Base(nodes[3].Path)

	model.SetCursorIndex(len(nodes) - 1)
	host.Layout()

	// Icon+search at row 0, breadcrumb at row 1, first item at row 2.
	host.Handle(tea.MouseClickMsg{X: 2, Y: 2, Button: tea.MouseLeft})

	if got := cursorBase(model); got != want {
		t.Errorf("filter+breadcrumb active: click at screenY=2 selected %q, want %q (visible[3])", got, want)
	}
}

func TestTreeBreadcrumbRevealsClickedAncestor(t *testing.T) {
	for _, target := range []string{"parent", "child"} {
		root := t.TempDir()
		parent := filepath.Join(root, "parent")
		child := filepath.Join(parent, "child")
		if err := os.MkdirAll(child, 0o755); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 24; i++ {
			if err := os.WriteFile(filepath.Join(child, "file"+string(rune('a'+i))+".go"), nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		model := filetree.NewFileTreeModel(root)
		model.Expand(parent)
		model.Expand(child)
		model.SetScrollOffset(5)
		host := treeMouseHost(t, model)
		buf := surface.NewCellBuffer(40, 10)
		host.Paint(buf)
		r, ok := host.ScreenRect(shelltree.TreeID)
		if !ok {
			t.Fatal("missing mounted tree")
		}
		x := -1
		for column := r.X; column+len(target) <= r.X+r.W; column++ {
			var label strings.Builder
			for index := range len(target) {
				label.WriteRune(buf.Get(column+index, r.Y).Char)
			}
			if label.String() == target {
				x = column
				break
			}
		}
		if x < 0 {
			t.Fatalf("ancestor %s is not painted in the breadcrumb", target)
		}
		host.Handle(tea.MouseClickMsg{X: x, Y: r.Y, Button: tea.MouseLeft})
		if got := cursorBase(model); got != target || model.ScrollOffset() >= 5 {
			t.Fatalf("breadcrumb did not select/reveal %s: selection=%s offset=%d", target, got, model.ScrollOffset())
		}
		host.Paint(buf)
		rowY := r.Y
		if filetree.BreadcrumbLine(model, r.W) != "" {
			rowY++
		}
		var row strings.Builder
		for column := r.X; column < r.X+r.W; column++ {
			row.WriteRune(buf.Get(column, rowY).Char)
		}
		if !strings.Contains(row.String(), target) {
			t.Fatalf("clicked ancestor was not revealed in the first painted tree row: %q", row.String())
		}
	}
}

func TestScrolledTreeRebasesOnlySharedOffscreenAncestry(t *testing.T) {
	root := t.TempDir()
	deep := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		if err := os.WriteFile(filepath.Join(deep, "file"+string(rune('a'+i))+".go"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "z.go"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	model := filetree.NewFileTreeModel(root)
	for _, path := range []string{"a", "a/b", "a/b/c"} {
		model.Expand(filepath.Join(root, path))
	}
	model.Update(filetree.TreeHeightMsg{Height: 6})
	model.SetScrollOffset(3) // Only c's children fill the viewport.
	screen := shelltree.New(theme.DefaultDark(), model)
	host := ui.NewHost(theme.DefaultDark(), screen)
	host.Resize(37, 7)
	host.Layout()
	if crumb := screen.TreeControl().Breadcrumb(); !strings.Contains(crumb, "c") {
		t.Fatalf("viewport failed to publish its shared breadcrumb: %q", crumb)
	}
	paint := func() string {
		buf := surface.NewCellBuffer(37, 7)
		host.Paint(buf)
		var text strings.Builder
		for y := 1; y < 7; y++ {
			// Scrollbar/thumb scheduling is independent of tree-row geometry.
			text.WriteString(shelltree.PlainRow(buf, y, 30))
			text.WriteByte('\n')
		}
		return text.String()
	}
	host.Focus(shelltree.TreeID)
	immediate := paint()
	first := strings.Split(immediate, "\n")[1]
	idx := strings.Index(first, "filea.go")
	if idx < 0 || rendering.VisibleWidth(first[:idx]) > 12 {
		t.Fatalf("offscreen ancestors still consumed the first file's label space: %q", immediate)
	}
	compactX := rendering.VisibleWidth(first[:idx])
	host.MarkLayout()
	host.Layout()
	if settled := paint(); settled != immediate {
		t.Fatalf("immediate and settled tree geometry diverged:\n%s\n%s", immediate, settled)
	}
	host.Focus(shelltree.FilterID)
	host.MarkLayout()
	host.Layout()
	if next := paint(); next != immediate {
		t.Fatalf("focus changed the shared breadcrumb or row indentation:\n%s\n%s", immediate, next)
	}
	model.SetScrollOffset(len(model.VisibleNodes()) - 6) // Includes root-level z.go.
	host.MarkLayout()
	host.Layout()
	if crumb := screen.TreeControl().Breadcrumb(); !strings.Contains(crumb, "a") || !strings.Contains(crumb, "c") {
		t.Fatalf("normal breadcrumb disappeared at the mixed-branch boundary: %q", crumb)
	}
	normal := strings.Split(paint(), "\n")[1]
	if index := strings.Index(normal, "file"); index < 0 || rendering.VisibleWidth(normal[:index]) <= compactX {
		t.Fatalf("mixed-branch mode did not restore absolute guide columns: %q", normal)
	}
	buf := surface.NewCellBuffer(37, 7)
	host.Paint(buf)
	crumbRect, ok := host.ScreenRect(shelltree.TreeID)
	if !ok {
		t.Fatal("missing tree rect for normal breadcrumb hit test")
	}
	parentX := -1
	for x := crumbRect.X; x < crumbRect.X+crumbRect.W; x++ {
		if buf.Get(x, crumbRect.Y).Char == 'c' {
			parentX = x
		}
	}
	if parentX < 0 {
		t.Fatal("normal breadcrumb did not paint the nearest parent")
	}
	host.Handle(tea.MouseClickMsg{X: parentX, Y: crumbRect.Y, Button: tea.MouseLeft})
	if selected := model.SelectedNode(); selected == nil || selected.Path != deep {
		t.Fatalf("normal breadcrumb lost its parent click target: %+v", selected)
	}
}
