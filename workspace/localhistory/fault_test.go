package localhistory

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestRestoreRollbackAtEveryApplyBoundary(t *testing.T) {
	for failAt := 0; failAt < 3; failAt++ {
		t.Run(fmt.Sprintf("step-%d", failAt), func(t *testing.T) {
			store, workspace := newTestStore(t)
			for i := 0; i < 3; i++ {
				writeTestFile(t, workspace, fmt.Sprintf("%d.txt", i), []byte("base"), 0o644)
			}
			base, err := store.Snapshot(context.Background(), SnapshotOptions{})
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 3; i++ {
				writeTestFile(t, workspace, fmt.Sprintf("%d.txt", i), []byte("current"), 0o644)
			}
			plan, err := store.PlanRestore(context.Background(), base.ID, SnapshotOptions{})
			if err != nil {
				t.Fatal(err)
			}
			store.beforeApply = func(index int, _ Change) error {
				if index == failAt {
					return errors.New("fault")
				}
				return nil
			}
			if _, err := store.ApplyRestore(context.Background(), plan.ID, nil); err == nil {
				t.Fatal("fault was ignored")
			}
			for i := 0; i < 3; i++ {
				if got := string(readTestFile(t, workspace, fmt.Sprintf("%d.txt", i))); got != "current" {
					t.Fatalf("file %d after rollback=%q", i, got)
				}
			}
		})
	}
}
