package localhistory

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func newTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	workspace := t.TempDir()
	store, err := Open(t.TempDir(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store, workspace
}

func writeTestFile(t *testing.T, root, rel string, data []byte, mode os.FileMode) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, mode); err != nil {
		t.Fatal(err)
	}
}

func readTestFile(t *testing.T, root, rel string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestHistoryAtomicWriteReplacesWithoutDeletingOnFailure(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "snapshot.json")
	for _, content := range []string{"before", "after"} {
		if err := writeBytesAtomic(path, []byte(content), 0o600); err != nil {
			t.Fatalf("publish %q: %v", content, err)
		}
		if got := string(readTestFile(t, root, "snapshot.json")); got != content {
			t.Fatalf("published bytes=%q, want %q", got, content)
		}
	}
	injected := errors.New("staging failed")
	if err := writeAtomic(path, 0o600, func(w io.Writer) error {
		if _, err := io.WriteString(w, "partial"); err != nil {
			return err
		}
		return injected
	}); !errors.Is(err, injected) {
		t.Fatalf("staging error was not preserved: %v", err)
	}
	if got := string(readTestFile(t, root, "snapshot.json")); got != "after" {
		t.Fatalf("failed write changed the destination: %q", got)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 {
		t.Fatalf("staging files leaked: %v, error=%v", entries, err)
	}
}

func TestHistoryAtomicWritePreservesReadOnlyWindowsDestination(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows read-only file replacement contract")
	}
	root := t.TempDir()
	path := filepath.Join(root, "protected.json")
	writeTestFile(t, root, "protected.json", []byte("original"), 0o600)
	if err := os.Chmod(path, 0o400); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
	if err := writeBytesAtomic(path, []byte("replacement"), 0o600); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("genuine permission failure was not reported: %v", err)
	}
	if got := string(readTestFile(t, root, "protected.json")); got != "original" {
		t.Fatalf("denied replacement destroyed the original: %q", got)
	}
}

func TestSnapshotRestoreAndUndoRestore(t *testing.T) {
	store, workspace := newTestStore(t)
	writeTestFile(t, workspace, "src/a.txt", []byte("original\n"), 0o640)
	writeTestFile(t, workspace, "delete.txt", []byte("bring me back"), 0o600)
	writeTestFile(t, workspace, "empty.bin", nil, 0o644)
	writeTestFile(t, workspace, "binary.bin", []byte{0, 1, 2, 255}, 0o644)
	if runtime.GOOS != "windows" {
		if err := os.Symlink("src/a.txt", filepath.Join(workspace, "inside-link")); err != nil {
			t.Fatal(err)
		}
	}

	base, err := store.Snapshot(context.Background(), SnapshotOptions{Kind: "baseline", UseGitIgnore: true})
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, workspace, "src/a.txt", []byte("mutated\n"), 0o600)
	if err := os.Remove(filepath.Join(workspace, "delete.txt")); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, workspace, "new.txt", []byte("new"), 0o644)

	plan, err := store.PlanRestore(context.Background(), base.ID, SnapshotOptions{UseGitIgnore: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Changes) < 3 {
		t.Fatalf("restore changes=%d want at least 3", len(plan.Changes))
	}
	result, err := store.ApplyRestore(context.Background(), plan.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.SafetySnapshotID == "" || result.AfterSnapshotID == "" {
		t.Fatalf("restore result missing safety/after IDs: %+v", result)
	}
	if got := string(readTestFile(t, workspace, "src/a.txt")); got != "original\n" {
		t.Fatalf("restored a.txt=%q", got)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(workspace, "src", "a.txt"))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o640 {
			t.Fatalf("restored mode=%v", info.Mode().Perm())
		}
	}
	if got := string(readTestFile(t, workspace, "delete.txt")); got != "bring me back" {
		t.Fatalf("restored delete.txt=%q", got)
	}
	if _, err := os.Stat(filepath.Join(workspace, "new.txt")); !os.IsNotExist(err) {
		t.Fatalf("new.txt survived restore: %v", err)
	}
	if len(readTestFile(t, workspace, "empty.bin")) != 0 {
		t.Fatal("empty file did not round-trip")
	}
	if got := readTestFile(t, workspace, "binary.bin"); !bytes.Equal(got, []byte{0, 1, 2, 255}) {
		t.Fatalf("binary restore=%v", got)
	}
	if runtime.GOOS != "windows" {
		target, err := os.Readlink(filepath.Join(workspace, "inside-link"))
		if err != nil || target != "src/a.txt" {
			t.Fatalf("symlink restore target=%q err=%v", target, err)
		}
	}

	undoPlan, err := store.PlanRestore(context.Background(), result.SafetySnapshotID, SnapshotOptions{UseGitIgnore: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ApplyRestore(context.Background(), undoPlan.ID, nil); err != nil {
		t.Fatal(err)
	}
	if got := string(readTestFile(t, workspace, "src/a.txt")); got != "mutated\n" {
		t.Fatalf("undo restore a.txt=%q", got)
	}
	if _, err := os.Stat(filepath.Join(workspace, "delete.txt")); !os.IsNotExist(err) {
		t.Fatalf("undo restore recreated delete.txt: %v", err)
	}
	if got := string(readTestFile(t, workspace, "new.txt")); got != "new" {
		t.Fatalf("undo restore new.txt=%q", got)
	}
}

func TestRestoreRejectsStalePreviewWithoutMutation(t *testing.T) {
	store, workspace := newTestStore(t)
	writeTestFile(t, workspace, "a.txt", []byte("one"), 0o644)
	base, err := store.Snapshot(context.Background(), SnapshotOptions{Kind: "baseline"})
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, workspace, "a.txt", []byte("two"), 0o644)
	plan, err := store.PlanRestore(context.Background(), base.ID, SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, workspace, "a.txt", []byte("three"), 0o644)
	if _, err := store.ApplyRestore(context.Background(), plan.ID, nil); err == nil {
		t.Fatal("stale restore plan was accepted")
	}
	if got := string(readTestFile(t, workspace, "a.txt")); got != "three" {
		t.Fatalf("stale rejection mutated file to %q", got)
	}
}

func TestRestoreFailureRollsBackSafetySnapshot(t *testing.T) {
	store, workspace := newTestStore(t)
	writeTestFile(t, workspace, "a.txt", []byte("base-a"), 0o644)
	writeTestFile(t, workspace, "b.txt", []byte("base-b"), 0o644)
	base, err := store.Snapshot(context.Background(), SnapshotOptions{Kind: "baseline"})
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, workspace, "a.txt", []byte("current-a"), 0o644)
	writeTestFile(t, workspace, "b.txt", []byte("current-b"), 0o644)
	plan, err := store.PlanRestore(context.Background(), base.ID, SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	store.beforeApply = func(index int, _ Change) error {
		if index == 1 {
			return errors.New("injected apply failure")
		}
		return nil
	}
	if _, err := store.ApplyRestore(context.Background(), plan.ID, nil); err == nil {
		t.Fatal("injected failure did not fail restore")
	}
	if got := string(readTestFile(t, workspace, "a.txt")); got != "current-a" {
		t.Fatalf("rollback a.txt=%q", got)
	}
	if got := string(readTestFile(t, workspace, "b.txt")); got != "current-b" {
		t.Fatalf("rollback b.txt=%q", got)
	}
}

func TestSnapshotUsesContentDefinedChunkDeduplication(t *testing.T) {
	store, workspace := newTestStore(t)
	rng := rand.New(rand.NewSource(42))
	data := make([]byte, 12*1024*1024)
	if _, err := rng.Read(data); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, workspace, "large.bin", data, 0o644)
	first, err := store.Snapshot(context.Background(), SnapshotOptions{Kind: "first", MaxFileSize: 20 * 1024 * 1024})
	if err != nil {
		t.Fatal(err)
	}
	copy(data[len(data)/2:len(data)/2+64], bytes.Repeat([]byte{0x7f}, 64))
	writeTestFile(t, workspace, "large.bin", data, 0o644)
	second, err := store.Snapshot(context.Background(), SnapshotOptions{Kind: "second", MaxFileSize: 20 * 1024 * 1024})
	if err != nil {
		t.Fatal(err)
	}
	firstObj, err := store.readFileObject(first.Entries["large.bin"].Hash)
	if err != nil {
		t.Fatal(err)
	}
	secondObj, err := store.readFileObject(second.Entries["large.bin"].Hash)
	if err != nil {
		t.Fatal(err)
	}
	firstChunks := make(map[string]bool)
	for _, ref := range firstObj.Chunks {
		firstChunks[ref.Hash] = true
	}
	reused := 0
	for _, ref := range secondObj.Chunks {
		if firstChunks[ref.Hash] {
			reused++
		}
	}
	if len(firstObj.Chunks) < 2 || reused == 0 {
		t.Fatalf("chunks first=%d second=%d reused=%d", len(firstObj.Chunks), len(secondObj.Chunks), reused)
	}
}

func TestSnapshotIgnoresDerivedAndGitignoredContent(t *testing.T) {
	store, workspace := newTestStore(t)
	writeTestFile(t, workspace, ".gitignore", []byte("ignored.txt\n"), 0o644)
	writeTestFile(t, workspace, "ignored.txt", []byte("ignored"), 0o644)
	writeTestFile(t, workspace, "node_modules/pkg.js", []byte("derived"), 0o644)
	writeTestFile(t, workspace, "keep.txt", []byte("keep"), 0o644)
	manifest, err := store.Snapshot(context.Background(), SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := manifest.Entries["ignored.txt"]; ok {
		t.Fatal(".gitignore pattern was not applied")
	}
	if _, ok := manifest.Entries["node_modules"]; ok {
		t.Fatal("derived directory was not ignored")
	}
	if _, ok := manifest.Entries["keep.txt"]; !ok {
		t.Fatal("source file was not captured")
	}
}

func TestGitignoreNegationCanReincludeDefaultDerivedDirectory(t *testing.T) {
	store, workspace := newTestStore(t)
	writeTestFile(t, workspace, ".gitignore", []byte("!node_modules/\nnode_modules/*\n!node_modules/keep.txt\n"), 0o644)
	writeTestFile(t, workspace, "node_modules/keep.txt", []byte("source"), 0o644)
	writeTestFile(t, workspace, "node_modules/drop.txt", []byte("derived"), 0o644)
	manifest, err := store.Snapshot(context.Background(), SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := manifest.Entries["node_modules/keep.txt"]; !ok {
		t.Fatal("negated source file was not re-included")
	}
	if _, ok := manifest.Entries["node_modules/drop.txt"]; ok {
		t.Fatal("non-negated derived file was captured")
	}
}

func TestPartialRestoreChangesOnlySelectedPath(t *testing.T) {
	store, workspace := newTestStore(t)
	writeTestFile(t, workspace, "a.txt", []byte("base-a"), 0o644)
	writeTestFile(t, workspace, "b.txt", []byte("base-b"), 0o644)
	base, err := store.Snapshot(context.Background(), SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, workspace, "a.txt", []byte("new-a"), 0o644)
	writeTestFile(t, workspace, "b.txt", []byte("new-b"), 0o644)
	plan, err := store.PlanRestore(context.Background(), base.ID, SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ApplyRestore(context.Background(), plan.ID, []string{"a.txt"}); err != nil {
		t.Fatal(err)
	}
	if got := string(readTestFile(t, workspace, "a.txt")); got != "base-a" {
		t.Fatalf("selected a.txt=%q", got)
	}
	if got := string(readTestFile(t, workspace, "b.txt")); got != "new-b" {
		t.Fatalf("unselected b.txt=%q", got)
	}
}

func TestUnsafeSymlinkRestoreIsRejected(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink privileges vary on Windows")
	}
	store, workspace := newTestStore(t)
	if err := os.Symlink("../outside", filepath.Join(workspace, "escape")); err != nil {
		t.Fatal(err)
	}
	base, err := store.Snapshot(context.Background(), SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(workspace, "escape")); err != nil {
		t.Fatal(err)
	}
	plan, err := store.PlanRestore(context.Background(), base.ID, SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Changes) != 1 || plan.Changes[0].UnsafeReason == "" {
		t.Fatalf("unsafe symlink plan=%+v", plan.Changes)
	}
	if _, err := store.ApplyRestore(context.Background(), plan.ID, []string{"escape"}); err == nil {
		t.Fatal("unsafe symlink restore was accepted")
	}
}

func TestVerifyDetectsCorruptChunk(t *testing.T) {
	store, workspace := newTestStore(t)
	writeTestFile(t, workspace, "a.txt", []byte("content"), 0o644)
	manifest, err := store.Snapshot(context.Background(), SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	obj, err := store.readFileObject(manifest.Entries["a.txt"].Hash)
	if err != nil {
		t.Fatal(err)
	}
	chunkPath, err := objectPath(store.root, "chunks", obj.Chunks[0].Hash, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(chunkPath, []byte("corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.Verify(context.Background(), manifest.ID); err == nil {
		t.Fatal("corrupt chunk passed verification")
	}
}

func TestOpenRejectsStoreInsideWorkspace(t *testing.T) {
	workspace := t.TempDir()
	if _, err := Open(filepath.Join(workspace, ".history"), workspace); err == nil {
		t.Fatal("store inside workspace was accepted")
	}
}

func TestOpenRejectsSymlinkedStoreInsideWorkspace(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink privileges vary on Windows")
	}
	workspace := t.TempDir()
	external := t.TempDir()
	if err := os.Mkdir(filepath.Join(workspace, "hidden-store"), 0o700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(external, "store-alias")
	if err := os.Symlink(filepath.Join(workspace, "hidden-store"), alias); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(alias, workspace); err == nil {
		t.Fatal("symlinked store inside workspace was accepted")
	}
}

func TestPartialRestoreRejectsSymlinkParentEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink privileges vary on Windows")
	}
	store, workspace := newTestStore(t)
	writeTestFile(t, workspace, "dir/file.txt", []byte("base"), 0o644)
	base, err := store.Snapshot(context.Background(), SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(workspace, "dir")); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(workspace, "dir")); err != nil {
		t.Fatal(err)
	}
	plan, err := store.PlanRestore(context.Background(), base.ID, SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ApplyRestore(context.Background(), plan.ID, []string{"dir/file.txt"}); err == nil {
		t.Fatal("partial restore followed a symlink parent")
	}
	if _, err := os.Stat(filepath.Join(outside, "file.txt")); !os.IsNotExist(err) {
		t.Fatalf("restore wrote outside workspace: %v", err)
	}
}

func TestCaseOnlyRenameRestoreUsesStagedMoveOrdering(t *testing.T) {
	store, workspace := newTestStore(t)
	writeTestFile(t, workspace, "Name.txt", []byte("same"), 0o644)
	base, err := store.Snapshot(context.Background(), SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(workspace, "Name.txt")); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, workspace, "name.txt", []byte("same"), 0o644)
	plan, err := store.PlanRestore(context.Background(), base.ID, SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ApplyRestore(context.Background(), plan.ID, nil); err != nil {
		t.Fatal(err)
	}
	if got := string(readTestFile(t, workspace, "Name.txt")); got != "same" {
		t.Fatalf("restored=%q", got)
	}
	if runtime.GOOS != "windows" {
		if _, err := os.Stat(filepath.Join(workspace, "name.txt")); !os.IsNotExist(err) {
			t.Fatalf("lowercase alias survived: %v", err)
		}
	}
}

func TestHardlinkIdentityRoundTrips(t *testing.T) {
	store, workspace := newTestStore(t)
	writeTestFile(t, workspace, "source", []byte("linked"), 0o644)
	if err := os.Link(filepath.Join(workspace, "source"), filepath.Join(workspace, "alias")); err != nil {
		t.Skipf("hardlinks unavailable: %v", err)
	}
	base, err := store.Snapshot(context.Background(), SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if base.Entries["alias"].Type != EntryHardlink && base.Entries["source"].Type != EntryHardlink {
		t.Fatalf("entries=%+v", base.Entries)
	}
	_ = os.Remove(filepath.Join(workspace, "source"))
	_ = os.Remove(filepath.Join(workspace, "alias"))
	plan, err := store.PlanRestore(context.Background(), base.ID, SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ApplyRestore(context.Background(), plan.ID, nil); err != nil {
		t.Fatal(err)
	}
	a, err := os.Stat(filepath.Join(workspace, "source"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.Stat(filepath.Join(workspace, "alias"))
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(a, b) {
		t.Fatal("restored files are not hard-linked")
	}
}

func TestChangedExistingHardlinkPassesStaleValidation(t *testing.T) {
	store, workspace := newTestStore(t)
	writeTestFile(t, workspace, "source", []byte("old"), 0o644)
	if err := os.Link(filepath.Join(workspace, "source"), filepath.Join(workspace, "alias")); err != nil {
		t.Skipf("hardlinks unavailable: %v", err)
	}
	target, err := store.Snapshot(context.Background(), SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "source"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	plan, err := store.PlanRestore(context.Background(), target.ID, SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ApplyRestore(context.Background(), plan.ID, nil); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"source", "alias"} {
		if got := string(readTestFile(t, workspace, path)); got != "old" {
			t.Fatalf("%s=%q", path, got)
		}
	}
	sourceInfo, _ := os.Stat(filepath.Join(workspace, "source"))
	aliasInfo, _ := os.Stat(filepath.Join(workspace, "alias"))
	if !os.SameFile(sourceInfo, aliasInfo) {
		t.Fatal("changed hardlink topology was not restored")
	}
}

func TestInstanceLockRejectsSecondStoreWithoutInterruptingLiveScope(t *testing.T) {
	workspace := t.TempDir()
	root := t.TempDir()
	first, err := Open(root, workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	writeTestFile(t, workspace, "live.txt", []byte("before"), 0o644)
	before, err := first.Snapshot(context.Background(), SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := first.BeginMessageScope("session", "message", before.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(root, workspace); err == nil {
		t.Fatal("second store opened while the first instance owned live scopes")
	}
	scope, ok, err := first.MessageScope("session", "message")
	if err != nil || !ok || scope.State != "active" {
		t.Fatalf("second open altered live scope: scope=%+v ok=%v err=%v", scope, ok, err)
	}
}
