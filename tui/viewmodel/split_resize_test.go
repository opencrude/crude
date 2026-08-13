package viewmodel

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/filetree"
	"crude/tui/screens/shelltree"
)

func TestSidebarSplitDividerDragResizesTree(t *testing.T) {
	app := newFullTestApp(t)
	model := filetree.NewFileTreeModel(t.TempDir())
	app.Tree = NewTreeState(model, 30)
	app.Tree.Visible = true
	app.sidebarScreen.SetModel(model)
	app.paintShellFrame()

	start := app.Tree.Width
	if start != 30 {
		t.Fatalf("start width=%d", start)
	}
	divX := app.activityChromeWidth() + start
	if id := app.rootHost.HitTest(divX, 8); id != shelltree.DividerID {
		t.Fatalf("HitTest(%d,8)=%q want %q", divX, id, shelltree.DividerID)
	}

	_, _ = app.dispatchMouseClick(tea.MouseClickMsg{X: divX, Y: 8, Button: tea.MouseLeft})
	if !app.rootHost.HasPointerCapture() {
		t.Fatal("divider click should capture the pointer")
	}
	_, _ = app.dispatchMouseMotion(tea.MouseMotionMsg{X: divX + 8, Y: 8, Button: tea.MouseLeft})
	if app.Tree.Width != start+8 {
		t.Fatalf("after +8 drag width=%d want %d", app.Tree.Width, start+8)
	}
	_, _ = app.dispatchMouseRelease(tea.MouseReleaseMsg{X: divX + 8, Y: 8, Button: tea.MouseLeft})
	if app.rootHost.HasPointerCapture() {
		t.Fatal("release should clear capture")
	}
	if app.Tree.Width != start+8 {
		t.Fatalf("width after release=%d want %d", app.Tree.Width, start+8)
	}
}

func TestSidebarSplitDividerDragClampsToHalfWidth(t *testing.T) {
	app := newFullTestApp(t)
	model := filetree.NewFileTreeModel(t.TempDir())
	app.Tree = NewTreeState(model, 30)
	app.Tree.Visible = true
	app.sidebarScreen.SetModel(model)
	app.paintShellFrame()

	divX := app.activityChromeWidth() + app.Tree.Width
	_, _ = app.dispatchMouseClick(tea.MouseClickMsg{X: divX, Y: 8, Button: tea.MouseLeft})
	_, _ = app.dispatchMouseMotion(tea.MouseMotionMsg{X: 200, Y: 8, Button: tea.MouseLeft})
	want := app.width / 2
	if app.Tree.Width != want {
		t.Fatalf("clamped width=%d want %d", app.Tree.Width, want)
	}
}
