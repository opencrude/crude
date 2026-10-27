package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"crude/localhistory"
)

func TestExecutionRetentionPreservesUndoAndCleanup(t *testing.T) {
	for _, undo := range []bool{false, true} {
		name := "committed"
		if undo {
			name = "undone"
		}
		t.Run(name, func(t *testing.T) {
			s, _, root := executionFixture(t)
			ctx := context.Background()
			c := executionCreate(t, s)
			executionPut(t, c.Root, "disk.txt", "child\n")
			receipt, err := s.Apply(ctx, executionReview(t, s, c.ID))
			if err != nil {
				t.Fatal(err)
			}
			diskText(t, root, "disk.txt", "child\n")
			safeties := []string{receipt.SafetySnapshotID}
			prune := func() {
				t.Helper()
				executionPut(t, root, "later.txt", name)
				if _, err := s.history.store.Snapshot(ctx, localhistory.SnapshotOptions{Kind: "timeline"}); err != nil {
					t.Fatal(err)
				}
				// The physical quota path ignores age/count preferences and
				// previously removed the integration backup (and its scope WAL).
				if _, err := s.history.store.Prune(ctx, localhistory.RetentionPolicy{BudgetBytes: 1, MaxSnapshots: 1, KeepHours: 24}); err != nil {
					t.Fatal(err)
				}
				for _, id := range safeties {
					if err := s.history.store.Verify(ctx, id); err != nil {
						t.Fatalf("receipt safety evidence pruned: %s: %v", id, err)
					}
				}
			}
			prune()
			if undo {
				inverse, err := s.Undo(ctx, receipt.OperationID)
				if err != nil {
					t.Fatalf("undo after retention: %v", err)
				}
				safeties = append(safeties, inverse.SafetySnapshotID)
				prune()
				diskText(t, root, "disk.txt", "original\n")
				again, err := s.Undo(ctx, receipt.OperationID)
				if err != nil || again != inverse {
					t.Fatalf("undo lost durable outcome after retention: %+v %v", again, err)
				}
			}
			// The unrelated later file and the integration result must survive.
			diskText(t, root, "later.txt", name)
			if err := s.Unpin(c.ID, c.Owner); err != nil {
				t.Fatal(err)
			}
			if err := s.Cleanup(ctx, c.ID, c.Owner); err != nil {
				t.Fatalf("cleanup after retention: %v", err)
			}
			saved, err := s.Inspect(c.ID)
			if err != nil || !saved.Removed || saved.ArchiveCommit == "" {
				t.Fatalf("cleanup did not archive result: %+v %v", saved, err)
			}
			for _, id := range safeties {
				if err := s.history.store.Verify(ctx, id); err != nil {
					t.Fatalf("cleanup removed safety evidence: %s: %v", id, err)
				}
			}
			if !undo {
				diskText(t, root, "disk.txt", "child\n")
			}
		})
	}
}

func TestExecutionRetentionMissingOrCorruptSafetyStillBlocksCleanup(t *testing.T) {
	for _, damage := range []string{"missing", "corrupt"} {
		t.Run(damage, func(t *testing.T) {
			s, _, root := executionFixture(t)
			ctx := context.Background()
			c := executionCreate(t, s)
			executionPut(t, c.Root, "disk.txt", "child\n")
			receipt, err := s.Apply(ctx, executionReview(t, s, c.ID))
			if err != nil {
				t.Fatal(err)
			}
			manifest := filepath.Join(s.history.store.Root(), "manifests", receipt.SafetySnapshotID+".json")
			if damage == "missing" {
				err = os.Remove(manifest)
			} else {
				err = os.WriteFile(manifest, []byte("{}"), 0o600)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := s.Unpin(c.ID, c.Owner); err != nil {
				t.Fatal(err)
			}
			if err := s.Cleanup(ctx, c.ID, c.Owner); err == nil {
				t.Fatal("cleanup bypassed invalid safety evidence")
			}
			saved, err := s.Inspect(c.ID)
			if err != nil || saved.Removed {
				t.Fatalf("failed cleanup lost execution: %+v %v", saved, err)
			}
			diskText(t, c.Root, "disk.txt", "child\n")
			diskText(t, root, "disk.txt", "child\n")
		})
	}
}
