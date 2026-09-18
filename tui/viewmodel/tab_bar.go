package viewmodel

import (
	"fmt"

	tea "charm.land/bubbletea/v2"

	"crude/tui/screens/shelltabs"
	"crude/tui/ui"
	"crude/tui/window"
)

func (m *App) tabBarVisible() bool {
	return m.Win != nil && m.Win.IsOpen() && m.Win.TabCount() >= 1 && m.Win.Layout() == window.TabHorizontal
}

// tabBarRailCol is the screen column where the strip's first label (and so its
// underline and hit area) begins: the content column plus the separator column
// the main bar leaves beside the navigation rail. The baseline sits one column
// left of it, on the rail itself. Hit testing and tests share this one owner
// instead of re-deriving the arithmetic.
func (m *App) tabBarRailCol() int {
	return m.computeLayout().SidebarW + shelltabs.RailInset
}

// tabBarLocalX converts a raw screen X into the tab bar screen's
// slot-local X. The slot spans the whole content column; the shelltabs
// screen translates through its tabs bounds internally.
func (m *App) tabBarLocalX(screenX int) int {
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
	return m.tabScreen.HitTest(m.tabBarLocalX(screenX))
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
	hover := -1
	if m.Win.Hover.IsActive() {
		hover = m.Win.Hover.Index
	}
	barFocused := m.FocusStack != nil && m.FocusStack.Top() == FocusTabBar
	m.tabScreen.SetProjectionFromWindow(m.Win, hover, barFocused)
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
			if m.FocusStack.Top() == FocusTabBar {
				m.FocusStack.Pop()
			}
			m.handleTabSwitch()
		}
	}
	return res.Cmd
}

// handleTabBarEvent routes tab-bar mouse events through the root host (the
// tab slot geometry and hit-testing live in the shell tree). Actions from
// the Tabs control bubble to the viewmodel via handleTabBarHostResult.
func (m *App) handleTabBarEvent(msg tea.Msg) tea.Cmd {
	if !m.tabBarVisible() || m.tabScreen == nil || m.rootHost == nil {
		return nil
	}
	m.syncTabBarHost()
	m.rootHost.Focus(shelltabs.TabsID)
	res := m.rootHost.Handle(msg)
	switch msg := msg.(type) {
	case tea.MouseMotionMsg:
		if m.tabScreen.TabsControl().HoverIndex() >= 0 {
			m.Win.Hover.Set(m.tabScreen.TabsControl().HoverIndex())
		} else {
			m.Win.Hover.Clear()
		}
		_ = msg
	}
	return m.handleTabBarHostResult(res)
}
