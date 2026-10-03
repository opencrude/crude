package viewmodel

import (
	"path/filepath"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/chatprojection"
	"crude/tui/editor"
	"crude/tui/screens/launcher"
	"crude/tui/screens/shellchat"
	ttypes "crude/tui/types"
)

func openTestEditor(t *testing.T, app *App) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "foo.go")
	app.EditorState.Editor = editor.NewPreview(app.Theme, path, "package main\n", 1)
	app.EditorState.ActiveView = path
	return path
}

// Root-host capture is validated by Host.Layout: when the captured component
// leaves the tree (slot deactivated), validatePointerCapture cancels it. The
// viewmodel no longer runs per-host capture arbitration (syncPersistentHost
// Capture) — unmounting is the cancel path.

func TestRootHostCapture_ChatHiddenByEditor(t *testing.T) {
	app := newFullTestApp(t)
	screen, host, _ := setupCapturedChatSelection(t, app, 1, []chatprojection.MessageRow{
		{ID: "m1", Lines: []string{"editor hides chat"}},
	})

	openTestEditor(t, app)
	app.paintShellFrame()

	if host.HasPointerCapture() {
		t.Fatal("chat capture should cancel when editor deactivates the chat slot")
	}
	if screen.IsSelecting() {
		t.Fatal("chat selection should cancel when editor is active")
	}

	app.EditorState.ActiveView = ""
	app.EditorState.Editor.Close()
	app.paintShellFrame()

	if host.HasPointerCapture() {
		t.Fatal("returning to chat must not restore latent capture")
	}
	if screen.IsSelecting() {
		t.Fatal("returning to chat must not restore latent selection")
	}
}

func TestRootHostCapture_ChatReleaseElsewhereAfterEditor(t *testing.T) {
	app := newFullTestApp(t)
	screen, host, _ := setupCapturedChatSelection(t, app, 1, []chatprojection.MessageRow{
		{ID: "m1", Lines: []string{"release elsewhere"}},
	})

	openTestEditor(t, app)
	app.paintShellFrame()

	_, _ = app.dispatchMouseRelease(tea.MouseReleaseMsg{
		X: 10, Y: app.height - 1, Button: tea.MouseLeft,
	})

	if host.HasPointerCapture() || screen.IsSelecting() {
		t.Fatal("release while chat hidden should not leave latent chat capture")
	}

	app.EditorState.ActiveView = ""
	app.EditorState.Editor.Close()
	app.paintShellFrame()
	if host.HasPointerCapture() || screen.IsSelecting() {
		t.Fatal("return to chat after hidden release must stay clean")
	}
}

func TestRootHostCapture_SidebarHidden(t *testing.T) {
	app := newFullTestApp(t)
	host, _, _ := setupScrollableSidebarCapture(t, app)

	app.Tree.Visible = false
	app.paintShellFrame()

	if host.HasPointerCapture() {
		t.Fatal("sidebar capture should cancel when the tree is hidden")
	}
}

func TestRootHostCapture_LauncherHiddenByAgent(t *testing.T) {
	app := newTestAppWithLauncher(t)
	app.width = 100
	app.height = 30
	app.ChatAreaHeight = 20
	app.Tree = NewTreeState(nil, 30)
	app.Tree.Visible = false
	app.paintShellFrame()

	rect, _ := app.rootHost.ScreenRect(launcher.WorkspaceListID)
	res := app.rootHost.Handle(tea.MouseClickMsg{X: rect.X + 1, Y: rect.Y, Button: tea.MouseLeft})
	if !res.Handled || !app.rootHost.HasPointerCapture() {
		t.Fatal("launcher click should capture pointer")
	}

	app.AddOrchestrator(
		"agent")

	if app.rootHost.HasPointerCapture() {
		t.Fatal("launcher capture should cancel when agent replaces launcher")
	}
}

func TestRootHostCapture_LauncherHiddenByMinSize(t *testing.T) {
	app := newTestAppWithLauncher(t)
	app.width = 100
	app.height = 30
	app.ChatAreaHeight = 20
	app.Tree = NewTreeState(nil, 30)
	app.paintShellFrame()

	rect, _ := app.rootHost.ScreenRect(launcher.WorkspaceListID)
	res := app.rootHost.Handle(tea.MouseClickMsg{X: rect.X + 1, Y: rect.Y, Button: tea.MouseLeft})
	if !res.Handled || !app.rootHost.HasPointerCapture() {
		t.Fatal("launcher click should capture pointer")
	}

	// Shrink the terminal so the launcher chat area drops below the
	// 3-row minimum (computeContentLayout derives ChatAreaHeight from
	// real geometry every frame — manual overrides do not stick). The
	// launcher needs ChatAreaHeight >= 3, i.e. height >= top chrome +
	// bottom chrome + 3.
	app.height = app.chromeTopRow() + app.chromeBottomRows() + 2
	app.paintShellFrame()
	if app.rootHost.HasPointerCapture() {
		t.Fatal("launcher capture should cancel when min-size gate hides launcher")
	}
}

// TestRootHostCapture_SingleCaptureService verifies the root host is the one
// capture owner: a drag captured anywhere keeps routing to the captured
// component even when the pointer crosses every other shell region.
func TestRootHostCapture_SingleCaptureService(t *testing.T) {
	app := newFullTestApp(t)
	screen, host, _ := setupCapturedChatSelection(t, app, 1, []chatprojection.MessageRow{
		{ID: "m1", Lines: []string{"single capture owner"}},
	})

	// Motion over the header, sidebar, tab bar, and status rows — every
	// motion must keep the chat selection drag alive.
	for _, pt := range [][2]int{{5, 1}, {app.Tree.Width - 1, 6}, {40, 3}, {40, app.height - 1}} {
		_, _ = app.dispatchMouseMotion(tea.MouseMotionMsg{X: pt[0], Y: pt[1], Button: tea.MouseLeft})
	}
	if !host.HasPointerCapture() || !screen.IsSelecting() {
		t.Fatal("captured chat drag must survive motion across all shell regions")
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
		t.Fatal("keyboard should cancel root host capture")
	}
	if screen.IsSelecting() {
		t.Fatal("keyboard should end in-progress selection")
	}

	res := app.rootHandleFocus(shellchat.ScreenID, ttypes.SelectionTickMsg(time.Now()))
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
	host.Handle(tea.MouseMotionMsg{X: app.chatRect.X + padLR + 6, Y: app.chatRect.Y, Button: tea.MouseLeft})

	if !screen.IsSelecting() || !screen.HasActiveSelection() {
		t.Fatal("expected in-progress non-empty selection")
	}
	if !host.HasPointerCapture() {
		t.Fatal("expected root host capture during selection drag")
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
		t.Fatal("setMode should cancel root host capture")
	}
	if screen.IsSelecting() {
		t.Fatal("setMode should clear in-progress selection")
	}
}

func TestSetMode_ClearsCapturedScrollbarDrag(t *testing.T) {
	app := newFullTestApp(t)
	screen, host := setupCapturedChatScrollbar(t, app, 3, []chatprojection.MessageRow{
		{ID: "m1", Lines: []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l", "m", "n", "o", "p", "q", "r", "s", "t", "u", "v", "w", "x", "y", "z"}},
	})

	app.setMode(ModeChat)

	if host.HasPointerCapture() {
		t.Fatal("setMode should cancel root host capture")
	}
	if screen.IsScrollbarDragging() {
		t.Fatal("setMode should end scrollbar drag")
	}
}
