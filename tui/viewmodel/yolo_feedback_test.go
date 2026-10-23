package viewmodel

import (
	"crude/tui/screens/decision"
	"testing"

	"crude/tui/ui"
	"crude/types"
	"crude/workspace"
)

func TestYoloTransitionsUseOnePathAndDoNotImpersonateAssistant(t *testing.T) {
	app := newFullTestApp(t)
	wsOf(app).Logger = stubLogger{}
	id := addBackendTestOrchestrator(t, app)
	t.Cleanup(wsOf(app).CloseAllSessions)
	app.ActiveSession = app.ws.SessionsByAgent()[id]
	app.AgentView.SetActive(id)
	state := app.ws.AgentMgr().Get(id).State
	app.executeSlashCommand("/yolo")
	projection := app.activeSessionProjection()
	app.resolveSessionDecision(decision.ResultMsg{Target: projection.decision, Purpose: "yolo", Confirmed: true})
	if !app.ws.YoloForAgent(id) || !app.OverlayState.IsYolo() {
		t.Fatal("confirmed policy did not reach backend and header")
	}
	app.executeSlashCommand("/yolo")
	if app.ws.YoloForAgent(id) || app.OverlayState.IsYolo() {
		t.Fatal("/yolo did not restore the owning session's approval policy")
	}
	messages := state.Messages.Messages()
	if len(messages) != 2 || messages[0].LifecyclePhase != "yolo_enabled" || messages[1].LifecyclePhase != "yolo_disabled" {
		t.Fatalf("enable/disable feedback differs: %+v", messages)
	}
	for _, message := range messages {
		if message.Role != types.RoleStatus || message.LifecycleOperationID == "" {
			t.Fatalf("YOLO feedback became assistant or transient turn content: %+v", message)
		}
	}
	state.Messages.ClearTurnStatus()
	app.executeSlashCommand("/yolo")
	app.resolveSessionDecision(decision.ResultMsg{Target: projection.decision, Purpose: "yolo", Confirmed: false})
	app.resolveSessionDecision(decision.ResultMsg{Target: projection.decision, Purpose: "yolo", Confirmed: true})
	if len(state.Messages.Messages()) != 2 || app.ws.YoloForAgent(id) {
		t.Fatal("cancel or stale confirmation changed policy/transcript")
	}
}

func TestYoloScopesToOwningRootAndCompleteDescendantTree(t *testing.T) {
	app := newFullTestApp(t)
	wsOf(app).Logger = stubLogger{}
	first := addBackendTestOrchestrator(t, app)
	second := addBackendTestOrchestrator(t, app)
	t.Cleanup(wsOf(app).CloseAllSessions)
	pane := app.ws.PaneMgr().GetOrCreate("child", workspace.PaneAgent, "child")
	childState := workspace.NewAgentState("child", pane.Messages, app.ws.Logger())
	app.ws.AgentMgr().AddSubAgent("child", first, "Child", "model", "researcher", childState)

	if !app.ws.SetYoloForAgent("child", true) {
		t.Fatal("could not enable YOLO through child owner")
	}
	if !app.ws.YoloForAgent(first) || !app.ws.YoloForAgent("child") || app.ws.YoloForAgent(second) {
		t.Fatal("YOLO did not stay within the first root's session tree")
	}
	app.enqueueAgentConfirmation(agentPromptRequest{kind: agentPromptConfirmation, agentID: "child", blockID: "child-yolo", question: "Allow write?"})
	if app.agentPrompt != nil || app.overlayHost.IsOpen(ui.OverlayAgentPrompt) {
		t.Fatal("YOLO descendant still presented a confirmation")
	}
	if !app.ws.SetYoloForAgent(second, true) || !app.ws.SetYoloForAgent(first, false) {
		t.Fatal("could not update independent session policies")
	}
	if app.ws.YoloForAgent(first) || app.ws.YoloForAgent("child") || !app.ws.YoloForAgent(second) {
		t.Fatal("changing the first session leaked into the second session")
	}
}

func TestAgentFailurePublishesOneNoticeForItsActualOwner(t *testing.T) {
	app := newFullTestApp(t)
	app.AddOrchestrator("background")
	app.ws.AgentMgr().Get("background").State.Running = true
	app.AddOrchestrator("foreground")
	cmd := app.handleAgentEvent(types.AgentEventMsg{AgentID: "background", Type: "error", Data: "history sync failed"})
	if cmd != nil {
		cmd()
	}
	notices := app.ws.Status().Notifications(false)
	if len(notices) != 1 || notices[0].Owner.ID != "background" || notices[0].Detail != "history sync failed" {
		t.Fatalf("diagnostic was duplicated or attributed to foreground: %+v", notices)
	}
}
