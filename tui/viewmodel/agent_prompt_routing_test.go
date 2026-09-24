package viewmodel

import (
	"testing"

	"crude/tui/ui"
	"crude/workspace"
)

func TestBackgroundSubAgentConfirmationUsesModalAndKeepsChildOwner(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 120, 30
	app.AddOrchestrator("root")
	session := &workspace.Session{SessionID: "root-session"}
	app.ActiveSession = session
	app.SessionsByAgent = map[string]*workspace.Session{"root": session}
	pane := app.PaneMgr.GetOrCreate("child", workspace.PaneAgent, "child")
	childState := workspace.NewAgentState("child", pane.Messages, app.Logger)
	app.AgentMgr.AddSubAgent("child", "root", "Child", "model", "researcher", childState)
	app.switchToAgentView("root")
	app.paintShellFrame()

	app.enqueueAgentConfirmation(agentPromptRequest{kind: agentPromptConfirmation, agentID: "child", blockID: "child-block", question: "Allow child write?"})
	if app.agentPrompt == nil || app.agentPrompt.inline || app.agentPrompt.request.agentID != "child" {
		t.Fatalf("background child prompt routing=%+v", app.agentPrompt)
	}
	if !app.overlayHost.IsOpen(ui.OverlayAgentPrompt) {
		t.Fatal("background child prompt did not open the attention modal")
	}
	if projection := app.activeSessionProjection(); projection != nil && projection.deck.IsOpen() {
		t.Fatal("background child prompt also opened the inline session deck")
	}
}
