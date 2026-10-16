package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"crude/localhistory"
)

func gitMust(t *testing.T, dir string, args ...string) {
	t.Helper()
	if err := runGit(t, dir, append([]string{"-c", "user.name=T", "-c", "user.email=t@example.test", "-c", "init.defaultBranch=main"}, args...)...); err != nil {
		t.Fatalf("git %v failed", args)
	}
}

func writeFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// remoteFixture is a bare origin with two clones that each committed a
// conflicting edit; "theirs" is already pushed.
func remoteFixture(t *testing.T) (origin, ours, theirs string) {
	t.Helper()
	base := t.TempDir()
	origin, ours, theirs = filepath.Join(base, "origin.git"), filepath.Join(base, "ours"), filepath.Join(base, "theirs")
	gitMust(t, "", "init", "--bare", "-b", "main", origin)
	gitMust(t, "", "clone", origin, theirs)
	gitMust(t, theirs, "config", "user.name", "T")
	gitMust(t, theirs, "config", "user.email", "t@example.test")
	writeFile(t, filepath.Join(theirs, "app.txt"), "header\nvalue = 1\nfooter\n")
	gitMust(t, theirs, "add", ".")
	gitMust(t, theirs, "commit", "-m", "base")
	gitMust(t, theirs, "push", "-u", "origin", "main")
	gitMust(t, "", "clone", origin, ours)
	gitMust(t, ours, "config", "user.name", "T")
	gitMust(t, ours, "config", "user.email", "t@example.test")
	writeFile(t, filepath.Join(theirs, "app.txt"), "header\nvalue = 2\nfooter\n")
	gitMust(t, theirs, "commit", "-am", "theirs")
	gitMust(t, theirs, "push")
	writeFile(t, filepath.Join(ours, "app.txt"), "header\nvalue = 3\nfooter\n")
	gitMust(t, ours, "commit", "-am", "ours")
	return origin, ours, theirs
}

// The whole remote journey: pull stops on a conflict, the conflict is parsed,
// resolved through the guarded writer with a recovery snapshot, the merge is
// completed and pushed without force.
func TestGitPullConflictResolveCompleteAndPush(t *testing.T) {
	origin, ours, _ := remoteFixture(t)
	service := NewGitService(ours)

	if err := service.Fetch(t.Context()); err != nil {
		t.Fatal(err)
	}
	snapshot, err := service.Snapshot(t.Context())
	if err != nil || snapshot.Ahead != 1 || snapshot.Behind != 1 || len(snapshot.Remotes) != 1 {
		t.Fatalf("after fetch: ahead=%d behind=%d remotes=%v err=%v", snapshot.Ahead, snapshot.Behind, snapshot.Remotes, err)
	}
	result, err := service.Pull(t.Context())
	if err != nil || len(result.Conflicts) != 1 || result.Conflicts[0] != "app.txt" {
		t.Fatalf("pull = %+v err=%v", result, err)
	}
	snapshot, _ = service.Snapshot(t.Context())
	if snapshot.Operation != GitOperationMerge || len(snapshot.ConflictedPaths()) != 1 {
		t.Fatalf("snapshot mid-merge: operation=%q conflicts=%v", snapshot.Operation, snapshot.ConflictedPaths())
	}
	if err := service.ContinueOperation(t.Context()); err == nil || !strings.Contains(err.Error(), "still have conflicts") {
		t.Fatalf("completing with conflicts = %v", err)
	}

	file, err := service.ConflictFile(t.Context(), "app.txt")
	if err != nil {
		t.Fatal(err)
	}
	regions := file.Conflicts()
	if len(regions) != 1 || strings.Join(regions[0].Current, "") != "value = 3\n" || strings.Join(regions[0].Incoming, "") != "value = 2\n" {
		t.Fatalf("parsed conflict = %+v", regions)
	}
	store, err := localhistory.Open(filepath.Join(t.TempDir(), "history"), ours)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	owner := &Workspace{RootPath: ours, Git: service, LocalHistory: store}
	if _, err := owner.ResolveGitConflict(t.Context(), "app.txt", "stale", []GitConflictChoice{GitChooseBoth}); !errors.Is(err, ErrGitReviewStale) {
		t.Fatalf("stale fingerprint = %v", err)
	}
	safety, err := owner.ResolveGitConflict(t.Context(), "app.txt", file.Fingerprint, []GitConflictChoice{GitChooseBoth})
	if err != nil || safety == "" {
		t.Fatalf("resolve = %q %v", safety, err)
	}
	if data, _ := os.ReadFile(filepath.Join(ours, "app.txt")); string(data) != "header\nvalue = 3\nvalue = 2\nfooter\n" {
		t.Fatalf("resolved file = %q", data)
	}
	if err := service.ContinueOperation(t.Context()); err != nil {
		t.Fatal(err)
	}
	snapshot, _ = service.Snapshot(t.Context())
	if snapshot.Operation != "" || len(snapshot.Changes) != 0 || snapshot.Ahead != 2 {
		t.Fatalf("after completing: operation=%q changes=%v ahead=%d", snapshot.Operation, snapshot.Changes, snapshot.Ahead)
	}
	if err := service.Push(t.Context()); err != nil {
		t.Fatal(err)
	}
	snapshot, _ = service.Snapshot(t.Context())
	if snapshot.Ahead != 0 || snapshot.Behind != 0 {
		t.Fatalf("after push: ahead=%d behind=%d", snapshot.Ahead, snapshot.Behind)
	}
	_ = origin
}

func TestGitMergeConflictAbortRestoresBranch(t *testing.T) {
	_, ours, _ := remoteFixture(t)
	service := NewGitService(ours)
	if err := service.Fetch(t.Context()); err != nil {
		t.Fatal(err)
	}
	before, _ := service.Snapshot(t.Context())
	result, err := service.Merge(t.Context(), "origin/main")
	if err != nil || len(result.Conflicts) != 1 {
		t.Fatalf("merge = %+v %v", result, err)
	}
	if err := service.AbortOperation(t.Context()); err != nil {
		t.Fatal(err)
	}
	after, _ := service.Snapshot(t.Context())
	if after.Operation != "" || after.Head != before.Head || len(after.Changes) != 0 {
		t.Fatalf("abort left operation=%q head changed=%v changes=%v", after.Operation, after.Head != before.Head, after.Changes)
	}
	if _, err := service.Merge(t.Context(), "--no-ff"); err == nil {
		t.Fatal("an option was accepted as a merge target")
	}
}

// A new branch without an upstream is published and tracked on push.
func TestGitPushPublishesNewBranch(t *testing.T) {
	_, ours, _ := remoteFixture(t)
	gitMust(t, ours, "switch", "-c", "feature")
	service := NewGitService(ours)
	if err := service.Push(t.Context()); err != nil {
		t.Fatal(err)
	}
	snapshot, _ := service.Snapshot(t.Context())
	if snapshot.Upstream != "origin/feature" {
		t.Fatalf("upstream = %q", snapshot.Upstream)
	}
}

func TestGitConflictParsingStylesAndErrors(t *testing.T) {
	text := "a\r\n<<<<<<< HEAD\r\nmine\r\n||||||| base\r\norig\r\n=======\r\ntheirs\r\n>>>>>>> feature\r\nz"
	segments, err := ParseGitConflicts(text)
	if err != nil {
		t.Fatal(err)
	}
	if len(segments) != 3 || segments[1].Conflict.CurrentLabel != "HEAD" || segments[1].Conflict.IncomingLabel != "feature" || strings.Join(segments[1].Conflict.Base, "") != "orig\r\n" {
		t.Fatalf("diff3 segments = %+v", segments)
	}
	for choice, want := range map[GitConflictChoice]string{
		GitChooseCurrent:  "a\r\nmine\r\nz",
		GitChooseIncoming: "a\r\ntheirs\r\nz",
		GitChooseBoth:     "a\r\nmine\r\ntheirs\r\nz",
	} {
		got, err := ResolveGitConflicts(segments, []GitConflictChoice{choice})
		if err != nil || got != want {
			t.Errorf("%s = %q %v, want %q", choice, got, err, want)
		}
	}
	if _, err := ResolveGitConflicts(segments, []GitConflictChoice{GitChooseNone}); err == nil {
		t.Fatal("an undecided conflict was written")
	}
	if _, err := ParseGitConflicts("<<<<<<< HEAD\nmine\n=======\n"); err == nil {
		t.Fatal("an unclosed conflict parsed")
	}
	noNewline, _ := ParseGitConflicts("<<<<<<< HEAD\nmine\n=======\ntheirs\n>>>>>>> b\n")
	noNewline[0].Conflict.Current[0] = "mine"
	if got, _ := ResolveGitConflicts(noNewline, []GitConflictChoice{GitChooseBoth}); got != "mine\ntheirs\n" {
		t.Fatalf("both without a trailing newline = %q", got)
	}
}

func TestGitCredentialFailuresAreRecognised(t *testing.T) {
	for _, output := range []string{
		"fatal: could not read Username for 'https://github.com': terminal prompts disabled",
		"git@github.com: Permission denied (publickey).\nfatal: Could not read from remote repository.",
	} {
		if !gitNeedsCredentials(output) {
			t.Errorf("not recognised: %q", output)
		}
	}
	if gitNeedsCredentials("fatal: couldn't find remote ref main") {
		t.Error("an ordinary failure was reported as a credential problem")
	}
}
