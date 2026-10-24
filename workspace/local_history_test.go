package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"crude/localhistory"
)

func TestWorkspaceLocalHistoryCapturePreviewApply(t *testing.T) {
	project := t.TempDir()
	store, err := localhistory.Open(t.TempDir(), project)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	bus := NewEventBus()
	ws := &Workspace{RootPath: project, LocalHistory: store, eventBus: bus}
	path := filepath.Join(project, "a.txt")
	if err := os.WriteFile(path, []byte("before"), 0o644); err != nil {
		t.Fatal(err)
	}
	snapshot, err := ws.CaptureHistory(context.Background(), localhistory.SnapshotOptions{Kind: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("after"), 0o644); err != nil {
		t.Fatal(err)
	}
	plan, err := ws.PreviewHistoryRestore(context.Background(), snapshot.ID, localhistory.SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ws.ApplyHistoryRestore(context.Background(), plan.ID, nil); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "before" {
		t.Fatalf("restored content=%q", data)
	}
	events := bus.RecentEvents(1)
	if len(events) != 1 || !strings.Contains(events[0], "HistoryRestored") {
		t.Fatalf("restore event log=%v", events)
	}
}

func TestHistoryMutationReportsDocumentPersistenceFailure(t *testing.T) {
	s, db, snap, _ := durableDeltaFixture(t)
	if _, err := db.DB.Exec(`CREATE TRIGGER refuse_history_reconcile BEFORE UPDATE ON workspace_documents BEGIN SELECT RAISE(ABORT,'history reconciliation refused'); END`); err != nil {
		t.Fatal(err)
	}
	err := s.WithHistoryMutation(t.Context(), []string{snap.Path}, func() error {
		return os.WriteFile(snap.Path, []byte("restored bytes"), 0600)
	})
	if err == nil || !strings.Contains(err.Error(), "history reconciliation refused") {
		t.Fatalf("disk effect was falsely reported reconciled: %v", err)
	}
	if data, err := os.ReadFile(snap.Path); err != nil || string(data) != "restored bytes" {
		t.Fatalf("known disk effect lost: %q %v", data, err)
	}
	got, err := s.Snapshot(snap.ID)
	if err != nil || string(got.Text) != "original" || !got.Conflict {
		t.Fatalf("unpersisted document was not preserved and fenced: %+v %v", got, err)
	}
	if _, err := db.DB.Exec(`DROP TRIGGER refuse_history_reconcile`); err != nil {
		t.Fatal(err)
	}
	if err := s.ReconcileDisk(t.Context(), snap.ID); err != nil {
		t.Fatal(err)
	}
	got, err = s.Snapshot(snap.ID)
	if err != nil || string(got.Text) != "restored bytes" || got.Conflict || got.Dirty {
		t.Fatalf("explicit recovery did not settle: %+v %v", got, err)
	}
}
