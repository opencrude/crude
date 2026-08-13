package viewmodel

import (
	"strings"
	"testing"

	"crude/provider"
	"crude/workspace"
)

func newMinimizeTestAgent(t *testing.T, app *App, id string) chan struct{} {
	t.Helper()
	cancel := make(chan struct{}, 1)
	app.AddOrchestrator(
		id,
		make(chan provider.UserInput, 8),
		make(chan provider.UserInput, 8),
		cancel,
		make(chan string, 1),
	)
	return cancel
}

// Closing an agent tab must not stop the agent. The orchestrator keeps
// running, the conversation is retained, and the tab is the only thing that
// goes away — otherwise a stray click destroys work in flight.
func TestClosingAgentTabMinimizesWithoutCancelling(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 120, 40
	cancel := newMinimizeTestAgent(t, app, "orch-1")

	if cmd := app.handleAgentTabClose(0); cmd != nil {
		t.Fatal("closing an agent tab should not return a command")
	}

	select {
	case <-cancel:
		t.Fatal("closing a tab must not cancel the running turn")
	default:
	}
	node := app.AgentMgr.Get("orch-1")
	if node == nil {
		t.Fatal("closing a tab must not remove the agent")
	}
	if !node.Hidden {
		t.Fatal("closed agent should be minimized")
	}
	if app.AgentMgr.VisibleCount() != 0 || app.Win.TabCount() != 0 {
		t.Fatalf("tab should be gone: visible=%d tabs=%d", app.AgentMgr.VisibleCount(), app.Win.TabCount())
	}
	if app.PaneMgr.Get("orch-1") == nil {
		t.Fatal("minimizing must retain the agent's pane")
	}
	if !app.subPaneProjections.Has("orch-1") {
		t.Fatal("minimizing must retain the chat projection")
	}
	if _, ok := app.SessionsByAgent["orch-1"]; app.SessionsByAgent != nil && !ok {
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
	if len(items) != 1 || items[0].ID != "agent:orch-1" {
		t.Fatalf("Agents panel must list the minimized agent: %#v", items)
	}
	if !strings.Contains(items[0].Detail, "minimized") {
		t.Fatalf("row should show it is minimized: %#v", items[0])
	}

	if cmd := app.handleSidebarActivate(items[0]); cmd != nil {
		cmd()
	}
	if app.AgentMgr.IsHidden("orch-1") {
		t.Fatal("activating a minimized agent must restore it")
	}
	if app.AgentMgr.ActiveID() != "orch-1" {
		t.Fatalf("restored agent should become active, got %q", app.AgentMgr.ActiveID())
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
	cancel := newMinimizeTestAgent(t, app, "orch-1")

	app.terminateAgent("orch-1")

	select {
	case <-cancel:
	default:
		t.Fatal("terminate must cancel the running turn")
	}
	if app.AgentMgr.Get("orch-1") != nil {
		t.Fatal("terminate must remove the agent")
	}
	if app.PaneMgr.Get("orch-1") != nil {
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
	app.AgentMgr.AddSubAgent("sub-1", "orch-1", "Research", "model", "researcher",
		workspace.NewAgentState("sub-1", nil, nil))

	app.minimizeAgent("orch-1")
	if !app.AgentMgr.IsHidden("sub-1") {
		t.Fatal("sub-agents must minimize with their orchestrator")
	}
	if app.AgentMgr.VisibleCount() != 0 {
		t.Fatalf("nothing should stay surfaced, visible=%d", app.AgentMgr.VisibleCount())
	}

	app.restoreAgent("orch-1")
	if app.AgentMgr.IsHidden("sub-1") {
		t.Fatal("restoring an orchestrator must restore its sub-agents")
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
