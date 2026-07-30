package viewmodel

import (
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/filetree"
	"crude/tui/screens/launcher"
	"crude/tui/ui"
	wm "crude/tui/wm"
	"crude/workspace"
)

func setupLauncherRoutingApp(t *testing.T, workspaces []workspace.WorkspaceInfo) (*App, *launcher.Screen) {
	t.Helper()
	app := newTestAppWithLauncher(t)
	app.width = 100
	app.height = 30
	app.ChatAreaHeight = 20
	app.Tree = NewTreeState(filetree.NewFileTreeModel(t.TempDir()), 30)
	app.Tree.Visible = true
	if app.FocusStack == nil {
		app.FocusStack = workspace.NewFocusStack()
	}
	if app.windowManager == nil {
		app.windowManager = wm.NewWindowManager()
		app.overlayHost = ui.NewOverlayHost(app.windowManager)
	}

	app.launcherScreen.SetData(".", workspaces)
	layout := app.computeLayout()
	app.launcherHost.Resize(layout.InnerW, app.ChatAreaHeight)
	app.launcherHost.Layout()

	if app.focusTarget() != FocusLauncher {
		t.Fatalf("focusTarget=%v want FocusLauncher", app.focusTarget())
	}
	return app, app.launcherScreen
}

// TestLauncherViewmodelKeyboardContractTable verifies viewmodel dispatch to the
// launcher Host and global ActionRegistry chords while the welcome screen is active.
func TestLauncherViewmodelKeyboardContractTable(t *testing.T) {
	t.Run("routes_down_to_launcher_host", func(t *testing.T) {
		app, screen := setupLauncherRoutingApp(t, []workspace.WorkspaceInfo{
			{ProjectName: "alpha", ProjectDir: "/projects/alpha"},
		})
		app.dispatchKeyPress(tea.KeyPressMsg{Code: tea.KeyDown})
		if got := screen.SelectedItem(); got == nil || got.Kind != launcher.ItemOpenFolder {
			t.Fatalf("selection=%#v want OpenFolder", got)
		}
	})

	t.Run("global_sidebar_toggle_from_launcher", func(t *testing.T) {
		app, _ := setupLauncherRoutingApp(t, nil)
		visibleBefore := app.Tree.Visible
		app.dispatchKeyPress(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
		if app.Tree.Visible == visibleBefore {
			t.Fatal("Ctrl+B should toggle sidebar via ActionRegistry on launcher")
		}
	})

	t.Run("global_palette_opens_without_clearing_launcher", func(t *testing.T) {
		app, _ := setupLauncherRoutingApp(t, nil)
		app.registerOverlayHandlers()
		app.dispatchKeyPress(tea.KeyPressMsg{Text: "ctrl+alt+p", Mod: tea.ModCtrl | tea.ModAlt, Code: 'p'})
		if app.overlayHost.Window(ui.OverlayCommandPalette) == nil {
			t.Fatal("Ctrl+Alt+P should open command palette from launcher")
		}
		if app.launcherScreen == nil {
			t.Fatal("palette open must not clear launcher")
		}
	})

	t.Run("esc_filter_clear_then_launcher_stays", func(t *testing.T) {
		app, screen := setupLauncherRoutingApp(t, []workspace.WorkspaceInfo{
			{ProjectName: "alpha", ProjectDir: "/projects/alpha"},
		})
		app.dispatchKeyPress(tea.KeyPressMsg{Code: '/', Text: "/"})
		app.dispatchKeyPress(tea.KeyPressMsg{Code: tea.KeyEsc})
		if screen.FilterActive() {
			t.Fatal("Esc should clear launcher filter")
		}
		app.dispatchKeyPress(tea.KeyPressMsg{Code: tea.KeyEsc})
		if app.launcherScreen == nil {
			t.Fatal("second Esc does not dismiss launcher (no agents yet)")
		}
	})

	t.Run("ctrl_c_does_not_dismiss_launcher", func(t *testing.T) {
		app, screen := setupLauncherRoutingApp(t, []workspace.WorkspaceInfo{
			{ProjectName: "alpha", ProjectDir: "/projects/alpha"},
		})
		app.dispatchKeyPress(tea.KeyPressMsg{Code: tea.KeyDown})
		before := screen.SelectedItem()
		app.dispatchKeyPress(tea.KeyPressMsg{Text: "ctrl+c", Mod: tea.ModCtrl, Code: 'c'})
		if app.launcherScreen == nil {
			t.Fatal("Ctrl+C must not clear launcher")
		}
		if got := screen.SelectedItem(); got == nil || before == nil || got.Kind != before.Kind {
			t.Fatalf("Ctrl+C must not move selection")
		}
	})

	t.Run("enter_emits_activation_without_double_session", func(t *testing.T) {
		app, screen := setupLauncherRoutingApp(t, nil)
		_, cmd := app.dispatchKeyPress(tea.KeyPressMsg{Code: tea.KeyEnter})
		if cmd == nil {
			t.Fatal("Enter should produce launcher activation cmd")
		}
		msg, ok := cmd().(launcher.ActivateMsg)
		if !ok || msg.Screen != screen || msg.Kind != launcher.ItemNewSession {
			t.Fatalf("activation=%#v", cmd())
		}
		if app.AgentMgr.Count() != 0 {
			t.Fatal("contract test must not invoke AddOrchestrator; activation is observable only")
		}
	})

	t.Run("min_size_hidden_launcher_does_not_consume_keys", func(t *testing.T) {
		app, screen := setupLauncherRoutingApp(t, []workspace.WorkspaceInfo{
			{ProjectName: "alpha", ProjectDir: "/projects/alpha"},
		})
		app.ChatAreaHeight = 2 // below renderLauncher minimum of 3
		if app.focusTarget() == FocusLauncher {
			t.Fatal("hidden launcher must not own focusTarget")
		}
		before := screen.SelectedItem()
		app.dispatchKeyPress(tea.KeyPressMsg{Code: tea.KeyDown})
		if got := screen.SelectedItem(); got == nil || before == nil || got.Kind != before.Kind {
			t.Fatalf("keys must not route to hidden launcher: before=%#v after=%#v", before, got)
		}
	})

	t.Run("long_workspace_list_scroll_via_viewmodel", func(t *testing.T) {
		workspaces := make([]workspace.WorkspaceInfo, 25)
		for i := range workspaces {
			workspaces[i] = workspace.WorkspaceInfo{
				ProjectName: fmt.Sprintf("ws-%02d", i),
				ProjectDir:  fmt.Sprintf("/tmp/ws-%02d", i),
			}
		}
		app, screen := setupLauncherRoutingApp(t, workspaces)
		app.dispatchKeyPress(tea.KeyPressMsg{Code: tea.KeyEnd})
		if screen.ListScrollOffset() == 0 {
			t.Fatal("End should scroll workspace list through viewmodel dispatch")
		}
	})
}
