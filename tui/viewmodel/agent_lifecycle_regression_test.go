package viewmodel

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"crude/agent"
	"crude/tui/chatprojection"
	"crude/types"
	"crude/workspace"
)

func TestCancelledAgentEnvelopeRetainsLateSemanticEvidenceWithoutReactivation(t *testing.T) {
	for _, owner := range []string{"root", "child"} {
		for _, phase := range []string{"cancelling", "terminal"} {
			t.Run(owner+"/"+phase, func(t *testing.T) {
				app := newFullTestApp(t)
				wsOf(app).SessionsByAgent = map[string]*workspace.Session{"root": {SessionID: "root", AgentID: "root"}}
				app.AddOrchestrator("root")
				if owner == "child" {
					pane := app.ws.PaneMgr().GetOrCreate("child", workspace.PaneAgent, "child")
					app.ws.AgentMgr().AddSubAgent("child", "root", "Child", "model", "researcher", workspace.NewAgentState("child", pane.Messages, app.ws.Logger()))
				}
				state := app.ws.AgentMgr().Get(owner).State
				sequence := uint64(0)
				deliver := func(kind string, data any) agent.AgentEvent {
					sequence++
					event := agent.AgentEvent{Type: kind, Data: data, AgentID: owner, Identity: types.RuntimeEventIdentity{
						EventID: fmt.Sprintf("%s-%d", owner, sequence), OwnerID: owner, AgentID: owner,
						RunID: "exact-run", RuntimeEpoch: 1, Generation: 1, Sequence: sequence,
					}}
					_, _ = app.Update(event)
					return event
				}
				deliver("turn_started", types.TurnIdentity{Generation: 1, AccountID: "account", ProviderID: "provider", ModelID: "model"})
				deliver("tool_call", map[string]any{"tool_id": "tool", "internal_id": "call", "name": "read_file", "args": `{"path":"evidence.txt"}`})
				state.BeginCancellation()
				if phase == "terminal" {
					deliver("done", map[string]any{"reason": "cancelled"})
					if owner == "child" {
						state.Completed, state.Status = true, "cancelled"
					}
				}
				deliver("tool_result", map[string]any{"tool_id": "tool", "internal_id": "call", "name": "read_file", "result": "late exact tool evidence"})
				last := deliver("stream_token", "late exact stream evidence")
				wantPhase := types.AgentCancelling
				if phase == "terminal" {
					wantPhase = types.AgentCancelled
				}
				assertStatus := func() {
					t.Helper()
					if status, ok := app.ws.Status().Agent(owner); !ok || status.Phase != wantPhase || status.NeedsAction {
						t.Errorf("late evidence changed terminal status before/after inventory: got=%+v want=%s", status, wantPhase)
					}
				}
				assertStatus()
				deliver("phase", map[string]any{"name": "waiting_provider"})
				assertStatus()
				app.syncAgentStatusInventory()
				assertStatus()
				state.Tick(1000)
				tool, stream := false, false
				for _, message := range state.Messages.Messages() {
					tool = tool || message.Role == workspace.MessageRoleTool && message.Content == "late exact tool evidence"
					stream = stream || message.Role == workspace.MessageRoleAssistant && strings.Contains(message.Content, "late exact stream evidence")
				}
				if !tool || !stream {
					t.Errorf("late semantic evidence disappeared: tool=%v stream=%v", tool, stream)
				}
				if state.IsActive() || state.Messages.HasPendingToolLifecycles() {
					t.Errorf("late semantic evidence reactivated stopped animation: running=%v thinking=%v streaming=%v pendingTools=%v",
						state.Running, state.Thinking, state.Streaming, state.Messages.HasPendingToolLifecycles())
				}
				version := state.Messages.Version()
				_, _ = app.Update(last)
				last.Identity.EventID, last.Identity.OwnerID, last.Identity.Sequence = "foreign", "foreign-owner", sequence+1
				_, _ = app.Update(last)
				if state.Messages.Version() != version {
					t.Fatal("duplicate/foreign semantic envelope changed stopped evidence")
				}
				next := agent.AgentEvent{Type: "turn_started", AgentID: owner,
					Data:     types.TurnIdentity{Generation: 2, AccountID: "account", ProviderID: "provider", ModelID: "model"},
					Identity: types.RuntimeEventIdentity{EventID: owner + "-next", OwnerID: owner, AgentID: owner, RunID: "next-run", RuntimeEpoch: 1, Generation: 2, Sequence: sequence + 1},
				}
				_, _ = app.Update(next)
				next.Type, next.Data = "phase", map[string]any{"name": "waiting_provider"}
				next.Identity.EventID, next.Identity.Sequence = owner+"-next-phase", sequence+2
				_, _ = app.Update(next)
				if !state.IsActive() || state.Completed || state.TurnIdentity.Generation != 2 {
					t.Fatalf("genuine next generation stayed terminal: active=%v completed=%v generation=%d", state.IsActive(), state.Completed, state.TurnIdentity.Generation)
				}
			})
		}
	}
}

func TestSubAgentTerminalStatusIsNamedRoleAwareAndForcesCachedFooterFrame(t *testing.T) {
	for _, tc := range []struct{ status, want string }{
		{"completed", "✓ Samuel sub Rebecca · Researcher completed"},
		{"cancelled", "○ Samuel sub Rebecca · Researcher cancelled"},
		{"crashed", "✕ Samuel sub Rebecca · Researcher crashed"},
		{"failed_policy", "✕ Samuel sub Rebecca · Researcher blocked by policy"},
	} {
		t.Run(tc.status, func(t *testing.T) {
			app := newFullTestApp(t)
			app.width, app.height = 120, 30
			app.AddOrchestrator("root")
			pane := app.ws.PaneMgr().GetOrCreate("child", workspace.PaneAgent, "child")
			app.ws.AgentMgr().AddSubAgent("child", "root", "Samuel sub Rebecca", "model", "researcher", workspace.NewAgentState("child", pane.Messages, app.ws.Logger()))
			app.cachedOutput, app.frameGate.deferred, app.frameGate.due = "cached", true, false
			app.handleSubAgentCompleted(workspace.AgentEvent{Type: "sub_agent_completed", Data: map[string]string{
				"agent_id": "child", "agent_name": "Samuel sub Rebecca", "category": "researcher", "status": tc.status,
			}})
			if app.shouldDeferFrame() {
				t.Fatal("terminal status remained hidden behind the presentation cache")
			}
			notice, ok := app.ws.Status().LatestTransient(time.Now(), 5*time.Second)
			if !ok || notice.Summary != tc.want || notice.Owner.ID != "child" || notice.Owner.ParentID != "root" {
				t.Fatalf("terminal notice=%+v, ok=%v", notice, ok)
			}
			app.paintShellFrame()
			if footer := rowText(app, app.height-1); !strings.Contains(footer, tc.want) || strings.Contains(footer, "✓ done") {
				t.Fatalf("footer did not paint deliberate terminal status: %q", footer)
			}
		})
	}
}

func TestClosingOrchestratorTabCleansEntireAgentSubtree(t *testing.T) {
	app := newFullTestApp(t)
	app.AddOrchestrator(
		"root")

	childPane := app.ws.PaneMgr().GetOrCreate("child", workspace.PaneAgent, "child")
	app.subPaneProjections.Get("child", workspace.PaneAgent)
	child := app.ws.AgentMgr().AddSubAgent(
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
	if app.Win.TabByTitle(app.agentTabTitle("child")) != nil {
		t.Fatal("a sub-agent opened a tab without being opened")
	}
	app.RestoreAgent("child")
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

	if got := app.ws.AgentMgr().Count(); got != 0 {
		t.Fatalf("agent manager retained %d nodes after root close", got)
	}
	for _, id := range []string{"root", "child"} {
		if app.ws.PaneMgr().Has(id) {
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

	app.handleAgentEvent(types.AgentEventMsg{
		Type:    "sub_agent_started",
		AgentID: "late-child",
		Data: map[string]string{
			"agent_id":   "late-child",
			"agent_name": "Late child",
			"parent_id":  "root",
		},
	})

	if app.ws.AgentMgr().Get("late-child") != nil {
		t.Fatal("late child was added without a live parent")
	}
	if app.ws.PaneMgr().Has("late-child") {
		t.Fatal("late child left an orphan pane")
	}
	if app.subPaneProjections.Has("late-child") {
		t.Fatal("late child left an orphan projection")
	}
	if got := testIdleAgentCount(app); got != 0 {
		t.Fatalf("late child created a ghost idle count: %d", got)
	}
}

func TestRetryEventReachesCanonicalTranscript(t *testing.T) {
	app := newFullTestApp(t)
	app.AddOrchestrator("root")
	for _, attempt := range []int{1, 2} {
		app.handleAgentEvent(types.AgentEventMsg{AgentID: "root", Type: "retry", Data: map[string]any{"attempt": attempt, "limit": 6, "backoff": float64(3), "reason": "Provider temporarily unavailable"}})
	}
	messages := app.ws.AgentMgr().Get("root").State.Messages.Messages()
	count := 0
	for _, message := range messages {
		if message.LifecyclePhase == "retrying" {
			count++
			if message.LifecycleRetryAttempt != 3 {
				t.Fatalf("stale retry attempt: %+v", message)
			}
		}
	}
	if count != 1 {
		t.Fatalf("retry event swallowed or duplicated: %+v", messages)
	}
}

func TestSubAgentTerminalEventCannotRecreateGhostIdleStatus(t *testing.T) {
	app := newFullTestApp(t)
	app.AddOrchestrator("root")
	baselineActive := testActiveAgentCount(app)
	baselineIdle := testIdleAgentCount(app)
	app.handleAgentEvent(types.AgentEventMsg{
		Type:    "sub_agent_started",
		AgentID: "child",
		Data: map[string]string{
			"agent_id": "child", "agent_name": "Child", "parent_id": "root",
		},
	})
	if got := testActiveAgentCount(app); got != baselineActive+1 {
		t.Fatalf("active count before completion=%d, want %d", got, baselineActive+1)
	}

	app.handleAgentEvent(types.AgentEventMsg{
		Type:    "sub_agent_completed",
		AgentID: "child",
		Data: map[string]string{
			"agent_id": "child", "agent_name": "Child", "status": "cancelled",
		},
	})

	child := app.ws.AgentMgr().Get("child")
	if child == nil || child.State == nil || !child.State.Completed || child.State.Status != "cancelled" {
		t.Fatalf("terminal child state = %#v", child)
	}
	if got := testIdleAgentCount(app); got != baselineIdle {
		t.Fatalf("terminal child recreated ghost idle status: got %d want %d", got, baselineIdle)
	}
}

func TestFailedSubAgentParentRowAndReviewPaneRemainAuditable(t *testing.T) {
	app := newFullTestApp(t)
	app.AddOrchestrator("root")
	root := app.ws.AgentMgr().Get("root").State.Messages
	root.UpdateToolCall("spawn-1", "tool-1", "agent_start", "category: researcher")
	app.handleAgentEvent(types.AgentEventMsg{Type: "sub_agent_started", AgentID: "child", Data: map[string]string{
		"agent_id": "child", "agent_name": "Lily", "category": "researcher", "task": "Inspect recovery", "parent_id": "root", "parent_tool_id": "spawn-1",
	}})
	parent := root.Messages()
	if len(parent) != 1 || parent[0].SubPaneID != "child" || parent[0].ToolProgressLabel != "Lily · Researcher — Inspect recovery" {
		t.Fatalf("running child identity not in parent row: %+v", parent)
	}
	if pane := app.ws.PaneMgr().Get("child"); pane == nil || pane.Title != "Lily · Researcher" {
		t.Fatalf("running subpane title omitted the agent type: %+v", pane)
	}
	app.handleAgentEvent(types.AgentEventMsg{Type: "sub_agent_completed", AgentID: "child", Data: map[string]string{
		"agent_id": "child", "agent_name": "Lily", "status": "failed_error",
	}})
	pane := app.ws.PaneMgr().Get("child")
	if pane == nil || !pane.Done || pane.Status != "failed_error" {
		t.Fatalf("failed child pane = %+v", pane)
	}
	view := pane.View()
	if view.Summary == nil || !strings.Contains(chatprojection.BuildSubPaneSummaryLine(view, 80).PlainText(), "× Lily") ||
		!strings.Contains(chatprojection.BuildSubPaneSummaryLine(view, 80).PlainText(), " — failed") {
		t.Fatalf("failed child summary hidden: %+v", view)
	}
}

func TestParallelSubAgentStartsLinkToTheirOwnParentCalls(t *testing.T) {
	app := newFullTestApp(t)
	app.AddOrchestrator("root")
	parent := app.ws.AgentMgr().Get("root").State.Messages
	parent.UpdateToolCall("first", "first-msg", "agent_start", "description: inspect")
	parent.UpdateToolCall("second", "second-msg", "agent_start", "description: test")
	for _, start := range []struct{ id, name, call string }{
		{"child-two", "Jane", "second"}, {"child-one", "Lily", "first"},
	} {
		app.handleAgentEvent(types.AgentEventMsg{Type: "sub_agent_started", AgentID: start.id, Data: map[string]string{
			"agent_id": start.id, "agent_name": start.name, "parent_id": "root", "parent_tool_id": start.call,
		}})
	}
	rows := parent.Messages()
	if len(rows) != 2 || rows[0].SubPaneID != "child-one" || rows[1].SubPaneID != "child-two" {
		t.Fatalf("parallel child links crossed: %+v", rows)
	}
}

func TestCompletedSubAgentCanBeReopenedForReview(t *testing.T) {
	app := newFullTestApp(t)
	app.AddOrchestrator("root")
	const assignment = "# Assignment\n- Description: inspect lifecycle"
	app.handleAgentEvent(types.AgentEventMsg{
		Type:    "sub_agent_started",
		AgentID: "child",
		Data: map[string]string{
			"agent_id": "child", "agent_name": "Child", "parent_id": "root", "prompt": assignment,
		},
	})
	if app.Win.TabByTitle(app.agentTabTitle("child")) != nil {
		t.Fatal("a started sub-agent opened a tab by itself")
	}
	app.restoreAgent("child")
	if app.Win.TabByTitle(app.agentTabTitle("child")) == nil {
		t.Fatal("opening the live child did not create its tab")
	}
	pane := app.ws.PaneMgr().Get("child")
	projection := app.subPaneProjections.GetExisting("child")

	app.handleAgentEvent(types.AgentEventMsg{
		Type:    "sub_agent_completed",
		AgentID: "child",
		Data: map[string]string{
			"agent_id": "child", "agent_name": "Child", "status": "completed",
		},
	})
	child := app.ws.AgentMgr().Get("child")
	if child == nil || !child.IsCompleted() || !app.AgentView.IsHidden(child.ID) {
		t.Fatalf("completed child was not archived: %#v", child)
	}
	if app.Win.TabByTitle(app.agentTabTitle("child")) != nil {
		t.Fatal("completed child tab remained surfaced instead of archiving")
	}
	if app.ws.PaneMgr().Get("child") != pane || app.subPaneProjections.GetExisting("child") != projection {
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
	if app.AgentView.IsHidden(child.ID) || app.AgentView.ActiveID() != "child" {
		t.Fatalf("completed child did not become active review: hidden=%v active=%q", app.AgentView.IsHidden(child.ID), app.AgentView.ActiveID())
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
	pane := app.ws.PaneMgr().GetOrCreate("child", workspace.PaneAgent, "child")
	childState := workspace.NewAgentState("child", pane.Messages, app.ws.Logger())
	app.ws.AgentMgr().AddSubAgent("child", "root", "child", "model", "researcher", childState)
	app.switchToAgentView("child")
	if app.activeAgentTabID() != "child" {
		t.Fatal("child was not the visible prompt target")
	}

	app.completeSubAgent("child")
	active := app.Win.ActiveTab()
	if active == nil || active.Title != app.agentTabTitle("root") || app.AgentView.ActiveID() != "root" || app.activeAgentTabID() != "root" {
		t.Fatalf("completion left split active state: tab=%#v manager=%q prompt=%q",
			active, app.AgentView.ActiveID(), app.activeAgentTabID())
	}
}

func TestUsagePaneFollowsRequestRevisionsWithoutDoubleCounting(t *testing.T) {
	app := newFullTestApp(t)
	wsOf(app).SessionsByAgent = map[string]*workspace.Session{"root": workspace.NewSession("session", "", nil, nil)}
	app.AddOrchestrator("root")
	emit := func(prompt, completion any, reported int64) {
		app.handleAgentEvent(types.AgentEventMsg{AgentID: "root", Type: "usage", Data: map[string]any{
			"request_id": "request", "reported_at": reported, "prompt": prompt, "completion": completion,
		}})
	}
	emit(int64(90), float64(10), 1)
	emit(int64(90), float64(10), 1)
	if got := app.ws.PaneMgr().Get("root").TokenCount; got != 100 {
		t.Fatalf("duplicate event inflated pane telemetry: %d", got)
	}
	emit(90, 20, 2)
	emit(90, 999, 1)
	if got := app.ws.PaneMgr().Get("root").TokenCount; got != 110 {
		t.Fatalf("pane did not follow authoritative request revision: %d", got)
	}
}

func TestDangerousBackgroundAgentAlertIsVisibleWithoutChangingSelection(t *testing.T) {
	app, ws := newRealShellApp(t)
	defer ws.Close()
	app.AddOrchestrator("root")
	pane := app.ws.PaneMgr().GetOrCreate("child", workspace.PaneAgent, "child")
	childState := workspace.NewAgentState("child", pane.Messages, app.ws.Logger())
	app.ws.AgentMgr().AddSubAgent("child", "root", "child", "model", "crafter", childState)
	app.MinimizeAgent("child")
	path := openEditorTabs(t, app, "main.go")[0]

	app.handleAgentEvent(types.AgentEventMsg{
		Type: "safety_alert", AgentID: "child",
		Data: map[string]any{"risk": "blocked", "reason": "Blocked disk write"},
	})
	app.paintShellFrame()
	child := app.ws.AgentMgr().Get("child")
	if !app.AgentView.IsHidden(child.ID) || app.AgentView.ActiveID() == "child" || app.EditorState.ActiveView != path {
		t.Fatalf("background alert changed navigation: hidden=%v active=%q editor=%q", app.AgentView.IsHidden(child.ID), app.AgentView.ActiveID(), app.EditorState.ActiveView)
	}
	assertEditorStillPainted(t, app, path)
	if status := testStatusText(app); !strings.Contains(status, "Blocked disk write") {
		t.Fatalf("danger alert not visible in global status: %q", status)
	}
}
