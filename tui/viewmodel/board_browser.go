package viewmodel

import (
	"context"
	"errors"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/core/coreprotocol"
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

func (m *App) ensureBoardBrowser() *boardbrowser.Screen {
	if m.boardBrowser == nil {
		m.boardBrowser = boardbrowser.New(m.Theme)
	}
	return m.boardBrowser
}

func (m *App) openBoardBrowser() tea.Cmd {
	if m.termDock == nil {
		return nil
	}
	m.termDock.SetPanel(termdock.PanelBoard, m.ensureBoardBrowser())
	if m.revealPlacedPanel(termdock.PanelBoard) {
		return m.boardRequestCmd(m.boardBrowser.Request("refresh"))
	}
	m.termDockHeight = defaultWorkspacePanelHeight(termdock.PanelBoard)
	if height := m.workspacePanelHeights[termdock.PanelBoard]; height > 0 {
		m.termDockHeight = height
	}
	m.termDock.ShowPanel(termdock.PanelBoard)
	m.setSessionDeckFocused(false)
	m.setTermDockFocused(true)
	m.FocusStack.Clear()
	return m.boardRequestCmd(m.boardBrowser.Request("refresh"))
}

func (m *App) toggleBoardBrowser() tea.Cmd {
	if m.termDock != nil && m.placementOf(termdock.PanelBoard) == placeDock && m.termDock.IsOpen() && m.termDock.ActivePanel() == termdock.PanelBoard {
		m.hideWorkspacePanel()
		return nil
	}
	return m.openBoardBrowser()
}

func (m *App) boardRequestCmd(request boardbrowser.Request) tea.Cmd {
	owner, generation := m.ws, m.workspaceSwitchGen.Load()
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

func (m *App) handleBoardAction(action ui.Action) (tea.Cmd, bool) {
	if !strings.HasPrefix(action.ID, "board.") || m.boardBrowser == nil {
		return nil, false
	}
	return m.boardRequestCmd(m.boardBrowser.Request(strings.TrimPrefix(action.ID, "board."))), true
}

func (m *App) applyBoardResult(message boardResultMsg) {
	if message.generation != m.workspaceSwitchGen.Load() || message.request.Target != m.boardBrowser {
		return
	}
	var fault *coreprotocol.Fault
	if errors.As(message.err, &fault) && fault.Code == "board_failed" {
		// This is the latest-page topic's own warning. Do not turn it
		// into an action error that could outlive the next publication.
		m.boardBrowser.FinishUnavailableTopicRead(message.request.Sequence)
		return
	}
	m.boardBrowser.Apply(message.request.Sequence, message.snapshot, message.err)
}
