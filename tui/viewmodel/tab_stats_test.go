package viewmodel

import (
	"os"
	"path/filepath"
	"testing"

	"crude/tui/screens/statusoverview"
	"crude/tui/ui"
)

func TestOpenTabsUsesVisibleTabInventory(t *testing.T) {
	app := newTransitionApp(t)
	t.Cleanup(wsOf(app).Close)
	app.minimizeAgent("agent-1")
	app.AddOrchestrator("visible")
	app.AddOrchestrator("minimized")
	app.minimizeAgent("minimized")
	app.AddOrchestrator("unmounted")
	app.Win.CloseTabByID(app.Win.TabByTitle(app.agentTabTitle("unmounted")).ID)
	path := filepath.Join(t.TempDir(), "visible.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := app.openEditorTab(path); err != nil {
		t.Fatal(err)
	}
	items := app.openTabItems()
	if len(items) != 2 || items[0].ID != "agent:visible" || items[1].ID != "editor:"+path {
		t.Fatalf("Open Tabs does not match visible strip order: %+v", items)
	}
	// Workspace deliberately retains minimized agents; only Open Tabs filters.
	retained := false
	for _, item := range app.statusTabItems() {
		retained = retained || item.ID == "agent:minimized"
	}
	if !retained {
		t.Fatal("Open Tabs filtering removed minimized agents from Workspace")
	}
	app.Win.CloseTabByID(app.Win.TabByTitle(app.editorTabTitle(path)).ID)
	if items = app.openTabItems(); len(items) != 1 || items[0].ID != "agent:visible" {
		t.Fatalf("retained editor without a tab leaked into modal: %+v", items)
	}
}

func TestOpenTabsModalIsWiderAndClamped(t *testing.T) {
	app := newFullTestApp(t)
	app.AddOrchestrator("root")
	for _, width := range []int{160, 80} {
		app.width, app.height = width, 36
		app.openTabStats("agent:root")
		win := app.overlayHost.Window(ui.OverlayTabStats)
		if win == nil {
			t.Fatal("Open Tabs modal did not open")
		}
		if got, want := win.ContentBounds().W, min(112, width-4); got != want {
			t.Fatalf("modal width=%d want %d", got, want)
		}
		app.overlayHost.Close(ui.OverlayTabStats)
	}
}

func TestPreviousSessionsIsFinalWorkspaceSection(t *testing.T) {
	app := newFullTestApp(t)
	app.AddOrchestrator("root")
	items := app.statusPanelProjection(statusoverview.ViewTabs).Views[statusoverview.ViewTabs]
	index := -1
	for i, item := range items {
		if item.ID == "previous-sessions" {
			index = i
		}
		if index >= 0 && item.ID == "agent:root" {
			t.Fatal("agent row appears after Previous Sessions")
		}
	}
	if index < 1 || items[len(items)-1].ID != "sessions-empty" {
		t.Fatalf("Previous Sessions is not the final section: %+v", items)
	}
}
