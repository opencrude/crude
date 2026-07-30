package viewmodel

import (
	"path/filepath"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/provider"
	"crude/tui/chatprojection"
	"crude/tui/editor"
	ttypes "crude/tui/types"
)

func openTestEditor(t *testing.T, app *App) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "foo.go")
	app.EditorState.Editor = editor.NewPreview(app.Theme, path, "package main\n", 1)
	app.EditorState.ActiveView = path
	return path
}

func TestSyncPersistentHostCapture_ChatHiddenByEditor(t *testing.T) {
	app := newFullTestApp(t)
	screen, host, _ := setupCapturedChatSelection(t, app, 1, []chatprojection.MessageRow{
		{ID: "m1", Lines: []string{"editor hides chat"}},
	})

	openTestEditor(t, app)
	app.syncPersistentHostCapture()

	if host.HasPointerCapture() {
		t.Fatal("chat capture should cancel when editor is active")
	}
	if screen.IsSelecting() {
		t.Fatal("chat selection should cancel when editor is active")
	}

	app.EditorState.ActiveView = ""
	app.EditorState.Editor.Close()
	app.syncPersistentHostCapture()

	if host.HasPointerCapture() {
		t.Fatal("returning to chat must not restore latent capture")
	}
	if screen.IsSelecting() {
		t.Fatal("returning to chat must not restore latent selection")
	}
}

func TestSyncPersistentHostCapture_ChatReleaseElsewhereAfterEditor(t *testing.T) {
	app := newFullTestApp(t)
	screen, host, _ := setupCapturedChatSelection(t, app, 1, []chatprojection.MessageRow{
		{ID: "m1", Lines: []string{"release elsewhere"}},
	})

	openTestEditor(t, app)
	app.syncPersistentHostCapture()

	_, _ = app.dispatchMouseRelease(tea.MouseReleaseMsg{
		X: 10, Y: app.height - 1, Button: tea.MouseLeft,
	})

	if host.HasPointerCapture() || screen.IsSelecting() {
		t.Fatal("release while chat hidden should not leave latent chat capture")
	}

	app.EditorState.ActiveView = ""
	app.EditorState.Editor.Close()
	app.syncPersistentHostCapture()
	if host.HasPointerCapture() || screen.IsSelecting() {
		t.Fatal("return to chat after hidden release must stay clean")
	}
}

func TestSyncPersistentHostCapture_SidebarHidden(t *testing.T) {
	app := newFullTestApp(t)
	host, _, _ := setupScrollableSidebarCapture(t, app)

	app.Tree.Visible = false
	app.syncPersistentHostCapture()

	if host.HasPointerCapture() {
		t.Fatal("sidebar capture should cancel when tree is hidden")
	}
}

func TestSyncPersistentHostCapture_LauncherHiddenByAgent(t *testing.T) {
	app := newTestAppWithLauncher(t)
	app.width = 100
	app.height = 30
	app.ChatAreaHeight = 20
	app.Tree = NewTreeState(nil, 30)
	app.Tree.Visible = false

	layout := app.computeLayout()
	app.launcherHost.Resize(layout.InnerW, app.ChatAreaHeight)
	app.launcherHost.Layout()

	localX := layout.InnerW*40/100 + 7
	localY := 8
	res := app.launcherHost.Handle(tea.MouseClickMsg{X: localX, Y: localY, Button: tea.MouseLeft})
	if !res.Handled || !app.launcherHost.HasPointerCapture() {
		t.Fatal("launcher click should capture pointer")
	}
	launcherHost := app.launcherHost

	app.AddOrchestrator(
		"agent",
		make(chan provider.UserInput, 1),
		make(chan provider.UserInput, 1),
		make(chan struct{}, 1),
		make(chan string, 1),
	)
	app.syncPersistentHostCapture()

	if launcherHost.HasPointerCapture() {
		t.Fatal("launcher capture should cancel when agent replaces launcher")
	}
}

func TestSyncPersistentHostCapture_LauncherHiddenByMinSize(t *testing.T) {
	app := newTestAppWithLauncher(t)
	app.width = 100
	app.height = 30
	app.ChatAreaHeight = 20
	app.Tree = NewTreeState(nil, 30)

	layout := app.computeLayout()
	app.launcherHost.Resize(layout.InnerW, app.ChatAreaHeight)
	app.launcherHost.Layout()

	localX := layout.InnerW*40/100 + 3 + 2 + 2
	if localX >= layout.InnerW {
		localX = layout.InnerW - 2
	}
	localY := 4 + 1 + 2 + 1
	res := app.launcherHost.Handle(tea.MouseClickMsg{X: localX, Y: localY, Button: tea.MouseLeft})
	if !res.Handled || !app.launcherHost.HasPointerCapture() {
		t.Fatal("launcher click should capture pointer")
	}

	app.ChatAreaHeight = 2 // below renderLauncher minimum of 3
	app.syncPersistentHostCapture()
	if app.launcherHost.HasPointerCapture() {
		t.Fatal("launcher capture should cancel when min-size gate hides launcher")
	}
}

func TestCapturedPersistentHost_CancelsInactiveBeforeRouting(t *testing.T) {
	app := newFullTestApp(t)
	_, chatHost, _ := setupCapturedChatSelection(t, app, 1, []chatprojection.MessageRow{
		{ID: "m1", Lines: []string{"inactive cancel"}},
	})
	sidebarHost, _, _ := setupScrollableSidebarCapture(t, app)

	openTestEditor(t, app)

	host, _ := app.capturedPersistentHost()
	if host != sidebarHost {
		t.Fatalf("expected sidebar host to route, got %p", host)
	}
	if chatHost.HasPointerCapture() {
		t.Fatal("chat capture should be canceled before routing")
	}
	if !sidebarHost.HasPointerCapture() {
		t.Fatal("sidebar capture should remain when still active")
	}
}

func TestSyncPersistentHostCapture_DuplicateCapturePrefersChat(t *testing.T) {
	app := newFullTestApp(t)
	_, chatHost, _ := setupCapturedChatSelection(t, app, 1, []chatprojection.MessageRow{
		{ID: "m1", Lines: []string{"winner"}},
	})
	sidebarHost, _, _ := setupScrollableSidebarCapture(t, app)

	if !chatHost.HasPointerCapture() || !sidebarHost.HasPointerCapture() {
		t.Fatal("setup should leave both hosts captured")
	}

	app.syncPersistentHostCapture()

	if !chatHost.HasPointerCapture() {
		t.Fatal("chat should win duplicate capture")
	}
	if sidebarHost.HasPointerCapture() {
		t.Fatal("lower-priority sidebar capture should be canceled")
	}
}

func TestKeyboardDismiss_CapturedSelectionClearsCaptureAndAutoScroll(t *testing.T) {
	lines := make([]string, 12)
	for i := range lines {
		lines[i] = "scroll line"
	}
	app := newFullTestApp(t)
	screen, host, localX := setupCapturedChatSelection(t, app, 5, []chatprojection.MessageRow{
		{ID: "m1", Lines: lines},
	})

	belowY := app.chatRect.Y + app.chatRect.H
	_, cmd := app.dispatchMouseMotion(tea.MouseMotionMsg{
		X: app.chatRect.X + localX, Y: belowY, Button: tea.MouseLeft,
	})
	if cmd == nil {
		t.Fatal("expected selection auto-scroll cmd")
	}

	scroll := screen.ScrollModel()
	beforeOffset := scroll.Offset()

	app.dispatchKeyPress(tea.KeyPressMsg{Code: 'a', Text: "a"})

	if host.HasPointerCapture() {
		t.Fatal("keyboard should cancel chat host capture")
	}
	if screen.IsSelecting() {
		t.Fatal("keyboard should end in-progress selection")
	}

	res := app.chatHost.Handle(ttypes.SelectionTickMsg(time.Now()))
	if res.Handled && scroll.Offset() != beforeOffset {
		t.Fatalf("selection auto-scroll should be disarmed, offset=%d before=%d", scroll.Offset(), beforeOffset)
	}
	if app.Input.Value() != "a" {
		t.Fatalf("key should reach input after capture cancel, got %q", app.Input.Value())
	}
}

func TestKeyboardDismiss_InProgressNonEmptySelectionDoesNotSwallowKey(t *testing.T) {
	app := newFullTestApp(t)
	screen, host, _ := setupCapturedChatSelection(t, app, 1, []chatprojection.MessageRow{
		{ID: "m1", Lines: []string{"hello world"}},
	})
	padLR := app.Theme.Spacing.PadLR
	host.Handle(tea.MouseMotionMsg{X: padLR + 6, Y: 0, Button: tea.MouseLeft})

	if !screen.IsSelecting() || !screen.HasActiveSelection() {
		t.Fatal("expected in-progress non-empty selection")
	}
	if !host.HasPointerCapture() {
		t.Fatal("expected host capture during selection drag")
	}

	app.dispatchKeyPress(tea.KeyPressMsg{Code: 'a', Text: "a"})

	if host.HasPointerCapture() || screen.IsSelecting() {
		t.Fatal("keyboard should cancel capture and end selection")
	}
	if app.Input.Value() != "a" {
		t.Fatalf("key should reach input during in-progress selection, got %q", app.Input.Value())
	}
}

func TestSetMode_ClearsCapturedSelection(t *testing.T) {
	app := newFullTestApp(t)
	screen, host, _ := setupCapturedChatSelection(t, app, 1, []chatprojection.MessageRow{
		{ID: "m1", Lines: []string{"mode switch"}},
	})

	app.setMode(ModeChat)

	if host.HasPointerCapture() {
		t.Fatal("setMode should cancel chat host capture")
	}
	if screen.IsSelecting() {
		t.Fatal("setMode should clear in-progress selection")
	}
}

func TestSetMode_ClearsCapturedScrollbarDrag(t *testing.T) {
	app := newFullTestApp(t)
	screen, host := setupCapturedChatScrollbar(t, app, 3, []chatprojection.MessageRow{
		{ID: "m1", Lines: []string{"a", "b", "c", "d", "e", "f", "g", "h"}},
	})

	app.setMode(ModeChat)

	if host.HasPointerCapture() {
		t.Fatal("setMode should cancel chat host capture")
	}
	if screen.IsScrollbarDragging() {
		t.Fatal("setMode should end scrollbar drag")
	}
}
