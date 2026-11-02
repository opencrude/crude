//go:build windows

package localhistory

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRestoreReadOnlyFileRefusesWithoutMutation(t *testing.T) {
	store, root := newTestStore(t)
	path := filepath.Join(root, "protected.txt")
	writeTestFile(t, root, "protected.txt", []byte("original"), 0600)
	base, err := store.Snapshot(context.Background(), SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, root, "protected.txt", []byte("current"), 0600)
	if err := os.Chmod(path, 0444); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0600) })
	plan, err := store.PlanRestore(context.Background(), base.ID, SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.ApplyRestore(context.Background(), plan.ID, nil)
	if !errors.Is(err, os.ErrPermission) {
		t.Fatalf("restore did not explain permission refusal: %v", err)
	}
	if got := string(readTestFile(t, root, "protected.txt")); got != "current" {
		t.Fatalf("refused restore changed file: %q", got)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm()&0200 != 0 {
		t.Fatalf("read-only attribute changed: %v, %v", info, err)
	}
}
