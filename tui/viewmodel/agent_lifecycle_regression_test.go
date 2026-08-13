package viewmodel

import (
	"testing"

	"crude/provider"
	ttypes "crude/tui/types"
	"crude/workspace"
)

func TestClosingOrchestratorTabCleansEntireAgentSubtree(t *testing.T) {
	app := newFullTestApp(t)
	app.AddOrchestrator(
		"root",
		make(chan provider.UserInput, 1),
		make(chan provider.UserInput, 1),
		make(chan struct{}, 1),
		make(chan string, 1),
	)

	childPane := app.PaneMgr.GetOrCreate("child", workspace.PaneAgent, "child")
	app.subPaneProjections.Get("child", workspace.PaneAgent)
	child := app.AgentMgr.AddSubAgent(
		"child", "root", "Researcher", "child-model", "researcher",
		workspace.NewAgentState("child", childPane.Messages, nil),
	)
	if child == nil {
		t.Fatal("failed to create child agent")
	}

	// Both idle runtime agents have status entries, so the precondition
	// mirrors the sleeping-agent count shown after work settles.
	app.StatusBar.SetThinkingForAgent("root", false, false, false)
	app.StatusBar.SetThinkingForAgent("child", false, false, false)
	if got := app.StatusBar.SleepingAgentCount(); got != 2 {
		t.Fatalf("sleeping count before close=%d, want 2", got)
	}

	app.syncAgentWindowTabs()
	if app.Win.TabByTitle(app.agentTabTitle("child")) == nil {
		t.Fatal("child tab missing before orchestrator close")
	}

	rootTab := app.Win.TabByTitle(app.agentTabTitle("root"))
	if rootTab == nil {
		t.Fatal("root tab missing before close")
	}
	app.Win.CloseTabByID(rootTab.ID)
	app.cleanupAgent("root")
	app.handleTabSwitch()

	if got := app.AgentMgr.Count(); got != 0 {
		t.Fatalf("agent manager retained %d nodes after root close", got)
	}
	for _, id := range []string{"root", "child"} {
		if app.PaneMgr.Has(id) {
			t.Errorf("pane %q survived subtree close", id)
		}
		if app.subPaneProjections.Has(id) {
			t.Errorf("projection %q survived subtree close", id)
		}
		if app.Win.TabByTitle(app.agentTabTitle(id)) != nil {
			t.Errorf("tab %q survived subtree close", id)
		}
	}
	if got := app.StatusBar.SleepingAgentCount(); got != 0 {
		t.Fatalf("sleeping count after close=%d, want 0", got)
	}

	items := app.agentSidebarItems()
	if len(items) != 1 || items[0].Selectable() || items[0].Label != "No agents" {
		t.Fatalf("Agents panel disagrees with empty lifecycle state: %#v", items)
	}
}

func TestLateSubAgentStartAfterParentCloseIsDropped(t *testing.T) {
	app := newFullTestApp(t)
	app.AddOrchestrator(
		"root",
		make(chan provider.UserInput, 1),
		nil,
		make(chan struct{}, 1),
		nil,
	)
	app.cleanupAgent("root")

	app.handleAgentEvent(ttypes.AgentEventMsg{
		Type:    "sub_agent_started",
		AgentID: "late-child",
		Data: map[string]string{
			"agent_id":   "late-child",
			"agent_name": "Late child",
			"parent_id":  "root",
		},
	})

	if app.AgentMgr.Get("late-child") != nil {
		t.Fatal("late child was added without a live parent")
	}
	if app.PaneMgr.Has("late-child") {
		t.Fatal("late child left an orphan pane")
	}
	if app.subPaneProjections.Has("late-child") {
		t.Fatal("late child left an orphan projection")
	}
	if got := app.StatusBar.SleepingAgentCount(); got != 0 {
		t.Fatalf("late child created a ghost sleeping count: %d", got)
	}
}
