package viewmodel

import (
	"strings"
	"testing"
	"time"

	"crude/tui/types"
	ttypes "crude/types"
	"crude/types/activity"
	"crude/types/reasoning"
	"crude/workspace"
)

func TestManualCompactionShowsOperationStatusInsteadOfIdlePlan(t *testing.T) {
	app := newFullTestApp(t)
	wsOf(app).Logger = stubLogger{}
	id := addBackendTestOrchestrator(t, app)
	data := map[string]any{
		"request_id": "compact-status-1", "trigger": "manual", "role": "root",
		"model_id": "aeron/model", "stage": 2, "completed_stages": 1,
		"stage_count": 4, "stage_label": "Distilling work", "before_tokens": 510100,
	}
	plan := &activity.Plan{ID: "plan", Status: "active", Total: 4, Completed: 3,
		Current: []activity.Step{{Number: 4, Text: "Run build, tests, and whole-repo lint"}}}
	paint := func() string {
		app.syncAgentStatusInventory()
		app.configureShell()
		view := app.ws.AgentActivity(id)
		view.Plan = plan
		app.chatScreen.SetActivity(view)
		app.renderShellToSurface()
		return shellBufferRow(app, app.chatRect.Y+app.chatRect.H-1)
	}
	app.handleAgentEvent(types.AgentEventMsg{AgentID: id, Type: "context_compaction_started", Data: data})
	app.handleAgentEvent(types.AgentEventMsg{AgentID: id, Type: "context_compaction_stage", Data: data})
	if got := paint(); !strings.Contains(got, "Compacting context") || strings.Contains(got, "Idle") || strings.Contains(got, "Plan 3/4") {
		t.Fatalf("active compaction strip=%q", got)
	}
	status, _ := app.ws.Status().Agent(id)
	if status.Phase != ttypes.AgentCompacting || status.RequestID != "compact-status-1" {
		t.Fatalf("active status=%+v", status)
	}
	data["stage"], data["completed_stages"] = 4, 4
	data["after_tokens"] = 20500
	app.handleAgentEvent(types.AgentEventMsg{AgentID: id, Type: "context_compacted", Data: data})
	for range 3 {
		if got := paint(); !strings.Contains(got, "Context compacted") || !strings.Contains(got, "Ready to continue") || strings.Contains(got, "Idle") || strings.Contains(got, "Plan 3/4") {
			t.Fatalf("settled compaction strip=%q", got)
		}
	}
	status, _ = app.ws.Status().Agent(id)
	if status.Phase != ttypes.AgentCompactionResult || status.NeedsAction {
		t.Fatalf("settled status=%+v", status)
	}
	// A resumed boundary is still the authoritative latest operation even when
	// the transient status center has no event snapshot yet.
	wsOf(app).Status.RemoveAgent(id)
	if got := paint(); !strings.Contains(got, "Context compacted") || strings.Contains(got, "Plan 3/4") {
		t.Fatalf("rehydrated boundary strip=%q", got)
	}
	app.handleAgentEvent(types.AgentEventMsg{AgentID: id, Type: "turn_started", Data: ttypes.TurnIdentity{Generation: 1, ModelID: "model"}})
	app.handleAgentEvent(types.AgentEventMsg{AgentID: id, Type: "stream_token", Data: "Ready"})
	app.SpinFrame = int(time.Now().UnixMilli())
	planVisible := false
	for range 4 {
		got := paint()
		if strings.Contains(got, "Context compacted") {
			t.Fatalf("old compaction displaced new turn: %q", got)
		}
		planVisible = planVisible || strings.Contains(got, "4/4 Run build")
		app.SpinFrame += 6000
	}
	if !planVisible {
		t.Fatal("new turn did not restore the compact current task")
	}
}

func TestResumedManualCompactionBoundaryShowsReadyStatus(t *testing.T) {
	app := newFullTestApp(t)
	wsOf(app).Logger = stubLogger{}
	id := addBackendTestOrchestrator(t, app)
	planView := &activity.Plan{ID: "resumed-plan", Status: "active", Total: 4, Completed: 3,
		Current: []activity.Step{{Number: 4, Text: "Run build, tests, and lint"}}}
	app.ws.AgentMgr().Get(id).State.Messages.AppendMessage(workspace.Message{
		InternalID: "resumed-compaction", Role: workspace.MessageRoleStatus,
		LifecyclePhase: "compaction_completed", LifecycleOperationID: "resumed-compaction",
		LifecycleTrigger: "manual", LifecycleCompleted: 4, LifecycleStageCount: 4,
		LifecycleBeforeTokens: 510100, LifecycleAfterTokens: 20500, Time: time.Now(),
	})
	app.syncAgentMessages(id, true)
	wsOf(app).Status.SetAgent(ttypes.AgentStatusSnapshot{Ref: ttypes.ResourceRef{ID: id}, Phase: ttypes.AgentIdle, Detail: "Idle", RequestID: "old-request"})
	if status, found := app.ws.Status().Agent(id); found && status.Phase == ttypes.AgentCompactionResult {
		t.Fatal("fixture should restore from the durable boundary, not an event snapshot")
	}
	app.configureShell()
	view := app.ws.AgentActivity(id)
	view.Plan = planView
	app.chatScreen.SetActivity(view)
	app.renderShellToSurface()
	got := shellBufferRow(app, app.chatRect.Y+app.chatRect.H-1)
	if !strings.Contains(got, "Context compacted") || !strings.Contains(got, "Ready to continue") || strings.Contains(got, "Plan 3/4") {
		t.Fatalf("resumed boundary strip=%q", got)
	}
	app.appendAgentMessage(id, types.ChatMessage{Role: types.RoleUser, Content: "continue", Time: time.Now()})
	app.paintShellFrame()
	if got := shellBufferRow(app, app.chatRect.Y+app.chatRect.H-1); strings.Contains(got, "Context compacted") {
		t.Fatalf("new input still showed old compaction: %q", got)
	}
}

func TestSubmitProjectsLifecycleIntoChatNotBottomStatus(t *testing.T) {
	app := newFullTestApp(t)
	wsOf(app).Logger = stubLogger{}
	agentID := addBackendTestOrchestrator(t, app)

	runAppCommand(t, app, app.submitInput(agentID, "inspect lifecycle", nil))
	messages := app.ws.AgentMgr().Get(agentID).State.Messages.Messages()
	if len(messages) != 2 || messages[1].Role != types.RoleStatus || messages[1].LifecyclePhase != "preparing" {
		t.Fatalf("submitted conversation = %#v", messages)
	}
	if bottom := testStatusText(app); strings.Contains(bottom, "Getting next turn ready") || strings.Contains(bottom, "uploading context") {
		t.Fatalf("per-agent lifecycle leaked into bottom status: %q", bottom)
	}
	if working, _ := app.backgroundAgentCounts(); working != 0 {
		t.Fatalf("selected agent was duplicated as background work: %d", working)
	}
}

func TestPostCompactionThinkingCoalescesAndPaintsAfterBoundary(t *testing.T) {
	app := newFullTestApp(t)
	wsOf(app).Logger = stubLogger{}
	agentID := addBackendTestOrchestrator(t, app)
	base := map[string]any{
		"request_id": "compact-1", "trigger": "manual", "role": "root",
		"provider_id": "provider", "model_id": "aeron/model",
		"stage": 4, "completed_stages": 4, "stage_count": 4,
		"before_tokens": 155955, "after_tokens": 10476,
	}
	_ = app.handleAgentDomainEvent(workspace.AgentEvent{Type: "context_compaction_started", AgentID: agentID, Data: base})
	_ = app.handleAgentDomainEvent(workspace.AgentEvent{Type: "context_compacted", AgentID: agentID, Data: base})
	app.appendAgentMessage(agentID, types.ChatMessage{Role: types.RoleUser, Content: "continue after compact"})
	_ = app.handleAgentDomainEvent(workspace.AgentEvent{Type: "thinking", AgentID: agentID, Data: reasoning.Delta{Text: "first "}})
	_ = app.handleAgentDomainEvent(workspace.AgentEvent{Type: "thinking", AgentID: agentID, Data: reasoning.Delta{Text: "second"}})

	messages := app.ws.AgentMgr().Get(agentID).State.Messages.Messages()
	statusAt, userAt, thinkingAt, thinkingCount := -1, -1, -1, 0
	for index, message := range messages {
		switch {
		case message.LifecycleOperationID == "compact-1":
			statusAt = index
		case message.Content == "continue after compact":
			userAt = index
		case message.Role == types.RoleThinking:
			thinkingAt = index
			thinkingCount++
			if message.Content != "first second" {
				t.Fatalf("thinking content=%q", message.Content)
			}
		}
	}
	if thinkingCount != 1 || statusAt < 0 || userAt <= statusAt || thinkingAt <= userAt {
		t.Fatalf("post-compaction order status=%d user=%d thinking=%d count=%d messages=%+v", statusAt, userAt, thinkingAt, thinkingCount, messages)
	}
	app.syncAgentMessages(agentID, true)
	renderer := app.activeMessageList()
	renderer.SetSize(100, 20)
	renderer.SetChatAreaBounds(0, 20)
	rows := renderer.ExportMessageRowsSnapshot(100).Rows
	statusRow, thinkingRow := -1, -1
	for index, row := range rows {
		if row.ContextBoundary {
			statusRow = index
		}
		if strings.Contains(strings.Join(row.Lines, "\n"), "first second") {
			thinkingRow = index
		}
	}
	if statusRow < 0 || thinkingRow <= statusRow {
		t.Fatalf("paint projection order status=%d thinking=%d", statusRow, thinkingRow)
	}
	if rows[statusRow].ContextBoundaryStart != 1 || len(rows[thinkingRow].Lines) < 2 || strings.TrimSpace(rows[thinkingRow].Lines[0]) != "" {
		t.Fatalf("mandatory inter-object spacing missing: boundary=%+v thinking=%+v", rows[statusRow], rows[thinkingRow])
	}
}
