package viewmodel

import (
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/provider"
	"crude/tui/editor"
	"crude/tui/filetree"
	"crude/tui/screens/shelltabs"
	"crude/tui/screens/shelltree"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/tui/window"
)

// TestTabBarCloseClickAtProductionGeometry drives the real viewmodel mouse
// path (classifyMouse → handleTabBarEvent → handleAgentTabClose) with a
// sidebar + PadLR inset matching live layout — not the isolated shelltabs
// Host contract.
func TestTabBarCloseClickAtProductionGeometry(t *testing.T) {
	dir := t.TempDir()
	pathA := filepath.Join(dir, "alpha.go")
	pathB := filepath.Join(dir, "beta.go")
	for _, p := range []string{pathA, pathB} {
		if err := os.WriteFile(p, []byte("package main\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	app := newFullTestApp(t)
	app.width, app.height = 140, 40
	app.Theme = theme.DefaultDark()
	app.BufferMgr = editor.NewBufferManager()
	app.Win = window.New(app.Theme)
	app.tabScreen = shelltabs.New(app.Theme)
	app.tabHost = ui.NewHost(app.Theme, app.tabScreen)
	app.FocusStack.Clear()
	app.launcherScreen = nil
	app.launcherHost = nil

	model := filetree.NewFileTreeModel(dir)
	app.Tree = NewTreeState(model, 30)
	app.Tree.Visible = true
	th := theme.DefaultDark()
	app.sidebarScreen = shelltree.New(th, model)
	app.sidebarHost = ui.NewHost(th, app.sidebarScreen)

	app.AddOrchestrator(
		"agent",
		make(chan provider.UserInput, 1),
		make(chan provider.UserInput, 1),
		make(chan struct{}, 1),
		make(chan string, 1),
	)
	if _, err := app.openEditorTab(pathA); err != nil {
		t.Fatal(err)
	}
	if _, err := app.openEditorTab(pathB); err != nil {
		t.Fatal(err)
	}
	if app.Win.TabCount() < 2 {
		t.Fatalf("need ≥2 tabs, got %d", app.Win.TabCount())
	}
	before := app.Win.TabCount()

	app.ensureTabBarGeometry()
	tabs := app.tabScreen.TabsControl()
	closeLocal := tabs.CloseLocalX(0)
	if closeLocal < 0 {
		t.Fatal("could not resolve close local X for tab 0")
	}
	layout := app.computeLayout()
	screenX := layout.SidebarW + layout.PadLR + closeLocal

	idx, closeHit := app.tabBarHitTest(screenX)
	if idx != 0 || !closeHit {
		t.Fatalf("tabBarHitTest(%d)=(%d,%v) want (0,true); sidebar=%d pad=%d local=%d",
			screenX, idx, closeHit, layout.SidebarW, layout.PadLR, closeLocal)
	}

	_, _ = app.dispatchMouseClick(tea.MouseClickMsg{
		X: screenX, Y: 3, Button: tea.MouseLeft,
	})
	if app.Win.TabCount() != before-1 {
		t.Fatalf("after close click tab count=%d want %d", app.Win.TabCount(), before-1)
	}
}
