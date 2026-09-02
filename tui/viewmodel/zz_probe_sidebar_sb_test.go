package viewmodel

import (
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/filetree"
	"crude/tui/screens/shelltree"
)

// TestZZProbeSidebarScrollbarDrag drives a real drag on the sidebar tree
// scrollbar through the root host and verifies the tree content scrolls.
func TestZZProbeSidebarScrollbarDrag(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 100, 30
	app.renderShellToSurface()

	sb := app.sidebarScreen
	if sb == nil {
		t.Fatal("sidebar screen missing")
	}
	// Build a deep tree so the sidebar overflows.
	base := t.TempDir()
	for i := 0; i < 40; i++ {
		if err := os.MkdirAll(filepath.Join(base, "d"+string(rune('a'+i%26))+string(rune('a'+i/26))), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	model := filetree.NewFileTreeModel(base)
	app.Tree = NewTreeState(model, 20)
	app.sidebarScreen.SetModel(model)
	tree := sb.TreeControl()
	if tree == nil {
		t.Fatal("tree control missing")
	}
	app.paintShellFrame()
	scroll := tree.Scroll()
	if scroll.MaxOffset() <= 0 {
		t.Skipf("tree does not overflow (content=%d viewport=%d)", scroll.Content(), scroll.Viewport())
	}

	bar, ok := app.rootHost.ScreenRect(shelltree.ScrollbarID)
	if !ok {
		t.Fatal("sidebar scrollbar rect unknown on root host")
	}
	cx := bar.X + bar.W/2
	top, _ := scroll.ThumbBounds(bar.H)
	cy0 := bar.Y + top

	app.renderShellToSurface()
	before := app.Tree.Model.ScrollOffset()

	app.rootHost.Handle(tea.MouseClickMsg{X: cx, Y: cy0, Button: tea.MouseLeft})
	owner, _, captured := app.rootHost.CapturedPointer()
	if !captured || owner != shelltree.ScrollbarID {
		t.Fatalf("sidebar scrollbar capture owner=%q captured=%v", owner, captured)
	}
	dragY := min(cy0+10, bar.Y+bar.H-1)
	app.rootHost.Handle(tea.MouseMotionMsg{X: cx + 10, Y: dragY, Button: tea.MouseLeft})
	app.rootHost.Handle(tea.MouseReleaseMsg{X: cx + 10, Y: dragY, Button: tea.MouseLeft})

	t.Logf("scroll.Offset=%d before modelOffset=%d after modelOffset=%d",
		scroll.Offset(), before, app.Tree.Model.ScrollOffset())
	if scroll.Offset() == 0 {
		t.Fatal("drag did not move tree ScrollModel")
	}
	if app.Tree.Model.ScrollOffset() == before {
		t.Fatal("ScrollModel moved but FileTreeModel offset did not follow — scrollbar->content direction broken")
	}
}
