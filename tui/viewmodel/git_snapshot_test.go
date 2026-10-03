package viewmodel

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"crude/tui/filetree"
	ttypes "crude/tui/types"
	"crude/workspace"
)

// newGitRepoFixture creates a real repository with one commit and one
// uncommitted modification, so a silent reconcile read has a branch, a change
// count, and history to project.
func newGitRepoFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	runGitFixture(t, root, "init")
	runGitFixture(t, root, "checkout", "-B", "work")
	if err := os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGitFixture(t, root, "add", "--", "tracked.txt")
	runGitFixture(t, root, "commit", "-m", "initial")
	if err := os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("two\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

func runGitFixture(t *testing.T, dir string, args ...string) {
	t.Helper()
	full := append([]string{"-C", dir, "-c", "user.name=Crude Test", "-c", "user.email=crude@example.test"}, args...)
	if out, err := exec.Command("git", full...).CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
}

// readGitSnapshotNow runs one silent reconcile read to completion, exactly as
// the tick loop would, and applies its result.
func readGitSnapshotNow(t *testing.T, app *App) workspace.GitSnapshot {
	t.Helper()
	cmd := app.startGitSnapshotRead()
	if cmd == nil {
		t.Fatal("silent reconcile read did not start")
	}
	msg, ok := cmd().(sourceControlResultMsg)
	if !ok {
		t.Fatalf("reconcile command returned %T, want sourceControlResultMsg", cmd())
	}
	if !msg.silent {
		t.Fatal("reconcile read must be marked silent")
	}
	if msg.err != nil {
		t.Fatalf("reconcile read failed: %v", msg.err)
	}
	if cmd := app.applySourceControlResult(msg); cmd != nil {
		t.Fatal("a silent result must not chain tree work the watchers already did")
	}
	return app.sourceControl.Snapshot()
}

// headerGitSegment returns the painted header's Git identity segment (the
// clickable branch segment that owns the branch, counts, and tracking).
func headerGitSegment(t *testing.T, app *App) string {
	t.Helper()
	for _, segment := range app.statusHeaderLeftSegments() {
		if segment.Action == "git-branch" {
			return segment.Text
		}
	}
	return ""
}

// The header identity must follow the repository on its own: an event burst
// coalesces into one read, and a continuous burst may not hold it stale past
// the hold limit.
func TestHeaderGitReconcileBurstTiming(t *testing.T) {
	app := newTestApp(t)
	base := time.Now()

	app.requestGitSnapshot(base)
	if app.gitSnapshotReady(base.Add(100 * time.Millisecond)) {
		t.Fatal("reconcile ran before the repository event burst settled")
	}
	if !app.gitSnapshotReady(base.Add(gitSnapshotIdleDelay)) {
		t.Fatal("reconcile never followed the settled burst")
	}

	for i := 0; i < 40; i++ {
		app.requestGitSnapshot(base.Add(time.Duration(i) * 100 * time.Millisecond))
	}
	if app.gitSnapshotReady(base.Add(1500 * time.Millisecond)) {
		t.Fatal("reconcile ran mid-burst instead of waiting for the typing-pause analogue")
	}
	if !app.gitSnapshotReady(base.Add(gitSnapshotHoldLimit)) {
		t.Fatal("continuous event burst held the header identity stale past the hold limit")
	}

	// Quiet repository: the fallback bounds staleness when no event arrives at
	// all, and never fires before the first read settled.
	app.gitSnapshotEventAt = time.Time{}
	app.gitSnapshotBurstAt = time.Time{}
	app.gitSnapshotAt = time.Time{}
	if app.gitSnapshotReady(base.Add(time.Hour)) {
		t.Fatal("fallback ran before any snapshot settled")
	}
	app.gitSnapshotAt = base
	if app.gitSnapshotReady(base.Add(gitSnapshotFallback - time.Second)) {
		t.Fatal("fallback reconcile ran before its interval elapsed")
	}
	if !app.gitSnapshotReady(base.Add(gitSnapshotFallback)) {
		t.Fatal("no watcher event ever reconciles the header again")
	}
}

// A watcher event must reach the header identity through the tick loop without
// the user opening the Source Control panel, and it must not supersede an
// explicit read: a superseded result loses that operation's notice, commit
// clear, and error report.
func TestRepositoryEventReconcilesHeaderWithoutOpeningPanel(t *testing.T) {
	app := newTestApp(t)
	root := newGitRepoFixture(t)
	app.RootPath = root
	app.Git = workspace.NewGitService(root)
	treeModel := filetree.NewFileTreeModel(root)
	app.Tree = NewTreeState(treeModel, 24)
	t.Cleanup(treeModel.StopWatcher)

	if _, cmd := app.Update(filetree.TreeGitChangedMsg{Model: treeModel}); cmd == nil {
		t.Fatal("repository metadata change did not even refresh the tree")
	}
	if app.gitSnapshotEventAt.IsZero() {
		t.Fatal("repository metadata change did not arm the header reconcile")
	}

	// Settle the burst: the tick loop is what starts the read.
	app.gitSnapshotEventAt = time.Now().Add(-gitSnapshotIdleDelay)
	app.gitSnapshotBurstAt = app.gitSnapshotEventAt
	if _, _ = app.Update(ttypes.TickMsg(time.Now())); app.gitReadsInFlight != 1 {
		t.Fatalf("in-flight Git reads = %d, want the reconcile read started by the tick", app.gitReadsInFlight)
	}

	// An explicit read owns the pipeline: reconciliation waits for it, then
	// still reconciles the event that arrived meanwhile.
	app.gitSnapshotEventAt = time.Now()
	if app.gitSnapshotReady(time.Now().Add(time.Hour)) {
		t.Fatal("reconcile started while a Git read was in flight")
	}
	if cmd := app.startGitSnapshotRead(); cmd != nil {
		t.Fatal("reconcile superseded the in-flight read")
	}
	gen := app.gitRefreshGen.Load()
	app.applySourceControlResult(sourceControlResultMsg{gen: gen, snapshot: workspace.GitSnapshot{Branch: "work"}})
	if app.gitReadsInFlight != 0 {
		t.Fatalf("in-flight reads = %d after settling, want 0", app.gitReadsInFlight)
	}
	app.gitSnapshotEventAt = time.Now().Add(-gitSnapshotIdleDelay)
	if !app.gitSnapshotReady(time.Now()) {
		t.Fatal("the event that arrived during the explicit read was dropped")
	}
}

// The painted header must follow the repository: branch switches and the
// changed-file count both come from the reconciled snapshot.
func TestHeaderGitIdentityFollowsRepository(t *testing.T) {
	app := newTestApp(t)
	root := newGitRepoFixture(t)
	app.RootPath = root
	app.Git = workspace.NewGitService(root)

	snapshot := readGitSnapshotNow(t, app)
	if snapshot.Branch != "work" {
		t.Fatalf("branch = %q, want the fixture branch", snapshot.Branch)
	}
	if len(snapshot.Changes) != 1 {
		t.Fatalf("changes = %#v, want the uncommitted modification", snapshot.Changes)
	}
	if segment := headerGitSegment(t, app); !strings.Contains(segment, "work") || !strings.Contains(segment, "✎ 1") {
		t.Fatalf("header Git segment = %q, want the branch and one changed file", segment)
	}

	// An external tool commits and switches branches; the next reconcile is the
	// only thing standing between the header and a stale identity.
	runGitFixture(t, root, "add", "--", "tracked.txt")
	runGitFixture(t, root, "commit", "-m", "external commit")
	runGitFixture(t, root, "checkout", "-B", "other")
	snapshot = readGitSnapshotNow(t, app)
	if snapshot.Branch != "other" || len(snapshot.Changes) != 0 {
		t.Fatalf("reconciled snapshot = %+v, want branch other with a clean tree", snapshot)
	}
	if segment := headerGitSegment(t, app); strings.Contains(segment, "✎") {
		t.Fatalf("header Git segment = %q, want the change count dropped after the commit", segment)
	}
	identity := headerGitSegment(t, app)
	if !strings.Contains(identity, "other") {
		t.Fatalf("header Git segment = %q, want the new branch", identity)
	}
}

// A silent reconcile that finds the same projection must not rebuild the panel
// list or invalidate the editor's gutter annotation (a Git read of its own).
func TestSilentGitSnapshotSkipsUnchangedProjection(t *testing.T) {
	app := newTestApp(t)
	newSaveTestEditorAt(t, app, filepath.Join(t.TempDir(), "edit.go"), "package main\n")
	snapshot := workspace.GitSnapshot{
		Branch:  "main",
		Changes: []workspace.GitChange{{Path: "a.go", Area: workspace.GitAreaWorking, Code: "M"}},
	}
	app.sourceControl.SetSnapshot(snapshot)

	apply := func(next workspace.GitSnapshot) {
		t.Helper()
		gen := app.gitRefreshGen.Add(1)
		app.gitReadsInFlight++
		if cmd := app.applySourceControlResult(sourceControlResultMsg{gen: gen, snapshot: next, silent: true}); cmd != nil {
			t.Fatal("silent result chained tree work")
		}
	}

	app.editorGitStale.Store(false)
	apply(snapshot)
	if app.editorGitStale.Load() {
		t.Fatal("unchanged projection invalidated the gutter annotation")
	}

	changed := snapshot
	changed.Changes = append(append([]workspace.GitChange(nil), snapshot.Changes...),
		workspace.GitChange{Path: "b.go", Area: workspace.GitAreaUntracked, Code: "U"})
	apply(changed)
	if !app.editorGitStale.Load() {
		t.Fatal("moved repository state must invalidate the gutter annotation")
	}
	if got := len(app.sourceControl.Snapshot().Changes); got != 2 {
		t.Fatalf("panel changes = %d, want the reconciled projection", got)
	}
}

// Nobody asked for a reconcile read, so it must not report failures, record
// status operations, or flash the panel into its loading state — while an
// explicit refresh keeps reporting exactly as before.
func TestSilentGitSnapshotStaysSilent(t *testing.T) {
	app := newTestApp(t)
	app.Logger = stubLogger{}
	// No read in this test is executed: it asserts the presentation a read is
	// allowed to produce, so the service never touches a real repository.
	app.Git = workspace.NewGitService(t.TempDir())
	app.Tree.Visible = true
	app.setActivity(activityGit, true)
	snapshot := workspace.GitSnapshot{
		Branch:  "main",
		Changes: []workspace.GitChange{{Path: "a.go", Area: workspace.GitAreaWorking, Code: "M"}},
	}
	app.sourceControl.SetSnapshot(snapshot)

	failed := app.gitRefreshGen.Add(1)
	app.gitReadsInFlight++
	app.applySourceControlResult(sourceControlResultMsg{
		gen: failed, silent: true, err: errors.New("open Git repository: repository does not exist"),
	})
	if app.gitReadsInFlight != 0 {
		t.Fatalf("failed read left %d slots in flight", app.gitReadsInFlight)
	}
	if branch := app.sourceControl.Snapshot().Branch; branch != "main" {
		t.Fatalf("failed reconcile blanked the painted projection: %q", branch)
	}
	if issues := app.Status.Issues(); len(issues) != 0 {
		t.Fatalf("silent failure raised issues: %#v", issues)
	}
	if notices := app.Status.Notifications(true); len(notices) != 0 {
		t.Fatalf("silent failure notified the user: %#v", notices)
	}
	if ops := app.Status.Operations(); len(ops) != 0 {
		t.Fatalf("silent failure recorded operations: %#v", ops)
	}

	// While an unrequested read is in flight the panel keeps painting the
	// projection it already has: no loading flash for work nobody asked for.
	if cmd := app.startGitSnapshotRead(); cmd == nil {
		t.Fatal("reconcile read did not start")
	}
	app.paintShellFrame()
	if plain := bufferPlain(app.mainSurf.Buffer()); !strings.Contains(plain, "Changes · 1") || strings.Contains(plain, "Refreshing repository") {
		t.Fatalf("unrequested read flashed the panel loading state:\n%s", plain)
	}
	settled := app.gitRefreshGen.Load()
	app.applySourceControlResult(sourceControlResultMsg{gen: settled, snapshot: snapshot, silent: true})

	// An explicit refresh still owns the loading state and its failure report.
	app.refreshSourceControl()
	app.paintShellFrame()
	if plain := bufferPlain(app.mainSurf.Buffer()); !strings.Contains(plain, "Refreshing repository") {
		t.Fatalf("explicit refresh no longer shows the loading state:\n%s", plain)
	}
	explicit := app.gitRefreshGen.Load()
	app.applySourceControlResult(sourceControlResultMsg{gen: explicit, err: errors.New("boom")})
	if issues := app.Status.Issues(); len(issues) != 1 {
		t.Fatalf("explicit failure issues = %#v, want one reported issue", issues)
	}
	if notices := app.Status.Notifications(true); len(notices) != 1 {
		t.Fatalf("explicit failure notices = %#v, want one error notification", notices)
	}
}

// A workspace boundary abandons in-flight reads: their results may not consume
// the replacement project's slot, and a rejected switch gives the slots back.
func TestWorkspaceSwitchRebasesInFlightGitReads(t *testing.T) {
	app := newTestApp(t)
	app.gitRefreshGen.Store(7)
	app.gitReadsInFlight = 2

	state := app.beginWorkspaceSwitch()
	if app.gitReadsInFlight != 0 {
		t.Fatalf("switch left %d abandoned reads owning the new project's slot", app.gitReadsInFlight)
	}
	app.applySourceControlResult(sourceControlResultMsg{
		gen: 7, silent: true, snapshot: workspace.GitSnapshot{Branch: "old-branch"},
	})
	if app.gitReadsInFlight != 0 {
		t.Fatal("an abandoned read stole a slot from the replacement project")
	}
	if branch := app.sourceControl.Snapshot().Branch; branch != "" {
		t.Fatalf("an abandoned read painted the old project: %q", branch)
	}

	state.restore(app)
	if app.gitReadsInFlight != 2 {
		t.Fatalf("rejected switch lost in-flight reads: %d, want 2", app.gitReadsInFlight)
	}
}
