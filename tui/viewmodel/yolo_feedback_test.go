package viewmodel

import (
	"testing"

	tuitypes "crude/tui/types"
	"crude/tui/ui"
	"crude/types"
	"crude/workspace"
)

func TestYoloTransitionsUseOnePathAndDoNotImpersonateAssistant(t *testing.T) {
	app := newFullTestApp(t)
	app.Logger = stubLogger{}
	id := addBackendTestOrchestrator(t, app)
	t.Cleanup(app.CloseAllSessions)
	app.ActiveSession = app.SessionsByAgent[id]
	app.AgentView.SetActive(id)
	state := app.AgentMgr.Get(id).State
	app.executeSlashCommand("/yolo")
	projection := app.activeSessionProjection()
	app.resolveSessionYolo(projection.yolo, true)
	if !app.YoloForAgent(id) || !app.OverlayState.IsYolo() {
		t.Fatal("confirmed policy did not reach backend and header")
	}
	app.executePaletteCommand("safety.yolo.toggle")
	if app.YoloForAgent(id) || app.OverlayState.IsYolo() {
		t.Fatal("palette did not restore the owning session's approval policy")
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
	app.executePaletteCommand("safety.yolo.toggle")
	app.resolveSessionYolo(projection.yolo, false)
	app.resolveSessionYolo(projection.yolo, true)
	if len(state.Messages.Messages()) != 2 || app.YoloForAgent(id) {
		t.Fatal("cancel or stale confirmation changed policy/transcript")
	}
}

func TestYoloScopesToOwningRootAndCompleteDescendantTree(t *testing.T) {
	app := newFullTestApp(t)
	app.Logger = stubLogger{}
	first := addBackendTestOrchestrator(t, app)
	second := addBackendTestOrchestrator(t, app)
	t.Cleanup(app.CloseAllSessions)
	pane := app.PaneMgr.GetOrCreate("child", workspace.PaneAgent, "child")
	childState := workspace.NewAgentState("child", pane.Messages, app.Logger)
	app.AgentMgr.AddSubAgent("child", first, "Child", "model", "researcher", childState)

	if !app.SetYoloForAgent("child", true) {
		t.Fatal("could not enable YOLO through child owner")
	}
	if !app.YoloForAgent(first) || !app.YoloForAgent("child") || app.YoloForAgent(second) {
		t.Fatal("YOLO did not stay within the first root's session tree")
	}
	app.enqueueAgentConfirmation(agentPromptRequest{kind: agentPromptConfirmation, agentID: "child", blockID: "child-yolo", question: "Allow write?"})
	if app.agentPrompt != nil || app.overlayHost.IsOpen(ui.OverlayAgentPrompt) {
		t.Fatal("YOLO descendant still presented a confirmation")
	}
	if !app.SetYoloForAgent(second, true) || !app.SetYoloForAgent(first, false) {
		t.Fatal("could not update independent session policies")
	}
	if app.YoloForAgent(first) || app.YoloForAgent("child") || !app.YoloForAgent(second) {
		t.Fatal("changing the first session leaked into the second session")
	}
}

func TestAgentFailurePublishesOneNoticeForItsActualOwner(t *testing.T) {
	app := newFullTestApp(t)
	app.AddOrchestrator("background")
	app.AgentMgr.Get("background").State.Running = true
	app.AddOrchestrator("foreground")
	cmd := app.handleAgentEvent(tuitypes.AgentEventMsg{AgentID: "background", Type: "error", Data: "history sync failed"})
	if cmd != nil {
		cmd()
	}
	notices := app.Status.Notifications(false)
	if len(notices) != 1 || notices[0].Owner.ID != "background" || notices[0].Detail != "history sync failed" {
		t.Fatalf("diagnostic was duplicated or attributed to foreground: %+v", notices)
	}
}
