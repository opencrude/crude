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
	want := []string{"Open", "Copy path", "Delete workspace…"}
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

	// Right-click above the workspace rows (heading/search area) opens nothing.
	_, _ = app.dispatchMouseRightClick(tea.MouseClickMsg{X: rect.X + 2, Y: rect.Y - 4, Button: tea.MouseRight})
	if app.windowManager.Window(string(ui.OverlayContextMenu)) != nil {
		t.Fatal("right-click off the workspace rows must not open a menu")
	}

	// A visual separator is not part of either neighboring resource.
	_, _ = app.dispatchMouseRightClick(tea.MouseClickMsg{X: rect.X + 2, Y: rect.Y + 2, Button: tea.MouseRight})
	if app.windowManager.Window(string(ui.OverlayContextMenu)) != nil {
		t.Fatal("right-click on a workspace separator opened a menu")
	}
	_, _ = app.dispatchMouseRightClick(tea.MouseClickMsg{X: rect.X + 2, Y: rect.Y + 1, Button: tea.MouseRight})
	if app.windowManager.Window(string(ui.OverlayContextMenu)) == nil {
		t.Fatal("right-click on a workspace path lost the menu")
	}
}
