package viewmodel

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/provider"
	"crude/sessiondb"
	"crude/tui/chatprojection"
	"crude/tui/input"
	"crude/tui/screens/confirmation"
	filepickerscreen "crude/tui/screens/filepicker"
	"crude/tui/screens/launcher"
	"crude/tui/settings"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/tui/wm"
	ttypes "crude/types"
	"crude/workspace"
)

func TestModeName_Chat(t *testing.T) {
	if modeName(ModeChat) != "chat" {
		t.Fatalf("expected 'chat', got '%s'", modeName(ModeChat))
	}
}

func TestFocusTarget_Launcher(t *testing.T) {
	app := newTestApp(t)
	app.width = 80
	app.height = 30
	_ = app.computeContentLayout()
	if app.ChatAreaHeight < 3 {
		t.Fatalf("launcher visibility setup: ChatAreaHeight=%d", app.ChatAreaHeight)
	}

	target := app.focusTarget()
	if target != FocusLauncher {
		t.Fatalf("expected FocusLauncher with visible launcher, got %v", target)
	}
}

func TestFocusTarget_InputWithAgent(t *testing.T) {
	app := newTestApp(t)
	app.ws.AgentMgr().AddOrchestrator("ses_1_orch_1", &workspace.AgentState{})

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
	ml := chatprojection.NewRenderer(th)
	sp := app.ws.PaneMgr().GetOrCreate("ses_1_orch_1", workspace.PaneAgent, "ses_1_orch_1")
	proj := app.subPaneProjections.Get("ses_1_orch_1", workspace.PaneAgent)
	proj.renderer = ml
	app.ws.AgentMgr().AddOrchestrator("ses_1_orch_1", &workspace.AgentState{Messages: sp.Messages})
	app.AgentView.SetActive("ses_1_orch_1")

	result := app.activeMsgList()
	if result != ml {
		t.Fatal("expected activeMsgList to return the agent's MsgList")
	}
}

func TestActiveMessageList_Fallback(t *testing.T) {
	app := newTestApp(t)
	th := theme.DefaultDark()
	ml := chatprojection.NewRenderer(th)
	sp := app.ws.PaneMgr().GetOrCreate("ses_1_orch_1", workspace.PaneAgent, "ses_1_orch_1")
	proj := app.subPaneProjections.Get("ses_1_orch_1", workspace.PaneAgent)
	proj.renderer = ml
	app.ws.AgentMgr().AddOrchestrator("ses_1_orch_1", &workspace.AgentState{Messages: sp.Messages})
	app.AgentView.SetActive("ses_1_orch_1")
	// The fixture selects the shell explicitly; clear it so activeMessageList falls back
	app.AgentView.ClearActive()

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
	agent := &workspace.AgentState{Streaming: true}
	app.ws.AgentMgr().AddOrchestrator("ses_1_orch_1", agent)

	a := app.agentForView()
	if a != agent {
		t.Fatal("expected agentForView to return the active agent")
	}
	if !a.Streaming {
		t.Fatal("expected streaming to be true")
	}
}

// computeContentLayout nil-activeML guard tests

func TestComputeContentLayout_NilActiveML(t *testing.T) {
	app := newFullTestApp(t)
	app.width = 80
	app.height = 24

	// Add an agent with nil MsgList. Select the shell explicitly,
	// so activeMsgList() returns nil (the node exists but MsgList is nil).
	app.ws.AgentMgr().AddOrchestrator("ses_1_orch_1", &workspace.AgentState{Messages: nil})

	// This should not panic: the nil activeML guard in computeContentLayout
	// should produce a chat layout instead of dereferencing nil.
	lay := app.computeContentLayout()
	if lay.ChatH < 1 {
		t.Fatal("expected a valid chat layout height")
	}
}

func TestComputeContentLayout_NilActiveML_EmptyAgents(t *testing.T) {
	app := newFullTestApp(t)
	app.width = 80
	app.height = 24

	// No agents, no editors, no launcher → the else-if for
	// len(m.agents)==0 handles this, producing empty chat.
	app.launcherScreen = nil

	lay := app.computeContentLayout()
	if lay.ChatH < 1 {
		t.Fatal("expected a valid chat layout height")
	}
}

func TestComputeContentLayout_LauncherFallback(t *testing.T) {
	app := newFullTestApp(t)
	app.width = 80
	app.height = 24

	// Launcher present, no agents → the launcher slot is active and the
	// content layout only reserves the chat rows.
	_ = app.computeContentLayout()
	if app.launcherScreen == nil {
		t.Fatal("launcher screen should still be present")
	}
	if !app.launcherHostVisible() {
		t.Fatal("launcher slot should be active with no tabs")
	}
	if app.launcherScreen.TotalItems() < 2 {
		t.Fatalf("expected at least 2 launcher items, got %d", app.launcherScreen.TotalItems())
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

	app.AddOrchestrator("ses_1_orch_1")

	if app.ws.AgentMgr().Count() != 1 {
		t.Fatalf("expected 1 agent, got %d", app.ws.AgentMgr().Count())
	}

	// First handleQuit closes the tab and shows launcher. The agent is
	// minimized, not destroyed; its orchestrator keeps running.
	_, cmd := app.handleQuit()
	if cmd != nil {
		t.Fatal("expected nil cmd (launcher shown)")
	}
	if app.AgentView.VisibleCount() != 0 {
		t.Fatal("expected no surfaced agents after closing the last tab")
	}
	if !app.AgentView.IsHidden("ses_1_orch_1") {
		t.Fatal("closing a tab must minimize the agent, not terminate it")
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

// TestHandleQuit_TwoAgentTabs_ClosesActiveOnly is the regression test for
// Ctrl+Q hard-exiting the app while an agent tab was active with another
// agent tab open. The window-tabs branch of handleQuit must consume the
// quit after closing the active tab (minimizing its agent); the tea.Quit
// fall-through is only reachable when there are genuinely no window tabs
// to close, mirroring handleAgentTabClose's never-quit ladder.
func TestHandleQuit_TwoAgentTabs_ClosesActiveOnly(t *testing.T) {
	app := newFullTestApp(t)
	app.width = 80
	app.height = 24

	app.AddOrchestrator("ses_1_orch_1")
	app.AddOrchestrator("ses_2_orch_1")

	if app.ws.AgentMgr().Count() != 2 {
		t.Fatalf("expected 2 agents, got %d", app.ws.AgentMgr().Count())
	}
	if !app.Win.IsOpen() || app.Win.TabCount() != 2 {
		t.Fatalf("expected 2 window tabs, got %d (open=%v)", app.Win.TabCount(), app.Win.IsOpen())
	}
	activeIdx := app.Win.ActiveIndex()
	if activeIdx < 0 {
		t.Fatal("expected an active window tab")
	}
	activeTitle := app.Win.Tabs()[activeIdx].Title
	if !app.isAgentTabTitle(activeTitle) {
		t.Fatalf("expected active tab to be an agent tab, got %q", activeTitle)
	}
	activeID := app.agentIDFromTabTitle(activeTitle)

	// First Ctrl+Q: close only the active agent tab, with no quit.
	_, cmd := app.handleQuit()
	if cmd != nil {
		t.Fatal("expected nil cmd (no tea.Quit) while another agent tab remains")
	}
	if app.Quit {
		t.Fatal("expected app.Quit to stay false after closing the active tab")
	}
	if !app.AgentView.IsHidden(activeID) {
		t.Fatalf("active agent %q must be minimized, not terminated", activeID)
	}
	if app.ws.AgentMgr().Get(activeID) == nil {
		t.Fatal("closing a tab must keep the agent alive for restore")
	}
	if !app.Win.IsOpen() || app.Win.TabCount() != 1 {
		t.Fatalf("expected remaining tab to stay open, got %d tabs (open=%v)", app.Win.TabCount(), app.Win.IsOpen())
	}
	if app.AgentView.VisibleCount() != 1 {
		t.Fatalf("expected 1 surfaced agent, got %d", app.AgentView.VisibleCount())
	}

	// Second Ctrl+Q: the last agent tab closes and the launcher shows,
	// still without quitting.
	_, cmd = app.handleQuit()
	if cmd != nil {
		t.Fatal("expected nil cmd (launcher shown) after closing the last tab")
	}
	if app.Quit {
		t.Fatal("expected app.Quit to stay false while the launcher is shown")
	}
	if app.Win.IsOpen() || app.Win.TabCount() != 0 {
		t.Fatalf("expected all window tabs closed, got %d", app.Win.TabCount())
	}
	if app.AgentView.VisibleCount() != 0 {
		t.Fatalf("expected no surfaced agents, got %d", app.AgentView.VisibleCount())
	}
	if app.launcherScreen == nil {
		t.Fatal("expected launcher to be shown after the last tab closed")
	}

	// Third Ctrl+Q with nothing surfaced and no sessions: quit.
	_, cmd = app.handleQuit()
	if cmd == nil {
		t.Fatal("expected Quit cmd when no tabs and no sessions remain")
	}
	if !app.Quit {
		t.Fatal("expected Quit flag")
	}
}

func TestHandleQuit_LastTabRefreshesLauncher(t *testing.T) {
	app := newFullTestApp(t)
	app.width = 80
	app.height = 24

	app.AddOrchestrator("ses_1_orch_1")
	app.launcherScreen = nil // simulate launcher being cleared

	// Close last tab
	_, cmd := app.handleQuit()
	if cmd != nil {
		t.Fatal("expected nil cmd")
	}
	// openLauncher recreates the screen; this test verifies no panic occurs.
	if app.launcherScreen == nil {
		t.Fatal("expected launcher screen to be recreated")
	}
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
// gone, not just when the active tab transitions between types.
func TestHandleTabSwitch_ClearsStaleViewOnLastClose(t *testing.T) {
	app := newTestApp(t)

	// No tab → both views empty.
	app.handleTabSwitch()
	if app.EditorState.ActiveView != "" {
		t.Fatalf("expected activeEditorView=\"\" with no tab, got %q", app.EditorState.ActiveView)
	}
	if app.AgentView.ActiveID() != "" {
		t.Fatalf("expected ActiveID=\"\" with no tab, got %q", app.AgentView.ActiveID())
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
	app.EditorState.ActiveView = "/tmp/foo.go"

	app.handleTabSwitch()

	if app.EditorState.ActiveView != "" {
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
	selectDeckSession(t, app, app.ActiveSession, "agent")
	app.openSessionSettings()
	projection := app.activeSessionDeck()
	if projection == nil || !projection.deck.IsOpen() {
		t.Fatal("session settings deck did not open")
	}

	_, cmd := app.handleQuit()
	if cmd != nil {
		t.Fatal("expected nil cmd after closing overlay")
	}
	if projection.deck.IsOpen() {
		t.Fatal("expected session deck to close")
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
	app.Win.OpenTab("fixed", "🔒", false)

	cmd := app.handleAgentTabClose(0)
	if cmd != nil {
		t.Fatal("expected nil cmd for non-closable tab")
	}
}

func TestHandleAgentTabClose_AgentTab(t *testing.T) {
	app := newFullTestApp(t)
	app.width = 80
	app.height = 24

	app.AddOrchestrator("ses_1_orch_1")

	if app.ws.AgentMgr().Count() != 1 {
		t.Fatalf("expected 1 agent, got %d", app.ws.AgentMgr().Count())
	}

	cmd := app.handleAgentTabClose(0)
	if cmd != nil {
		t.Fatal("expected nil cmd after closing agent tab")
	}
	if app.AgentView.VisibleCount() != 0 {
		t.Fatal("expected no surfaced agents after closing last tab")
	}
	if app.ws.AgentMgr().Get("ses_1_orch_1") == nil {
		t.Fatal("closing a tab must keep the agent alive for restore")
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

func TestSubmitInputRejectsAgentWithoutBackendOwner(t *testing.T) {
	app := newFullTestApp(t)
	app.width = 80
	app.height = 24
	app.AddOrchestrator("ses_1_orch_1")

	cmd := app.submitInput("ses_1_orch_1", "hello", nil)
	if cmd != nil {
		t.Fatal("direct rejected admission should not return a command")
	}
	if got := app.ws.AgentMgr().Get("ses_1_orch_1").State.Messages.Messages(); len(got) != 0 {
		t.Fatalf("rejected input leaked into live conversation: %+v", got)
	}
}

func TestSubmitInputUsesWorkspaceTurnAdmission(t *testing.T) {
	app := newFullTestApp(t)
	app.width = 80
	app.height = 24
	agentID := addBackendTestOrchestrator(t, app)

	cmd := app.submitInput(agentID, "hello", nil)
	if cmd == nil {
		t.Fatal("durable turn admission must run off Update")
	}
	msg, ok := cmd().(inputAdmissionMsg)
	if !ok {
		t.Fatalf("admission returned %T", cmd())
	}
	_ = app.applyInputAdmission(msg)
	messages := app.ws.AgentMgr().Get(agentID).State.Messages.Messages()
	if len(messages) != 2 || messages[0].Content != "hello" || messages[1].Role != workspace.MessageRoleStatus {
		t.Fatalf("admitted conversation=%+v", messages)
	}
}

func TestPreparedInputStopCommitsOnceWithoutClearingNewDraft(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 80, 24
	agentID := addBackendTestOrchestrator(t, app)
	app.ActiveSession = app.ws.SessionsByAgent()[agentID]
	app.Input.SetInput("original input")
	draftRevision := app.Input.DraftRevision()

	command := app.submitPreparedInput(agentID, provider.PlainInput("original input"), draftRevision)
	if command == nil {
		t.Fatal("prepare did not return an off-UI commit command")
	}
	app.ws.CancelAgent(agentID)
	app.Input.SetReferenceStore(app.ActiveSession.RefStore)
	app.Input.SetInput("newer draft\nline\nline\nline\nline\nline\nline\nline\nline\n")
	ref := sessiondb.Reference{ID: "new-draft-evidence", Kind: sessiondb.KindPastedText, Label: "[Pasted Text1]", Content: "new draft evidence"}
	app.ActiveSession.RefStore.AddExisting(ref)
	app.Input.InsertReference(ref)
	undoText := app.Input.Value()
	app.Input, _, _ = app.Input.Update(tea.KeyPressMsg{Code: '!', Text: "!"}, false, false)
	app.Input.LayoutShell(80, 4)
	app.Input.SetCursorFlat(3)
	app.Input.SetSelection(2, 6)
	app.Input.SetScrollOffset(1)
	text, revision, cursor, scroll := app.Input.Value(), app.Input.DraftRevision(), app.Input.CursorFlat(), app.Input.ScrollOffset()
	from, to := app.Input.InputSelectionRange()
	message, ok := command().(inputAdmissionMsg)
	if !ok {
		t.Fatalf("commit returned %T", command())
	}
	if message.result.Status != workspace.InputAdmissionPreservationPending {
		t.Fatalf("status=%q err=%v", message.result.Status, message.result.Err)
	}
	_ = app.applyInputAdmission(message)
	afterFrom, afterTo := app.Input.InputSelectionRange()
	if app.Input.Value() != text || app.Input.DraftRevision() != revision || app.Input.CursorFlat() != cursor ||
		app.Input.ScrollOffset() != scroll || afterFrom != from || afterTo != to ||
		len(app.Input.SnapshotReferenceIDs()) != 1 || app.Input.SnapshotReferenceIDs()[0] != ref.ID {
		t.Fatal("late receipt changed newer text, references, cursor, selection or scroll")
	}
	messages := app.ws.AgentMgr().Get(agentID).State.Messages.Messages()
	if len(messages) != 1 || messages[0].InternalID != message.result.Receipt.InputID || messages[0].Content != "original input" {
		t.Fatalf("saved input projection=%+v receipt=%+v", messages, message.result.Receipt)
	}
	_ = app.applyInputAdmission(message)
	messages = app.ws.AgentMgr().Get(agentID).State.Messages.Messages()
	if len(messages) != 1 {
		t.Fatalf("duplicate receipt appended another user row: %+v", messages)
	}
	state, found, err := sessiondb.LoadContextState(app.ActiveSession.DB, app.ActiveSession.SessionID, app.ActiveSession.SessionID)
	if err != nil || !found {
		t.Fatalf("load future context: found=%v err=%v", found, err)
	}
	count := 0
	for _, contextMessage := range state.Messages {
		if contextMessage.InternalID == message.result.Receipt.InputID {
			count++
			if contextMessage.ContentString() != "original input" {
				t.Fatal("Stop changed accepted input in future context")
			}
		}
	}
	if count != 1 {
		t.Fatalf("accepted input appears %d times in future context", count)
	}
	app.Input, _, _ = app.Input.Update(tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl}, false, false)
	if app.Input.Value() != undoText {
		t.Fatal("Stop/receipt consumed the newer draft's undo history")
	}
}

func TestPreparedInputStopRejectsForeignOrStaleReceipt(t *testing.T) {
	for _, field := range []string{"session", "agent", "input", "draft", "epoch", "generation", "workspace"} {
		t.Run(field, func(t *testing.T) {
			app := newFullTestApp(t)
			agentID := addBackendTestOrchestrator(t, app)
			app.ActiveSession = app.ws.SessionsByAgent()[agentID]
			app.Input.SetInput("exact submitted draft")
			command := app.submitPreparedInput(agentID, provider.PlainInput(app.Input.Value()), app.Input.DraftRevision())
			if command == nil {
				t.Fatal("prepare did not schedule commit")
			}
			app.ws.CancelAgent(agentID)
			message := command().(inputAdmissionMsg)
			if message.result.Status != workspace.InputAdmissionPreservationPending {
				t.Fatalf("commit=%+v", message.result)
			}
			switch field {
			case "session":
				message.result.Receipt.SessionID = "foreign"
			case "agent":
				message.result.Receipt.AgentID = "foreign"
			case "input":
				message.result.Receipt.InputID = "foreign"
			case "draft":
				message.result.Receipt.DraftRevision++
			case "epoch":
				message.result.Receipt.OwnerEpoch++
			case "generation":
				message.result.Receipt.TurnGeneration++
			case "workspace":
				app.workspaceSwitchGen.Add(1)
			}
			app.applyInputAdmission(message)
			if app.Input.Value() != "exact submitted draft" || len(app.ws.AgentMgr().Get(agentID).State.Messages.Messages()) != 0 {
				t.Fatal("foreign/stale receipt changed the composer or conversation")
			}
		})
	}
}

func TestPreparedInputStopReceiptCannotClearSiblingAgentDraft(t *testing.T) {
	app := newFullTestApp(t)
	agentID := addBackendTestOrchestrator(t, app)
	app.ActiveSession = app.ws.SessionsByAgent()[agentID]
	app.Input.SetInput("submitted draft")
	command := app.submitPreparedInput(agentID, provider.PlainInput(app.Input.Value()), app.Input.DraftRevision())
	if command == nil {
		t.Fatal("prepare did not schedule commit")
	}
	app.ws.CancelAgent(agentID)
	message := command().(inputAdmissionMsg)
	app.ws.AgentMgr().AddSubAgent("sibling", agentID, "Sibling", "test", "researcher", workspace.NewAgentState("sibling", nil, nil))
	app.AgentView.SetHidden("sibling", false)
	app.AgentView.SetActive("sibling")
	app.applyInputAdmission(message)
	if app.Input.Value() != "submitted draft" {
		t.Fatal("receipt cleared a different agent's composer in the same session")
	}
	if len(app.ws.AgentMgr().Get(agentID).State.Messages.Messages()) != 1 || len(app.ws.AgentMgr().Get("sibling").State.Messages.Messages()) != 0 {
		t.Fatal("receipt did not project into its exact owning agent")
	}
}

func TestPreparedInputStopLateAcceptedReceiptCannotReactivate(t *testing.T) {
	app := newFullTestApp(t)
	agentID := addBackendTestOrchestrator(t, app)
	app.ActiveSession = app.ws.SessionsByAgent()[agentID]
	app.Input.SetInput("submitted draft")
	command := app.submitPreparedInput(agentID, provider.PlainInput(app.Input.Value()), app.Input.DraftRevision())
	if command == nil {
		t.Fatal("prepare did not schedule commit")
	}
	app.ws.CancelAgent(agentID)
	message := command().(inputAdmissionMsg)
	// Dispatch acceptance may precede Stop while its UI acknowledgement is queued.
	message.result.Status = workspace.InputAdmissionAccepted
	state := app.ws.AgentMgr().Get(agentID).State
	state.BeginCancellation()
	app.applyInputAdmission(message)
	count := 0
	for _, row := range state.Messages.Messages() {
		if row.Role == ttypes.RoleUser && row.Content == "submitted draft" {
			count++
		}
	}
	if state.IsActive() || !state.Cancelling || count != 1 {
		t.Fatalf("late receipt: active=%v cancelling=%v userRows=%d", state.IsActive(), state.Cancelling, count)
	}
}

func TestPreparedInputStopReceiptCannotClearRestoredDraftWithSameRevision(t *testing.T) {
	app := newFullTestApp(t)
	agentID := addBackendTestOrchestrator(t, app)
	app.ActiveSession = app.ws.SessionsByAgent()[agentID]
	app.Input.SetInput("submitted draft")
	command := app.submitPreparedInput(agentID, provider.PlainInput(app.Input.Value()), app.Input.DraftRevision())
	if command == nil {
		t.Fatal("prepare did not schedule commit")
	}
	app.ws.CancelAgent(agentID)
	message := command().(inputAdmissionMsg)
	app.Input.ClearInput()
	app.Input.SetInput("restored unsubmitted draft")
	app.Input.SetDraftRevision(message.draftRevision)
	app.applyInputAdmission(message)
	if app.Input.Value() != "restored unsubmitted draft" {
		t.Fatal("receipt consumed a different draft lifetime with a restored revision")
	}
	if rows := app.ws.AgentMgr().Get(agentID).State.Messages.Messages(); len(rows) != 1 || rows[0].Content != "submitted draft" {
		t.Fatalf("receipt lost its accepted input: %+v", rows)
	}
}

func TestPreparedInputStopFloatingComposerKeepsSeparateAndNewerDrafts(t *testing.T) {
	for _, change := range []string{"unchanged", "cursor", "selection", "scroll", "edit-undo", "reopened"} {
		t.Run(change, func(t *testing.T) {
			app := newFullTestApp(t)
			agentID := addBackendTestOrchestrator(t, app)
			app.ActiveSession = app.ws.SessionsByAgent()[agentID]
			app.Input.SetInput("separate shell draft")
			_, _ = app.toggleComposer()
			screen := app.sessionComposer()
			screen.SetValue("submitted text\nline\nline\nline\nline\nline\nline\nline\n")
			var editor *ui.TextArea
			for _, child := range screen.Children() {
				if provider, ok := child.(ui.TextEditorProvider); ok {
					editor, _ = provider.TextEditor().(*ui.TextArea)
					break
				}
			}
			if editor == nil {
				t.Fatal("composer editor missing")
			}
			editor.Layout(ui.LayoutContext{}, ui.Rect{W: 40, H: 3})
			command := app.submitComposerInput(agentID, screen.Value())
			if command == nil {
				t.Fatal("composer did not schedule durable admission")
			}
			app.ws.CancelAgent(agentID)
			switch change {
			case "cursor":
				editor.SetCursorFlat(2)
			case "selection":
				editor.SetSelection(1, 4)
			case "scroll":
				editor.SetScrollOffset(0)
			case "edit-undo":
				editor.InsertString("!")
				editor.Handle(ui.EventContext{}, ui.KeyEvent(tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl}))
			case "reopened":
				app.overlayHost.Close(ui.OverlayComposer)
				_, _ = app.toggleComposer()
				screen.SetValue("new unsubmitted floating draft")
			}
			before := editor.EditState()
			text := screen.Value()
			_ = app.applyInputAdmission(command().(inputAdmissionMsg))
			if app.Input.Value() != "separate shell draft" {
				t.Fatal("floating composer receipt consumed the separate shell draft")
			}
			if change == "unchanged" {
				if app.overlayHost.IsOpen(ui.OverlayComposer) {
					t.Fatal("receipt did not close its unchanged submitted composer")
				}
			} else if !app.overlayHost.IsOpen(ui.OverlayComposer) || editor.EditState() != before || screen.Value() != text {
				t.Fatal("receipt closed or changed a newer/reopened floating draft")
			}
		})
	}
}

func TestSubmitSteering_NonexistentAgent(t *testing.T) {
	app := newTestApp(t)
	cmd := app.submitSteering("nonexistent", "hello", nil)
	if cmd != nil {
		t.Fatal("expected nil cmd for nonexistent agent")
	}
}

func TestRejectedSteeringKeepsDraftAndDoesNotFabricateHumanEvidence(t *testing.T) {
	app := newFullTestApp(t)
	app.AddOrchestrator("unbound")
	app.Input.SetInput("unsubmitted exact draft")
	app.Input.SetCursorFlat(3)
	app.Input.SetSelection(1, 5)
	revision, cursor := app.Input.DraftRevision(), app.Input.CursorFlat()
	if cmd := app.submitSteering("unbound", app.Input.Value(), nil); cmd != nil {
		t.Fatal("rejected steering scheduled persistence")
	}
	from, to := app.Input.InputSelectionRange()
	if app.Input.Value() != "unsubmitted exact draft" || app.Input.DraftRevision() != revision ||
		app.Input.CursorFlat() != cursor || from != 1 || to != 5 {
		t.Fatal("rejected steering changed the unsubmitted editor state")
	}
	if rows := app.ws.AgentMgr().Get("unbound").State.Messages.Messages(); len(rows) != 0 {
		t.Fatalf("rejected steering fabricated human evidence: %+v", rows)
	}
}

func TestCollaborationDeliverySettlementClearsPendingStatus(t *testing.T) {
	app := newFullTestApp(t)
	conversation := workspace.NewConversation()
	first := conversation.AppendPendingSteering(workspace.Message{Role: workspace.MessageRoleUser, Content: "first"})
	second := conversation.AppendPendingSteering(workspace.Message{Role: workspace.MessageRoleUser, Content: "second"})
	app.ws.AgentMgr().AddOrchestrator("agent", workspace.NewAgentState("agent", conversation, nil))
	app.handleAgentDomainEvent(workspace.AgentEvent{Type: "collaboration_delivery_admitted", AgentID: "agent", Data: map[string]any{"history_message_id": first}})
	if got := conversation.PendingSteeringCount(); got != 1 {
		t.Fatalf("pending human steering count=%d want 1", got)
	}
	app.handleAgentDomainEvent(workspace.AgentEvent{Type: "collaboration_delivery_rejected", AgentID: "agent", Data: map[string]any{"history_message_id": second}})
	if got := conversation.PendingSteeringCount(); got != 0 {
		t.Fatalf("pending human steering count=%d want 0", got)
	}
}

func TestStoppedSteeringSettlesInChatWithoutTouchingNewDraft(t *testing.T) {
	app := newFullTestApp(t)
	owner := workspace.NewSession("owner", t.TempDir(), nil, nil)
	other := workspace.NewSession("other", t.TempDir(), nil, nil)
	app.ActiveSession = owner
	wsOf(app).SessionsByAgent = map[string]*workspace.Session{"agent": owner}
	conversation := workspace.NewConversation()
	first := conversation.AppendPendingSteering(workspace.Message{Role: workspace.MessageRoleUser, Content: "first note"})
	second := conversation.AppendPendingSteering(workspace.Message{Role: workspace.MessageRoleUser, Content: "second note"})
	app.ws.AgentMgr().AddOrchestrator("agent", workspace.NewAgentState("agent", conversation, nil))
	app.Input.SetInput("newer draft")
	app.handleAgentDomainEvent(workspace.AgentEvent{Type: "collaboration_delivery_rejected", AgentID: "agent", Data: map[string]any{"history_message_id": first}})
	app.handleAgentDomainEvent(workspace.AgentEvent{Type: "collaboration_delivery_rejected", AgentID: "agent", Data: map[string]any{"history_message_id": second}})
	if got := app.Input.Value(); got != "newer draft" {
		t.Fatalf("stopped delivery changed newer draft = %q", got)
	}
	if messages := conversation.Messages(); len(messages) != 2 || messages[0].Content != "first note" || messages[1].Content != "second note" || messages[0].PendingSteering || messages[1].PendingSteering {
		t.Fatalf("stopped notes not retained as settled context: %+v", messages)
	}

	third := conversation.AppendPendingSteering(workspace.Message{Role: workspace.MessageRoleUser, Content: "background note"})
	app.ActiveSession = other
	app.Input.SetInput("other session draft")
	app.handleAgentDomainEvent(workspace.AgentEvent{Type: "collaboration_delivery_rejected", AgentID: "agent", Data: map[string]any{"history_message_id": third}})
	if got := app.Input.Value(); got != "other session draft" {
		t.Fatalf("failed note leaked into wrong session: %q", got)
	}
	app.ActiveSession = owner
	app.maybeHydrateComposerDraft()
	if got := app.Input.Value(); got != "" {
		t.Fatalf("session switch restored submitted context into composer: %q", got)
	}
}

func TestCancelledPendingSteeringSettlesInPlaceAndIsIdempotent(t *testing.T) {
	app := newFullTestApp(t)
	owner := workspace.NewSession("owner", t.TempDir(), nil, nil)
	app.ActiveSession = owner
	wsOf(app).SessionsByAgent = map[string]*workspace.Session{"agent": owner}
	conversation := workspace.NewConversation()
	id := conversation.AppendPendingSteering(workspace.Message{Role: workspace.MessageRoleUser, Content: "cancel me"})
	app.ws.AgentMgr().AddOrchestrator("agent", workspace.NewAgentState("agent", conversation, nil))
	app.Input.SetInput("typed while waiting")
	clear := workspace.AgentEvent{Type: "collaboration_delivery_rejected", AgentID: "agent", Data: map[string]any{"history_message_id": id}}
	app.handleAgentDomainEvent(clear)
	app.handleAgentDomainEvent(clear)
	if got := app.Input.Value(); got != "typed while waiting" {
		t.Fatalf("cancel settlement changed draft: %q", got)
	}
	if got := conversation.PendingSteeringCount(); got != 0 {
		t.Fatalf("cancel left %d blinking notes", got)
	}
}

// removeOrchestrator tests

func TestRemoveOrchestrator_CleansUp(t *testing.T) {
	app := newTestApp(t)
	app.ws.AgentMgr().AddOrchestrator("ses_1_orch_1", &workspace.AgentState{})
	app.subPaneProjections.Get("ses_1_orch_1", workspace.PaneAgent)

	app.RemoveOrchestrator("ses_1_orch_1")

	if n := app.ws.AgentMgr().Get("ses_1_orch_1"); n != nil {
		t.Fatal("agent should be removed from agentMgr")
	}
	if p := app.ws.PaneMgr().Get("ses_1_orch_1"); p != nil {
		t.Fatal("agent should be removed from paneMgr")
	}
	if app.subPaneProjections.Has("ses_1_orch_1") {
		t.Fatal("subPane projection should be removed when orchestrator closes")
	}

}

func TestAddOrchestratorKeepsExistingSessionReachable(t *testing.T) {
	app := newFullTestApp(t)
	app.AddOrchestrator("first")
	app.AddOrchestrator("second")

	if app.ws.AgentMgr().Get("first") == nil || app.ws.AgentMgr().Get("second") == nil {
		t.Fatal("adding a second session hid the first orchestrator")
	}
	if got := app.ws.AgentMgr().Count(); got != 2 {
		t.Fatalf("agent count = %d, want 2", got)
	}
}

func TestHandleAgentEventDropsUnknownOrchestrator(t *testing.T) {
	app := newFullTestApp(t)
	app.handleAgentEvent(ttypes.AgentEventMsg{Type: "done", AgentID: "stale", Data: "done"})
	if app.ws.AgentMgr().Get("stale") != nil {
		t.Fatal("stale event created an orchestrator without live channels")
	}
}

func TestHandleSubPaneCompletionIgnoresRemovedAgent(t *testing.T) {
	app := newFullTestApp(t)
	app.handleSubPaneCompletion("removed")
}

func TestHandleCancelConfirmationStopsTurnWithoutDenyingPermission(t *testing.T) {
	app := newFullTestApp(t)
	selectDeckSession(t, app, app.ActiveSession, "agent")
	state := app.ws.AgentMgr().Get("agent").State
	projection := app.activeSessionProjection()
	openTestAgentPrompt(t, app, projection, agentPromptRequest{question: "Continue?"})
	projection.deckHost.Resize(80, 12)
	projection.deckHost.Layout()
	projection.deckHost.Focus(confirmation.ActionsID)
	cmd := app.handleSessionDeckEvent(tea.KeyPressMsg{Code: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("confirmation escape did not emit a result")
	}
	app.handleAppMessages(cmd())

	if !state.Cancelling {
		t.Fatal("Escape did not transition the owning agent to cancelling")
	}
}

// syncAgentWindowTabs tests

func TestSyncAgentWindowTabs_Empty(t *testing.T) {
	app := newTestApp(t)
	// Should not panic
	app.syncAgentWindowTabs()
}

// classifyShellRegion tests

func TestClassifyShellRegion_NoAgents_NoEditor(t *testing.T) {
	app := newFullTestApp(t)
	app.width = 80
	app.height = 24

	// With no launcher (e.g. during the very first frame before the
	// launcher is created), classifyShellRegion falls through to regionInput
	// This matches the old behavior, so we don't regress.
	region := app.classifyShellRegion(10, 10, false)
	if region != regionInput {
		t.Fatalf("expected regionInput with no agents and no launcher, got %v", region)
	}
}

// TestClassifyShellRegion_NoAgents_NoEditor_WithLauncher verifies the new
// behavior: when the launcher is on-screen, clicks in the main area
// are classified as regionLauncher (not regionInput). This is the
// fix for "click on New Session doesn't work".
func TestClassifyShellRegion_NoAgents_NoEditor_WithLauncher(t *testing.T) {
	app := newFullTestApp(t)
	app.width = 80
	app.height = 24
	app.ChatAreaHeight = 17

	region := app.classifyShellRegion(10, 10, false)
	if region != regionLauncher {
		t.Fatalf("expected regionLauncher with launcher present, got %v", region)
	}
}

// TestLauncherState_HitTest exercises full-card hit testing on the
// native welcome launcher. The hero card bounds are registered in the
// component host and clickable across all of its rows.
func TestLauncherState_HitTest(t *testing.T) {
	screen := launcher.New(theme.DefaultDark())
	screen.SetData(".", nil)
	host := ui.NewHost(theme.DefaultDark(), screen)
	screen.SetFocusHandler(host.Focus)
	host.Resize(80, 24)
	host.Layout()

	rect, _ := host.ScreenRect(launcher.NewSessionID)
	id := host.HitTest(rect.X+1, rect.Y)
	if id != launcher.NewSessionID {
		t.Fatalf("expected hero card hit at (40,1), got %q", id)
	}

	// Clicking the hero card should activate it.
	res := host.Handle(tea.MouseClickMsg{X: rect.X + 1, Y: rect.Y, Button: tea.MouseLeft})
	if res.Cmd == nil {
		t.Fatal("click on hero card should produce an activation command")
	}
	if _, ok := res.Cmd().(launcher.ActivateMsg); !ok {
		t.Fatalf("expected ActivateMsg, got %#v", res.Cmd())
	}

	if id := host.HitTest(1000, 1); id != ui.RootID {
		t.Fatalf("out of range should miss the component tree, got %q", id)
	}
}

func TestClassifyMouse_WithAgent(t *testing.T) {
	app := newFullTestApp(t)
	app.width = 80
	app.height = 24
	app.ChatAreaHeight = 17 // set as computeContentLayout would

	app.AddOrchestrator("ses_1_orch_1")

	region := app.classifyShellRegion(10, 10, false)
	if region != regionChat {
		t.Fatalf("expected regionChat with agent, got %v", region)
	}
}

func TestClassifyMouse_InputArea(t *testing.T) {
	app := newFullTestApp(t)
	app.width = 80
	app.height = 24
	app.ChatAreaHeight = 17

	app.AddOrchestrator("ses_1_orch_1")

	// y=24 is in the input area (chatH=17 + 4 + tabBarExtra=2 = 23)
	region := app.classifyShellRegion(10, 24, false)
	if region != regionInput {
		t.Fatalf("expected regionInput for input area, got %v", region)
	}
}

// handleInputEvent tests

func TestHandleInputEvent_SubmitWithActiveAgentView(t *testing.T) {
	app := newFullTestApp(t)
	app.width = 80
	app.height = 24

	app.AddOrchestrator("ses_1_orch_1")

	cmds := app.handleInputEvent(input.SubmitEvent{Text: "hello"})
	if len(cmds) != 1 {
		t.Fatalf("expected one durable admission command, got %d cmds", len(cmds))
	}
}

func TestHandleInputEvent_SubmitEmptyAgentView(t *testing.T) {
	app := newFullTestApp(t)
	app.width = 80
	app.height = 24

	cmds := app.handleInputEvent(input.SubmitEvent{Text: "hello"})
	if len(cmds) != 0 {
		t.Fatalf("rejected pre-local send must not queue work, got %d cmds", len(cmds))
	}
	// No guessed fallback target is appended.
}

func TestHandleInputEvent_SteeringSubmit(t *testing.T) {
	app := newFullTestApp(t)
	app.width = 80
	app.height = 24

	app.AddOrchestrator("ses_1_orch_1")
	app.Input.SetInput("keep this unsubmitted draft")
	revision := app.Input.DraftRevision()
	historyCount := 0
	if app.Prompts != nil {
		historyCount = len(app.Prompts.Entries())
	}

	cmds := app.handleInputEvent(input.SteeringSubmitEvent{Text: "btw text"})
	if len(cmds) != 1 || cmds[0] != nil {
		t.Fatalf("unbound steering must not schedule a history write, got %v", cmds)
	}
	if app.Input.Value() != "keep this unsubmitted draft" || app.Input.DraftRevision() != revision ||
		len(app.ws.AgentMgr().Get("ses_1_orch_1").State.Messages.Messages()) != 0 {
		t.Fatal("rejected steering consumed the draft or fabricated chat evidence")
	}
	if app.Prompts != nil && len(app.Prompts.Entries()) != historyCount {
		t.Fatal("rejected steering was recorded as accepted prompt history")
	}
}

func TestHandleInputEvent_SlashCommand(t *testing.T) {
	// handleInputEvent always appends the result of executeSlashCommand
	// to the cmds slice. Even if it returns nil, the slice gets a nil entry.
	app := newFullTestApp(t)
	cmds := app.handleInputEvent(input.SlashCommandEvent{Command: ""})
	// cmds has 1 entry (nil cmd); verify no panic
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
	selectDeckSession(t, app, app.ActiveSession, "agent")
	cmds := app.handleInputEvent(input.SettingsOpenEvent{})
	if len(cmds) != 0 {
		t.Fatalf("expected 0 cmds, got %d", len(cmds))
	}
	projection := app.activeSessionDeck()
	if projection == nil || !projection.deck.IsOpen() || !projection.deckFocused {
		t.Fatal("expected focused session panel after SettingsOpenEvent")
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

var _ tea.Model = (*App)(nil)

func TestFlatDraggableSettingsAndFilePickerWindows(t *testing.T) {
	app := newFullTestApp(t)
	app.windowManager = wm.NewWindowManager()
	app.overlayHost = ui.NewOverlayHost(app.windowManager)
	app.FilePicker = filepickerscreen.New(app.Theme)
	app.FilePicker.Open(t.TempDir(), filepickerscreen.ModeFolder, nil)

	app.openFilePickerWindow()
	fileWindow := app.windowManager.Window("filepicker")
	if fileWindow == nil || !fileWindow.Decorated() || !fileWindow.Movable() {
		t.Fatal("file picker should have a draggable title bar")
	}
	if !fileWindow.Resizable() {
		t.Fatal("workspace picker should be resizable for deep filesystem navigation")
	}
	if _, ok := fileWindow.Content.(*ui.WindowFrameWidget); !ok {
		t.Fatal("file picker should use ui.WindowFrame chrome")
	}
	fileFrame := fileWindow.Content.(*ui.WindowFrameWidget)
	if fileFrame.Body() != app.FilePicker || fileFrame.Host().Focused() != filepickerscreen.FilterID {
		t.Fatal("file picker should mount and focus its native component body")
	}

	app.openWorkspaceSettings()
	settingsWindow := app.windowManager.Window("workspace-settings")
	if settingsWindow == nil || !settingsWindow.Decorated() || !settingsWindow.Movable() {
		t.Fatal("workspace settings should be a draggable IDE window")
	}
	if !settingsWindow.Resizable() {
		t.Fatal("workspace settings should use unified chrome and remain resizable")
	}
	if _, ok := settingsWindow.Content.(*ui.WindowFrameWidget); !ok {
		t.Fatal("workspace settings should use ui.WindowFrame chrome")
	}
	framed := settingsWindow.Content.(*ui.WindowFrameWidget)
	if _, ok := framed.Body().(*settings.WorkspaceSettingsSurface); !ok {
		t.Fatal("workspace settings should use the native structured shell")
	}
}
