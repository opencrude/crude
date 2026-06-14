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
)

func newTestChannels() (chan<- provider.UserInput, chan<- provider.UserInput, chan<- struct{}, chan string) {
	return make(chan provider.UserInput, 8), make(chan provider.UserInput, 8), make(chan struct{}, 1), make(chan string, 1)
}

func newTestAppWithLauncher(t *testing.T) *App {
	t.Helper()
	th := theme.DefaultDark()
	app := &App{
		theme:                th,
		Mode:                 ModeChat,
		agents:               make(map[string]*AgentState),
		paneMgr:              NewPaneManager(),
		agentLabels:          make(map[string]string),
		agentModels:          make(map[string]string),
		agentCategories:      make(map[string]string),
		sessionInputChans:    make(map[string]chan<- provider.UserInput),
		sessionBtwChans:      make(map[string]chan<- provider.UserInput),
		sessionCancelChans:   make(map[string]chan<- struct{}),
		sessionBlockResolveCh: make(map[string]chan string),
		subAgentParents:       make(map[string]string),
		startTime:            time.Now(),
		launcher:             NewLauncherState(nil),
		input:                input.NewModel(th),
		statusbar:            widgets.NewStatusBar(th),
		spinFrame:            0,
	}
	app.win = window.New(th)
	app.overlay = window.New(th)
	return app
}

// TestLauncher_NewSession_CallsOnNewSession verifies that launcherSelect()
// invokes the OnNewSession callback.
func TestLauncher_NewSession_CallsOnNewSession(t *testing.T) {
	app := newTestAppWithLauncher(t)
	called := false
	app.OnNewSession = func() *NewSessionResult {
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
	agent, ok := app.agents["ses_1_orch_1"]
	if !ok {
		t.Fatal("orchestrator agent should exist")
	}
	if agent.MsgList == nil {
		t.Fatal("orchestrator should have its own MsgList")
	}
	if app.activeAgentView != "ses_1_orch_1" {
		t.Fatalf("expected activeAgentView ses_1_orch_1, got %q", app.activeAgentView)
	}
	if _, ok := app.sessionInputChans["ses_1_orch_1"]; !ok {
		t.Fatal("input channel should be wired")
	}
	if _, ok := app.sessionBtwChans["ses_1_orch_1"]; !ok {
		t.Fatal("btw channel should be wired")
	}
	if _, ok := app.sessionCancelChans["ses_1_orch_1"]; !ok {
		t.Fatal("cancel channel should be wired")
	}
	if _, ok := app.sessionBlockResolveCh["ses_1_orch_1"]; !ok {
		t.Fatal("blockResolve channel should be wired")
	}
}

// TestLauncher_NewSession_CreatesOrchestrator verifies that launcherSelect()
// with OnNewSession returning a result creates the orchestrator agent.
func TestLauncher_NewSession_CreatesOrchestrator(t *testing.T) {
	app := newTestAppWithLauncher(t)
	inputCh, btwCh, cancelCh, blockResolveCh := newTestChannels()

	app.OnNewSession = func() *NewSessionResult {
		return &NewSessionResult{
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
	if _, ok := app.agents["ses_1_orch_1"]; !ok {
		t.Fatal("orchestrator agent should exist")
	}
	if app.activeAgentView != "ses_1_orch_1" {
		t.Fatalf("expected activeAgentView ses_1_orch_1, got %q", app.activeAgentView)
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
	if len(app.agents) != 0 {
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

	subAgent, ok := app.agents["ses_1_orch_1_sub_abc"]
	if !ok {
		t.Fatal("sub-agent should exist in agents map")
	}
	if subAgent.MsgList == nil {
		t.Fatal("sub-agent should have a MsgList")
	}
	sp := app.paneMgr.Get("ses_1_orch_1_sub_abc")
	if sp == nil {
		t.Fatal("sub-agent should have a pane in paneMgr")
	}
	parentID, ok := app.subAgentParents["ses_1_orch_1_sub_abc"]
	if !ok {
		t.Fatal("sub-agent parent should be tracked")
	}
	if parentID != "ses_1_orch_1" {
		t.Fatalf("expected parent ses_1_orch_1, got %q", parentID)
	}
}

// TestMultipleOrchestrators verifies two orchestrators via AddOrchestrator.
func TestMultipleOrchestrators(t *testing.T) {
	app := newTestAppWithLauncher(t)

	inputCh1, btwCh1, cancelCh1, brCh1 := newTestChannels()
	app.AddOrchestrator("ses_1_orch_1", inputCh1, btwCh1, cancelCh1, brCh1)

	inputCh2, btwCh2, cancelCh2, brCh2 := newTestChannels()
	app.AddOrchestrator("ses_1_orch_2", inputCh2, btwCh2, cancelCh2, brCh2)

	if len(app.agents) != 2 {
		t.Fatalf("expected 2 agents, got %d", len(app.agents))
	}
	_, ok1 := app.agents["ses_1_orch_1"]
	_, ok2 := app.agents["ses_1_orch_2"]
	if !ok1 || !ok2 {
		t.Fatal("both orchestrators should exist")
	}

	app.switchToAgentView("ses_1_orch_2")
	if app.activeAgentView != "ses_1_orch_2" {
		t.Fatalf("expected active agent ses_1_orch_2, got %q", app.activeAgentView)
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
