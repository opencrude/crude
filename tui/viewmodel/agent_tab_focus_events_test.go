package viewmodel

import (
	"testing"

	"crude/tui/screens/question"
	ttypes "crude/tui/types"
	"crude/tui/ui"
	"crude/types"
	"crude/workspace"
)

func assertSelectedTab(t *testing.T, app *App, title string) {
	t.Helper()
	active := app.Win.ActiveTab()
	if active == nil || active.Title != title {
		t.Fatalf("selected tab=%v, want %q", active, title)
	}
}

// Backend events can arrive while another agent or an editor is selected.
// The prompt, not an unsolicited tab switch, is the interaction surface.
func TestBackgroundAgentEventsPreserveSelectedTabAndPromptOwner(t *testing.T) {
	app, ws := newRealShellApp(t)
	defer ws.Close()
	app.AddOrchestrator("root")
	app.AddOrchestrator("other")
	if app.AgentView.ActiveID() != "other" {
		t.Fatal("precondition: other agent selected")
	}

	app.handleAgentEvent(ttypes.AgentEventMsg{AgentID: "root", Type: "blocking", Data: map[string]string{
		"block_id": "root-permission", "question": "Allow write?", "operation": "write", "tool": "edit_file",
	}})
	assertSelectedTab(t, app, app.agentTabTitle("other"))
	if app.AgentView.ActiveID() != "other" || app.agentPrompt == nil || app.agentPrompt.inline ||
		app.agentPrompt.request.agentID != "root" || app.agentPrompt.request.blockID != "root-permission" ||
		!app.overlayHost.IsOpen(ui.OverlayAgentPrompt) {
		t.Fatalf("background permission did not retain owner/modal: selected=%q prompt=%+v", app.AgentView.ActiveID(), app.agentPrompt)
	}
	// A modal can accept a decision for its original owner even though a
	// backend-less test workspace reports that delivery is no longer active.
	app.resolveAgentConfirmationChoice(2)
	assertSelectedTab(t, app, app.agentTabTitle("other"))
	if app.agentPrompt != nil || app.overlayHost.IsOpen(ui.OverlayAgentPrompt) {
		t.Fatal("resolved permission left a modal open")
	}

	app.handleAgentEvent(ttypes.AgentEventMsg{AgentID: "root", Type: "safety_alert", Data: map[string]any{
		"risk": "destructive", "reason": "test alert",
	}})
	assertSelectedTab(t, app, app.agentTabTitle("other"))
	notice, ok := wsOf(app).Status.LatestUnread(types.SeverityWarning)
	if app.AgentView.ActiveID() != "other" || !ok || notice.Owner.ID != "root" || notice.Summary != "test alert" {
		t.Fatal("background safety alert changed navigation or lost its owning notification")
	}

	path := openEditorTabs(t, app, "main.go")[0]
	assertSelectedTab(t, app, app.editorTabTitle(path))
	app.handleAgentEvent(ttypes.AgentEventMsg{AgentID: "root", Type: "blocking", Data: map[string]any{
		"block_id": "root-question", "operation": "questions", "questions_json": `[{"question":"Which option?","options":["A","B"]}]`,
	}})
	assertEditorStillPainted(t, app, path)
	if app.agentPrompt == nil || app.agentPrompt.inline || app.agentPrompt.request.agentID != "root" ||
		app.agentPrompt.request.blockID != "root-question" || app.agentPrompt.question == nil ||
		!app.overlayHost.IsOpen(ui.OverlayAgentPrompt) {
		t.Fatalf("background question did not retain modal and owner: %+v", app.agentPrompt)
	}
	owner := app.agentPrompt.question
	app.resolveAgentQuestion(owner, question.Result{}, true)
	assertEditorStillPainted(t, app, path)
	if app.agentPrompt != nil || app.overlayHost.IsOpen(ui.OverlayAgentPrompt) {
		t.Fatal("answered question left modal open")
	}

	// Explicit navigation remains authoritative after unattended events.
	app.switchToAgentView("root")
	assertSelectedTab(t, app, app.agentTabTitle("root"))
	if app.AgentView.ActiveID() != "root" || app.EditorState.ActiveView != "" {
		t.Fatal("explicit agent navigation did not select its painted view")
	}
}

func TestLateBackgroundAgentInitializationPreservesEditorSelection(t *testing.T) {
	app, ws := newRealShellApp(t)
	defer ws.Close()
	app.AddOrchestrator("root")
	path := openEditorTabs(t, app, "main.go")[0]
	app.ws.AgentMgr().AddOrchestrator("late", nil)
	app.handleAgentEvent(ttypes.AgentEventMsg{AgentID: "late", Type: "phase", Data: map[string]any{"name": "waiting_provider"}})
	assertEditorStillPainted(t, app, path)
	if app.AgentView.ActiveID() != "" || app.ws.AgentMgr().Get("late").State == nil {
		t.Fatal("late initialization selected an agent or lost its state")
	}
}

func TestBackgroundChildPromptPreservesParentTab(t *testing.T) {
	app := newFullTestApp(t)
	app.AddOrchestrator("root")
	session := &workspace.Session{SessionID: "root-family"}
	wsOf(app).SessionsByAgent = map[string]*workspace.Session{"root": session}
	app.ActiveSession = session
	pane := app.ws.PaneMgr().GetOrCreate("child", workspace.PaneAgent, "child")
	app.ws.AgentMgr().AddSubAgent("child", "root", "Child", "model", "researcher",
		workspace.NewAgentState("child", pane.Messages, app.ws.Logger()))
	app.syncAgentWindowTabs()
	app.handleAgentEvent(ttypes.AgentEventMsg{AgentID: "child", Type: "blocking", Data: map[string]string{
		"block_id": "child-permission", "question": "Allow child write?", "operation": "write",
	}})
	assertSelectedTab(t, app, app.agentTabTitle("root"))
	if app.AgentView.ActiveID() != "root" || len(app.agentPrompts) != 1 || !app.agentPrompts[0].inline ||
		app.agentPrompts[0].request.agentID != "child" || app.overlayHost.IsOpen(ui.OverlayAgentPrompt) {
		t.Fatalf("child permission stole parent or lost family dock: %+v", app.agentPrompts)
	}
}
