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
			Logger:        &testLogger{},
		},
		OverlayState:   NewOverlayState(),
		LSPPopup:       NewLSPPopupState(),
		EditorState:    NewEditorState(),
		launcher:       NewLauncherState(".", nil),
		actionRegistry: ui.DefaultRegistry(),
	}
	app.Win = window.New(th)
	app.Overlay = window.New(th)
	return app
}

// TestLauncher_NewSession_NoBackendConfig verifies that launcherSelect()
// does not clear the launcher when no backend config is set.
func TestLauncher_NewSession_NoBackendConfig(t *testing.T) {
	app := newTestAppWithLauncher(t)

	cmd := app.launcherSelect()

	if app.launcher == nil {
		t.Fatal("launcher should still be present when no backend config")
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

// TestLauncher_AddOrchestrator_ClearsLauncher verifies that AddOrchestrator
// clears the launcher and creates the agent.
func TestLauncher_AddOrchestrator_ClearsLauncher(t *testing.T) {
	app := newTestAppWithLauncher(t)
	inputCh, btwCh, cancelCh, blockResolveCh := newTestChannels()

	app.AddOrchestrator("ses_1_orch_1", inputCh, btwCh, cancelCh, blockResolveCh)

	if app.launcher != nil {
		t.Fatal("launcher should be cleared by AddOrchestrator")
	}
	if app.AgentMgr.Get("ses_1_orch_1") == nil {
		t.Fatal("orchestrator agent should exist")
	}
	if app.AgentMgr.ActiveID() != "ses_1_orch_1" {
		t.Fatalf("expected ActiveID ses_1_orch_1, got %q", app.AgentMgr.ActiveID())
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
// resumes in-place (does NOT quit the process).
func TestLauncher_ResumeSession(t *testing.T) {
	app := newTestAppWithLauncher(t)

	app.launcher.sessions = []sessiondb.SessionMeta{
		{ID: "ses_test_1", ProjectName: "test-project"},
	}
	app.launcher.rebuildItems()
	// Select the session item if compatibility callers provide one.
	for i, it := range app.launcher.items {
		if it.Kind == widgets.LauncherItemSession {
			app.launcher.selected = i
			break
		}
	}

	cmd := app.launcherSelect()

	// Without a real backend the resume fails gracefully — but must not quit.
	if app.Quit {
		t.Fatal("launcherSelect must not quit for in-place resume")
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

// TestLauncher_NoSessions_StartActionsOnly verifies the welcome launcher
// always has Start actions even with no sessions/workspaces.
func TestLauncher_NoSessions_StartActionsOnly(t *testing.T) {
	app := newTestAppWithLauncher(t)
	app.launcher = NewLauncherState(".", nil)

	if app.launcher.TotalItems() < 2 {
		t.Fatalf("expected at least 2 start items, got %d", app.launcher.TotalItems())
	}
	if app.launcher.SelectedSessionID() != "" {
		t.Fatal("SelectedSessionID should be empty for default selection")
	}
	it := app.launcher.SelectedItem()
	if it == nil || it.Kind != widgets.LauncherItemNewSession {
		t.Fatal("default selection should be New Session")
	}
}

func TestLauncherWorkspaceSearchKeepsActionsFixed(t *testing.T) {
	launcher := &LauncherState{
		cwdPath: "/projects/current",
		workspaces: []workspace.WorkspaceInfo{
			{ProjectName: "alpha", ProjectDir: "/projects/alpha"},
			{ProjectName: "beta", ProjectDir: "/projects/beta"},
		},
		selected: 0,
	}
	launcher.rebuildItems()
	launcher.activateFilter()
	launcher.updateFilter("beta")

	if launcher.TotalItems() != 4 {
		t.Fatalf("search should not remove fixed actions, got %d items", launcher.TotalItems())
	}
	if len(launcher.filtered) != 1 || launcher.items[launcher.filtered[0]].Workspace.Name != "beta" {
		t.Fatalf("expected only beta in workspace results, got indices %v", launcher.filtered)
	}
	if !launcher.workspaceVisible(launcher.selected) {
		t.Fatalf("selection should move into filtered workspace results, selected=%d", launcher.selected)
	}
	if !strings.Contains(launcher.HelpView(nil), "type search") {
		t.Fatalf("active search help missing: %q", launcher.HelpView(nil))
	}
}

func TestLauncherWheelOnlyScrollsWorkspacePane(t *testing.T) {
	app := newTestAppWithLauncher(t)
	app.width = 120
	app.height = 30
	app.ChatAreaHeight = 20
	app.Tree = &workspace.TreeState{}
	app.launcher.workspaces = make([]workspace.WorkspaceInfo, 30)
	for i := range app.launcher.workspaces {
		app.launcher.workspaces[i] = workspace.WorkspaceInfo{
			ProjectName: fmt.Sprintf("workspace-%02d", i),
			ProjectDir:  fmt.Sprintf("/projects/workspace-%02d", i),
		}
	}
	app.launcher.rebuildItems()
	layout := app.computeLayout()
	paint := widgets.RenderWelcomeLauncher(
		app.launcher.cwdPath,
		app.launcher.items,
		app.launcher.selected,
		0,
		false,
		"",
		app.Theme,
		layout.InnerW,
		app.ChatAreaHeight,
		0,
		0,
		nil,
	)
	app.launcher.SetPaint(paint)

	rightX := layout.SidebarW + layout.PadLR + paint.WorkspacePaneX0 + 1
	searchY := layout.ChatAreaTop + paint.WorkspaceSearchY
	app.handleLauncherClick(rightX, searchY)
	if !app.launcher.filterActive {
		t.Fatal("clicking the workspace search row should focus search")
	}

	listY := layout.ChatAreaTop + paint.WorkspaceListY0
	app.dispatchMouseWheel(tea.MouseWheelMsg{X: rightX, Y: listY, Button: tea.MouseWheelDown})
	if app.launcher.workspaceScroll == 0 {
		t.Fatal("wheel inside workspace viewport should scroll results")
	}

	app.launcher.workspaceScroll = 0
	leftX := layout.SidebarW + layout.PadLR + 1
	app.dispatchMouseWheel(tea.MouseWheelMsg{X: leftX, Y: listY, Button: tea.MouseWheelDown})
	if app.launcher.workspaceScroll != 0 {
		t.Fatal("wheel over fixed left help/actions must not scroll workspace results")
	}
}

func TestLauncherUsesMainPaneWithoutHelpStrip(t *testing.T) {
	app := newFullTestApp(t)
	app.width = 100
	app.height = 30
	app.launcher = NewLauncherState(".", nil)

	view := app.buildMainView()
	if strings.Contains(view, "↑↓ navigate") {
		t.Fatalf("launcher help should not consume a main-pane row:\n%s", view)
	}
	if app.ChatAreaHeight != 25 {
		t.Fatalf("launcher should receive reclaimed separator/help rows: height=%d, want 25", app.ChatAreaHeight)
	}
}

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
