package viewmodel

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/screens/gitreview"
	"crude/tui/screens/sourcecontrol"
	"crude/tui/ui"
	"crude/tui/wm"
	types "crude/types"
	"crude/workspace"
)

type gitDiscardPreviewMsg struct {
	generation uint64
	sequence   uint64
	change     workspace.GitChange
	preview    workspace.GitDiscardPreview
	err        error
}

const defaultSourceControlTimeout = 30 * time.Second

type sourceControlResultMsg struct {
	gen         uint64
	snapshot    workspace.GitSnapshot
	err         error
	notice      string
	clearCommit bool
	// silent marks a watcher-driven reconciliation. Nobody asked for it, so it
	// never sets the panel's loading state, records a status operation, notifies
	// a failure, or touches the commit field — and it publishes only a snapshot
	// that actually differs from the painted one.
	silent bool
}

func (m *App) sourceControlContext() (context.Context, context.CancelFunc) {
	timeout := m.sourceControlTimeout
	if timeout <= 0 {
		timeout = defaultSourceControlTimeout
	}
	return context.WithTimeout(context.Background(), timeout)
}

func (m *App) refreshSourceControl() tea.Cmd {
	if m.sourceControl == nil {
		return nil
	}
	if m.Git == nil {
		m.Git = workspace.NewGitService(m.RootPath)
	}
	m.sourceControl.SetLoading(true)
	gen := m.gitRefreshGen.Add(1)
	m.recordGitOperation(gen, "Refresh source control")
	service := m.Git
	m.gitReadsInFlight++
	return func() tea.Msg {
		ctx, cancel := m.sourceControlContext()
		defer cancel()
		snapshot, err := service.Snapshot(ctx)
		return sourceControlResultMsg{gen: gen, snapshot: snapshot, err: err}
	}
}

func (m *App) sourceControlMutation(
	notice string,
	clearCommit bool,
	mutate func(context.Context, *workspace.GitService) error,
) tea.Cmd {
	if m.Git == nil {
		m.Git = workspace.NewGitService(m.RootPath)
	}
	if m.sourceControl != nil {
		m.sourceControl.SetLoading(true)
	}
	gen := m.gitRefreshGen.Add(1)
	m.recordGitOperation(gen, firstNonempty(notice, "Update source control"))
	service := m.Git
	m.gitReadsInFlight++
	return func() tea.Msg {
		ctx, cancel := m.sourceControlContext()
		defer cancel()
		if err := mutate(ctx, service); err != nil {
			return sourceControlResultMsg{gen: gen, err: err}
		}
		snapshot, err := service.Snapshot(ctx)
		return sourceControlResultMsg{
			gen: gen, snapshot: snapshot, err: err,
			notice: notice, clearCommit: clearCommit,
		}
	}
}

// startSourceControlMutation is used by context-menu callbacks, whose action
// signature cannot return a tea.Cmd. Completion still re-enters the update
// goroutine through the workspace event bus.
func (m *App) startSourceControlMutation(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	go func() {
		msg := cmd()
		if msg != nil && m.eventBus != nil {
			m.eventBus.Send(msg)
		}
	}()
}

func (m *App) handleSourceControlAction(action ui.Action) (tea.Cmd, bool) {
	switch action.ID {
	case sourcecontrol.ActionRefresh:
		return m.refreshSourceControl(), true
	case sourcecontrol.ActionOpenChange:
		change, ok := action.Payload.(workspace.GitChange)
		if !ok || change.AbsolutePath == "" {
			return nil, true
		}
		return m.openGitReview(gitreview.ModeChanges, &change), true
	case sourcecontrol.ActionReview:
		return m.openGitReview(gitreview.ModeChanges, nil), true
	case sourcecontrol.ActionHistory:
		return m.openGitReview(gitreview.ModeHistory, nil), true
	case sourcecontrol.ActionBranches:
		return m.openGitReview(gitreview.ModeBranches, nil), true
	case "sourcecontrol.more":
		items := []ui.ContextMenuItem{
			{Label: "History", Action: func() { m.startSourceControlMutation(m.openGitReview(gitreview.ModeHistory, nil)) }},
			{Label: "Branches", Action: func() { m.startSourceControlMutation(m.openGitReview(gitreview.ModeBranches, nil)) }},
			{Label: "Refresh", Action: func() { m.startSourceControlMutation(m.refreshSourceControl()) }},
		}
		m.openContextMenuState(tea.Mouse{X: m.frameLeftCol() + m.activityChromeWidth(), Y: m.shellTopRow() + 3}, &ui.ContextMenuState{Title: "Source Control", Sections: []ui.ContextMenuSection{{Items: items}}})
		return nil, true
	}
	return nil, false
}

func (m *App) applySourceControlResult(msg sourceControlResultMsg) tea.Cmd {
	// Every read settles exactly one in-flight slot. A read invalidated by a
	// workspace boundary no longer owns a slot: that boundary resets the count
	// itself (beginWorkspaceSwitch / finishWorkspaceSwitch).
	if msg.gen != 0 && msg.gen == m.gitRefreshGen.Load() && m.gitReadsInFlight > 0 {
		m.gitReadsInFlight--
	}
	if msg.gen == 0 || msg.gen != m.gitRefreshGen.Load() || m.sourceControl == nil {
		return nil
	}
	m.gitSnapshotAt = time.Now()
	if msg.err != nil {
		if msg.silent {
			// Unrequested reads never report: a workspace without a repository,
			// or a transient failure, must not raise an issue or toast. The last
			// known projection stays painted.
			if m.Logger != nil {
				m.Logger.Debug("GIT", "snapshot_reconcile_failed", "error", msg.err.Error())
			}
			return nil
		}
		m.finishGitOperation(msg.gen, types.OperationFailed, msg.err.Error())
		m.Status.SetIssue(types.IssueSnapshot{ID: "git", Owner: types.ResourceRef{Kind: types.ResourceGit, ID: m.RootPath}, Severity: types.SeverityError, Summary: "Source control failed", Detail: msg.err.Error()})
		m.sourceControl.SetError(msg.err)
		if m.Feedback != nil {
			m.Feedback.NotifyError("Source control: " + msg.err.Error())
		}
		return nil
	}
	if msg.silent {
		if sameGitSnapshot(m.sourceControl.Snapshot(), msg.snapshot) {
			return nil // already painted: no list rebuild, no gutter invalidation
		}
		m.presentGitSnapshot(msg.snapshot)
		return nil
	}
	m.finishGitOperation(msg.gen, types.OperationSucceeded, "")
	m.Status.ResolveIssue("git")
	m.presentGitSnapshot(msg.snapshot)
	if msg.clearCommit && m.gitReview != nil {
		m.gitReview.ClearCommitMessage()
	}
	if msg.notice != "" && m.Feedback != nil {
		m.Feedback.NotifyInfo(msg.notice)
	}
	if m.Tree != nil && m.Tree.Model != nil {
		return m.Tree.Model.RefreshGitStatus()
	}
	return nil
}

// presentGitSnapshot publishes one repository projection to every surface that
// derives from it: the Source Control panel, the header identity, the activity
// badge, and the editor's gutter/blame annotation (a moved HEAD invalidates it).
func (m *App) presentGitSnapshot(snapshot workspace.GitSnapshot) {
	if m == nil || m.sourceControl == nil {
		return
	}
	m.sourceControl.SetSnapshot(snapshot)
	if m.gitReview != nil {
		m.gitReview.SetSnapshot(snapshot)
	}
	if m.EditorState != nil && m.EditorState.Editor != nil && m.EditorState.Editor.IsOpen() {
		m.markEditorGitStaleNow()
	}
	if m.activityBar != nil {
		paths := make(map[string]bool)
		for _, change := range snapshot.Changes {
			paths[change.Path] = true
		}
		m.activityBar.SetBadge(activityGit, len(paths))
	}
}

func (m *App) recordGitOperation(generation uint64, label string) {
	if m.Status == nil {
		return
	}
	m.Status.SetOperation(types.OperationSnapshot{
		ID: fmt.Sprintf("git:%d", generation), Owner: types.ResourceRef{Kind: types.ResourceGit, ID: m.RootPath},
		Kind: "git", Label: label, State: types.OperationRunning, StartedAt: time.Now(), Cancellable: true,
	})
}

func (m *App) finishGitOperation(generation uint64, state types.OperationState, errorText string) {
	if m.Status == nil {
		return
	}
	m.Status.FinishOperation(fmt.Sprintf("git:%d", generation), state, errorText)
}

func (m *App) discardGitChange(preview workspace.GitDiscardPreview) tea.Cmd {
	owner := m.Workspace
	return m.sourceControlMutation(
		"Discarded "+filepath.Base(preview.Comparison.Path)+" · recover through Local History", false,
		func(ctx context.Context, _ *workspace.GitService) error {
			_, err := owner.DiscardGitReviewed(ctx, preview)
			return err
		},
	)
}

func (m *App) startDiscardGitChange(change workspace.GitChange) {
	if change.Area == workspace.GitAreaUntracked {
		m.Feedback.NotifyError("Discard never deletes untracked files")
		return
	}
	copy := change
	m.GitDiscardPending = &copy
	m.gitDiscardPreview = nil
	m.gitDiscardSequence++
	sequence, generation, owner := m.gitDiscardSequence, m.workspaceSwitchGen.Load(), m.Workspace
	m.startSourceControlMutation(func() tea.Msg {
		ctx, cancel := m.sourceControlContext()
		defer cancel()
		preview, err := owner.PreviewGitDiscard(ctx, change)
		return gitDiscardPreviewMsg{generation: generation, sequence: sequence, change: change, preview: preview, err: err}
	})
	m.TreeDeletePending = ""
	m.TreeDeleteChoice = 1 // safe default: Cancel
	frame := m.buildGitDiscardFrame(copy)
	w, h := frame.Measure(80, 40)
	fw := wm.NewFloatingWindow(string(ui.OverlayTreeDelete), "", frame, w, h,
		wm.WithDecoration(false),
		wm.WithMovable(true), // drag by frame title row
		wm.WithResizable(false),
		wm.WithClosable(false),
		wm.WithModal(true),
		wm.WithFocusable(true),
		wm.WithClickOutside(wm.ClickOutsideIgnore),
	)
	wm.CenterOnScreen(fw, m.width, m.height)
	m.overlayHost.Open(ui.OverlayTreeDelete, fw, ui.ModalDialogPolicy(), func() {
		m.TreeDeletePending = ""
		m.GitDiscardPending = nil
		m.gitDiscardPreview = nil
		m.gitDiscardSequence++
	})
}

func (m *App) buildGitDiscardFrame(change workspace.GitChange) *ui.ComponentWidget {
	detail := "Preparing an exact disk/index preview. Discard stays disabled until reviewed."
	if m.gitDiscardPreview != nil {
		comparison := m.gitDiscardPreview.Comparison
		detail = fmt.Sprintf("%s\n%s → %s\nReviewed fingerprint: %s\nA mandatory Local History safety snapshot preserves the removed bytes.\nRecover through Local History. Changed file/index/HEAD aborts.", change.Path, comparison.TargetLabel, comparison.BaseLabel, comparison.Fingerprint)
		before := strings.SplitN(comparison.After, "\n", 4)
		after := strings.SplitN(comparison.Before, "\n", 4)
		detail += "\nDisk: " + strings.Join(before[:min(3, len(before))], " / ") + "\nIndex: " + strings.Join(after[:min(3, len(after))], " / ")
	}
	return ui.BuildConfirmFrame(m.Theme, ui.ConfirmFrameSpec{
		Title:          "Discard Changes",
		Question:       fmt.Sprintf("Discard working-tree changes in %q?", filepath.Base(change.Path)),
		Detail:         detail,
		DetailMaxLines: 10,
		Severity:       ui.ConfirmPromptDanger,
		Buttons: []ui.ConfirmButtonSpec{
			{Label: "Discard", ActionID: ui.ConfirmActionDiscard},
			{Label: "Cancel", ActionID: ui.ConfirmActionCancel},
		},
		FocusedIndex: m.TreeDeleteChoice,
		Width:        64,
	})
}
