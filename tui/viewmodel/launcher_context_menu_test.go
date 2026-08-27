package viewmodel

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/screens/launcher"
	"crude/tui/ui"
)

// TestLauncherContextMenuOpensOnWorkspaceRow exercises the full right-click
// path: right-click on a recent-workspaces row opens the context menu with
// the management actions; right-click elsewhere in the launcher opens
// nothing; a second right-click dismisses an open menu.
func TestLauncherContextMenuOpensOnWorkspaceRow(t *testing.T) {
	app := newTestAppWithLauncher(t)
	app.width = 120
	app.height = 30
	app.ChatAreaHeight = 20
	app.Tree = &TreeState{}
	app.paintShellFrame()

	rect, ok := app.rootHost.ScreenRect(launcher.WorkspaceListID)
	if !ok || rect.Empty() {
		t.Fatal("workspace list must be mounted with geometry")
	}

	// Right-click the first workspace row.
	_, _ = app.dispatchMouseRightClick(tea.MouseClickMsg{X: rect.X + 2, Y: rect.Y, Button: tea.MouseRight})
	fw := app.windowManager.Window(string(ui.OverlayContextMenu))
	if fw == nil {
		t.Fatal("context menu should open for a right-click on a workspace row")
	}
	st := app.LastContextMenu
	if st == nil {
		t.Fatal("context menu state missing after open")
	}
	var labels []string
	for _, sec := range st.Sections {
		for _, it := range sec.Items {
			labels = append(labels, it.Label)
		}
	}
	want := []string{"Open", "Copy path", "Remove from list"}
	if len(labels) != len(want) {
		t.Fatalf("menu labels=%v want %v", labels, want)
	}
	for i := range want {
		if labels[i] != want[i] {
			t.Fatalf("menu labels=%v want %v", labels, want)
		}
	}

	// The same gesture dismisses an open menu.
	_, _ = app.dispatchMouseRightClick(tea.MouseClickMsg{X: rect.X + 2, Y: rect.Y, Button: tea.MouseRight})
	if app.windowManager.Window(string(ui.OverlayContextMenu)) != nil {
		t.Fatal("second right-click should close the context menu")
	}

	// Right-click above the workspace rows (hero/search area) opens nothing.
	_, _ = app.dispatchMouseRightClick(tea.MouseClickMsg{X: rect.X + 2, Y: rect.Y - 4, Button: tea.MouseRight})
	if app.windowManager.Window(string(ui.OverlayContextMenu)) != nil {
		t.Fatal("right-click off the workspace rows must not open a menu")
	}
}

// TestLauncherContextMenuRemoveAction verifies the Remove-from-list action
// drops the row from the launcher listing. The persisted delete targets the
// fake test ID (a non-existent workspace hash), so no real data is touched.
func TestLauncherContextMenuRemoveAction(t *testing.T) {
	app := newTestAppWithLauncher(t)
	before := app.launcherScreen.TotalItems()
	if before != 14 { // 2 fixed actions + 12 seeded workspaces
		t.Fatalf("setup: TotalItems=%d want 14", before)
	}

	app.removeLauncherWorkspace(launcher.WorkspaceEntry{ID: "ws7", Name: "proj-07", Path: "/tmp/proj-07"})

	if got := app.launcherScreen.TotalItems(); got != before-1 {
		t.Fatalf("TotalItems=%d want %d after removal", got, before-1)
	}
}
