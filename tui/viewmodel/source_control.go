package viewmodel

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/screens/sourcecontrol"
	"crude/tui/ui"
	"crude/tui/wm"
	types "crude/types"
	"crude/workspace"
)

const defaultSourceControlTimeout = 30 * time.Second

type sourceControlResultMsg struct {
	gen         uint64
	snapshot    workspace.GitSnapshot
	err         error
	notice      string
	clearCommit bool
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
		cmd, err := m.openEditorTab(change.AbsolutePath)
		if err != nil {
			m.Feedback.NotifyError("Open change: " + err.Error())
			return nil, true
		}
		return cmd, true
	case sourcecontrol.ActionCheckoutBranch:
		branch, ok := action.Payload.(workspace.GitBranchSummary)
		if !ok || branch.Name == "" || branch.Current {
			return nil, true
		}
		return m.sourceControlMutation(
			"Switched to "+branch.Name, false,
			func(ctx context.Context, service *workspace.GitService) error {
				return service.Checkout(ctx, branch.Name)
			},
		), true
	case sourcecontrol.ActionCommit:
		message, _ := action.Payload.(string)
		if message == "" {
			m.Feedback.NotifyError("Enter a commit message")
			return nil, true
		}
		return m.sourceControlMutation(
			"Committed changes", true,
			func(ctx context.Context, service *workspace.GitService) error {
				return service.Commit(ctx, message)
			},
		), true
	}
	return nil, false
}

func (m *App) applySourceControlResult(msg sourceControlResultMsg) tea.Cmd {
	if msg.gen == 0 || msg.gen != m.gitRefreshGen.Load() || m.sourceControl == nil {
		return nil
	}
	if msg.err != nil {
		m.finishGitOperation(msg.gen, types.OperationFailed, msg.err.Error())
		m.Status.SetIssue(types.IssueSnapshot{ID: "git", Owner: types.ResourceRef{Kind: types.ResourceGit, ID: m.RootPath}, Severity: types.SeverityError, Summary: "Source control failed", Detail: msg.err.Error()})
		m.sourceControl.SetError(msg.err)
		if m.Feedback != nil {
			m.Feedback.NotifyError("Source control: " + msg.err.Error())
		}
		return nil
	}
	m.finishGitOperation(msg.gen, types.OperationSucceeded, "")
	m.Status.ResolveIssue("git")
	m.sourceControl.SetSnapshot(msg.snapshot)
	if msg.clearCommit {
		m.sourceControl.ClearCommitMessage()
	}
	if m.activityBar != nil {
		m.activityBar.SetBadge(activityGit, len(msg.snapshot.Changes))
	}
	if msg.notice != "" && m.Feedback != nil {
		m.Feedback.NotifyInfo(msg.notice)
	}
	if m.Tree != nil && m.Tree.Model != nil {
		return m.Tree.Model.RefreshGitStatus()
	}
	return nil
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

func (m *App) stageGitChange(change workspace.GitChange) tea.Cmd {
	return m.sourceControlMutation(
		"Staged "+filepath.Base(change.Path), false,
		func(ctx context.Context, service *workspace.GitService) error {
			return service.Stage(ctx, change.Path)
		},
	)
}

func (m *App) unstageGitChange(change workspace.GitChange) tea.Cmd {
	return m.sourceControlMutation(
		"Unstaged "+filepath.Base(change.Path), false,
		func(ctx context.Context, service *workspace.GitService) error {
			return service.Unstage(ctx, change.Path)
		},
	)
}

func (m *App) discardGitChange(change workspace.GitChange) tea.Cmd {
	return m.sourceControlMutation(
		"Discarded changes in "+filepath.Base(change.Path), false,
		func(ctx context.Context, service *workspace.GitService) error {
			if change.Area == workspace.GitAreaUntracked {
				return fmt.Errorf("untracked files are never deleted by Discard")
			}
			return service.Discard(ctx, change.Path)
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
	})
}

func (m *App) buildGitDiscardFrame(change workspace.GitChange) *ui.ComponentWidget {
	return ui.BuildConfirmFrame(m.Theme, ui.ConfirmFrameSpec{
		Title:    "Discard Changes",
		Question: fmt.Sprintf("Discard working-tree changes in %q?", filepath.Base(change.Path)),
		Detail:   change.Path + " · this cannot be undone",
		Severity: ui.ConfirmPromptDanger,
		Buttons: []ui.ConfirmButtonSpec{
			{Label: "Discard", ActionID: ui.ConfirmActionDiscard},
			{Label: "Cancel", ActionID: ui.ConfirmActionCancel},
		},
		FocusedIndex: m.TreeDeleteChoice,
		Width:        64,
	})
}

func (m *App) stageAllGitChanges() tea.Cmd {
	return m.sourceControlMutation(
		"Staged all changes", false,
		func(ctx context.Context, service *workspace.GitService) error {
			return service.StageAll(ctx)
		},
	)
}

func (m *App) unstageAllGitChanges() tea.Cmd {
	return m.sourceControlMutation(
		"Unstaged all changes", false,
		func(ctx context.Context, service *workspace.GitService) error {
			return service.UnstageAll(ctx)
		},
	)
}
