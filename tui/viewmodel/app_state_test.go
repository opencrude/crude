package viewmodel

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/provider"
	"crude/tui/input"
	"crude/tui/theme"
	"crude/tui/widgets"
	"crude/tui/window"
)

func TestModeName_Chat(t *testing.T) {
	if modeName(ModeChat) != "chat" {
		t.Fatalf("expected 'chat', got '%s'", modeName(ModeChat))
	}
}

func TestFocusTarget_Launcher(t *testing.T) {
	app := newTestApp(t)

	target := app.focusTarget()
	if target != FocusLauncher {
		t.Fatalf("expected FocusLauncher with no agents, got %v", target)
	}
}

func TestFocusTarget_InputWithAgent(t *testing.T) {
	app := newTestApp(t)
	app.agents["ses_1_orch_1"] = &AgentState{}
	app.activeAgentView = "ses_1_orch_1"

	target := app.focusTarget()
	if target != FocusInput {
		t.Fatalf("expected FocusInput with agent, got %v", target)
	}
}

func TestActiveMsgList_Empty(t *testing.T) {
	app := newTestApp(t)
	ml := app.activeMsgList()
	if ml != nil {
		t.Fatal("expected nil activeMsgList with no agents")
	}
}

func TestActiveMsgList_WithAgent(t *testing.T) {
	app := newTestApp(t)
	th := theme.DefaultDark()
	ml := widgets.NewMessageList(th)
	app.agents["ses_1_orch_1"] = &AgentState{MsgList: ml}
	app.activeAgentView = "ses_1_orch_1"

	result := app.activeMsgList()
	if result != ml {
		t.Fatal("expected activeMsgList to return the agent's MsgList")
	}
}

func TestActiveMessageList_Fallback(t *testing.T) {
	app := newTestApp(t)
	th := theme.DefaultDark()
	ml := widgets.NewMessageList(th)
	app.agents["ses_1_orch_1"] = &AgentState{MsgList: ml}
	// activeAgentView is empty, should fallback to first agent's MsgList

	result := app.activeMessageList()
	if result != ml {
		t.Fatal("expected activeMessageList to fallback to first agent's MsgList")
	}
}

func TestActiveMessageList_NilWhenEmpty(t *testing.T) {
	app := newTestApp(t)
	result := app.activeMessageList()
	if result != nil {
		t.Fatal("expected nil with no agents")
	}
}

func TestAgentForView_NoAgents(t *testing.T) {
	app := newTestApp(t)
	a := app.agentForView()
	if a != nil {
		t.Fatal("expected nil agentForView with no agents")
	}
}

func TestAgentForView_WithActiveAgentView(t *testing.T) {
	app := newTestApp(t)
	agent := &AgentState{Streaming: true}
	app.agents["ses_1_orch_1"] = agent
	app.activeAgentView = "ses_1_orch_1"

	a := app.agentForView()
	if a != agent {
		t.Fatal("expected agentForView to return the active agent")
	}
	if !a.Streaming {
		t.Fatal("expected streaming to be true")
	}
}

// buildMainView nil-activeML guard tests

func TestBuildMainView_NilActiveML(t *testing.T) {
	app := newFullTestApp(t)
	app.width = 80
	app.height = 24

	// Add an agent but set activeAgentView to empty, so
	// activeMessageList() returns nil (the fallback iterates
	// but we can also simulate by having an agent with nil MsgList).
	app.agents["ses_1_orch_1"] = &AgentState{MsgList: nil}
	app.activeAgentView = "ses_1_orch_1"

	// This should not panic — the nil activeML guard in buildMainView
	// should produce empty content instead of dereferencing nil.
	out := app.buildMainView()
	if out == "" {
		t.Fatal("expected non-empty output from buildMainView")
	}
}

func TestBuildMainView_NilActiveML_EmptyAgents(t *testing.T) {
	app := newFullTestApp(t)
	app.width = 80
	app.height = 24

	// No agents, no editors, no launcher → the else-if for
	// len(m.agents)==0 handles this, producing empty chat.
	app.launcher = nil

	out := app.buildMainView()
	if out == "" {
		t.Fatal("expected non-empty output from buildMainView")
	}
}

func TestBuildMainView_LauncherFallback(t *testing.T) {
	app := newFullTestApp(t)
	app.width = 80
	app.height = 24

	// Launcher present, no agents → launcher content shown
	app.launcher = NewLauncherState(nil)

	out := app.buildMainView()
	if out == "" {
		t.Fatal("expected non-empty output from buildMainView with launcher")
	}
}

// handleQuit tests

func TestHandleQuit_NoTabs(t *testing.T) {
	app := newTestApp(t)

	_, cmd := app.handleQuit()
	if cmd == nil || !app.Quit {
		t.Fatal("expected Quit when no tabs")
	}
}

func TestHandleQuit_ClosesLastAgentTab(t *testing.T) {
	app := newFullTestApp(t)
	app.width = 80
	app.height = 24

	app.AddOrchestrator("ses_1_orch_1", make(chan provider.UserInput, 8), make(chan provider.UserInput, 8), make(chan struct{}, 1), make(chan string, 1))
	app.AddOrchestrator("ses_1_orch_2", make(chan provider.UserInput, 8), make(chan provider.UserInput, 8), make(chan struct{}, 1), make(chan string, 1))

	if len(app.agents) != 2 {
		t.Fatalf("expected 2 agents, got %d", len(app.agents))
	}

	// handleQuit quits the program regardless of remaining tabs
	_, cmd := app.handleQuit()

	if cmd == nil {
		t.Fatal("expected Quit cmd from handleQuit")
	}
	if !app.Quit {
		t.Fatal("expected Quit flag")
	}
}

func TestHandleQuit_LastTabRefreshesLauncher(t *testing.T) {
	app := newFullTestApp(t)
	app.width = 80
	app.height = 24

	app.AddOrchestrator("ses_1_orch_1", make(chan provider.UserInput, 8), make(chan provider.UserInput, 8), make(chan struct{}, 1), make(chan string, 1))
	app.launcher = nil // simulate launcher being cleared

	// Close last tab
	_, cmd := app.handleQuit()
	if cmd != nil {
		t.Fatal("expected nil cmd")
	}
	// launcher should be refreshed since onListSessions was not set,
	// but this test verifies no panic occurs.
}

func TestHandleQuit_ClosesOverlayFirst(t *testing.T) {
	app := newTestApp(t)
	app.overlay.OpenTab("Settings", "", true)
	app.Mode = ModeSettings

	_, cmd := app.handleQuit()
	if cmd != nil {
		t.Fatal("expected nil cmd after closing overlay")
	}
	if app.Mode != ModeChat {
		t.Fatal("expected ModeChat after closing overlay")
	}
}

// handleAgentTabClose tests

func TestHandleAgentTabClose_InvalidIndex(t *testing.T) {
	app := newTestApp(t)
	cmd := app.handleAgentTabClose(-1)
	if cmd != nil {
		t.Fatal("expected nil cmd for invalid index")
	}

	cmd = app.handleAgentTabClose(0)
	if cmd != nil {
		t.Fatal("expected nil cmd for index out of range")
	}
}

func TestHandleAgentTabClose_NonClosableTab(t *testing.T) {
	app := newFullTestApp(t)
	app.width = 80
	app.height = 24

	// Open a non-closable tab
	app.win.OpenTab("fixed", "🔒", false)

	cmd := app.handleAgentTabClose(0)
	if cmd != nil {
		t.Fatal("expected nil cmd for non-closable tab")
	}
}

func TestHandleAgentTabClose_AgentTab(t *testing.T) {
	app := newFullTestApp(t)
	app.width = 80
	app.height = 24

	app.AddOrchestrator("ses_1_orch_1", make(chan provider.UserInput, 8), make(chan provider.UserInput, 8), make(chan struct{}, 1), make(chan string, 1))

	if len(app.agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(app.agents))
	}

	cmd := app.handleAgentTabClose(0)
	if cmd != nil {
		t.Fatal("expected nil cmd after closing agent tab")
	}
	if len(app.agents) != 0 {
		t.Fatal("expected agents to be empty after closing last tab")
	}
}

// submitInput tests

func TestSubmitInput_NonexistentAgent(t *testing.T) {
	app := newTestApp(t)
	cmd := app.submitInput("nonexistent", "hello", nil)
	if cmd != nil {
		t.Fatal("expected nil cmd for nonexistent agent")
	}
}

func TestSubmitInput_NoChannel(t *testing.T) {
	app := newFullTestApp(t)
	app.width = 80
	app.height = 24
	th := theme.DefaultDark()
	ml := widgets.NewMessageList(th)
	app.agents["ses_1_orch_1"] = &AgentState{MsgList: ml}

	cmd := app.submitInput("ses_1_orch_1", "hello", nil)
	// cmd is non-nil even without a channel — the returned closure
	// uses select-with-default so it won't block.
	if cmd == nil {
		t.Fatal("expected non-nil cmd (returns a closure)")
	}
}

func TestSubmitInput_WithChannel(t *testing.T) {
	app := newFullTestApp(t)
	app.width = 80
	app.height = 24
	th := theme.DefaultDark()
	ml := widgets.NewMessageList(th)
	app.agents["ses_1_orch_1"] = &AgentState{MsgList: ml}
	inputCh := make(chan provider.UserInput, 8)
	app.sessionInputChans["ses_1_orch_1"] = inputCh

	cmd := app.submitInput("ses_1_orch_1", "hello", nil)
	if cmd == nil {
		t.Fatal("expected non-nil cmd when channel is wired")
	}
}

func TestSubmitBtw_NonexistentAgent(t *testing.T) {
	app := newTestApp(t)
	cmd := app.submitBtw("nonexistent", "hello", nil)
	if cmd != nil {
		t.Fatal("expected nil cmd for nonexistent agent")
	}
}

// removeOrchestrator tests

func TestRemoveOrchestrator_CleansUp(t *testing.T) {
	app := newTestApp(t)
	app.agents["ses_1_orch_1"] = &AgentState{}
	app.sessionInputChans["ses_1_orch_1"] = make(chan provider.UserInput, 8)
	app.sessionBtwChans["ses_1_orch_1"] = make(chan provider.UserInput, 8)
	app.sessionCancelChans["ses_1_orch_1"] = make(chan struct{}, 1)
	app.sessionBlockResolveCh["ses_1_orch_1"] = make(chan string, 1)
	app.subAgentParents["sub_1"] = "ses_1_orch_1"

	app.RemoveOrchestrator("ses_1_orch_1")

	if _, ok := app.agents["ses_1_orch_1"]; ok {
		t.Fatal("agent should be removed")
	}
	if _, ok := app.sessionInputChans["ses_1_orch_1"]; ok {
		t.Fatal("input channel should be removed")
	}
	if _, ok := app.subAgentParents["sub_1"]; ok {
		t.Fatal("sub-agent parent relationship should be cleaned up")
	}
}

// syncAgentWindowTabs tests

func TestSyncAgentWindowTabs_Empty(t *testing.T) {
	app := newTestApp(t)
	// Should not panic
	app.syncAgentWindowTabs()
}

// classifyMouse tests

func TestClassifyMouse_NoAgents_NoEditor(t *testing.T) {
	app := newFullTestApp(t)
	app.width = 80
	app.height = 24

	region := app.classifyMouse(10, 10, false)
	if region != regionInput {
		t.Fatalf("expected regionInput with no agents, got %v", region)
	}
}

func TestClassifyMouse_WithAgent(t *testing.T) {
	app := newFullTestApp(t)
	app.width = 80
	app.height = 24
	app.chatAreaHeight = 17 // set as buildMainView would

	app.AddOrchestrator("ses_1_orch_1", make(chan provider.UserInput, 8), make(chan provider.UserInput, 8), make(chan struct{}, 1), make(chan string, 1))

	region := app.classifyMouse(10, 10, false)
	if region != regionChat {
		t.Fatalf("expected regionChat with agent, got %v", region)
	}
}

func TestClassifyMouse_InputArea(t *testing.T) {
	app := newFullTestApp(t)
	app.width = 80
	app.height = 24
	app.chatAreaHeight = 17

	app.AddOrchestrator("ses_1_orch_1", make(chan provider.UserInput, 8), make(chan provider.UserInput, 8), make(chan struct{}, 1), make(chan string, 1))

	// y=24 is in the input area (chatH=17 + 4 + tabBarExtra=2 = 23)
	region := app.classifyMouse(10, 24, false)
	if region != regionInput {
		t.Fatalf("expected regionInput for input area, got %v", region)
	}
}

// handleInputEvent tests

func TestHandleInputEvent_SubmitWithActiveAgentView(t *testing.T) {
	app := newFullTestApp(t)
	app.width = 80
	app.height = 24

	th := theme.DefaultDark()
	ml := widgets.NewMessageList(th)
	app.agents["ses_1_orch_1"] = &AgentState{MsgList: ml}
	app.activeAgentView = "ses_1_orch_1"
	inputCh := make(chan provider.UserInput, 8)
	app.sessionInputChans["ses_1_orch_1"] = inputCh

	cmds := app.handleInputEvent(input.SubmitEvent{Text: "hello"})
	if len(cmds) != 1 {
		t.Fatalf("expected 1 cmd, got %d", len(cmds))
	}
}

func TestHandleInputEvent_SubmitEmptyAgentView(t *testing.T) {
	app := newFullTestApp(t)
	app.width = 80
	app.height = 24

	cmds := app.handleInputEvent(input.SubmitEvent{Text: "hello"})
	if len(cmds) != 1 {
		t.Fatalf("expected 1 cmd, got %d", len(cmds))
	}
	// submitInput with empty activeAgentView should return nil cmd
	// (agent lookup fails), but handleInputEvent still appends it.
}

func TestHandleInputEvent_BtwSubmit(t *testing.T) {
	app := newFullTestApp(t)
	app.width = 80
	app.height = 24

	th := theme.DefaultDark()
	ml := widgets.NewMessageList(th)
	app.agents["ses_1_orch_1"] = &AgentState{MsgList: ml}
	app.activeAgentView = "ses_1_orch_1"
	btwCh := make(chan provider.UserInput, 8)
	app.sessionBtwChans["ses_1_orch_1"] = btwCh

	cmds := app.handleInputEvent(input.BtwSubmitEvent{Text: "btw text"})
	if len(cmds) != 1 {
		t.Fatalf("expected 1 cmd, got %d", len(cmds))
	}
}

func TestHandleInputEvent_SlashCommand(t *testing.T) {
	// handleInputEvent always appends the result of executeSlashCommand
	// to the cmds slice. Even if it returns nil, the slice gets a nil entry.
	app := newFullTestApp(t)
	cmds := app.handleInputEvent(input.SlashCommandEvent{Command: ""})
	// cmds has 1 entry (nil cmd) — verify no panic
	if len(cmds) != 1 {
		t.Fatalf("expected 1 cmd entry (nil), got %d", len(cmds))
	}
	if cmds[0] != nil {
		t.Fatal("expected nil cmd for empty slash command")
	}
}

func TestHandleInputEvent_RefOpen(t *testing.T) {
	app := newTestApp(t)
	cmds := app.handleInputEvent(input.RefOpenEvent{ID: "test-ref"})

	// Should not panic and return 0 cmds
	if len(cmds) != 0 {
		t.Fatalf("expected 0 cmds, got %d", len(cmds))
	}
}

func TestHandleInputEvent_SettingsOpen(t *testing.T) {
	app := newTestApp(t)
	cmds := app.handleInputEvent(input.SettingsOpenEvent{})
	if len(cmds) != 0 {
		t.Fatalf("expected 0 cmds, got %d", len(cmds))
	}
	if app.Mode != ModeSettings {
		t.Fatal("expected ModeSettings after SettingsOpenEvent")
	}
}

func TestCloseEditorTab_NoEditorOpen(t *testing.T) {
	app := newTestApp(t)
	app.closeEditorTab()
}

func TestDispatchChatKey_Escape(t *testing.T) {
	app := newFullTestApp(t)
	app.width = 80
	app.height = 24

	var cmds []tea.Cmd
	msg := tea.KeyPressMsg{Code: tea.KeyEsc}
	_, cmd := app.dispatchChatKey(msg, &cmds)
	if cmd != nil {
		t.Fatal("expected nil cmd for escape with no agents")
	}
}

func TestDispatchChatKey_CtrlC(t *testing.T) {
	app := newTestApp(t)
	app.width = 80
	app.height = 24

	var cmds []tea.Cmd
	msg := tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	_, cmd := app.dispatchChatKey(msg, &cmds)
	// ctrl+c without active selection returns nil cmd
	if cmd != nil {
		t.Fatal("expected nil cmd for ctrl+c with no selection")
	}
}

func newTestApp(t *testing.T) *App {
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
	}

	app.win = window.New(th)
	app.overlay = window.New(th)

	return app
}

func newFullTestApp(t *testing.T) *App {
	t.Helper()
	th := theme.DefaultDark()
	im := input.NewModel(th)
	sb := widgets.NewStatusBar(th)

	app := &App{
		theme:                th,
		Mode:                 ModeChat,
		width:                80,
		height:               24,
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
		input:                im,
		statusbar:            sb,
		spinFrame:            0,
	}

	app.win = window.New(th)
	app.overlay = window.New(th)

	return app
}

var _ tea.Model = (*App)(nil)
