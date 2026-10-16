package viewmodel

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/localhistory"
	"crude/shellport"
	"crude/tui/screens/gitmerge"
	"crude/tui/screens/gitpanel"
	"crude/tui/ui"
	"crude/workspace"
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
	syncGit(t, ours, "fetch")
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
		case sourceControlResultMsg, gitConflictLoadedMsg, gitConflictSavedMsg, gitMergeDecisionMsg, gitMessageEditorMsg:
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
