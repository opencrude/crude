package viewmodel

import (
	"context"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/screens/boardbrowser"
	"crude/tui/screens/termdock"
	"crude/tui/ui"
	"crude/workspace"
)

type boardResultMsg struct {
	generation uint64
	request    boardbrowser.Request
	snapshot   workspace.BoardSnapshot
	err        error
}

func (app *App) openBoardBrowser() tea.Cmd {
	if app.termDock == nil {
		return nil
	}
	if app.boardBrowser == nil {
		app.boardBrowser = boardbrowser.New(app.Theme)
		app.termDock.SetPanel(termdock.PanelBoard, app.boardBrowser)
	}
	options := make([]ui.FormOption, 0)
	for _, root := range app.AgentMgr.Orchestrators() {
		options = append(options, ui.FormOption{ID: root.ID, Label: workspace.AgentDisplayLabel(root)})
	}
	app.boardBrowser.SetRoots(options)
	app.termDockHeight = defaultWorkspacePanelHeight(termdock.PanelBoard)
	if height := app.workspacePanelHeights[termdock.PanelBoard]; height > 0 {
		app.termDockHeight = height
	}
	app.termDock.ShowPanel(termdock.PanelBoard)
	app.setSessionDeckFocused(false)
	app.setTermDockFocused(true)
	app.FocusStack.Clear()
	return app.boardRequestCmd(app.boardBrowser.Request("refresh"))
}

func (app *App) toggleBoardBrowser() tea.Cmd {
	if app.termDock != nil && app.termDock.IsOpen() && app.termDock.ActivePanel() == termdock.PanelBoard {
		app.hideWorkspacePanel()
		return nil
	}
	return app.openBoardBrowser()
}

func (app *App) boardRequestCmd(request boardbrowser.Request) tea.Cmd {
	owner, generation := &workspace.Workspace{RootPath: app.RootPath, WorkspaceDir: app.WorkspaceDir, WorkspaceDB: app.WorkspaceDB, Capabilities: app.Capabilities, Board: app.Board}, app.workspaceSwitchGen.Load()
	workspaceOwner := app.Workspace
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		var err error
		if request.Operation == "join" {
			coordinator := ""
			if len(request.Roots) > 0 {
				coordinator = request.Roots[0]
			}
			err = workspaceOwner.JoinCollaboration(ctx, request.BoardID, request.Roots, coordinator)
		}
		if request.Operation == "mark-read" {
			err = owner.MarkBoardRead(ctx, request.BoardID, request.Through)
		}
		var snapshot workspace.BoardSnapshot
		if err == nil {
			snapshot, err = owner.BoardSnapshot(ctx, request.BoardID, request.Query, request.Thread, request.Since)
		}
		return boardResultMsg{generation: generation, request: request, snapshot: snapshot, err: err}
	}
}

func (app *App) handleBoardAction(action ui.Action) (tea.Cmd, bool) {
	if !strings.HasPrefix(action.ID, "board.") || app.boardBrowser == nil {
		return nil, false
	}
	return app.boardRequestCmd(app.boardBrowser.Request(strings.TrimPrefix(action.ID, "board."))), true
}

func (app *App) applyBoardResult(message boardResultMsg) {
	if message.generation != app.workspaceSwitchGen.Load() || message.request.Target != app.boardBrowser {
		return
	}
	app.boardBrowser.Apply(message.request.Sequence, message.snapshot, message.err)
}
