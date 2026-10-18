package viewmodel

import (
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/shellport"

	"crude/tui/surface"
	"crude/tui/ui"
	"crude/tui/window"
	"crude/tui/wm"
)

// A pane window is a floating window.Window group: any tab (file, agent,
// panel, review) can live in one. The window is only a mount point; the tab
// keeps its identity, editor, session and selection, and the same viewmodel
// policy (focus, keys, composer ownership) drives it as a docked group.

const paneWindowPrefix = "pane-window:"

func paneWindowID(group window.GroupID) ui.OverlayID {
	return ui.OverlayID(paneWindowPrefix + strconv.FormatUint(uint64(group), 10))
}

func paneGroupFromOverlay(id ui.OverlayID) (window.GroupID, bool) {
	value, ok := strings.CutPrefix(string(id), paneWindowPrefix)
	if !ok {
		return 0, false
	}
	n, err := strconv.ParseUint(value, 10, 64)
	return window.GroupID(n), err == nil && n != 0
}

type paneWindow struct {
	fw    *wm.FloatingWindow
	frame *ui.WindowFrameWidget
	body  *paneWindowBody
}

// paneWindowBody mounts exactly one floating group inside its window frame.
type paneWindowBody struct {
	ui.Base
	layout *mainPaneLayout
	group  window.GroupID
	fw     *wm.FloatingWindow
}

func (b *paneWindowBody) Children() []ui.Component {
	if g := b.layout.groups[b.group]; g != nil {
		return []ui.Component{g}
	}
	return nil
}

func (b *paneWindowBody) Measure(_ ui.LayoutContext, c ui.Constraints) ui.Size {
	return c.ClampSize(ui.Size{W: c.MaxW, H: c.MaxH})
}

func (b *paneWindowBody) Layout(ctx ui.LayoutContext, r ui.Rect) {
	b.SetBounds(r)
	if !b.layout.app.Win.IsFloating(b.group) {
		return
	}
	g := b.layout.group(b.group)
	x, y := 0, 0
	if b.fw != nil {
		x, y = b.fw.Position()
	}
	local := ui.Rect{W: r.W, H: r.H}
	b.layout.configureGroup(g, local, x+r.X, y+r.Y)
	g.Layout(ctx, local)
}

func (*paneWindowBody) Paint(*surface.CellBuffer, int, int, ui.PaintContext) {}
func (*paneWindowBody) Handle(ui.EventContext, ui.Event) ui.EventResult      { return ui.EventResult{} }
func (*paneWindowBody) HandleAction(_ ui.EventContext, a ui.Action) ui.EventResult {
	return ui.EventResult{Handled: true, Action: &a}
}
func (*paneWindowBody) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleGroup, Label: "Window tab group"}
}

func (m *App) paneWindowTitle(group window.GroupID) string {
	tab := m.Win.GroupActiveTab(group)
	if tab == nil {
		return "Window"
	}
	title := tab.DisplayTitle
	if title == "" {
		title = tab.Title
	}
	if n := len(m.Win.GroupTabs(group)); n > 1 {
		title += " +" + strconv.Itoa(n-1)
	}
	return title
}

// openTabInWindow moves a tab into its own window and focuses it.
func (m *App) openTabInWindow(id window.TabID) {
	if m.Win == nil || m.overlayHost == nil || m.Win.IndexOf(id) < 0 {
		return
	}
	group := m.Win.DetachTab(id)
	if group == 0 {
		return
	}
	m.unfocus()
	m.handleTabSwitch()
	m.syncPaneWindows()
	if m.windowManager != nil {
		m.windowManager.FocusWindow(string(paneWindowID(group)))
	}
	m.paneFocusSeen = group
	m.forceFrame()
}

// openAgentInWindow surfaces an agent (restoring a minimized one) in its
// own window. An agent already alone in a window is raised.
func (m *App) openAgentInWindow(agentID string) {
	m.restoreAgent(agentID)
	if tab := m.Win.TabByTitle(m.agentTabTitle(agentID)); tab != nil {
		m.openTabInWindow(tab.ID)
	}
}

// openFileInWindow opens a file's editor tab in its own window. A live
// (core-backed) document lands asynchronously and floats when it lands.
func (m *App) openFileInWindow(path string) tea.Cmd {
	if _, connected := m.ws.(shellport.LiveDocuments); connected {
		if tab := m.EditorState.Tabs[path]; tab == nil || !tab.Editor.IsOpen() {
			cmd := m.openPaneFiles([]string{path}, m.Win.DockedGroup())
			if cmd == nil {
				return nil
			}
			return func() tea.Msg {
				msg, ok := cmd().(paneFilesOpenedMsg)
				if !ok {
					return nil
				}
				msg.window = true
				return msg
			}
		}
	}
	cmd, err := m.openEditorTab(path)
	if err != nil {
		m.Feedback.NotifyError("open " + path + ": " + err.Error())
		return cmd
	}
	if tab := m.Win.ActiveTab(); tab != nil && m.isEditorTabTitle(tab.Title) && m.editorPathFromTabTitle(tab.Title) == path {
		m.openTabInWindow(tab.ID)
	}
	return cmd
}

// dockPaneWindow returns a window's tabs to the main area. A window that
// holds only a workspace surface returns it to the bottom dock, its home.
func (m *App) dockPaneWindow(group window.GroupID) {
	if m.Win == nil || !m.Win.IsFloating(group) {
		return
	}
	if tabs := m.Win.GroupTabs(group); len(tabs) == 1 && tabs[0].Resource.Kind == window.ResourcePanel && m.placementOf(tabs[0].Resource.ID) == placeTab {
		m.dockPanel(tabs[0].Resource.ID)
		m.syncPaneWindows()
		return
	}
	if !m.Win.DockGroup(group) {
		return
	}
	m.unfocus()
	m.handleTabSwitch()
	m.syncPaneWindows()
	m.forceFrame()
}

func (m *App) newPaneWindow(group window.GroupID) *paneWindow {
	p := m.ensureMainPanes()
	oid := paneWindowID(group)
	count := len(p.windows)
	w := max(48, min(m.width*70/100, 150))
	h := max(14, min(m.height*70/100, 48))
	w, h = min(w, max(24, m.width-4)), min(h, max(10, m.height-4))
	body := &paneWindowBody{Base: ui.NewBase(ui.NewID(ui.RootID, string(oid)+"-body")), layout: p, group: group}
	frame := ui.NewWindowFrameWidget(m.Theme, ui.NewID(ui.RootID, string(oid)+"-frame"), ui.FrameWindow, m.paneWindowTitle(group), body, nil, true, w, h)
	if frame.Frame() != nil {
		// A pane window is main content: it paints on the shell background,
		// so every element looks as it does in the main area.
		frame.Frame().SetFill(ui.ColorBg)
		frame.Frame().SetTitleActions(paneWindowTitleActions)
	}
	fw := wm.NewFloatingWindow(string(oid), m.paneWindowTitle(group), frame, w, h,
		wm.WithDecoration(true), wm.WithMovable(true), wm.WithResizable(true),
		wm.WithClosable(true), wm.WithFocusable(true), wm.WithMinSize(min(30, w), min(8, h)),
		wm.WithClickOutside(wm.ClickOutsideKeepRoute))
	fw.SetTitleActionWidth(ui.TitleActionWidth * len(paneWindowTitleActions))
	wm.CenterOnScreen(fw, m.width, m.height)
	if count > 0 {
		x, y := fw.Position()
		step := 2 * (count % 6)
		fw.SetPosition(min(x+step, max(0, m.width-w)), min(y+step/2, max(0, m.height-h)))
	}
	body.fw = fw
	pw := &paneWindow{fw: fw, frame: frame, body: body}
	// Closing the window never closes its tabs: they return to the main area.
	m.overlayHost.Open(oid, fw, ui.DefaultOverlayPolicy(), func() {
		if p.windows[group] == pw {
			delete(p.windows, group)
		}
		if m.Win != nil && m.Win.IsFloating(group) {
			m.Win.DockGroup(group)
			m.handleTabSwitch()
			m.forceFrame()
		}
	})
	return pw
}

// paneWindowTitleActions are a pane window's title-bar buttons, rightmost
// first: Dock beside close, then + to open another tab in the window, which
// has no tab strip while it holds a single tab.
var paneWindowTitleActions = []ui.TitleAction{
	{Glyph: "⇲", Hint: "Dock", ID: paneWindowDock},
	{Glyph: "+", Hint: "New tab in this window", ID: paneWindowNewTab},
}

const (
	paneWindowDock   = "pane-window.dock"
	paneWindowNewTab = "pane-window.new-tab"
)

// paneWindowNewTabMenu offers what a new tab in the window holds, the same
// choices as the main strip's + and an empty pane.
func (m *App) paneWindowNewTabMenu(group window.GroupID) *ui.ContextMenuState {
	action := func(id string) func() {
		return func() {
			cmd, _ := m.handleMainPaneAction(&ui.Action{ID: id, Payload: group})
			m.startSourceControlMutation(cmd)
		}
	}
	return &ui.ContextMenuState{Title: "New tab", Sections: []ui.ContextMenuSection{{
		SectionLabel: "New tab in this window",
		Items: []ui.ContextMenuItem{
			{Label: "New Session", Detail: "agent", Action: action("pane.new-session")},
			{Label: "Open File…", Detail: "editor", Action: action("pane.open-file")},
		},
	}}}
}

// syncPaneWindows makes the open windows match Window's floating groups and
// lays each one out with this frame's state, before focused-view geometry is
// derived. Mutations of the group (tab close, dock, detach) take effect here.
func (m *App) syncPaneWindows() {
	if m.Win == nil || m.overlayHost == nil {
		return
	}
	p := m.ensureMainPanes()
	if p.windows == nil {
		p.windows = make(map[window.GroupID]*paneWindow)
	}
	floating := m.Win.FloatingGroupIDs()
	// With every tab in windows the main area is empty and shows the
	// launcher, whichever transition emptied it (an eject, a session opened
	// into a window): surfacing an agent anywhere drops the launcher screen.
	if len(floating) > 0 && m.launcherScreen == nil && m.mainAreaEmpty() {
		m.openLauncher()
	}
	for group := range p.windows {
		if !m.Win.IsFloating(group) || !m.overlayHost.IsOpen(paneWindowID(group)) {
			delete(p.windows, group)
			if m.overlayHost.Get(paneWindowID(group)) != nil {
				m.overlayHost.Close(paneWindowID(group))
			}
		}
	}
	for _, group := range floating {
		pw := p.windows[group]
		if pw == nil {
			pw = m.newPaneWindow(group)
			p.windows[group] = pw
		}
		title := m.paneWindowTitle(group)
		pw.fw.Title = title
		if pw.frame.Frame() != nil {
			pw.frame.Frame().SetTitle(title)
			pw.frame.Frame().SetTokens(m.rootHost.Tokens())
		}
		host := pw.frame.Host()
		w, h := pw.fw.Size()
		host.Resize(w, h)
		host.MarkLayout()
		host.Layout()
		pw.fw.MarkContentDirty()
	}
}

// windowScreenAction converts pointer geometry a window host reports in its
// own coordinates to screen coordinates. A tab dragged out of a window's
// strip then drops onto the main area exactly where the pointer is.
func windowScreenAction(fw *wm.FloatingWindow, a *ui.Action) *ui.Action {
	if a == nil || fw == nil {
		return a
	}
	drag, ok := a.Payload.(ui.TabDrag)
	if !ok {
		return a
	}
	x, y := fw.Position()
	drag.X += x
	drag.Y += y
	out := *a
	out.Payload = drag
	return &out
}

// paneWindowHost is the frame host mounting a floating group, if any.
func (m *App) paneWindowHost(group window.GroupID) *ui.Host {
	if m.mainPanes == nil || m.Win == nil || !m.Win.IsFloating(group) {
		return nil
	}
	if pw := m.mainPanes.windows[group]; pw != nil {
		return pw.frame.Host()
	}
	return nil
}

// mainHost is the host mounting the focused group's content: the root host
// for the split layout, or the focused window's host.
func (m *App) mainHost() *ui.Host {
	if m.Win != nil {
		if host := m.paneWindowHost(m.Win.ActiveGroup()); host != nil {
			return host
		}
	}
	return m.rootHost
}

// hostFor returns the host where id is mounted, preferring the root host.
func (m *App) hostFor(id ui.ComponentID) *ui.Host {
	if m.rootHost != nil && m.rootHost.Component(id) != nil {
		return m.rootHost
	}
	if host := m.mainHost(); host != nil && host.Component(id) != nil {
		return host
	}
	return m.rootHost
}

// screenRectOf is a mounted component's screen rect in whichever host
// mounts it; window hosts are offset by their window's position.
func (m *App) screenRectOf(id ui.ComponentID) (ui.Rect, bool) {
	if m.rootHost != nil && m.rootHost.Component(id) != nil {
		return m.rootHost.ScreenRect(id)
	}
	if m.mainPanes == nil {
		return ui.Rect{}, false
	}
	for _, pw := range m.mainPanes.windows {
		host := pw.frame.Host()
		if host.Component(id) == nil {
			continue
		}
		r, ok := host.ScreenRect(id)
		x, y := pw.fw.Position()
		r.X += x
		r.Y += y
		return r, ok
	}
	return ui.Rect{}, false
}

// paneWindowGroupAt is the floating group whose window content is at (x, y).
func (m *App) paneWindowGroupAt(x, y int) *mainPaneGroup {
	if m.mainPanes == nil || m.windowManager == nil {
		return nil
	}
	fw := m.windowManager.WindowAt(x, y)
	if fw == nil {
		return nil
	}
	group, ok := paneGroupFromOverlay(ui.OverlayID(fw.ID))
	if !ok {
		return nil
	}
	if g := m.mainPanes.groups[group]; g != nil && g.screen(ui.Rect{W: g.Bounds().W, H: g.Bounds().H}).Contains(x, y) {
		return g
	}
	return nil
}

// paneWindowContextMenu is the right-click menu inside a pane window: the
// same tab, editor and chat menus a docked group offers.
func (m *App) paneWindowContextMenu(group window.GroupID, mouse tea.Mouse) *ui.ContextMenuState {
	m.activatePaneWindow(group)
	g := m.paneWindowGroupAt(mouse.X, mouse.Y)
	if g == nil || g.id != group {
		return nil
	}
	switch {
	case mouse.Y < g.body.Y:
		return m.buildTabBarContextMenu(mouse)
	case g.editor.Contains(mouse.X, mouse.Y):
		return m.buildEditorContextMenu(mouse)
	case g.input.Contains(mouse.X, mouse.Y):
		return m.buildInputContextMenu(mouse)
	case g.chat.Contains(mouse.X, mouse.Y):
		return m.buildChatContextMenu(mouse)
	}
	return nil
}

// focusedPaneWindow is the group whose window holds WM focus.
func (m *App) focusedPaneWindow() window.GroupID {
	if m.windowManager == nil {
		return 0
	}
	if fw := m.windowManager.Focused(); fw != nil {
		if group, ok := paneGroupFromOverlay(ui.OverlayID(fw.ID)); ok && m.Win != nil && m.Win.IsFloating(group) {
			return group
		}
	}
	return 0
}

// focusedPopup is the focused WM window that owns input as a popup. Pane
// windows are main content, so they never count.
func (m *App) focusedPopup() *wm.FloatingWindow {
	if m.windowManager == nil {
		return nil
	}
	fw := m.windowManager.Focused()
	if fw == nil {
		return nil
	}
	if _, ok := paneGroupFromOverlay(ui.OverlayID(fw.ID)); ok {
		return nil
	}
	return fw
}

// syncPaneWindowFocus keeps Window's focused group and WM window focus in
// agreement. Whichever side changed since the last sync wins: code that
// focuses a window group raises its window; a user who raises or leaves a
// window moves the focused group with it.
func (m *App) syncPaneWindowFocus() {
	if m.Win == nil {
		return
	}
	active := m.Win.ActiveGroup()
	raised := m.focusedPaneWindow()
	switch {
	case active != m.paneFocusSeen && m.Win.IsFloating(active):
		if raised != active && m.windowManager != nil {
			m.windowManager.FocusWindow(string(paneWindowID(active)))
		}
	case raised != 0 && raised != active:
		m.retargetMainGroup(raised)
	case raised == 0 && m.Win.IsFloating(active) && (m.windowManager == nil || m.windowManager.Focused() == nil):
		// Only leaving every window returns work to the main area; a popup
		// opened from the window's editor (find, completion) keeps its group.
		m.retargetMainGroup(m.Win.DockedGroup())
	}
	m.paneFocusSeen = m.Win.ActiveGroup()
}

// retargetMainGroup moves the focused group without touching the focus
// stack: the region the user chose (tree, dock) keeps keys.
func (m *App) retargetMainGroup(group window.GroupID) {
	if group == m.Win.ActiveGroup() || !m.Win.FocusGroup(group) {
		return
	}
	m.handleTabSwitch()
	m.forceFrame()
}

// activatePaneWindow is pointer policy for a press inside a window's
// content: its group takes focus like a click into a docked pane.
func (m *App) activatePaneWindow(group window.GroupID) {
	if group != m.Win.ActiveGroup() {
		m.focusMainGroup(group)
	} else {
		m.unfocus()
	}
	m.paneFocusSeen = group
	m.syncPaneWindows()
}
