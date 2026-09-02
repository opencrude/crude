//go:build !windows

package localhistory

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestSpecialFileIsMetadataOnlyAndNeverRestored(t *testing.T) {
	store, workspace := newTestStore(t)
	path := filepath.Join(workspace, "pipe")
	if err := unix.Mkfifo(path, 0o600); err != nil {
		t.Skip(err)
	}
	snapshot, err := store.Snapshot(context.Background(), SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Entries["pipe"].Type != EntrySpecial {
		t.Fatalf("entry=%+v", snapshot.Entries["pipe"])
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	plan, err := store.PlanRestore(context.Background(), snapshot.ID, SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Changes) != 1 || plan.Changes[0].UnsafeReason == "" {
		t.Fatalf("plan=%+v", plan.Changes)
	}
	if _, err := store.ApplyRestore(context.Background(), plan.ID, []string{"pipe"}); err == nil {
		t.Fatal("special file restore was accepted")
	}
}
