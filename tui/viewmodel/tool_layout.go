package viewmodel

import (
	"slices"

	"crude/tui/screens/activitybar"
	"crude/tui/ui"
)

func (m *App) captureToolView() {
	if m.Tree == nil {
		return
	}
	if m.toolLayout.Views == nil {
		m.toolLayout.Views = make(map[string]ToolViewState)
	}
	state := ToolViewState{Width: m.Tree.Width}
	if m.activityList != nil && m.activityView != activityFiles {
		list := m.activityList.ViewState()
		state.Filter, state.Selection, state.Offset = list.Filter, list.Selection, list.Offset
	}
	m.toolLayout.Views[m.activityView.ID()] = state
}

func (m *App) navigationActions(view activitybar.View) ui.ContextMenuSection {
	if view == activityWorkspaces || view == activityRemotes {
		return ui.ContextMenuSection{SectionLabel: "Navigation", Items: []ui.ContextMenuItem{{Label: "Reset navigation", Action: m.resetToolLayout}}}
	}
	pinned := slices.Contains(m.toolLayout.Rail, view.ID())
	if m.toolLayout.Rail == nil {
		pinned = true
	}
	label := "Unpin shortcut"
	if !pinned {
		label = "Pin shortcut"
	}
	return ui.ContextMenuSection{SectionLabel: "Navigation", Items: []ui.ContextMenuItem{
		{Label: label, Action: func() { m.changeNavigation(view, 0) }},
		{Label: "Move up", Disabled: !pinned, Action: func() { m.changeNavigation(view, -1) }},
		{Label: "Move down", Disabled: !pinned, Action: func() { m.changeNavigation(view, 1) }},
		{Label: "Reset navigation", Action: m.resetToolLayout},
	}}
}

func (m *App) changeNavigation(view activitybar.View, direction int) {
	if m.toolLayout.Rail == nil {
		m.toolLayout.Rail = DefaultToolLayout().Rail
	}
	index := slices.Index(m.toolLayout.Rail, view.ID())
	if direction == 0 {
		if index < 0 {
			m.toolLayout.Rail = append(m.toolLayout.Rail, view.ID())
		} else {
			m.toolLayout.Rail = slices.Delete(m.toolLayout.Rail, index, index+1)
		}
	} else if index >= 0 {
		next := min(len(m.toolLayout.Rail)-1, max(0, index+direction))
		m.toolLayout.Rail[index], m.toolLayout.Rail[next] = m.toolLayout.Rail[next], m.toolLayout.Rail[index]
	}
	m.activityBar.SetOrder(m.toolLayout.Rail)
	m.viewMemoryVersion++
	m.startSourceControlMutation(m.queueViewMemorySave())
}

func (m *App) resetToolLayout() {
	m.toolLayout = DefaultToolLayout()
	m.workspacePanelHeights = make(map[string]int)
	m.termDockHeight = defaultTermDockHeight
	m.activityBar.SetOrder(m.toolLayout.Rail)
	m.viewMemoryVersion++
	m.startSourceControlMutation(m.queueViewMemorySave())
}
