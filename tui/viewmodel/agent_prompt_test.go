package viewmodel

import (
	"testing"

	"crude/tui/ui"
	"crude/workspace"
)

func TestAgentConfirmationUsesGlobalModalAndRespectsYolo(t *testing.T) {
	app := newFullTestApp(t)
	app.AddOrchestrator("root")
	req := agentPromptRequest{kind: agentPromptConfirmation, agentID: "root", blockID: "b1", question: "Allow write?", risk: "project-write"}
	app.enqueueAgentConfirmation(req)
	if app.agentPrompt == nil || !app.overlayHost.IsOpen(ui.OverlayAgentPrompt) {
		t.Fatal("confirmation did not open as the app-level modal")
	}
	app.finishAgentPrompt()
	app.OverlayState.SetYolo(true)
	app.enqueueAgentConfirmation(req)
	if app.agentPrompt != nil || app.overlayHost.IsOpen(ui.OverlayAgentPrompt) {
		t.Fatal("YOLO confirmation was incorrectly presented")
	}
}

func TestActiveAgentConfirmationStaysInline(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 120, 30
	app.AddOrchestrator("root")
	session := &workspace.Session{SessionID: "root-session"}
	app.ActiveSession = session
	app.SessionsByAgent = map[string]*workspace.Session{"root": session}
	app.paintShellFrame()
	app.enqueueAgentConfirmation(agentPromptRequest{kind: agentPromptConfirmation, agentID: "root", blockID: "inline", question: "Allow write?"})
	projection := app.activeSessionProjection()
	if app.agentPrompt == nil || !app.agentPrompt.inline || projection == nil || projection.deck.Active() == nil || projection.deck.Active().ID != sessionConfirmationPanelID {
		t.Fatalf("active prompt was not retained inline: state=%+v panel=%v", app.agentPrompt, projection)
	}
	if app.overlayHost.IsOpen(ui.OverlayAgentPrompt) {
		t.Fatal("inline confirmation also opened the global modal")
	}
}

func TestSubAgentConfirmationRoutesThroughOwningSession(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 120, 30
	app.AddOrchestrator("root")
	session := &workspace.Session{SessionID: "root-session"}
	app.ActiveSession = session
	app.SessionsByAgent = map[string]*workspace.Session{"root": session}
	pane := app.PaneMgr.GetOrCreate("child", workspace.PaneAgent, "child")
	childState := workspace.NewAgentState("child", pane.Messages, app.Logger)
	app.AgentMgr.AddSubAgent("child", "root", "Child", "model", "researcher", childState)
	app.syncAgentWindowTabs()
	app.switchToAgentView("child")
	app.paintShellFrame()
	app.enqueueAgentConfirmation(agentPromptRequest{kind: agentPromptConfirmation, agentID: "child", blockID: "child-block", question: "Allow child write?"})
	_, projection := app.sessionProjectionForAgent("child")
	if projection == nil || projection.confirmationAgentID != "child" || projection.confirmationBlockID != "child-block" {
		t.Fatalf("child prompt owner lost: projection=%+v", projection)
	}
	if app.agentPrompt == nil || !app.agentPrompt.inline {
		t.Fatal("visible child prompt was not rendered inline")
	}
}

func TestAgentPromptsQueueInsteadOfOverwriting(t *testing.T) {
	app := newFullTestApp(t)
	app.AddOrchestrator("root")
	app.enqueueAgentConfirmation(agentPromptRequest{kind: agentPromptConfirmation, agentID: "root", blockID: "one", question: "one"})
	app.enqueueAgentConfirmation(agentPromptRequest{kind: agentPromptConfirmation, agentID: "root", blockID: "two", question: "two"})
	if app.agentPrompt == nil || app.agentPrompt.request.blockID != "one" || len(app.agentPromptQueue) != 1 {
		t.Fatalf("prompt state=%+v queue=%d", app.agentPrompt, len(app.agentPromptQueue))
	}
	app.resolveAgentConfirmationChoice(2)
	if app.agentPrompt == nil || app.agentPrompt.request.blockID != "two" {
		t.Fatal("resolving first prompt did not reveal the queued prompt")
	}
}
