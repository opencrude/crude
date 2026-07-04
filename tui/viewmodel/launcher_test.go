package viewmodel

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/provider"
	"crude/sessiondb"
	"crude/tui/input"
	"crude/tui/theme"
	"crude/tui/types"
	"crude/tui/widgets"
	"crude/tui/window"
	"crude/tui/workspace"
)

func newTestChannels() (chan<- provider.UserInput, chan<- provider.UserInput, chan<- struct{}, chan string) {
	return make(chan provider.UserInput, 8), make(chan provider.UserInput, 8), make(chan struct{}, 1), make(chan string, 1)
}

func newTestAppWithLauncher(t *testing.T) *App {
	t.Helper()
	th := theme.DefaultDark()
	app := &App{
		Workspace: &workspace.Workspace{
			Theme:         th,
			AgentMgr:      workspace.NewAgentManager(),
			PaneMgr:       workspace.NewPaneManager(),
			StartTime:     time.Now(),
			Input:         input.NewModel(th),
			StatusBar:     widgets.NewStatusBar(th),
			SpinFrame:     0,
			ActiveSession: &workspace.Session{},
		},
		OverlayState: NewOverlayState(),
		LSPPopup:     NewLSPPopupState(),
		EditorState:  NewEditorState(),
		launcher:     NewLauncherState(nil),
	}
	app.Win = window.New(th)
	app.Overlay = window.New(th)
	return app
}

// TestLauncher_NewSession_CallsOnNewSession verifies that launcherSelect()
// invokes the OnNewSession callback.
func TestLauncher_NewSession_CallsOnNewSession(t *testing.T) {
	app := newTestAppWithLauncher(t)
	called := false
	app.OnNewSession = func() *workspace.NewSessionResult {
		called = true
		return nil
	}

	cmd := app.launcherSelect()

	if !called {
		t.Fatal("OnNewSession was not called")
	}
	if cmd != nil {
		t.Fatal("expected nil cmd from launcherSelect")
	}
}

// TestLauncher_AddOrchestrator_CreatesAgent verifies AddOrchestrator clears
// the launcher, creates the agent, and wires channels.
func TestLauncher_AddOrchestrator_CreatesAgent(t *testing.T) {
	app := newTestAppWithLauncher(t)
	inputCh, btwCh, cancelCh, blockResolveCh := newTestChannels()

	app.AddOrchestrator("ses_1_orch_1", inputCh, btwCh, cancelCh, blockResolveCh)

	if app.launcher != nil {
		t.Fatal("launcher should be cleared by AddOrchestrator")
	}
	n := app.AgentMgr.Get("ses_1_orch_1")
	if n == nil {
		t.Fatal("orchestrator agent should exist")
	}
	if n.State == nil || n.State.MsgList == nil {
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
		t.Fatal("blockResolve channel should be wired")
	}
}

// TestLauncher_NewSession_CreatesOrchestrator verifies that launcherSelect()
// with OnNewSession returning a result creates the orchestrator agent.
func TestLauncher_NewSession_CreatesOrchestrator(t *testing.T) {
	app := newTestAppWithLauncher(t)
	inputCh, btwCh, cancelCh, blockResolveCh := newTestChannels()

	app.OnNewSession = func() *workspace.NewSessionResult {
		return &workspace.NewSessionResult{
			OrchID:         "ses_1_orch_1",
			InputCh:        inputCh,
			BtwCh:          btwCh,
			CancelCh:       cancelCh,
			BlockResolveCh: blockResolveCh,
		}
	}

	app.launcherSelect()

	if app.launcher != nil {
		t.Fatal("launcher should be cleared after creating orchestrator")
	}
	if app.AgentMgr.Get("ses_1_orch_1") == nil {
		t.Fatal("orchestrator agent should exist")
	}
	if app.AgentMgr.ActiveID() != "ses_1_orch_1" {
		t.Fatalf("expected ActiveID ses_1_orch_1, got %q", app.AgentMgr.ActiveID())
	}
}

// TestLauncher_NewSession_WithoutOnNewSession verifies graceful fallback.
func TestLauncher_NewSession_WithoutOnNewSession(t *testing.T) {
	app := newTestAppWithLauncher(t)
	app.OnNewSession = nil

	cmd := app.launcherSelect()

	if app.launcher == nil {
		t.Fatal("launcher should still be present when no callback")
	}
	if cmd != nil {
		t.Fatal("expected nil cmd when OnNewSession is nil")
	}
}

// TestLauncher_FileOpen_NoSessionCreated verifies that opening a file
// does NOT create any agent.
func TestLauncher_FileOpen_NoSessionCreated(t *testing.T) {
	app := newTestAppWithLauncher(t)

	msg := tea.KeyPressMsg{Code: tea.KeyEnter}
	app.Update(msg)

	if app.launcher == nil {
		t.Fatal("launcher should not be cleared by non-session events")
	}
	if app.AgentMgr.Count() != 0 {
		t.Fatal("no agents should be created by file-open events")
	}
}

// TestLauncher_ResumeSession verifies that selecting an existing session
// calls OnResumeSession and triggers quit.
func TestLauncher_ResumeSession(t *testing.T) {
	app := newTestAppWithLauncher(t)

	app.launcher.sessions = []sessiondb.SessionMeta{
		{ID: "ses_test_1", ProjectName: "test-project"},
	}
	app.launcher.selected = 1

	resumeCalled := false
	app.OnResumeSession = func(id string) {
		resumeCalled = true
		if id != "ses_test_1" {
			t.Fatalf("expected resume ID ses_test_1, got %q", id)
		}
	}

	cmd := app.launcherSelect()

	if !resumeCalled {
		t.Fatal("OnResumeSession should be called for existing session")
	}
	if cmd == nil || !app.Quit {
		t.Fatal("launcherSelect should trigger quit for resume")
	}
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
	if subAgent.State == nil || subAgent.State.MsgList == nil {
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

// TestLauncher_NoSessions_NewSessionOnly verifies the launcher shows
// "Create Orchestrator" even when there are no existing sessions.
func TestLauncher_NoSessions_NewSessionOnly(t *testing.T) {
	app := newTestAppWithLauncher(t)
	app.launcher = NewLauncherState(nil)

	if app.launcher.TotalItems() != 1 {
		t.Fatalf("expected 1 item (New Session), got %d", app.launcher.TotalItems())
	}
	if app.launcher.SelectedSessionID() != "" {
		t.Fatal("SelectedSessionID should be empty for 'New Session'")
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
