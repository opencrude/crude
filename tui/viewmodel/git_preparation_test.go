package viewmodel

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"crude/shellport"
	"crude/tui/screens/diffview"
	"crude/tui/screens/gitdiff"
	"crude/tui/screens/gitpanel"
	"crude/tui/screens/termdock"
	"crude/tui/ui"
	"crude/workspace"

	tea "charm.land/bubbletea/v2"
)

func TestGitPreparationFailureSettlesEveryWorkingView(t *testing.T) {
	app := gitSyncApp(t, newGitRepoFixture(t))
	app.width, app.height = 160, 44
	change := app.gitPanel.Snapshot().Changes[0]
	app.ws = &preparationPort{Workspace: app.ws, service: &preparationService{GitService: app.ws.Git(),
		compare: func(context.Context, []workspace.GitChange) ([]workspace.GitChangeComparison, error) {
			return nil, errors.New("repository read failed")
		}}}
	pumpGit(t, app, app.openGitPanel(gitpanel.ModeChanges))
	pumpGit(t, app, app.openGitFileReview(change))
	app.paintShellFrame()
	text := bufferPlain(app.mainSurf.Buffer())
	if strings.Contains(text, "Preparing diff") || strings.Contains(text, "Reading changes") || !strings.Contains(text, "repository read failed") {
		t.Fatalf("failed preparation did not settle visibly:\n%s", text)
	}
}

type preparationPort struct {
	shellport.Workspace
	service shellport.GitService
}

func (p *preparationPort) EnsureGit() shellport.GitService { return p.service }
func (p *preparationPort) Git() shellport.GitService       { return p.service }

type preparationService struct {
	shellport.GitService
	compare func(context.Context, []workspace.GitChange) ([]workspace.GitChangeComparison, error)
}

type heldGitComparison struct {
	shellport.GitService
	started chan struct{}
	joined  chan struct{}
	reads   atomic.Int32
}

func (s *heldGitComparison) CompareChanges(ctx context.Context, changes []workspace.GitChange) ([]workspace.GitChangeComparison, error) {
	if s.reads.Add(1) == 1 {
		close(s.started)
		<-ctx.Done()
		close(s.joined)
		return nil, ctx.Err()
	}
	return s.GitService.CompareChanges(ctx, changes)
}

func (s *heldGitComparison) Stage(ctx context.Context, paths ...string) error {
	select {
	case <-s.joined:
		return s.GitService.Stage(ctx, paths...)
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestGitStageActionDoesNotWaitBehindObsoleteComparison(t *testing.T) {
	root := newGitRepoFixture(t)
	app := gitSyncApp(t, root)
	app.sourceControlTimeout = time.Second
	service := &heldGitComparison{GitService: app.ws.Git(), started: make(chan struct{}), joined: make(chan struct{})}
	app.ws = &preparationPort{Workspace: app.ws, service: service}
	snapshot, err := service.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	app.gitPanel.SetSnapshot(snapshot)
	app.toggleWorkspacePanel(termdock.PanelGit)
	read := app.prepareGitChanges()
	result := make(chan tea.Msg, 1)
	go func() { result <- read() }()
	<-service.started
	action := app.stageGitChanges(snapshot.Changes)
	if action == nil {
		t.Fatal("Stage all did not start")
	}
	// A queued core status publication can arrive before the action completes.
	// It must not reinsert the same expensive comparison ahead of the action.
	app.presentGitSnapshot(snapshot)
	if next := app.prepareGitChangesIfVisible(); next != nil {
		t.Fatal("passive status delivery queued a comparison ahead of Stage all")
	}
	message := action().(sourceControlResultMsg)
	old := (<-result).(gitChangesPreparedMsg)
	app.applyGitChangesPrepared(old)
	if message.err != nil || len(message.snapshot.StagedPaths()) != 1 {
		t.Fatalf("obsolete comparison blocked the real index action: %+v", message)
	}
	app.applySourceControlResult(message)
	if app.gitPanel.Snapshot().Changes[0].Area != workspace.GitAreaStaged {
		t.Fatal("real index update did not reach the panel")
	}
}

func (s *preparationService) CompareChanges(ctx context.Context, changes []workspace.GitChange) ([]workspace.GitChangeComparison, error) {
	return s.compare(ctx, changes)
}

func TestGitPreparationTimeoutRetryAndCleanTransition(t *testing.T) {
	app := gitSyncApp(t, newGitRepoFixture(t))
	app.width, app.height = 160, 44
	change := app.gitPanel.Snapshot().Changes[0]
	pumpGit(t, app, app.openGitPanel(gitpanel.ModeChanges))
	pumpGit(t, app, app.openGitFileReview(change))
	var attempts atomic.Int32
	app.ws = &preparationPort{Workspace: app.ws, service: &preparationService{GitService: app.ws.Git(), compare: func(ctx context.Context, changes []workspace.GitChange) ([]workspace.GitChangeComparison, error) {
		if attempts.Add(1) == 1 {
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return []workspace.GitChangeComparison{{Change: changes[0], Comparison: workspace.GitComparison{Path: change.Path, Before: []byte("old\n"), After: []byte("fresh marker\n"), BeforePresent: true, AfterPresent: true, Fingerprint: "fresh"}}}, nil
	}}}
	app.sourceControlTimeout = 20 * time.Millisecond
	msg := app.prepareGitChanges()()
	app.applyGitChangesPrepared(msg.(gitChangesPreparedMsg))
	app.paintShellFrame()
	if text := bufferPlain(app.mainSurf.Buffer()); !strings.Contains(text, "deadline exceeded") || strings.Contains(text, "Preparing diff") {
		t.Fatalf("timeout not settled:\n%s", text)
	}
	app.applyGitChangesPrepared(app.prepareGitChanges()().(gitChangesPreparedMsg))
	app.paintShellFrame()
	if text := bufferPlain(app.mainSurf.Buffer()); !strings.Contains(text, "fresh marker") || strings.Contains(text, "deadline exceeded") {
		t.Fatalf("retry not installed:\n%s", text)
	}
	app.gitPanel.SetSnapshot(gitDockSnapshot(app.ws.RootPath()))
	if cmd := app.prepareGitChanges(); cmd != nil {
		t.Fatal("clean state scheduled unnecessary comparison")
	}
	app.paintShellFrame()
	if text := bufferPlain(app.mainSurf.Buffer()); strings.Contains(text, "fresh marker") || strings.Contains(text, "Preparing diff") || !strings.Contains(text, "No changes under "+change.Path) {
		t.Fatalf("clean transition stale:\n%s", text)
	}
}

func TestGitPreparationSupersedesWithoutPublishingOldEvidence(t *testing.T) {
	app := newFullTestApp(t)
	change := workspace.GitChange{Path: "a.txt", Area: workspace.GitAreaWorking}
	app.gitPanel.SetSnapshot(gitDockSnapshot(app.ws.RootPath(), change))
	app.gitPrepareInFlight = true
	app.gitPrepareGen = 1
	app.gitPrepareCancel = func() {}
	app.prepareGitChanges()
	files := gitdiff.PrepareChanges([]workspace.GitChangeComparison{{Change: change, Comparison: workspace.GitComparison{Before: []byte("old"), After: []byte("stale")}}})
	app.applyGitChangesPrepared(gitChangesPreparedMsg{workspace: app.workspaceSwitchGen.Load(), generation: 1, files: files})
	if len(app.gitPanel.PreparedList()) != 0 {
		t.Fatal("superseded evidence became visible")
	}
}

func TestComparisonShutdownJoinsQueuedAndRunningWork(t *testing.T) {
	app := newFullTestApp(t)
	ctx, cancel := context.WithCancel(context.Background())
	var runs atomic.Int32
	command := app.comparisonCommand(cancel, func() tea.Msg { runs.Add(1); <-ctx.Done(); return nil })
	app.StopAsyncIO()
	command()
	if runs.Load() != 1 || len(app.comparisonWork) != 0 {
		t.Fatal("queued work leaked or ran twice")
	}
}

func TestGitPreparationRealRepositoryReviewAndClean(t *testing.T) {
	root := newGitRepoFixture(t)
	app := gitSyncApp(t, root)
	app.width, app.height = 160, 44
	app.toggleWorkspacePanel(termdock.PanelGit)
	pumpGit(t, app, app.refreshSourceControl())
	if len(app.gitPanel.PreparedList()) != 1 {
		t.Fatal("actual repository not prepared")
	}
	if err := os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("one\n"), 0600); err != nil {
		t.Fatal(err)
	}
	pumpGit(t, app, app.refreshSourceControl())
	if len(app.gitPanel.PreparedList()) != 0 {
		t.Fatal("clean repository retained previous diff")
	}
}

func TestComparisonSearchDeliveredThroughAppAfterFocusChangeAndClose(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 160, 44
	change := workspace.GitChange{Path: "a.txt", Area: workspace.GitAreaWorking, Code: "M"}
	app.openGitFileReview(change)
	view := app.activeGitReview()
	view.screen.ShowChanges(gitdiff.PrepareChanges([]workspace.GitChangeComparison{{Change: change, Comparison: workspace.GitComparison{After: []byte("needle\n")}}}), "main")
	app.paintShellFrame()
	app.Update(tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl})
	for _, r := range "needle" {
		app.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	if command := view.screen.Document().SearchCommand(); command != nil {
		t.Fatal("App did not own search command delivery")
	}
	// The App owns the exact job's result even when its command was not executed
	// by this deterministic loop. Start a fresh query and feed its completion.
	view.screen.Document().SetQuery("needle")
	command := view.screen.Document().SearchCommand()
	result := command().(diffview.SearchReadyMsg)
	app.rootHost.Focus(ui.ComponentID("other"))
	app.Update(result)
	if view.screen.Document().SearchStatus() != "1 / 1 matches" {
		t.Fatal("focus change lost completion")
	}
	view.screen.Document().SetQuery("other")
	late := view.screen.Document().SearchCommand()()
	app.closeGitReview(view.tab)
	if late.(diffview.SearchReadyMsg).Apply() {
		t.Fatal("closed reader accepted search result")
	}
}

func TestGitPreparationWorkspaceBoundaryRejectsLateWork(t *testing.T) {
	app := newFullTestApp(t)
	change := workspace.GitChange{Path: "a.txt", Area: workspace.GitAreaWorking}
	app.gitPanel.SetSnapshot(gitDockSnapshot(app.ws.RootPath(), change))
	app.gitPrepareGen = 5
	app.gitPrepareInFlight = true
	cancelled := false
	app.gitPrepareCancel = func() { cancelled = true }
	old := gitChangesPreparedMsg{workspace: app.workspaceSwitchGen.Load(), generation: 5, files: gitdiff.PrepareChanges([]workspace.GitChangeComparison{{Change: change, Comparison: workspace.GitComparison{After: []byte("old workspace")}}})}
	app.beginWorkspaceSwitch()
	app.applyGitChangesPrepared(old)
	if !cancelled || app.gitPrepareInFlight || len(app.gitPanel.PreparedList()) != 0 {
		t.Fatal("workspace boundary admitted old preparation or kept worker ownership")
	}
}

func TestGitHistoryPreviewDoesNotShareCommitTabSequence(t *testing.T) {
	app := newFullTestApp(t)
	commit := workspace.GitCommitSummary{Hash: strings.Repeat("a", 40), ShortHash: "aaaaaaa"}
	app.inspectGitCommit(commit)
	preview := app.gitPreviewSequence
	app.openGitCommitReview(workspace.GitCommitSummary{Hash: strings.Repeat("b", 40), ShortHash: "bbbbbbb"})
	app.applyGitEvidence(gitEvidenceMsg{workspace: app.workspaceSwitchGen.Load(), sequence: preview, err: errors.New("preview failure marker")})
	if app.gitPreviewSequence == app.gitEvidenceSequence {
		t.Fatal("commit tab reused preview sequence")
	}
	app.gitPanel.SetMode(gitpanel.ModeHistory)
	app.toggleWorkspacePanel(termdock.PanelGit)
	// Completing the preview must settle its exact pending state even while a
	// different commit tab owns the newest request sequence.
	if app.gitEvidenceCancel[0] != nil {
		t.Fatal("preview completion lost behind commit-tab request")
	}
}
