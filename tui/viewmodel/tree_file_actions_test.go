package viewmodel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/screens/shelltree"
)

func treeActionsApp(t *testing.T) (*App, string) {
	t.Helper()
	app, _ := newRealShellApp(t)
	root := app.ws.RootPath()
	for path, data := range map[string]string{
		"manual/guide.txt": "guide",
		"src/main.go":      "package main\n",
		"notes.txt":        "notes",
		"target/inner.txt": "inner",
	} {
		full := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	_, _ = app.Update(tea.WindowSizeMsg{Width: 120, Height: 34})
	app.setActivity(activityFiles, true)
	runAppCommand(t, app, app.Tree.Model.RefreshFromDisk())
	_ = app.View()
	return app, root
}

func treeDrag(t *testing.T, app *App, from string, toX, toY int) {
	t.Helper()
	x, y := editorTreePoint(t, app, from)
	_, _ = app.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	_, _ = app.Update(tea.MouseMotionMsg{X: x, Y: y + 1, Button: tea.MouseLeft})
	_, _ = app.Update(tea.MouseMotionMsg{X: toX, Y: toY, Button: tea.MouseLeft})
	_ = app.View()
}

func treeRelease(t *testing.T, app *App, x, y int) {
	t.Helper()
	_, cmd := app.Update(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft})
	runAppCommand(t, app, cmd)
	_ = app.View()
}

func treeKey(t *testing.T, app *App, key tea.KeyPressMsg) {
	t.Helper()
	app.setFocusTree()
	_, cmd := app.Update(key)
	runAppCommand(t, app, cmd)
	_ = app.View()
}

func treeSelect(t *testing.T, app *App, name string) {
	t.Helper()
	x, y := editorTreePoint(t, app, name)
	_, _ = app.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	treeRelease(t, app, x, y)
}

func requireFile(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil || string(data) != want {
		t.Fatalf("%s: %q err=%v, want %q", path, data, err, want)
	}
}

func requireMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("%s still exists", path)
	}
}

// Dragging a file onto a folder previews the destination, asks, moves only
// on Move, and Ctrl+Z in the tree moves it back.
func TestTreeDragMovesAfterConfirmAndUndoes(t *testing.T) {
	app, root := treeActionsApp(t)
	tx, ty := editorTreePoint(t, app, "target")
	treeDrag(t, app, "notes.txt", tx, ty)
	if !bufferContainsPlain(app.mainSurf.Buffer(), "Move to target") {
		t.Fatal("drag did not name the receiving folder")
	}
	requireFile(t, filepath.Join(root, "notes.txt"), "notes")
	treeRelease(t, app, tx, ty)
	if !app.overlayHost.IsOpen(overlayTreeMove) || !strings.Contains(app.View().RenderContent(), "Move notes.txt to target?") {
		t.Fatal("drop did not ask before moving")
	}
	requireFile(t, filepath.Join(root, "notes.txt"), "notes")
	_, cmd := app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runAppCommand(t, app, cmd)
	_ = app.View()
	requireMissing(t, filepath.Join(root, "notes.txt"))
	requireFile(t, filepath.Join(root, "target", "notes.txt"), "notes")
	if app.Tree.Model.NodeByPath(filepath.Join(root, "target", "notes.txt")) == nil {
		t.Fatal("tree did not show the moved file")
	}

	treeKey(t, app, tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl})
	requireFile(t, filepath.Join(root, "notes.txt"), "notes")
	requireMissing(t, filepath.Join(root, "target", "notes.txt"))
}

// Esc, Cancel and drops that change nothing leave every file in place.
func TestTreeDragCancelAndInvalidDropsChangeNothing(t *testing.T) {
	app, root := treeActionsApp(t)
	tx, ty := editorTreePoint(t, app, "target")
	treeDrag(t, app, "notes.txt", tx, ty)
	app.setFocusTree()
	_, _ = app.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	treeRelease(t, app, tx, ty)
	if app.overlayHost.Window(overlayTreeMove) != nil {
		t.Fatal("a cancelled drag still asked to move")
	}

	treeDrag(t, app, "notes.txt", tx, ty)
	treeRelease(t, app, tx, ty)
	_, cmd := app.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	runAppCommand(t, app, cmd)
	_, cmd = app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runAppCommand(t, app, cmd)
	requireFile(t, filepath.Join(root, "notes.txt"), "notes")

	// A folder dropped into itself is not offered.
	sx, sy := editorTreePoint(t, app, "src")
	treeDrag(t, app, "src", sx, sy)
	if !bufferContainsPlain(app.mainSurf.Buffer(), "Drop on another folder") {
		t.Fatal("dropping a folder into itself was offered")
	}
	treeRelease(t, app, sx, sy)
	if app.overlayHost.Window(overlayTreeMove) != nil {
		t.Fatal("a folder drop into itself asked to move")
	}
	requireFile(t, filepath.Join(root, "src", "main.go"), "package main\n")
}

// A short click still opens the file; only movement starts a drag.
func TestTreeClickStillOpensFile(t *testing.T) {
	app, root := treeActionsApp(t)
	treeSelect(t, app, "notes.txt")
	if app.EditorState.Tabs[filepath.Join(root, "notes.txt")] == nil {
		t.Fatal("clicking a file did not open it")
	}
}

// Copy/paste into a folder and Duplicate copy with a free name; cut/paste
// moves. The source of a copy is unchanged.
func TestTreeClipboardCopyCutPasteAndDuplicate(t *testing.T) {
	app, root := treeActionsApp(t)
	treeSelect(t, app, "notes.txt")
	treeKey(t, app, tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	treeSelect(t, app, "target")
	treeKey(t, app, tea.KeyPressMsg{Code: 'v', Mod: tea.ModCtrl})
	requireFile(t, filepath.Join(root, "target", "notes.txt"), "notes")
	requireFile(t, filepath.Join(root, "notes.txt"), "notes")

	// Paste again: the name is taken, so it gets a copy name.
	treeKey(t, app, tea.KeyPressMsg{Code: 'v', Mod: tea.ModCtrl})
	requireFile(t, filepath.Join(root, "target", "notes copy.txt"), "notes")

	treeSelect(t, app, "manual")
	treeKey(t, app, tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})
	requireFile(t, filepath.Join(root, "manual copy", "guide.txt"), "guide")

	treeSelect(t, app, "src")
	treeKey(t, app, tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl})
	treeSelect(t, app, "target")
	treeKey(t, app, tea.KeyPressMsg{Code: 'v', Mod: tea.ModCtrl})
	requireMissing(t, filepath.Join(root, "src"))
	requireFile(t, filepath.Join(root, "target", "src", "main.go"), "package main\n")
}

// Every file action is in the tree context menu with its key.
func TestTreeContextMenuOffersFileActions(t *testing.T) {
	app, _ := treeActionsApp(t)
	x, y := editorTreePoint(t, app, "notes.txt")
	app.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseRight})
	if app.LastContextMenu == nil {
		t.Fatal("tree right-click did not open its menu")
	}
	for label, detail := range map[string]string{"Cut": "Ctrl+X", "Copy": "Ctrl+C", "Duplicate": "Ctrl+D", "Rename": "F2", "Delete": "Delete"} {
		item := menuItem(t, app.LastContextMenu, label)
		if item.Disabled || item.Action == nil || item.Detail != detail {
			t.Fatalf("%s: disabled=%v detail=%q", label, item.Disabled, item.Detail)
		}
	}
	if item := menuItem(t, app.LastContextMenu, "Paste"); !item.Disabled {
		t.Fatal("Paste is offered with nothing cut or copied")
	}
	menuItem(t, app.LastContextMenu, "Open to the side")
	menuItem(t, app.LastContextMenu, "Copy relative path")
}

func TestValidMoveTargetKeepsFoldersOutOfThemselves(t *testing.T) {
	root := string(filepath.Separator) + "w"
	cases := []struct {
		source, dir string
		want        bool
	}{
		{filepath.Join(root, "a.txt"), filepath.Join(root, "b"), true},
		{filepath.Join(root, "a.txt"), root, false},
		{filepath.Join(root, "src"), filepath.Join(root, "src"), false},
		{filepath.Join(root, "src"), filepath.Join(root, "src", "nested"), false},
		{filepath.Join(root, "src"), filepath.Join(root, "srcx"), true},
	}
	for _, c := range cases {
		if got := shelltree.ValidMoveTarget(c.source, c.dir); got != c.want {
			t.Fatalf("ValidMoveTarget(%s, %s) = %v", c.source, c.dir, got)
		}
	}
}
