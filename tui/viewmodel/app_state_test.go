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
	app.agentMgr.AddOrchestrator("ses_1_orch_1", nil, nil, nil, nil, &AgentState{})

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
	app.agentMgr.AddOrchestrator("ses_1_orch_1", nil, nil, nil, nil, &AgentState{MsgList: ml})

	result := app.activeMsgList()
	if result != ml {
		t.Fatal("expected activeMsgList to return the agent's MsgList")
	}
}

func TestActiveMessageList_Fallback(t *testing.T) {
	app := newTestApp(t)
	th := theme.DefaultDark()
	ml := widgets.NewMessageList(th)
	app.agentMgr.AddOrchestrator("ses_1_orch_1", nil, nil, nil, nil, &AgentState{MsgList: ml})
	// AddOrchestrator sets active automatically; clear it so activeMessageList falls back
	app.agentMgr.ClearActive()

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
	app.agentMgr.AddOrchestrator("ses_1_orch_1", nil, nil, nil, nil, agent)

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

	// Add an agent with nil MsgList. AddOrchestrator sets it active,
	// so activeMsgList() returns nil (the node exists but MsgList is nil).
	app.agentMgr.AddOrchestrator("ses_1_orch_1", nil, nil, nil, nil, &AgentState{MsgList: nil})

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

	if app.agentMgr.Count() != 1 {
		t.Fatalf("expected 1 agent, got %d", app.agentMgr.Count())
	}

	// First handleQuit closes the tab and shows launcher
	_, cmd := app.handleQuit()
	if cmd != nil {
		t.Fatal("expected nil cmd (launcher shown)")
	}
	if app.agentMgr.Count() != 0 {
		t.Fatal("expected agent to be cleaned up")
	}

	// Second handleQuit quits since there's nothing left
	_, cmd = app.handleQuit()
	if cmd == nil {
		t.Fatal("expected Quit cmd from second handleQuit")
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

// TestHandleTabSwitch_ClearsStaleViewOnLastClose is a regression
// test for the bug where closing the last editor tab left
// activeEditorView pointing at the now-closed path. The stale
// value caused (a) the renderer to fall through to the input
// widget branch instead of the launcher, and (b) Ctrl+Q to attempt
// a no-op close of the stale path instead of quitting the app.
//
// handleTabSwitch is the single source of truth for these view
// fields, so it must clear them whenever the underlying tab is
// gone — not just when the active tab transitions between types.
func TestHandleTabSwitch_ClearsStaleViewOnLastClose(t *testing.T) {
	app := newTestApp(t)

	// No tab → both views empty.
	app.handleTabSwitch()
	if app.editorState.ActiveView != "" {
		t.Fatalf("expected activeEditorView=\"\" with no tab, got %q", app.editorState.ActiveView)
	}
	if app.agentMgr.ActiveID() != "" {
		t.Fatalf("expected ActiveID=\"\" with no tab, got %q", app.agentMgr.ActiveID())
	}
}

// TestHandleQuit_AfterLastEditorCloses_Quits is a regression test
// for the Ctrl+Q doesn't quit bug. Before the fix, closing the
// last editor tab left activeEditorView stale, so handleQuit saw
// an "open" editor and tried to close it again (no-op), never
// reaching the Quit path.
func TestHandleQuit_AfterLastEditorCloses_Quits(t *testing.T) {
	app := newTestApp(t)

	// Simulate "had an editor open, then closed it" by populating
	// the views as if a tab existed, then calling handleTabSwitch
	// with no tab to force the reset path.
	app.editorState.ActiveView = "/tmp/foo.go"

	app.handleTabSwitch()

	if app.editorState.ActiveView != "" {
		t.Fatalf("setup: handleTabSwitch should have cleared activeEditorView")
	}

	// Now Ctrl+Q should quit since there's no editor, no agent, no
	// floating window, no overlay.
	_, cmd := app.handleQuit()
	if cmd == nil {
		t.Fatal("expected Quit cmd after closing last editor tab and pressing Ctrl+Q")
	}
	if !app.Quit {
		t.Fatal("expected Quit flag to be set")
	}
}

func TestHandleQuit_ClosesOverlayFirst(t *testing.T) {
	app := newTestApp(t)
	app.overlay.OpenTab("Settings", "", true)
	app.overlayState.Set(ModeSettings)

	_, cmd := app.handleQuit()
	if cmd != nil {
		t.Fatal("expected nil cmd after closing overlay")
	}
	if !app.overlayState.IsChat() {
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

	if app.agentMgr.Count() != 1 {
		t.Fatalf("expected 1 agent, got %d", app.agentMgr.Count())
	}

	cmd := app.handleAgentTabClose(0)
	if cmd != nil {
		t.Fatal("expected nil cmd after closing agent tab")
	}
	if app.agentMgr.Count() != 0 {
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
	app.agentMgr.AddOrchestrator("ses_1_orch_1", nil, nil, nil, nil, &AgentState{MsgList: ml})

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
	inputCh := make(chan provider.UserInput, 8)
	app.agentMgr.AddOrchestrator("ses_1_orch_1", inputCh, nil, nil, nil, &AgentState{MsgList: ml})

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
	app.agentMgr.AddOrchestrator("ses_1_orch_1", nil, nil, nil, nil, &AgentState{})

	app.RemoveOrchestrator("ses_1_orch_1")

	if n := app.agentMgr.Get("ses_1_orch_1"); n != nil {
		t.Fatal("agent should be removed from agentMgr")
	}
	if p := app.paneMgr.Get("ses_1_orch_1"); p != nil {
		t.Fatal("agent should be removed from paneMgr")
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

	// With no launcher (e.g. during the very first frame before the
	// launcher is created), classifyMouse falls through to regionInput
	// — same as the old behavior, so we don't regress.
	region := app.classifyMouse(10, 10, false)
	if region != regionInput {
		t.Fatalf("expected regionInput with no agents and no launcher, got %v", region)
	}
}

// TestClassifyMouse_NoAgents_NoEditor_WithLauncher verifies the new
// behavior: when the launcher is on-screen, clicks in the main area
// are classified as regionLauncher (not regionInput). This is the
// fix for "click on New Session doesn't work".
func TestClassifyMouse_NoAgents_NoEditor_WithLauncher(t *testing.T) {
	app := newFullTestApp(t)
	app.width = 80
	app.height = 24
	app.chatAreaHeight = 17
	app.launcher = NewLauncherState(nil)

	region := app.classifyMouse(10, 10, false)
	if region != regionLauncher {
		t.Fatalf("expected regionLauncher with launcher present, got %v", region)
	}
}

// TestLauncherState_HitTest exercises the launcher's hit-test for
// the New Session entry. Line 0 = top border, line 1 = content
// (clickable), line 2 = bottom border, line 3+ = transition/sessions.
func TestLauncherState_HitTest(t *testing.T) {
	st := NewLauncherState(nil)
	// New Session box = 4 lines (top border, 2 content, bottom border).
	// ALL FOUR lines are clickable (item 0) — the whole bordered box
	// is one button.
	for line := 0; line <= 3; line++ {
		if got := st.HitTest(line); got != 0 {
			t.Errorf("line %d should map to item 0, got %d", line, got)
		}
	}
	// Out of range.
	if got := st.HitTest(100); got != -1 {
		t.Errorf("line 100 (out of range) should not be clickable, got %d", got)
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
	inputCh := make(chan provider.UserInput, 8)
	app.agentMgr.AddOrchestrator("ses_1_orch_1", inputCh, nil, nil, nil, &AgentState{MsgList: ml})

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
	btwCh := make(chan provider.UserInput, 8)
	app.agentMgr.AddOrchestrator("ses_1_orch_1", nil, btwCh, nil, nil, &AgentState{MsgList: ml})

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
	if !app.overlayState.Is(ModeSettings) {
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
		Workspace: &Workspace{
			theme:        th,
			overlayState: NewOverlayState(),
			agentMgr:     NewAgentManager(),
			paneMgr:      NewPaneManager(),
			tree:         NewTreeState(nil, nil, 30),
			focusStack:   NewFocusStack(),
			startTime:    time.Now(),
			lspPopup:     NewLSPPopupState(),
			editorState:  NewEditorState(),
		},
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
		Workspace: &Workspace{
			theme:        th,
			overlayState: NewOverlayState(),
			agentMgr:     NewAgentManager(),
			paneMgr:      NewPaneManager(),
			tree:         NewTreeState(nil, nil, 30),
			focusStack:   NewFocusStack(),
			startTime:    time.Now(),
			input:        im,
			statusbar:    sb,
			spinFrame:    0,
			lspPopup:     NewLSPPopupState(),
			editorState:  NewEditorState(),
		},
		width:  80,
		height: 24,
	}

	app.win = window.New(th)
	app.overlay = window.New(th)

	return app
}

var _ tea.Model = (*App)(nil)
