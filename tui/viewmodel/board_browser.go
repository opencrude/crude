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
	committed  bool
}

func (app *App) openBoardBrowser() tea.Cmd {
	if app.termDock == nil {
		return nil
	}
	if app.boardBrowser == nil {
		app.boardBrowser = boardbrowser.New(app.Theme)
		app.termDock.SetPanel(termdock.PanelBoard, app.boardBrowser)
	}
	if app.revealPlacedPanel(termdock.PanelBoard) {
		return app.boardRequestCmd(app.boardBrowser.Request("refresh"))
	}
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
	if app.termDock != nil && app.placementOf(termdock.PanelBoard) == placeDock && app.termDock.IsOpen() && app.termDock.ActivePanel() == termdock.PanelBoard {
		app.hideWorkspacePanel()
		return nil
	}
	return app.openBoardBrowser()
}

func (app *App) boardRequestCmd(request boardbrowser.Request) tea.Cmd {
	owner, generation := app.ws, app.workspaceSwitchGen.Load()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		var err error
		committed := false
		if request.Operation == "mark-read" {
			err = owner.MarkBoardRead(ctx, request.Through)
			committed = err == nil
		}
		var snapshot workspace.BoardSnapshot
		if err == nil {
			snapshot, err = owner.BoardSnapshot(ctx, request.Query, request.Thread, request.Since, request.Snapshot)
		}
		return boardResultMsg{generation: generation, request: request, snapshot: snapshot, err: err, committed: committed}
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
