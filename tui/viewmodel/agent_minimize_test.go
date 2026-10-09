package viewmodel

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"crude/sessiondb"
	"crude/workspace"
)

func newMinimizeTestAgent(t *testing.T, app *App, id string) {
	t.Helper()
	app.AddOrchestrator(
		id)

}

// Closing an agent tab must not stop the agent. The orchestrator keeps
// running, the conversation is retained, and the tab is the only thing that
// goes away — otherwise a stray click destroys work in flight.
func TestClosingAgentTabMinimizesWithoutCancelling(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 120, 40
	newMinimizeTestAgent(t, app, "orch-1")

	if cmd := app.handleAgentTabClose(0); cmd != nil {
		t.Fatal("closing an agent tab should not return a command")
	}

	node := app.ws.AgentMgr().Get("orch-1")
	if node == nil {
		t.Fatal("closing a tab must not remove the agent")
	}
	if !app.AgentView.IsHidden(node.ID) {
		t.Fatal("closed agent should be minimized")
	}
	if app.AgentView.VisibleCount() != 0 || app.Win.TabCount() != 0 {
		t.Fatalf("tab should be gone: visible=%d tabs=%d", app.AgentView.VisibleCount(), app.Win.TabCount())
	}
	if app.ws.PaneMgr().Get("orch-1") == nil {
		t.Fatal("minimizing must retain the agent's pane")
	}
	if !app.subPaneProjections.Has("orch-1") {
		t.Fatal("minimizing must retain the chat projection")
	}
	if _, ok := app.ws.SessionsByAgent()["orch-1"]; app.ws.SessionsByAgent() != nil && !ok {
		t.Fatal("minimizing must keep the session owned by its agent")
	}

	app.paintShellFrame()
	if !app.shellSlotActive(shellLauncherSlot) {
		t.Fatal("with every agent minimized the launcher takes the content area")
	}
}

// A minimized agent must be reachable again, and restoring it has to bring
// back the tab and the conversation, not start something new.
func TestMinimizedAgentRestoresFromAgentsPanel(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 120, 40
	newMinimizeTestAgent(t, app, "orch-1")
	_ = app.handleAgentTabClose(0)

	items := app.agentSidebarItems()
	if len(items) != 2 || items[0].ID != sidebarItemNewAgent || items[1].ID != "agent:orch-1" {
		t.Fatalf("Agents panel must list the minimized agent: %#v", items)
	}
	if !strings.Contains(items[1].Detail, "minimized") {
		t.Fatalf("row should show it is minimized: %#v", items[1])
	}

	if cmd := app.handleSidebarActivate(items[1]); cmd != nil {
		cmd()
	}
	if app.AgentView.IsHidden("orch-1") {
		t.Fatal("activating a minimized agent must restore it")
	}
	if app.AgentView.ActiveID() != "orch-1" {
		t.Fatalf("restored agent should become active, got %q", app.AgentView.ActiveID())
	}
	if app.Win.TabByTitle(app.agentTabTitle("orch-1")) == nil {
		t.Fatal("restoring must reopen the agent tab")
	}
	app.paintShellFrame()
	if app.shellSlotActive(shellLauncherSlot) {
		t.Fatal("a restored agent must take the content area back from the launcher")
	}
}

// Terminating stays available and stays destructive: it cancels the turn and
// drops the agent for good.
func TestTerminateAgentCancelsAndRemoves(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 120, 40
	newMinimizeTestAgent(t, app, "orch-1")

	app.terminateAgent("orch-1")

	if app.ws.AgentMgr().Get("orch-1") != nil {
		t.Fatal("terminate must remove the agent")
	}
	if app.ws.PaneMgr().Get("orch-1") != nil {
		t.Fatal("terminate must drop the agent's pane")
	}
	if app.Win.TabCount() != 0 {
		t.Fatalf("terminate must close the tab, tabs=%d", app.Win.TabCount())
	}
}

// Minimizing a parent takes its sub-agents with it so a restored
// orchestrator comes back whole rather than half-surfaced.
func TestMinimizeHidesSubAgentsWithTheirOrchestrator(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 120, 40
	newMinimizeTestAgent(t, app, "orch-1")
	app.ws.AgentMgr().AddSubAgent("sub-1", "orch-1", "Research", "model", "researcher",
		workspace.NewAgentState("sub-1", nil, nil))

	app.minimizeAgent("orch-1")
	if !app.AgentView.IsHidden("sub-1") {
		t.Fatal("sub-agents must minimize with their orchestrator")
	}
	if app.AgentView.VisibleCount() != 0 {
		t.Fatalf("nothing should stay surfaced, visible=%d", app.AgentView.VisibleCount())
	}

	app.restoreAgent("orch-1")
	if app.AgentView.IsHidden("sub-1") {
		t.Fatal("restoring an orchestrator must restore its sub-agents")
	}
}

// A resumed session hydrates completed children as hidden review surfaces.
// Restoring the root must not open every historical review in the tab bar.
func TestRestoreRootPreservesArchivedChildrenAndRootTabOrder(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 120, 40
	app.AddOrchestrator("root")
	for i := 0; i < 24; i++ {
		id := fmt.Sprintf("archived-%d", i)
		child := app.ws.AgentMgr().AddSubAgent(id, "root", "Review", "model", "researcher",
			workspace.NewAgentState(id, nil, nil))
		app.MinimizeAgent(child.ID) // hydrated completed sub-agent
		child.State.Completed = true
	}
	live := app.ws.AgentMgr().AddSubAgent("live", "root", "Live", "model", "researcher",
		workspace.NewAgentState("live", nil, nil))
	app.syncAgentWindowTabs()
	app.minimizeAgent("root")
	app.restoreAgent("root")

	if app.AgentView.IsHidden(live.ID) || app.AgentView.IsHidden("live") {
		t.Fatal("the previously open live child should return with its root")
	}
	if app.Win.TabCount() != 2 {
		t.Fatalf("restoring root opened archived reviews: tabs=%d want 2", app.Win.TabCount())
	}
	tabs := app.Win.Tabs()
	if tabs[0].Title != app.agentTabTitle("root") || tabs[1].Title != app.agentTabTitle("live") {
		t.Fatalf("main agent must precede its live child: %+v", tabs)
	}
	for i := 0; i < 24; i++ {
		id := fmt.Sprintf("archived-%d", i)
		if !app.AgentView.IsHidden(id) || app.Win.TabByTitle(app.agentTabTitle(id)) != nil {
			t.Fatalf("archived review %q was reopened", id)
		}
	}
	app.syncAgentWindowTabs() // background sync must preserve the same projection
	if app.Win.TabCount() != 2 {
		t.Fatalf("background sync reopened archived reviews: tabs=%d", app.Win.TabCount())
	}
	painted := paintedTabStrip(t, app, 120)
	rootPos, livePos := strings.Index(painted, "Main Agent"), strings.Index(painted, "Live")
	if rootPos < 0 || livePos <= rootPos || strings.Contains(painted, "Review") {
		t.Fatalf("painted bar reordered main/child or showed archived reviews: %q", painted)
	}
}

func TestRestoreChildOfMinimizedRootOnlyOpensItsPath(t *testing.T) {
	app := newFullTestApp(t)
	app.AddOrchestrator("root")
	for _, id := range []string{"review", "sibling"} {
		child := app.ws.AgentMgr().AddSubAgent(id, "root", id, "model", "researcher",
			workspace.NewAgentState(id, nil, nil))
		app.MinimizeAgent(child.ID)
		child.State.Completed = true
	}
	app.minimizeAgent("root")
	app.restoreAgent("review")
	if app.AgentView.IsHidden("root") || app.AgentView.IsHidden("review") || !app.AgentView.IsHidden("sibling") {
		t.Fatalf("restore child path lost visibility choices: root=%v review=%v sibling=%v",
			app.AgentView.IsHidden("root"), app.AgentView.IsHidden("review"), app.AgentView.IsHidden("sibling"))
	}
	if tabs := app.Win.Tabs(); len(tabs) != 2 || tabs[0].Title != app.agentTabTitle("root") ||
		tabs[1].Title != app.agentTabTitle("review") || app.AgentView.ActiveID() != "review" {
		t.Fatalf("restore child path opened sibling or lost selection: tabs=%+v active=%q", tabs, app.AgentView.ActiveID())
	}
}

func TestRestoreNestedReviewRevealsOnlyItsAncestors(t *testing.T) {
	app := newFullTestApp(t)
	app.AddOrchestrator("root")
	app.ws.AgentMgr().AddSubAgent("parent", "root", "parent", "model", "researcher",
		workspace.NewAgentState("parent", nil, nil))
	app.ws.AgentMgr().AddSubAgent("review", "parent", "review", "model", "researcher",
		workspace.NewAgentState("review", nil, nil))
	sibling := app.ws.AgentMgr().AddSubAgent("sibling", "parent", "sibling", "model", "researcher",
		workspace.NewAgentState("sibling", nil, nil))
	app.MinimizeAgent(sibling.ID)
	app.minimizeAgent("parent")
	app.minimizeAgent("root")
	app.restoreAgent("review")
	if got := app.AgentView.TabOrder(); !slices.Equal(got, []string{"root", "parent", "review"}) {
		t.Fatalf("nested restore opened siblings or lost an ancestor: %v", got)
	}
	if app.AgentView.ActiveID() != "review" || app.Win.TabCount() != 3 ||
		app.Win.ActiveTab().Title != app.agentTabTitle("review") {
		t.Fatalf("nested review not selected: active=%q tabs=%+v", app.AgentView.ActiveID(), app.Win.Tabs())
	}
}

func TestResumeSessionWithManyCompletedChildrenOnlyOpensMainAgent(t *testing.T) {
	app, ws := newRealShellApp(t)
	defer ws.Close()
	if cmd := app.launcherNewSession(); cmd != nil {
		_ = cmd()
	}
	rootID := app.AgentView.ActiveID()
	if rootID == "" || app.ActiveSession == nil {
		t.Fatal("failed to create session for resume")
	}
	owner := app.ActiveSession
	sessionID := owner.SessionID
	// Empty sessions are discarded by shutdown. Keep a real backend context
	// boundary so this fixture takes the same resume path as a worked session.
	if err := app.ws.BackendSession(owner.SessionID).AppendWorkspaceHistoryEvent("Fixture continuity", "Continuity saved"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 40; i++ {
		id := fmt.Sprintf("archived-%d", i)
		if err := sessiondb.InsertSubAgentResult(owner.DB, sessiondb.SubAgentResultRow{
			ParentSessionID: sessionID, ChildSessionID: id, AgentID: id,
			AgentName: "Review " + id, AgentCategory: "researcher", Status: "completed",
			SummaryGist: "Review complete", CreatedAt: time.Now().Add(time.Duration(i) * time.Millisecond).UnixNano(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	app.cleanupAgent(rootID)
	wsOf(app).CloseAllSessions()
	// A closed backend session and its in-memory root tree are separate:
	// restarting the UI starts with an empty agent manager and tab strip.
	app.ws.AgentMgr().Remove(rootID)
	app.Win.CloseAll()
	result := app.ws.OpenAgentSession(sessionID)
	if result == nil || result.Error != "" {
		t.Fatalf("resume failed: %+v", result)
	}
	app.AddOrchestrator(result.OrchID)
	if app.AgentView.ActiveID() == "" || app.Win.TabCount() != 1 ||
		app.Win.ActiveTab().Title != app.agentTabTitle(app.AgentView.ActiveID()) {
		t.Fatalf("resumed main agent did not own its only tab: active=%q tabs=%+v",
			app.AgentView.ActiveID(), app.Win.Tabs())
	}
	if app.ws.AgentMgr().Count() != 41 {
		t.Fatalf("lost child reviews on resume: count=%d", app.ws.AgentMgr().Count())
	}
	for i := 0; i < 40; i++ {
		id := fmt.Sprintf("archived-%d", i)
		if !app.AgentView.IsHidden(id) || app.Win.TabByTitle(app.agentTabTitle(id)) != nil {
			t.Fatalf("resumed child %q opened a tab", id)
		}
	}
	app.syncAgentWindowTabs()
	if app.Win.TabCount() != 1 {
		t.Fatalf("background sync reopened child tabs: %+v", app.Win.Tabs())
	}
	app.restoreAgent("archived-20")
	if app.Win.TabCount() != 2 || app.AgentView.ActiveID() != "archived-20" ||
		app.Win.Tabs()[0].Title != app.agentTabTitle(app.ws.AgentMgr().OrchestratorFor("archived-20").ID) {
		t.Fatalf("explicit review did not open just one child after root: %+v", app.Win.Tabs())
	}
}

// A minimized agent must not be resurrected as a tab by the next tab sync.
func TestTabSyncDoesNotResurrectMinimizedAgents(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 120, 40
	newMinimizeTestAgent(t, app, "orch-1")
	app.minimizeAgent("orch-1")

	for i := 0; i < 3; i++ {
		app.syncAgentWindowTabs()
		app.paintShellFrame()
	}
	if app.Win.TabCount() != 0 {
		t.Fatalf("minimized agent reappeared as a tab, tabs=%d", app.Win.TabCount())
	}
	if app.AgentTabViews() != nil && len(app.AgentTabViews()) != 0 {
		t.Fatalf("minimized agent must not appear in tab views: %#v", app.AgentTabViews())
	}
}
