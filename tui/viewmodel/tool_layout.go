package viewmodel

import (
	"maps"
	"slices"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/screens/activitybar"
	"crude/tui/ui"
	"crude/workspace"
)

type toolLayoutMsg struct {
	generation, version uint64
	layout              workspace.ToolLayout
	err                 error
}
type toolLayoutSaveMsg struct{ generation uint64 }
type toolLayoutSavedMsg struct {
	generation, version uint64
	err                 error
}

func (app *App) loadToolLayoutCmd() tea.Cmd {
	owner, generation, version := &workspace.Workspace{WorkspaceDB: app.WorkspaceDB}, app.workspaceSwitchGen.Load(), app.toolLayoutVersion
	return func() tea.Msg {
		layout, err := owner.LoadToolLayout()
		return toolLayoutMsg{generation: generation, version: version, layout: layout, err: err}
	}
}

func (app *App) captureToolView() {
	if app.Tree == nil {
		return
	}
	if app.toolLayout.Views == nil {
		app.toolLayout.Views = make(map[string]workspace.ToolViewState)
	}
	state := workspace.ToolViewState{Width: app.Tree.Width}
	if app.activityList != nil && (app.activityView == activityAgents || app.activityView == activitySkills || app.activityView == activityMCP) {
		list := app.activityList.ViewState()
		state.Filter, state.Selection, state.Offset = list.Filter, list.Selection, list.Offset
	}
	app.toolLayout.Views[app.activityView.ID()] = state
}

func (app *App) navigationActions(view activitybar.View) ui.ContextMenuSection {
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
		app.toolLayout.Rail = workspace.DefaultToolLayout().Rail
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
	app.toolLayoutVersion++
	app.startSourceControlMutation(app.queueToolLayoutSave())
}

func (app *App) resetToolLayout() {
	app.toolLayout = workspace.DefaultToolLayout()
	app.workspacePanelHeights = make(map[string]int)
	app.workspacePanelMaximized = false
	app.termDockHeight = defaultTermDockHeight
	app.activityBar.SetOrder(app.toolLayout.Rail)
	app.toolLayoutVersion++
	app.startSourceControlMutation(app.queueToolLayoutSave())
}

func (app *App) queueToolLayoutSave() tea.Cmd {
	if app.toolLayoutSaveQueued {
		return nil
	}
	app.toolLayoutSaveQueued = true
	generation := app.workspaceSwitchGen.Load()
	return tea.Tick(400*time.Millisecond, func(time.Time) tea.Msg { return toolLayoutSaveMsg{generation: generation} })
}

func (app *App) saveToolLayoutCmd() tea.Cmd {
	app.captureToolView()
	layout := app.toolLayout
	if layout.Rail == nil {
		layout.Rail = workspace.DefaultToolLayout().Rail
	}
	layout.PanelHeights, layout.Views = maps.Clone(app.workspacePanelHeights), maps.Clone(layout.Views)
	layout.Rail = slices.Clone(layout.Rail)
	owner := &workspace.Workspace{WorkspaceDB: app.WorkspaceDB}
	generation, version := app.workspaceSwitchGen.Load(), app.toolLayoutVersion
	app.toolLayoutSaving = true
	return func() tea.Msg { return toolLayoutSavedMsg{generation, version, owner.SaveToolLayout(layout)} }
}
