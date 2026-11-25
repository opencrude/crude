package viewmodel

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/core/coreclient"
	"crude/core/coreprotocol"
	"crude/core/shellport"
	"crude/tui/screens/gitmerge"
	"crude/tui/screens/gitpanel"
	"crude/tui/ui"
	"crude/types/statuscenter"
	"crude/workspace"
	"crude/workspace/localhistory"
)

func syncGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=T", "-c", "user.email=t@example.test", "-c", "init.defaultBranch=main"}, args...)...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %s", args, out)
	}
}

// conflictingClones returns a clone one commit ahead of and one behind its
// origin, with a conflicting edit to app.txt on each side.
func conflictingClones(t *testing.T) string {
	t.Helper()
	return divergedClone(t, true)
}

// divergedClone builds the conflicting clone. Without fetched, the clone has
// not seen the origin's new commit: the user forgot to pull.
func divergedClone(t *testing.T, fetched bool) string {
	t.Helper()
	base := t.TempDir()
	origin, theirs, ours := filepath.Join(base, "origin.git"), filepath.Join(base, "theirs"), filepath.Join(base, "ours")
	syncGit(t, base, "init", "--bare", "-b", "main", origin)
	syncGit(t, base, "clone", origin, theirs)
	write := func(dir, text string) {
		if err := os.WriteFile(filepath.Join(dir, "app.txt"), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(theirs, "top\nvalue = 1\nbottom\n")
	syncGit(t, theirs, "add", ".")
	syncGit(t, theirs, "commit", "-m", "base")
	syncGit(t, theirs, "push", "-u", "origin", "main")
	syncGit(t, base, "clone", origin, ours)
	for _, dir := range []string{theirs, ours} {
		syncGit(t, dir, "config", "user.name", "T")
		syncGit(t, dir, "config", "user.email", "t@example.test")
	}
	write(theirs, "top\nvalue = 2\nbottom\n")
	syncGit(t, theirs, "commit", "-am", "theirs")
	syncGit(t, theirs, "push")
	write(ours, "top\nvalue = 3\nbottom\n")
	syncGit(t, ours, "commit", "-am", "ours")
	if fetched {
		syncGit(t, ours, "fetch")
	}
	return ours
}

// pumpGit runs cmd and feeds only Git results back into the app, following
// their commands. Frame ticks and other timers are abandoned, not awaited.
func pumpGit(t *testing.T, app *App, cmd tea.Cmd) {
	t.Helper()
	queue := []tea.Cmd{cmd}
	for steps := 0; len(queue) > 0 && steps < 64; steps++ {
		next := queue[0]
		queue = queue[1:]
		if next == nil {
			continue
		}
		done := make(chan tea.Msg, 1)
		go func() { done <- next() }()
		var msg tea.Msg
		select {
		case msg = <-done:
		case <-time.After(5 * time.Second):
			continue
		}
		switch msg := msg.(type) {
		case tea.BatchMsg:
			queue = append(queue, msg...)
		case sourceControlResultMsg, gitChangesPreparedMsg, gitEvidenceMsg, gitConflictLoadedMsg, gitConflictSavedMsg, gitMergeDecisionMsg, gitMessageEditorMsg:
			_, follow := app.Update(msg)
			queue = append(queue, follow)
		}
	}
}

func gitSyncApp(t *testing.T, root string) *App {
	t.Helper()
	app := newFullTestApp(t)
	app.width, app.height = 160, 48
	ws := wsOf(app)
	ws.RootPath = root
	ws.Git = workspace.NewGitService(root)
	store, err := localhistory.Open(filepath.Join(t.TempDir(), "history"), root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ws.LocalHistory = store
	pumpGit(t, app, app.refreshSourceControl())
	return app
}

// The panel shows the sync state; Pull stops on the conflict and opens the
// resolver; the conflict is decided with the keyboard, saved through the
// guarded writer, the merge completed and the result pushed.
func TestGitPullResolveCompletePushJourney(t *testing.T) {
	root := conflictingClones(t)
	app := gitSyncApp(t, root)
	pumpGit(t, app, app.toggleGitPanel())
	app.paintShellFrame()
	plain := bufferPlain(app.mainSurf.Buffer())
	for _, want := range []string{"Pull ↓1", "Push ↑1", "Fetch"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("status row lacks %q:\n%s", want, plain)
		}
	}

	cmd, handled := app.handleGitPanelAction(ui.Action{ID: gitpanel.ActionPull})
	if !handled || app.gitPanel.RemoteBusy() != "Pull" {
		t.Fatalf("pull not started: handled=%v busy=%q", handled, app.gitPanel.RemoteBusy())
	}
	pumpGit(t, app, cmd)
	if app.gitPanel.RemoteBusy() != "" {
		t.Fatal("the busy label outlived the pull")
	}
	if !app.overlayHost.IsOpen(gitMergeOverlay) || app.gitMerge == nil {
		t.Fatal("a conflicted pull did not open the resolver")
	}
	resolver := app.gitMerge.resolver
	file, loaded := resolver.File()
	if !loaded || file.Path != "app.txt" || len(file.Conflicts()) != 1 {
		t.Fatalf("resolver file = %+v loaded=%v", file, loaded)
	}

	// Keys reach the resolver through the shell's real overlay routing.
	app.paintShellFrame()
	_, follow := app.Update(tea.KeyPressMsg{Code: '2', Text: "2"})
	pumpGit(t, app, follow)
	if got := resolver.Choices(); len(got) != 1 || got[0] != workspace.GitChooseIncoming {
		t.Fatalf("choices after key 2 = %v", got)
	}
	_, follow = app.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	pumpGit(t, app, follow)
	if data, _ := os.ReadFile(filepath.Join(root, "app.txt")); string(data) != "top\nvalue = 2\nbottom\n" {
		t.Fatalf("resolved file = %q", data)
	}
	if conflicts := resolver.Conflicts(); len(conflicts) != 0 {
		t.Fatalf("still conflicted: %v", conflicts)
	}

	cmd, _ = app.handleGitPanelAction(ui.Action{ID: gitpanel.ActionContinueOperation})
	pumpGit(t, app, cmd)
	if snapshot := app.gitPanel.Snapshot(); snapshot.Operation != "" || snapshot.Ahead != 2 {
		t.Fatalf("after completing: operation=%q ahead=%d", snapshot.Operation, snapshot.Ahead)
	}
	if app.overlayHost.IsOpen(gitMergeOverlay) {
		t.Fatal("the resolver outlived the merge")
	}
	cmd, _ = app.handleGitPanelAction(ui.Action{ID: gitpanel.ActionPush})
	pumpGit(t, app, cmd)
	if snapshot := app.gitPanel.Snapshot(); snapshot.Ahead != 0 || snapshot.Behind != 0 {
		t.Fatalf("after push: ahead=%d behind=%d", snapshot.Ahead, snapshot.Behind)
	}
}

// Stop cancels a running remote command and the panel returns to idle; a
// later refresh cannot strand the busy label of an earlier command.
func TestGitRemoteStopAndRefreshNeverStrandTheBusyLabel(t *testing.T) {
	root := conflictingClones(t)
	app := gitSyncApp(t, root)
	pumpGit(t, app, app.toggleGitPanel())
	cmd := app.runGitRemote("Fetch", "Fetched", func(ctx context.Context, _ shellport.GitService) (workspace.GitSyncResult, error) {
		<-ctx.Done()
		return workspace.GitSyncResult{}, ctx.Err()
	})
	refresh := app.refreshSourceControl() // supersedes the read generation
	pumpGit(t, app, refresh)
	app.handleGitPanelAction(ui.Action{ID: gitpanel.ActionStopRemote})
	pumpGit(t, app, cmd)
	if busy := app.gitPanel.RemoteBusy(); busy != "" {
		t.Fatalf("busy label stranded: %q", busy)
	}
	if app.gitReadsInFlight != 0 {
		t.Fatalf("read slots leaked: %d", app.gitReadsInFlight)
	}
}

// The full message editor edits a copy: Cancel keeps the panel draft, Save
// replaces it.
func TestGitMessageEditorSavesOrCancels(t *testing.T) {
	root := conflictingClones(t)
	app := gitSyncApp(t, root)
	app.gitPanel.SetMessage("Draft subject")
	app.openGitMessageEditor(app.gitPanel.Message())
	if !app.overlayHost.IsOpen(gitMessageEditorOverlay) {
		t.Fatal("editor did not open")
	}
	pumpGit(t, app, func() tea.Msg { return gitMessageEditorMsg{action: gitpanel.ActionMessageCancel, text: "discarded"} })
	if app.gitPanel.Message() != "Draft subject" || app.overlayHost.IsOpen(gitMessageEditorOverlay) {
		t.Fatalf("cancel changed the draft to %q", app.gitPanel.Message())
	}
	app.openGitMessageEditor(app.gitPanel.Message())
	pumpGit(t, app, func() tea.Msg {
		return gitMessageEditorMsg{action: gitpanel.ActionMessageSave, text: "Refine sync\n\nExplain the change."}
	})
	if app.gitPanel.Message() != "Refine sync\n\nExplain the change." {
		t.Fatalf("save did not replace the draft: %q", app.gitPanel.Message())
	}
	_ = gitmerge.DecideSave
}

// The user forgot to pull and pressed Push. The status row, the notification
// and the Pull button must explain the failure and the next step, and the
// status row must keep the reason after the refresh that follows the push.
func TestGitPushRejectedForMissingCommitsTellsTheUserToPull(t *testing.T) {
	root := divergedClone(t, false)
	app := gitSyncApp(t, root)
	pumpGit(t, app, app.toggleGitPanel())
	app.paintShellFrame()
	if plain := bufferPlain(app.mainSurf.Buffer()); !strings.Contains(plain, "Push ↑1") || strings.Contains(plain, "Pull ↓") {
		t.Fatalf("fixture: the panel must not know about the remote commit yet:\n%s", plain)
	}
	cmd, handled := app.handleGitPanelAction(ui.Action{ID: gitpanel.ActionPush})
	if !handled || cmd == nil {
		t.Fatal("Push did not start")
	}
	pumpGit(t, app, cmd)
	const want = "Push failed. The remote branch has commits that your branch does not have. Pull, then push again."
	if got := app.gitPanel.RemoteError(); got != want {
		t.Fatalf("panel failure = %q, want %q", got, want)
	}
	app.paintShellFrame()
	plain := bufferPlain(app.mainSurf.Buffer())
	// The docked status row is narrow: it shows the whole first sentence, not
	// a cut one; the notification below carries the reason and next step.
	if !strings.Contains(plain, "✕ Push failed.") || strings.Contains(plain, "Push failed. Th…") {
		t.Fatalf("the status row does not show that Push failed:\n%s", plain)
	}
	if !strings.Contains(plain, "Pull ↓1") {
		t.Fatalf("Pull does not show the commit that the push needs first:\n%s", plain)
	}
	var notice *statuscenter.NotificationRecord
	for _, record := range app.ws.Status().Notifications(false) {
		if record.Severity == statuscenter.SeverityError && strings.HasPrefix(record.Summary, "Push failed.") {
			notice = &record
		}
	}
	if notice == nil || notice.Summary != want || !strings.Contains(notice.Detail, "[rejected]") || strings.Contains(notice.Summary, "\n") {
		t.Fatalf("notification = %+v", notice)
	}
	// Pull clears the failure as soon as it starts.
	cmd, _ = app.handleGitPanelAction(ui.Action{ID: gitpanel.ActionPull})
	if app.gitPanel.RemoteError() != "" {
		t.Fatal("the old push failure outlived the next remote command")
	}
	pumpGit(t, app, cmd)
}

// A mutation superseded by a refresh still releases its read slot; a leaked
// slot stopped silent reconciliation for the rest of the session.
func TestSupersededGitMutationReleasesItsReadSlot(t *testing.T) {
	root := conflictingClones(t)
	app := gitSyncApp(t, root)
	mutation := app.sourceControlMutation("Staged", nil, func(context.Context, shellport.GitService) error { return nil })
	refresh := app.refreshSourceControl()
	pumpGit(t, app, refresh)
	pumpGit(t, app, mutation)
	if app.gitReadsInFlight != 0 {
		t.Fatalf("read slots leaked: %d", app.gitReadsInFlight)
	}
	if !app.gitSnapshotReady(time.Now().Add(time.Hour)) && app.gitSnapshotDue(time.Now().Add(time.Hour)) {
		t.Fatal("silent reconciliation stays blocked")
	}
}

type heldGitPush struct {
	shellport.GitService
	entered chan context.Context
}

func (s *heldGitPush) Push(ctx context.Context) error {
	s.entered <- ctx
	<-ctx.Done()
	return ctx.Err()
}

func TestGitUserPushHasNoDeadlineAndStopCancelsIt(t *testing.T) {
	app := gitSyncApp(t, newGitRepoFixture(t))
	service := &heldGitPush{GitService: app.ws.Git(), entered: make(chan context.Context, 1)}
	app.ws = &preparationPort{Workspace: app.ws, service: service}
	t.Cleanup(app.cancelGitRemote)
	pumpGit(t, app, app.openGitPanel(gitpanel.ModeChanges))
	cmd, handled := app.handleGitPanelAction(ui.Action{ID: gitpanel.ActionPush})
	if !handled || cmd == nil {
		t.Fatal("Push did not start")
	}
	result := make(chan tea.Msg, 1)
	go func() { result <- cmd() }()
	var ctx context.Context
	select {
	case ctx = <-service.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("Push did not reach the Git service")
	}
	if _, deadline := ctx.Deadline(); deadline {
		t.Fatal("a user-started push received a deadline before Stop")
	}
	app.paintShellFrame()
	if plain := bufferPlain(app.mainSurf.Buffer()); !strings.Contains(plain, "Stop") {
		t.Fatalf("a running push did not show Stop:\n%s", plain)
	}
	if _, handled := app.handleGitPanelAction(ui.Action{ID: gitpanel.ActionStopRemote}); !handled {
		t.Fatal("Stop was not handled")
	}
	select {
	case message := <-result:
		stopped, ok := message.(sourceControlResultMsg)
		if !ok || !errors.Is(stopped.err, context.Canceled) {
			t.Fatalf("Stop result = %+v", message)
		}
		_, command := app.Update(stopped)
		pumpGit(t, app, command)
	case <-time.After(3 * time.Second):
		t.Fatal("the Push command did not stop")
	}
	if app.gitPanel.RemoteBusy() != "" || app.gitPanel.RemoteError() != "" || app.operationIssue != nil {
		t.Fatal("a stopped push left the panel busy or was reported as a failure")
	}
}

type gitPushResultService struct {
	shellport.GitService
	result error
	calls  int
}

func (s *gitPushResultService) Push(context.Context) error {
	s.calls++
	return s.result
}

func TestGitPushKeepsOriginalOutcomeWhenReceiptCleanupFails(t *testing.T) {
	for _, tc := range []struct {
		name      string
		known     bool
		fault     *coreprotocol.Fault
		cause     error
		operation statuscenter.OperationState
		inline    string
	}{
		{"unknown", false, nil, context.Canceled, statuscenter.OperationFailed, "Push outcome unknown."},
		{"success", true, nil, &coreprotocol.Fault{Code: "cleanup_failed", Message: "the release failed"}, statuscenter.OperationSucceeded, ""},
		{"refusal", true, &coreprotocol.Fault{Code: "service_failed", Message: "Push failed. The hook refused the push."}, context.Canceled, statuscenter.OperationFailed, "The hook refused the push."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := gitSyncApp(t, newGitRepoFixture(t))
			status := wsOf(app).Status
			service := &gitPushResultService{GitService: app.ws.Git(), result: &coreclient.UncertainError{RequestID: "test-request", ResultKnown: tc.known, ResultFault: tc.fault, Cause: tc.cause}}
			app.ws = &preparationPort{Workspace: app.ws, service: service}
			cmd, handled := app.handleGitPanelAction(ui.Action{ID: gitpanel.ActionPush})
			if !handled || cmd == nil {
				t.Fatal("Push did not start")
			}
			pumpGit(t, app, cmd)
			if got := app.gitPanel.RemoteError(); !strings.Contains(got, tc.inline) || (tc.inline == "" && got != "") || strings.Contains(got, "test-request") {
				t.Fatalf("Git panel outcome = %q, want %q without the request ID", got, tc.inline)
			}
			if service.calls != 1 || app.gitPanel.RemoteBusy() != "" {
				t.Fatal("the push replayed or did not settle")
			}
			for _, operation := range status.Operations() {
				if operation.Label == "Push" {
					if operation.State != tc.operation {
						t.Fatalf("Push state = %q, want %q", operation.State, tc.operation)
					}
					return
				}
			}
			t.Fatal("Push did not record an outcome")
		})
	}
}
