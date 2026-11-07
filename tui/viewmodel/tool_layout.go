package viewmodel

import (
	"slices"

	"crude/tui/screens/activitybar"
	"crude/tui/ui"
)

func (app *App) captureToolView() {
	if app.Tree == nil {
		return
	}
	if app.toolLayout.Views == nil {
		app.toolLayout.Views = make(map[string]ToolViewState)
	}
	state := ToolViewState{Width: app.Tree.Width}
	if app.activityList != nil && app.activityView != activityFiles {
		list := app.activityList.ViewState()
		state.Filter, state.Selection, state.Offset = list.Filter, list.Selection, list.Offset
	}
	app.toolLayout.Views[app.activityView.ID()] = state
}

func (app *App) navigationActions(view activitybar.View) ui.ContextMenuSection {
	if view == activityWorkspaces || view == activityRemotes {
		return ui.ContextMenuSection{SectionLabel: "Navigation", Items: []ui.ContextMenuItem{{Label: "Reset navigation", Action: app.resetToolLayout}}}
	}
	pinned := slices.Contains(app.toolLayout.Rail, view.ID())
	if app.toolLayout.Rail == nil {
		pinned = true
	}
	label := "Unpin shortcut"
	if !pinned {
		label = "Pin shortcut"
	}
	return ui.ContextMenuSection{SectionLabel: "Navigation", Items: []ui.ContextMenuItem{
		{Label: label, Action: func() { app.changeNavigation(view, 0) }},
		{Label: "Move up", Disabled: !pinned, Action: func() { app.changeNavigation(view, -1) }},
		{Label: "Move down", Disabled: !pinned, Action: func() { app.changeNavigation(view, 1) }},
		{Label: "Reset navigation", Action: app.resetToolLayout},
	}}
}

func (app *App) changeNavigation(view activitybar.View, direction int) {
	if app.toolLayout.Rail == nil {
		app.toolLayout.Rail = DefaultToolLayout().Rail
	}
	index := slices.Index(app.toolLayout.Rail, view.ID())
	if direction == 0 {
		if index < 0 {
			app.toolLayout.Rail = append(app.toolLayout.Rail, view.ID())
		} else {
			app.toolLayout.Rail = slices.Delete(app.toolLayout.Rail, index, index+1)
		}
	} else if index >= 0 {
		next := min(len(app.toolLayout.Rail)-1, max(0, index+direction))
		app.toolLayout.Rail[index], app.toolLayout.Rail[next] = app.toolLayout.Rail[next], app.toolLayout.Rail[index]
	}
	app.activityBar.SetOrder(app.toolLayout.Rail)
	app.viewMemoryVersion++
	app.startSourceControlMutation(app.queueViewMemorySave())
}

func (app *App) resetToolLayout() {
	app.toolLayout = DefaultToolLayout()
	app.workspacePanelHeights = make(map[string]int)
	app.termDockHeight = defaultTermDockHeight
	app.activityBar.SetOrder(app.toolLayout.Rail)
	app.viewMemoryVersion++
	app.startSourceControlMutation(app.queueViewMemorySave())
}
