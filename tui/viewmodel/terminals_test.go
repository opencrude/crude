package viewmodel

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/screens/shellterm"
	"crude/tui/screens/termdock"
	"crude/tui/ui"
	"crude/workspace"
)

func TestTerminalPickerUsesMatchingSuffixNumbersAndNamedPlacement(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("in-app PTYs are unix-only")
	}
	app := newFullTestApp(t)
	app.width, app.height = 120, 40
	manager := app.termMgr()
	t.Cleanup(func() {
		app.discardTerminalUI()
		manager.CloseAll()
	})
	for range 16 {
		terminal, err := manager.Create(workspace.TerminalCreateOpts{Command: "/bin/cat", CWD: t.TempDir(), Cols: 40, Rows: 8, Title: "Terminal"})
		if err != nil {
			t.Fatal(err)
		}
		app.dockTerminal(terminal.ID)
	}
	app.paintShellFrame()
	app.openTerminalPicker()
	items := app.LastContextMenu.Sections[0].Items
	if len(items) != 17 {
		t.Fatalf("menu contains %d entries, want new plus sixteen terminals", len(items))
	}
	for number, item := range items[1:] {
		if item.Label != fmt.Sprintf("Terminal %d", number+1) || item.Detail != "Docked" {
			t.Fatalf("picker item %d=%+v must show a real number and readable placement", number+1, item)
		}
	}
	app.closeContextMenu()
	ids := app.termDock.IDs()
	app.promoteTerminal(ids[0])
	app.openTerminalPicker()
	items = app.LastContextMenu.Sections[0].Items
	if items[1].Label != "Terminal 16" || items[1].Detail != "Floating" || items[2].Label != "Terminal 1" {
		t.Fatalf("floating/docked numbering is inconsistent: %+v", items[:3])
	}
}

func TestTerminalTabWheelRoutesThroughShellWithoutChangingPTY(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 100, 30
	for number := 1; number <= 16; number++ {
		id := fmt.Sprintf("terminal-fixture-%d", number)
		terminal := shellterm.New(app.Theme, id, "Terminal")
		t.Cleanup(terminal.Close)
		app.termDock.Mount(id, terminal)
	}
	app.paintShellFrame()
	strip, ok := app.rootHost.ScreenRect(termdock.TabsID)
	if !ok {
		t.Fatal("terminal tabs not mounted")
	}
	for range 20 {
		app.dispatchMouseWheel(tea.MouseWheelMsg{X: strip.X + 3, Y: strip.Y, Button: tea.MouseWheelLeft})
	}
	app.paintShellFrame()
	row := rowText(app, strip.Y)
	if !strings.Contains(row, "T 1") || app.termDock.ActiveID() != "terminal-fixture-16" {
		t.Fatalf("shell wheel did not browse earlier terminal labels: %q", row)
	}
	for range 20 {
		app.dispatchMouseWheel(tea.MouseWheelMsg{X: strip.X + 3, Y: strip.Y, Button: tea.MouseWheelRight})
	}
	app.paintShellFrame()
	if row := rowText(app, strip.Y); !strings.Contains(row, "T 16") || app.termDock.ActiveID() != "terminal-fixture-16" {
		t.Fatalf("shell wheel did not browse back to the last terminal: %q", row)
	}
}

func TestTerminalPickerActionsKeepRealIDsAfterRenumbering(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("in-app PTYs are unix-only")
	}
	app := newFullTestApp(t)
	app.width, app.height = 120, 40
	manager := app.termMgr()
	t.Cleanup(func() {
		app.discardTerminalUI()
		manager.CloseAll()
	})
	var ids []string
	for range 3 {
		terminal, err := manager.Create(workspace.TerminalCreateOpts{Command: "/bin/cat", CWD: t.TempDir(), Cols: 40, Rows: 8, Title: "Terminal"})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, terminal.ID)
		app.dockTerminal(terminal.ID)
	}
	app.paintShellFrame()
	app.openTerminalPicker()
	items := app.LastContextMenu.Sections[0].Items
	closedAction, secondAction := items[1].Action, items[2].Action
	app.closeContextMenu()
	app.closeTerminal(ids[0])
	if app.termDock.Number(ids[1]) != 1 {
		t.Fatal("remaining terminal did not receive the first display number")
	}
	secondAction()
	if app.termDock.ActiveID() != ids[1] {
		t.Fatal("saved picker action targeted a display position instead of its original PTY")
	}
	closedAction()
	if app.termDock.ActiveID() != ids[1] {
		t.Fatal("closed terminal's action selected the new occupant of its old display number")
	}
	if _, exists := manager.Get(ids[0]); exists {
		t.Fatal("closed picker action recreated its old terminal")
	}
}

func TestDockAndPromoteTerminalUsesWorkspaceDockAndOverlay(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("in-app PTYs are unix-only")
	}
	app := newFullTestApp(t)
	app.width, app.height = 120, 40
	mgr := app.termMgr()
	if mgr == nil {
		t.Fatal("TermMgr missing")
	}
	info, err := mgr.Create(workspace.TerminalCreateOpts{
		Command: "/bin/cat",
		CWD:     t.TempDir(),
		Cols:    40,
		Rows:    12,
		Title:   "cat",
	})
	if err != nil {
		t.Skipf("pty create: %v", err)
	}
	t.Cleanup(func() { mgr.Close(info.ID) })

	app.dockTerminal(info.ID)
	app.paintShellFrame()
	if app.termDock == nil || !app.termDock.Contains(info.ID) {
		t.Fatal("docked terminal missing from workspace terminal dock")
	}
	if !app.shellSlotActive(shellTermDockSlot) {
		t.Fatal("terminal dock slot must be mounted")
	}
	if projection := app.activeSessionDeck(); projection != nil && projection.deck != nil {
		for _, p := range projection.deck.Panels() {
			if _, ok := terminalIDFromPanel(p.ID); ok {
				t.Fatal("terminals must not share the session PanelDeck")
			}
		}
	}
	if app.overlayHost.IsOpen(ui.TerminalOverlayID(info.ID)) {
		t.Fatal("docked terminal must not also be a floating window")
	}

	app.promoteTerminal(info.ID)
	app.paintShellFrame()
	if !app.overlayHost.IsOpen(ui.TerminalOverlayID(info.ID)) {
		t.Fatal("promoted terminal must open a floating window")
	}
	if app.termDock.Contains(info.ID) {
		t.Fatal("promoted terminal must leave the dock")
	}
	got, ok := mgr.Get(info.ID)
	if !ok || got.Placement != workspace.TerminalFloating {
		t.Fatalf("placement=%v floating=%v", got.Placement, ok)
	}

	app.dockTerminal(info.ID)
	app.paintShellFrame()
	if app.overlayHost.IsOpen(ui.TerminalOverlayID(info.ID)) {
		t.Fatal("dock must close the floating window without killing the PTY")
	}
	if _, ok := mgr.Get(info.ID); !ok {
		t.Fatal("PTY must survive dock/promote")
	}
	if !app.termDock.Contains(info.ID) {
		t.Fatal("redocked terminal missing from dock")
	}
}

func TestFloatingTerminalBlinkInvalidatesRetainedWindow(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("in-app PTYs are unix-only")
	}
	app := newFullTestApp(t)
	app.width, app.height = 120, 40
	mgr := app.termMgr()
	info, err := mgr.Create(workspace.TerminalCreateOpts{
		Command: "/bin/cat",
		CWD:     t.TempDir(),
		Cols:    40,
		Rows:    12,
	})
	if err != nil {
		t.Skipf("pty create: %v", err)
	}
	t.Cleanup(func() {
		app.discardTerminalUI()
		mgr.Close(info.ID)
	})

	app.promoteTerminal(info.ID)
	screen := app.termScreens[info.ID]
	fw := app.overlayHost.Window(ui.TerminalOverlayID(info.ID))
	if screen == nil || fw == nil {
		t.Fatal("floating terminal projection missing")
	}
	screen.Feed([]byte("x\x1b[1;1H"))
	screen.SetTermFocused(true)

	// First render performs the floating layout (a resize is cursor activity).
	fw.Render(app.renderCtx)
	base := time.Now()
	app.tickTerminalCursors(base)
	fw.MarkContentDirty()
	fw.Render(app.renderCtx)
	if fw.IsContentDirty() {
		t.Fatal("window remained dirty after rendering the current cursor phase")
	}

	off := base.Add(ui.CursorActivityHoldMS * time.Millisecond)
	for (off.UnixMilli()/ui.CursorBlinkHalfPeriodMS)%2 == 0 {
		off = off.Add(ui.CursorBlinkHalfPeriodMS * time.Millisecond)
	}
	app.tickTerminalCursors(off)
	if !fw.IsContentDirty() {
		t.Fatal("blink phase change did not invalidate floating terminal content")
	}
}

func TestPromoteTwoTerminalsStacksByZOrder(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("in-app PTYs are unix-only")
	}
	app := newFullTestApp(t)
	app.width, app.height = 120, 40
	mgr := app.termMgr()
	first, err := mgr.Create(workspace.TerminalCreateOpts{Command: "/bin/cat", CWD: t.TempDir(), Cols: 20, Rows: 8})
	if err != nil {
		t.Skipf("pty create: %v", err)
	}
	second, err := mgr.Create(workspace.TerminalCreateOpts{Command: "/bin/cat", CWD: t.TempDir(), Cols: 20, Rows: 8})
	if err != nil {
		t.Skipf("pty create: %v", err)
	}
	t.Cleanup(func() {
		mgr.Close(first.ID)
		mgr.Close(second.ID)
	})
	app.promoteTerminal(first.ID)
	app.promoteTerminal(second.ID)
	top := app.windowManager.TopWindow()
	if top == nil || top.ID != string(ui.TerminalOverlayID(second.ID)) {
		t.Fatalf("top=%v want second terminal", top)
	}
	app.windowManager.FocusWindow(string(ui.TerminalOverlayID(first.ID)))
	top = app.windowManager.TopWindow()
	if top == nil || top.ID != string(ui.TerminalOverlayID(first.ID)) {
		t.Fatalf("after focus top=%v want first terminal", top)
	}
}

func TestTerminalDockAvailableWithoutAISession(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("in-app PTYs are unix-only")
	}
	app := newFullTestApp(t)
	app.width, app.height = 120, 40
	app.AgentMgr = workspace.NewAgentManager()
	app.ActiveSession = nil
	mgr := app.termMgr()
	info, err := mgr.Create(workspace.TerminalCreateOpts{Command: "/bin/cat", CWD: t.TempDir(), Cols: 20, Rows: 8})
	if err != nil {
		t.Skipf("pty create: %v", err)
	}
	t.Cleanup(func() { mgr.Close(info.ID) })
	app.dockTerminal(info.ID)
	app.paintShellFrame()
	if app.overlayHost.IsOpen(ui.TerminalOverlayID(info.ID)) {
		t.Fatal("new docked terminal must not float merely because no AI session exists")
	}
	if !app.termDock.Contains(info.ID) {
		t.Fatal("expected workspace dock mount without an AI session")
	}
	lay := app.computeContentLayout()
	if lay.TermDockRect.Empty() {
		t.Fatal("dock rect empty")
	}
	if lay.TermDockRect.Y+lay.TermDockRect.H != app.height-app.chromeBottomRows() {
		t.Fatalf("dock must end at bottom chrome: dock=%v height=%d", lay.TermDockRect, app.height)
	}
}

func TestTerminalDockTabsAndClose(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("in-app PTYs are unix-only")
	}
	app := newFullTestApp(t)
	app.width, app.height = 120, 40
	mgr := app.termMgr()
	a, err := mgr.Create(workspace.TerminalCreateOpts{Command: "/bin/cat", CWD: t.TempDir(), Cols: 20, Rows: 8, Title: "one"})
	if err != nil {
		t.Skipf("pty create: %v", err)
	}
	b, err := mgr.Create(workspace.TerminalCreateOpts{Command: "/bin/cat", CWD: t.TempDir(), Cols: 20, Rows: 8, Title: "two"})
	if err != nil {
		t.Skipf("pty create: %v", err)
	}
	t.Cleanup(func() {
		mgr.Close(a.ID)
		mgr.Close(b.ID)
	})
	app.dockTerminal(a.ID)
	app.dockTerminal(b.ID)
	if got := app.termDock.IDs(); len(got) != 2 {
		t.Fatalf("ids=%v", got)
	}
	if app.termDock.ActiveID() != b.ID {
		t.Fatalf("active=%s want %s", app.termDock.ActiveID(), b.ID)
	}
	app.closeTerminal(b.ID)
	if _, ok := mgr.Get(b.ID); ok {
		t.Fatal("closed tab must kill that PTY")
	}
	if _, ok := mgr.Get(a.ID); !ok {
		t.Fatal("other PTY must survive")
	}
	app.closeTerminal(a.ID)
	app.paintShellFrame()
	if app.termDockVisible() || app.shellSlotActive(shellTermDockSlot) {
		t.Fatal("last close must hide the dock")
	}
}

func TestTerminalDockDoesNotOverlapMainContent(t *testing.T) {
	app := newFullTestApp(t)
	for _, size := range [][2]int{{80, 24}, {120, 40}, {60, 20}} {
		app.width, app.height = size[0], size[1]
		app.termDockHeight = 12
		if app.termDock == nil {
			app.termDock = termdock.New(app.Theme)
		}
		screen := app.ensureTermScreen("layout", "layout")
		app.termDock.Mount("layout", screen)
		lay := app.computeContentLayout()
		dock := lay.TermDockRect
		if dock.Empty() {
			t.Fatalf("size %v: dock hidden", size)
		}
		if !lay.EditorRect.Empty() && lay.EditorRect.Y+lay.EditorRect.H > dock.Y {
			t.Fatalf("editor overlaps dock: editor=%v dock=%v", lay.EditorRect, dock)
		}
		if !lay.ChatRect.Empty() && lay.ChatRect.Y+lay.ChatRect.H > dock.Y {
			t.Fatalf("chat overlaps dock: chat=%v dock=%v", lay.ChatRect, dock)
		}
		if !lay.InputRect.Empty() && lay.InputRect.Y+lay.InputRect.H > dock.Y {
			t.Fatalf("input overlaps dock: input=%v dock=%v", lay.InputRect, dock)
		}
		if !lay.DeckRect.Empty() && lay.DeckRect.Y+lay.DeckRect.H > dock.Y {
			t.Fatalf("session deck overlaps dock: deck=%v dock=%v", lay.DeckRect, dock)
		}
		if dock.Y+dock.H != app.height-app.chromeBottomRows() {
			t.Fatalf("dock does not end at bottom chrome: dock=%v h=%d", dock, app.height)
		}
		app.termDock.Unmount("layout")
	}
}

func TestTerminalDockFocusSendsKeysToPTYNotQuit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("in-app PTYs are unix-only")
	}
	app := newFullTestApp(t)
	app.width, app.height = 120, 40
	mgr := app.termMgr()
	info, err := mgr.Create(workspace.TerminalCreateOpts{Command: "/bin/cat", CWD: t.TempDir(), Cols: 20, Rows: 8})
	if err != nil {
		t.Skipf("pty create: %v", err)
	}
	t.Cleanup(func() { mgr.Close(info.ID) })
	app.dockTerminal(info.ID)
	app.paintShellFrame()
	if !app.termDockOwnsKeys() {
		t.Fatal("docking should focus the terminal dock")
	}
	if !app.terminalOwnsKeys() {
		t.Fatal("docked terminal should own keys")
	}
}

func TestTerminalDockFocusStaysOnVTAndHasNoActionBar(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("in-app PTYs are unix-only")
	}
	app := newFullTestApp(t)
	app.width, app.height = 120, 40
	mgr := app.termMgr()
	info, err := mgr.Create(workspace.TerminalCreateOpts{Command: "/bin/cat", CWD: t.TempDir(), Cols: 20, Rows: 8})
	if err != nil {
		t.Skipf("pty create: %v", err)
	}
	t.Cleanup(func() { mgr.Close(info.ID) })
	app.dockTerminal(info.ID)
	app.paintShellFrame()
	vt := app.termDock.Active()
	if vt == nil {
		t.Fatal("missing active terminal")
	}
	if got := app.rootHost.Focused(); got != vt.ID() {
		t.Fatalf("focus=%q want VT %q", got, vt.ID())
	}
	// The legacy in-body action bar is gone: panel controls live in the dock
	// header row (VS Code style), so no actions child may be registered.
	actionsID := ui.NewID(vt.ID(), "actions")
	if app.rootHost.Focus(actionsID) {
		t.Fatalf("legacy action bar %q must not exist in the dock", actionsID)
	}
	app.syncRootFocus()
	if got := app.rootHost.Focused(); got != vt.ID() {
		t.Fatalf("after sync focus=%q want VT %q", got, vt.ID())
	}
}

func TestTermDockHeaderIconsFloatAndCloseViaMouse(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("in-app PTYs are unix-only")
	}
	app := newFullTestApp(t)
	app.width, app.height = 160, 40
	mgr := app.termMgr()
	info, err := mgr.Create(workspace.TerminalCreateOpts{Command: "/bin/cat", CWD: t.TempDir(), Cols: 20, Rows: 8})
	if err != nil {
		t.Skipf("pty create: %v", err)
	}
	t.Cleanup(func() { mgr.Close(info.ID) })
	app.dockTerminal(info.ID)
	app.paintShellFrame()

	lay := app.computeContentLayout()
	iconRow := lay.TermDockRect.Y + 1
	floatX := -1
	for column := lay.TermDockRect.X; column < lay.TermDockRect.X+lay.TermDockRect.W; column++ {
		if app.mainSurf.Buffer().Get(column, iconRow).Char == '⇱' {
			floatX = column
			break
		}
	}
	if floatX < 0 {
		t.Fatal("terminal float control was not painted")
	}
	model, _ := app.dispatchMouseClick(tea.MouseClickMsg{X: floatX, Y: iconRow, Button: tea.MouseLeft})
	app = model.(*App)
	got, _ := mgr.Get(info.ID)
	if got.Placement != workspace.TerminalFloating {
		t.Fatalf("placement=%v want floating after clicking the ⇱ header icon", got.Placement)
	}
	if app.termDock.Contains(info.ID) {
		t.Fatal("dock still contains the floated terminal")
	}
	if !app.overlayHost.IsOpen(ui.TerminalOverlayID(info.ID)) {
		t.Fatal("floating overlay window missing after header float")
	}

	// Dock it back through the floating title action, then close via ✕.
	app.dockTerminal(info.ID)
	app.paintShellFrame()
	lay = app.computeContentLayout()
	iconRow = lay.TermDockRect.Y + 1
	closeX := -1
	for column := lay.TermDockRect.X; column < lay.TermDockRect.X+lay.TermDockRect.W; column++ {
		if app.mainSurf.Buffer().Get(column, iconRow).Char == '✕' {
			closeX = column
			break
		}
	}
	if closeX < 0 {
		t.Fatal("terminal close control was not painted")
	}
	model, _ = app.dispatchMouseClick(tea.MouseClickMsg{X: closeX, Y: iconRow, Button: tea.MouseLeft})
	app = model.(*App)
	if _, ok := mgr.Get(info.ID); ok {
		t.Fatal("terminal still alive after clicking the ✕ header icon")
	}
	_ = app
}

func TestTermDockLayoutSitsRightOfSidebar(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("in-app PTYs are unix-only")
	}
	app := newFullTestApp(t)
	app.width, app.height = 120, 40
	mgr := app.termMgr()
	info, err := mgr.Create(workspace.TerminalCreateOpts{Command: "/bin/cat", CWD: t.TempDir(), Cols: 20, Rows: 8})
	if err != nil {
		t.Skipf("pty create: %v", err)
	}
	t.Cleanup(func() { mgr.Close(info.ID) })
	app.dockTerminal(info.ID)
	lay := app.computeContentLayout()
	if lay.TermDockRect.Empty() {
		t.Fatal("dock rect empty after docking")
	}
	if lay.TermDockRect.X != lay.SidebarW {
		t.Fatalf("dock X=%d want SidebarW=%d (panel must not underlap the sidebar)", lay.TermDockRect.X, lay.SidebarW)
	}
	if got, want := lay.TermDockRect.W, app.width-lay.SidebarW; got != want {
		t.Fatalf("dock W=%d want %d", got, want)
	}
}
