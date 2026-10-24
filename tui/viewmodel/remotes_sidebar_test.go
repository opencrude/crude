package viewmodel

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"crude/remoteconn"
	"crude/tui/screens/activitybar"
	"crude/tui/screens/activitylist"
)

func remoteSidebarFixture(t *testing.T, width, height int) (*App, *fakeRemoteManager) {
	t.Helper()
	app := newFullTestApp(t)
	wsOf(app).Logger = stubLogger{}
	app.Update(tea.WindowSizeMsg{Width: width, Height: height})
	manager := &fakeRemoteManager{snapshot: remoteconn.Snapshot{Connections: []remoteconn.Connection{
		{ID: "alpha", Name: "Alpha", Kind: "ssh", Host: "alpha.example"},
		{ID: "beta", Name: "Beta", Kind: "ssh", Host: "beta.example"},
	}}}
	app.SetRemoteManager(manager, remoteconn.Local)
	app.remotes.snapshot = manager.snapshot
	app.setActivity(activityRemotes, true)
	app.setFocusTree()
	app.paintShellFrame()
	return app, manager
}

func remoteSidebarPoint(t *testing.T, app *App, id string, subtitle bool) tea.Mouse {
	t.Helper()
	app.paintShellFrame()
	list, _ := app.rootHost.ScreenRect(activitylist.ListID)
	pane, _ := app.rootHost.ScreenRect(activitylist.ScreenID)
	for y := list.Y; y < list.Y+list.H; y++ {
		item, hit := app.activityList.ItemAtLocalY(y - pane.Y)
		if hit && item.ID == id {
			if subtitle {
				y++
			}
			return tea.Mouse{X: list.X + 2, Y: y}
		}
	}
	t.Fatalf("connection %q is not visible", id)
	return tea.Mouse{}
}

func TestConnectionsSidebarContextMenuTargetsClickedConnection(t *testing.T) {
	for _, size := range [][2]int{{160, 40}, {60, 24}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			app, _ := remoteSidebarFixture(t, size[0], size[1])
			// Beta's subtitle owns the same menu as its name, even with the
			// browsing cursor still on Add connection.
			point := remoteSidebarPoint(t, app, "remote:beta", true)
			_, cmd := app.Update(tea.MouseClickMsg{X: point.X, Y: point.Y, Button: tea.MouseRight})
			runAppCommand(t, app, cmd)
			menu := app.LastContextMenu
			if menu == nil || menu.Title != "Beta" {
				t.Fatalf("right click opened the wrong destination menu: %+v", menu)
			}
			for _, label := range []string{"Connect / Open workspace…", "Test connection", "Edit / Pair…", "Remove…", "Refresh connections"} {
				if item := menuItem(t, menu, label); item.Disabled || item.Action == nil {
					t.Fatalf("connection action %q unavailable", label)
				}
			}
			for _, section := range menu.Sections {
				for _, item := range section.Items {
					if strings.Contains(item.Label, "file") || strings.Contains(item.Label, "folder") || strings.Contains(item.Label, "shortcut") {
						t.Fatalf("Connections leaked another sidebar's action %q", item.Label)
					}
				}
			}
			app.closeContextMenu()
			activateMenuItem(t, menu, "Edit / Pair…")
			if app.remotes.editorID != "beta" || app.remotes.editor == nil || app.remotes.editor.Model.Field("host").Value.String() != "beta.example" {
				t.Fatal("right-click Edit did not target Beta")
			}
			app.cancelRemoteEditor()
			local := app.buildActivityRegionContextMenu(remoteSidebarPoint(t, app, "remote:local", false))
			for _, section := range local.Sections {
				for _, item := range section.Items {
					if item.Label == "Edit / Pair…" || item.Label == "Remove…" || item.Label == "Test connection" {
						t.Fatalf("Local offered saved-connection operation %q", item.Label)
					}
				}
			}
			app.remotes.current = "beta"
			active := app.buildActivityRegionContextMenu(remoteSidebarPoint(t, app, "remote:beta", false))
			if !menuItem(t, active, "Remove…").Disabled || menuItem(t, active, "Disconnect / Switch to Local…").Disabled {
				t.Fatal("active connection removal/disconnect policy bypassed")
			}
			app.workspaceSwitchGen.Add(1)
			activateMenuItem(t, menu, "Edit / Pair…")
			if app.remotes.editor != nil {
				t.Fatal("stale context menu acted in a replacement workspace lifetime")
			}
		})
	}
}

func TestConnectionsRefreshUsesWorkspaceLoadingContract(t *testing.T) {
	app, manager := remoteSidebarFixture(t, 120, 35)
	menu := app.buildActivityRegionContextMenu(remoteSidebarPoint(t, app, "remote:beta", false))
	results := make(chan tea.Msg, 2)
	restore := installDetachedMessageSinkForTest(app, func(msg tea.Msg) { results <- msg })
	defer restore()
	manager.snapshotErr = errors.New("catalog unavailable")
	activateMenuItem(t, menu, "Refresh connections")
	if !app.remotes.loading || !app.wantsFastTick() || app.refreshRemotes() != nil {
		t.Fatal("refresh failed to start the shared animation or admitted duplicate work")
	}
	app.SpinFrame = 100
	app.paintShellFrame()
	progress, ok := app.rootHost.ScreenRect(activitylist.ProgressID)
	list, _ := app.rootHost.ScreenRect(activitylist.ListID)
	if !ok {
		t.Fatal("Connections did not mount the standard progress rule")
	}
	first := app.mainSurf.Buffer().Clone()
	for _, text := range []string{"Refresh connections", "Loading", "Refreshing"} {
		if strings.Contains(bufferPlain(first), text) {
			t.Fatalf("refresh injected a dedicated control/text row %q", text)
		}
	}
	app.SpinFrame = 1500
	app.paintShellFrame()
	changed := false
	for x := progress.X; x < progress.X+progress.W; x++ {
		changed = changed || first.Get(x, progress.Y).FG != app.mainSurf.Buffer().Get(x, progress.Y).FG
	}
	if !changed {
		t.Fatal("Connections loading rule did not animate")
	}
	app.Tree.Visible = false
	if app.wantsFastTick() {
		t.Fatal("hidden Connections kept requesting animation frames")
	}
	app.Tree.Visible = true
	app.setActivity(activityAgents, true)
	if app.wantsFastTick() {
		t.Fatal("Connections loading leaked into Agents")
	}
	app.setActivity(activityRemotes, true)
	if !app.wantsFastTick() {
		t.Fatal("returning to Connections lost pending animation")
	}
	select {
	case result := <-results:
		app.Update(result)
	case <-time.After(2 * time.Second):
		t.Fatal("refresh did not deliver its completion")
	}
	app.paintShellFrame()
	text := bufferPlain(app.mainSurf.Buffer())
	if app.wantsFastTick() || app.remotes.loading || !strings.Contains(text, "Beta") || !strings.Contains(text, "Couldn’t refresh") {
		t.Fatal("failed refresh lost usable connections or failed to settle visibly")
	}
	if got, _ := app.rootHost.ScreenRect(activitylist.ListID); got != list {
		t.Fatal("refresh changed list geometry")
	}
	manager.snapshotErr = nil
	runAppCommand(t, app, app.refreshRemotes())
	app.paintShellFrame()
	if app.remotes.loadError != "" || strings.Contains(bufferPlain(app.mainSurf.Buffer()), "Couldn’t refresh") {
		t.Fatal("successful refresh left the old failure visible")
	}
	for _, item := range app.remoteSidebarItems() {
		if item.ID == "remote.refresh" {
			t.Fatal("dedicated Refresh row survived")
		}
	}
}

func TestConnectionsRailMenuHasOnlyEffectiveNavigation(t *testing.T) {
	app, _ := remoteSidebarFixture(t, 120, 35)
	menu := app.buildActivityRailContextMenu(activitybar.ViewRemotes)
	menuItem(t, menu, "Add connection…")
	menuItem(t, menu, "Refresh connections")
	for _, section := range menu.Sections {
		for _, item := range section.Items {
			if item.Label == "Unpin shortcut" || item.Label == "Move up" || item.Label == "Move down" {
				t.Fatalf("mandatory Connections offered ineffective action %q", item.Label)
			}
		}
	}
}
