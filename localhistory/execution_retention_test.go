package localhistory

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func retentionExecutionScope(t *testing.T, store *Store, source, status string) (Manifest, Manifest) {
	t.Helper()
	ctx := context.Background()
	writeTestFile(t, store.workspaceRoot, "execution.txt", []byte("before\n"), 0o644)
	before, err := store.Snapshot(ctx, SnapshotOptions{Kind: "safety"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.BeginMessageScope("execution/agent", "operation", before.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordAgentOperation(ctx, AgentOperation{
		ID: "operation", SessionID: "execution/agent", MessageID: "operation", AgentID: "agent", Source: source,
		Paths: []OperationPath{{Path: "execution.txt",
			Before: &OperationState{Type: EntryFile, Mode: 0o644, Data: []byte("before\n")},
			After:  &OperationState{Type: EntryFile, Mode: 0o644, Data: []byte("after\n")},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	if status == "committed" || status == "aborted" {
		if err := store.CompleteAgentOperation("operation", status == "committed"); err != nil {
			t.Fatal(err)
		}
	}
	writeTestFile(t, store.workspaceRoot, "execution.txt", []byte("after\n"), 0o644)
	after, err := store.Snapshot(ctx, SnapshotOptions{Kind: "safety"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CloseMessageScope("execution/agent", "operation", after.ID, []string{"execution.txt"}, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	return before, after
}

func retentionSnapshot(t *testing.T, store *Store, text string) Manifest {
	t.Helper()
	writeTestFile(t, store.workspaceRoot, "execution.txt", []byte(text), 0o644)
	manifest, err := store.Snapshot(context.Background(), SnapshotOptions{Kind: "safety"})
	if err != nil {
		t.Fatal(err)
	}
	return manifest
}

func TestExecutionRetentionPreservesJournalBackedScopes(t *testing.T) {
	for _, source := range []string{"execution_apply", "execution_undo"} {
		for _, status := range []string{"prepared", "committed", "ambiguous", "aborted"} {
			for _, policy := range []RetentionPolicy{{MaxSnapshots: 1}, {BudgetBytes: 1, KeepHours: 24}} {
				t.Run(fmt.Sprintf("%s/%s/budget=%d", source, status, policy.BudgetBytes), func(t *testing.T) {
					store, workspace := newTestStore(t)
					before, after := retentionExecutionScope(t, store, source, status)
					// A different, ordinary closed scope must remain prunable,
					// even in the execution namespace and with safety-kind snapshots.
					ordinary := retentionSnapshot(t, store, "ordinary\n")
					if err := store.BeginMessageScope("execution/agent", "ordinary", ordinary.ID, time.Now()); err != nil {
						t.Fatal(err)
					}
					recordAgentFileOperation(t, store, "execution/agent", "ordinary", "agent", "write_file", "execution.txt", []byte("ordinary\n"), []byte("latest\n"))
					if err := store.CloseMessageScope("execution/agent", "ordinary", ordinary.ID, nil, nil, time.Now()); err != nil {
						t.Fatal(err)
					}
					latest := retentionSnapshot(t, store, "latest\n")
					if status == "ambiguous" {
						// Restart changes prepared operations to ambiguous; their
						// safety evidence must remain protected after that transition.
						root := store.root
						if err := store.Close(); err != nil {
							t.Fatal(err)
						}
						var err error
						store, err = Open(root, workspace)
						if err != nil {
							t.Fatal(err)
						}
						t.Cleanup(func() { _ = store.Close() })
					}
					result, err := store.Prune(context.Background(), policy)
					if err != nil {
						t.Fatal(err)
					}
					if result.RemovedSnapshots == 0 {
						t.Fatal("ordinary history was exempted from retention")
					}
					for _, snapshot := range []Manifest{before, after} {
						_, err := store.LoadManifest(snapshot.ID)
						if status == "aborted" {
							if !os.IsNotExist(err) {
								t.Fatalf("aborted safety snapshot retained: %s: %v", snapshot.ID, err)
							}
						} else if err != nil {
							t.Fatalf("execution safety snapshot pruned: %s: %v", snapshot.ID, err)
						}
					}
					if _, err := store.LoadManifest(ordinary.ID); !os.IsNotExist(err) {
						t.Fatalf("ordinary closed scope retained: %v", err)
					}
					if _, err := store.LoadManifest(latest.ID); err != nil {
						t.Fatalf("latest snapshot pruned: %v", err)
					}
					var count int
					if err := store.db.QueryRow(`SELECT count(*) FROM history_agent_operation_paths WHERE operation_id='operation'`).Scan(&count); err != nil {
						t.Fatal(err)
					}
					if status != "aborted" && count != 1 || status == "aborted" && count != 0 {
						t.Fatalf("operation WAL path count=%d, status=%s", count, status)
					}
					if err := store.VerifyAll(context.Background()); err != nil {
						t.Fatal(err)
					}
				})
			}
		}
	}
}

func TestExecutionRetentionFailsClosedOnDamagedEvidence(t *testing.T) {
	for _, damage := range []string{"missing scope", "unknown status", "empty before", "empty closed after", "missing index", "missing manifest", "corrupt manifest", "missing object", "corrupt chunk"} {
		t.Run(damage, func(t *testing.T) {
			store, _ := newTestStore(t)
			before, after := retentionExecutionScope(t, store, "execution_apply", "committed")
			ordinary := retentionSnapshot(t, store, "ordinary\n")
			retentionSnapshot(t, store, "latest\n")
			exec := func(query string, args ...any) {
				t.Helper()
				if _, err := store.db.Exec(query, args...); err != nil {
					t.Fatal(err)
				}
			}
			switch damage {
			case "missing scope":
				exec(`PRAGMA foreign_keys=OFF`)
				exec(`DELETE FROM history_message_scopes WHERE message_id='operation'`)
				exec(`PRAGMA foreign_keys=ON`)
			case "unknown status":
				exec(`UPDATE history_agent_operations SET status='unknown'`)
			case "empty before":
				exec(`UPDATE history_message_scopes SET before_snapshot_id=''`)
			case "empty closed after":
				exec(`UPDATE history_message_scopes SET after_snapshot_id=''`)
			case "missing index":
				exec(`DELETE FROM history_snapshots WHERE snapshot_id=?`, after.ID)
			case "missing manifest", "corrupt manifest":
				path := filepath.Join(store.root, "manifests", after.ID+".json")
				if damage == "missing manifest" {
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
				} else if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "missing object", "corrupt chunk":
				hash := after.Entries["execution.txt"].Hash
				path, err := objectPath(store.root, "files", hash, ".json")
				if err != nil {
					t.Fatal(err)
				}
				if damage == "missing object" {
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
				} else {
					object, err := store.readFileObject(hash)
					if err != nil {
						t.Fatal(err)
					}
					path, err = objectPath(store.root, "chunks", object.Chunks[0].Hash, "")
					if err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(path, []byte("corrupt"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
			}
			result, err := store.Prune(context.Background(), RetentionPolicy{MaxSnapshots: 1, BudgetBytes: 1})
			if err == nil {
				t.Fatal("prune accepted damaged execution safety evidence")
			}
			if result.RemovedSnapshots != 0 {
				t.Fatalf("prune mutated history before failing: %+v", result)
			}
			for _, id := range []string{before.ID, ordinary.ID} {
				if _, err := store.LoadManifest(id); err != nil {
					t.Fatalf("failed prune deleted evidence %s: %v", id, err)
				}
				var indexed bool
				if err := store.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM history_snapshots WHERE snapshot_id=?)`, id).Scan(&indexed); err != nil || !indexed {
					t.Fatalf("failed prune deleted index %s: indexed=%v err=%v", id, indexed, err)
				}
			}
		})
	}
}

func TestExecutionRetentionCaptureScopePreventsGap(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	writeTestFile(t, store.workspaceRoot, "execution.txt", []byte("before\n"), 0o644)
	safety, err := store.SnapshotMessageScope(ctx, "execution/agent", "operation", SnapshotOptions{Kind: "safety"})
	if err != nil {
		t.Fatal(err)
	}
	// Retention can run before RecordAgentOperation. The capture must already
	// belong to an active scope, rather than relying on still being newest.
	retentionSnapshot(t, store, "later\n")
	if _, err := store.Prune(ctx, RetentionPolicy{MaxSnapshots: 1, BudgetBytes: 1}); err != nil {
		t.Fatal(err)
	}
	if err := store.Verify(ctx, safety.ID); err != nil {
		t.Fatalf("capture was not protected before operation registration: %v", err)
	}
	scope, ok, err := store.MessageScope("execution/agent", "operation")
	if err != nil || !ok || scope.State != "active" || scope.BeforeSnapshot != safety.ID {
		t.Fatalf("capture scope=%+v exists=%v err=%v", scope, ok, err)
	}
	if _, err := store.SnapshotMessageScope(ctx, "execution/agent", "operation", SnapshotOptions{Kind: "safety"}); err == nil {
		t.Fatal("duplicate capture silently replaced existing scope")
	}
	scope, ok, err = store.MessageScope("execution/agent", "operation")
	if err != nil || !ok || scope.BeforeSnapshot != safety.ID {
		t.Fatalf("duplicate capture changed original scope: %+v %v %v", scope, ok, err)
	}
}

func TestExecutionRetentionCaptureScopeUnderContention(t *testing.T) {
	store, workspace := newTestStore(t)
	writeTestFile(t, workspace, "execution.txt", []byte("unchanged\n"), 0o644)
	ctx := context.Background()
	const captures = 32
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < captures; i++ {
			message := fmt.Sprintf("capture-%d", i)
			manifest, err := store.SnapshotMessageScope(ctx, "execution/agent", message, SnapshotOptions{Kind: "safety"})
			if err != nil {
				results <- err
				return
			}
			if err := store.Verify(ctx, manifest.ID); err != nil {
				results <- fmt.Errorf("capture %s lost under concurrent prune: %w", message, err)
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < captures; i++ {
			// Advance newest before pruning so it cannot accidentally protect
			// an unregistered capture in place of the scope lock.
			if _, err := store.Snapshot(ctx, SnapshotOptions{Kind: "timeline"}); err != nil {
				results <- err
				return
			}
			if _, err := store.Prune(ctx, RetentionPolicy{MaxSnapshots: 1, BudgetBytes: 1}); err != nil {
				results <- err
				return
			}
		}
	}()
	close(start)
	wg.Wait()
	close(results)
	for err := range results {
		t.Error(err)
	}
	for i := 0; i < captures; i++ {
		scope, ok, err := store.MessageScope("execution/agent", fmt.Sprintf("capture-%d", i))
		if err != nil || !ok {
			t.Fatalf("capture %d missing scope: exists=%v err=%v", i, ok, err)
		}
		if err := store.Verify(ctx, scope.BeforeSnapshot); err != nil {
			t.Fatalf("capture %d lost safety data: %v", i, err)
		}
	}
}
