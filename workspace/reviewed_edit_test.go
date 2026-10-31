package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"crude/document"
	"crude/fileio"
	"crude/localhistory"
	"crude/lspedit"
	"crude/types"
)

type reviewedFixture struct {
	documents *DocumentService
	store     *localhistory.Store
	files     *FileOpsService
	owner     document.Owner
	source    types.InsightSource
	path      string
}

func newReviewedFixture(t *testing.T) reviewedFixture {
	t.Helper()
	root := t.TempDir()
	db, err := openWorkspaceDBAt(filepath.Join(t.TempDir(), "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	docs := NewDocumentService(root, nil, nil, db.DB)
	t.Cleanup(docs.Close)
	store, err := localhistory.Open(filepath.Join(t.TempDir(), "history"), root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	path := filepath.Join(root, "source.txt")
	if err := os.WriteFile(path, []byte("var old = 1\r\nold\r\n"), 0640); err != nil {
		t.Fatal(err)
	}
	owner := document.Owner{ClientID: "review-owner", Epoch: 1}
	snapshot, err := docs.Open(owner, path)
	if err != nil {
		t.Fatal(err)
	}
	docs.mu.Lock()
	source := docs.analysisIdentityLocked(docs.docs[snapshot.ID])
	docs.mu.Unlock()
	return reviewedFixture{docs, store, NewFileOpsService(root, nil, nil), owner, source, path}
}

func (f reviewedFixture) prepare(t *testing.T, check func() error) ReviewedEditPreview {
	t.Helper()
	p, err := f.documents.prepareReviewedEdit(t.Context(), f.owner, "Rename", types.InsightBoundary{Source: f.source},
		[]lspedit.DocumentEdit{{URI: fileURI(f.path), Edits: []types.LSPTextEdit{
			{Line: 0, Col: 4, EndLine: 0, EndCol: 7, NewText: "new"},
			{Line: 1, EndLine: 1, EndCol: 3, NewText: "new"},
		}}}, check)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestReviewedEditExactCanonicalPreviewSafetyApplyReplayAndRestore(t *testing.T) {
	f := newReviewedFixture(t)
	p := f.prepare(t, func() error { return nil })
	if !strings.Contains(p.Text, `- "old"`) || !strings.Contains(p.Text, `+ "new"`) || len(p.Files) != 1 {
		t.Fatal("review does not expose the literal independent expected changes")
	}
	disk, err := os.ReadFile(f.path)
	if err != nil || string(disk) != "var old = 1\r\nold\r\n" {
		t.Fatal("preview changed bytes")
	}
	// Detached presentation is not the stored authority.
	p.Files[0].AfterSHA256, p.Text = "forged", "forged preview"
	result := f.documents.ApplyReviewedEdit(t.Context(), f.owner, p.ID, p.Digest, f.store, f.files)
	if result.State != "committed" || result.Error != "" || result.SafetySnapshotID == "" || result.SafetyReference != p.ID {
		t.Fatalf("reviewed apply failed: %+v", result)
	}
	disk, err = os.ReadFile(f.path)
	if err != nil || string(disk) != "var new = 1\r\nnew\r\n" {
		t.Fatalf("literal rename result: %q %v", disk, err)
	}
	snap, err := f.documents.Snapshot(f.source.DocumentID)
	if err != nil || string(snap.Text) != string(disk) || snap.Dirty || snap.Revision <= f.source.Revision {
		t.Fatalf("disk effect was not canonical: %+v %v", snap, err)
	}
	safety, err := f.store.LoadRetainedSnapshot(t.Context(), p.ID)
	if err != nil || safety.ID != result.SafetySnapshotID || safety.Entries["source.txt"].Hash != reviewedHash("var old = 1\r\nold\r\n") {
		t.Fatalf("missing exact recoverable preimage: %+v %v", safety, err)
	}
	if replay := f.documents.ApplyReviewedEdit(t.Context(), f.owner, p.ID, p.Digest, f.store, f.files); replay != result {
		t.Fatalf("same review did not observe its original outcome: %+v", replay)
	}
	restore, err := f.store.PlanRestorePaths(t.Context(), safety.ID, localhistory.SnapshotOptions{DisableGitIgnore: true}, []string{"source.txt"})
	if err != nil || len(restore.Changes) != 1 {
		t.Fatalf("undo review unavailable: %+v %v", restore, err)
	}
	if err := f.documents.WithHistoryMutation(t.Context(), []string{f.path}, func() error {
		_, err := f.store.ApplyRestoreChoices(t.Context(), restore.ID, []localhistory.RestoreChoice{{Path: "source.txt"}})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	snap, err = f.documents.Snapshot(f.source.DocumentID)
	if err != nil || string(snap.Text) != "var old = 1\r\nold\r\n" || snap.Dirty {
		t.Fatalf("recoverable restore bypassed canonical owner: %+v %v", snap, err)
	}
}

func TestReviewedEditRejectsEqualTextPeerHistoryAndForeignOwner(t *testing.T) {
	f := newReviewedFixture(t)
	peer := document.Owner{ClientID: "peer", Epoch: 1}
	snapshot, err := f.documents.Open(peer, f.path)
	if err != nil {
		t.Fatal(err)
	}
	p := f.prepare(t, func() error { return nil })
	foreign := f.documents.ApplyReviewedEdit(t.Context(), peer, p.ID, p.Digest, f.store, f.files)
	if foreign.State != "refused" {
		t.Fatal("another shell acquired the private review")
	}
	receipt, err := f.documents.Apply(document.Change{DocumentID: snapshot.ID, Owner: peer, RequestID: "peer-insert",
		BaseRevision: snapshot.Revision, Edits: []document.Edit{{Text: "X"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.documents.Undo(peer, snapshot.ID, "peer-undo", receipt.Revision); err != nil {
		t.Fatal(err)
	}
	result := f.documents.ApplyReviewedEdit(t.Context(), f.owner, p.ID, p.Digest, f.store, f.files)
	if result.State != "refused" || result.Error == "" || result.SafetySnapshotID != "" {
		t.Fatalf("equal-text newer revision reused review: %+v", result)
	}
	disk, _ := os.ReadFile(f.path)
	if string(disk) != "var old = 1\r\nold\r\n" {
		t.Fatal("stale review changed disk")
	}
}

func TestReviewedEditContextCancelAndNativeReplacementRefuseBeforeEffects(t *testing.T) {
	for _, mode := range []string{"context", "cancel", "replacement", "missing-history", "release", "detach"} {
		t.Run(mode, func(t *testing.T) {
			f := newReviewedFixture(t)
			var stale atomic.Bool
			p := f.prepare(t, func() error {
				if stale.Load() {
					return errors.New("provider or context replaced")
				}
				return nil
			})
			ctx := t.Context()
			store := f.store
			switch mode {
			case "context":
				stale.Store(true)
			case "cancel":
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = cancelled
			case "replacement":
				if err := os.Rename(f.path, f.path+".old"); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(f.path, []byte("var old = 1\r\nold\r\n"), 0640); err != nil {
					t.Fatal(err)
				}
			case "missing-history":
				store = nil
			case "release":
				if err := f.documents.ReleaseReviewedEdit(f.owner, p.ID, p.Digest); err != nil {
					t.Fatal(err)
				}
			case "detach":
				f.documents.Detach(f.owner)
			}
			result := f.documents.ApplyReviewedEdit(ctx, f.owner, p.ID, p.Digest, store, f.files)
			if result.State == "committed" || result.Error == "" {
				t.Fatalf("%s accepted an invalid review: %+v", mode, result)
			}
			disk, _ := os.ReadFile(f.path)
			if string(disk) != "var old = 1\r\nold\r\n" {
				t.Fatal("refused review changed bytes")
			}
		})
	}
}

func TestReviewedEditDetachAtCommitGuardCannotWrite(t *testing.T) {
	f := newReviewedFixture(t)
	p := f.prepare(t, func() error { return nil })
	var atCommit atomic.Bool
	f.documents.mu.Lock()
	f.documents.reviewed[p.ID].check = func() error {
		// The real writer owns this gate while staging. Detach deliberately
		// does not wait for DocumentService.mu, just as the core ingress does.
		unlock, available := fileio.TryLockMutation()
		if available {
			unlock()
		} else {
			atCommit.Store(true)
			f.documents.Detach(f.owner)
		}
		return nil
	}
	f.documents.mu.Unlock()
	result := f.documents.ApplyReviewedEdit(t.Context(), f.owner, p.ID, p.Digest, f.store, f.files)
	disk, _ := os.ReadFile(f.path)
	if !atCommit.Load() {
		t.Fatal("regression did not reach the writer's commit fence")
	}
	if result.State != "refused" || string(disk) != "var old = 1\r\nold\r\n" {
		t.Fatalf("detached review owner still committed: state=%s disk=%q", result.State, disk)
	}
	if result.SafetySnapshotID == "" {
		t.Fatal("regression did not reach captured safety before the commit guard")
	}
	if _, err := f.store.LoadRetainedSnapshot(t.Context(), result.SafetyReference); err == nil {
		t.Fatal("positive no-commit refusal unnecessarily retained a safety pin")
	}
	observed, err := f.documents.InspectReviewedEdit(t.Context(), f.owner, p.ID, p.Digest)
	if err != nil || observed != result {
		t.Fatal("original no-effect result was not durably observable", observed, err)
	}
}

func TestReviewedEditAttemptedCommitNeverClaimsNoEffect(t *testing.T) {
	f := newReviewedFixture(t)
	p := f.prepare(t, func() error { return nil })
	f.files.commitWorkspaceEdit = func(from, to string) error {
		if err := fileio.CommitAtomicFile(from, to); err != nil {
			return err
		}
		return errors.New("injected unconfirmed result after the real writer")
	}
	result := f.documents.ApplyReviewedEdit(t.Context(), f.owner, p.ID, p.Digest, f.store, f.files)
	disk, _ := os.ReadFile(f.path)
	if result.State != "unconfirmed" || string(disk) != "var new = 1\r\nnew\r\n" {
		t.Fatal("attempted native effect became a no-effect refusal", result, string(disk))
	}
	if _, err := f.store.LoadRetainedSnapshot(t.Context(), result.SafetyReference); err != nil {
		t.Fatal("unconfirmed effect lost its exact recovery preimage", err)
	}
}

func TestReviewedEditInspectSurvivesOwnerEpochWithoutRenewingApplyAuthority(t *testing.T) {
	f := newReviewedFixture(t)
	p := f.prepare(t, func() error { return nil })
	result := f.documents.ApplyReviewedEdit(t.Context(), f.owner, p.ID, p.Digest, f.store, f.files)
	if result.State != "committed" {
		t.Fatal(result)
	}
	f.documents.Detach(f.owner)
	f.documents.ReleaseDetached(f.owner)
	newOwner := document.Owner{ClientID: f.owner.ClientID, Epoch: f.owner.Epoch + 1}
	if _, err := f.documents.Open(newOwner, f.path); err != nil {
		t.Fatal(err)
	}
	inspected, err := f.documents.InspectReviewedEdit(t.Context(), newOwner, p.ID, p.Digest)
	if err != nil || inspected != result {
		t.Fatalf("original reviewed result inaccessible after attachment epoch: %+v %v", inspected, err)
	}
	if applied := f.documents.ApplyReviewedEdit(t.Context(), newOwner, p.ID, p.Digest, f.store, f.files); applied.State != "refused" {
		t.Fatal("new epoch renewed an old mutation authority", applied)
	}
	if _, err := f.documents.InspectReviewedEdit(t.Context(), document.Owner{ClientID: "foreign", Epoch: 1}, p.ID, p.Digest); err == nil {
		t.Fatal("another client acquired private receipt evidence")
	}
}

func TestReviewedEditSafetyDoesNotReadOrRetainUnrequestedWorkspaceContent(t *testing.T) {
	f := newReviewedFixture(t)
	unrelated := filepath.Join(filepath.Dir(f.path), "private-not-reviewed.txt")
	if err := os.WriteFile(unrelated, []byte("unrequested private data\n"), 0600); err != nil {
		t.Fatal(err)
	}
	p := f.prepare(t, func() error { return nil })
	result := f.documents.ApplyReviewedEdit(t.Context(), f.owner, p.ID, p.Digest, f.store, f.files)
	if result.State != "committed" {
		t.Fatal(result)
	}
	snapshot, err := f.store.LoadRetainedSnapshot(t.Context(), result.SafetyReference)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Entries) != 1 || len(snapshot.ScopePaths) != 1 || snapshot.ScopePaths[0] != "source.txt" ||
		snapshot.Entries["source.txt"].Hash != reviewedHash("var old = 1\r\nold\r\n") {
		t.Fatal("review safety retained unrequested workspace content", snapshot.Entries)
	}
	restore, err := f.store.PlanRestore(t.Context(), snapshot.ID, localhistory.SnapshotOptions{DisableGitIgnore: true})
	if err != nil || len(restore.Changes) != 1 || restore.Changes[0].Path != "source.txt" {
		t.Fatalf("default restore expanded a partial safety snapshot's authority: %+v %v", restore, err)
	}
}
