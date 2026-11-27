package viewmodel

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/core/coreclient"
	"crude/core/coreprotocol"
	"crude/core/shellport"
	historyscreen "crude/tui/screens/localhistory"
	"crude/tui/screens/workspacehub"
	"crude/tui/ui"
	"crude/tui/wm"
	"crude/types/setting"
	"crude/workspace/fileio"
	"crude/workspace/localhistory"
)

type historyLatestMsg struct {
	SessionID string
	MessageID string
	AgentID   string
	Error     string
}
type historyCandidatesMsg struct {
	SessionID string
	Scopes    []localhistory.MessageScope
	Error     string
}
type historyOperationMsg struct {
	Label string
	Error string
}

type historyOperation struct {
	owner      shellport.Workspace
	generation uint64
	screen     *historyscreen.Screen
	busy       bool
	state      historyscreen.AppliedMsg
	cancel     context.CancelFunc
	run        tea.Cmd
	requestID  string
}

type historyApplyResultMsg struct {
	operation *historyOperation
	result    localhistory.RestoreResult
	err       error
	requestID string
}

// openLocalHistory shows the Local History tab of the Workspace panel with
// every recorded change.
func (m *App) openLocalHistory() tea.Cmd {
	if m.historyPanel == nil {
		return nil
	}
	m.historyPanel.SetMode(historyscreen.ModeTimeline)
	if m.historyPanel.Path() != "" {
		m.historyPanel.BeginTimeline("")
	}
	return m.openWorkspaceHub(workspacehub.TabHistory)
}

// openLocalHistoryForPath shows the recorded changes of one file or folder.
func (m *App) openLocalHistoryForPath(path string) tea.Cmd {
	if m.historyPanel == nil {
		return nil
	}
	rel, err := filepath.Rel(m.ws.RootPath(), path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		m.Feedback.NotifyError("Local History keeps only files in this workspace")
		return nil
	}
	if rel = filepath.ToSlash(rel); rel == "." {
		rel = ""
	}
	m.historyPanel.SetMode(historyscreen.ModeTimeline)
	m.historyPanel.BeginTimeline(rel)
	return m.openWorkspaceHub(workspacehub.TabHistory)
}

// openAgentReceipts shows one agent's recorded file edits.
func (m *App) openAgentReceipts(session, agent string) tea.Cmd {
	if m.historyPanel == nil {
		return nil
	}
	name := "Agent"
	if node := m.ws.AgentMgr().Get(agent); node != nil {
		name = m.agentLabel(node)
	}
	m.historyPanel.SetMode(historyscreen.ModeAgent)
	m.historyPanel.SetAgent(historyscreen.AgentRef{SessionID: session, AgentID: agent, Name: name})
	return tea.Batch(m.openWorkspaceHub(workspacehub.TabHistory), m.readHistoryReceipts(localhistory.AgentReceiptQuery{SessionID: session, AgentID: agent}))
}

// openLatestHistoryCmd previews undoing the agent's latest changes in
// sessionID.
func (m *App) openLatestHistoryCmd(sessionID, agentID string) tea.Cmd {
	if sessionID == "" || m.ws.HistoryService() == nil {
		return func() tea.Msg { return historyLatestMsg{Error: "No active session history"} }
	}
	if agentID == "" {
		return func() tea.Msg { return historyLatestMsg{Error: "No active agent history"} }
	}
	return func() tea.Msg {
		scopes, err := m.ws.HistoryService().AgentUndoableScopes(sessionID, agentID)
		if err != nil {
			return historyLatestMsg{Error: err.Error()}
		}
		if len(scopes) == 0 {
			return historyLatestMsg{Error: "No snapshot-verified file changes from the active agent to undo"}
		}
		latest := scopes[0]
		overlapping := overlappingLatestScopes(scopes)
		if len(overlapping) > 1 {
			return historyCandidatesMsg{SessionID: sessionID, Scopes: overlapping}
		}
		return historyLatestMsg{SessionID: sessionID, MessageID: latest.MessageID, AgentID: agentID}
	}
}

func overlappingLatestScopes(scopes []localhistory.MessageScope) []localhistory.MessageScope {
	if len(scopes) == 0 {
		return nil
	}
	latest := scopes[0]
	var out []localhistory.MessageScope
	for _, scope := range scopes {
		if scope.OpenedAt <= latest.ClosedAt && scope.ClosedAt >= latest.OpenedAt {
			out = append(out, scope)
		}
	}
	return out
}

// openLocalHistoryForMessage previews undoing one message's changes.
func (m *App) openLocalHistoryForMessage(sessionID, messageID, agentID string) tea.Cmd {
	heading := "Undo the changes of this message?"
	if agentID != "" {
		name := "the agent"
		if node := m.ws.AgentMgr().Get(agentID); node != nil {
			name = m.agentLabel(node)
		}
		heading = "Undo " + name + "'s latest changes?"
	}
	return m.openRestorePreview(historyscreen.PreviewRequest{SessionID: sessionID, MessageID: messageID, AgentID: agentID, Heading: heading})
}

// openLocalHistoryForSnapshot previews returning to the files a snapshot
// kept, such as the copy made before a restore.
func (m *App) openLocalHistoryForSnapshot(snapshotID string) tea.Cmd {
	if snapshotID == "" {
		return nil
	}
	return m.openRestorePreview(historyscreen.PreviewRequest{SnapshotID: snapshotID, Heading: "Undo this restore?"})
}

// openRestorePreview opens the Restore preview dialog for request. An
// unresolved restore keeps the dialog on that outcome: a new preview never
// replaces it.
func (m *App) openRestorePreview(request historyscreen.PreviewRequest) tea.Cmd {
	if m.ws == nil || m.ws.HistoryService() == nil {
		if m.Feedback != nil {
			m.Feedback.NotifyError("Local History is off for this workspace")
		}
		return nil
	}
	screen := m.restoreScreen()
	if operation := m.historyOperation; operation != nil {
		operation.screen = screen
		if !operation.busy {
			screen.Handle(ui.EventContext{}, ui.CustomEvent(operation.state))
		}
		return nil
	}
	if m.historySafetyOperation != nil {
		return m.openHistorySafety()
	}
	screen.Begin(request)
	m.markRestoreDirty()
	return m.historyPreviewCmd(screen.Request())
}

// restoreScreen returns the open Restore preview dialog's body, opening the
// dialog when it is closed.
func (m *App) restoreScreen() *historyscreen.Screen {
	if m.historyScreen != nil && m.overlayHost.IsOpen(ui.OverlayLocalHistory) {
		m.closeHistorySafetyView()
		m.mountHistoryBody(m.historyScreen)
		return m.historyScreen
	}
	m.overlayHost.Close(ui.OverlayLocalHistory)
	screen := historyscreen.New(m.Theme)
	m.historyScreen = screen
	win := m.newRestoreWindow(screen)
	wm.CenterOnScreen(win, m.width, m.height)
	m.overlayHost.Open(ui.OverlayLocalHistory, win, ui.ModalDialogPolicy(), func() {
		m.closeHistorySafetyView()
		m.historyScreen = nil
	})
	if frame, ok := win.Content.(*ui.WindowFrameWidget); ok {
		frame.Focus(screen.PreferredFocus())
	}
	return screen
}

// deliverRestoreState gives a plan or a restore outcome to the open Restore
// preview. It goes to the dialog's body directly: delivery through keyboard
// focus would lose it while no control of a small dialog has focus yet.
func (m *App) deliverRestoreState(msg tea.Msg, outcome bool) {
	if m.historyScreen == nil || !m.overlayHost.IsOpen(ui.OverlayLocalHistory) {
		return
	}
	m.historyScreen.Handle(ui.EventContext{}, ui.CustomEvent(msg))
	m.focusRestoreDialog(outcome)
	m.markRestoreDirty()
}

// focusRestoreDialog gives the Restore preview keyboard focus once its
// controls are mounted: the file list with a plan, otherwise the buttons.
// A plan keeps a control the user already focused; an outcome replaces the
// list, so it always moves focus.
func (m *App) focusRestoreDialog(outcome bool) {
	fw := m.overlayHost.Window(ui.OverlayLocalHistory)
	if fw == nil || m.historyScreen == nil {
		return
	}
	frame, ok := fw.Content.(*ui.WindowFrameWidget)
	if !ok || frame.Body() != m.historyScreen {
		return
	}
	host := frame.Host()
	host.Layout()
	if current := host.Focused(); !outcome && current != ui.RootID && current != "" && host.Component(current) != nil {
		return
	}
	host.Focus(m.historyScreen.PreferredFocus())
}

func (m *App) markRestoreDirty() {
	if fw := m.overlayHost.Window(ui.OverlayLocalHistory); fw != nil {
		if frame, ok := fw.Content.(*ui.WindowFrameWidget); ok {
			frame.Host().MarkLayout()
		}
		fw.MarkContentDirty()
	}
	m.forceFrame()
}

// openHistorySafetyWindow shows the copies kept for reviewed edits in the
// same dialog.
func (m *App) openHistorySafetyWindow() tea.Cmd {
	if _, ok := m.ws.(shellport.HistorySafety); !ok {
		return nil
	}
	m.restoreScreen()
	return m.openHistorySafety()
}

func (m *App) newRestoreWindow(screen *historyscreen.Screen) *wm.FloatingWindow {
	w, h := fitFloatingSize(m.width, m.height, 112, 32, 60, 16)
	minW, minH := min(60, w), min(16, h)
	frame := ui.NewWindowFrameWidget(m.Theme, ui.NewID(ui.RootID, "local-history-frame"), ui.FrameWindow, "Restore preview", screen, nil, true, w, h)
	return wm.NewFloatingWindow(string(ui.OverlayLocalHistory), "Restore preview", frame, w, h,
		wm.WithDecoration(true), wm.WithMovable(true), wm.WithClosable(true), wm.WithFocusable(true), wm.WithResizable(true), wm.WithModal(true), wm.WithMinSize(minW, minH), wm.WithMaxSize(max(w, m.width-2), max(h, m.height-2)), wm.WithClickOutside(wm.ClickOutsideIgnore))
}

// openHistoryCandidates shows the Local History tab when several agent turns
// changed the same files, so the user picks the turn to undo.
func (m *App) openHistoryCandidates(msg historyCandidatesMsg) tea.Cmd {
	if m.historyPanel == nil || len(msg.Scopes) == 0 {
		return nil
	}
	m.historyPanel.SetMode(historyscreen.ModeTimeline)
	m.historyPanel.BeginTimeline("")
	m.historyPanel.SelectMessage(msg.SessionID, msg.Scopes[0].MessageID)
	m.Feedback.NotifyInfo("Several agent turns changed the same files. Select the turn to undo, then choose Undo changes.")
	return m.openWorkspaceHub(workspacehub.TabHistory)
}

func (m *App) captureHistoryCmd() tea.Cmd {
	settings := m.ws.LoadAppSettings().LocalHistory
	return func() tea.Msg {
		if m.ws.LocalHistory() == nil {
			return historyOperationMsg{Error: "Local history unavailable"}
		}
		_, changed, err := m.ws.LocalHistory().SnapshotIfChanged(context.Background(), localhistory.SnapshotOptions{Kind: "manual", Label: "Manual snapshot", DisableGitIgnore: !settings.UseGitIgnoreValue(), ExtraIgnorePatterns: append([]string(nil), settings.ExtraIgnorePatterns...), MaxFileSize: int64(settings.MaxFileSizeMB) * 1024 * 1024})
		if err != nil {
			return historyOperationMsg{Error: err.Error()}
		}
		if !changed {
			return historyOperationMsg{Label: "Workspace already matches the latest snapshot"}
		}
		return historyOperationMsg{Label: "Local history snapshot created"}
	}
}
func historySnapshotOptions(settings setting.LocalHistory) localhistory.SnapshotOptions {
	return localhistory.SnapshotOptions{
		DisableGitIgnore:    !settings.UseGitIgnoreValue(),
		ExtraIgnorePatterns: append([]string(nil), settings.ExtraIgnorePatterns...),
		MaxFileSize:         int64(settings.MaxFileSizeMB) * 1024 * 1024,
	}
}

func (m *App) historyPreviewCmd(request historyscreen.PreviewRequest) tea.Cmd {
	if m.ws.LocalHistory() == nil {
		return nil
	}
	settings := m.ws.LoadAppSettings().LocalHistory
	return func() tea.Msg {
		if request.MessageID != "" {
			var plan localhistory.RestorePlan
			var err error
			if request.AgentID == "" {
				plan, err = m.ws.HistoryService().PlanMessageRestore(context.Background(), request.SessionID, request.MessageID)
			} else {
				plan, err = m.ws.HistoryService().PlanAgentRestore(context.Background(), request.SessionID, request.MessageID, request.AgentID)
			}
			if err != nil {
				return historyscreen.PlanMsg{Error: err.Error(), Generation: request.Generation}
			}
			return historyscreen.PlanMsg{Plan: plan, Generation: request.Generation}
		}
		var plan localhistory.RestorePlan
		var err error
		if len(request.Paths) > 0 {
			plan, err = m.ws.LocalHistory().PlanRestorePaths(context.Background(), request.SnapshotID, historySnapshotOptions(settings), request.Paths)
		} else {
			plan, err = m.ws.LocalHistory().PlanRestore(context.Background(), request.SnapshotID, historySnapshotOptions(settings))
		}
		if err != nil {
			return historyscreen.PlanMsg{Error: err.Error(), Generation: request.Generation}
		}
		return historyscreen.PlanMsg{Plan: plan, Generation: request.Generation}
	}
}

func (m *App) historyApplyCmd(request historyscreen.ApplyRequest) tea.Cmd {
	if m.ws.HistoryService() == nil || m.historyOperation != nil {
		return nil
	}
	for _, choice := range request.Choices {
		if m.historyPathDirty(choice.Path) {
			return func() tea.Msg {
				return historyscreen.AppliedMsg{Error: "Save or discard unsaved editor changes before restoring " + choice.Path}
			}
		}
	}
	operation := &historyOperation{owner: m.ws, generation: m.workspaceSwitchGen.Load(), screen: m.historyScreen, busy: true}
	m.historyOperation = operation
	choices := slices.Clone(request.Choices)
	return m.historyCommand(operation, func(ctx context.Context) historyApplyResultMsg {
		result, err := operation.owner.ApplyHistoryRestoreChoices(ctx, request.PlanID, choices)
		return historyApplyResultMsg{operation: operation, result: result, err: err}
	})
}

// The original command owns its cancellation/join even if its modal is closed.
// Shutdown may observe the same once-guarded command, never replay its effect.
func (m *App) historyCommand(operation *historyOperation, work func(context.Context) historyApplyResultMsg) tea.Cmd {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	operation.cancel = cancel
	var once sync.Once
	var result historyApplyResultMsg
	operation.run = func() tea.Msg { once.Do(func() { defer cancel(); result = work(ctx) }); return result }
	return operation.run
}

func (m *App) historyCheckCmd(id string) tea.Cmd {
	operation := m.historyOperation
	if operation == nil || operation.busy || operation.requestID != id {
		return nil
	}
	receipts, ok := operation.owner.(shellport.HistoryRestoreReceipts)
	if !ok {
		return nil
	}
	operation.busy = true
	return m.historyCommand(operation, func(ctx context.Context) historyApplyResultMsg {
		msg := historyApplyResultMsg{operation: operation, requestID: id}
		receipt, err := receipts.HistoryRestoreReceipt(ctx, id)
		if err != nil {
			msg.err = err
			return msg
		}
		if receipt.RequestID != id || receipt.Method != coreprotocol.HistoryApply {
			msg.err = errors.New("original restore receipt identity does not match")
			return msg
		}
		if receipt.State != "completed" {
			msg.err = errors.New("the restore has not finished. Check again in a moment")
			return msg
		}
		if len(receipt.Response.Payload) > 0 {
			if err := coreprotocol.Decode(receipt.Response.Payload, &msg.result); err != nil {
				msg.err = err
				return msg
			}
		}
		msg.requestID = ""
		if receipt.Response.Error != nil {
			msg.err = receipt.Response.Error
			if strings.HasPrefix(receipt.Response.Error.Code, "outcome_") {
				msg.requestID = id
			} else if receipt.Response.Error.Code == "publication_failed" && msg.result.SafetySnapshotID != "" {
				msg.err = fileio.CommittedWarning(receipt.Response.Error.Message)
			}
		}
		return msg
	})
}

func (m *App) applyHistoryResult(msg historyApplyResultMsg) tea.Cmd {
	operation := msg.operation
	if operation == nil || m.historyOperation != operation || operation.owner != m.ws || operation.generation != m.workspaceSwitchGen.Load() {
		return nil
	}
	operation.busy = false
	state := historyscreen.AppliedMsg{Result: msg.result}
	var uncertain *coreclient.UncertainError
	switch {
	case errors.As(msg.err, &uncertain):
		state.RequestID = uncertain.RequestID
		state.Error = "Crude did not get the restore result. Press Enter to check it before you restore again."
	case msg.requestID != "":
		state.RequestID = msg.requestID
		state.Error = "Crude did not get the restore result. Press Enter to check it before you restore again."
	case msg.err != nil && msg.result.SafetySnapshotID != "":
		state.Warning = msg.err.Error()
		state.Incomplete = !fileio.WriteCommitted(msg.err)
	case msg.err != nil:
		state.Error = msg.err.Error()
	}
	operation.state, operation.requestID = state, state.RequestID
	if state.RequestID == "" {
		m.historyOperation = nil
	}
	if operation.screen == m.historyScreen && m.historyScreen != nil {
		return func() tea.Msg { return state }
	}
	// Closing a modal does not abandon a known effect or its safety evidence.
	if state.RequestID != "" {
		m.Feedback.NotifyError(state.Error)
		return nil
	}
	if state.Error != "" {
		m.Feedback.NotifyError(state.Error)
		return nil
	}
	return m.applyHistoryRefresh(state.Result)
}

func (m *App) stopHistoryOperation() {
	m.stopHistorySafetyOperation()
	if operation := m.historyOperation; operation != nil && operation.cancel != nil {
		operation.cancel()
		if operation.run != nil {
			operation.run()
		}
	}
}

func (m *App) historyPathDirty(rel string) bool {
	if m.EditorState == nil {
		return false
	}
	for path, tab := range m.EditorState.Tabs {
		if tab == nil || tab.Editor == nil || !tab.Editor.Modified() {
			continue
		}
		resolved, err := filepath.Rel(m.ws.RootPath(), path)
		if filepath.IsAbs(filepath.FromSlash(rel)) && filepath.Clean(path) == filepath.Clean(filepath.FromSlash(rel)) || err == nil && filepath.ToSlash(resolved) == rel {
			return true
		}
	}
	return false
}

func (m *App) applyHistoryRefresh(result localhistory.RestoreResult) tea.Cmd {
	var cmds []tea.Cmd
	if m.EditorState != nil {
		for path, tab := range m.EditorState.Tabs {
			if tab == nil || tab.Editor == nil || tab.Editor.Modified() {
				continue
			}
			rel, err := filepath.Rel(m.ws.RootPath(), path)
			if slices.Contains(result.Paths, filepath.ToSlash(filepath.Clean(path))) || err == nil && slices.Contains(result.Paths, filepath.ToSlash(rel)) {
				cmds = append(cmds, m.refreshEditorDiskCmd(tab.Editor))
			}
		}
	}
	if m.Tree != nil && m.Tree.Model != nil {
		cmds = append(cmds, m.Tree.Model.RefreshFromDisk())
		if cmd := m.Tree.Model.RefreshGitStatus(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	if m.Feedback != nil {
		m.Feedback.NotifyInfo(fmt.Sprintf("Restored %s. Undo it in Local History.", countNoun(len(result.Paths), "file")))
	}
	return tea.Batch(cmds...)
}
