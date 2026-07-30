package viewmodel

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/provider"
	"crude/tui/chatprojection"
	"crude/tui/filetree"
	"crude/tui/screens/shellchat"
	"crude/tui/screens/shelltree"
	"crude/tui/theme"
	ttypes "crude/tui/types"
	"crude/tui/ui"
	"crude/workspace"
)

func setupCapturedChatSelection(t *testing.T, app *App, viewportH int, rows []chatprojection.MessageRow) (*shellchat.Screen, *ui.Host, int) {
	t.Helper()
	if app == nil {
		app = newFullTestApp(t)
	}
	th := theme.DefaultDark()
	padLR := th.Spacing.PadLR
	innerW := 40

	screen := shellchat.New(th)
	screen.SetMessages(rows, shellchat.PaintParams{
		Compose: shellchat.ComposeParams{
			InnerW: innerW,
			PadLR:  padLR,
			Theme:  th,
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
		contentH = viewportH
	}
	screen.SyncScrollMetrics(viewportH, contentH)
	w := shellchat.ContentSurfaceWidth(innerW, padLR, false)
	host := ui.NewHost(th, screen)
	host.Resize(w, viewportH)
	host.Layout()

	app.chatScreen = screen
	app.chatHost = host
	app.chatRect = ui.Rect{X: 30, Y: 5, W: w, H: viewportH}
	app.height = 24
	app.width = 80
	if app.Tree == nil {
		app.Tree = NewTreeState(nil, 30)
	}
	app.Tree.Visible = true
	if app.AgentMgr.Count() == 0 {
		app.AddOrchestrator(
			"agent",
			make(chan provider.UserInput, 1),
			make(chan provider.UserInput, 1),
			make(chan struct{}, 1),
			make(chan string, 1),
		)
	}
	app.launcherScreen = nil
	app.launcherHost = nil

	localX := padLR + 1
	res := host.Handle(tea.MouseClickMsg{X: localX, Y: 0, Button: tea.MouseLeft})
	if !res.Handled || !host.HasPointerCapture() {
		t.Fatal("chat host click should handle and capture pointer")
	}
	if !screen.IsSelecting() {
		t.Fatal("expected in-progress text selection")
	}
	return screen, host, localX
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
		t.Fatal("release should clear host pointer capture")
	}
	if screen.IsSelecting() {
		t.Fatal("selection should finish when release routed to chatHost while captured")
	}
}

func TestDispatchMouseRelease_CapturedChatOverSidebar(t *testing.T) {
	th := theme.DefaultDark()
	treeModel := filetree.NewFileTreeModel(t.TempDir())
	app := newFullTestApp(t)
	app.Tree = NewTreeState(treeModel, 30)
	app.Tree.Visible = true
	sidebarScreen := shelltree.New(th, treeModel)
	app.sidebarHost = ui.NewHost(th, sidebarScreen)
	app.sidebarHost.Resize(app.Tree.Width, 20)
	app.sidebarHost.Layout()

	screen, host, _ := setupCapturedChatSelection(t, app, 1, []chatprojection.MessageRow{
		{ID: "m1", Lines: []string{"sidebar release test"}},
	})

	sidebarX := app.Tree.Width - 2
	sidebarY := 8
	_, _ = app.dispatchMouseRelease(tea.MouseReleaseMsg{
		X: sidebarX, Y: sidebarY, Button: tea.MouseLeft,
	})

	if host.HasPointerCapture() {
		t.Fatal("sidebar release coords should still route to captured chatHost")
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
		t.Fatal("popup motion must not steal chat pointer capture")
	}
	if !screen.IsSelecting() {
		t.Fatal("chat selection must continue while captured over popup")
	}
	if paletteHost.Hovered() != hoverBefore {
		t.Fatal("popup hover must not update while chatHost owns capture")
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
	th := theme.DefaultDark()
	treeModel := filetree.NewFileTreeModel(t.TempDir())
	app := newFullTestApp(t)
	app.Tree = NewTreeState(treeModel, 30)
	app.Tree.Visible = true
	sidebarScreen := shelltree.New(th, treeModel)
	app.sidebarHost = ui.NewHost(th, sidebarScreen)
	app.sidebarHost.Resize(app.Tree.Width, 20)
	app.sidebarHost.Layout()

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
		t.Fatal("sidebar hover must not update while chatHost owns capture")
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
	app.launcherHost = nil
	app.AddOrchestrator(
		"agent",
		make(chan provider.UserInput, 1),
		make(chan provider.UserInput, 1),
		make(chan struct{}, 1),
		make(chan string, 1),
	)

	th := theme.DefaultDark()
	sidebarScreen := shelltree.New(th, treeModel)
	app.sidebarScreen = sidebarScreen
	host := ui.NewHost(th, sidebarScreen)
	app.sidebarHost = host

	treeModel.Update(filetree.TreeHeightMsg{Height: 10})
	host.Resize(app.Tree.Width+1, app.height-app.Tree.YOffset-2)
	host.Layout()

	scrollbarX := app.Tree.Width - 1
	clickY := app.Tree.YOffset + 4
	_, _ = app.dispatchMouseClick(tea.MouseClickMsg{X: scrollbarX, Y: clickY, Button: tea.MouseLeft})
	if !host.HasPointerCapture() {
		t.Fatal("sidebar scrollbar click should capture pointer")
	}
	return host, treeModel, scrollbarX
}

func setupCapturedChatScrollbar(t *testing.T, app *App, viewportH int, rows []chatprojection.MessageRow) (*shellchat.Screen, *ui.Host) {
	t.Helper()
	if app == nil {
		app = newFullTestApp(t)
	}
	th := theme.DefaultDark()
	padLR := th.Spacing.PadLR
	innerW := 40

	screen := shellchat.New(th)
	screen.SetMessages(rows, shellchat.PaintParams{
		Compose: shellchat.ComposeParams{
			InnerW:        innerW,
			PadLR:         padLR,
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
		contentH = viewportH
	}
	screen.SyncScrollMetrics(viewportH, contentH)
	w := shellchat.ContentSurfaceWidth(innerW, padLR, true)
	host := ui.NewHost(th, screen)
	host.Resize(w, viewportH)
	host.Layout()

	app.chatScreen = screen
	app.chatHost = host
	app.chatRect = ui.Rect{X: 30, Y: 5, W: w, H: viewportH}
	app.height = 24
	app.width = 80
	if app.Tree == nil {
		app.Tree = NewTreeState(nil, 30)
	}
	app.Tree.Visible = true
	if app.AgentMgr.Count() == 0 {
		app.AddOrchestrator(
			"agent",
			make(chan provider.UserInput, 1),
			make(chan provider.UserInput, 1),
			make(chan struct{}, 1),
			make(chan string, 1),
		)
	}
	app.launcherScreen = nil
	app.launcherHost = nil

	if screen.ScrollModel().MaxOffset() <= 0 {
		t.Fatal("expected scrollable chat content for scrollbar capture")
	}
	sbX := w - padLR - 1
	res := host.Handle(tea.MouseClickMsg{X: sbX, Y: 0, Button: tea.MouseLeft})
	if !res.Handled || !host.HasPointerCapture() {
		t.Fatal("chat scrollbar click should handle and capture pointer")
	}
	if !screen.IsScrollbarDragging() {
		t.Fatal("expected in-progress scrollbar drag")
	}
	return screen, host
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

	layout := app.computeLayout()
	app.launcherHost.Resize(layout.InnerW, app.ChatAreaHeight)
	app.launcherHost.Layout()

	// Establish capture via the host (same as a handled workspace-row click).
	localX := layout.InnerW*40/100 + 3 + 2 + 2
	if localX >= layout.InnerW {
		localX = layout.InnerW - 2
	}
	localY := 4 + 1 + 2 + 1 // hero + blank + header/search + first row
	res := app.launcherHost.Handle(tea.MouseClickMsg{X: localX, Y: localY, Button: tea.MouseLeft})
	if !res.Handled || !app.launcherHost.HasPointerCapture() {
		t.Fatal("launcher list click should capture pointer")
	}

	releaseY := layout.ChatAreaTop + app.ChatAreaHeight + 2
	clickX := layout.SidebarW + layout.PadLR + localX
	_, _ = app.dispatchMouseRelease(tea.MouseReleaseMsg{
		X: clickX, Y: releaseY, Button: tea.MouseLeft,
	})
	if app.launcherHost.HasPointerCapture() {
		t.Fatal("launcher release outside main pane should clear host capture")
	}
}
