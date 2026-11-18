package localhistory

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestRestoreNeverRecursivelyDeletesUnselectedDirectoryData(t *testing.T) {
	store, workspace := newTestStore(t)
	writeTestFile(t, workspace, ".gitignore", []byte("tree/secret\n"), 0o644)
	base, err := store.Snapshot(context.Background(), SnapshotOptions{UseGitIgnore: true})
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, workspace, "tree/tracked.txt", []byte("agent"), 0o644)
	writeTestFile(t, workspace, "tree/secret", []byte("user secret"), 0o600)
	plan, err := store.PlanRestore(context.Background(), base.ID, SnapshotOptions{UseGitIgnore: true})
	if err != nil {
		t.Fatal(err)
	}
	var directory Change
	for _, change := range plan.Changes {
		if change.Path == "tree" {
			directory = change
		}
	}
	if directory.ConflictReason == "" || directory.ExactAllowed {
		t.Fatalf("directory deletion was not guarded: %+v", directory)
	}
	if _, err := store.ApplyRestoreChoices(context.Background(), plan.ID, []RestoreChoice{{Path: "tree", Exact: true}}); err == nil {
		t.Fatal("unsafe directory deletion was accepted")
	}
	for path, want := range map[string]string{"tree/tracked.txt": "agent", "tree/secret": "user secret"} {
		if got := string(readTestFile(t, workspace, path)); got != want {
			t.Fatalf("%s changed to %q", path, got)
		}
	}
}

func TestPartialHardlinkRestoreRejectsChangedSource(t *testing.T) {
	store, workspace := newTestStore(t)
	writeTestFile(t, workspace, "a", []byte("old"), 0o644)
	if err := os.Link(filepath.Join(workspace, "a"), filepath.Join(workspace, "z")); err != nil {
		t.Skipf("hardlinks unavailable: %v", err)
	}
	target, err := store.Snapshot(context.Background(), SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(workspace, "z")); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, workspace, "a", []byte("new"), 0o644)
	plan, err := store.PlanRestorePaths(context.Background(), target.ID, SnapshotOptions{}, []string{"z"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ApplyRestore(context.Background(), plan.ID, []string{"z"}); err == nil || !strings.Contains(err.Error(), "hardlink source does not match") {
		t.Fatalf("partial hardlink restore error=%v", err)
	}
	if _, err := os.Lstat(filepath.Join(workspace, "z")); !os.IsNotExist(err) {
		t.Fatalf("invalid hardlink was created: %v", err)
	}
}

func TestSymlinkChainEscapeIsRejected(t *testing.T) {
	store, workspace := newTestStore(t)
	outside := filepath.Join(filepath.Dir(workspace), "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../outside", filepath.Join(workspace, "bridge")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Symlink("bridge", filepath.Join(workspace, "alias")); err != nil {
		t.Fatal(err)
	}
	target, err := store.Snapshot(context.Background(), SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(workspace, "alias")); err != nil {
		t.Fatal(err)
	}
	plan, err := store.PlanRestorePaths(context.Background(), target.ID, SnapshotOptions{}, []string{"alias"})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Changes) != 1 || plan.Changes[0].UnsafeReason == "" {
		t.Fatalf("escaping chain was considered safe: %+v", plan.Changes)
	}
	if _, err := store.ApplyRestore(context.Background(), plan.ID, []string{"alias"}); err == nil {
		t.Fatal("escaping symlink chain was restored")
	}
}

func TestInterruptedRestoreRollsBackOnOpen(t *testing.T) {
	workspace, historyRoot := t.TempDir(), t.TempDir()
	store, err := Open(historyRoot, workspace)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, workspace, "a", []byte("current a"), 0o644)
	writeTestFile(t, workspace, "b", []byte("current b"), 0o644)
	safety, err := store.Snapshot(context.Background(), SnapshotOptions{Kind: "safety_before_restore", IncludePaths: []string{"a", "b"}})
	if err != nil {
		t.Fatal(err)
	}
	paths := []string{"a", "b"}
	targetA, err := store.storeBytes(context.Background(), []byte("partially restored"), 0o644)
	if err != nil {
		t.Fatal(err)
	}
	targetB, err := store.storeBytes(context.Background(), []byte("restored b"), 0o644)
	if err != nil {
		t.Fatal(err)
	}
	intended := []Change{{Path: "a", Target: &targetA}, {Path: "b", Target: &targetB}}
	if err := store.beginRestoreTransaction(strings.Repeat("a", 64), safety.ID, paths, intended); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, workspace, "a", []byte("partially restored"), 0o644)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(historyRoot, workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if got := string(readTestFile(t, workspace, "a")); got != "current a" {
		t.Fatalf("interrupted restore was not rolled back: %q", got)
	}
	var state string
	if err := reopened.db.QueryRow(`SELECT state FROM history_restore_transactions WHERE plan_id=?`, strings.Repeat("a", 64)).Scan(&state); err != nil || state != "rolled_back" {
		t.Fatalf("transaction state=%q err=%v", state, err)
	}
}

func TestInterruptedRestoreRecoveryPreservesPostCrashWrite(t *testing.T) {
	workspace, historyRoot := t.TempDir(), t.TempDir()
	store, err := Open(historyRoot, workspace)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, workspace, "file", []byte("safety"), 0o644)
	safety, err := store.Snapshot(context.Background(), SnapshotOptions{Kind: "safety_before_restore", IncludePaths: []string{"file"}})
	if err != nil {
		t.Fatal(err)
	}
	target, err := store.storeBytes(context.Background(), []byte("restore target"), 0o644)
	if err != nil {
		t.Fatal(err)
	}
	planID := strings.Repeat("e", 64)
	if err := store.beginRestoreTransaction(planID, safety.ID, []string{"file"}, []Change{{Path: "file", Target: &target}}); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, workspace, "file", []byte("restore target"), 0o644)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, workspace, "file", []byte("post-crash external"), 0o644)
	reopened, err := Open(historyRoot, workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if got := string(readTestFile(t, workspace, "file")); got != "post-crash external" {
		t.Fatalf("recovery overwrote post-crash work: %q", got)
	}
	var state string
	if err := reopened.db.QueryRow(`SELECT state FROM history_restore_transactions WHERE plan_id=?`, planID).Scan(&state); err != nil || state != "conflicted" {
		t.Fatalf("transaction state=%q err=%v", state, err)
	}
}

func TestSnapshotAndRestorePreserveSpecialModeBits(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not expose Unix special mode bits")
	}
	store, workspace := newTestStore(t)
	path := filepath.Join(workspace, "mode")
	writeTestFile(t, workspace, "mode", []byte("content"), 0o755)
	if err := os.Chmod(path, 0o755|os.ModeSetuid); err != nil {
		t.Skipf("setuid unavailable: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode()&os.ModeSetuid == 0 {
		t.Skip("filesystem clears setuid")
	}
	target, err := store.Snapshot(context.Background(), SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	plan, err := store.PlanRestore(context.Background(), target.ID, SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ApplyRestore(context.Background(), plan.ID, []string{"mode"}); err != nil {
		t.Fatal(err)
	}
	info, err = os.Stat(path)
	if err != nil || info.Mode()&os.ModeSetuid == 0 {
		t.Fatalf("special mode was lost: mode=%v err=%v", info.Mode(), err)
	}
}

func TestRestoreTransactionPathsAreDurableJSON(t *testing.T) {
	store, _ := newTestStore(t)
	paths := []string{"a", "nested/b"}
	if err := store.beginRestoreTransaction(strings.Repeat("b", 64), strings.Repeat("c", 64), paths); err != nil {
		t.Fatal(err)
	}
	var raw string
	if err := store.db.QueryRow(`SELECT paths FROM history_restore_transactions WHERE plan_id=?`, strings.Repeat("b", 64)).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var got []string
	if err := json.Unmarshal([]byte(raw), &got); err != nil || len(got) != len(paths) {
		t.Fatalf("paths=%q decoded=%v err=%v", raw, got, err)
	}
}

func TestSelectiveUndoCaseOnlyRenameOnCaseInsensitiveFilesystem(t *testing.T) {
	store, workspace := newTestStore(t)
	upper := filepath.Join(workspace, "Case.txt")
	lower := filepath.Join(workspace, "case.txt")
	writeTestFile(t, workspace, "Case.txt", []byte("case data"), 0o644)
	upperInfo, err := os.Stat(upper)
	if err != nil {
		t.Fatal(err)
	}
	lowerInfo, err := os.Stat(lower)
	if err != nil || !os.SameFile(upperInfo, lowerInfo) {
		t.Skip("filesystem is case-sensitive")
	}
	before, err := store.Snapshot(context.Background(), SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.BeginMessageScope("session", "message", before.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	data := []byte("case data")
	if err := store.RecordAgentOperation(context.Background(), AgentOperation{
		ID: "case-rename", SessionID: "session", MessageID: "message", AgentID: "agent", Source: "move_file",
		Paths: []OperationPath{
			{Path: "Case.txt", Before: &OperationState{Type: EntryFile, Mode: 0o644, Data: data}},
			{Path: "case.txt", After: &OperationState{Type: EntryFile, Mode: 0o644, Data: data}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteAgentOperation("case-rename", true); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(upper, lower); err != nil {
		t.Fatal(err)
	}
	after, err := store.Snapshot(context.Background(), SnapshotOptions{ParentID: before.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CloseMessageScope("session", "message", after.ID, []string{"Case.txt", "case.txt"}, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	plan, err := store.PlanAgentRestore(context.Background(), "session", "message", "agent", SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ApplyRestore(context.Background(), plan.ID, []string{"Case.txt", "case.txt"}); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(workspace)
	if err != nil {
		t.Fatal(err)
	}
	foundUpper := false
	for _, entry := range entries {
		foundUpper = foundUpper || entry.Name() == "Case.txt"
		if entry.Name() == "case.txt" {
			t.Fatalf("lowercase destination remained after undo: %v", entries)
		}
	}
	if !foundUpper {
		t.Fatalf("original case was not restored: %v", entries)
	}
}

func TestRestoreRejectsLastMomentExternalWriteWithoutClobberingIt(t *testing.T) {
	store, workspace := newTestStore(t)
	writeTestFile(t, workspace, "file", []byte("target"), 0o644)
	target, err := store.Snapshot(context.Background(), SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, workspace, "file", []byte("preview current"), 0o644)
	plan, err := store.PlanRestore(context.Background(), target.ID, SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	store.beforeApply = func(_ int, _ Change) error {
		writeTestFile(t, workspace, "file", []byte("last moment external"), 0o644)
		return nil
	}
	if _, err := store.ApplyRestore(context.Background(), plan.ID, []string{"file"}); err == nil || !strings.Contains(err.Error(), "immediately before apply") {
		t.Fatalf("restore error=%v", err)
	}
	if got := string(readTestFile(t, workspace, "file")); got != "last moment external" {
		t.Fatalf("rejected restore clobbered late write: %q", got)
	}
}

func TestRollbackPreservesWriteAfterEarlierPathWasRestored(t *testing.T) {
	store, workspace := newTestStore(t)
	writeTestFile(t, workspace, "a", []byte("target a"), 0o644)
	writeTestFile(t, workspace, "b", []byte("target b"), 0o644)
	target, err := store.Snapshot(context.Background(), SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, workspace, "a", []byte("current a"), 0o644)
	writeTestFile(t, workspace, "b", []byte("current b"), 0o644)
	plan, err := store.PlanRestore(context.Background(), target.ID, SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	store.beforeApply = func(index int, _ Change) error {
		if index == 1 {
			writeTestFile(t, workspace, "a", []byte("external after a restore"), 0o644)
		}
		return nil
	}
	if _, err := store.ApplyRestore(context.Background(), plan.ID, []string{"a", "b"}); err == nil || !strings.Contains(err.Error(), "preserved newer writes at a") {
		t.Fatalf("restore error=%v", err)
	}
	if got := string(readTestFile(t, workspace, "a")); got != "external after a restore" {
		t.Fatalf("rollback clobbered later a: %q", got)
	}
	if got := string(readTestFile(t, workspace, "b")); got != "current b" {
		t.Fatalf("rollback did not restore unchanged b: %q", got)
	}
}

func TestFinalDirectoryModeFailureRollsBackAppliedChildren(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows directory permission modes are not Unix-compatible")
	}
	store, workspace := newTestStore(t)
	writeTestFile(t, workspace, "dir/file", []byte("target"), 0o644)
	if err := os.Chmod(filepath.Join(workspace, "dir"), 0o700); err != nil {
		t.Fatal(err)
	}
	target, err := store.Snapshot(context.Background(), SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, workspace, "dir/file", []byte("current"), 0o644)
	if err := os.Chmod(filepath.Join(workspace, "dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	plan, err := store.PlanRestore(context.Background(), target.ID, SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	store.beforeDirectoryMode = func(Change) error { return errors.New("injected directory chmod failure") }
	if _, err := store.ApplyRestore(context.Background(), plan.ID, []string{"dir", "dir/file"}); err == nil || !strings.Contains(err.Error(), "workspace was rolled back") {
		t.Fatalf("restore error=%v", err)
	}
	if got := string(readTestFile(t, workspace, "dir/file")); got != "current" {
		t.Fatalf("child was not rolled back: %q", got)
	}
	info, err := os.Stat(filepath.Join(workspace, "dir"))
	if err != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("directory mode=%v err=%v", info.Mode(), err)
	}
}

func TestStoreCloseWaitsForAdmittedOperationBeforeUnlock(t *testing.T) {
	workspace, historyRoot := t.TempDir(), t.TempDir()
	store, err := Open(historyRoot, workspace)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.beginOperation(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- store.Close() }()
	select {
	case err := <-done:
		t.Fatalf("store closed beneath admitted operation: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	if err := store.beginOperation(); err == nil {
		t.Fatal("store admitted a new operation while closing")
	}
	store.endOperation()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("store close did not finish after operation released")
	}
}

func TestRetentionPreservesPendingRestoreNotificationAuthority(t *testing.T) {
	store, workspace := newTestStore(t)
	writeTestFile(t, workspace, "file", []byte("one"), 0o644)
	first, err := store.Snapshot(context.Background(), SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, workspace, "file", []byte("two"), 0o644)
	second, err := store.Snapshot(context.Background(), SnapshotOptions{ParentID: first.ID})
	if err != nil {
		t.Fatal(err)
	}
	paths, _ := json.Marshal([]string{"file"})
	if _, err := store.db.Exec(`INSERT INTO history_restore_events(plan_id,target_snapshot_id,safety_snapshot_id,after_snapshot_id,session_id,message_id,agent_id,notified,created_at,paths)
VALUES(?,?,?,?,?,?,?,0,?,?)`, strings.Repeat("d", 64), first.ID, first.ID, first.ID, "session", "message", "agent", time.Now().UnixNano(), string(paths)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Prune(context.Background(), RetentionPolicy{MaxSnapshots: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadManifest(first.ID); err != nil {
		t.Fatalf("pending notification target was pruned: %v", err)
	}
	if _, err := store.LoadManifest(second.ID); err != nil {
		t.Fatalf("latest snapshot was pruned: %v", err)
	}
	pending, err := store.PendingRestoreNotifications("session")
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending notifications=%+v err=%v", pending, err)
	}
}

func TestRestoreRejectsWorkspaceRootSymlinkReplacement(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "workspace")
	if err := os.Mkdir(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	historyRoot := t.TempDir()
	writeTestFile(t, workspace, "file", []byte("target"), 0o644)
	store, err := Open(historyRoot, workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	target, err := store.Snapshot(context.Background(), SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, workspace, "file", []byte("preview current"), 0o644)
	plan, err := store.PlanRestore(context.Background(), target.ID, SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	backup := workspace + "-backup"
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.Mkdir(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, outside, "file", []byte("preview current"), 0o644)
	if err := os.Rename(workspace, backup); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, workspace); err != nil {
		_ = os.Rename(backup, workspace)
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := store.ApplyRestore(context.Background(), plan.ID, []string{"file"}); err == nil || !strings.Contains(err.Error(), "workspace root identity changed") {
		t.Fatalf("restore error=%v", err)
	}
	if got := string(readTestFile(t, outside, "file")); got != "preview current" {
		t.Fatalf("restore escaped through replaced root: %q", got)
	}
}

func TestCrashPreparedReceiptCannotClaimMatchingExternalWrite(t *testing.T) {
	workspace, historyRoot := t.TempDir(), t.TempDir()
	writeTestFile(t, workspace, "file", []byte("before"), 0o644)
	store, err := Open(historyRoot, workspace)
	if err != nil {
		t.Fatal(err)
	}
	before, err := store.Snapshot(context.Background(), SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.BeginMessageScope("session", "message", before.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordAgentOperation(context.Background(), AgentOperation{
		ID: "prepared-before-crash", SessionID: "session", MessageID: "message", AgentID: "agent", Source: "write_file",
		Paths: []OperationPath{{Path: "file", Before: &OperationState{Type: EntryFile, Mode: 0o644, Data: []byte("before")}, After: &OperationState{Type: EntryFile, Mode: 0o644, Data: []byte("intended")}}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, workspace, "file", []byte("intended"), 0o644)
	reopened, err := Open(historyRoot, workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	after, err := reopened.Snapshot(context.Background(), SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := reopened.RecoverInterruptedScopes(after.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if scopes, err := reopened.AgentUndoableScopes("session", "agent", 10); err != nil || len(scopes) != 0 {
		t.Fatalf("crash-prepared receipt claimed external write: scopes=%+v err=%v", scopes, err)
	}
}

func TestHardlinkRestoreRejectsIgnoredAliasOutsideSelection(t *testing.T) {
	store, workspace := newTestStore(t)
	writeTestFile(t, workspace, ".gitignore", []byte("secret\n"), 0o644)
	writeTestFile(t, workspace, "a", []byte("old"), 0o644)
	if err := os.Link(filepath.Join(workspace, "a"), filepath.Join(workspace, "z")); err != nil {
		t.Skipf("hardlinks unavailable: %v", err)
	}
	target, err := store.Snapshot(context.Background(), SnapshotOptions{UseGitIgnore: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Link(filepath.Join(workspace, "a"), filepath.Join(workspace, "secret")); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, workspace, "a", []byte("new"), 0o644)
	plan, err := store.PlanRestore(context.Background(), target.ID, SnapshotOptions{UseGitIgnore: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ApplyRestore(context.Background(), plan.ID, nil); err == nil || !strings.Contains(err.Error(), "hardlink alias secret is outside") {
		t.Fatalf("restore error=%v", err)
	}
	if got := string(readTestFile(t, workspace, "secret")); got != "new" {
		t.Fatalf("ignored alias changed: %q", got)
	}
}

func TestExternalRestoreRejectsReplacedParentWithMatchingFile(t *testing.T) {
	store, _ := newTestStore(t)
	externalRoot := filepath.Join(t.TempDir(), "external")
	if err := os.Mkdir(externalRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	externalPath := filepath.Join(externalRoot, "agent.txt")
	before, err := store.Snapshot(context.Background(), SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.BeginMessageScope("session", "message", before.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	key := filepath.ToSlash(externalPath)
	if err := store.RecordAgentOperation(context.Background(), AgentOperation{
		ID: "external-parent", SessionID: "session", MessageID: "message", AgentID: "agent", Source: "write_file",
		Paths: []OperationPath{{Path: key, After: &OperationState{Type: EntryFile, Mode: 0o644, Data: []byte("agent")}}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteAgentOperation("external-parent", true); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, externalRoot, "agent.txt", []byte("agent"), 0o644)
	after, err := store.Snapshot(context.Background(), SnapshotOptions{ParentID: before.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CloseMessageScope("session", "message", after.ID, nil, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	plan, err := store.PlanAgentRestore(context.Background(), "session", "message", "agent", SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	backup := externalRoot + "-original"
	if err := os.Rename(externalRoot, backup); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(externalRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, externalRoot, "agent.txt", []byte("agent"), 0o644)
	if _, err := store.ApplyRestore(context.Background(), plan.ID, []string{key}); err == nil || !strings.Contains(err.Error(), "parent directory changed") {
		t.Fatalf("restore error=%v", err)
	}
	if got := string(readTestFile(t, externalRoot, "agent.txt")); got != "agent" {
		t.Fatalf("replacement parent file changed: %q", got)
	}
}
