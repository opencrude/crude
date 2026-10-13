package worktree

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestDiskConflictsAndIntegrationEvidence(t *testing.T) {
	root, store, s := fixture(t)
	ctx := context.Background()
	c, err := s.Create(ctx, Request{Owner: testOwner, Mode: Isolated})
	if err != nil {
		t.Fatal(err)
	}
	put(t, c.Root, "a.txt", []byte("child"))
	p, err := s.Review(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	put(t, root, "unrelated", []byte("newer unrelated human"))
	if err = s.ValidateDestination(ctx, p, nil); err != nil {
		t.Fatal(err)
	}
	put(t, root, "a.txt", []byte("newer human"))
	if err = s.ValidateDestination(ctx, p, nil); err == nil {
		t.Fatal("disk conflict allowed")
	}
	r := Receipt{PlanDigest: p.Digest, OperationID: "operation", SafetySnapshotID: "safety"}
	if err = s.RecordIntegration(ctx, p, r, nil); err == nil {
		t.Fatal("unapplied result marked integrated")
	}
	put(t, root, "a.txt", []byte("child"))
	if err = s.RecordIntegration(ctx, p, Receipt{PlanDigest: p.Digest}, nil); err == nil {
		t.Fatal("missing safety receipt accepted")
	}
	if err = s.RecordIntegration(ctx, p, r, nil); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(root, store, Options{})
	if err != nil {
		t.Fatal(err)
	}
	saved, err := reopened.Inspect(c.ID)
	if err != nil || saved.Integration == nil || saved.Integration.OperationID != "operation" || len(saved.Pins) != 1 {
		t.Fatalf("lost durable evidence: %+v %v", saved, err)
	}
	if err = s.Unpin(c.ID, testOwner); err != nil {
		t.Fatal(err)
	}
	if err = s.Cleanup(ctx, c.ID, testOwner); err != nil {
		t.Fatal("verified integrated archive cleanup failed", err)
	}
	saved, err = s.Inspect(c.ID)
	if err != nil || !saved.Removed || saved.ArchiveCommit == "" || string(saved.ArchivedResult["a.txt"].Data) != "child" {
		t.Fatalf("missing archived result: %+v %v", saved, err)
	}
}

func TestModesDeletedDocumentsAndCanceledCreation(t *testing.T) {
	root, _, s := fixture(t)
	ctx := context.Background()
	if err := os.Chmod(filepath.Join(root, "a.txt"), 0755); err != nil {
		t.Fatal(err)
	}
	docs := map[string]Document{"deleted": {Revision: 5, Exists: false}, "unsaved new": {Revision: 1, Exists: true, Data: []byte("new")}}
	c, err := s.Create(ctx, Request{Owner: testOwner, Mode: Isolated, Documents: docs})
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(c.Root, "a.txt"))
	if err != nil || fi.Mode().Perm() != 0755 {
		t.Fatalf("mode lost: %v %v", fi, err)
	}
	put(t, c.Root, "deleted", []byte("new child"))
	p, err := s.Review(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Changes) != 1 || p.Changes[0].Before != nil {
		t.Fatalf("deleted accepted baseline: %+v", p)
	}
	if err = s.ValidateDestination(ctx, p, nil); err == nil {
		t.Fatal("deleted accepted document ownership ignored")
	}
	if err = s.ValidateDestination(ctx, p, docs); err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = s.Create(cancelled, Request{Owner: testOwner, Mode: Isolated}); err == nil {
		t.Fatal("canceled creation succeeded")
	}
}

func TestCountBudgetAndIgnoredRetention(t *testing.T) {
	root, store, _ := fixture(t)
	s, err := Open(root, store, Options{MaxContexts: 1})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	c, err := s.Create(ctx, Request{Owner: testOwner, Mode: Isolated})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Create(ctx, Request{Owner: testOwner, Mode: Isolated}); err == nil {
		t.Fatal("count budget ignored")
	}
	put(t, c.Root, "secret", []byte("new ignored evidence"))
	if err = s.Unpin(c.ID, testOwner); err != nil {
		t.Fatal(err)
	}
	if err = s.Cleanup(ctx, c.ID, testOwner); err == nil {
		t.Fatal("ignored artifact removed")
	}
}
