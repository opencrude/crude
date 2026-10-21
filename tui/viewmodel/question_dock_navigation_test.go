package viewmodel

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/screens/question"
	"crude/tui/screens/shellterm"
	"crude/tui/screens/statusoverview"
	"crude/tui/screens/termdock"
	"crude/tui/ui"
	"crude/types"
	"crude/workspace"
)

// These journeys use rendered controls and App.Update, not the deck policy
// helpers, to exercise the chrome that must remain usable while an agent waits.
func questionDockJourneyApp(t *testing.T) (*App, *agentPromptState) {
	t.Helper()
	app, _ := newRealShellApp(t)
	selectDeckSession(t, app, &workspace.Session{SessionID: "question-family"}, "agent")
	_, _ = app.Update(tea.WindowSizeMsg{Width: 160, Height: 50})
	app.Input.SetInput("private composer draft\n  keep its whitespace")
	_, cmd := app.Update(types.AgentMsg(types.AgentEventMsg{AgentID: "agent", Type: "blocking", Data: map[string]any{
		"block_id": "question-navigation", "operation": "questions", "tool": "question",
		"questions_json": `[{"header":"Navigation","question":"Which release should we prepare?"}]`,
	}}))
	runAppCommand(t, app, cmd)
	questionDockPaint(app)
	if len(app.agentPrompts) != 1 || app.agentPrompts[0].question == nil || !app.agentPrompts[0].inline {
		t.Fatal("real blocking event did not mount the owning family's question dock")
	}
	return app, app.agentPrompts[0]
}

func questionDockPaint(app *App) {
	app.forceFrame()
	_ = app.View()
}

func questionDockSend(t *testing.T, app *App, msg tea.Msg) {
	t.Helper()
	_, cmd := app.Update(msg)
	runAppCommand(t, app, cmd)
	questionDockPaint(app)
}

func questionDockClick(t *testing.T, app *App, x, y int) {
	t.Helper()
	questionDockSend(t, app, tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	questionDockSend(t, app, tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft})
}

func questionDockTextPoint(t *testing.T, app *App, rect ui.Rect, text string) (int, int) {
	t.Helper()
	needle := []rune(text)
	for y := rect.Y; y < rect.Y+rect.H; y++ {
		for x := rect.X; x+len(needle) <= rect.X+rect.W; x++ {
			match := true
			for i, r := range needle {
				if app.mainSurf.Buffer().Get(x+i, y).Char != r {
					match = false
					break
				}
			}
			if match {
				return x, y
			}
		}
	}
	t.Fatalf("rendered control %q missing from %+v:\n%s", text, rect, paneJourneyText(app, rect))
	return 0, 0
}

func questionDockClickText(t *testing.T, app *App, rect ui.Rect, text string) {
	t.Helper()
	x, y := questionDockTextPoint(t, app, rect, text)
	questionDockClick(t, app, x, y)
}

func questionDockClickInput(t *testing.T, app *App, prompt *agentPromptState) {
	t.Helper()
	r, ok := prompt.projection.deckHost.ScreenRect(question.InputID)
	if !ok || r.Empty() {
		t.Fatal("question write-in is not mounted")
	}
	questionDockClick(t, app, app.sessionDeckRect.X+r.X, app.sessionDeckRect.Y+r.Y)
	if !prompt.projection.deckFocused {
		t.Fatal("returning to the question did not restore deck keyboard ownership")
	}
}

func questionDockAssertRetained(t *testing.T, app *App, prompt *agentPromptState, answer, draft string) {
	t.Helper()
	if app.promptForScreen(prompt.question) != prompt || !prompt.projection.deck.IsOpen() || prompt.question.Answer(0) != answer {
		t.Fatal("shell navigation discarded the exact pending question or answer")
	}
	if app.Input.Value() != draft {
		t.Fatalf("shell navigation changed the composer: %q", app.Input.Value())
	}
	if node := app.ws.AgentMgr().Get("agent"); node == nil || node.State.PendingBlockID != "question-navigation" {
		t.Fatal("navigation answered, denied, or stopped the pending agent")
	}
}

func TestQuestionDockJourneyWorkspacePanelHideSwitchAndKeys(t *testing.T) {
	app, prompt := questionDockJourneyApp(t)
	draft := app.Input.Value()
	questionDockClickInput(t, app, prompt)
	questionDockSend(t, app, tea.PasteMsg{Content: "release candidate"})
	if !strings.Contains(paneJourneyText(app, app.sessionDeckRect), "release candidate") {
		t.Fatal("question answer did not paint before leaving the dock")
	}
	footer, ok := app.rootHost.ScreenRect(app.footerScreen.ID())
	if !ok {
		t.Fatal("footer is not mounted")
	}
	questionDockClickText(t, app, footer, "Problems")
	if !app.termDockVisible() || app.termDock.ActivePanel() != termdock.PanelProblems || !app.termDockOwnsKeys() {
		t.Fatal("footer did not reveal and focus Problems while a question was pending")
	}
	r, ok := app.rootHost.ScreenRect(statusoverview.SearchID)
	if !ok {
		t.Fatal("Problems search is not mounted")
	}
	questionDockClick(t, app, r.X, r.Y)
	questionDockSend(t, app, tea.KeyPressMsg{Code: 'z', Text: "z"})
	if !strings.Contains(paneJourneyText(app, r), "z") || prompt.question.Answer(0) != "release candidate" {
		t.Fatal("focused workspace-panel keys were stolen by the question")
	}
	questionDockClickText(t, app, footer, "Notices")
	if app.termDock.ActivePanel() != termdock.PanelNotices || !app.termDockVisible() {
		t.Fatal("footer could not switch from Problems to Notices")
	}
	header, ok := app.rootHost.ScreenRect(termdock.HeaderID)
	if !ok {
		t.Fatal("workspace panel header is not mounted")
	}
	questionDockClickText(t, app, header, "⌄")
	if app.termDockVisible() {
		t.Fatal("workspace panel Hide was swallowed by the question dock")
	}
	questionDockAssertRetained(t, app, prompt, "release candidate", draft)
	questionDockClickText(t, app, footer, "Problems")
	questionDockSend(t, app, tea.KeyPressMsg{Code: tea.KeyEsc})
	if app.termDockVisible() {
		t.Fatal("Esc in an unrelated workspace panel was stolen by the question")
	}
	questionDockClickInput(t, app, prompt)
	questionDockSend(t, app, tea.KeyPressMsg{Code: tea.KeyEnd, Mod: tea.ModCtrl})
	questionDockSend(t, app, tea.PasteMsg{Content: " retained"})
	questionDockAssertRetained(t, app, prompt, "release candidate retained", draft)
	questionDockSend(t, app, tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModCtrl})
	if !prompt.question.Reviewing() || !strings.Contains(paneJourneyText(app, app.sessionDeckRect), "Review answers") {
		t.Fatal("answer completion did not enter the retained review step")
	}
	questionDockClickText(t, app, footer, "Notices")
	header, _ = app.rootHost.ScreenRect(termdock.HeaderID)
	questionDockClickText(t, app, header, "⌄")
	if !prompt.question.Reviewing() || !strings.Contains(paneJourneyText(app, app.sessionDeckRect), "release candidate retained") {
		t.Fatal("workspace panel navigation discarded the review or its answer")
	}
	questionDockAssertRetained(t, app, prompt, "release candidate retained", draft)
}

func TestQuestionDockJourneyDeliberateComposerFocusKeepsDraftUnsubmitted(t *testing.T) {
	app, prompt := questionDockJourneyApp(t)
	questionDockClickInput(t, app, prompt)
	questionDockSend(t, app, tea.PasteMsg{Content: "answer remains separate"})
	draft := app.Input.Value()
	questionDockClickText(t, app, app.inputRect, "private composer draft")
	if prompt.projection.deckFocused {
		t.Fatal("deliberate composer press did not leave question focus")
	}
	questionDockSend(t, app, tea.KeyPressMsg{Code: tea.KeyEnd, Mod: tea.ModCtrl})
	questionDockSend(t, app, tea.KeyPressMsg{Code: 'x', Text: "x"})
	if app.Input.Value() != draft+"x" {
		t.Fatal("deliberate composer focus did not edit the retained draft")
	}
	beforeRevision := app.Input.DraftRevision()
	beforeMessages := app.ws.AgentMgr().Get("agent").State.Messages.Version()
	questionDockSend(t, app, tea.KeyPressMsg{Code: tea.KeyEnter})
	if app.Input.DraftRevision() != beforeRevision || app.ws.AgentMgr().Get("agent").State.Messages.Version() != beforeMessages {
		t.Fatal("Enter submitted or changed the composer while the question was pending")
	}
	questionDockAssertRetained(t, app, prompt, "answer remains separate", draft+"x")
	questionDockClickInput(t, app, prompt)
	questionDockAssertRetained(t, app, prompt, "answer remains separate", draft+"x")
}

func TestQuestionDockJourneyTabMinimizeRestoreAndEditorInput(t *testing.T) {
	app, prompt := questionDockJourneyApp(t)
	draft := app.Input.Value()
	questionDockClickInput(t, app, prompt)
	questionDockSend(t, app, tea.PasteMsg{Content: "kept answer"})
	path := openMultiLineEditor(t, app, "package retained\n")
	ed := app.EditorState.Editor
	questionDockPaint(app)
	clickMainTab(t, app, app.agentTabTitle("agent"), false)
	questionDockPaint(app)
	clickMainTab(t, app, app.editorTabTitle(path), false)
	before := ed.Value()
	questionDockSend(t, app, tea.KeyPressMsg{Code: 'x', Text: "x"})
	if ed.Value() == before || !ed.Modified() || !strings.Contains(paneJourneyText(app, app.editorRect), "x") {
		t.Fatal("deliberately selected editor did not receive and paint the next key")
	}
	clickMainTab(t, app, app.agentTabTitle("agent"), true)
	questionDockPaint(app)
	if !app.AgentView.IsHidden("agent") || app.ws.AgentMgr().Get("agent") == nil || app.Win.TabByTitle(app.agentTabTitle("agent")) != nil {
		t.Fatal("closing the question owner's tab did not minimize the live agent")
	}
	if !app.sessionDeckRect.Empty() || app.overlayHost.IsOpen(ui.OverlayAgentPrompt) {
		t.Fatal("minimizing a retained dock promoted it to a global modal")
	}
	questionDockAssertRetained(t, app, prompt, "kept answer", draft)
	questionDockSend(t, app, tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl | tea.ModShift})
	if !app.sidebarHostVisible() || app.activityView != activityAgents {
		t.Fatal("Agents sidebar shortcut was swallowed by the pending question")
	}
	questionDockClickText(t, app, ui.Rect{W: app.sidebarHitWidth(), H: app.height}, "minimized")
	// Resource rows use explicit double activation rather than a command-row click.
	questionDockSend(t, app, tea.KeyPressMsg{Code: tea.KeyEnter})
	if app.AgentView.IsHidden("agent") || app.activeAgentTabID() != "agent" || app.sessionDeckRect.Empty() {
		t.Fatal("Agents sidebar did not restore the minimized question owner")
	}
	questionDockClickInput(t, app, prompt)
	questionDockAssertRetained(t, app, prompt, "kept answer", draft)
	if !strings.Contains(paneJourneyText(app, app.sessionDeckRect), "kept answer") {
		t.Fatal("restored dock did not repaint its retained answer")
	}
}

func TestQuestionDockJourneyTerminalKeysAndInitialPromptFocus(t *testing.T) {
	app, prompt := questionDockJourneyApp(t)
	draft := app.Input.Value()
	screen := app.ensureTermScreen("navigation-terminal", "Navigation PTY")
	screen.SetControlled(true)
	var sent []shellterm.Command
	screen.SetCommand(func(command shellterm.Command) tea.Cmd {
		sent = append(sent, command)
		return nil
	})
	app.termDock.Mount("navigation-terminal", screen)
	app.termDock.ShowPanel(termdock.PanelTerminal)
	app.setTermDockFocused(true)
	questionDockPaint(app)
	// This UI-boundary probe records neutral screen commands rather than
	// executing them against an invented PTY/lease. Native PTY routing has its
	// separate terminal-controls integration suite.
	send := func(msg tea.Msg) {
		_, _ = app.Update(msg)
		questionDockPaint(app)
	}
	// A retained terminal focus flag must not outrank a newly focused prompt.
	send(tea.PasteMsg{Content: "answer before terminal"})
	if prompt.question.Answer(0) != "answer before terminal" || len(sent) != 0 {
		t.Fatal("initial question keyboard focus leaked into the existing terminal")
	}
	r, ok := app.rootHost.ScreenRect(screen.ID())
	if !ok || r.Empty() {
		t.Fatal("terminal body is not mounted")
	}
	send(tea.MouseClickMsg{X: r.X + 2, Y: r.Y, Button: tea.MouseLeft})
	send(tea.MouseReleaseMsg{X: r.X + 2, Y: r.Y, Button: tea.MouseLeft})
	send(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	send(tea.PasteMsg{Content: "terminal paste"})
	if !app.terminalDockOwnsKeys() || len(sent) != 2 || sent[0].Kind != shellterm.CommandKey || sent[0].Key.Code != 'c' || sent[1].Kind != shellterm.CommandPaste || string(sent[1].Paste) != "terminal paste" {
		t.Fatalf("deliberate terminal input was captured by the question: %+v", sent)
	}
	questionDockAssertRetained(t, app, prompt, "answer before terminal", draft)
	r, ok = prompt.projection.deckHost.ScreenRect(question.InputID)
	if !ok {
		t.Fatal("retained question write-in is not mounted")
	}
	send(tea.MouseClickMsg{X: app.sessionDeckRect.X + r.X, Y: app.sessionDeckRect.Y + r.Y, Button: tea.MouseLeft})
	send(tea.MouseReleaseMsg{X: app.sessionDeckRect.X + r.X, Y: app.sessionDeckRect.Y + r.Y, Button: tea.MouseLeft})
	send(tea.KeyPressMsg{Code: tea.KeyEnd, Mod: tea.ModCtrl})
	send(tea.PasteMsg{Content: " again"})
	questionDockAssertRetained(t, app, prompt, "answer before terminal again", draft)
}

func TestQuestionDockJourneyCapturesSurviveAndTrueModalsShield(t *testing.T) {
	t.Run("workspace_divider_drag_crosses_question", func(t *testing.T) {
		app, prompt := questionDockJourneyApp(t)
		footer, _ := app.rootHost.ScreenRect(app.footerScreen.ID())
		questionDockClickText(t, app, footer, "Problems")
		r, ok := app.rootHost.ScreenRect(termdock.DividerID)
		if !ok {
			t.Fatal("workspace divider is not mounted")
		}
		before := app.termDockHeight
		questionDockSend(t, app, tea.MouseClickMsg{X: r.X + 5, Y: r.Y, Button: tea.MouseLeft})
		if !app.rootHost.HasPointerCapture() {
			t.Fatal("workspace divider did not capture while question was pending")
		}
		questionDockSend(t, app, tea.MouseMotionMsg{X: r.X + 5, Y: r.Y - 3, Button: tea.MouseLeft})
		questionDockSend(t, app, tea.MouseReleaseMsg{X: r.X + 5, Y: r.Y - 3, Button: tea.MouseLeft})
		if app.termDockHeight <= before || app.rootHost.HasPointerCapture() {
			t.Fatal("pending question interrupted the divider's captured motion/release")
		}
		questionDockAssertRetained(t, app, prompt, "", app.Input.Value())
	})
	t.Run("dirty_close_popup_remains_modal", func(t *testing.T) {
		app, prompt := questionDockJourneyApp(t)
		path := openMultiLineEditor(t, app, "package dirty\n")
		questionDockPaint(app)
		questionDockSend(t, app, tea.KeyPressMsg{Code: 'x', Text: "x"})
		questionDockSend(t, app, tea.KeyPressMsg{Code: 'w', Mod: tea.ModCtrl})
		if app.DirtyPromptPath != path || !app.overlayHost.IsOpen(ui.OverlayDirtyClose) {
			t.Fatal("dirty editor did not open its safe close decision")
		}
		footer, _ := app.rootHost.ScreenRect(app.footerScreen.ID())
		questionDockClickText(t, app, footer, "Problems")
		questionDockSend(t, app, tea.KeyPressMsg{Code: tea.KeyF12, Mod: tea.ModAlt})
		if app.termDockVisible() || app.DirtyPromptPath != path || app.windowManager.Focused() != app.overlayHost.Window(ui.OverlayDirtyClose) {
			t.Fatal("nonmodal prompt policy bypassed a true dirty-close modal")
		}
		questionDockAssertRetained(t, app, prompt, "", app.Input.Value())
	})
	t.Run("yolo_deck_remains_modal_and_cancels_underlying_capture", func(t *testing.T) {
		app, projection := setupSessionDeckApp(t)
		wsOf(app).Logger = stubLogger{}
		app.width, app.height = 160, 50
		app.toggleWorkspacePanel(termdock.PanelProblems)
		app.paintShellFrame()
		r, ok := app.rootHost.ScreenRect(termdock.DividerID)
		if !ok {
			t.Fatal("workspace divider is not mounted")
		}
		_, _ = app.Update(tea.MouseClickMsg{X: r.X + 5, Y: r.Y, Button: tea.MouseLeft})
		if !app.rootHost.HasPointerCapture() {
			t.Fatal("fixture did not begin a divider drag")
		}
		app.openSessionYoloPanel()
		app.paintShellFrame()
		before := app.termDockHeight
		_, _ = app.Update(tea.MouseMotionMsg{X: r.X + 5, Y: r.Y - 3, Button: tea.MouseLeft})
		_, _ = app.Update(tea.MouseReleaseMsg{X: r.X + 5, Y: r.Y - 3, Button: tea.MouseLeft})
		if app.rootHost.HasPointerCapture() || app.termDockHeight != before || !app.sessionDeckModal() {
			t.Fatal("YOLO safety decision allowed an underlying captured drag")
		}
		header, _ := app.rootHost.ScreenRect(termdock.HeaderID)
		x, y := questionDockTextPoint(t, app, header, "⌄")
		_, _ = app.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
		if !app.termDockVisible() || !projection.deckFocused || app.terminalSummonAllowed() {
			t.Fatal("YOLO safety decision allowed unrelated panel controls or terminal summon")
		}
	})
}
