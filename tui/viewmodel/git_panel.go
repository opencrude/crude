package viewmodel

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/shellport"
	"crude/tui/screens/gitdiff"
	"crude/tui/screens/gitpanel"
	"crude/tui/screens/termdock"
	"crude/tui/ui"
	"crude/tui/window"
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
	gen      uint64
	snapshot workspace.GitSnapshot
	err      error
	notice   string
	// commit is the request a successful commit completes, so the panel
	// clears only the draft that was actually committed.
	commit *gitpanel.CommitRequest
	// silent marks a watcher-driven reconciliation. Nobody asked for it, so it
	// never sets the panel's loading state, records a status operation, notifies
	// a failure, or touches the commit field.
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
	if m.gitPanel == nil {
		return nil
	}
	m.ws.EnsureGit()
	m.gitPanel.SetLoading(true)
	gen := m.gitRefreshGen.Add(1)
	m.recordGitOperation(gen, "Refresh Git")
	service := m.ws.Git()
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
	commit *gitpanel.CommitRequest,
	mutate func(context.Context, shellport.GitService) error,
) tea.Cmd {
	m.ws.EnsureGit()
	if m.gitPanel != nil {
		m.gitPanel.SetLoading(true)
	}
	gen := m.gitRefreshGen.Add(1)
	m.recordGitOperation(gen, firstNonempty(notice, "Update Git"))
	service := m.ws.Git()
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
			notice: notice, commit: commit,
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

func (m *App) applySourceControlResult(msg sourceControlResultMsg) tea.Cmd {
	// Every read settles exactly one in-flight slot. A read invalidated by a
	// workspace boundary no longer owns a slot: that boundary resets the count
	// itself (beginWorkspaceSwitch / finishWorkspaceSwitch).
	if msg.gen != 0 && msg.gen == m.gitRefreshGen.Load() && m.gitReadsInFlight > 0 {
		m.gitReadsInFlight--
	}
	if msg.gen == 0 || msg.gen != m.gitRefreshGen.Load() || m.gitPanel == nil {
		return nil
	}
	m.gitSnapshotAt = time.Now()
	if msg.err != nil {
		if msg.silent {
			// Unrequested reads never report: a workspace without a repository,
			// or a transient failure, must not raise an issue or toast. The last
			// known projection stays painted.
			if m.ws.Logger() != nil {
				m.ws.Logger().Debug("GIT", "snapshot_reconcile_failed", "error", msg.err.Error())
			}
			return nil
		}
		if msg.commit != nil {
			m.gitPanel.SetBusy("")
		}
		m.finishGitOperation(msg.gen, types.OperationFailed, msg.err.Error())
		m.ws.Status().SetIssue(types.IssueSnapshot{ID: "git", Owner: types.ResourceRef{Kind: types.ResourceGit, ID: m.ws.RootPath()}, Severity: types.SeverityError, Summary: "Source control failed", Detail: msg.err.Error()})
		m.gitPanel.SetError(msg.err)
		if m.Feedback != nil {
			m.Feedback.NotifyError("Source control: " + msg.err.Error())
		}
		return nil
	}
	if msg.silent {
		// File bytes can change while every status code stays the same, so a
		// visible diff re-reads even when the snapshot itself is unchanged.
		if !sameGitSnapshot(m.gitPanel.Snapshot(), msg.snapshot) {
			m.presentGitSnapshot(msg.snapshot)
		} else {
			m.gitPanel.SetLoading(false)
		}
		return m.prepareGitChangesIfVisible()
	}
	m.finishGitOperation(msg.gen, types.OperationSucceeded, "")
	m.ws.Status().ResolveIssue("git")
	m.presentGitSnapshot(msg.snapshot)
	if msg.commit != nil {
		m.gitPanel.CompleteCommit(*msg.commit)
	}
	if msg.notice != "" && m.Feedback != nil {
		m.Feedback.NotifyInfo(msg.notice)
	}
	cmds := []tea.Cmd{m.prepareGitChangesIfVisible()}
	if m.Tree != nil && m.Tree.Model != nil {
		cmds = append(cmds, m.Tree.Model.RefreshGitStatus())
	}
	return tea.Batch(cmds...)
}

// presentGitSnapshot publishes one repository projection to every surface that
// derives from it: the Git panel, the header identity, the footer Git pill,
// and the editor's gutter/blame annotation (a moved HEAD invalidates it).
func (m *App) presentGitSnapshot(snapshot workspace.GitSnapshot) {
	if m == nil || m.gitPanel == nil {
		return
	}
	m.gitPanel.SetSnapshot(snapshot)
	m.projectTreeGitSnapshot(snapshot)
	if m.EditorState != nil && m.EditorState.Editor != nil && m.EditorState.Editor.IsOpen() {
		m.markEditorGitStaleNow()
	}
}

func (m *App) recordGitOperation(generation uint64, label string) {
	if m.ws.Status() == nil {
		return
	}
	m.ws.Status().SetOperation(types.OperationSnapshot{
		ID: fmt.Sprintf("git:%d", generation), Owner: types.ResourceRef{Kind: types.ResourceGit, ID: m.ws.RootPath()},
		Kind: "git", Label: label, State: types.OperationRunning, StartedAt: time.Now(), Cancellable: true,
	})
}

func (m *App) finishGitOperation(generation uint64, state types.OperationState, errorText string) {
	if m.ws.Status() == nil {
		return
	}
	m.ws.Status().FinishOperation(fmt.Sprintf("git:%d", generation), state, errorText)
}

func (m *App) discardGitChange(preview workspace.GitDiscardPreview) tea.Cmd {
	owner := m.ws
	return m.sourceControlMutation(
		"Discarded "+filepath.Base(preview.Comparison.Path)+" · recover through Local History", nil,
		func(ctx context.Context, _ shellport.GitService) error {
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
	sequence, generation, owner := m.gitDiscardSequence, m.workspaceSwitchGen.Load(), m.ws
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

// gitChangesPreparedMsg carries one batched comparison of every listed change,
// prepared (diffed and lexed) off the update goroutine.
type gitChangesPreparedMsg struct {
	workspace  uint64
	generation uint64
	files      []gitdiff.File
	err        error
}

// gitEvidenceMsg carries one inspected commit for the panel preview or the
// main review.
type gitEvidenceMsg struct {
	workspace uint64
	sequence  uint64
	tab       window.TabID // review tab; zero targets the panel preview
	evidence  workspace.GitCommitEvidence
	files     []gitdiff.File
	err       error
}

const defaultGitPanelHeight = 18

// toggleGitPanel opens the Git dock destination, focuses it when it is open
// but unfocused, and hides it when it already owns focus.
func (m *App) toggleGitPanel() tea.Cmd {
	if m.termDock == nil || m.gitPanel == nil {
		return nil
	}
	if m.termDock.IsOpen() && m.termDock.ActivePanel() == termdock.PanelGit && !m.termDockFocused {
		m.setSessionDeckFocused(false)
		m.setTermDockFocused(true)
		m.FocusStack.Clear()
		return nil
	}
	m.toggleWorkspacePanel(termdock.PanelGit)
	if !m.gitPanelOpen() {
		return nil
	}
	return m.refreshSourceControl()
}

// openGitPanel shows the Git dock on mode without toggling it closed.
func (m *App) openGitPanel(mode gitpanel.Mode) tea.Cmd {
	if m.gitPanel == nil {
		return nil
	}
	m.gitPanel.SetMode(mode)
	if m.gitPanelOpen() {
		m.setTermDockFocused(true)
		if m.rootHost != nil {
			m.rootHost.Focus(m.gitPanel.PreferredFocus())
		}
		return m.refreshSourceControl()
	}
	return m.toggleGitPanel()
}

func (m *App) gitPanelOpen() bool {
	return m.termDock != nil && m.gitPanel != nil && m.termDock.IsOpen() && m.termDock.ActivePanel() == termdock.PanelGit
}

// prepareGitChangesIfVisible re-derives every listed diff while a surface
// shows them; hidden Git views cost nothing.
func (m *App) prepareGitChangesIfVisible() tea.Cmd {
	if !m.gitPanelOpen() && !m.gitChangeReviewOpen() {
		return nil
	}
	return m.prepareGitChanges()
}

// prepareGitChanges compares and prepares the painted change list. One run is
// in flight at a time; a request during a run reruns once it settles, so the
// latest snapshot always wins without stacking reads.
func (m *App) prepareGitChanges() tea.Cmd {
	if m.gitPanel == nil {
		return nil
	}
	if m.gitPrepareInFlight {
		m.gitPrepareDirty = true
		return nil
	}
	m.ws.EnsureGit()
	changes := append([]workspace.GitChange(nil), m.gitPanel.Snapshot().Changes...)
	m.gitPrepareInFlight = true
	m.gitPrepareGen++
	generation, owner, service := m.gitPrepareGen, m.workspaceSwitchGen.Load(), m.ws.Git()
	return func() tea.Msg {
		if len(changes) == 0 {
			return gitChangesPreparedMsg{workspace: owner, generation: generation}
		}
		ctx, cancel := m.sourceControlContext()
		defer cancel()
		items, err := service.CompareChanges(ctx, changes)
		if err != nil {
			return gitChangesPreparedMsg{workspace: owner, generation: generation, err: err}
		}
		return gitChangesPreparedMsg{workspace: owner, generation: generation, files: gitdiff.PrepareChanges(items)}
	}
}

func (m *App) applyGitChangesPrepared(msg gitChangesPreparedMsg) tea.Cmd {
	if msg.workspace != m.workspaceSwitchGen.Load() || msg.generation != m.gitPrepareGen || m.gitPanel == nil {
		return nil
	}
	m.gitPrepareInFlight = false
	if msg.err != nil {
		if m.ws.Logger() != nil {
			m.ws.Logger().Debug("GIT", "prepare_changes_failed", "error", msg.err.Error())
		}
	} else {
		m.gitPanel.SetPrepared(msg.files)
		m.showPreparedGitChanges(msg.files, m.gitPanel.Snapshot().Branch)
	}
	if m.gitPrepareDirty {
		m.gitPrepareDirty = false
		return m.prepareGitChanges()
	}
	return nil
}

// gitDirtyEditor names the first change whose open editor holds unsaved bytes.
// Staging reads disk, so staging such a file would record something the user
// is not looking at.
func (m *App) gitDirtyEditor(changes []workspace.GitChange) string {
	if m.EditorState == nil {
		return ""
	}
	for _, change := range changes {
		if tab := m.EditorState.Tabs[change.AbsolutePath]; tab != nil && tab.Editor != nil && tab.Editor.Modified() {
			return change.Path
		}
	}
	return ""
}

func gitChangePaths(changes []workspace.GitChange) []string {
	paths := make([]string, 0, len(changes))
	for _, change := range changes {
		paths = append(paths, change.Path)
	}
	return paths
}

func gitCountNoun(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func (m *App) stageGitChanges(changes []workspace.GitChange) tea.Cmd {
	if len(changes) == 0 {
		return nil
	}
	if path := m.gitDirtyEditor(changes); path != "" {
		m.Feedback.NotifyError(path + " has unsaved edits · save it before staging")
		return nil
	}
	paths := gitChangePaths(changes)
	notice := "Staged " + gitCountNoun(len(paths), "file")
	if len(paths) == 1 {
		notice = "Staged " + filepath.Base(paths[0])
	}
	return m.sourceControlMutation(notice, nil, func(ctx context.Context, service shellport.GitService) error {
		return service.Stage(ctx, paths...)
	})
}

func (m *App) unstageGitChanges(changes []workspace.GitChange) tea.Cmd {
	if len(changes) == 0 {
		return nil
	}
	paths := gitChangePaths(changes)
	notice := "Unstaged " + gitCountNoun(len(paths), "file")
	if len(paths) == 1 {
		notice = "Unstaged " + filepath.Base(paths[0])
	}
	return m.sourceControlMutation(notice, nil, func(ctx context.Context, service shellport.GitService) error {
		return service.Unstage(ctx, paths...)
	})
}

// commitGit commits exactly the set the panel showed; "commit all" stages the
// listed paths first. Either way a staged set the user did not see fails closed.
func (m *App) commitGit(request gitpanel.CommitRequest) tea.Cmd {
	if request.StageAll {
		if path := m.gitDirtyEditor(request.Changes); path != "" {
			m.gitPanel.SetError(fmt.Errorf("%s has unsaved edits · save it before committing everything", path))
			return nil
		}
	}
	m.gitPanel.SetBusy("Committing")
	copy := request
	notice := "Committed " + gitCountNoun(len(request.Paths), "file")
	return m.sourceControlMutation(notice, &copy, func(ctx context.Context, service shellport.GitService) error {
		if request.StageAll {
			if err := service.Stage(ctx, request.Paths...); err != nil {
				return err
			}
		}
		return service.CommitStaged(ctx, request.Paths, request.Message)
	})
}

// handleGitFileIntent runs a per-file action from the panel preview or review.
// Stage/unstage of a painted comparison records exactly the painted bytes; a
// file that changed since it was painted fails closed and is re-read.
func (m *App) handleGitFileIntent(intent gitdiff.FileIntent) tea.Cmd {
	switch intent.Op {
	case gitdiff.OpOpen:
		path := intent.Comparison.AbsolutePath
		if path == "" {
			path = intent.Change.AbsolutePath
		}
		if intent.Historical {
			if _, err := os.Stat(path); err != nil {
				m.Feedback.NotifyError(intent.Comparison.Path + " no longer exists in the working tree")
				return nil
			}
		}
		m.openFileAtLine(path, max(1, intent.Line))
		return nil
	case gitdiff.OpDiscard:
		if !intent.Historical {
			m.startDiscardGitChange(intent.Change)
		}
		return nil
	case gitdiff.OpStageHunk, gitdiff.OpUnstageHunk:
		if intent.Historical {
			return nil
		}
		stage := intent.Op == gitdiff.OpStageHunk
		if stage {
			if path := m.gitDirtyEditor([]workspace.GitChange{intent.Change}); path != "" {
				m.Feedback.NotifyError(path + " has unsaved edits · save it before staging")
				return nil
			}
		}
		comparison, hunk := intent.Comparison, intent.Hunk
		verb := "Unstaged a hunk of "
		if stage {
			verb = "Staged a hunk of "
		}
		return m.sourceControlMutation(verb+filepath.Base(intent.Change.Path), nil, func(ctx context.Context, service shellport.GitService) error {
			if stage {
				return service.StageHunk(ctx, comparison, hunk)
			}
			return service.UnstageHunk(ctx, comparison, hunk)
		})
	case gitdiff.OpDiscardHunk:
		if !intent.Historical {
			m.confirmDiscardGitHunk(intent)
		}
		return nil
	case gitdiff.OpStage, gitdiff.OpUnstage:
		if intent.Historical {
			return nil
		}
		stage := intent.Op == gitdiff.OpStage
		if stage {
			if path := m.gitDirtyEditor([]workspace.GitChange{intent.Change}); path != "" {
				m.Feedback.NotifyError(path + " has unsaved edits · save it before staging")
				return nil
			}
		}
		comparison := intent.Comparison
		exact := comparison.Fingerprint != "" && comparison.OriginalPath == "" && intent.Change.Area != workspace.GitAreaConflict
		verb := "Unstaged "
		if stage {
			verb = "Staged "
		}
		return m.sourceControlMutation(verb+filepath.Base(intent.Change.Path), nil, func(ctx context.Context, service shellport.GitService) error {
			switch {
			case exact && stage:
				return service.StageReviewed(ctx, comparison)
			case exact:
				return service.UnstageReviewed(ctx, comparison)
			case stage:
				return service.Stage(ctx, intent.Change.Path)
			}
			return service.Unstage(ctx, intent.Change.Path)
		})
	}
	return nil
}

// openFileAtLine opens path with the cursor on a one-based line.
func (m *App) openFileAtLine(path string, line int) {
	m.jumpToDiagnostic(path, workspace.LSPDiagnostic{Line: line - 1, EndLine: line - 1})
}

// inspectGitCommit loads a commit for the panel's History preview.
func (m *App) inspectGitCommit(commit workspace.GitCommitSummary) tea.Cmd {
	m.gitEvidenceSequence++
	m.gitPanel.RequestEvidence(commit.Hash)
	return m.loadGitEvidence(commit.Hash, 0, m.gitEvidenceSequence)
}

// inspectGitCommitInto loads a commit for one review tab.
func (m *App) inspectGitCommitInto(commit workspace.GitCommitSummary, view *gitReviewView) tea.Cmd {
	m.gitEvidenceSequence++
	view.evidenceSequence = m.gitEvidenceSequence
	return m.loadGitEvidence(commit.Hash, view.tab, view.evidenceSequence)
}

func (m *App) loadGitEvidence(hash string, tab window.TabID, sequence uint64) tea.Cmd {
	m.ws.EnsureGit()
	owner, service := m.workspaceSwitchGen.Load(), m.ws.Git()
	return func() tea.Msg {
		ctx, cancel := m.sourceControlContext()
		defer cancel()
		evidence, err := service.InspectCommit(ctx, hash)
		if err != nil {
			return gitEvidenceMsg{workspace: owner, sequence: sequence, tab: tab, err: err}
		}
		return gitEvidenceMsg{workspace: owner, sequence: sequence, tab: tab, evidence: evidence, files: gitdiff.PrepareComparisons(evidence.Comparisons)}
	}
}

func (m *App) applyGitEvidence(msg gitEvidenceMsg) {
	if msg.workspace != m.workspaceSwitchGen.Load() {
		return
	}
	if msg.tab != 0 {
		view := m.gitReviews[msg.tab]
		if view == nil || view.evidenceSequence != msg.sequence {
			return
		}
		if msg.err != nil {
			view.screen.SetError(msg.err)
			return
		}
		view.screen.ShowCommit(msg.evidence, msg.files)
		return
	}
	if msg.sequence != m.gitEvidenceSequence || m.gitPanel == nil {
		return
	}
	if msg.err != nil {
		m.gitPanel.SetError(msg.err)
		return
	}
	m.gitPanel.SetCommitEvidence(msg.evidence, msg.files)
}

// handleGitPanelAction routes intents from the Git dock and the review.
func (m *App) handleGitPanelAction(action ui.Action) (tea.Cmd, bool) {
	switch action.ID {
	case gitpanel.ActionRefresh:
		return m.refreshSourceControl(), true
	case gitpanel.ActionStage:
		changes, _ := action.Payload.([]workspace.GitChange)
		return m.stageGitChanges(changes), true
	case gitpanel.ActionUnstage:
		changes, _ := action.Payload.([]workspace.GitChange)
		return m.unstageGitChanges(changes), true
	case gitpanel.ActionDiscard:
		if change, ok := action.Payload.(workspace.GitChange); ok {
			m.startDiscardGitChange(change)
		}
		return nil, true
	case gitpanel.ActionOpenFile:
		if change, ok := action.Payload.(workspace.GitChange); ok && change.AbsolutePath != "" {
			key := gitdiff.ChangeKey(change)
			line := 1
			if file, ok := m.gitPanel.PreparedFiles()[key]; ok {
				line = file.FirstChangedLine()
			}
			m.openFileAtLine(change.AbsolutePath, line)
		}
		return nil, true
	case gitpanel.ActionReview:
		key, _ := action.Payload.(string)
		for _, change := range m.gitPanel.Snapshot().Changes {
			if key != "" && gitdiff.ChangeKey(change) == key {
				return m.openGitFileReview(change), true
			}
		}
		return m.openGitReview("", ""), true
	case gitpanel.ActionCommit:
		if request, ok := action.Payload.(gitpanel.CommitRequest); ok {
			return m.commitGit(request), true
		}
		return nil, true
	case gitpanel.ActionGenerate:
		request, _ := action.Payload.(gitpanel.GenerateRequest)
		return m.startCommitMessageWriter(request), true
	case gitpanel.ActionStopGenerate:
		m.stopCommitMessageWriter()
		return nil, true
	case gitpanel.ActionInspect:
		if commit, ok := action.Payload.(workspace.GitCommitSummary); ok {
			return m.inspectGitCommit(commit), true
		}
		return nil, true
	case gitpanel.ActionOpenCommit:
		if commit, ok := action.Payload.(workspace.GitCommitSummary); ok {
			return m.openGitCommitReview(commit), true
		}
		return nil, true
	case gitpanel.ActionCheckout:
		if branch, ok := action.Payload.(workspace.GitBranchSummary); ok {
			m.startGitCheckout(branch)
		}
		return nil, true
	case gitdiff.ActionFile:
		if intent, ok := action.Payload.(gitdiff.FileIntent); ok {
			return m.handleGitFileIntent(intent), true
		}
		return nil, true
	}
	return m.handleGitReviewScreenAction(action)
}

// startGitCheckout confirms a branch switch, re-checking that HEAD and the
// repository are still the ones the user saw.
func (m *App) startGitCheckout(branch workspace.GitBranchSummary) {
	snapshot := m.gitPanel.Snapshot()
	state := &ui.ContextMenuState{Title: "Switch to " + branch.Name, Sections: []ui.ContextMenuSection{{Items: []ui.ContextMenuItem{
		{Label: "Switch branch", Detail: "uncommitted changes move with you", Action: func() {
			m.startSourceControlMutation(m.sourceControlMutation("Switched to "+branch.Name, nil, func(ctx context.Context, service shellport.GitService) error {
				current, err := service.Snapshot(ctx)
				if err != nil {
					return err
				}
				if current.Head != snapshot.Head || current.RepositoryRoot != snapshot.RepositoryRoot {
					return workspace.ErrGitReviewStale
				}
				return service.Checkout(ctx, branch.Name)
			}))
		}},
		{Label: "Cancel"},
	}}}}
	m.openContextMenuState(tea.Mouse{X: max(0, m.width/2-15), Y: max(m.shellTopRow(), m.height/2)}, state)
}

// confirmDiscardGitHunk asks before reverting one hunk on disk. The revert is
// recoverable through the mandatory Local History snapshot it captures.
func (m *App) confirmDiscardGitHunk(intent gitdiff.FileIntent) {
	if path := m.gitDirtyEditor([]workspace.GitChange{intent.Change}); path != "" {
		m.Feedback.NotifyError(path + " has unsaved edits · save it before discarding")
		return
	}
	owner, comparison, hunk := m.ws, intent.Comparison, intent.Hunk
	name := filepath.Base(intent.Change.Path)
	state := &ui.ContextMenuState{Title: fmt.Sprintf("Discard hunk in %s (lines %d–%d)?", name, hunk.NewStart+1, hunk.NewEnd), Sections: []ui.ContextMenuSection{{Items: []ui.ContextMenuItem{
		{Label: "Cancel"},
		{Label: "Discard hunk", Detail: "recoverable in Local History", Action: func() {
			m.startSourceControlMutation(m.sourceControlMutation("Discarded a hunk of "+name+" · recover through Local History", nil, func(ctx context.Context, _ shellport.GitService) error {
				_, err := owner.DiscardGitHunk(ctx, comparison, hunk)
				return err
			}))
		}},
	}}}}
	m.openContextMenuState(tea.Mouse{X: max(0, m.width/2-20), Y: max(m.shellTopRow(), m.height/2)}, state)
}
