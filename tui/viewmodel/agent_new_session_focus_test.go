package viewmodel

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	ttypes "crude/tui/types"
	"crude/tui/ui"
	"crude/workspace"
)

func openEditorTabs(t *testing.T, app *App, names ...string) []string {
	t.Helper()
	paths := make([]string, 0, len(names))
	for _, name := range names {
		path := filepath.Join(app.RootPath, name)
		if _, err := os.Stat(path); err != nil {
			if err := os.WriteFile(path, []byte("package "+name+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := app.openEditorTab(path); err != nil {
			t.Fatalf("open %s: %v", name, err)
		}
		paths = append(paths, path)
	}
	return paths
}

func editorTabPresent(app *App, path string) bool {
	return app.Win != nil && app.Win.TabByTitle(app.editorTabTitle(path)) != nil
}

func agentTabPresent(app *App, id string) bool {
	return app.Win != nil && app.Win.TabByTitle(app.agentTabTitle(id)) != nil
}

func assertAgentIsPaintedView(t *testing.T, app *App, agentID string) {
	t.Helper()
	if agentID == "" {
		t.Fatal("expected an agent id")
	}
	if !agentTabPresent(app, agentID) {
		t.Fatalf("agent tab missing for %q; tabs=%d", agentID, app.Win.TabCount())
	}
	active := app.Win.ActiveTab()
	want := app.agentTabTitle(agentID)
	if active == nil || active.Title != want {
		got := ""
		if active != nil {
			got = active.Title
		}
		t.Fatalf("active window tab=%q want %q", got, want)
	}
	if app.EditorState.ActiveView != "" {
		t.Fatalf("editor still painted ActiveView=%q", app.EditorState.ActiveView)
	}
	if app.AgentView.ActiveID() != agentID {
		t.Fatalf("active agent=%q want %q", app.AgentView.ActiveID(), agentID)
	}
	app.paintShellFrame()
	if !app.shellSlotActive(shellChatSlot) {
		t.Fatal("chat slot must be the painted main view")
	}
	if app.shellSlotActive(shellEditorSlot) {
		t.Fatal("editor slot must not stay painted over the new agent")
	}
	if app.shellSlotActive(shellLauncherSlot) {
		t.Fatal("launcher must not stay painted over the new agent")
	}
}

func assertEditorStillPainted(t *testing.T, app *App, path string) {
	t.Helper()
	if app.EditorState.ActiveView != path {
		t.Fatalf("ActiveView=%q want %q", app.EditorState.ActiveView, path)
	}
	active := app.Win.ActiveTab()
	want := app.editorTabTitle(path)
	if active == nil || active.Title != want {
		got := ""
		if active != nil {
			got = active.Title
		}
		t.Fatalf("active window tab=%q want %q", got, want)
	}
	app.paintShellFrame()
	if !app.shellSlotActive(shellEditorSlot) {
		t.Fatal("editor slot must stay painted")
	}
	if app.shellSlotActive(shellChatSlot) {
		t.Fatal("chat must not steal the editor")
	}
}

// Opening New Agent while editor tabs are already open must add the agent
// tab immediately and make it the active/painted view. The previous
// syncAgentWindowTabs early-return on ActiveView left the session in
// AgentMgr with no tab until every editor tab was closed.
func TestNewAgentWhileEditingSurfacesAndActivatesTab(t *testing.T) {
	app, ws := newRealShellApp(t)
	defer ws.Close()

	paths := openEditorTabs(t, app, "main.go", "extra.go")
	if app.Win.TabCount() != 2 {
		t.Fatalf("precondition: two editor tabs, got %d", app.Win.TabCount())
	}
	assertEditorStillPainted(t, app, paths[1])

	if cmd := app.launcherNewSession(); cmd != nil {
		_ = cmd()
	}
	id := app.AgentView.ActiveID()
	if id == "" {
		t.Fatal("New Agent must create and activate a session")
	}
	if app.Win.TabCount() != 3 {
		t.Fatalf("agent tab must join the open editor tabs, tabs=%d want 3", app.Win.TabCount())
	}
	for _, path := range paths {
		if !editorTabPresent(app, path) {
			t.Fatalf("editor tab for %s disappeared; closing editors must not be required to see the agent", path)
		}
	}
	assertAgentIsPaintedView(t, app, id)
}

func TestNewAgentWhileEditing_SidebarButton(t *testing.T) {
	app, ws := newRealShellApp(t)
	defer ws.Close()

	paths := openEditorTabs(t, app, "main.go")
	if cmd := app.handleSidebarActivate(ui.ListItem{
		ID:      sidebarItemNewAgent,
		Label:   "New Agent",
		Command: true,
	}); cmd != nil {
		_ = cmd()
	}
	id := app.AgentView.ActiveID()
	if id == "" {
		t.Fatal("Agents panel New Agent must start a session")
	}
	if !editorTabPresent(app, paths[0]) {
		t.Fatal("editor tab must remain after New Agent")
	}
	assertAgentIsPaintedView(t, app, id)
}

func TestNewAgentWhileEditing_AddOrchestrator(t *testing.T) {
	app, ws := newRealShellApp(t)
	defer ws.Close()

	paths := openEditorTabs(t, app, "main.go")
	app.AddOrchestrator(
		"orch-from-editor")

	if !editorTabPresent(app, paths[0]) {
		t.Fatal("editor tab must remain")
	}
	assertAgentIsPaintedView(t, app, "orch-from-editor")
}

// Background tab sync (the tick path) must create missing agent tabs
// without yanking the editor. User actions call activateAgentWindowTab;
// tick must not.
func TestTabSyncWhileEditingDoesNotStealEditor(t *testing.T) {
	app, ws := newRealShellApp(t)
	defer ws.Close()

	app.AddOrchestrator(
		"orch-1")

	paths := openEditorTabs(t, app, "main.go")
	if !agentTabPresent(app, "orch-1") {
		t.Fatal("precondition: agent tab should already exist")
	}
	assertEditorStillPainted(t, app, paths[0])

	for i := 0; i < 3; i++ {
		app.syncAgentWindowTabs()
		_, _ = app.handleTick(ttypes.TickMsg(time.Now()))
	}
	if !agentTabPresent(app, "orch-1") {
		t.Fatal("tick must keep the agent tab")
	}
	if !editorTabPresent(app, paths[0]) {
		t.Fatal("tick must keep the editor tab")
	}
	assertEditorStillPainted(t, app, paths[0])
}

// Direct sync while editing must still *create* the agent tab. This is the
// half of the old bug that skipped OpenTab entirely when ActiveView was set.
func TestTabSyncWhileEditingCreatesMissingAgentTab(t *testing.T) {
	app, ws := newRealShellApp(t)
	defer ws.Close()

	paths := openEditorTabs(t, app, "main.go")
	app.AgentMgr.AddOrchestrator(
		"background-orch",

		workspace.NewAgentState("background-orch", nil, nil))

	app.syncAgentWindowTabs()
	if !agentTabPresent(app, "background-orch") {
		t.Fatal("syncAgentWindowTabs must OpenTab while an editor is showing")
	}
	if !editorTabPresent(app, paths[0]) {
		t.Fatal("creating the agent tab must not close the editor tab")
	}
	assertEditorStillPainted(t, app, paths[0])
}

func TestBackgroundSubAgentTabCreationDoesNotStealRootSelection(t *testing.T) {
	app := newFullTestApp(t)
	app.AddOrchestrator("root")
	pane := app.PaneMgr.GetOrCreate("child", workspace.PaneAgent, "child")
	app.AgentMgr.AddSubAgent("child", "root", "child", "model", "researcher",
		workspace.NewAgentState("child", pane.Messages, app.Logger))
	app.syncAgentWindowTabs()

	active := app.Win.ActiveTab()
	if active == nil || active.Title != app.agentTabTitle("root") || app.AgentView.ActiveID() != "root" {
		t.Fatalf("background child stole selection: tab=%#v activeAgent=%q", active, app.AgentView.ActiveID())
	}
}

func TestAgentTabSyncPreservesUserOrderWithoutMovingEditorOrSelection(t *testing.T) {
	app, ws := newRealShellApp(t)
	defer ws.Close()
	app.AddOrchestrator("root")
	firstEditor := openEditorTabs(t, app, "first.go")[0]
	for _, id := range []string{"older", "newer"} {
		pane := app.PaneMgr.GetOrCreate(id, workspace.PaneAgent, id)
		app.AgentMgr.AddSubAgent(id, "root", id, "model", "researcher",
			workspace.NewAgentState(id, pane.Messages, app.Logger))
		app.syncAgentWindowTabs()
	}
	secondEditor := openEditorTabs(t, app, "second.go")[0]
	// A deliberate reorder must not be undone by the next background tick.
	rootTab := app.Win.TabByTitle(app.agentTabTitle("root"))
	app.Win.MoveTab(app.Win.IndexOf(rootTab.ID), 3)
	app.syncAgentWindowTabs()
	want := []string{app.editorTabTitle(firstEditor), app.agentTabTitle("older"),
		app.agentTabTitle("newer"), app.agentTabTitle("root"), app.editorTabTitle(secondEditor)}
	tabs := app.Win.Tabs()
	if len(tabs) != len(want) {
		t.Fatalf("tabs=%+v want %v", tabs, want)
	}
	for i, tab := range tabs {
		if tab.Title != want[i] {
			t.Fatalf("tab order=%+v want %v", tabs, want)
		}
	}
	assertEditorStillPainted(t, app, secondEditor)
	app.switchToAgentView("older")
	app.syncAgentWindowTabs()
	if app.activeAgentTabID() != "older" || app.Win.Tabs()[3].Title != app.agentTabTitle("root") {
		t.Fatal("background sync changed selected child or user tab order")
	}
}

func TestAgentTabSyncGroupsMultipleRootsWithoutStealingSelection(t *testing.T) {
	app := newFullTestApp(t)
	app.AddOrchestrator("first")
	app.AgentMgr.AddSubAgent("first-child", "first", "first child", "model", "researcher",
		workspace.NewAgentState("first-child", nil, nil))
	app.AddOrchestrator("second")
	app.AgentMgr.AddSubAgent("second-child", "second", "second child", "model", "researcher",
		workspace.NewAgentState("second-child", nil, nil))
	app.syncAgentWindowTabs()
	want := []string{"first", "first-child", "second", "second-child"}
	for i, id := range want {
		if got := app.Win.Tabs()[i].Title; got != app.agentTabTitle(id) {
			t.Fatalf("two roots grouped incorrectly: tabs=%+v", app.Win.Tabs())
		}
	}
	if app.activeAgentTabID() != "second" {
		t.Fatalf("background tab sync stole active main agent: %q", app.activeAgentTabID())
	}
}

func TestTabSyncRepairsStaleManagerSelectionFromVisibleWindow(t *testing.T) {
	app := newFullTestApp(t)
	app.AddOrchestrator("root")
	pane := app.PaneMgr.GetOrCreate("child", workspace.PaneAgent, "child")
	app.AgentMgr.AddSubAgent("child", "root", "child", "model", "researcher",
		workspace.NewAgentState("child", pane.Messages, app.Logger))
	app.syncAgentWindowTabs()
	app.AgentView.SetActive("child") // simulate stale background/domain selection

	app.syncAgentWindowTabs()
	active := app.Win.ActiveTab()
	if active == nil || active.Title != app.agentTabTitle("root") || app.AgentView.ActiveID() != "root" || app.activeAgentTabID() != "root" {
		t.Fatalf("window authority did not repair split state: tab=%#v manager=%q prompt=%q",
			active, app.AgentView.ActiveID(), app.activeAgentTabID())
	}
}

func TestRestoreAgentWhileEditingActivatesTab(t *testing.T) {
	app, ws := newRealShellApp(t)
	defer ws.Close()

	app.AddOrchestrator(
		"orch-1")

	app.minimizeAgent("orch-1")
	paths := openEditorTabs(t, app, "main.go")
	if agentTabPresent(app, "orch-1") {
		t.Fatal("minimized agent must not keep a tab")
	}

	app.restoreAgent("orch-1")
	if !editorTabPresent(app, paths[0]) {
		t.Fatal("restoring must not close the editor tab")
	}
	assertAgentIsPaintedView(t, app, "orch-1")
}

func TestSubAgentTabCreatedWhileEditingDoesNotStealEditor(t *testing.T) {
	app, ws := newRealShellApp(t)
	defer ws.Close()

	app.AddOrchestrator(
		"root")

	paths := openEditorTabs(t, app, "main.go")

	childPane := app.PaneMgr.GetOrCreate("child", workspace.PaneAgent, "child")
	if app.AgentMgr.AddSubAgent(
		"child", "root", "Researcher", "child-model", "researcher",
		workspace.NewAgentState("child", childPane.Messages, nil),
	) == nil {
		t.Fatal("failed to create child")
	}
	app.syncAgentWindowTabs()
	if !agentTabPresent(app, "child") {
		t.Fatal("sub-agent tab must appear while editing")
	}
	if !agentTabPresent(app, "root") {
		t.Fatal("parent agent tab must remain")
	}
	assertEditorStillPainted(t, app, paths[0])
}
