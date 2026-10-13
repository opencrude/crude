package viewmodel

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/sessiondb"
	"crude/tui/screens/launcher"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
	"crude/workspace"
)

type testLogger struct{}

func (l *testLogger) Info(tag, msg string, pairs ...any)  {}
func (l *testLogger) Warn(tag, msg string, pairs ...any)  {}
func (l *testLogger) Error(tag, msg string, pairs ...any) {}
func (l *testLogger) Debug(tag, msg string, pairs ...any) {}

func newTestAppWithLauncher(t *testing.T) *App {
	t.Helper()
	app := newTestApp(t)
	wsOf(app).Logger = &testLogger{}
	// Seed enough list rows for pointer-capture geometry tests that click mid-list.
	workspaces := make([]workspace.WorkspaceInfo, 12)
	for i := range workspaces {
		workspaces[i] = workspace.WorkspaceInfo{
			ID:           fmt.Sprintf("ws%d", i),
			ProjectName:  fmt.Sprintf("proj-%02d", i),
			ProjectDir:   fmt.Sprintf("/tmp/proj-%02d", i),
			SessionCount: 1,
		}
	}
	app.launcherScreen.SetData(".", workspaces)
	return app
}

// TestLauncher_NewSession_NoBackendConfig verifies that launcherSelect()
// does not clear the launcher when no backend config is set.
func TestLauncher_NewSession_NoBackendConfig(t *testing.T) {
	app := newTestAppWithLauncher(t)

	cmd := app.launcherNewSession()

	if app.launcherScreen == nil {
		t.Fatal("launcher should still be present when no backend config")
	}
	if cmd != nil {
		t.Fatal("expected nil cmd from launcherNewSession")
	}
	app.width, app.height = 100, 28
	app.showNextOperationIssue()
	app.paintShellFrame()
	if text := bufferPlain(app.mainSurf.Buffer()); strings.Contains(text, "Session could not be created") || strings.Contains(text, "Open Notices") {
		t.Fatalf("failure text was painted under the launcher action:\n%s", text)
	}
	if !app.overlayHost.IsOpen(ui.OverlayOperationIssue) {
		t.Fatal("startup failure did not open a recovery dialog")
	}
}

// TestLauncher_AddOrchestrator_CreatesAgent verifies AddOrchestrator clears
// the launcher and creates the agent. Blocking responses route through the
// workspace rather than a frontend-owned channel.
func TestLauncher_AddOrchestrator_CreatesAgent(t *testing.T) {
	app := newTestAppWithLauncher(t)

	app.AddOrchestrator("ses_1_orch_1")

	if app.launcherScreen != nil {
		t.Fatal("launcher should be cleared by AddOrchestrator")
	}
	n := app.ws.AgentMgr().Get("ses_1_orch_1")
	if n == nil {
		t.Fatal("orchestrator agent should exist")
	}
	if n.State == nil || n.State.Messages == nil {
		t.Fatal("orchestrator should have its own MsgList")
	}
	if app.AgentView.ActiveID() != "ses_1_orch_1" {
		t.Fatalf("expected ActiveID ses_1_orch_1, got %q", app.AgentView.ActiveID())
	}
}

// TestLauncher_AddOrchestrator_ClearsLauncher verifies that AddOrchestrator
// clears the launcher and creates the agent.
func TestLauncher_AddOrchestrator_ClearsLauncher(t *testing.T) {
	app := newTestAppWithLauncher(t)

	app.AddOrchestrator("ses_1_orch_1")

	if app.launcherScreen != nil {
		t.Fatal("launcher should be cleared by AddOrchestrator")
	}
	if app.ws.AgentMgr().Get("ses_1_orch_1") == nil {
		t.Fatal("orchestrator agent should exist")
	}
	if app.AgentView.ActiveID() != "ses_1_orch_1" {
		t.Fatalf("expected active agent ses_1_orch_1, got %q", app.AgentView.ActiveID())
	}
}

// TestLauncher_FileOpen_NoSessionCreated verifies that opening a file
// does NOT create any agent.
func TestLauncher_FileOpen_NoSessionCreated(t *testing.T) {
	app := newTestAppWithLauncher(t)

	msg := tea.KeyPressMsg{Code: tea.KeyEnter}
	app.Update(msg)

	if app.launcherScreen == nil {
		t.Fatal("launcher should not be cleared by non-session events")
	}
	if app.ws.AgentMgr().Count() != 0 {
		t.Fatal("no agents should be created by file-open events")
	}
}

// TestLauncher_ResumeSession verifies that selecting an existing session
// resumes in-place (does NOT quit the process).
func TestLauncher_ResumeSession(t *testing.T) {
	app := newTestAppWithLauncher(t)

	app.launcherScreen.SetData(".", nil)

	cmd := app.launcherResumeSession("ses_test_1", &sessiondb.SessionMeta{ID: "ses_test_1", ProjectName: "test-project"})

	// Without a real backend the resume fails gracefully — but must not quit.
	if app.Quit {
		t.Fatal("launcherResumeSession must not quit for in-place resume")
	}
	_ = cmd
}

// TestSubAgentStarted_SubAgentWithParent verifies that sub_agent_started
// with a parent_id creates a sub-pane.
func TestSubAgentStarted_SubAgentWithParent(t *testing.T) {
	app := newTestAppWithLauncher(t)
	const fullName = "smoke-async-researcher-with-full-identity"

	// Create the parent orchestrator via AddOrchestrator
	app.AddOrchestrator("ses_1_orch_1")
	parent := app.ws.AgentMgr().Get("ses_1_orch_1")
	parent.State.Messages.UpdateToolCall("tool-1", "internal-1", "agent_start", "category: researcher description: inspect lifecycle")

	// Create a sub-agent with parent_id
	subEvt := types.AgentEventMsg{
		Type:    "sub_agent_started",
		AgentID: "ses_1_orch_1_sub_abc",
		Data: map[string]string{
			"agent_id":       "ses_1_orch_1_sub_abc",
			"agent_name":     fullName,
			"parent_id":      "ses_1_orch_1",
			"parent_tool_id": "tool-1",
			"prompt":         "# Assignment\n- Description: inspect lifecycle",
		},
	}
	app.handleAgentEvent(subEvt)

	subAgent := app.ws.AgentMgr().Get("ses_1_orch_1_sub_abc")
	if subAgent == nil {
		t.Fatal("sub-agent should exist")
	}
	if subAgent.State == nil || subAgent.State.Messages == nil {
		t.Fatal("sub-agent should have a MsgList")
	}
	sp := app.ws.PaneMgr().Get("ses_1_orch_1_sub_abc")
	if sp == nil {
		t.Fatal("sub-agent should have a pane in paneMgr")
	}
	if subAgent.Parent == nil {
		t.Fatal("sub-agent should have a parent")
	}
	if subAgent.Parent.ID != "ses_1_orch_1" {
		t.Fatalf("expected parent ses_1_orch_1, got %q", subAgent.Parent.ID)
	}
	if subAgent.Label != fullName || sp.Title != fullName {
		t.Fatalf("canonical child identity was truncated: label=%q pane=%q", subAgent.Label, sp.Title)
	}
	messages := subAgent.State.Messages.Messages()
	if len(messages) == 0 || messages[0].Role != workspace.MessageRoleAssignment ||
		messages[0].Content != "# Assignment\n- Description: inspect lifecycle" {
		t.Fatalf("sub-agent assignment = %+v", messages)
	}
	parentMessages := parent.State.Messages.Messages()
	if len(parentMessages) != 1 || parentMessages[0].SubPaneID != "ses_1_orch_1_sub_abc" {
		t.Fatalf("sub-agent start created duplicate parent projections: %+v", parentMessages)
	}
}

// TestMultipleOrchestrators verifies two orchestrators via AddOrchestrator.
func TestMultipleOrchestrators(t *testing.T) {
	app := newTestAppWithLauncher(t)

	app.AddOrchestrator("ses_1_orch_1")

	// Add a sub-agent via the manager (simulating handleAgentEvent flow)
	app.ws.AgentMgr().AddSubAgent("ses_1_sub_abc", "ses_1_orch_1", "helper", "", "", &workspace.AgentState{})
	app.syncAgentWindowTabs()

	if app.ws.AgentMgr().Count() != 2 {
		t.Fatalf("expected 2 agents (orchestrator + sub), got %d", app.ws.AgentMgr().Count())
	}
	if app.ws.AgentMgr().Get("ses_1_orch_1") == nil {
		t.Fatal("orchestrator should exist")
	}
	if app.ws.AgentMgr().Get("ses_1_sub_abc") == nil {
		t.Fatal("sub-agent should exist")
	}

	// Switch between available agents (sub-agent first, then orchestrator)
	app.switchToAgentView("ses_1_sub_abc")
	if app.AgentView.ActiveID() != "ses_1_sub_abc" {
		t.Fatalf("expected active agent ses_1_sub_abc, got %q", app.AgentView.ActiveID())
	}
}

// TestLauncher_NoSessions_StartActionsOnly verifies the welcome launcher
// always has Start actions even with no sessions/workspaces.
func TestLauncher_NoSessions_StartActionsOnly(t *testing.T) {
	app := newTestAppWithLauncher(t)
	app.launcherScreen.SetData(".", nil)

	if app.launcherScreen.TotalItems() < 2 {
		t.Fatalf("expected at least 2 start items, got %d", app.launcherScreen.TotalItems())
	}
	it := app.launcherScreen.SelectedItem()
	if it == nil || it.Kind != launcher.ItemNewSession {
		t.Fatal("default selection should be New Session")
	}
}

// TestLauncherWorkspaceSearchKeepsActionsFixed verifies filtering only
// affects workspace results; the fixed action cards remain.
func TestLauncherWorkspaceSearchKeepsActionsFixed(t *testing.T) {
	screen := launcher.New(theme.DefaultDark())
	screen.SetData("/projects/current", []workspace.WorkspaceInfo{
		{ProjectName: "alpha", ProjectDir: "/projects/alpha"},
		{ProjectName: "beta", ProjectDir: "/projects/beta"},
	})

	// Activate filter and type "beta".
	host := ui.NewHost(theme.DefaultDark(), screen)
	screen.SetFocusHandler(host.Focus)
	host.Resize(80, 24)
	screen.SetAnimation(0, 0)

	host.Handle(tea.KeyPressMsg{Code: '/', Text: "/"})
	host.Handle(tea.KeyPressMsg{Code: 'b', Text: "b"})
	host.Handle(tea.KeyPressMsg{Code: 'e', Text: "e"})
	host.Handle(tea.KeyPressMsg{Code: 't', Text: "t"})
	host.Handle(tea.KeyPressMsg{Code: 'a', Text: "a"})

	if screen.TotalItems() != 4 {
		t.Fatalf("search should not remove fixed actions, got %d items", screen.TotalItems())
	}
	it := screen.SelectedItem()
	if it == nil || it.Kind != launcher.ItemWorkspace || it.Workspace.Name != "beta" {
		t.Fatalf("expected only beta selected, got %#v", it)
	}
	if !screen.FilterActive() {
		t.Fatal("active search state should be retained by the screen")
	}
}

// TestLauncherWheelOnlyScrollsWorkspacePane verifies the mouse wheel
// scrolls only the workspace list, not the fixed left column.
func TestLauncherWheelOnlyScrollsWorkspacePane(t *testing.T) {
	app := newTestAppWithLauncher(t)
	app.width = 120
	app.height = 30
	app.ChatAreaHeight = 20
	app.Tree = &TreeState{}
	app.paintShellFrame()

	workspaces := make([]workspace.WorkspaceInfo, 30)
	for i := range workspaces {
		workspaces[i] = workspace.WorkspaceInfo{
			ProjectName: fmt.Sprintf("workspace-%02d", i),
			ProjectDir:  fmt.Sprintf("/projects/workspace-%02d", i),
		}
	}
	app.launcherScreen.SetData(".", workspaces)

	app.paintShellFrame()
	search, _ := app.rootHost.ScreenRect(launcher.SearchID)
	list, _ := app.rootHost.ScreenRect(launcher.WorkspaceListID)
	app.rootHandleFocus(launcher.ScreenID, tea.MouseClickMsg{X: search.X + 1, Y: search.Y, Button: tea.MouseLeft})

	scrollBefore := app.launcherScreen.ListScrollOffset()
	app.rootHost.Handle(tea.MouseWheelMsg{X: list.X + 1, Y: list.Y, Button: tea.MouseWheelDown})
	if app.launcherScreen.ListScrollOffset() == scrollBefore {
		t.Fatal("wheel inside workspace viewport should scroll results")
	}

	// Wheel over the fixed actions should not scroll workspace resources.
	app.launcherScreen.SetListScroll(0)
	action, _ := app.rootHost.ScreenRect(launcher.NewSessionID)
	app.rootHost.Handle(tea.MouseWheelMsg{X: action.X + 1, Y: action.Y, Button: tea.MouseWheelDown})
	if app.launcherScreen.ListScrollOffset() != 0 {
		t.Fatal("wheel over fixed left help/actions must not scroll workspace results")
	}
}

// TestLauncherUsesMainPaneWithoutHelpStrip verifies launcher help lives in
// the status region, not inside the main pane.
func TestLauncherUsesMainPaneWithoutHelpStrip(t *testing.T) {
	app := newFullTestApp(t)
	app.width = 100
	app.height = 30
	app.launcherScreen.SetData(".", nil)

	_ = app.computeContentLayout()
	// Launcher reclaims the tab-bar rows; with the tab bar hidden the chat
	// area spans from chromeTopRow down to the bottom chrome (30 - 2 - 3).
	if want := 30 - app.chromeBottomRows() - app.chromeTopRow(); app.ChatAreaHeight != want {
		t.Fatalf("launcher should receive reclaimed separator/help rows: height=%d, want %d", app.ChatAreaHeight, want)
	}
}

func TestLauncher_GlobalEvent_HandledWithoutMode(t *testing.T) {
	app := newTestAppWithLauncher(t)

	evt := types.AgentEventMsg{
		Type:    "async_command_status",
		AgentID: "",
		Data:    []struct{}{},
	}
	cmd := app.handleAgentEvent(evt)
	if cmd != nil {
		t.Fatal("async_command_status should return nil cmd")
	}
}
