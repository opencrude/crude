package viewmodel

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/provider"
	"crude/sessiondb"
	"crude/tui/input"
	"crude/tui/screens/launcher"
	"crude/tui/theme"
	"crude/tui/types"
	"crude/tui/ui"
	"crude/tui/widgets"
	"crude/tui/window"
	"crude/workspace"
)

func newTestChannels() (chan<- provider.UserInput, chan<- provider.UserInput, chan<- struct{}, chan string) {
	return make(chan provider.UserInput, 8), make(chan provider.UserInput, 8), make(chan struct{}, 1), make(chan string, 1)
}

type testLogger struct{}

func (l *testLogger) Info(tag, msg string, pairs ...any)  {}
func (l *testLogger) Warn(tag, msg string, pairs ...any)  {}
func (l *testLogger) Error(tag, msg string, pairs ...any) {}
func (l *testLogger) Debug(tag, msg string, pairs ...any) {}

func newTestAppWithLauncher(t *testing.T) *App {
	t.Helper()
	th := theme.DefaultDark()
	screen := launcher.New(th)
	screen.Refresh(".", nil)
	host := ui.NewHost(th, screen)
	screen.SetFocusHandler(host.Focus)
	app := &App{
		Workspace: &workspace.Workspace{
			AgentMgr:      workspace.NewAgentManager(),
			PaneMgr:       workspace.NewPaneManager(),
			StartTime:     time.Now(),
			ActiveSession: &workspace.Session{},
			Logger:        &testLogger{},
		},
		Theme:              th,
		Input:              input.NewModel(th),
		StatusBar:          widgets.NewStatusBar(th),
		SpinFrame:          0,
		OverlayState:       NewOverlayState(),
		LSPPopup:           NewLSPPopupState(),
		EditorState:        NewEditorState(),
		launcherScreen:     screen,
		launcherHost:       host,
		actionRegistry:     ui.DefaultRegistry(),
		subPaneProjections: newSubPaneProjectionRegistry(th),
		sessionProjections: newSessionProjectionRegistry(th),
	}
	app.Win = window.New(th)
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
}

// TestLauncher_AddOrchestrator_CreatesAgent verifies AddOrchestrator clears
// the launcher, creates the agent, and wires channels.
func TestLauncher_AddOrchestrator_CreatesAgent(t *testing.T) {
	app := newTestAppWithLauncher(t)
	inputCh, btwCh, cancelCh, blockResolveCh := newTestChannels()

	app.AddOrchestrator("ses_1_orch_1", inputCh, btwCh, cancelCh, blockResolveCh)

	if app.launcherScreen != nil {
		t.Fatal("launcher should be cleared by AddOrchestrator")
	}
	n := app.AgentMgr.Get("ses_1_orch_1")
	if n == nil {
		t.Fatal("orchestrator agent should exist")
	}
	if n.State == nil || n.State.Messages == nil {
		t.Fatal("orchestrator should have its own MsgList")
	}
	if app.AgentMgr.ActiveID() != "ses_1_orch_1" {
		t.Fatalf("expected ActiveID ses_1_orch_1, got %q", app.AgentMgr.ActiveID())
	}
	if n.InputChan == nil {
		t.Fatal("input channel should be wired")
	}
	if n.BtwChan == nil {
		t.Fatal("btw channel should be wired")
	}
	if n.CancelChan == nil {
		t.Fatal("cancel channel should be wired")
	}
	if n.BlockResolveCh == nil {
		t.Fatal("block resolve channel should be wired")
	}
}

// TestLauncher_AddOrchestrator_ClearsLauncher verifies that AddOrchestrator
// clears the launcher and creates the agent.
func TestLauncher_AddOrchestrator_ClearsLauncher(t *testing.T) {
	app := newTestAppWithLauncher(t)
	inputCh, btwCh, cancelCh, blockResolveCh := newTestChannels()

	app.AddOrchestrator("ses_1_orch_1", inputCh, btwCh, cancelCh, blockResolveCh)

	if app.launcherScreen != nil {
		t.Fatal("launcher should be cleared by AddOrchestrator")
	}
	if app.AgentMgr.Get("ses_1_orch_1") == nil {
		t.Fatal("orchestrator agent should exist")
	}
	if app.AgentMgr.ActiveID() != "ses_1_orch_1" {
		t.Fatalf("expected active agent ses_1_orch_1, got %q", app.AgentMgr.ActiveID())
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
	if app.AgentMgr.Count() != 0 {
		t.Fatal("no agents should be created by file-open events")
	}
}

// TestLauncher_ResumeSession verifies that selecting an existing session
// resumes in-place (does NOT quit the process).
func TestLauncher_ResumeSession(t *testing.T) {
	app := newTestAppWithLauncher(t)

	app.launcherScreen.SetData(".", []sessiondb.SessionMeta{
		{ID: "ses_test_1", ProjectName: "test-project"},
	}, nil)
	app.launcherScreen.Refresh(".", []sessiondb.SessionMeta{
		{ID: "ses_test_1", ProjectName: "test-project"},
	})

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

	// Create the parent orchestrator via AddOrchestrator
	inputCh, btwCh, cancelCh, blockResolveCh := newTestChannels()
	app.AddOrchestrator("ses_1_orch_1", inputCh, btwCh, cancelCh, blockResolveCh)

	// Create a sub-agent with parent_id
	subEvt := types.AgentEventMsg{
		Type:    "sub_agent_started",
		AgentID: "ses_1_orch_1_sub_abc",
		Data: map[string]string{
			"agent_id":   "ses_1_orch_1_sub_abc",
			"agent_name": "helper",
			"parent_id":  "ses_1_orch_1",
		},
	}
	app.handleAgentEvent(subEvt)

	subAgent := app.AgentMgr.Get("ses_1_orch_1_sub_abc")
	if subAgent == nil {
		t.Fatal("sub-agent should exist")
	}
	if subAgent.State == nil || subAgent.State.Messages == nil {
		t.Fatal("sub-agent should have a MsgList")
	}
	sp := app.PaneMgr.Get("ses_1_orch_1_sub_abc")
	if sp == nil {
		t.Fatal("sub-agent should have a pane in paneMgr")
	}
	if subAgent.Parent == nil {
		t.Fatal("sub-agent should have a parent")
	}
	if subAgent.Parent.ID != "ses_1_orch_1" {
		t.Fatalf("expected parent ses_1_orch_1, got %q", subAgent.Parent.ID)
	}
}

// TestMultipleOrchestrators verifies two orchestrators via AddOrchestrator.
func TestMultipleOrchestrators(t *testing.T) {
	app := newTestAppWithLauncher(t)

	inputCh, btwCh, cancelCh, brCh := newTestChannels()
	app.AddOrchestrator("ses_1_orch_1", inputCh, btwCh, cancelCh, brCh)

	// Add a sub-agent via the manager (simulating handleAgentEvent flow)
	app.AgentMgr.AddSubAgent("ses_1_sub_abc", "ses_1_orch_1", "helper", "", "", &workspace.AgentState{})
	app.syncAgentWindowTabs()

	if app.AgentMgr.Count() != 2 {
		t.Fatalf("expected 2 agents (orchestrator + sub), got %d", app.AgentMgr.Count())
	}
	if app.AgentMgr.Get("ses_1_orch_1") == nil {
		t.Fatal("orchestrator should exist")
	}
	if app.AgentMgr.Get("ses_1_sub_abc") == nil {
		t.Fatal("sub-agent should exist")
	}

	// Switch between available agents (sub-agent first, then orchestrator)
	app.switchToAgentView("ses_1_sub_abc")
	if app.AgentMgr.ActiveID() != "ses_1_sub_abc" {
		t.Fatalf("expected active agent ses_1_sub_abc, got %q", app.AgentMgr.ActiveID())
	}
}

// TestLauncher_NoSessions_StartActionsOnly verifies the welcome launcher
// always has Start actions even with no sessions/workspaces.
func TestLauncher_NoSessions_StartActionsOnly(t *testing.T) {
	app := newTestAppWithLauncher(t)
	app.launcherScreen.Refresh(".", nil)

	if app.launcherScreen.TotalItems() < 2 {
		t.Fatalf("expected at least 2 start items, got %d", app.launcherScreen.TotalItems())
	}
	if app.launcherScreen.SelectedSessionID() != "" {
		t.Fatal("SelectedSessionID should be empty for default selection")
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
	screen.SetData("/projects/current", nil, []workspace.WorkspaceInfo{
		{ProjectName: "alpha", ProjectDir: "/projects/alpha"},
		{ProjectName: "beta", ProjectDir: "/projects/beta"},
	})

	// Activate filter and type "beta".
	host := ui.NewHost(theme.DefaultDark(), screen)
	screen.SetFocusHandler(host.Focus)
	host.Resize(80, 24)
	screen.SetShortcuts([][2]string{})
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

	workspaces := make([]workspace.WorkspaceInfo, 30)
	for i := range workspaces {
		workspaces[i] = workspace.WorkspaceInfo{
			ProjectName: fmt.Sprintf("workspace-%02d", i),
			ProjectDir:  fmt.Sprintf("/projects/workspace-%02d", i),
		}
	}
	app.launcherScreen.SetData(".", nil, workspaces)

	layout := app.computeLayout()
	app.launcherHost.Resize(layout.InnerW, app.ChatAreaHeight)
	app.launcherHost.Layout()

	// Click search row to focus filter, then wheel inside list.
	app.launcherHost.Handle(tea.MouseClickMsg{X: 50, Y: 6, Button: tea.MouseLeft})

	scrollBefore := app.launcherScreen.ListScrollOffset()
	app.launcherHost.Handle(tea.MouseWheelMsg{X: 50, Y: 7, Button: tea.MouseWheelDown})
	if app.launcherScreen.ListScrollOffset() == scrollBefore {
		t.Fatal("wheel inside workspace viewport should scroll results")
	}

	// Wheel over left column should not scroll.
	app.launcherScreen.SetListScroll(0)
	app.launcherHost.Handle(tea.MouseWheelMsg{X: 2, Y: 7, Button: tea.MouseWheelDown})
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
	app.launcherScreen = launcher.New(app.Theme)
	app.launcherScreen.Refresh(".", nil)

	view := app.buildMainView()
	if strings.Contains(view, "↑↓ navigate") {
		t.Fatalf("launcher help should not consume a main-pane row:\n%s", view)
	}
	if app.ChatAreaHeight != 25 {
		t.Fatalf("launcher should receive reclaimed separator/help rows: height=%d, want 25", app.ChatAreaHeight)
	}
}

// TestLauncherShortcutsOnlyShowReachableBindings verifies shortcut labels
// derive from the action registry and do not embed their own labels.
func TestLauncherShortcutsOnlyShowReachableBindings(t *testing.T) {
	app := newTestAppWithLauncher(t)
	shortcuts := app.launcherShortcuts()
	for _, shortcut := range shortcuts {
		if shortcut[1] == "Settings" {
			t.Fatalf("palette-only Settings action must not claim a direct launcher shortcut: %q", shortcut[0])
		}
		if shortcut[1] != "" && strings.Contains(strings.ToLower(shortcut[0]), strings.ToLower(shortcut[1])) {
			t.Fatalf("shortcut %q embeds its label %q", shortcut[0], shortcut[1])
		}
	}
}

// TestLauncher_GlobalEvent_HandledWithoutMode verifies global events
// (empty agentID) are handled even with no agents.
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
