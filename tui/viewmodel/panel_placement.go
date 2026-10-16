package viewmodel

import (
	"strings"

	"crude/tui/screens/termdock"
	"crude/tui/ui"
	"crude/tui/window"
)

// panelPlacement is where a workspace surface (Git, Problems, Notices,
// Workspace, Memory, Board) is shown. A surface is mounted in exactly one
// place; the dock is the default. A surface in a window is an ordinary tab
// whose group floats, so tabs and windows share one mechanism.
type panelPlacement uint8

const (
	placeDock panelPlacement = iota
	placeTab
)

var panelTitles = map[string]string{
	termdock.PanelWorkspace: "Workspace",
	termdock.PanelProblems:  "Problems",
	termdock.PanelNotices:   "Notices",
	termdock.PanelMemory:    "Memory",
	termdock.PanelBoard:     "Board",
	termdock.PanelGit:       "Git",
}

// movablePanel reports whether a dock surface can be shown as a tab or
// window. Terminals have their own floating windows.
func movablePanel(id string) bool {
	_, ok := panelTitles[id]
	return ok
}

func (m *App) placementOf(id string) panelPlacement {
	return m.panelPlacements[id]
}

func (m *App) setPlacement(id string, place panelPlacement) {
	if m.panelPlacements == nil {
		m.panelPlacements = make(map[string]panelPlacement)
	}
	if place == placeDock {
		delete(m.panelPlacements, id)
		return
	}
	m.panelPlacements[id] = place
}

func panelResource(id string) window.Resource {
	return window.Resource{Kind: window.ResourcePanel, ID: id}
}

// panelTab returns the tab showing the surface, if it is placed in one.
func (m *App) panelTab(id string) *window.Tab {
	if m.Win == nil {
		return nil
	}
	for _, tab := range m.Win.Tabs() {
		if tab.Resource == panelResource(id) {
			copy := tab
			return &copy
		}
	}
	return nil
}

// panelShown reports whether the surface is visible in any placement.
func (m *App) panelShown(id string) bool {
	if m.placementOf(id) == placeTab {
		if tab := m.panelTab(id); tab != nil {
			for _, group := range m.Win.AllGroupIDs() {
				if active := m.Win.GroupActiveTab(group); active != nil && active.ID == tab.ID {
					return true
				}
			}
		}
		return false
	}
	return m.termDock != nil && m.termDock.IsOpen() && m.termDock.ActivePanel() == id
}

func preferredFocus(content ui.Component) ui.ComponentID {
	if focus, ok := content.(interface{ PreferredFocus() ui.ComponentID }); ok {
		return focus.PreferredFocus()
	}
	return content.ID()
}

// revealPlacedPanel brings a surface that lives in a tab (docked or in a
// window) to the front. It reports false for a docked surface, which the
// dock toggles.
func (m *App) revealPlacedPanel(id string) bool {
	if m.placementOf(id) != placeTab {
		return false
	}
	tab := m.panelTab(id)
	if tab == nil {
		m.setPlacement(id, placeDock)
		return false
	}
	m.Win.SwitchTo(m.Win.IndexOf(tab.ID))
	m.handleTabSwitch()
	m.focusPanelContent(id)
	m.syncPaneWindowFocus()
	return true
}

func (m *App) focusPanelContent(id string) {
	content := m.termDock.Panel(id)
	if content == nil || m.rootHost == nil {
		return
	}
	m.FocusStack.Clear()
	m.setTermDockFocused(false)
	m.setSessionDeckFocused(false)
	m.syncPaneWindows()
	m.hostFor(preferredFocus(content)).Focus(preferredFocus(content))
}

// releasePlacement takes the surface out of its current place without
// showing it anywhere else.
func (m *App) releasePlacement(id string) {
	switch m.placementOf(id) {
	case placeTab:
		m.setPlacement(id, placeDock)
		if tab := m.panelTab(id); tab != nil {
			m.Win.CloseTabByID(tab.ID)
			m.handleTabSwitch()
		}
	default:
		if m.termDock != nil && m.termDock.IsOpen() && m.termDock.ActivePanel() == id {
			m.termDock.ClosePanel()
			m.setTermDockFocused(false)
		}
	}
	if inset, ok := m.termDock.Panel(id).(interface{ SetDockHeaderInset(int) }); ok {
		inset.SetDockHeaderInset(0)
	}
}

// openPanelAsTab makes the surface an ordinary main tab, like a file or an
// agent: it can be split, moved between panes and closed back to the dock.
func (m *App) openPanelAsTab(id string) {
	if !movablePanel(id) || m.termDock == nil || m.termDock.Panel(id) == nil {
		return
	}
	if m.placementOf(id) == placeTab && m.revealPlacedPanel(id) {
		return
	}
	m.releasePlacement(id)
	m.setPlacement(id, placeTab)
	tab := m.Win.OpenResource(panelResource(id), panelTitles[id], true)
	m.Win.SwitchTo(m.Win.IndexOf(tab.ID))
	m.handleTabSwitch()
	m.focusPanelContent(id)
	m.preparePlacedPanel(id)
	m.forceFrame()
}

// openPanelAsWindow shows the surface as a tab in its own movable,
// resizable window; the window's Dock button returns the tab to the main
// area and closing the tab returns the surface to the dock.
func (m *App) openPanelAsWindow(id string) {
	if !movablePanel(id) || m.termDock == nil || m.termDock.Panel(id) == nil {
		return
	}
	if tab := m.panelTab(id); tab != nil && m.Win.IsFloating(tab.Group) && len(m.Win.GroupTabs(tab.Group)) == 1 {
		m.revealPlacedPanel(id)
		return
	}
	m.openPanelAsTab(id)
	if tab := m.panelTab(id); tab != nil {
		m.openTabInWindow(tab.ID)
		m.focusPanelContent(id)
	}
}

// dockPanel returns a tab or window surface to the dock and shows it there.
func (m *App) dockPanel(id string) {
	if m.placementOf(id) == placeDock {
		return
	}
	m.releasePlacement(id)
	if id == termdock.PanelGit {
		m.startSourceControlMutation(m.toggleGitPanel())
		return
	}
	m.toggleWorkspacePanel(id)
}

// preparePlacedPanel refreshes the data a surface shows when it appears in
// a new place, as opening it in the dock does.
func (m *App) preparePlacedPanel(id string) {
	switch id {
	case termdock.PanelGit:
		m.startSourceControlMutation(m.refreshSourceControl())
	case termdock.PanelWorkspace, termdock.PanelProblems, termdock.PanelNotices:
		m.workspacePanelLastVersion, m.workspacePanelLastSecond = 0, 0
		m.syncWorkspacePanels()
	}
}

// activePanelTabContent is the surface shown by the focused group's tab.
func (m *App) activePanelTabContent() ui.Component {
	if m.Win == nil || m.termDock == nil {
		return nil
	}
	tab := m.Win.ActiveTab()
	if tab == nil || tab.Resource.Kind != window.ResourcePanel || m.placementOf(tab.Resource.ID) != placeTab {
		return nil
	}
	return m.termDock.Panel(tab.Resource.ID)
}

// panelOwnsFocus reports whether id is the surface or one of its controls.
func panelOwnsFocus(content ui.Component, id ui.ComponentID) bool {
	if owner, ok := content.(interface{ OwnsFocus(ui.ComponentID) bool }); ok {
		return owner.OwnsFocus(id)
	}
	root := string(content.ID())
	return string(id) == root || strings.HasPrefix(string(id), root+"/")
}
