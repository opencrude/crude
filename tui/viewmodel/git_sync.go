package viewmodel

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/shellport"
	"crude/tui/screens/gitmerge"
	"crude/tui/screens/gitpanel"
	"crude/tui/ui"
	"crude/tui/wm"
	types "crude/types"
	"crude/workspace"
)

const (
	gitMessageEditorOverlay ui.OverlayID = "git-message-editor"
	gitMergeOverlay         ui.OverlayID = "git-merge"
	// gitRemoteTimeout bounds a fetch, pull, push or merge; Stop ends one
	// sooner. A slow network clone of a large history can legitimately run
	// for minutes.
	gitRemoteTimeout = 10 * time.Minute
)

// cancelGitRemote stops the running remote command, if any. Its result is
// then ignored by sequence.
func (m *App) cancelGitRemote() {
	if m.gitRemoteCancel != nil {
		m.gitRemoteCancel()
		m.gitRemoteCancel = nil
	}
}

// runGitRemote runs one fetch, pull, push or merge with a Stop the user can
// press. Its completion is identified by its own sequence, so a refresh the
// user starts meanwhile never strands the busy label.
func (m *App) runGitRemote(label, done string, op func(context.Context, shellport.GitService) (workspace.GitSyncResult, error)) tea.Cmd {
	if m.gitPanel == nil || m.gitPanel.RemoteBusy() != "" {
		return nil
	}
	m.ws.EnsureGit()
	service := m.ws.Git()
	if service == nil {
		return nil
	}
	m.cancelGitRemote()
	ctx, cancel := context.WithTimeout(context.Background(), gitRemoteTimeout)
	m.gitRemoteCancel = cancel
	m.gitRemoteSeq++
	seq, workspaceGen := m.gitRemoteSeq, m.workspaceSwitchGen.Load()
	gen := m.gitRefreshGen.Add(1)
	m.gitReadsInFlight++
	m.recordGitOperation(gen, label)
	m.gitPanel.SetRemoteBusy(label)
	return func() tea.Msg {
		defer cancel()
		result, err := op(ctx, service)
		msg := sourceControlResultMsg{gen: gen, remoteSeq: seq, workspaceGen: workspaceGen, remoteLabel: label, err: err}
		if err != nil {
			return msg
		}
		msg.notice, msg.conflicts = done, result.Conflicts
		if len(result.Conflicts) > 0 {
			msg.notice = fmt.Sprintf("%s stopped: %s", label, gitCountNoun(len(result.Conflicts), "conflicted file"))
		}
		readCtx, readCancel := m.sourceControlContext()
		defer readCancel()
		msg.snapshot, msg.err = service.Snapshot(readCtx)
		return msg
	}
}

func (m *App) applyGitRemoteResult(msg sourceControlResultMsg) tea.Cmd {
	if msg.remoteSeq != m.gitRemoteSeq || msg.workspaceGen != m.workspaceSwitchGen.Load() || m.gitPanel == nil {
		return nil
	}
	m.gitRemoteCancel = nil
	m.gitPanel.SetRemoteBusy("")
	if msg.err != nil {
		if errors.Is(msg.err, context.Canceled) {
			m.finishGitOperation(msg.gen, types.OperationCancelled, "")
			if m.Feedback != nil {
				m.Feedback.NotifyInfo(msg.remoteLabel + " stopped")
			}
			return m.refreshSourceControl()
		}
		m.finishGitOperation(msg.gen, types.OperationFailed, msg.err.Error())
		m.ws.Status().SetIssue(types.IssueSnapshot{ID: "git", Owner: types.ResourceRef{Kind: types.ResourceGit, ID: m.ws.RootPath()}, Severity: types.SeverityError, Summary: msg.remoteLabel + " failed", Detail: msg.err.Error()})
		m.gitPanel.SetError(msg.err)
		if m.Feedback != nil {
			m.Feedback.NotifyError(msg.remoteLabel + ": " + msg.err.Error())
		}
		return m.refreshSourceControl()
	}
	m.finishGitOperation(msg.gen, types.OperationSucceeded, "")
	m.ws.Status().ResolveIssue("git")
	m.gitSnapshotAt = time.Now()
	m.presentGitSnapshot(msg.snapshot)
	if m.Feedback != nil && msg.notice != "" {
		if len(msg.conflicts) > 0 {
			m.Feedback.NotifyError(msg.notice)
		} else {
			m.Feedback.NotifyInfo(msg.notice)
		}
	}
	cmds := []tea.Cmd{m.prepareGitChangesIfVisible()}
	if m.Tree != nil && m.Tree.Model != nil {
		cmds = append(cmds, m.Tree.Model.RefreshGitStatus())
	}
	if len(msg.conflicts) > 0 && !m.liveGitMerge(m.gitMerge) {
		cmds = append(cmds, m.openGitMergeDialog())
	}
	return tea.Batch(cmds...)
}

func (m *App) handleGitSyncAction(action ui.Action) (tea.Cmd, bool) {
	switch action.ID {
	case gitpanel.ActionFetch:
		return m.runGitRemote("Fetch", "Fetched from remotes", func(ctx context.Context, service shellport.GitService) (workspace.GitSyncResult, error) {
			return workspace.GitSyncResult{}, service.Fetch(ctx)
		}), true
	case gitpanel.ActionPull:
		return m.runGitRemote("Pull", "Pulled from "+m.gitPanel.Snapshot().Upstream, func(ctx context.Context, service shellport.GitService) (workspace.GitSyncResult, error) {
			return service.Pull(ctx)
		}), true
	case gitpanel.ActionPush:
		return m.runGitRemote("Push", "Pushed "+m.gitPanel.Snapshot().Branch, func(ctx context.Context, service shellport.GitService) (workspace.GitSyncResult, error) {
			return workspace.GitSyncResult{}, service.Push(ctx)
		}), true
	case gitpanel.ActionStopRemote:
		m.cancelGitRemote()
		return nil, true
	case gitpanel.ActionResolve:
		return m.openGitMergeDialog(), true
	case gitpanel.ActionAbortOperation:
		m.confirmAbortGitOperation()
		return nil, true
	case gitpanel.ActionContinueOperation:
		return m.continueGitOperation(), true
	case gitpanel.ActionEditMessage:
		text, _ := action.Payload.(string)
		m.openGitMessageEditor(text)
		return nil, true
	case gitpanel.ActionBranchMenu:
		if branch, ok := action.Payload.(workspace.GitBranchSummary); ok {
			m.openGitBranchMenu(branch)
		}
		return nil, true
	}
	return nil, false
}

// openGitBranchMenu offers what a branch row can do: switch to it, or merge
// it into the current branch.
func (m *App) openGitBranchMenu(branch workspace.GitBranchSummary) {
	snapshot := m.gitPanel.Snapshot()
	items := []ui.ContextMenuItem{
		{Label: "Switch to " + branch.Name, Detail: "uncommitted changes move with you", Action: func() { m.startGitCheckout(branch) }},
	}
	if snapshot.Operation == "" && !strings.HasPrefix(snapshot.Branch, "detached@") {
		items = append(items, ui.ContextMenuItem{Label: "Merge into " + snapshot.Branch, Detail: "conflicts open the resolver", Action: func() {
			m.startSourceControlMutation(m.runGitRemote("Merge", "Merged "+branch.Name+" into "+snapshot.Branch, func(ctx context.Context, service shellport.GitService) (workspace.GitSyncResult, error) {
				current, err := service.Snapshot(ctx)
				if err != nil {
					return workspace.GitSyncResult{}, err
				}
				if current.Head != snapshot.Head || current.Branch != snapshot.Branch {
					return workspace.GitSyncResult{}, workspace.ErrGitReviewStale
				}
				return service.Merge(ctx, branch.Name)
			}))
		}})
	}
	items = append(items, ui.ContextMenuItem{Label: "Cancel"})
	m.openContextMenuState(tea.Mouse{X: max(0, m.width/2-18), Y: max(m.shellTopRow(), m.height/2)}, &ui.ContextMenuState{Title: branch.Name, Sections: []ui.ContextMenuSection{{Items: items}}})
}

// confirmAbortGitOperation asks before abandoning the merge: resolutions
// already written to the working tree are discarded with it.
func (m *App) confirmAbortGitOperation() {
	snapshot := m.gitPanel.Snapshot()
	if snapshot.Operation == "" {
		return
	}
	doing := strings.ToLower(gitOperationName(snapshot.Operation))
	state := &ui.ContextMenuState{Title: "Abort " + doing + "?", Sections: []ui.ContextMenuSection{{Items: []ui.ContextMenuItem{
		{Label: "Cancel"},
		{Label: "Abort " + doing, Detail: "restores the branch as it was; resolutions are discarded", Action: func() {
			m.closeGitMergeDialog()
			m.startSourceControlMutation(m.sourceControlMutation("Aborted the "+doing, nil, func(ctx context.Context, service shellport.GitService) error {
				return service.AbortOperation(ctx)
			}))
		}},
	}}}}
	m.openContextMenuState(tea.Mouse{X: max(0, m.width/2-20), Y: max(m.shellTopRow(), m.height/2)}, state)
}

func gitOperationName(operation string) string {
	switch operation {
	case workspace.GitOperationRebase:
		return "Rebase"
	case workspace.GitOperationCherryPick:
		return "Cherry-pick"
	case workspace.GitOperationRevert:
		return "Revert"
	}
	return "Merge"
}

func (m *App) continueGitOperation() tea.Cmd {
	snapshot := m.gitPanel.Snapshot()
	if snapshot.Operation == "" {
		return nil
	}
	m.closeGitMergeDialog()
	name := strings.ToLower(gitOperationName(snapshot.Operation))
	return m.sourceControlMutation("Completed the "+name, nil, func(ctx context.Context, service shellport.GitService) error {
		return service.ContinueOperation(ctx)
	})
}

// gitMessageEditorMsg carries the editor's decision and edited text.
type gitMessageEditorMsg struct {
	action, text string
}

// openGitMessageEditor shows the full commit message in its own dialog. The
// panel draft is the authority; the dialog edits a copy.
func (m *App) openGitMessageEditor(text string) {
	if m.overlayHost == nil || m.gitPanel == nil {
		return
	}
	tokens := ui.NewTokenSet(m.Theme)
	body := gitpanel.NewMessageEditor(tokens, text, m.gitPanel.Snapshot().Operation == "" && len(m.gitPanel.Snapshot().ConflictedPaths()) == 0,
		func(action, text string) tea.Cmd {
			return func() tea.Msg { return gitMessageEditorMsg{action: action, text: text} }
		})
	w, h := min(96, max(40, m.width-6)), min(28, max(10, m.height-4))
	frame := ui.NewWindowFrameWidget(m.Theme, "git-message-editor/frame", ui.FrameDialog, "Commit message", body, nil, false, w, h)
	frame.Host().Focus(gitpanel.MessageEditorID)
	fw := wm.NewFloatingWindow(string(gitMessageEditorOverlay), "", frame, w, h, wm.WithDecoration(true), wm.WithMovable(true), wm.WithResizable(true), wm.WithModal(true), wm.WithFocusable(true), wm.WithClosable(false), wm.WithClickOutside(wm.ClickOutsideIgnore))
	wm.CenterOnScreen(fw, m.width, m.height)
	m.overlayHost.Open(gitMessageEditorOverlay, fw, ui.ModalDialogPolicy(), nil)
}

func (m *App) applyGitMessageEditor(msg gitMessageEditorMsg) tea.Cmd {
	if m.overlayHost == nil || !m.overlayHost.IsOpen(gitMessageEditorOverlay) {
		return nil
	}
	m.overlayHost.Close(gitMessageEditorOverlay)
	if m.gitPanel == nil || msg.action == gitpanel.ActionMessageCancel {
		return nil
	}
	m.gitPanel.SetMessage(msg.text)
	if msg.action != gitpanel.ActionMessageCommit {
		return nil
	}
	request, ok := m.gitPanel.CommitRequest()
	if !ok {
		return nil
	}
	m.gitPanel.SetBusy("Committing")
	return m.commitGit(request)
}

// gitMergeDialog is the open conflict resolver and its workspace generation.
type gitMergeDialog struct {
	resolver     *gitmerge.Resolver
	frame        *ui.WindowFrameWidget
	workspaceGen uint64
	sequence     uint64
}

type gitMergeDecisionMsg struct {
	dialog   *gitMergeDialog
	decision gitmerge.Decision
}

type gitConflictLoadedMsg struct {
	dialog   *gitMergeDialog
	sequence uint64
	file     workspace.GitConflictFile
	err      error
}

type gitConflictSavedMsg struct {
	dialog   *gitMergeDialog
	path     string
	snapshot workspace.GitSnapshot
	err      error
}

func (m *App) liveGitMerge(dialog *gitMergeDialog) bool {
	return dialog != nil && m.gitMerge == dialog && dialog.workspaceGen == m.workspaceSwitchGen.Load() &&
		m.overlayHost != nil && m.overlayHost.IsOpen(gitMergeOverlay)
}

func (m *App) closeGitMergeDialog() {
	if m.gitMerge != nil && m.overlayHost != nil {
		m.overlayHost.Close(gitMergeOverlay)
	}
	m.gitMerge = nil
}

// openGitMergeDialog opens (or focuses) the resolver for the operation that
// stopped, loading the first conflicted file.
func (m *App) openGitMergeDialog() tea.Cmd {
	if m.overlayHost == nil || m.gitPanel == nil {
		return nil
	}
	snapshot := m.gitPanel.Snapshot()
	if snapshot.Operation == "" {
		return nil
	}
	if m.liveGitMerge(m.gitMerge) {
		m.syncGitMergeDialog(snapshot)
		return nil
	}
	dialog := &gitMergeDialog{workspaceGen: m.workspaceSwitchGen.Load()}
	tokens := ui.NewTokenSet(m.Theme)
	dialog.resolver = gitmerge.New(tokens, func(decision gitmerge.Decision) tea.Cmd {
		return func() tea.Msg { return gitMergeDecisionMsg{dialog: dialog, decision: decision} }
	})
	w, h := min(150, max(50, m.width-4)), min(46, max(14, m.height-4))
	dialog.frame = ui.NewWindowFrameWidget(m.Theme, "git-merge/frame", ui.FrameDialog, "Resolve conflicts", dialog.resolver, nil, false, w, h)
	dialog.frame.Host().Focus(gitmerge.ScreenID)
	fw := wm.NewFloatingWindow(string(gitMergeOverlay), "", dialog.frame, w, h, wm.WithDecoration(true), wm.WithMovable(true), wm.WithResizable(true), wm.WithModal(true), wm.WithFocusable(true), wm.WithClosable(false), wm.WithClickOutside(wm.ClickOutsideIgnore))
	wm.CenterOnScreen(fw, m.width, m.height)
	m.overlayHost.Open(gitMergeOverlay, fw, ui.ModalDialogPolicy(), func() {
		if m.gitMerge == dialog {
			m.gitMerge = nil
		}
	})
	m.gitMerge = dialog
	return m.syncGitMergeDialog(snapshot)
}

// syncGitMergeDialog follows a new snapshot: the operation's end closes the
// dialog, and a resolver without a loaded file reads the first conflict.
func (m *App) syncGitMergeDialog(snapshot workspace.GitSnapshot) tea.Cmd {
	dialog := m.gitMerge
	if !m.liveGitMerge(dialog) {
		return nil
	}
	if snapshot.Operation == "" {
		m.closeGitMergeDialog()
		return nil
	}
	conflicts := snapshot.ConflictedPaths()
	dialog.resolver.SetConflicts(snapshot.Operation, conflicts)
	m.markGitMergeDirty()
	if _, loaded := dialog.resolver.File(); loaded || dialog.resolver.Loading() != "" || len(conflicts) == 0 {
		return nil
	}
	return m.loadGitConflict(dialog, conflicts[0])
}

func (m *App) markGitMergeDirty() {
	if dialog := m.gitMerge; dialog != nil && dialog.frame != nil {
		dialog.frame.Host().MarkLayout()
		dialog.frame.MarkContentDirty()
	}
}

func (m *App) loadGitConflict(dialog *gitMergeDialog, path string) tea.Cmd {
	m.ws.EnsureGit()
	service := m.ws.Git()
	if service == nil {
		return nil
	}
	dialog.sequence++
	sequence := dialog.sequence
	dialog.resolver.SetLoading(path)
	m.markGitMergeDirty()
	return func() tea.Msg {
		ctx, cancel := m.sourceControlContext()
		defer cancel()
		file, err := service.ConflictFile(ctx, path)
		return gitConflictLoadedMsg{dialog: dialog, sequence: sequence, file: file, err: err}
	}
}

func (m *App) applyGitConflictLoaded(msg gitConflictLoadedMsg) tea.Cmd {
	if !m.liveGitMerge(msg.dialog) || msg.sequence != msg.dialog.sequence {
		return nil
	}
	if msg.err != nil {
		msg.dialog.resolver.SetError(msg.err)
	} else {
		msg.dialog.resolver.SetFile(msg.file)
	}
	m.markGitMergeDirty()
	return nil
}

func (m *App) applyGitMergeDecision(msg gitMergeDecisionMsg) tea.Cmd {
	dialog := msg.dialog
	if !m.liveGitMerge(dialog) {
		return nil
	}
	decision := msg.decision
	switch decision.Kind {
	case gitmerge.DecideSelect:
		return m.loadGitConflict(dialog, decision.Path)
	case gitmerge.DecideClose:
		m.closeGitMergeDialog()
	case gitmerge.DecideOpen:
		root := m.gitPanel.Snapshot().RepositoryRoot
		m.closeGitMergeDialog()
		m.openFileAtLine(filepath.Join(root, filepath.FromSlash(decision.Path)), decision.Line)
	case gitmerge.DecideAbort:
		m.confirmAbortGitOperation()
	case gitmerge.DecideComplete:
		return m.continueGitOperation()
	case gitmerge.DecideSave:
		if path := m.gitDirtyEditor([]workspace.GitChange{{Path: decision.Path, AbsolutePath: filepath.Join(m.gitPanel.Snapshot().RepositoryRoot, filepath.FromSlash(decision.Path))}}); path != "" {
			dialog.resolver.SetError(fmt.Errorf("%s has unsaved edits in the editor; save or close them first", path))
			m.markGitMergeDirty()
			return nil
		}
		dialog.resolver.SetBusy("Saving " + decision.Path)
		m.markGitMergeDirty()
		owner, service := m.ws, m.ws.Git()
		return func() tea.Msg {
			ctx, cancel := m.sourceControlContext()
			defer cancel()
			_, err := owner.ResolveGitConflict(ctx, decision.Path, decision.Fingerprint, decision.Choices)
			var snapshot workspace.GitSnapshot
			if err == nil {
				snapshot, err = service.Snapshot(ctx)
			}
			return gitConflictSavedMsg{dialog: dialog, path: decision.Path, snapshot: snapshot, err: err}
		}
	}
	m.markGitMergeDirty()
	return nil
}

func (m *App) applyGitConflictSaved(msg gitConflictSavedMsg) tea.Cmd {
	if !m.liveGitMerge(msg.dialog) {
		return nil
	}
	resolver := msg.dialog.resolver
	if msg.err != nil {
		if errors.Is(msg.err, workspace.ErrGitReviewStale) {
			resolver.SetError(fmt.Errorf("%s changed since it was read; it was reloaded, decide again", msg.path))
			m.markGitMergeDirty()
			return m.loadGitConflict(msg.dialog, msg.path)
		}
		resolver.SetError(msg.err)
		m.markGitMergeDirty()
		return nil
	}
	resolver.SetBusy("")
	if m.Feedback != nil {
		m.Feedback.NotifyInfo("Resolved " + msg.path + " · recoverable in Local History")
	}
	// Syncing first returns the read of the next conflicted file as this
	// result's command; presenting the snapshot then finds it already loading.
	cmds := []tea.Cmd{m.syncGitMergeDialog(msg.snapshot)}
	m.presentGitSnapshot(msg.snapshot)
	cmds = append(cmds, m.prepareGitChangesIfVisible())
	if m.Tree != nil && m.Tree.Model != nil {
		cmds = append(cmds, m.Tree.Model.RefreshGitStatus())
	}
	return tea.Batch(cmds...)
}
