package viewmodel

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/shellport"
	"crude/tui/rendering"
	"crude/tui/screens/shellterm"
	"crude/tui/screens/termdock"
	"crude/tui/ui"
	"crude/tui/wm"
	"crude/workspace"
)

const (
	defaultTermCols       = workspace.DefaultTerminalCols
	defaultTermRows       = workspace.DefaultTerminalRows
	defaultTermDockHeight = 12
	minTermDockHeight     = 6
	minMainAboveDock      = 8
)

// termDockTopRow is the highest row the terminal dock may occupy: the top of
// the main chrome (legacy: the sidebar's row 3).
func (m *App) termDockTopRow() int { return m.shellTopRow() }

func (m *App) termMgr() shellport.TerminalManager {
	if m == nil || m.ws == nil {
		return nil
	}
	return m.ws.TermMgr()
}

func (m *App) termDockVisible() bool {
	return m.termDock != nil && m.termDock.IsOpen() && m.termDockHeightClamped() > 0
}

func (m *App) termDockOwnsKeys() bool {
	return m.termDockFocused && m.termDockVisible() && !m.sessionDeckBlocking()
}

func (m *App) terminalDockOwnsKeys() bool {
	return m.termDockOwnsKeys() && m.termDock != nil && m.termDock.TerminalActive()
}

func (m *App) terminalOwnsKeys() bool {
	if m.windowManager != nil {
		if fw := m.windowManager.Focused(); fw != nil {
			return ui.IsTerminalOverlay(ui.OverlayID(fw.ID))
		}
	}
	return m.terminalDockOwnsKeys()
}

func (m *App) termDockHeightClamped() int {
	if m.termDock == nil || !m.termDock.IsOpen() {
		return 0
	}
	availableH := m.height - m.chromeBottomRows() - m.termDockTopRow()
	if availableH < termdock.MinRenderableHeight {
		return 0
	}
	if m.workspacePanelMaximized && !m.sessionDeckBlocking() {
		return max(0, availableH-ui.StripHeight)
	}
	h := m.termDockHeight
	if h <= 0 {
		h = defaultTermDockHeight
	}
	maxH := m.height - m.chromeBottomRows() - minMainAboveDock
	if m.sessionDeckBlocking() {
		maxH = min(maxH, availableH-10)
		if maxH < minTermDockHeight {
			return 0
		}
	}
	if maxH < termdock.MinRenderableHeight {
		maxH = termdock.MinRenderableHeight
	}
	if maxH > availableH {
		maxH = availableH
	}
	minH := minTermDockHeight
	if minH < termdock.MinRenderableHeight {
		minH = termdock.MinRenderableHeight
	}
	if minH > maxH {
		minH = maxH
	}
	if h < minH {
		h = minH
	}
	if h > maxH {
		h = maxH
	}
	return h
}

func (m *App) setTermDockHeight(h int) {
	m.termDockHeight = max(minTermDockHeight, h)
	if m.termDock != nil {
		if m.workspacePanelHeights == nil {
			m.workspacePanelHeights = make(map[string]int)
		}
		m.workspacePanelHeights[m.termDock.ActivePanel()] = m.termDockHeight
		m.toolLayoutVersion++
	}
}

func (m *App) setTermDockFocused(v bool) {
	m.termDockFocused = v && m.termDockVisible()
	if m.termDock != nil {
		m.termDock.SetFocused(m.termDockFocused)
	}
}

func (m *App) ensureTermScreen(id, title string) *shellterm.Screen {
	if m.termScreens == nil {
		m.termScreens = make(map[string]*shellterm.Screen)
	}
	if s := m.termScreens[id]; s != nil {
		return s
	}
	s := shellterm.New(m.Theme, id, title)
	s.SetCommand(m.terminalCommand)
	s.SetCopy(rendering.CopyToClipboardCmd)
	m.termScreens[id] = s
	return s
}

func (m *App) reportTerminalIOError(operation, id string, err error) {
	if err == nil || m.Feedback == nil {
		return
	}
	if id == "" {
		m.Feedback.NotifyError(fmt.Sprintf("Terminal %s failed: %v", operation, err))
		return
	}
	m.Feedback.NotifyError(fmt.Sprintf("Terminal %s failed (%s): %v", operation, id, err))
}

func (m *App) dropTermScreen(id string) {
	if m.termDock != nil {
		m.termDock.Unmount(id)
	}
	if s := m.termScreens[id]; s != nil {
		s.Close()
		delete(m.termScreens, id)
	}
	if m.termDock != nil && m.termDock.Empty() {
		m.termDockFocused = false
	}
}

func (m *App) openNewTerminal() tea.Cmd {
	service := m.terminalService()
	if service == nil {
		if m.Feedback != nil {
			m.Feedback.NotifyError("Terminals are unavailable")
		}
		return nil
	}
	root, generation, controller := m.ws.RootPath(), m.workspaceSwitchGen.Load(), m.terminalController()
	return func() tea.Msg {
		info, err := service.Create(workspace.TerminalCreateOpts{CWD: root, Cols: defaultTermCols, Rows: defaultTermRows})
		var lease workspace.TerminalLease
		if err == nil {
			lease, err = service.Acquire(info.ID, controller)
		}
		return terminalCreatedMsg{service: service, generation: generation, info: info, lease: lease, err: err}
	}
}

func (m *App) dockTerminal(id string) {
	mgr := m.termMgr()
	if mgr == nil || m.termDock == nil {
		return
	}
	info, ok := mgr.Get(id)
	if !ok {
		return
	}
	screen := m.ensureTermScreen(id, info.Title)
	screen.SetFloating(false)
	screen.SetAttention(false)
	if m.overlayHost != nil {
		m.suppressTermClose = id
		m.overlayHost.Close(ui.TerminalOverlayID(id))
		m.suppressTermClose = ""
	}
	m.setTerminalPlacement(id, workspace.TerminalDocked)
	m.termDock.Mount(id, screen)
	m.termDock.ShowPanel(termdock.PanelTerminal)
	m.setSessionDeckFocused(false)
	m.setTermDockFocused(true)
	m.FocusStack.Clear()
}

func (m *App) promoteTerminal(id string) {
	mgr := m.termMgr()
	if mgr == nil {
		return
	}
	info, ok := mgr.Get(id)
	if !ok {
		return
	}
	screen := m.ensureTermScreen(id, info.Title)
	if m.termDock != nil {
		m.termDock.Unmount(id)
	}
	screen.SetFloating(true)
	screen.SetAttention(false)
	m.setTerminalPlacement(id, workspace.TerminalFloating)
	m.setTermDockFocused(false)
	m.openTerminalWindow(id, screen)
}

func (m *App) openTerminalWindow(id string, screen *shellterm.Screen) {
	if m.overlayHost == nil || screen == nil {
		return
	}
	oid := ui.TerminalOverlayID(id)
	if m.overlayHost.IsOpen(oid) {
		if fw := m.overlayHost.Window(oid); fw != nil && m.windowManager != nil {
			m.windowManager.FocusWindow(string(oid))
		}
		return
	}
	w := max(48, min(m.width*70/100, 100))
	h := max(14, min(m.height*60/100, 32))
	if w > m.width-4 {
		w = max(24, m.width-4)
	}
	if h > m.height-4 {
		h = max(10, m.height-4)
	}
	frame := ui.NewWindowFrameWidget(
		m.Theme,
		ui.NewID(ui.RootID, string(oid)+"-frame"),
		ui.FrameWindow,
		screen.Title(),
		screen,
		nil,
		true,
		w,
		h,
	)
	if frame.Frame() != nil {
		// JetBrains-style title-bar control: dock the tool window back into
		// the panel (replaces the old in-body action bar).
		frame.Frame().SetTitleAction("⇲", "Dock")
		if screen.HasAttention() {
			frame.Frame().SetIcon("•")
		}
	}
	fw := wm.NewFloatingWindow(string(oid), screen.Title(), frame, w, h,
		wm.WithDecoration(true),
		wm.WithMovable(true),
		wm.WithResizable(true),
		wm.WithClosable(true),
		wm.WithFocusable(true),
		wm.WithMinSize(min(40, w), min(10, h)),
		wm.WithClickOutside(wm.ClickOutsideKeepRoute),
	)
	// The dock title action owns its title-bar cells; clicks there must not
	// start a window drag.
	fw.SetTitleActionWidth(3)
	wm.CenterOnScreen(fw, m.width, m.height)
	closedID := id
	m.overlayHost.Open(oid, fw, ui.DefaultOverlayPolicy(), func() {
		if m.suppressTermClose == closedID {
			return
		}
		m.closeTerminal(closedID)
	})
	if frame.Host() != nil {
		frame.Host().Focus(screen.ID())
	}
}

func (m *App) focusTerminal(id string) {
	mgr := m.termMgr()
	if mgr == nil {
		return
	}
	info, ok := mgr.Get(id)
	if !ok {
		return
	}
	if m.terminalPlacement(info.ID) == workspace.TerminalFloating {
		oid := ui.TerminalOverlayID(id)
		if !m.overlayHost.IsOpen(oid) {
			m.promoteTerminal(id)
			return
		}
		if m.windowManager != nil {
			m.windowManager.FocusWindow(string(oid))
			m.setFloatingTerminalAttention(id, false)
		}
		return
	}
	m.dockTerminal(id)
}

func (m *App) closeTerminal(id string) {
	if id == "" {
		return
	}
	if m.terminalClosed == nil {
		m.terminalClosed = make(map[string]bool)
	}
	m.terminalClosed[id] = true
	if m.overlayHost != nil {
		m.suppressTermClose = id
		m.overlayHost.Close(ui.TerminalOverlayID(id))
		m.suppressTermClose = ""
	}
	m.dropTermScreen(id)
	delete(m.terminalLeases, id)
	delete(m.terminalPlacements, id)
	if service := m.terminalService(); service != nil {
		m.runDetachedAsyncCmd(func() tea.Msg { service.Close(id); return nil })
	}
}

// tickTerminalCursors advances libghostty cursor presentation. Docked
// terminals are repainted with the root shell after every Update; retained
// floating windows need an explicit dirty mark only when their blink phase
// actually changes.
func (m *App) tickTerminalCursors(now time.Time) {
	for id, s := range m.termScreens {
		if s == nil {
			continue
		}
		var fw *wm.FloatingWindow
		if m.overlayHost != nil {
			fw = m.overlayHost.Window(ui.TerminalOverlayID(id))
		}
		if fw != nil && fw.Focused() && s.HasAttention() {
			m.setFloatingTerminalAttention(id, false)
		}
		if s.TickCursor(now) && fw != nil {
			fw.MarkContentDirty()
		}
	}
}

// markTerminalBell exposes an unseen bell on the dock tab or floating frame
// of a terminal that does not currently own input.
func (m *App) markTerminalBell(id string) {
	if m.floatingTerminalOwnsInput(id) {
		return
	}
	wmFocused := m.windowManager != nil && m.windowManager.Focused() != nil
	focused := !wmFocused && m.terminalDockOwnsKeys() && m.termDock.ActiveID() == id
	if focused {
		return
	}
	if m.termDock != nil && m.termDock.Contains(id) {
		m.termDock.SetAttention(id, true)
		return
	}
	m.setFloatingTerminalAttention(id, true)
}

func (m *App) floatingTerminalOwnsInput(id string) bool {
	if m.windowManager == nil {
		return false
	}
	fw := m.windowManager.Focused()
	return fw != nil && fw.ID == string(ui.TerminalOverlayID(id))
}

func (m *App) setFloatingTerminalAttention(id string, attention bool) {
	s := m.termScreens[id]
	if s == nil {
		return
	}
	changed := s.SetAttention(attention)
	if m.overlayHost == nil {
		return
	}
	fw := m.overlayHost.Window(ui.TerminalOverlayID(id))
	if fw == nil {
		return
	}
	if frame, ok := fw.Content.(*ui.WindowFrameWidget); ok && frame.Frame() != nil {
		icon := ""
		if attention {
			icon = "•"
		}
		frame.Frame().SetIcon(icon)
	}
	if changed {
		fw.MarkContentDirty()
	}
}

func (m *App) handleTerminalExited(msg workspace.TerminalExitedMsg) {
	m.closeTerminal(msg.ID)
}

func (m *App) handleTerminalAction(action ui.Action) bool {
	switch action.ID {
	case shellterm.ActionPromote:
		id, _ := action.Payload.(string)
		m.promoteTerminal(id)
		return true
	case shellterm.ActionDock:
		id, _ := action.Payload.(string)
		m.dockTerminal(id)
		return true
	case shellterm.ActionClose:
		id, _ := action.Payload.(string)
		m.closeTerminal(id)
		return true
	case shellterm.ActionTakeControl:
		id, _ := action.Payload.(string)
		m.openTerminalControlMenu(id)
		return true
	}
	return false
}

func (m *App) toggleTerminalFocus() tea.Cmd {
	if m.terminalOwnsKeys() {
		if m.windowManager != nil {
			if fw := m.windowManager.Focused(); fw != nil && ui.IsTerminalOverlay(ui.OverlayID(fw.ID)) {
				m.windowManager.ClearFocus()
			}
		}
		m.setTermDockFocused(false)
		return nil
	}
	mgr := m.termMgr()
	if mgr == nil {
		return m.openNewTerminal()
	}
	list := mgr.List()
	if len(list) == 0 {
		return m.openNewTerminal()
	}
	last := list[len(list)-1]
	m.focusTerminal(last.ID)
	return nil
}

func (m *App) applyTerminalTitle(id, title string) {
	if title == "" {
		return
	}
	if s := m.termScreens[id]; s != nil {
		s.SetWindowTitle(title)
	}
	if m.termDock != nil {
		m.termDock.SetTitle(id, title)
	}
	if m.overlayHost != nil {
		if fw := m.overlayHost.Window(ui.TerminalOverlayID(id)); fw != nil {
			fw.Title = title
			if w, ok := fw.Content.(*ui.WindowFrameWidget); ok && w.Frame() != nil {
				w.Frame().SetTitle(title)
			}
			fw.MarkContentDirty()
		}
	}
}

func (m *App) discardTerminalUI() {
	if m.ws.Terminals() != nil && m.terminalClientID != "" {
		m.ws.Terminals().Detach(m.terminalController())
	}
	if m.overlayHost != nil {
		for _, e := range m.overlayHost.All() {
			if !ui.IsTerminalOverlay(e.ID) {
				continue
			}
			id, _ := ui.TerminalIDFromOverlay(e.ID)
			m.suppressTermClose = id
			m.overlayHost.Close(e.ID)
			m.suppressTermClose = ""
		}
	}
	if m.termDock != nil {
		for _, id := range m.termDock.IDs() {
			m.termDock.Unmount(id)
		}
	}
	for id, s := range m.termScreens {
		if s != nil {
			s.Close()
		}
		delete(m.termScreens, id)
	}
	m.termScreens = make(map[string]*shellterm.Screen)
	m.terminalLeases = nil
	m.terminalReads = nil
	m.terminalDirty = nil
	m.terminalPlacements = nil
	m.terminalQueue = nil
	m.terminalBusy = false
	m.terminalClipboard = nil
	m.terminalClosed = nil
	m.termDockFocused = false
}

func (m *App) dispatchTermDockKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.rootHost == nil || m.termDock == nil {
		return m, nil
	}
	m.syncRootFocus()
	res := m.rootHost.Handle(msg)
	return m, tea.Batch(res.Cmd, m.handleHostAction(res.Action))
}

func (m *App) dispatchTermDockPaste(msg tea.PasteMsg) (tea.Model, tea.Cmd) {
	if m.rootHost == nil {
		return m, nil
	}
	m.syncRootFocus()
	res := m.rootHost.Handle(msg)
	return m, tea.Batch(res.Cmd, m.handleHostAction(res.Action))
}

// sessionDeck still calls this after Close of a panel; terminals no longer
// live there, so a leftover terminal/ prefix is ignored.
func (m *App) reconcileClosedTerminalPanels(before []string) {
	_ = before
}

func (m *App) deckPanelIDs() []string {
	projection := m.activeSessionDeck()
	if projection == nil || projection.deck == nil {
		return nil
	}
	panels := projection.deck.Panels()
	ids := make([]string, 0, len(panels))
	for _, p := range panels {
		ids = append(ids, p.ID)
	}
	return ids
}

func terminalIDFromPanel(panelID string) (string, bool) {
	const prefix = "terminal/"
	if !strings.HasPrefix(panelID, prefix) {
		return "", false
	}
	return strings.TrimPrefix(panelID, prefix), true
}
