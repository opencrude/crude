package viewmodel

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/fileio"
	"crude/provider"
	"crude/tui/editor"
	"crude/tui/screens/confirmation"
	"crude/tui/screens/question"
	"crude/tui/screens/yolo"
	"crude/types"
	"crude/workspace"
)

func setupSessionDeckApp(t *testing.T) (*App, *sessionProjection) {
	t.Helper()
	app := newFullTestApp(t)
	app.width = 100
	app.height = 30
	app.launcherScreen = nil
	app.ActiveSession = &workspace.Session{
		SessionID: "session",
		Settings:  workspace.NewSessionSettingsState(types.Settings{}, types.Settings{}, nil),
	}
	app.AddOrchestrator(
		"agent",
		make(chan provider.UserInput, 1),
		make(chan provider.UserInput, 1),
		make(chan struct{}, 1),
		make(chan string, 1),
	)
	return app, app.activeSessionProjection()
}

// TestPanelDeckViewmodelContractTable documents session PanelDeck keyboard/mouse
// capture, blocking policy, passive background jobs, and focus restoration.
func TestPanelDeckViewmodelContractTable(t *testing.T) {
	t.Run("session_settings_captures_keys_and_esc_closes_once", func(t *testing.T) {
		app, projection := setupSessionDeckApp(t)
		app.openSessionSettings()
		if !app.sessionDeckCapturesInput() || !projection.deckFocused {
			t.Fatal("interactive settings panel should capture deck input")
		}
		app.Input.SetInput("leak-test")
		app.dispatchKeyPress(tea.KeyPressMsg{Code: 'x', Text: "x"})
		if app.Input.Value() != "leak-test" {
			t.Fatal("keys must not leak to chat input while settings focused")
		}

		_, cmd := app.dispatchKeyPress(tea.KeyPressMsg{Code: tea.KeyEsc})
		if cmd != nil {
			if msg := cmd(); msg != nil {
				app.Update(msg)
			}
		}
		if projection.deck.IsOpen() {
			t.Fatal("Esc on root settings form should close panel (form.exit.backward)")
		}
		if app.sessionDeckCapturesInput() {
			t.Fatal("closed deck must release input capture")
		}
	})

	t.Run("confirmation_blocking_prevents_chat_and_mouse_leak", func(t *testing.T) {
		app, projection := setupSessionDeckApp(t)
		projection.confirmation.Reset(confirmation.Details{Question: "Proceed?", Tool: "bash"})
		projection.confirmationAgentID = "agent"
		app.openSessionConfirmationPanel(projection)
		if !app.sessionDeckBlocking() || !app.sessionDeckCapturesInput() {
			t.Fatal("confirmation must be blocking and capture input")
		}
		app.Input.SetInput("should-not-reach")
		app.dispatchKeyPress(tea.KeyPressMsg{Code: 'a', Text: "a"})
		if app.Input.Value() != "should-not-reach" {
			t.Fatal("blocking confirmation leaked key to chat input")
		}
		if region := app.classifyShellRegion(app.width-1, app.height-1, false); region != regionSessionDeck {
			t.Fatalf("clicks classify as deck=%v want sessionDeck", region)
		}
	})

	t.Run("blocking_panel_shields_pointer_outside_deck_rect", func(t *testing.T) {
		app, projection := setupSessionDeckApp(t)
		for range 40 {
			app.appendActiveMessage(types.ChatMessage{
				Role:    types.RoleAssistant,
				Content: "underlying chat row must not receive modal pointer input",
				Time:    time.Now(),
			})
		}
		app.syncAgentMessages("agent", false)
		projection.confirmation.Reset(confirmation.Details{Question: "Proceed?", Tool: "bash"})
		projection.confirmationAgentID = "agent"
		app.openSessionConfirmationPanel(projection)
		app.paintShellFrame()

		if app.chatRect.Empty() || app.sessionDeckRect.Empty() {
			t.Fatalf("invalid shell geometry: chat=%+v deck=%+v", app.chatRect, app.sessionDeckRect)
		}
		scroll := app.chatScreen.ScrollModel()
		if scroll.MaxOffset() <= 0 {
			t.Fatal("test requires scrollable content behind the blocking panel")
		}
		beforeOffset := scroll.Offset()
		beforeFocus := app.rootHost.Focused()
		x, y := app.chatRect.X+1, app.chatRect.Y

		_, _ = app.dispatchMouseWheel(tea.MouseWheelMsg{X: x, Y: y, Button: tea.MouseWheelUp})
		if got := scroll.Offset(); got != beforeOffset {
			t.Fatalf("blocking panel leaked wheel to chat: offset=%d want %d", got, beforeOffset)
		}
		_, _ = app.dispatchMouseClick(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
		if got := app.rootHost.Focused(); got != beforeFocus {
			t.Fatalf("blocking panel leaked click focus: got %q want %q", got, beforeFocus)
		}
		_, _ = app.dispatchMouseClick(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseRight})
		if app.LastContextMenu != nil {
			t.Fatal("blocking panel leaked right-click to an underlying context menu")
		}
	})

	t.Run("confirmation_y_esc_resolves_once", func(t *testing.T) {
		app, projection := setupSessionDeckApp(t)
		blockCh := make(chan string, 1)
		app.AgentMgr.Get("agent").BlockResolveCh = blockCh
		projection.confirmation.Reset(confirmation.Details{Question: "Proceed?", Tool: "bash"})
		projection.confirmationAgentID = "agent"
		app.openSessionConfirmationPanel(projection)
		projection.deckHost.Resize(100, 12)
		projection.deckHost.Layout()
		app.focusSessionDeckAfterLayout()
		if projection.deckHost.Focused() != confirmation.ActionsID {
			t.Fatalf("confirmation focus=%q want %q", projection.deckHost.Focused(), confirmation.ActionsID)
		}
		app.Input.SetInput("must-not-leak")
		_, cmd := app.dispatchKeyPress(tea.KeyPressMsg{Code: 'y', Text: "y"})
		if cmd != nil {
			if msg := cmd(); msg != nil {
				app.Update(msg)
			}
		}
		select {
		case got := <-blockCh:
			if got != "allow" {
				t.Fatalf("y resolve=%q want allow", got)
			}
		default:
			t.Fatal("confirmation y should resolve to owning agent once")
		}
		if projection.deck.IsOpen() {
			t.Fatal("resolved confirmation should close deck panel")
		}
		if app.Input.Value() != "must-not-leak" {
			t.Fatal("confirmation y leaked key to chat input")
		}
		select {
		case extra := <-blockCh:
			t.Fatalf("confirmation y resolved twice: %q", extra)
		default:
		}
	})

	t.Run("question_esc_returns_to_rail_then_rail_esc_resolves_once", func(t *testing.T) {
		app, projection := setupSessionDeckApp(t)
		blockCh := make(chan string, 1)
		app.AgentMgr.Get("agent").BlockResolveCh = blockCh
		projection.question.Reset(`[{"question":"Continue?","options":["yes","no"]}]`)
		projection.questionAgentID = "agent"
		app.openSessionQuestionPanel(projection)
		if !app.sessionDeckBlocking() {
			t.Fatal("question panel must block")
		}
		projection.deckHost.Resize(100, 12)
		projection.deckHost.Layout()
		app.focusSessionDeckAfterLayout()
		app.Input.SetInput("blocked")
		// First Esc: the answer pane hands focus back to the question rail
		// without denying anything.
		_, cmd := app.dispatchKeyPress(tea.KeyPressMsg{Code: tea.KeyEsc})
		if cmd != nil {
			if msg := cmd(); msg != nil {
				app.Update(msg)
			}
		}
		if !projection.deck.IsOpen() {
			t.Fatal("esc in the answer pane must not close the panel")
		}
		if projection.deckHost.Focused() != question.RailID {
			t.Fatalf("esc focus=%q want %q", projection.deckHost.Focused(), question.RailID)
		}
		select {
		case got := <-blockCh:
			t.Fatalf("rail-return esc resolved the tool early: %q", got)
		default:
		}
		// Second Esc, now on the rail, is the explicit deny path.
		_, cmd = app.dispatchKeyPress(tea.KeyPressMsg{Code: tea.KeyEsc})
		if cmd != nil {
			if msg := cmd(); msg != nil {
				app.Update(msg)
			}
		}
		if projection.deck.IsOpen() {
			t.Fatal("cancelled question should close panel")
		}
		if app.Input.Value() != "blocked" {
			t.Fatal("question cancel leaked key to chat input")
		}
		select {
		case got := <-blockCh:
			if got != "cancelled" {
				t.Fatalf("question cancel resolve=%q want cancelled", got)
			}
		default:
			t.Fatal("question Esc should resolve to owning agent once")
		}
		select {
		case extra := <-blockCh:
			t.Fatalf("question cancel resolved twice: %q", extra)
		default:
		}
	})

	t.Run("yolo_esc_resolves_disabled_once", func(t *testing.T) {
		app, projection := setupSessionDeckApp(t)
		projection.yoloAgentID = "agent"
		app.openSessionYoloPanel()
		projection.deckHost.Resize(100, 12)
		projection.deckHost.Layout()
		app.focusSessionDeckAfterLayout()
		if projection.deckHost.Focused() != yolo.ActionsID {
			t.Fatalf("YOLO focus=%q want %q", projection.deckHost.Focused(), yolo.ActionsID)
		}
		app.Input.SetInput("must-not-leak")
		_, cmd := app.dispatchKeyPress(tea.KeyPressMsg{Code: tea.KeyEsc})
		if cmd != nil {
			if msg := cmd(); msg != nil {
				app.Update(msg)
			}
		}
		if projection.deck.IsOpen() {
			t.Fatal("YOLO Esc should close panel")
		}
		if app.OverlayState.IsYolo() {
			t.Fatal("YOLO Esc must resolve disabled")
		}
		if app.Input.Value() != "must-not-leak" {
			t.Fatal("YOLO Esc leaked key to chat input")
		}
	})

	t.Run("background_jobs_passive_no_focus_steal", func(t *testing.T) {
		app, projection := setupSessionDeckApp(t)
		app.openSessionSettings()
		now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
		app.updateSessionBackgroundJobs("agent", []fileio.AsyncCmdInfo{{
			Handle: "tests", Command: "go test ./...", Status: fileio.AsyncRunning, StartedAt: now,
		}}, now)
		if projection.deck.Active().ID != sessionSettingsPanelID {
			t.Fatalf("passive jobs must not displace interactive panel: active=%q", projection.deck.Active().ID)
		}
		if !projection.deckFocused {
			t.Fatal("passive jobs must not steal deck focus")
		}
	})

	t.Run("last_panel_close_restores_input_ownership", func(t *testing.T) {
		app, projection := setupSessionDeckApp(t)
		app.openSessionSettings()
		app.closeSessionPanel(sessionSettingsPanelID)
		if projection.deck.IsOpen() || app.sessionDeckCapturesInput() {
			t.Fatal("closing last panel should leave deck empty and release capture")
		}
		app.Input.ClearInput()
		app.dispatchKeyPress(tea.KeyPressMsg{Code: 'h', Text: "h"})
		if app.Input.Value() != "h" {
			t.Fatalf("input should accept keys after deck close, got %q", app.Input.Value())
		}
	})

	t.Run("session_switch_preserves_independent_decks", func(t *testing.T) {
		app := newFullTestApp(t)
		first := &workspace.Session{SessionID: "first", Settings: workspace.NewSessionSettingsState(types.Settings{}, types.Settings{}, nil)}
		second := &workspace.Session{SessionID: "second", Settings: workspace.NewSessionSettingsState(types.Settings{}, types.Settings{}, nil)}
		app.ActiveSession = first
		app.openSessionSettings()
		firstDeck := app.activeSessionProjection()
		app.ActiveSession = second
		app.openSessionSettings()
		secondDeck := app.activeSessionProjection()
		if firstDeck == secondDeck || !firstDeck.deck.IsOpen() || !secondDeck.deck.IsOpen() {
			t.Fatal("each session should retain an independent open deck")
		}
		app.closeSessionPanel(sessionSettingsPanelID)
		app.ActiveSession = first
		if !firstDeck.deck.IsOpen() {
			t.Fatal("switching back should restore first session deck state")
		}
	})

	t.Run("blocking_wins_over_editor_focus", func(t *testing.T) {
		app, projection := setupSessionDeckApp(t)
		path := t.TempDir() + "/main.go"
		ed := editor.NewPreview(app.Theme, path, "package main\n", 1)
		app.EditorState.Editor = ed
		app.EditorState.ActiveView = path
		projection.confirmation.Reset(confirmation.Details{Question: "Proceed?"})
		app.openSessionConfirmationPanel(projection)
		if !app.sessionDeckCapturesInput() {
			t.Fatal("blocking panel must capture over editor")
		}
	})

	t.Run("settings_tab_navigation_documented_via_form", func(t *testing.T) {
		app, projection := setupSessionDeckApp(t)
		app.openSessionSettings()
		app.focusSessionDeckAfterLayout()
		_, _ = app.dispatchKeyPress(tea.KeyPressMsg{Code: tea.KeyTab})
		if !projection.deck.IsOpen() || !app.sessionDeckCapturesInput() {
			t.Fatal("settings deck should remain focused for Tab navigation")
		}
	})

	t.Run("yolo_has_no_tab_navigation", func(t *testing.T) {
		app, projection := setupSessionDeckApp(t)
		app.openSessionYoloPanel()
		host := projection.deckHost
		res := host.Handle(tea.KeyPressMsg{Code: tea.KeyTab})
		if res.Action != nil && res.Action.ID == "ui.focus" {
			t.Fatal("YOLO blocking panel uses action buttons only; Tab does not cycle fields")
		}
	})
}
