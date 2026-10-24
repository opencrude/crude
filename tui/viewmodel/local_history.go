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

	"crude/coreclient"
	"crude/coreprotocol"
	"crude/fileio"
	"crude/localhistory"
	"crude/shellport"
	historyscreen "crude/tui/screens/localhistory"
	"crude/tui/ui"
	"crude/tui/wm"
	ttypes "crude/types"
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

func (m *App) openLocalHistory() tea.Cmd { return m.openLocalHistoryForMessage("", "", "") }

func (m *App) openLocalHistoryForPath(path string) tea.Cmd {
	cmd := m.openLocalHistoryForMessage("", "", "")
	if cmd == nil || m.ws.LocalHistory() == nil {
		return cmd
	}
	rel, err := filepath.Rel(m.ws.RootPath(), path)
	if err != nil {
		return cmd
	}
	rel = filepath.ToSlash(rel)
	return func() tea.Msg {
		records, err := m.ws.LocalHistory().FileHistory(rel, 500)
		if err != nil {
			return historyscreen.TimelineMsg{Error: err.Error()}
		}
		stats, err := m.ws.LocalHistory().Stats()
		if err != nil {
			return historyscreen.TimelineMsg{Error: err.Error()}
		}
		return historyscreen.TimelineMsg{Records: records, Stats: stats, Paths: []string{rel}}
	}
}

func (m *App) openLatestHistoryCmd(agentID string) tea.Cmd {
	if m.ActiveSession == nil || m.ws.HistoryService() == nil {
		return func() tea.Msg { return historyLatestMsg{Error: "No active session history"} }
	}
	if agentID == "" {
		return func() tea.Msg { return historyLatestMsg{Error: "No active agent history"} }
	}
	sessionID := m.ActiveSession.SessionID
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

func (m *App) openLocalHistoryForMessage(sessionID, messageID, agentID string) tea.Cmd {
	if m.ws == nil || m.ws.HistoryService() == nil {
		if m.Feedback != nil {
			m.Feedback.NotifyError("Local history is unavailable")
		}
		return nil
	}
	m.overlayHost.Close(ui.OverlayLocalHistory)
	screen := historyscreen.New(m.Theme)
	m.historyScreen = screen
	win := m.newLocalHistoryWindow(screen)
	wm.CenterOnScreen(win, m.width, m.height)
	m.overlayHost.Open(ui.OverlayLocalHistory, win, ui.ModalDialogPolicy(), func() { m.historyScreen = nil })
	if operation := m.historyOperation; operation != nil {
		operation.screen = screen
		if !operation.busy {
			screen.Handle(ui.EventContext{}, ui.CustomEvent(operation.state))
		}
		return nil // Never replace unresolved state with a fresh Apply preview.
	}
	if messageID != "" {
		return func() tea.Msg {
			var plan localhistory.RestorePlan
			var err error
			if agentID == "" {
				plan, err = m.ws.HistoryService().PlanMessageRestore(context.Background(), sessionID, messageID)
			} else {
				plan, err = m.ws.HistoryService().PlanAgentRestore(context.Background(), sessionID, messageID, agentID)
			}
			if err != nil {
				return historyscreen.PlanMsg{Error: err.Error()}
			}
			return historyscreen.PlanMsg{Plan: plan}
		}
	}
	return func() tea.Msg {
		records, err := m.ws.LocalHistory().Timeline(500)
		if err != nil {
			return historyscreen.TimelineMsg{Error: err.Error()}
		}
		stats, err := m.ws.LocalHistory().Stats()
		if err != nil {
			return historyscreen.TimelineMsg{Error: err.Error()}
		}
		return historyscreen.TimelineMsg{Records: records, Stats: stats}
	}
}

func (m *App) openLocalHistoryForSnapshot(snapshotID string) tea.Cmd {
	if snapshotID == "" || m.openLocalHistoryForMessage("", "", "") == nil {
		return nil
	}
	return m.historyPreviewCmd(historyscreen.PreviewRequest{SnapshotID: snapshotID})
}

func (m *App) newLocalHistoryWindow(screen *historyscreen.Screen) *wm.FloatingWindow {
	w, h := fitFloatingSize(m.width, m.height, 82, 28, 58, 16)
	minW, minH := min(58, w), min(16, h)
	frame := ui.NewWindowFrameWidget(m.Theme, ui.NewID(ui.RootID, "local-history-frame"), ui.FrameWindow, "Local History", screen, nil, true, w, h)
	win := wm.NewFloatingWindow(string(ui.OverlayLocalHistory), "Local History", frame, w, h,
		wm.WithDecoration(true), wm.WithMovable(true), wm.WithClosable(true), wm.WithFocusable(true), wm.WithResizable(true), wm.WithModal(true), wm.WithMinSize(minW, minH), wm.WithMaxSize(max(w, m.width-2), max(h, m.height-2)), wm.WithClickOutside(wm.ClickOutsideIgnore))
	return win
}

func (m *App) openHistoryCandidates(msg historyCandidatesMsg) tea.Cmd {
	if m.openLocalHistoryForMessage("", "", "") == nil {
		return nil
	}
	return func() tea.Msg {
		timeline, err := m.ws.LocalHistory().Timeline(10000)
		if err != nil {
			return historyscreen.TimelineMsg{Error: err.Error()}
		}
		byID := make(map[string]localhistory.SnapshotRecord, len(timeline))
		for _, record := range timeline {
			byID[record.ID] = record
		}
		records := make([]localhistory.SnapshotRecord, 0, len(msg.Scopes))
		refs := make(map[string]historyscreen.MessageRef)
		for _, scope := range msg.Scopes {
			record := byID[scope.BeforeSnapshot]
			record.ID = "message-scope:" + msg.SessionID + ":" + scope.MessageID + ":" + scope.AgentID
			record.Label = "Message " + scope.MessageID + fmt.Sprintf(" · %d changed path(s)", len(scope.ChangedPaths))
			records = append(records, record)
			refs[record.ID] = historyscreen.MessageRef{SessionID: msg.SessionID, MessageID: scope.MessageID, AgentID: scope.AgentID}
		}
		stats, _ := m.ws.LocalHistory().Stats()
		return historyscreen.TimelineMsg{Records: records, Stats: stats, Messages: refs}
	}
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
func historySnapshotOptions(settings ttypes.LocalHistorySettings) localhistory.SnapshotOptions {
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
				return historyscreen.PlanMsg{Error: err.Error()}
			}
			return historyscreen.PlanMsg{Plan: plan}
		}
		var plan localhistory.RestorePlan
		var err error
		if len(request.Paths) > 0 {
			plan, err = m.ws.LocalHistory().PlanRestorePaths(context.Background(), request.SnapshotID, historySnapshotOptions(settings), request.Paths)
		} else {
			plan, err = m.ws.LocalHistory().PlanRestore(context.Background(), request.SnapshotID, historySnapshotOptions(settings))
		}
		if err != nil {
			return historyscreen.PlanMsg{Error: err.Error()}
		}
		return historyscreen.PlanMsg{Plan: plan}
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
			msg.err = fmt.Errorf("original restore is %s; do not repeat", receipt.State)
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
		state.Error = msg.err.Error() + " · Check original result; do not repeat"
	case msg.requestID != "":
		state.RequestID = msg.requestID
		state.Error = msg.err.Error() + " · Check original result; do not repeat"
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
		m.Feedback.NotifyInfo(fmt.Sprintf("Restored %d path(s) · safety %s", len(result.Paths), shortHistoryID(result.SafetySnapshotID)))
	}
	return tea.Batch(cmds...)
}

func shortHistoryID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
