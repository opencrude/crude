package viewmodel

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/chatprojection"
	"crude/tui/filetree"
	"crude/tui/screens/launcher"
	"crude/tui/screens/shellchat"
	ttypes "crude/tui/types"
	"crude/tui/ui"
	"crude/workspace"
)

// chatViewportApp sizes the app so the chat viewport (chatRect.H) equals
// viewportH through the production layout path:
// ChatH = height - 2 (footer) - 3 (padding+header) - inputLineCount(1).
func chatViewportApp(t *testing.T, app *App, viewportH int) *App {
	t.Helper()
	if app == nil {
		app = newFullTestApp(t)
	}
	app.width = 80
	app.height = viewportH + 6 + ui.StripHeight + shellchat.ActivityHeight
	return app
}

// setupCapturedChatSelection mounts a chat screen into the shell tree with
// the given rows, sizes the app so the chat viewport matches viewportH, and
// clicks inside the chat area to start a text-selection drag. The ROOT host
// owns the capture. Returns (screen, rootHost, chat-local click X).
func setupCapturedChatSelection(t *testing.T, app *App, viewportH int, rows []chatprojection.MessageRow) (*shellchat.Screen, *ui.Host, int) {
	t.Helper()
	app = chatViewportApp(t, app, viewportH)
	app.launcherScreen = nil
	if app.Tree == nil {
		app.Tree = NewTreeState(nil, 30)
	}
	app.Tree.Visible = true
	if app.ws.AgentMgr().Count() == 0 {
		app.AddOrchestrator(
			"agent")

	}
	for _, row := range rows {
		app.appendActiveMessage(ttypes.ChatMessage{
			Role:    ttypes.RoleAssistant,
			Content: strings.Join(row.Lines, "\n"),
			Time:    time.Now(),
		})
	}
	app.syncAgentMessages("agent", false)

	app.paintShellFrame()

	padLR := app.Theme.Spacing.PadLR
	localX := padLR + 1
	res := app.rootHost.Handle(tea.MouseClickMsg{X: app.chatRect.X + localX, Y: app.chatRect.Y, Button: tea.MouseLeft})
	if !res.Handled || !app.rootHost.HasPointerCapture() {
		t.Fatal("chat click should handle and capture pointer on the root host")
	}
	if !app.chatScreen.IsSelecting() {
		t.Fatalf("expected in-progress text selection: slot=%+v viewport=%d appH=%d", app.chatRect, app.chatScreen.ScrollModel().Viewport(), app.height)
	}
	return app.chatScreen, app.rootHost, localX
}

func TestDispatchMouseRelease_RoutesCapturedChatOutsideRegion(t *testing.T) {
	app := newFullTestApp(t)
	screen, host, _ := setupCapturedChatSelection(t, app, 1, []chatprojection.MessageRow{
		{ID: "m1", Lines: []string{"capture routing hello"}},
	})

	releaseX := app.chatRect.X + app.Theme.Spacing.PadLR + 8
	releaseY := app.height - 1
	_, _ = app.dispatchMouseRelease(tea.MouseReleaseMsg{
		X: releaseX, Y: releaseY, Button: tea.MouseLeft,
	})

	if host.HasPointerCapture() {
		t.Fatal("release should clear root host pointer capture")
	}
	if screen.IsSelecting() {
		t.Fatal("selection should finish when release routed to captured chat while outside the region")
	}
}

func TestDispatchMouseRelease_CapturedChatOverSidebar(t *testing.T) {
	treeModel := filetree.NewFileTreeModel(t.TempDir())
	app := newFullTestApp(t)
	app.Tree = NewTreeState(treeModel, 30)
	app.Tree.Visible = true
	app.sidebarScreen.SetModel(treeModel)

	screen, host, _ := setupCapturedChatSelection(t, app, 1, []chatprojection.MessageRow{
		{ID: "m1", Lines: []string{"sidebar release test"}},
	})

	sidebarX := app.Tree.Width - 2
	sidebarY := 8
	_, _ = app.dispatchMouseRelease(tea.MouseReleaseMsg{
		X: sidebarX, Y: sidebarY, Button: tea.MouseLeft,
	})

	if host.HasPointerCapture() {
		t.Fatal("sidebar release coords should still route to captured chat")
	}
	if screen.IsSelecting() {
		t.Fatal("captured chat selection should finish on release over sidebar")
	}
}

func TestDispatchMouseMotion_CapturedChatOverPopup(t *testing.T) {
	app := newFullTestApp(t)
	screen, host, _ := setupCapturedChatSelection(t, app, 1, []chatprojection.MessageRow{
		{ID: "m1", Lines: []string{"popup motion while captured"}},
	})

	app.registerOverlayHandlers()
	if _, cmd := app.toggleCommandPalette(); cmd != nil {
		t.Fatal("unexpected cmd opening command palette")
	}
	fw := app.overlayHost.Window(ui.OverlayCommandPalette)
	if fw == nil {
		t.Fatal("command palette window missing")
	}
	cw, ok := fw.Content.(*ui.ComponentWidget)
	if !ok {
		t.Fatalf("palette content is %T", fw.Content)
	}
	paletteHost := cw.Host()
	sx, sy := fw.Position()
	w, h := fw.Size()
	paletteHost.Resize(w, h)
	paletteHost.Layout()

	hoverBefore := paletteHost.Hovered()
	_, cmd := app.dispatchMouseMotion(tea.MouseMotionMsg{
		X: sx + w/2, Y: sy + h/2, Button: tea.MouseLeft,
	})

	if !host.HasPointerCapture() {
		t.Fatal("popup motion must not steal root host pointer capture")
	}
	if !screen.IsSelecting() {
		t.Fatal("chat selection must continue while captured over popup")
	}
	if paletteHost.Hovered() != hoverBefore {
		t.Fatal("popup hover must not update while the root host owns capture")
	}
	_ = cmd
}

func TestDispatchMouseRelease_CapturedChatOverPopup(t *testing.T) {
	app := newFullTestApp(t)
	screen, host, _ := setupCapturedChatSelection(t, app, 1, []chatprojection.MessageRow{
		{ID: "m1", Lines: []string{"popup release while captured"}},
	})

	app.registerOverlayHandlers()
	if _, cmd := app.toggleCommandPalette(); cmd != nil {
		t.Fatal("unexpected cmd opening command palette")
	}
	fw := app.overlayHost.Window(ui.OverlayCommandPalette)
	cw := fw.Content.(*ui.ComponentWidget)
	paletteHost := cw.Host()
	sx, sy := fw.Position()
	w, h := fw.Size()
	paletteHost.Resize(w, h)
	paletteHost.Layout()

	var paletteActivated string
	app.commandPaletteModel.callback = func(id string) tea.Cmd {
		paletteActivated = id
		return nil
	}

	_, _ = app.dispatchMouseRelease(tea.MouseReleaseMsg{
		X: sx + w/2, Y: sy + h/2, Button: tea.MouseLeft,
	})

	if host.HasPointerCapture() {
		t.Fatal("release over popup should complete captured chat drag")
	}
	if screen.IsSelecting() {
		t.Fatal("chat selection should end on release over popup")
	}
	if paletteActivated != "" {
		t.Fatal("popup must not receive captured chat release as activation")
	}
}

func TestDispatchMouseMotion_CapturedSelectionAutoScrollOutsideViewport(t *testing.T) {
	lines := make([]string, 12)
	for i := range lines {
		lines[i] = "scroll line"
	}
	app := newFullTestApp(t)
	screen, host, localX := setupCapturedChatSelection(t, app, 5, []chatprojection.MessageRow{
		{ID: "m1", Lines: lines},
	})

	scroll := screen.ScrollModel()
	beforeOffset := scroll.Offset()

	belowY := app.chatRect.Y + app.chatRect.H
	_, cmd := app.dispatchMouseMotion(tea.MouseMotionMsg{
		X: app.chatRect.X + localX, Y: belowY, Button: tea.MouseLeft,
	})

	if !host.HasPointerCapture() || !screen.IsSelecting() {
		t.Fatal("selection drag must stay active when pointer leaves chat viewport")
	}
	if cmd == nil {
		t.Fatal("motion below viewport should arm selection auto-scroll tick")
	}

	msg := cmd()
	tick, ok := msg.(ttypes.SelectionTickMsg)
	if !ok {
		t.Fatalf("expected SelectionTickMsg, got %T", msg)
	}

	_, tickCmd := app.Update(tick)
	if scroll.Offset() <= beforeOffset && scroll.MaxOffset() > beforeOffset {
		t.Fatalf("selection tick should scroll down, offset=%d before=%d max=%d",
			scroll.Offset(), beforeOffset, scroll.MaxOffset())
	}
	_ = tickCmd

	_, _ = app.dispatchMouseRelease(tea.MouseReleaseMsg{
		X: app.chatRect.X + localX, Y: belowY, Button: tea.MouseLeft,
	})
	if host.HasPointerCapture() {
		t.Fatal("matching release should clear capture after auto-scroll drag")
	}
	if screen.IsSelecting() {
		t.Fatal("selection should finish after release outside viewport")
	}
}

func TestDispatchMouseMotion_CapturedChatDoesNotDuplicateSidebar(t *testing.T) {
	treeModel := filetree.NewFileTreeModel(t.TempDir())
	app := newFullTestApp(t)
	app.Tree = NewTreeState(treeModel, 30)
	app.Tree.Visible = true
	app.sidebarScreen.SetModel(treeModel)

	screen, host, _ := setupCapturedChatSelection(t, app, 1, []chatprojection.MessageRow{
		{ID: "m1", Lines: []string{"no duplicate sidebar routing"}},
	})

	hoverBefore := treeModel.Hover.Index
	_, _ = app.dispatchMouseMotion(tea.MouseMotionMsg{
		X: 5, Y: 10, Button: tea.MouseLeft,
	})

	if !host.HasPointerCapture() || !screen.IsSelecting() {
		t.Fatal("sidebar motion must not interrupt captured chat drag")
	}
	if treeModel.Hover.Index != hoverBefore {
		t.Fatal("sidebar hover must not update while the root host owns capture")
	}
}

func setupScrollableSidebarCapture(t *testing.T, app *App) (*ui.Host, *filetree.FileTreeModel, int) {
	t.Helper()
	dir := t.TempDir()
	for i := 0; i < 40; i++ {
		path := filepath.Join(dir, fmt.Sprintf("file%02d.go", i))
		if err := os.WriteFile(path, []byte("package x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	treeModel := filetree.NewFileTreeModel(dir)
	app.Tree = NewTreeState(treeModel, 30)
	app.Tree.Visible = true
	app.Tree.YOffset = 3
	app.width = 80
	app.height = 24
	app.launcherScreen = nil
	app.sidebarScreen.SetModel(treeModel)
	app.AddOrchestrator(
		"agent")

	treeModel.Update(filetree.TreeHeightMsg{Height: 10})
	app.paintShellFrame()

	scrollbarX := app.Tree.Width - 1
	clickY := app.Tree.YOffset + 4
	_, _ = app.dispatchMouseClick(tea.MouseClickMsg{X: scrollbarX, Y: clickY, Button: tea.MouseLeft})
	if !app.rootHost.HasPointerCapture() {
		t.Fatal("sidebar scrollbar click should capture pointer")
	}
	return app.rootHost, treeModel, scrollbarX
}

func setupCapturedChatScrollbar(t *testing.T, app *App, viewportH int, rows []chatprojection.MessageRow) (*shellchat.Screen, *ui.Host) {
	t.Helper()
	app = chatViewportApp(t, app, viewportH)
	app.launcherScreen = nil
	if app.Tree == nil {
		app.Tree = NewTreeState(nil, 30)
	}
	app.Tree.Visible = true
	if app.ws.AgentMgr().Count() == 0 {
		app.AddOrchestrator(
			"agent")

	}
	for _, row := range rows {
		app.appendActiveMessage(ttypes.ChatMessage{
			Role:    ttypes.RoleAssistant,
			Content: strings.Join(row.Lines, "\n\n"),
			Time:    time.Now(),
		})
	}
	app.syncAgentMessages("agent", false)

	app.paintShellFrame()

	if app.chatScreen.ScrollModel().MaxOffset() <= 0 {
		t.Fatal("expected scrollable chat content for scrollbar capture")
	}
	padLR := app.Theme.Spacing.PadLR
	sbX := app.chatRect.W - padLR - 1
	thumbTop, _ := app.chatScreen.ScrollModel().ThumbBounds(app.chatScreen.ScrollModel().Viewport())
	res := app.rootHost.Handle(tea.MouseClickMsg{X: app.chatRect.X + sbX, Y: app.chatRect.Y + thumbTop, Button: tea.MouseLeft})
	if !res.Handled || !app.rootHost.HasPointerCapture() {
		t.Fatal("chat scrollbar click should handle and capture pointer")
	}
	if !app.chatScreen.IsScrollbarDragging() {
		t.Fatal("expected in-progress scrollbar drag")
	}
	return app.chatScreen, app.rootHost
}

func TestDispatchMouseMotion_CapturedSidebarCrossesMainContent(t *testing.T) {
	app := newFullTestApp(t)
	host, _, _ := setupScrollableSidebarCapture(t, app)
	mainX := app.Tree.Width + 20
	mainY := app.Tree.YOffset + 8
	_, _ = app.dispatchMouseMotion(tea.MouseMotionMsg{
		X: mainX, Y: mainY, Button: tea.MouseLeft,
	})

	if !host.HasPointerCapture() {
		t.Fatal("sidebar scrollbar drag must stay captured when pointer crosses main content")
	}
}

func TestDispatchMouseRelease_CapturedSidebarCrossesMainContent(t *testing.T) {
	app := newFullTestApp(t)
	host, _, _ := setupScrollableSidebarCapture(t, app)

	mainX := app.Tree.Width + 20
	mainY := app.Tree.YOffset + 8
	_, _ = app.dispatchMouseRelease(tea.MouseReleaseMsg{
		X: mainX, Y: mainY, Button: tea.MouseLeft,
	})

	if host.HasPointerCapture() {
		t.Fatal("sidebar release over main content should clear host capture")
	}
}

func TestDispatchMouseRelease_CapturedSidebarWrongButtonPreservesCapture(t *testing.T) {
	app := newFullTestApp(t)
	host, _, _ := setupScrollableSidebarCapture(t, app)

	_, _ = app.dispatchMouseRelease(tea.MouseReleaseMsg{
		X: app.Tree.Width + 10, Y: app.Tree.YOffset + 8, Button: tea.MouseRight,
	})
	if !host.HasPointerCapture() {
		t.Fatal("non-matching release must not clear sidebar capture")
	}

	_, _ = app.dispatchMouseRelease(tea.MouseReleaseMsg{
		X: app.Tree.Width + 10, Y: app.Tree.YOffset + 8, Button: tea.MouseLeft,
	})
	if host.HasPointerCapture() {
		t.Fatal("matching release should clear sidebar capture")
	}
}

func TestDispatchMouseRelease_CapturedLauncherOutsideRegion(t *testing.T) {
	app := newTestAppWithLauncher(t)
	app.width = 100
	app.height = 30
	app.ChatAreaHeight = 20
	app.Tree = NewTreeState(nil, 30)
	app.Tree.Visible = false

	workspaces := make([]workspace.WorkspaceInfo, 8)
	for i := range workspaces {
		workspaces[i] = workspace.WorkspaceInfo{
			ProjectName: fmt.Sprintf("workspace-%02d", i),
			ProjectDir:  fmt.Sprintf("/tmp/workspace-%02d", i),
		}
	}
	app.launcherScreen.SetData(".", workspaces)
	app.paintShellFrame()

	rect, _ := app.rootHost.ScreenRect(launcher.WorkspaceListID)
	res := app.rootHost.Handle(tea.MouseClickMsg{X: rect.X + 1, Y: rect.Y, Button: tea.MouseLeft})
	if !res.Handled || !app.rootHost.HasPointerCapture() {
		t.Fatal("launcher list click should capture pointer")
	}

	releaseY := app.height - 1
	clickX := rect.X + 1
	_, _ = app.dispatchMouseRelease(tea.MouseReleaseMsg{
		X: clickX, Y: releaseY, Button: tea.MouseLeft,
	})
	if app.rootHost.HasPointerCapture() {
		t.Fatal("launcher release outside main pane should clear host capture")
	}
}
