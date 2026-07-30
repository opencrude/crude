package viewmodel

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/provider"
	"crude/tui/cell"
	"crude/tui/chatprojection"
	"crude/tui/editor"
	"crude/tui/screens/shellchat"
	"crude/tui/surface"
	"crude/tui/theme"
	ttypes "crude/tui/types"
	"crude/tui/ui"
	"crude/workspace"
)

func setupChatRoutingApp(t *testing.T, viewportH int, rows []chatprojection.MessageRow) (*App, *shellchat.Screen) {
	t.Helper()
	app := newFullTestApp(t)
	app.width = 80
	app.height = 24
	app.launcherScreen = nil
	app.launcherHost = nil
	app.mainChatSurf = surface.NewSurface("main-chat", 80, 20, cell.TransparentBlank())
	app.mainLauncherSurf = surface.NewSurface("main-launcher", 80, 20, cell.TransparentBlank())

	th := theme.DefaultDark()
	screen := shellchat.New(th)
	screen.SetMessages(rows, shellchat.PaintParams{
		Compose: shellchat.ComposeParams{
			InnerW:        40,
			PadLR:         th.Spacing.PadLR,
			Theme:         th,
			MainSBVisible: true,
		},
	})
	contentH := 0
	for _, row := range rows {
		h := len(row.Lines)
		if h < 1 {
			h = 1
		}
		contentH += h
	}
	if contentH < viewportH {
		contentH = viewportH + 5
	}
	host := ui.NewHost(th, screen)
	w := shellchat.ContentSurfaceWidth(40, th.Spacing.PadLR, true)
	host.Resize(w, viewportH)
	host.Layout()

	app.chatScreen = screen
	app.chatHost = host

	app.AddOrchestrator(
		"agent",
		make(chan provider.UserInput, 1),
		make(chan provider.UserInput, 1),
		make(chan struct{}, 1),
		make(chan string, 1),
	)
	node := app.AgentMgr.Get("agent")
	node.State.Completed = false
	node.State.Streaming = false
	node.State.Thinking = false

	for _, row := range rows {
		app.appendActiveMessage(ttypes.ChatMessage{
			Role:    ttypes.RoleAssistant,
			Content: strings.Join(row.Lines, "\n"),
			Time:    time.Unix(1, 0),
		})
	}
	_ = app.buildMainView()
	app.renderChatToSurface()
	screen.SyncScrollMetrics(app.chatRect.H, contentH)
	return app, screen
}

// TestChatViewmodelKeyboardContractTable verifies PgUp/PgDown/Home/End routing,
// Ctrl+C copy semantics, Esc/capture teardown, and input-empty ownership rules.
func TestChatViewmodelKeyboardContractTable(t *testing.T) {
	lines := make([]string, 30)
	for i := range lines {
		lines[i] = "scrollable line"
	}
	rows := []chatprojection.MessageRow{{ID: "m1", Lines: lines}}

	t.Run("pgup_pgdown_route_to_chat_when_input_empty", func(t *testing.T) {
		app, screen := setupChatRoutingApp(t, 5, rows)
		scroll := screen.ScrollModel()
		if scroll.MaxOffset() <= 0 {
			t.Fatalf("setup not scrollable: max=%d content=%d viewport=%d", scroll.MaxOffset(), scroll.Content(), scroll.Viewport())
		}
		scroll.StickToBottom()
		app.Input.ClearInput()

		app.dispatchKeyPress(tea.KeyPressMsg{Code: tea.KeyPgUp})
		if scroll.AutoScroll() {
			t.Fatal("PgUp should disarm auto-scroll")
		}
		if scroll.Offset() >= scroll.MaxOffset() {
			t.Fatal("PgUp should scroll away from bottom")
		}

		app.dispatchKeyPress(tea.KeyPressMsg{Code: tea.KeyPgDown})
		for i := 0; i < 10 && scroll.Offset() < scroll.MaxOffset(); i++ {
			app.dispatchKeyPress(tea.KeyPressMsg{Code: tea.KeyPgDown})
		}
		if !scroll.AutoScroll() {
			t.Fatal("PgDown at bottom should re-arm auto-scroll")
		}
	})

	t.Run("home_end_empty_input_routes_to_chat", func(t *testing.T) {
		app, screen := setupChatRoutingApp(t, 5, rows)
		scroll := screen.ScrollModel()
		scroll.StickToBottom()
		app.Input.ClearInput()

		app.dispatchKeyPress(tea.KeyPressMsg{Code: tea.KeyHome})
		if scroll.Offset() != 0 || scroll.AutoScroll() {
			t.Fatalf("Home with empty input: off=%d auto=%v", scroll.Offset(), scroll.AutoScroll())
		}

		app.dispatchKeyPress(tea.KeyPressMsg{Code: tea.KeyEnd})
		if scroll.Offset() != scroll.MaxOffset() || !scroll.AutoScroll() {
			t.Fatalf("End with empty input: off=%d max=%d auto=%v", scroll.Offset(), scroll.MaxOffset(), scroll.AutoScroll())
		}
	})

	t.Run("home_end_nonempty_input_goes_to_input", func(t *testing.T) {
		app, screen := setupChatRoutingApp(t, 5, rows)
		scroll := screen.ScrollModel()
		scroll.StickToBottom()
		app.Input.SetInput("editing")
		before := scroll.Offset()

		app.dispatchKeyPress(tea.KeyPressMsg{Code: tea.KeyHome})
		if scroll.Offset() != before {
			t.Fatal("Home with non-empty input should not scroll chat list")
		}
	})

	t.Run("ctrl_c_copy_input_selection", func(t *testing.T) {
		app, _ := setupChatRoutingApp(t, 3, []chatprojection.MessageRow{{ID: "m1", Lines: []string{"x"}}})
		app.Input.SetInput("copy me")
		updated, _, _ := app.Input.Update(tea.KeyPressMsg{Code: tea.KeyHome, Mod: tea.ModShift, Text: "shift+home"}, true, false, 20, false)
		app.Input = updated
		if !app.Input.InputSelecting() {
			t.Fatal("setup: shift+home should select input prefix")
		}
		_, cmd := app.dispatchKeyPress(tea.KeyPressMsg{Text: "ctrl+c", Mod: tea.ModCtrl, Code: 'c'})
		if cmd == nil {
			t.Fatal("Ctrl+C on input selection should emit copy cmd")
		}
	})

	t.Run("esc_clears_nonempty_input_when_agent_idle", func(t *testing.T) {
		app, _ := setupChatRoutingApp(t, 3, []chatprojection.MessageRow{{ID: "m1", Lines: []string{"x"}}})
		node := app.AgentMgr.Get("agent")
		node.State.Completed = true
		node.State.Streaming = false
		node.State.Thinking = false
		if sp := app.PaneMgr.Get("agent"); sp != nil {
			sp.Done = true
		}
		app.Input.SetInput("draft")
		app.dispatchKeyPress(tea.KeyPressMsg{Code: tea.KeyEsc})
		if app.Input.Value() != "" {
			t.Fatalf("Esc should clear input when agent/pane idle, got %q", app.Input.Value())
		}
	})

	t.Run("keyboard_dismisses_chat_capture", func(t *testing.T) {
		app, screen := setupChatRoutingApp(t, 5, rows)
		_, host, _ := setupCapturedChatSelection(t, app, 5, rows)
		app.dispatchKeyPress(tea.KeyPressMsg{Code: 'a', Text: "a"})
		if host.HasPointerCapture() || screen.IsSelecting() {
			t.Fatal("typing should cancel chat capture and selection")
		}
		if app.Input.Value() != "a" {
			t.Fatalf("key should reach input, got %q", app.Input.Value())
		}
	})

	t.Run("editor_mode_takeover_hides_chat_routing", func(t *testing.T) {
		app, screen := setupChatRoutingApp(t, 5, rows)
		path := t.TempDir() + "/edit.go"
		ed := editor.NewPreview(app.Theme, path, "package main\n", 1)
		app.EditorState.Editor = ed
		app.EditorState.ActiveView = path
		before := screen.ScrollModel().Offset()
		app.dispatchKeyPress(tea.KeyPressMsg{Code: tea.KeyPgUp})
		if screen.ScrollModel().Offset() != before {
			t.Fatal("PgUp must not scroll chat when editor is active")
		}
	})

	t.Run("stream_stick_to_bottom_only_when_armed", func(t *testing.T) {
		app, screen := setupChatRoutingApp(t, 5, rows)
		scroll := screen.ScrollModel()
		scroll.StickToBottom()

		app.chatHost.Handle(tea.MouseWheelMsg{
			X: app.Theme.Spacing.PadLR + 2, Y: 2, Button: tea.MouseWheelUp,
		})
		if scroll.AutoScroll() {
			t.Fatal("wheel up should disarm auto-scroll")
		}

		scroll.StickToBottom()
		node := app.AgentMgr.Get("agent")
		node.State.Messages.AppendMessage(workspace.Message{
			Role: ttypes.RoleAssistant, Content: "another chunk\nmore lines", Time: time.Now(),
		})
		app.syncAgentMessages("agent", false)
		_ = app.buildMainView()
		app.renderChatToSurface()
		if scroll.Offset() != scroll.MaxOffset() || !scroll.AutoScroll() {
			t.Fatalf("armed scroll should stick to bottom: off=%d max=%d auto=%v", scroll.Offset(), scroll.MaxOffset(), scroll.AutoScroll())
		}
	})

	t.Run("disarmed_append_preserves_offset_through_production_path", func(t *testing.T) {
		app, screen := setupChatRoutingApp(t, 5, rows)
		scroll := screen.ScrollModel()
		scroll.StickToBottom()
		app.chatHost.Handle(tea.MouseWheelMsg{
			X: app.Theme.Spacing.PadLR + 2, Y: 2, Button: tea.MouseWheelUp,
		})
		if scroll.AutoScroll() {
			t.Fatal("wheel up should disarm auto-scroll")
		}
		wantOff := scroll.Offset()
		maxBefore := scroll.MaxOffset()
		if wantOff <= 0 || wantOff >= maxBefore {
			t.Fatalf("setup: need disarmed mid-scroll offset, off=%d max=%d", wantOff, maxBefore)
		}

		node := app.AgentMgr.Get("agent")
		node.State.Messages.AppendMessage(workspace.Message{
			Role: ttypes.RoleAssistant, Content: strings.Repeat("appended line\n", 8), Time: time.Now(),
		})
		app.syncAgentMessages("agent", false)
		_ = app.buildMainView()
		app.renderChatToSurface()
		if scroll.AutoScroll() {
			t.Fatal("append while disarmed must not re-arm auto-scroll")
		}
		maxAfter := scroll.MaxOffset()
		if wantOff > maxAfter {
			wantOff = maxAfter
		}
		if scroll.Offset() != wantOff {
			t.Fatalf("disarmed append should preserve/clamp offset=%d got=%d max=%d", wantOff, scroll.Offset(), maxAfter)
		}
	})
}

// TestChatSelectionCopyContract verifies full dispatch produces exactly one copy
// command and clears selection without duplicate region routing.
func TestChatSelectionCopyContract(t *testing.T) {
	app, _ := setupChatRoutingApp(t, 2, []chatprojection.MessageRow{
		{ID: "m1", Lines: []string{"hello world"}},
	})
	_, host, localX := setupCapturedChatSelection(t, app, app.chatRect.H, []chatprojection.MessageRow{
		{ID: "m1", Lines: []string{"hello world"}},
	})
	host.Handle(tea.MouseMotionMsg{X: localX + 5, Y: 0, Button: tea.MouseLeft})
	res := host.Handle(tea.MouseReleaseMsg{X: localX + 5, Y: 0, Button: tea.MouseLeft})
	if res.Cmd == nil {
		t.Fatal("release should produce copy cmd on non-empty selection")
	}
	if app.chatScreen.IsSelecting() || host.HasPointerCapture() {
		t.Fatal("selection and capture must clear after release")
	}
	res2 := host.Handle(tea.MouseReleaseMsg{X: localX + 5, Y: 0, Button: tea.MouseLeft})
	if res2.Cmd != nil {
		t.Fatal("second release must not emit another copy cmd")
	}
}
