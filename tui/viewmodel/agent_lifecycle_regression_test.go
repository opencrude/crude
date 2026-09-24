package viewmodel

import (
	"strings"
	"testing"

	ttypes "crude/tui/types"
	"crude/workspace"
)

func TestClosingOrchestratorTabCleansEntireAgentSubtree(t *testing.T) {
	app := newFullTestApp(t)
	app.AddOrchestrator(
		"root")

	childPane := app.PaneMgr.GetOrCreate("child", workspace.PaneAgent, "child")
	app.subPaneProjections.Get("child", workspace.PaneAgent)
	child := app.AgentMgr.AddSubAgent(
		"child", "root", "Researcher", "child-model", "researcher",
		workspace.NewAgentState("child", childPane.Messages, nil),
	)
	if child == nil {
		t.Fatal("failed to create child agent")
	}
	app.syncAgentStatusInventory()

	// Both idle runtime agents have status entries, so the precondition
	// mirrors the aggregate idle-agent count shown after work settles.
	if got := testIdleAgentCount(app); got != 2 {
		t.Fatalf("idle count before close=%d, want 2", got)
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
	if got := testIdleAgentCount(app); got != 0 {
		t.Fatalf("idle count after close=%d, want 0", got)
	}

	items := app.agentSidebarItems()
	if len(items) != 2 || items[0].ID != sidebarItemNewAgent || items[1].Label != "No agents" {
		t.Fatalf("Agents panel disagrees with empty lifecycle state: %#v", items)
	}
}

func TestLateSubAgentStartAfterParentCloseIsDropped(t *testing.T) {
	app := newFullTestApp(t)
	app.AddOrchestrator(
		"root")

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
	if got := testIdleAgentCount(app); got != 0 {
		t.Fatalf("late child created a ghost idle count: %d", got)
	}
}

func TestSubAgentTerminalEventCannotRecreateGhostIdleStatus(t *testing.T) {
	app := newFullTestApp(t)
	app.AddOrchestrator("root")
	baselineActive := testActiveAgentCount(app)
	baselineIdle := testIdleAgentCount(app)
	app.handleAgentEvent(ttypes.AgentEventMsg{
		Type:    "sub_agent_started",
		AgentID: "child",
		Data: map[string]string{
			"agent_id": "child", "agent_name": "Child", "parent_id": "root",
		},
	})
	if got := testActiveAgentCount(app); got != baselineActive+1 {
		t.Fatalf("active count before completion=%d, want %d", got, baselineActive+1)
	}

	app.handleAgentEvent(ttypes.AgentEventMsg{
		Type:    "sub_agent_completed",
		AgentID: "child",
		Data: map[string]string{
			"agent_id": "child", "agent_name": "Child", "status": "cancelled",
		},
	})

	child := app.AgentMgr.Get("child")
	if child == nil || child.State == nil || !child.State.Completed || child.State.Status != "cancelled" {
		t.Fatalf("terminal child state = %#v", child)
	}
	if got := testIdleAgentCount(app); got != baselineIdle {
		t.Fatalf("terminal child recreated ghost idle status: got %d want %d", got, baselineIdle)
	}
}

func TestCompletedSubAgentCanBeReopenedForReview(t *testing.T) {
	app := newFullTestApp(t)
	app.AddOrchestrator("root")
	const assignment = "# Assignment\n- Description: inspect lifecycle"
	app.handleAgentEvent(ttypes.AgentEventMsg{
		Type:    "sub_agent_started",
		AgentID: "child",
		Data: map[string]string{
			"agent_id": "child", "agent_name": "Child", "parent_id": "root", "prompt": assignment,
		},
	})
	if app.Win.TabByTitle(app.agentTabTitle("child")) == nil {
		t.Fatal("live child tab was not created")
	}
	pane := app.PaneMgr.Get("child")
	projection := app.subPaneProjections.GetExisting("child")

	app.handleAgentEvent(ttypes.AgentEventMsg{
		Type:    "sub_agent_completed",
		AgentID: "child",
		Data: map[string]string{
			"agent_id": "child", "agent_name": "Child", "status": "completed",
		},
	})
	child := app.AgentMgr.Get("child")
	if child == nil || !child.IsCompleted() || !child.Hidden {
		t.Fatalf("completed child was not archived: %#v", child)
	}
	if app.Win.TabByTitle(app.agentTabTitle("child")) != nil {
		t.Fatal("completed child tab remained surfaced instead of archiving")
	}
	if app.PaneMgr.Get("child") != pane || app.subPaneProjections.GetExisting("child") != projection {
		t.Fatal("completion discarded the retained review surface")
	}
	foundClosedReview := false
	for _, item := range app.agentSidebarItems() {
		if item.ID == "agent:child" && item.Icon == "✓" {
			foundClosedReview = true
		}
	}
	if !foundClosedReview {
		t.Fatal("completed child was not discoverable as a closed review in Agents")
	}

	app.restoreAgent("child")
	if child.Hidden || app.AgentMgr.ActiveID() != "child" {
		t.Fatalf("completed child did not become active review: hidden=%v active=%q", child.Hidden, app.AgentMgr.ActiveID())
	}
	if app.Win.TabByTitle(app.agentTabTitle("child")) == nil {
		t.Fatal("completed child review tab was not reopened")
	}
	app.paintShellFrame()
	if !app.inputRect.Empty() {
		t.Fatalf("completed review mounted a live composer: %+v", app.inputRect)
	}
	help := app.contextualHelpView()
	if strings.Contains(help, "Enter send") || !strings.Contains(help, "read-only agent review") {
		t.Fatalf("completed review advertised live input: %q", help)
	}
	messages := child.State.Messages.Messages()
	if len(messages) == 0 || messages[0].Role != workspace.MessageRoleAssignment || messages[0].Content != assignment {
		t.Fatalf("reopened review lost assignment: %+v", messages)
	}
}

func TestCompletingActiveChildSelectsVisibleRootAndPromptTarget(t *testing.T) {
	app := newFullTestApp(t)
	app.AddOrchestrator("root")
	pane := app.PaneMgr.GetOrCreate("child", workspace.PaneAgent, "child")
	childState := workspace.NewAgentState("child", pane.Messages, app.Logger)
	app.AgentMgr.AddSubAgent("child", "root", "child", "model", "researcher", childState)
	app.switchToAgentView("child")
	if app.activeAgentTabID() != "child" {
		t.Fatal("child was not the visible prompt target")
	}

	app.completeSubAgent("child")
	active := app.Win.ActiveTab()
	if active == nil || active.Title != app.agentTabTitle("root") || app.AgentMgr.ActiveID() != "root" || app.activeAgentTabID() != "root" {
		t.Fatalf("completion left split active state: tab=%#v manager=%q prompt=%q",
			active, app.AgentMgr.ActiveID(), app.activeAgentTabID())
	}
}

func TestUsageEventTokensAccumulatesProviderNumericForms(t *testing.T) {
	if got := usageEventTokens(map[string]any{"prompt": int64(90), "completion": float64(10)}); got != 100 {
		t.Fatalf("usage tokens=%d want 100", got)
	}
	if got := usageEventTokens(map[string]int{"prompt": 7, "completion": 3}); got != 10 {
		t.Fatalf("integer usage tokens=%d want 10", got)
	}
}

func TestDangerousBackgroundAgentAlertIsVisibleWithoutChangingSelection(t *testing.T) {
	app, ws := newRealShellApp(t)
	defer ws.Close()
	app.AddOrchestrator("root")
	pane := app.PaneMgr.GetOrCreate("child", workspace.PaneAgent, "child")
	childState := workspace.NewAgentState("child", pane.Messages, app.Logger)
	app.AgentMgr.AddSubAgent("child", "root", "child", "model", "crafter", childState)
	app.MinimizeAgent("child")
	path := openEditorTabs(t, app, "main.go")[0]

	app.handleAgentEvent(ttypes.AgentEventMsg{
		Type: "safety_alert", AgentID: "child",
		Data: map[string]any{"risk": "blocked", "reason": "Blocked disk write"},
	})
	app.paintShellFrame()
	child := app.AgentMgr.Get("child")
	if !child.Hidden || app.AgentMgr.ActiveID() == "child" || app.EditorState.ActiveView != path {
		t.Fatalf("background alert changed navigation: hidden=%v active=%q editor=%q", child.Hidden, app.AgentMgr.ActiveID(), app.EditorState.ActiveView)
	}
	assertEditorStillPainted(t, app, path)
	if status := testStatusText(app); !strings.Contains(status, "Blocked disk write") {
		t.Fatalf("danger alert not visible in global status: %q", status)
	}
}
