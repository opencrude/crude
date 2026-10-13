package workspace

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"crude/document"
	"crude/localhistory"
	"crude/worktree"
)

func executionFixture(t *testing.T) (*ExecutionService, *sql.DB, string) {
	t.Helper()
	root := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		c := exec.Command("git", append([]string{"-C", root}, args...)...)
		if b, e := c.CombinedOutput(); e != nil {
			t.Fatalf("git: %v %s", e, b)
		}
	}
	run("init", "-q")
	run("config", "user.email", "test@example.invalid")
	run("config", "user.name", "test")
	for p, text := range map[string]string{"live.txt": "one\ntwo\nthree\n", "disk.txt": "original\n", "delete.txt": "delete\n"} {
		if e := os.WriteFile(filepath.Join(root, p), []byte(text), 0644); e != nil {
			t.Fatal(e)
		}
	}
	run("add", ".")
	run("commit", "-qm", "base")
	db, e := sql.Open("sqlite", filepath.Join(t.TempDir(), "state.db"))
	if e != nil {
		t.Fatal(e)
	}
	db.SetMaxOpenConns(1)
	saves := NewDocumentSaveService()
	docs := NewDocumentService(root, saves, nil, db)
	store, e := localhistory.Open(t.TempDir(), root)
	if e != nil {
		t.Fatal(e)
	}
	history := NewLocalHistoryService(root, store, nopLogger{})
	service, e := worktree.Open(root, t.TempDir(), worktree.Options{})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { docs.Close(); saves.Close(); history.Stop(); _ = store.Close(); _ = db.Close() })
	return &ExecutionService{Service: service, documents: docs, history: history}, db, root
}
func executionPut(t *testing.T, root, p, text string) {
	t.Helper()
	if e := os.MkdirAll(filepath.Dir(filepath.Join(root, p)), 0755); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(root, p), []byte(text), 0644); e != nil {
		t.Fatal(e)
	}
}
func executionCreate(t *testing.T, s *ExecutionService) worktree.Context {
	t.Helper()
	c, e := s.Create(context.Background(), worktree.Request{Owner: worktree.Owner{AgentID: "agent", RunID: "run"}, Mode: worktree.Isolated})
	if e != nil {
		t.Fatal(e)
	}
	return c
}
func executionReview(t *testing.T, s *ExecutionService, id string) worktree.PlanDelta {
	t.Helper()
	p, e := s.Review(context.Background(), id)
	if e != nil {
		t.Fatal(e)
	}
	return p
}
func diskText(t *testing.T, root, p, want string) {
	t.Helper()
	b, e := os.ReadFile(filepath.Join(root, p))
	if e != nil || string(b) != want {
		t.Fatalf("disk %s=%q want=%q err=%v", p, b, want, e)
	}
}

func TestExecutionApplyMixedDirtyDocumentsAndUndo(t *testing.T) {
	s, _, root := executionFixture(t)
	ctx := context.Background()
	owner := document.Owner{ClientID: "human", Epoch: 1}
	snap, e := s.documents.Open(owner, "live.txt")
	if e != nil {
		t.Fatal(e)
	}
	r := documentEdit(t, s.documents, owner, snap.ID, "dirty", snap.Revision, 0, 3, "ONE")
	c := executionCreate(t, s)
	executionPut(t, c.Root, "live.txt", "ONE\nTWO\nthree\n")
	executionPut(t, c.Root, "disk.txt", "child\n")
	executionPut(t, c.Root, "new/child.bin", "\x00binary\xff")
	if e = os.Remove(filepath.Join(c.Root, "delete.txt")); e != nil {
		t.Fatal(e)
	}
	if e = os.Chmod(filepath.Join(c.Root, "disk.txt"), 0755); e != nil {
		t.Fatal(e)
	}
	// Later accepted change in another line is merged, never overwritten.
	documentEdit(t, s.documents, owner, snap.ID, "later", r.Revision, 8, 13, "THREE")
	p := executionReview(t, s, c.ID)
	receipt, e := s.Apply(ctx, p)
	if e != nil {
		t.Fatal(e)
	}
	after := documentText(t, s.documents, snap.ID, "ONE\nTWO\nTHREE\n")
	if !after.Dirty {
		t.Fatal("dirty shared text forced saved")
	}
	diskText(t, root, "live.txt", "one\ntwo\nthree\n")
	diskText(t, root, "disk.txt", "child\n")
	diskText(t, root, "new/child.bin", "\x00binary\xff")
	if _, e = os.Stat(filepath.Join(root, "delete.txt")); !errors.Is(e, os.ErrNotExist) {
		t.Fatal(e)
	}
	fi, e := os.Stat(filepath.Join(root, "disk.txt"))
	// Windows has no executable permission bits to preserve.
	if e != nil || runtime.GOOS != "windows" && fi.Mode().Perm() != 0755 {
		t.Fatalf("mode %v %v", fi, e)
	}
	if _, e = s.history.store.LoadManifest(receipt.SafetySnapshotID); e != nil {
		t.Fatal("missing safety", e)
	}
	dup, e := s.Apply(ctx, p)
	if e != nil || dup != receipt {
		t.Fatalf("lost reply %v %v", dup, e)
	}
	// A newer non-overlapping shared edit survives undo; disk bytes were never the buffer baseline.
	documentEdit(t, s.documents, owner, snap.ID, "post", after.Revision, 0, 3, "One!")
	if _, e = s.Undo(ctx, receipt.OperationID); e != nil {
		t.Fatal(e)
	}
	documentText(t, s.documents, snap.ID, "One!\ntwo\nTHREE\n")
	diskText(t, root, "disk.txt", "original\n")
	diskText(t, root, "delete.txt", "delete\n")
	diskText(t, root, "live.txt", "one\ntwo\nthree\n")
	if _, e = os.Stat(filepath.Join(root, "new/child.bin")); !errors.Is(e, os.ErrNotExist) {
		t.Fatal(e)
	}
}
func TestExecutionPersistenceFailureRollsDiskBack(t *testing.T) {
	s, db, root := executionFixture(t)
	owner := document.Owner{ClientID: "human", Epoch: 1}
	snap, e := s.documents.Open(owner, "live.txt")
	if e != nil {
		t.Fatal(e)
	}
	documentEdit(t, s.documents, owner, snap.ID, "dirty", 1, 0, 3, "ONE")
	c := executionCreate(t, s)
	executionPut(t, c.Root, "live.txt", "ONE\nTWO\nthree\n")
	executionPut(t, c.Root, "disk.txt", "child")
	executionPut(t, c.Root, "new/deep/file", "new")
	p := executionReview(t, s, c.ID)
	if _, e = db.Exec(`CREATE TRIGGER execution_fail BEFORE UPDATE ON workspace_documents BEGIN SELECT RAISE(FAIL,'injected checkpoint failure'); END`); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Apply(context.Background(), p); e == nil {
		t.Fatal("injected commit accepted")
	}
	documentText(t, s.documents, snap.ID, "ONE\ntwo\nthree\n")
	diskText(t, root, "live.txt", "one\ntwo\nthree\n")
	diskText(t, root, "disk.txt", "original\n")
	if _, e = os.Stat(filepath.Join(root, "new")); !errors.Is(e, os.ErrNotExist) {
		t.Fatalf("parents not rolled back: %v", e)
	}
	j, e := s.Integration(integrationID(p))
	if e != nil || j.State != "rolled_back" {
		t.Fatalf("journal %+v %v", j, e)
	}
}
func TestExecutionInterruptedJournalPassiveRecovery(t *testing.T) {
	s, _, root := executionFixture(t)
	ctx := context.Background()
	c := executionCreate(t, s)
	executionPut(t, c.Root, "disk.txt", "child\n")
	p := executionReview(t, s, c.ID)
	before, e := s.DestinationFile("disk.txt")
	if e != nil {
		t.Fatal(e)
	}
	var after *worktree.File
	for _, ch := range p.Changes {
		if ch.Path == "disk.txt" {
			after = ch.After
		}
	}
	j := IntegrationStatus{Version: 1, State: "prepared", Plan: p, Receipt: worktree.Receipt{OperationID: integrationID(p), PlanDigest: p.Digest}, Disk: []worktree.Change{{Path: "disk.txt", Before: before, After: after}}}
	s.documents.mu.Lock()
	e = s.documents.integrationTable()
	if e == nil {
		e = s.documents.saveIntegration(j)
	}
	s.documents.mu.Unlock()
	if e != nil {
		t.Fatal(e)
	}
	executionPut(t, root, "disk.txt", "child\n")
	// New service loading the same DB is passive; no operation replay/rollback.
	reopened := NewDocumentService(root, nil, nil, s.documents.stateDB)
	defer reopened.Close()
	s.documents = reopened
	diskText(t, root, "disk.txt", "child\n")
	if _, e = s.Apply(ctx, p); e == nil {
		t.Fatal("unknown effect replayed")
	}
	if _, e = s.RecoverIntegration(ctx, j.Receipt.OperationID); e != nil {
		t.Fatal(e)
	}
	diskText(t, root, "disk.txt", "original\n")
}
func TestExecutionConflictsAndSymlinkNoPartialEffects(t *testing.T) {
	s, _, root := executionFixture(t)
	c := executionCreate(t, s)
	executionPut(t, c.Root, "disk.txt", "child")
	executionPut(t, c.Root, "z.txt", "new")
	p := executionReview(t, s, c.ID)
	executionPut(t, root, "disk.txt", "human")
	if _, e := s.Apply(context.Background(), p); e == nil {
		t.Fatal("conflict accepted")
	}
	diskText(t, root, "disk.txt", "human")
	if _, e := os.Stat(filepath.Join(root, "z.txt")); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("partial effect")
	}
	executionPut(t, root, "disk.txt", "original\n")
	p = executionReview(t, s, c.ID)
	executionPut(t, c.Root, "z.txt", "changed after review")
	if _, e := s.Apply(context.Background(), p); e == nil {
		t.Fatal("stale source accepted")
	}
	p = executionReview(t, s, c.ID)
	if e := os.Symlink(filepath.Join(t.TempDir(), "outside"), filepath.Join(root, "z.txt")); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Apply(context.Background(), p); e == nil {
		t.Fatal("symlink accepted")
	}
	diskText(t, root, "disk.txt", "original\n")
}
func TestExecutionExactTextDiff(t *testing.T) {
	for _, pair := range [][2]string{{"a\nb\nc\n", "A\nb\nC\n"}, {"", "new"}, {"last", "last\n"}, {"é\nx", "é\ny"}} {
		edits, e := worktree.TextEdits([]byte(pair[0]), []byte(pair[1]))
		if e != nil {
			t.Fatal(e)
		}
		got, _, e := document.Apply([]byte(pair[0]), edits)
		if e != nil || !bytes.Equal(got, []byte(pair[1])) {
			t.Fatal(pair, e)
		}
	}
}

func TestExecutionRecoveryRefusesThirdStateAndUndoOverlap(t *testing.T) {
	s, _, root := executionFixture(t)
	ctx := context.Background()
	c := executionCreate(t, s)
	executionPut(t, c.Root, "disk.txt", "child\n")
	p := executionReview(t, s, c.ID)
	receipt, e := s.Apply(ctx, p)
	if e != nil {
		t.Fatal(e)
	}
	executionPut(t, root, "disk.txt", "human\n")
	if _, e = s.Undo(ctx, receipt.OperationID); e == nil {
		t.Fatal("overlap undo accepted")
	}
	diskText(t, root, "disk.txt", "human\n")
	// Explicit unknown effect recovery also refuses a third-party postimage.
	j := IntegrationStatus{Version: 1, State: "prepared", Plan: p, Receipt: worktree.Receipt{OperationID: "crash-third"}, Disk: p.Changes}
	s.documents.mu.Lock()
	e = s.documents.saveIntegration(j)
	s.documents.mu.Unlock()
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.RecoverIntegration(ctx, "crash-third"); e == nil {
		t.Fatal("third state overwritten")
	}
	diskText(t, root, "disk.txt", "human\n")
}
func TestExecutionCleanupArchivesActualResultAndRetainsPins(t *testing.T) {
	s, _, root := executionFixture(t)
	ctx := context.Background()
	executionPut(t, root, "disk.txt", "inherited human\n")
	c := executionCreate(t, s)
	executionPut(t, c.Root, "live.txt", "child\n")
	p := executionReview(t, s, c.ID)
	receipt, e := s.Apply(ctx, p)
	if e != nil {
		t.Fatal(e)
	}
	if receipt.OperationID == "" {
		t.Fatal("missing receipt")
	}
	if e = s.Cleanup(ctx, c.ID, c.Owner); e == nil {
		t.Fatal("active pin removed")
	}
	if e = s.Unpin(c.ID, c.Owner); e != nil {
		t.Fatal(e)
	}
	if e = s.Cleanup(ctx, c.ID, c.Owner); e != nil {
		t.Fatal(e)
	}
	saved, e := s.Inspect(c.ID)
	if e != nil || !saved.Removed || saved.ArchiveCommit == "" || string(saved.ArchivedResult["disk.txt"].Data) != "inherited human\n" {
		t.Fatalf("archive %+v %v", saved, e)
	}
	diskText(t, root, "disk.txt", "inherited human\n")
	diskText(t, root, "live.txt", "child\n")
}

func TestExecutionCommittedReceiptSurvivesPassiveReopen(t *testing.T) {
	s, db, root := executionFixture(t)
	ctx := context.Background()
	owner := document.Owner{ClientID: "human", Epoch: 1}
	snap, e := s.documents.Open(owner, "live.txt")
	if e != nil {
		t.Fatal(e)
	}
	documentEdit(t, s.documents, owner, snap.ID, "dirty", 1, 0, 3, "ONE")
	c := executionCreate(t, s)
	executionPut(t, c.Root, "live.txt", "ONE\nTWO\nthree\n")
	p := executionReview(t, s, c.ID)
	r, e := s.Apply(ctx, p)
	if e != nil {
		t.Fatal(e)
	}
	s.documents.Close()
	reopened := NewDocumentService(root, nil, nil, db)
	defer reopened.Close()
	s.documents = reopened
	before := documentText(t, reopened, snap.ID, "ONE\nTWO\nthree\n")
	diskText(t, root, "live.txt", "one\ntwo\nthree\n")
	dup, e := s.Apply(ctx, p)
	if e != nil || dup != r {
		t.Fatalf("lost durable receipt: %+v %v", dup, e)
	}
	after := documentText(t, reopened, snap.ID, "ONE\nTWO\nthree\n")
	if after.Revision != before.Revision {
		t.Fatal("passive reopen/retry replayed edit")
	}
	list, e := s.Integrations()
	if e != nil || len(list) != 1 || list[0].State != "committed" {
		t.Fatalf("passive discovery %+v %v", list, e)
	}
}
