package viewmodel

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"

	tea "charm.land/bubbletea/v2"

	"crude/localhistory"
	historyscreen "crude/tui/screens/localhistory"
	"crude/tui/ui"
	"crude/tui/wm"
	ttypes "crude/types"
)

type historyLatestMsg struct {
	SessionID string
	MessageID string
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

func (m *App) openLocalHistory() tea.Cmd { return m.openLocalHistoryForMessage("", "") }

func (m *App) openLocalHistoryForPath(path string) tea.Cmd {
	cmd := m.openLocalHistoryForMessage("", "")
	if cmd == nil || m.LocalHistory == nil {
		return cmd
	}
	rel, err := filepath.Rel(m.RootPath, path)
	if err != nil {
		return cmd
	}
	rel = filepath.ToSlash(rel)
	return func() tea.Msg {
		records, err := m.LocalHistory.FileHistory(rel, 500)
		if err != nil {
			return historyscreen.TimelineMsg{Error: err.Error()}
		}
		stats, err := m.LocalHistory.Stats()
		if err != nil {
			return historyscreen.TimelineMsg{Error: err.Error()}
		}
		return historyscreen.TimelineMsg{Records: records, Stats: stats, Paths: []string{rel}}
	}
}

func (m *App) openLatestHistoryCmd() tea.Cmd {
	if m.ActiveSession == nil || m.HistoryService == nil {
		return func() tea.Msg { return historyLatestMsg{Error: "No active session history"} }
	}
	sessionID := m.ActiveSession.SessionID
	return func() tea.Msg {
		scopes, err := m.HistoryService.UndoableScopes(sessionID)
		if err != nil {
			return historyLatestMsg{Error: err.Error()}
		}
		if len(scopes) == 0 {
			return historyLatestMsg{Error: "No message with workspace changes to undo"}
		}
		latest := scopes[0]
		overlapping := overlappingLatestScopes(scopes)
		if len(overlapping) > 1 {
			return historyCandidatesMsg{SessionID: sessionID, Scopes: overlapping}
		}
		return historyLatestMsg{SessionID: sessionID, MessageID: latest.MessageID}
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

func (m *App) openLocalHistoryForMessage(sessionID, messageID string) tea.Cmd {
	if m.Workspace == nil || m.HistoryService == nil {
		if m.StatusBar != nil {
			m.StatusBar.SetError("Local history is unavailable")
		}
		return nil
	}
	m.overlayHost.Close(ui.OverlayLocalHistory)
	screen := historyscreen.New(m.Theme)
	m.historyScreen = screen
	w, h := min(82, max(1, m.width-4)), min(28, max(1, m.height-4))
	minW, minH := min(58, w), min(16, h)
	frame := ui.NewWindowFrameWidget(m.Theme, ui.NewID(ui.RootID, "local-history-frame"), ui.FrameWindow, "Local History", screen, nil, true, w, h)
	win := wm.NewFloatingWindow(string(ui.OverlayLocalHistory), "Local History", frame, w, h,
		wm.WithDecoration(false), wm.WithFocusable(true), wm.WithResizable(true), wm.WithMinSize(minW, minH), wm.WithClickOutside(wm.ClickOutsideIgnore))
	wm.CenterOnScreen(win, m.width, m.height)
	m.overlayHost.Open(ui.OverlayLocalHistory, win, ui.ModalDialogPolicy(), func() { m.historyScreen = nil })
	if messageID != "" {
		return func() tea.Msg {
			plan, err := m.HistoryService.PlanMessageRestore(context.Background(), sessionID, messageID)
			if err != nil {
				return historyscreen.PlanMsg{Error: err.Error()}
			}
			return historyscreen.PlanMsg{Plan: plan}
		}
	}
	return func() tea.Msg {
		records, err := m.LocalHistory.Timeline(500)
		if err != nil {
			return historyscreen.TimelineMsg{Error: err.Error()}
		}
		stats, err := m.LocalHistory.Stats()
		if err != nil {
			return historyscreen.TimelineMsg{Error: err.Error()}
		}
		return historyscreen.TimelineMsg{Records: records, Stats: stats}
	}
}

func (m *App) openHistoryCandidates(msg historyCandidatesMsg) tea.Cmd {
	if m.openLocalHistoryForMessage("", "") == nil {
		return nil
	}
	return func() tea.Msg {
		timeline, err := m.LocalHistory.Timeline(10000)
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
			record.Label = "Message " + scope.MessageID + fmt.Sprintf(" · %d changed path(s)", len(scope.ChangedPaths))
			records = append(records, record)
			refs[record.ID] = historyscreen.MessageRef{SessionID: msg.SessionID, MessageID: scope.MessageID}
		}
		stats, _ := m.LocalHistory.Stats()
		return historyscreen.TimelineMsg{Records: records, Stats: stats, Messages: refs}
	}
}

func (m *App) verifyHistoryCmd() tea.Cmd {
	return func() tea.Msg {
		if m.LocalHistory == nil {
			return historyOperationMsg{Error: "Local history unavailable"}
		}
		if err := m.LocalHistory.VerifyAll(context.Background()); err != nil {
			return historyOperationMsg{Error: err.Error()}
		}
		return historyOperationMsg{Label: "Local history verified"}
	}
}
func (m *App) pruneHistoryCmd() tea.Cmd {
	settings := m.LoadAppSettings().LocalHistory
	return func() tea.Msg {
		if m.LocalHistory == nil {
			return historyOperationMsg{Error: "Local history unavailable"}
		}
		result, err := m.LocalHistory.Prune(context.Background(), localhistory.RetentionPolicy{KeepHours: settings.RetentionKeepHours, KeepDaily: settings.RetentionKeepDaily, MaxSnapshots: settings.RetentionMaxSnapshots, BudgetBytes: int64(settings.StorageBudgetMB) * 1024 * 1024})
		if err != nil {
			return historyOperationMsg{Error: err.Error()}
		}
		return historyOperationMsg{Label: fmt.Sprintf("Pruned %d snapshots and %d objects", result.RemovedSnapshots, result.RemovedObjects)}
	}
}
func (m *App) captureHistoryCmd() tea.Cmd {
	settings := m.LoadAppSettings().LocalHistory
	return func() tea.Msg {
		if m.LocalHistory == nil {
			return historyOperationMsg{Error: "Local history unavailable"}
		}
		_, changed, err := m.LocalHistory.SnapshotIfChanged(context.Background(), localhistory.SnapshotOptions{Kind: "manual", Label: "Manual snapshot", DisableGitIgnore: !settings.UseGitIgnoreValue(), ExtraIgnorePatterns: append([]string(nil), settings.ExtraIgnorePatterns...), MaxFileSize: int64(settings.MaxFileSizeMB) * 1024 * 1024})
		if err != nil {
			return historyOperationMsg{Error: err.Error()}
		}
		if !changed {
			return historyOperationMsg{Label: "Workspace already matches the latest snapshot"}
		}
		return historyOperationMsg{Label: "Local history snapshot created"}
	}
}
func (m *App) openHistoryLocationCmd() tea.Cmd {
	if m.LocalHistory == nil {
		return func() tea.Msg { return historyOperationMsg{Error: "Local history unavailable"} }
	}
	path := m.LocalHistory.Root()
	return func() tea.Msg {
		var cmd *exec.Cmd
		switch runtime.GOOS {
		case "darwin":
			cmd = exec.Command("open", path)
		case "windows":
			cmd = exec.Command("explorer.exe", path)
		default:
			cmd = exec.Command("xdg-open", path)
		}
		if err := cmd.Run(); err != nil {
			return historyOperationMsg{Error: "Open history folder: " + err.Error()}
		}
		return historyOperationMsg{Label: "Opened local history folder"}
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
	if m.LocalHistory == nil {
		return nil
	}
	settings := m.LoadAppSettings().LocalHistory
	return func() tea.Msg {
		if request.MessageID != "" {
			plan, err := m.HistoryService.PlanMessageRestore(context.Background(), request.SessionID, request.MessageID)
			if err != nil {
				return historyscreen.PlanMsg{Error: err.Error()}
			}
			return historyscreen.PlanMsg{Plan: plan}
		}
		var plan localhistory.RestorePlan
		var err error
		if len(request.Paths) > 0 {
			plan, err = m.LocalHistory.PlanRestorePaths(context.Background(), request.SnapshotID, historySnapshotOptions(settings), request.Paths)
		} else {
			plan, err = m.LocalHistory.PlanRestore(context.Background(), request.SnapshotID, historySnapshotOptions(settings))
		}
		if err != nil {
			return historyscreen.PlanMsg{Error: err.Error()}
		}
		return historyscreen.PlanMsg{Plan: plan}
	}
}

func (m *App) historyApplyCmd(request historyscreen.ApplyRequest) tea.Cmd {
	if m.HistoryService == nil {
		return nil
	}
	for _, choice := range request.Choices {
		if m.historyPathDirty(choice.Path) {
			return func() tea.Msg {
				return historyscreen.AppliedMsg{Error: "Save or discard unsaved editor changes before restoring " + choice.Path}
			}
		}
	}
	return func() tea.Msg {
		result, err := m.ApplyHistoryRestoreChoices(context.Background(), request.PlanID, request.Choices)
		if err != nil {
			return historyscreen.AppliedMsg{Error: err.Error()}
		}
		warning := ""
		if err := m.AppendHistoryRestoreEvent(result); err != nil {
			warning = "conversation event failed: " + err.Error()
		}
		return historyscreen.AppliedMsg{Result: result, Warning: warning}
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
		resolved, err := filepath.Rel(m.RootPath, path)
		if err == nil && filepath.ToSlash(resolved) == rel {
			return true
		}
	}
	return false
}

func (m *App) applyHistoryRefresh(result localhistory.RestoreResult) tea.Cmd {
	if m.EditorState != nil {
		for path, tab := range m.EditorState.Tabs {
			if tab == nil || tab.Editor == nil || tab.Editor.Modified() {
				continue
			}
			rel, err := filepath.Rel(m.RootPath, path)
			if err == nil && slices.Contains(result.Paths, filepath.ToSlash(rel)) {
				if err := tab.Editor.Reload(); err != nil && m.Logger != nil {
					m.Logger.Warn("HISTORY", "editor_reload_failed", "file", path, "error", err.Error())
				}
			}
		}
	}
	var cmds []tea.Cmd
	if m.Tree != nil && m.Tree.Model != nil {
		cmds = append(cmds, m.Tree.Model.RefreshFromDisk())
		if cmd := m.Tree.Model.RefreshGitStatus(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	if m.StatusBar != nil {
		m.StatusBar.SetInfo(fmt.Sprintf("Restored %d path(s) · safety %s", len(result.Paths), shortHistoryID(result.SafetySnapshotID)))
	}
	return tea.Batch(cmds...)
}

func shortHistoryID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
