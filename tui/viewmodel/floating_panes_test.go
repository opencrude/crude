package viewmodel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/ui"
	"crude/tui/window"
	"crude/workspace"
)

// paneWindowText paints a pane window exactly as the WM does and returns
// its visible text.
func paneWindowText(t *testing.T, app *App, group window.GroupID) string {
	t.Helper()
	pw := app.mainPanes.windows[group]
	if pw == nil {
		t.Fatalf("group %d has no window", group)
	}
	w, h := pw.fw.Size()
	buf := surface.NewCellBuffer(w, h)
	host := pw.frame.Host()
	host.Resize(w, h)
	host.Layout()
	host.Paint(buf)
	return bufferPlain(buf)
}

func paneWindowContentPoint(t *testing.T, app *App, group window.GroupID) (int, int) {
	t.Helper()
	g := app.mainPanes.groups[group]
	if g == nil || g.editor.Empty() {
		t.Fatalf("window group %d mounts no editor", group)
	}
	return g.editor.X + g.editor.W/2, g.editor.Y + 1
}

// A file tab opened in a window keeps its identity and editor: it paints in
// the window, keys reach its editor, focus follows the pointer between the
// window and the main area, and Dock returns the tab.
func TestPaneWindowEditorJourney(t *testing.T) {
	app, ws := newRealShellApp(t)
	defer ws.Close()
	paneJourneySize(app, 160, 44)
	paths := openEditorTabs(t, app, "left_marker.go", "right_marker.go")
	right := app.Win.ActiveTab().ID
	docked := app.Win.ActiveGroup()

	app.openTabInWindow(right)
	_ = app.View()
	group := app.Win.GroupOf(right)
	if !app.Win.IsFloating(group) || !app.overlayHost.IsOpen(paneWindowID(group)) || app.Win.ActiveGroup() != group {
		t.Fatalf("tab did not move into a focused window: group=%d floating=%v", group, app.Win.IsFloating(group))
	}
	if text := paneWindowText(t, app, group); !strings.Contains(text, "package right_marker.go") || !strings.Contains(text, "right_marker.go") {
		t.Fatalf("window does not paint its tab:\n%s", text)
	}
	paneJourneyRequireText(t, app, docked, "package left_marker.go")
	if app.focusTarget() != FocusEditor || app.EditorState.ActiveView != paths[1] {
		t.Fatalf("window editor does not own keys: focus=%v view=%s", app.focusTarget(), app.EditorState.ActiveView)
	}

	paneJourneyKey(app, 'X', 0)
	rightEditor := app.editorForWindowTab(app.Win.Tabs()[app.Win.IndexOf(right)].Title)
	if !strings.Contains(rightEditor.Value(), "X") {
		t.Fatalf("typing did not reach the window's editor: %q", rightEditor.Value())
	}

	// Clicking the main area leaves the window; keys follow.
	dockedBody := app.mainPanes.groups[docked].editor
	_, _ = app.Update(tea.MouseClickMsg{X: dockedBody.X + 4, Y: dockedBody.Y + 1, Button: tea.MouseLeft})
	_, _ = app.Update(tea.MouseReleaseMsg{X: dockedBody.X + 4, Y: dockedBody.Y + 1, Button: tea.MouseLeft})
	_ = app.View()
	if app.Win.ActiveGroup() != docked || app.EditorState.ActiveView != paths[0] || app.focusTarget() != FocusEditor {
		t.Fatalf("main-area click did not take focus back: group=%d view=%s", app.Win.ActiveGroup(), app.EditorState.ActiveView)
	}
	paneJourneyKey(app, 'Y', 0)
	if strings.Contains(rightEditor.Value(), "Y") {
		t.Fatal("keys still reached the window after leaving it")
	}

	// Clicking into the window takes focus again.
	x, y := paneWindowContentPoint(t, app, group)
	_, _ = app.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	_, _ = app.Update(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft})
	_ = app.View()
	if app.Win.ActiveGroup() != group || app.EditorState.ActiveView != paths[1] {
		t.Fatalf("window click did not focus its group: group=%d view=%s", app.Win.ActiveGroup(), app.EditorState.ActiveView)
	}

	// The title-bar Dock button returns the tab to the main area.
	fw := app.mainPanes.windows[group].fw
	fx, fy := fw.Position()
	fwW, _ := fw.Size()
	_, _ = app.Update(tea.MouseClickMsg{X: fx + fwW - 5, Y: fy, Button: tea.MouseLeft})
	_, _ = app.Update(tea.MouseReleaseMsg{X: fx + fwW - 5, Y: fy, Button: tea.MouseLeft})
	_ = app.View()
	if app.Win.HasGroup(group) || app.overlayHost.IsOpen(paneWindowID(group)) || app.Win.IsFloating(app.Win.GroupOf(right)) {
		t.Fatal("Dock left the window open")
	}
	if app.Win.ActiveTab() == nil || app.Win.ActiveTab().ID != right || !strings.Contains(rightEditor.Value(), "X") {
		t.Fatal("docking lost the tab's identity or edits")
	}
}

// Closing the window (✕) never closes its tabs; they return to the main area.
func TestPaneWindowCloseDocksTabs(t *testing.T) {
	app, ws := newRealShellApp(t)
	defer ws.Close()
	paneJourneySize(app, 160, 44)
	openEditorTabs(t, app, "one.go", "two.go")
	two := app.Win.ActiveTab().ID
	app.openTabInWindow(two)
	_ = app.View()
	group := app.Win.GroupOf(two)
	app.closePopupWindowByID(string(paneWindowID(group)))
	_ = app.View()
	if app.Win.HasGroup(group) || app.Win.IndexOf(two) < 0 || app.Win.IsFloating(app.Win.GroupOf(two)) {
		t.Fatal("closing the window closed or stranded its tab")
	}
	// Closing the window's last tab closes the window.
	app.openTabInWindow(two)
	_ = app.View()
	group = app.Win.GroupOf(two)
	_ = app.handleAgentTabClose(app.Win.IndexOf(two))
	_ = app.View()
	if app.overlayHost.IsOpen(paneWindowID(group)) || len(app.mainPanes.windows) != 0 {
		t.Fatal("an empty window stayed open")
	}
}

// A right-click on a docked tab offers Open in Window; inside a window it
// offers Move to Main Area instead of split commands.
func TestPaneWindowTabMenu(t *testing.T) {
	app, ws := newRealShellApp(t)
	defer ws.Close()
	paneJourneySize(app, 160, 44)
	openEditorTabs(t, app, "menu.go")
	tab := app.Win.ActiveTab().ID
	labels := func(section ui.ContextMenuSection) string {
		var names []string
		for _, item := range section.Items {
			names = append(names, item.Label)
		}
		return strings.Join(names, ",")
	}
	docked := app.paneMenuSection(tab)
	if !strings.Contains(labels(docked), "Open in Window") {
		t.Fatalf("docked tab menu: %s", labels(docked))
	}
	docked.Items[0].Action()
	_ = app.View()
	if !app.Win.IsFloating(app.Win.GroupOf(tab)) {
		t.Fatal("Open in Window did not float the tab")
	}
	floating := app.paneMenuSection(tab)
	if got := labels(floating); got != "Move to Main Area" {
		t.Fatalf("window tab menu: %s", got)
	}
	floating.Items[0].Action()
	_ = app.View()
	if app.Win.IsFloating(app.Win.GroupOf(tab)) || len(app.mainPanes.windows) != 0 {
		t.Fatal("Move to Main Area did not dock the tab")
	}
}

// An agent opened in a window keeps its composer: typing while the window is
// focused edits that session's draft, and the window paints it.
func TestPaneWindowAgentComposer(t *testing.T) {
	app := newFullTestApp(t)
	wsOf(app).Logger = stubLogger{}
	app.Tree.Visible = false
	_, _ = app.Update(tea.WindowSizeMsg{Width: 160, Height: 44})
	id := "windowed"
	wsOf(app).SessionsByAgent = map[string]*workspace.Session{
		id:      workspace.NewSession("windowed", t.TempDir(), nil, nil),
		"other": workspace.NewSession("other", t.TempDir(), nil, nil),
	}
	app.AddOrchestrator(id)
	app.AddOrchestrator("other")
	app.openAgentInWindow(id)
	_ = app.View()
	tab := app.Win.TabByTitle(app.agentTabTitle(id))
	if tab == nil || !app.Win.IsFloating(tab.Group) || app.Win.ActiveGroup() != tab.Group {
		t.Fatal("agent did not open in a focused window")
	}
	if app.focusTarget() != FocusInput {
		t.Fatalf("focus = %v, want the window's composer", app.focusTarget())
	}
	for _, r := range "WINDRAFT" {
		paneJourneyKey(app, r, 0)
	}
	if !strings.Contains(app.Input.Value(), "WINDRAFT") {
		t.Fatalf("composer draft = %q", app.Input.Value())
	}
	if text := paneWindowText(t, app, tab.Group); !strings.Contains(text, "WINDRAFT") {
		t.Fatalf("window does not paint its composer:\n%s", text)
	}
	if g := app.mainPanes.groups[app.Win.DockedGroup()]; g == nil || strings.Contains(paneJourneyText(app, g.body), "WINDRAFT") {
		t.Fatal("the window's draft leaked into the docked agent")
	}
}

// With a core-backed (live) document the open lands asynchronously; the
// window intent travels with it and the landed tab floats. Opening another
// file while the window is focused lands in the window's group.
func TestPaneWindowLiveDocumentOpensInWindow(t *testing.T) {
	runtime := remoteJourneyRuntime(t, "http://127.0.0.1:1")
	root := t.TempDir()
	aPath, bPath := filepath.Join(root, "a.txt"), filepath.Join(root, "b.txt")
	for _, path := range []string{aPath, bPath} {
		if err := os.WriteFile(path, []byte("live text\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	shell := newRemoteJourneyShell(t, runtime, root)
	app := shell.app
	runAppCommand(t, app, app.openFileInWindow(aPath))
	tab := app.Win.TabByTitle(app.editorTabTitle(aPath))
	if tab == nil || !app.Win.IsFloating(tab.Group) || app.Win.ActiveGroup() != tab.Group {
		t.Fatalf("live document did not open in a focused window: %+v", tab)
	}
	runAppCommand(t, app, app.openPaneFiles([]string{bPath}, app.Win.ActiveGroup()))
	other := app.Win.TabByTitle(app.editorTabTitle(bPath))
	if other == nil || other.Group != tab.Group {
		t.Fatal("a file opened from a focused window did not land in it")
	}
}

// Dragging a tab out of a window's strip onto the main area moves it there
// and the window keeps its other tab; a drop back onto the window itself
// changes nothing.
func TestPaneWindowTabDragOutDocks(t *testing.T) {
	app, ws := newRealShellApp(t)
	defer ws.Close()
	paneJourneySize(app, 160, 44)
	openEditorTabs(t, app, "stay.go", "keep.go", "drag.go")
	tab := app.Win.ActiveTab().ID
	keep := app.Win.Tabs()[1].ID
	docked := app.Win.DockedGroup()
	app.openTabInWindow(tab)
	_ = app.View()
	group := app.Win.GroupOf(tab)
	// A window shows a strip once it holds two tabs to choose between.
	if !app.Win.MoveTabToGroup(keep, group, tab) {
		t.Fatal("could not add a second tab to the window")
	}
	_ = app.View()
	g := app.mainPanes.groups[group]
	strip, ok := app.screenRectOf(g.tabs.TabsControl().ID())
	if !ok {
		t.Fatal("window strip is not mounted")
	}
	pressX := -1
	for local := 0; local < strip.W; local++ {
		if i, closeHit := g.tabs.TabsControl().HitTest(local); i == 1 && !closeHit {
			pressX = strip.X + local
			break
		}
	}
	if pressX < 0 {
		t.Fatal("window tab has no hit target")
	}
	// A drag released inside the window is not a drop target.
	_, _ = app.Update(tea.MouseClickMsg{X: pressX, Y: strip.Y, Button: tea.MouseLeft})
	_, _ = app.Update(tea.MouseMotionMsg{X: pressX + 6, Y: strip.Y + 4, Button: tea.MouseLeft})
	_, _ = app.Update(tea.MouseReleaseMsg{X: pressX + 6, Y: strip.Y + 4, Button: tea.MouseLeft})
	_ = app.View()
	if app.Win.GroupOf(tab) != group {
		t.Fatal("a drop inside the window moved the tab")
	}
	// Dragged onto the docked pane's tab strip, it joins that group.
	target := app.mainPanes.groups[docked]
	tx, ty := target.body.X+2, target.body.Y-1
	fw := app.mainPanes.windows[group].fw
	if fw.ContainsPoint(tx, ty) {
		x, _ := fw.Position()
		fw.SetPosition(x, 30)
		_ = app.View()
		strip, _ = app.screenRectOf(g.tabs.TabsControl().ID())
		pressX = strip.X + (pressX - strip.X)
	}
	_, _ = app.Update(tea.MouseClickMsg{X: pressX, Y: strip.Y, Button: tea.MouseLeft})
	_, _ = app.Update(tea.MouseMotionMsg{X: tx, Y: ty, Button: tea.MouseLeft})
	_, _ = app.Update(tea.MouseReleaseMsg{X: tx, Y: ty, Button: tea.MouseLeft})
	_ = app.View()
	if app.Win.GroupOf(tab) != docked || app.Win.GroupOf(keep) != group || len(app.mainPanes.windows) != 1 {
		t.Fatalf("drag out did not dock the tab: group=%d floating=%v", app.Win.GroupOf(tab), app.Win.IsFloating(app.Win.GroupOf(tab)))
	}
}

// Several windows stay open at once, each painting its own tab. A file opened
// while a window is focused joins that window; its tab menu then offers its
// own window, so any number of windows can be built up.
func TestSeveralPaneWindowsAtOnce(t *testing.T) {
	app, ws := newRealShellApp(t)
	defer ws.Close()
	paneJourneySize(app, 160, 44)
	openEditorTabs(t, app, "first.go", "second.go", "third.go")
	tabs := app.Win.Tabs()
	app.openTabInWindow(tabs[0].ID)
	_ = app.View()
	app.openTabInWindow(tabs[1].ID)
	_ = app.View()
	first, second := app.Win.GroupOf(tabs[0].ID), app.Win.GroupOf(tabs[1].ID)
	if first == second || !app.overlayHost.IsOpen(paneWindowID(first)) || !app.overlayHost.IsOpen(paneWindowID(second)) {
		t.Fatalf("two tabs did not get two open windows: %d %d", first, second)
	}
	if !strings.Contains(paneWindowText(t, app, first), "package first.go") || !strings.Contains(paneWindowText(t, app, second), "package second.go") {
		t.Fatal("each window must paint its own tab")
	}

	// Move the third tab into the second window, then give it its own.
	third := tabs[2].ID
	if !app.Win.MoveTabToGroup(third, second, 0) {
		t.Fatal("could not move the third tab into the second window")
	}
	_ = app.View()
	section := app.paneMenuSection(third)
	var detach func()
	for _, item := range section.Items {
		if item.Label == "Open in New Window" {
			detach = item.Action
		}
	}
	if detach == nil {
		t.Fatalf("a tab in a two-tab window offers no new window: %+v", section.Items)
	}
	detach()
	_ = app.View()
	if group := app.Win.GroupOf(third); group == second || group == first || !app.overlayHost.IsOpen(paneWindowID(group)) {
		t.Fatalf("third tab did not get its own window: group %d", group)
	}
	if len(app.mainPanes.windows) != 3 {
		t.Fatalf("windows open = %d, want 3", len(app.mainPanes.windows))
	}
	if section := app.paneMenuSection(tabs[0].ID); len(section.Items) != 1 {
		t.Fatalf("a single-tab window offers a redundant new window: %+v", section.Items)
	}
}

// Moving the last main-area tab into a window leaves the launcher in the main
// area, the same screen as when nothing is open: no tab strip and no empty
// pane. Clicking it gives it keys; docking the window back replaces it with
// the tab, edits intact.
func TestLastTabInWindowLeavesTheLauncher(t *testing.T) {
	app, ws := newRealShellApp(t)
	defer ws.Close()
	paneJourneySize(app, 160, 44)
	openEditorTabs(t, app, "only.go")
	tab := app.Win.ActiveTab().ID
	app.openTabInWindow(tab)
	_ = app.View()
	group := app.Win.GroupOf(tab)
	if !app.Win.IsFloating(group) {
		t.Fatal("tab did not move into a window")
	}
	paneJourneyKey(app, 'Z', 0)

	screen := bufferPlain(capturePaintedShell(t, app))
	if !strings.Contains(screen, "New Session") {
		t.Fatalf("main area does not show the launcher:\n%s", screen)
	}
	if strings.Contains(screen, "Open File") {
		t.Fatalf("main area shows the empty pane instead of the launcher:\n%s", screen)
	}
	if app.tabBarVisible() {
		t.Fatal("an empty main area kept its tab strip")
	}

	// A main-area point the centered window does not cover.
	wx, wy := app.mainPanes.windows[group].fw.Position()
	ww, wh := app.mainPanes.windows[group].fw.Size()
	px, py := app.width-3, app.height-5
	if px >= wx && px < wx+ww && py >= wy && py < wy+wh {
		t.Fatalf("probe point %d,%d is inside the window", px, py)
	}
	clickAt(app, px, py)
	if app.focusTarget() != FocusLauncher {
		t.Fatalf("clicking the launcher left focus on %v", app.focusTarget())
	}

	// Surfacing an agent drops the launcher screen; while the main area is
	// still empty the next frame brings it back.
	app.launcherScreen = nil
	_ = app.View()
	if screen := bufferPlain(capturePaintedShell(t, app)); !strings.Contains(screen, "New Session") {
		t.Fatalf("launcher not restored for the empty main area:\n%s", screen)
	}

	app.dockPaneWindow(group)
	_ = app.View()
	if app.mainAreaEmpty() || !app.tabBarVisible() {
		t.Fatal("docking did not return the tab to the main area")
	}
	if ed := app.editorForWindowTab(app.Win.Tabs()[app.Win.IndexOf(tab)].Title); !strings.Contains(ed.Value(), "Z") {
		t.Fatalf("docking lost the window's edit: %q", ed.Value())
	}
	if screen := bufferPlain(capturePaintedShell(t, app)); strings.Contains(screen, "New Session") {
		t.Fatalf("launcher still painted after docking:\n%s", screen)
	}
}

// A single-tab window has no strip, so its title bar carries + to open
// another tab in the window (then the strip appears). The window paints on
// the shell background, like the same elements in the main area.
func TestPaneWindowPlusOpensAnotherTabInTheWindow(t *testing.T) {
	app, ws := newRealShellApp(t)
	defer ws.Close()
	paneJourneySize(app, 160, 44)
	// The window holds the only tab: its strip must not depend on the
	// (empty) main area's.
	openEditorTabs(t, app, "solo.go")
	tab := app.Win.ActiveTab().ID
	app.openTabInWindow(tab)
	_ = app.View()
	group := app.Win.GroupOf(tab)
	pw := app.mainPanes.windows[group]
	if app.mainPanes.groups[group].tabs.TabsControl() != nil {
		if _, mounted := app.screenRectOf(app.mainPanes.groups[group].tabs.TabsControl().ID()); mounted {
			t.Fatal("a single-tab window mounted a tab strip")
		}
	}

	// The fill under the frame is the shell background, never popup chrome.
	w, h := pw.fw.Size()
	surf := surface.NewSurface("pane", w, h, cell.TransparentBlank())
	pw.frame.Render(surf, nil)
	shellBg := app.rootHost.Tokens().Color(ui.ColorBg)
	if got := surf.Buffer().Get(1, 0).BG; got != shellBg {
		t.Fatalf("window title row bg = %v, want the shell background %v", got, shellBg)
	}

	text := paneWindowText(t, app, group)
	if first := strings.SplitN(text, "\n", 2)[0]; !strings.Contains(first, "+") || !strings.Contains(first, "⇲") {
		t.Fatalf("title bar lacks + and ⇲: %q", first)
	}
	x, y := pw.fw.Position()
	plusX := x + w - 3 - 2*ui.TitleActionWidth + 1
	_, _ = app.Update(tea.MouseClickMsg{X: plusX, Y: y, Button: tea.MouseLeft})
	_, _ = app.Update(tea.MouseReleaseMsg{X: plusX, Y: y, Button: tea.MouseLeft})
	_ = app.View()
	if !app.overlayHost.IsOpen(ui.OverlayContextMenu) || app.LastContextMenu == nil {
		t.Fatal("+ did not open the new-tab menu")
	}
	var labels []string
	for _, section := range app.LastContextMenu.Sections {
		for _, item := range section.Items {
			labels = append(labels, item.Label)
		}
	}
	if strings.Join(labels, ",") != "New Session,Open File…" {
		t.Fatalf("new-tab menu = %v", labels)
	}
	if !app.Win.IsFloating(group) || app.Win.GroupOf(tab) != group {
		t.Fatal("+ moved the window's tab")
	}

	// Opening a file targeted at the window lands in it and brings the strip.
	path := filepath.Join(app.ws.RootPath(), "joined.go")
	if err := os.WriteFile(path, []byte("package joined\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runAppCommand(t, app, app.openPaneFiles([]string{path}, group))
	joined := app.Win.TabByTitle(app.editorTabTitle(path))
	if joined == nil || joined.Group != group {
		t.Fatal("a file opened for the window did not land in it")
	}
	_ = app.View()
	if _, mounted := app.screenRectOf(app.mainPanes.groups[group].tabs.TabsControl().ID()); !mounted {
		t.Fatal("a two-tab window shows no strip")
	}
}
