package viewmodel

import (
	"testing"

	tuitypes "crude/tui/types"
	"crude/types"
)

func TestYoloTransitionsUseOnePathAndDoNotImpersonateAssistant(t *testing.T) {
	app := newFullTestApp(t)
	app.Logger = stubLogger{}
	id := addBackendTestOrchestrator(t, app)
	t.Cleanup(app.CloseAllSessions)
	app.ActiveSession = app.SessionsByAgent[id]
	app.AgentMgr.SetActive(id)
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
