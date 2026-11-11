package localhistory

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofrs/flock"
)

func retainedSnapshot(t *testing.T, store *Store, reference string) Manifest {
	t.Helper()
	manifest, err := store.SnapshotRetained(context.Background(), reference, SnapshotOptions{Kind: "safety", Label: "Reviewed edit"})
	if err != nil {
		t.Fatal(err)
	}
	return manifest
}

func TestRetainedSnapshotSurvivesPruneReopenAndExplicitRelease(t *testing.T) {
	for _, policy := range []RetentionPolicy{{MaxSnapshots: 1}, {KeepHours: 24, BudgetBytes: 1}} {
		t.Run(fmt.Sprintf("budget=%d", policy.BudgetBytes), func(t *testing.T) {
			store, workspace := newTestStore(t)
			ctx := context.Background()
			content := []byte("original\x00bytes\r\n")
			writeTestFile(t, workspace, "review.txt", content, 0o751)
			info, err := os.Stat(filepath.Join(workspace, "review.txt"))
			if err != nil {
				t.Fatal(err)
			}
			before := retainedSnapshot(t, store, "review-1_before")
			hash := sha256.Sum256(content)
			entry := before.Entries["review.txt"]
			if entry.Hash != hex.EncodeToString(hash[:]) || entry.Size != int64(len(content)) || entry.Mode != storedMode(info.Mode()) {
				t.Fatalf("wrong original bytes/hash/mode: %+v", entry)
			}
			writeTestFile(t, workspace, "review.txt", []byte("changed"), 0o600)
			other := retainedSnapshot(t, store, "review-10_before")
			retentionSnapshot(t, store, "ordinary\n")
			latest := retentionSnapshot(t, store, "latest\n")
			if _, err := store.Prune(ctx, policy); err != nil {
				t.Fatal(err)
			}
			root := store.root
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			store, err = Open(root, workspace)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			loaded, err := store.LoadRetainedSnapshot(ctx, "review-1_before")
			if err != nil || !reflect.DeepEqual(loaded, before) {
				t.Fatalf("lost exact retained manifest: %+v err=%v", loaded, err)
			}
			data, err := store.readFileBytes(entry.Hash)
			if err != nil || !bytes.Equal(data, content) {
				t.Fatalf("lost retained bytes: %q err=%v", data, err)
			}
			var attribution int
			if err := store.db.QueryRow(`SELECT (SELECT COUNT(*) FROM history_message_scopes)+(SELECT COUNT(*) FROM history_agent_operations)`).Scan(&attribution); err != nil || attribution != 0 {
				t.Fatalf("human safety acquired agent attribution: count=%d err=%v", attribution, err)
			}
			for range 2 {
				if err := store.ReleaseRetainedSnapshot(ctx, "review-1_before"); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := store.LoadRetainedSnapshot(ctx, "review-1_before"); !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("released reference still loads: %v", err)
			}
			if _, err := store.Prune(ctx, RetentionPolicy{MaxSnapshots: 1, BudgetBytes: 1}); err != nil {
				t.Fatal(err)
			}
			if _, err := store.LoadManifest(before.ID); !os.IsNotExist(err) {
				t.Fatalf("released snapshot not pruned: %v", err)
			}
			if _, err := store.readFileBytes(entry.Hash); !os.IsNotExist(err) {
				t.Fatalf("released content not collected: %v", err)
			}
			if loaded, err := store.LoadRetainedSnapshot(ctx, "review-10_before"); err != nil || loaded.ID != other.ID {
				t.Fatalf("release affected another exact reference: %s err=%v", loaded.ID, err)
			}
			if _, err := store.LoadManifest(latest.ID); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRetainedSnapshotRejectsDuplicateBeforeCapture(t *testing.T) {
	store, workspace := newTestStore(t)
	writeTestFile(t, workspace, "review.txt", []byte("original"), 0o640)
	original := retainedSnapshot(t, store, "review-1")
	before, err := store.Stats()
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, workspace, "review.txt", []byte("later bytes"), 0o600)
	if manifest, err := store.SnapshotRetained(context.Background(), "review-1", SnapshotOptions{}); err == nil || manifest.ID != "" {
		t.Fatalf("duplicate succeeded: %s err=%v", manifest.ID, err)
	}
	after, err := store.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if after.Snapshots != before.Snapshots || after.Manifests != before.Manifests || after.FileObjects != before.FileObjects || after.ChunkObjects != before.ChunkObjects {
		t.Fatalf("duplicate captured later bytes: before=%+v after=%+v", before, after)
	}
	loaded, err := store.LoadRetainedSnapshot(context.Background(), "review-1")
	if err != nil || !reflect.DeepEqual(loaded, original) {
		t.Fatalf("duplicate retargeted original: %+v err=%v", loaded, err)
	}
}

func TestRetainedSnapshotRejectsInvalidReferenceBeforeIO(t *testing.T) {
	// A nil receiver proves validation precedes every store/DB/filesystem access.
	var store *Store
	for _, reference := range []string{"", strings.Repeat("a", 129), "../path", "/path", "a.b", "-a", "_a", "a b", "a\nb", "é", "a\x00b", "raw:args", `a\b`} {
		if _, err := store.SnapshotRetained(context.Background(), reference, SnapshotOptions{}); err == nil {
			t.Fatalf("accepted invalid reference %q", reference)
		}
		if _, err := store.LoadRetainedSnapshot(context.Background(), reference); err == nil {
			t.Fatalf("loaded invalid reference %q", reference)
		}
		if err := store.ReleaseRetainedSnapshot(context.Background(), reference); err == nil {
			t.Fatalf("released invalid reference %q", reference)
		}
	}
	for _, reference := range []string{"A", "0", "a_B-9", strings.Repeat("a", 128)} {
		if !validSafetyReference(reference) {
			t.Fatalf("rejected valid reference %q", reference)
		}
	}
}

func TestRetainedSnapshotDamagedEvidenceFailsClosed(t *testing.T) {
	for _, damage := range []string{
		"malformed reference", "invalid id", "missing index", "changed metadata", "missing entry", "changed entry", "extra entry",
		"missing manifest", "corrupt manifest", "missing object", "corrupt object", "missing chunk", "corrupt chunk",
	} {
		t.Run(damage, func(t *testing.T) {
			store, workspace := newTestStore(t)
			writeTestFile(t, workspace, "review.txt", []byte("original"), 0o640)
			safety := retainedSnapshot(t, store, "review-1")
			ordinary := retentionSnapshot(t, store, "ordinary\n")
			retentionSnapshot(t, store, "latest\n")
			exec := func(query string, args ...any) {
				t.Helper()
				if _, err := store.db.Exec(query, args...); err != nil {
					t.Fatal(err)
				}
			}
			switch damage {
			case "malformed reference":
				exec(`UPDATE history_retained_snapshots SET reference='../bad'`)
			case "invalid id":
				exec(`PRAGMA foreign_keys=OFF`)
				exec(`UPDATE history_retained_snapshots SET snapshot_id='../bad'`)
				exec(`PRAGMA foreign_keys=ON`)
			case "missing index":
				exec(`PRAGMA foreign_keys=OFF`)
				exec(`DELETE FROM history_snapshots WHERE snapshot_id=?`, safety.ID)
				exec(`PRAGMA foreign_keys=ON`)
			case "changed metadata":
				exec(`UPDATE history_snapshots SET label='different' WHERE snapshot_id=?`, safety.ID)
			case "missing entry":
				exec(`DELETE FROM history_snapshot_entries WHERE snapshot_id=?`, safety.ID)
			case "changed entry":
				exec(`UPDATE history_snapshot_entries SET mode=0 WHERE snapshot_id=?`, safety.ID)
			case "extra entry":
				exec(`INSERT INTO history_snapshot_entries(snapshot_id,path,entry_type,mode) VALUES(?,'extra','directory',448)`, safety.ID)
			case "missing manifest", "corrupt manifest":
				path := filepath.Join(store.root, "manifests", safety.ID+".json")
				if damage == "missing manifest" {
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
				} else if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
					t.Fatal(err)
				}
			default:
				hash := safety.Entries["review.txt"].Hash
				path, err := objectPath(store.root, "files", hash, ".json")
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(damage, "chunk") {
					object, err := store.readFileObject(hash)
					if err != nil {
						t.Fatal(err)
					}
					path, err = objectPath(store.root, "chunks", object.Chunks[0].Hash, "")
					if err != nil {
						t.Fatal(err)
					}
				}
				if strings.HasPrefix(damage, "missing") {
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
				} else if err := os.WriteFile(path, []byte("corrupt"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := store.LoadRetainedSnapshot(context.Background(), "review-1"); err == nil {
				t.Fatal("load accepted damaged evidence")
			}
			result, err := store.Prune(context.Background(), RetentionPolicy{MaxSnapshots: 1, BudgetBytes: 1})
			if err == nil || result.RemovedSnapshots != 0 {
				t.Fatalf("prune accepted damaged evidence: %+v err=%v", result, err)
			}
			if _, err := store.LoadManifest(ordinary.ID); err != nil {
				t.Fatalf("failed prune deleted ordinary manifest: %v", err)
			}
			var indexed bool
			if err := store.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM history_snapshots WHERE snapshot_id=?)`, ordinary.ID).Scan(&indexed); err != nil || !indexed {
				t.Fatalf("failed prune deleted ordinary index: %v %v", indexed, err)
			}
			root := store.root
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := Open(root, workspace)
			if err == nil {
				_ = reopened.Close()
				t.Fatal("startup repaired or accepted damaged retained evidence")
			}
			// In particular, orphan recovery must not reconstruct a missing
			// retained index from its otherwise valid immutable manifest.
			if damage == "missing index" {
				db, err := sql.Open("sqlite", filepath.Join(root, "history.db"))
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM history_snapshots WHERE snapshot_id=?)`, safety.ID).Scan(&indexed); err != nil || indexed {
					t.Fatalf("startup repaired missing retained index: %v %v", indexed, err)
				}
			}
		})
	}
}

func TestRetainedSnapshotRegistrationFailureDoesNotClaimProtection(t *testing.T) {
	store, workspace := newTestStore(t)
	writeTestFile(t, workspace, "review.txt", []byte("original"), 0o644)
	if _, err := store.db.Exec(`CREATE TRIGGER reject_retained BEFORE INSERT ON history_retained_snapshots BEGIN SELECT RAISE(ABORT,'injected registration failure'); END`); err != nil {
		t.Fatal(err)
	}
	if manifest, err := store.SnapshotRetained(context.Background(), "review-1", SnapshotOptions{}); err == nil || manifest.ID != "" {
		t.Fatalf("registration failure claimed protected success: %s %v", manifest.ID, err)
	}
	if _, err := store.LoadRetainedSnapshot(context.Background(), "review-1"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("failed registration retained a reference: %v", err)
	}
}

func TestRetainedSnapshotCaptureRejectsUnavailableOrCorruptData(t *testing.T) {
	for _, failure := range []string{"workspace unavailable", "corrupt existing chunk", "database unavailable"} {
		t.Run(failure, func(t *testing.T) {
			store, workspace := newTestStore(t)
			writeTestFile(t, workspace, "review.txt", []byte("original"), 0o644)
			manifest, err := store.Snapshot(context.Background(), SnapshotOptions{})
			if err != nil {
				t.Fatal(err)
			}
			switch failure {
			case "workspace unavailable":
				if err := os.Rename(workspace, workspace+"-moved"); err != nil {
					t.Fatal(err)
				}
			case "corrupt existing chunk":
				object, err := store.readFileObject(manifest.Entries["review.txt"].Hash)
				if err != nil {
					t.Fatal(err)
				}
				path, err := objectPath(store.root, "chunks", object.Chunks[0].Hash, "")
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("corrupt"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "database unavailable":
				if err := store.db.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if manifest, err := store.SnapshotRetained(context.Background(), "review-1", SnapshotOptions{}); err == nil || manifest.ID != "" {
				t.Fatalf("capture failure claimed protected success: %s %v", manifest.ID, err)
			}
			if _, err := store.LoadRetainedSnapshot(context.Background(), "review-1"); err == nil {
				t.Fatal("failed capture acquired a retained reference")
			}
		})
	}
}

func TestRetainedSnapshotCancellationAndClosePreserveOwnerReference(t *testing.T) {
	store, workspace := newTestStore(t)
	writeTestFile(t, workspace, "review.txt", []byte("original"), 0o644)
	ctx, cancel := context.WithCancel(context.Background())
	safety, err := store.SnapshotRetained(ctx, "review-1", SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if _, err := store.SnapshotRetained(ctx, "cancelled", SnapshotOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled capture: %v", err)
	}
	if _, err := store.LoadRetainedSnapshot(ctx, "review-1"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled load: %v", err)
	}
	if err := store.ReleaseRetainedSnapshot(ctx, "review-1"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled release: %v", err)
	}
	root := store.root
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SnapshotRetained(context.Background(), "closed", SnapshotOptions{}); err == nil {
		t.Fatal("closed store accepted capture")
	}
	if _, err := store.LoadRetainedSnapshot(context.Background(), "review-1"); err == nil {
		t.Fatal("closed store accepted load")
	}
	if err := store.ReleaseRetainedSnapshot(context.Background(), "review-1"); err == nil {
		t.Fatal("closed store accepted release")
	}
	reopened, err := Open(root, workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if loaded, err := reopened.LoadRetainedSnapshot(context.Background(), "review-1"); err != nil || loaded.ID != safety.ID {
		t.Fatalf("cancel/close unpinned owner reference: %s %v", loaded.ID, err)
	}
	if _, err := reopened.LoadRetainedSnapshot(context.Background(), "cancelled"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("cancelled capture persisted: %v", err)
	}
}

type retainedOperationContext struct {
	context.Context
	entered chan struct{}
	resume  chan struct{}
	once    sync.Once
}

func (ctx *retainedOperationContext) Err() error {
	ctx.once.Do(func() {
		close(ctx.entered)
		<-ctx.resume
	})
	return ctx.Context.Err()
}

func TestRetainedSnapshotCloseJoinsAdmittedCapture(t *testing.T) {
	store, workspace := newTestStore(t)
	writeTestFile(t, workspace, "review.txt", []byte("original"), 0o644)
	ctx := &retainedOperationContext{Context: context.Background(), entered: make(chan struct{}), resume: make(chan struct{})}
	captured := make(chan error, 1)
	go func() {
		_, err := store.SnapshotRetained(ctx, "review-1", SnapshotOptions{})
		captured <- err
	}()
	// SnapshotRetained checks context after operation admission and while
	// holding s.mu. Shutdown must join that owner, not close its DB early.
	<-ctx.entered
	closed := make(chan error, 1)
	go func() { closed <- store.Close() }()
	select {
	case err := <-closed:
		close(ctx.resume)
		if cleanupErr := <-captured; cleanupErr != nil {
			t.Logf("Cleanup join: %v", cleanupErr)
		}
		t.Fatalf("close did not join the admitted capture: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(ctx.resume)
	if err := <-captured; err != nil {
		t.Fatalf("shutdown interrupted admitted capture: %v", err)
	}
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(store.root, workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if _, err := reopened.LoadRetainedSnapshot(context.Background(), "review-1"); err != nil {
		t.Fatalf("joined capture lost its durable owner reference: %v", err)
	}
}

func TestRetainedSnapshotUsesProcessMutationGate(t *testing.T) {
	store, workspace := newTestStore(t)
	writeTestFile(t, workspace, "review.txt", []byte("original"), 0o644)
	retainedSnapshot(t, store, "review-1")
	lock := flock.New(filepath.Join(store.root, "mutation.lock"))
	if err := lock.Lock(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Unlock() }()
	for _, operation := range []func(context.Context) error{
		func(ctx context.Context) error {
			_, err := store.SnapshotRetained(ctx, "blocked", SnapshotOptions{})
			return err
		},
		func(ctx context.Context) error {
			_, err := store.LoadRetainedSnapshot(ctx, "review-1")
			return err
		},
		func(ctx context.Context) error { return store.ReleaseRetainedSnapshot(ctx, "review-1") },
	} {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
		err := operation(ctx)
		cancel()
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("operation bypassed process mutation gate: %v", err)
		}
	}
}

func TestRetainedSnapshotCaptureUnderConcurrentPrune(t *testing.T) {
	store, workspace := newTestStore(t)
	writeTestFile(t, workspace, "review.txt", []byte("original"), 0o644)
	ctx := context.Background()
	const captures = 24
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		for i := range captures {
			reference := fmt.Sprintf("review-%d", i)
			manifest, err := store.SnapshotRetained(ctx, reference, SnapshotOptions{Kind: "safety"})
			if err != nil {
				results <- err
				return
			}
			if loaded, err := store.LoadRetainedSnapshot(ctx, reference); err != nil || loaded.ID != manifest.ID {
				results <- fmt.Errorf("capture %s lost under concurrent prune: %v", reference, err)
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		<-start
		for range captures {
			// Advance newest so it cannot mask an unregistered safety snapshot.
			if _, err := store.Snapshot(ctx, SnapshotOptions{}); err != nil {
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
	for i := range captures {
		if _, err := store.LoadRetainedSnapshot(ctx, fmt.Sprintf("review-%d", i)); err != nil {
			t.Fatalf("retained capture %d disappeared: %v", i, err)
		}
	}
}
