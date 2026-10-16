package viewmodel

import (
	"fmt"

	tea "charm.land/bubbletea/v2"

	"crude/tui/screens/shelltabs"
	"crude/tui/ui"
	"crude/tui/window"
)

func (m *App) tabBarVisible() bool {
	return m.Win != nil && !m.mainAreaEmpty() && m.Win.Layout() == window.TabHorizontal
}

// mainAreaEmpty reports that the docked main area is one group holding no
// tab. Tabs may still live in windows: the main area then shows the
// launcher, exactly as when nothing is open, never an empty pane the user
// did not ask for.
func (m *App) mainAreaEmpty() bool {
	if m.Win == nil {
		return true
	}
	root := m.Win.PaneRoot()
	return root.Split == nil && m.Win.GroupTabCount(root.Group) == 0
}

// tabBarRailCol is the screen column where the strip's first label (and so its
// underline and hit area) begins: the content column plus the separator column
// the main bar leaves beside the navigation rail. The baseline sits one column
// left of it, on the rail itself. Hit testing and tests share this one owner
// instead of re-deriving the arithmetic.
func (m *App) tabBarRailCol() int {
	if m.mainPanes != nil {
		if r, ok := m.screenRectOf(m.tabScreen.ID()); ok {
			return r.X + shelltabs.RailInset
		}
	}
	return m.computeLayout().SidebarW + shelltabs.RailInset
}

// tabBarLocalX converts a raw screen X into the tab bar screen's
// slot-local X. The slot spans the whole content column; the shelltabs
// screen translates through its tabs bounds internally.
func (m *App) tabBarLocalX(screenX int) int {
	if m.mainPanes != nil {
		if r, ok := m.screenRectOf(m.tabScreen.ID()); ok {
			return screenX - r.X
		}
	}
	x := screenX - m.computeLayout().SidebarW
	if x < 0 {
		return 0
	}
	return x
}

func (m *App) tabBarHitTest(screenX int) (tabIndex int, closeHit bool) {
	if !m.tabBarVisible() || m.tabScreen == nil {
		return -1, false
	}
	m.syncTabBarHost()
	i, closeHit := m.tabScreen.HitTest(m.tabBarLocalX(screenX))
	tabs := m.Win.GroupTabs(m.Win.ActiveGroup())
	if i < 0 || i >= len(tabs) {
		return -1, false
	}
	return m.Win.IndexOf(tabs[i].ID), closeHit
}

func (m *App) winTabIndexByID(id string) int {
	for i, tab := range m.Win.Tabs() {
		if uiTabID(tab.ID) == id {
			return i
		}
	}
	return -1
}

func uiTabID(id window.TabID) string {
	return fmt.Sprintf("%d", uint64(id))
}

func (m *App) syncTabBarHost() {
	if m.tabScreen == nil {
		return
	}
	m.syncEditorTabPresentation()
	barFocused := m.FocusStack != nil && m.FocusStack.Top() == FocusTabBar
	active := window.TabID(0)
	if tab := m.Win.ActiveTab(); tab != nil {
		active = tab.ID
	}
	m.tabScreen.SetProjectionFromTabs(m.Win.GroupTabs(m.Win.ActiveGroup()), active, barFocused)
}

// releaseTabBarFocus ends a tab operation without disturbing focus owned by a
// sidebar, dock, or modal. The selected content naturally owns the next key.
// Release at the user action, not a later asynchronous save acknowledgement.
func (m *App) releaseTabBarFocus() {
	if m.FocusStack != nil && m.FocusStack.Top() == FocusTabBar {
		m.FocusStack.Pop()
	}
	m.syncRootFocus()
}

func (m *App) handleTabBarHostResult(res ui.EventResult) tea.Cmd {
	if res.Action != nil {
		switch res.Action.ID {
		case "tabs.activate":
			if id, ok := res.Action.Payload.(string); ok {
				if idx := m.winTabIndexByID(id); idx >= 0 {
					m.Win.SwitchTo(idx)
					m.handleTabSwitch()
				}
			}
		case "tabs.close":
			if id, ok := res.Action.Payload.(string); ok {
				if idx := m.winTabIndexByID(id); idx >= 0 {
					return m.handleAgentTabClose(idx)
				}
			}
		case "tabs.enter":
			m.handleTabSwitch()
			m.releaseTabBarFocus()
		case "tabs.new":
			return m.launcherNewSession()
		}
	}
	return res.Cmd
}
