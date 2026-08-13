package viewmodel

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/tui/screens/shellterm"
	"crude/tui/ui"
	"crude/tui/wm"
	"crude/workspace"
)

const (
	terminalPanelPrefix = "terminal/"
	defaultTermCols     = 80
	defaultTermRows     = 24
)

func terminalPanelID(id string) string { return terminalPanelPrefix + id }

func terminalIDFromPanel(panelID string) (string, bool) {
	if !strings.HasPrefix(panelID, terminalPanelPrefix) {
		return "", false
	}
	return strings.TrimPrefix(panelID, terminalPanelPrefix), true
}

func (m *App) termMgr() *workspace.TerminalManager {
	if m == nil || m.Workspace == nil {
		return nil
	}
	return m.TermMgr
}

func (m *App) ensureTermScreen(id, title string) *shellterm.Screen {
	if m.termScreens == nil {
		m.termScreens = make(map[string]*shellterm.Screen)
	}
	if s := m.termScreens[id]; s != nil {
		return s
	}
	s := shellterm.New(m.Theme, id, title)
	s.SetWrite(func(data []byte) {
		if mgr := m.termMgr(); mgr != nil {
			_ = mgr.Write(id, data)
		}
	})
	s.SetResize(func(cols, rows int) {
		if mgr := m.termMgr(); mgr != nil {
			_ = mgr.Resize(id, cols, rows)
		}
	})
	s.SetTitleHandler(func(t string) {
		if mgr := m.termMgr(); mgr != nil {
			mgr.SetTitle(id, t)
		}
	})
	m.termScreens[id] = s
	return s
}

func (m *App) openNewTerminal() tea.Cmd {
	mgr := m.termMgr()
	if mgr == nil {
		if m.StatusBar != nil {
			m.StatusBar.SetError("Terminals are unavailable")
		}
		return nil
	}
	info, err := mgr.Create(workspace.TerminalCreateOpts{
		CWD:  m.RootPath,
		Cols: defaultTermCols,
		Rows: defaultTermRows,
	})
	if err != nil {
		if m.StatusBar != nil {
			m.StatusBar.SetError(err.Error())
		}
		return nil
	}
	m.ensureTermScreen(info.ID, info.Title)
	if m.activeSessionDeck() == nil {
		m.promoteTerminal(info.ID)
		return nil
	}
	m.dockTerminal(info.ID)
	m.setActivity(activityTerminals, true)
	return nil
}

func (m *App) dockTerminal(id string) {
	mgr := m.termMgr()
	if mgr == nil {
		return
	}
	info, ok := mgr.Get(id)
	if !ok {
		return
	}
	screen := m.ensureTermScreen(id, info.Title)
	screen.SetFloating(false)
	if m.overlayHost != nil {
		m.suppressTermClose = id
		m.overlayHost.Close(ui.TerminalOverlayID(id))
		m.suppressTermClose = ""
	}
	mgr.SetPlacement(id, workspace.TerminalDocked)
	projection := m.activeSessionDeck()
	if projection == nil || projection.deck == nil {
		m.promoteTerminal(id)
		return
	}
	projection.deck.Open(ui.PanelEntry{
		ID:              terminalPanelID(id),
		Title:           screen.Title(),
		Content:         screen,
		Closable:        true,
		Policy:          ui.PanelInteractive,
		PreferredHeight: 14,
	})
	projection.deckFocused = true
	projection.deckFocus = screen.ID()
	projection.deckHost.MarkLayout()
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
	screen.SetFloating(true)
	if projection := m.activeSessionDeck(); projection != nil && projection.deck != nil {
		_ = projection.deck.Close(terminalPanelID(id))
		projection.deckHost.MarkLayout()
	}
	mgr.SetPlacement(id, workspace.TerminalFloating)
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
	fw := wm.NewFloatingWindow(string(oid), screen.Title(), frame, w, h,
		wm.WithDecoration(true),
		wm.WithMovable(true),
		wm.WithResizable(true),
		wm.WithClosable(true),
		wm.WithFocusable(true),
		wm.WithMinSize(min(40, w), min(10, h)),
		wm.WithClickOutside(wm.ClickOutsideKeepRoute),
	)
	wm.CenterOnScreen(fw, m.width, m.height)
	closedID := id
	m.overlayHost.Open(oid, fw, ui.DefaultOverlayPolicy(), func() {
		if m.suppressTermClose == closedID {
			return
		}
		m.closeTerminal(closedID)
	})
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
	if info.Placement == workspace.TerminalFloating {
		oid := ui.TerminalOverlayID(id)
		if !m.overlayHost.IsOpen(oid) {
			m.promoteTerminal(id)
			return
		}
		if m.windowManager != nil {
			m.windowManager.FocusWindow(string(oid))
		}
		return
	}
	m.dockTerminal(id)
}

func (m *App) closeTerminal(id string) {
	if id == "" {
		return
	}
	if projection := m.activeSessionDeck(); projection != nil && projection.deck != nil {
		_ = projection.deck.Close(terminalPanelID(id))
		projection.deckHost.MarkLayout()
	}
	if m.overlayHost != nil {
		m.suppressTermClose = id
		m.overlayHost.Close(ui.TerminalOverlayID(id))
		m.suppressTermClose = ""
	}
	if m.termScreens != nil {
		delete(m.termScreens, id)
	}
	if mgr := m.termMgr(); mgr != nil {
		mgr.Close(id)
	}
}

func (m *App) handleTerminalOutput(msg workspace.TerminalOutputMsg) {
	if s := m.termScreens[msg.ID]; s != nil {
		s.Feed(msg.Data)
	}
	if m.overlayHost != nil {
		if fw := m.overlayHost.Window(ui.TerminalOverlayID(msg.ID)); fw != nil {
			fw.MarkContentDirty()
		}
	}
}

func (m *App) handleTerminalExited(msg workspace.TerminalExitedMsg) {
	if s := m.termScreens[msg.ID]; s != nil {
		s.SetExited(true)
	}
}

func (m *App) handleTerminalClosed(msg workspace.TerminalClosedMsg) {
	if m.termScreens != nil {
		delete(m.termScreens, msg.ID)
	}
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
	case shellterm.ActionWrite:
		payload, ok := action.Payload.(shellterm.WritePayload)
		if ok {
			if mgr := m.termMgr(); mgr != nil {
				_ = mgr.Write(payload.ID, payload.Data)
			}
		}
		return true
	}
	return false
}

func (m *App) reconcileClosedTerminalPanels(before []string) {
	projection := m.activeSessionDeck()
	after := map[string]bool{}
	if projection != nil && projection.deck != nil {
		for _, p := range projection.deck.Panels() {
			after[p.ID] = true
		}
	}
	for _, id := range before {
		termID, ok := terminalIDFromPanel(id)
		if !ok || after[id] {
			continue
		}
		if mgr := m.termMgr(); mgr != nil {
			if info, exists := mgr.Get(termID); exists && info.Placement == workspace.TerminalFloating {
				continue
			}
		}
		m.closeTerminal(termID)
	}
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

func (m *App) toggleTerminalFocus() tea.Cmd {
	mgr := m.termMgr()
	if mgr == nil {
		return m.openNewTerminal()
	}
	list := mgr.List()
	if len(list) == 0 {
		return m.openNewTerminal()
	}
	last := list[len(list)-1]
	m.setActivity(activityTerminals, true)
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
	if m.overlayHost != nil {
		if fw := m.overlayHost.Window(ui.TerminalOverlayID(id)); fw != nil {
			fw.Title = title
			if w, ok := fw.Content.(*ui.WindowFrameWidget); ok && w.Frame() != nil {
				w.Frame().SetTitle(title)
			}
			fw.MarkContentDirty()
		}
	}
	projection := m.activeSessionDeck()
	if projection == nil || projection.deck == nil {
		return
	}
	screen := m.termScreens[id]
	if screen == nil {
		return
	}
	for _, p := range projection.deck.Panels() {
		if p.ID != terminalPanelID(id) {
			continue
		}
		projection.deck.Open(ui.PanelEntry{
			ID:              p.ID,
			Title:           title,
			Content:         screen,
			Closable:        p.Closable,
			Policy:          p.Policy,
			PreferredHeight: p.PreferredHeight,
		})
		projection.deckHost.MarkLayout()
		return
	}
}

func (m *App) discardTerminalUI() {
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
	if projection := m.activeSessionDeck(); projection != nil && projection.deck != nil {
		for _, p := range projection.deck.Panels() {
			if _, ok := terminalIDFromPanel(p.ID); ok {
				_ = projection.deck.Close(p.ID)
			}
		}
		projection.deckHost.MarkLayout()
	}
	m.termScreens = make(map[string]*shellterm.Screen)
}
