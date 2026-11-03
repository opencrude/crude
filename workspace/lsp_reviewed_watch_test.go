package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"crude/fileio"
)

func TestReviewedWatchRetainsOnlyExactTargetEvents(t *testing.T) {
	var registry reviewedWatchRegistry
	a, b := filepath.Join(t.TempDir(), "a"), filepath.Join(t.TempDir(), "b")
	tx, err := registry.begin([]FileEdit{{Path: a}, {Path: b}})
	if err != nil {
		t.Fatal(err)
	}
	if registry.deferEvent(t.Context(), a+"-unrelated", 2, nil) {
		t.Fatal("unrelated project input was deferred")
	}
	if err := tx.check(); err == nil {
		t.Fatal("unrelated input did not revoke the review")
	}
	drainReviewedWatch(&registry, tx)
	tx, err = registry.begin([]FileEdit{{Path: a}, {Path: b}})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.beginCommit(a); err != nil {
		t.Fatal(err)
	}
	if !registry.deferEvent(t.Context(), a, 1, nil) {
		t.Fatal("atomic replacement event was not retained")
	}
	tx.endCommit(a, true)
	if err := tx.beginCommit(b); err != nil {
		t.Fatal(err)
	}
	tx.endCommit(b, true)
	events := drainReviewedWatch(&registry, tx)
	if len(events) != 1 || events[0].path != a || events[0].kind != 1 {
		t.Fatalf("retained events = %+v", events)
	}
	if registry.deferEvent(t.Context(), a, 1, nil) {
		t.Fatal("completed transaction retained a later event")
	}
}

func TestReviewedWatchRefusesAmbiguousTargetChanges(t *testing.T) {
	for _, when := range []string{"before commit", "remove before commit", "unexpected write", "second create", "second remove", "remove after create", "watch gap"} {
		t.Run(when, func(t *testing.T) {
			var registry reviewedWatchRegistry
			a, b := filepath.Join(t.TempDir(), "a"), filepath.Join(t.TempDir(), "b")
			tx, err := registry.begin([]FileEdit{{Path: a}, {Path: b}})
			if err != nil {
				t.Fatal(err)
			}
			if when != "before commit" && when != "remove before commit" {
				if err := tx.beginCommit(a); err != nil {
					t.Fatal(err)
				}
				tx.endCommit(a, true)
			}
			switch when {
			case "before commit":
				registry.deferEvent(t.Context(), a, 2, nil)
			case "remove before commit":
				registry.deferEvent(t.Context(), a, 3, nil)
			case "unexpected write":
				registry.deferEvent(t.Context(), a, 2, nil)
			case "second create":
				registry.deferEvent(t.Context(), a, 1, nil)
				registry.deferEvent(t.Context(), a, 1, nil)
			case "second remove":
				registry.deferEvent(t.Context(), a, 3, nil)
				registry.deferEvent(t.Context(), a, 3, nil)
			case "remove after create":
				registry.deferEvent(t.Context(), a, 1, nil)
				registry.deferEvent(t.Context(), a, 3, nil)
			case "watch gap":
				registry.setReliable(false)
			}
			if err := tx.beginCommit(b); err == nil {
				t.Fatal("ambiguous observation admitted another commit")
			}
			drainReviewedWatch(&registry, tx)
		})
	}
}

func TestReviewedWatchRetainsAtomicRemoveCreateSequence(t *testing.T) {
	for _, beforeReturn := range []bool{true, false} {
		t.Run(map[bool]string{true: "during commit", false: "after commit"}[beforeReturn], func(t *testing.T) {
			var registry reviewedWatchRegistry
			a, b := filepath.Join(t.TempDir(), "a"), filepath.Join(t.TempDir(), "b")
			tx, err := registry.begin([]FileEdit{{Path: a}, {Path: b}})
			if err != nil {
				t.Fatal(err)
			}
			if err := tx.beginCommit(a); err != nil {
				t.Fatal(err)
			}
			if !beforeReturn {
				tx.endCommit(a, true)
			}
			for _, kind := range []int{3, 1} {
				if !registry.deferEvent(t.Context(), a, kind, func() {
					t.Fatal("own atomic replacement invalidated its semantic source")
				}) {
					t.Fatal("own atomic replacement event was not retained")
				}
			}
			if beforeReturn {
				tx.endCommit(a, true)
			}
			if err := tx.beginCommit(b); err != nil {
				t.Fatal("own atomic replacement stopped the next reviewed file", err)
			}
			tx.endCommit(b, true)
			if err := tx.check(); err != nil {
				t.Fatal(err)
			}
			events := drainReviewedWatch(&registry, tx)
			if len(events) != 2 || events[0].path != a || events[0].kind != 3 ||
				events[1].path != a || events[1].kind != 1 {
				t.Fatalf("native replacement evidence was lost or reordered: %+v", events)
			}
		})
	}
}

func TestReviewedWatchCannotStartDuringGap(t *testing.T) {
	var registry reviewedWatchRegistry
	registry.setReliable(false)
	if _, err := registry.begin([]FileEdit{{Path: filepath.Join(t.TempDir(), "file")}}); err == nil {
		t.Fatal("review started while project observation was incomplete")
	}
	registry.setReliable(true)
	tx, err := registry.begin([]FileEdit{{Path: filepath.Join(t.TempDir(), "file")}})
	if err != nil {
		t.Fatal(err)
	}
	drainReviewedWatch(&registry, tx)
}

func TestReviewedWatchMultiFileCommitAndExternalChange(t *testing.T) {
	for _, external := range []bool{false, true} {
		t.Run(map[bool]string{false: "own events", true: "external target event"}[external], func(t *testing.T) {
			root := t.TempDir()
			a, b := filepath.Join(root, "a"), filepath.Join(root, "b")
			for _, path := range []string{a, b} {
				if err := os.WriteFile(path, []byte("before"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			m := NewLSPManager(nil)
			t.Cleanup(m.Close)
			edits := []FileEdit{{Path: a, OldContent: "before", NewContent: "after"}, {Path: b, OldContent: "before", NewContent: "after"}}
			tx, err := m.reviewWatch.begin(edits)
			if err != nil {
				t.Fatal(err)
			}
			ops := NewFileOpsService(root, nil, nil)
			commits := 0
			ops.commitWorkspaceEdit = func(from, to string) error {
				if err := fileio.CommitAtomicFile(from, to); err != nil {
					return err
				}
				commits++
				m.NotifyWatchedFilesChanged(context.Background(), to, 1)
				if external && commits == 1 {
					m.NotifyWatchedFilesChanged(context.Background(), b, 2)
				}
				return nil
			}
			result := ops.applyWorkspaceEdit(edits, nil, func() error { return nil }, m, tx)
			if external {
				if result.Err == nil || result.NoEffect || commits != 1 {
					t.Fatalf("external change did not stop the second commit: %+v", result)
				}
			} else if result.Err != nil || commits != 2 {
				t.Fatalf("owned notifications stopped the transaction: %+v", result)
			}
			want := "after"
			if external {
				want = "before"
			}
			for _, path := range []string{a, b} {
				data, err := os.ReadFile(path)
				if err != nil || string(data) != want {
					t.Fatalf("%s = %q, %v; want %q", path, data, err, want)
				}
			}
			m.replayReviewedWatch(tx)
		})
	}
}

func TestReviewedWatchOverflowDoesNotDropEvent(t *testing.T) {
	var registry reviewedWatchRegistry
	path := filepath.Join(t.TempDir(), "file")
	tx, err := registry.begin([]FileEdit{{Path: path}})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.beginCommit(path); err != nil {
		t.Fatal(err)
	}
	for range maxReviewedWatchEvents {
		if !registry.deferEvent(t.Context(), path, 1, nil) {
			t.Fatal("queue filled early")
		}
	}
	delivered := make(chan bool, 1)
	go func() {
		delivered <- registry.deferEvent(t.Context(), path, 2, nil)
	}()
	if err := tx.check(); err == nil || errors.Is(err, context.Canceled) {
		t.Fatal("overflow did not revoke the transaction")
	}
	if !registry.finish(tx) {
		t.Fatal("lost queued events")
	}
	first, ok := registry.nextReplay()
	if !ok || first.kind != 1 {
		t.Fatalf("first event = %+v, %v", first, ok)
	}
	if !<-delivered {
		t.Fatal("overflow event was dropped after space became available")
	}
	count, last := 1, first
	for {
		event, ok := registry.nextReplay()
		if !ok {
			break
		}
		count, last = count+1, event
	}
	if count != maxReviewedWatchEvents+1 || last.kind != 2 {
		t.Fatalf("replayed %d events; last = %+v", count, last)
	}
}

func TestReviewedWatchOrdersEventsArrivingDuringReplay(t *testing.T) {
	var registry reviewedWatchRegistry
	path := filepath.Join(t.TempDir(), "file")
	tx, err := registry.begin([]FileEdit{{Path: path}})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.beginCommit(path); err != nil {
		t.Fatal(err)
	}
	registry.deferEvent(t.Context(), path, 1, nil)
	tx.endCommit(path, true)
	if !registry.finish(tx) {
		t.Fatal("missing first event")
	}
	first, ok := registry.nextReplay()
	if !ok || first.kind != 1 {
		t.Fatalf("first event = %+v, %v", first, ok)
	}
	// The first event has been taken, but is not necessarily sent yet. The
	// next event must still wait behind it on the same owner.
	if !registry.deferEvent(t.Context(), path, 2, nil) {
		t.Fatal("later event bypassed replay")
	}
	next, ok := registry.nextReplay()
	if !ok || next.kind != 2 {
		t.Fatalf("later event = %+v, %v", next, ok)
	}
	if _, ok := registry.nextReplay(); ok {
		t.Fatal("unexpected extra event")
	}
	if registry.deferEvent(t.Context(), path, 3, nil) {
		t.Fatal("replay did not release the target")
	}
}

func TestReviewedWatchInvalidatesOtherChangesBeforeOrderedReplay(t *testing.T) {
	root := t.TempDir()
	target, unrelated := filepath.Join(root, "target"), filepath.Join(root, "config")
	m := NewLSPManager(nil)
	t.Cleanup(m.Close)
	tx, err := m.reviewWatch.begin([]FileEdit{{Path: target}})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.beginCommit(target); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	before := m.contextGeneration
	m.mu.Unlock()
	m.NotifyWatchedFilesChanged(t.Context(), target, 1)
	m.NotifyWatchedFilesChanged(t.Context(), unrelated, 2)
	m.mu.Lock()
	after := m.contextGeneration
	m.mu.Unlock()
	if after != before+1 || tx.check() == nil {
		t.Fatalf("external change did not immediately revoke the review: %d to %d", before, after)
	}
	tx.endCommit(target, true)
	events := drainReviewedWatch(&m.reviewWatch, tx)
	if len(events) != 2 || events[0].path != target || events[0].invalidated ||
		events[1].path != unrelated || !events[1].invalidated {
		t.Fatalf("ordered events or invalidation changed: %+v", events)
	}
}

// An unrelated event can arrive before a target notification. It must still
// revoke the in-flight transaction, including after its final disk commit.
func TestReviewedWatchUnrelatedChangeBeforeTargetNotification(t *testing.T) {
	root := t.TempDir()
	target, unrelated := filepath.Join(root, "target"), filepath.Join(root, "config")
	if err := os.WriteFile(target, []byte("before"), 0600); err != nil {
		t.Fatal(err)
	}
	m := NewLSPManager(nil)
	t.Cleanup(m.Close)
	edits := []FileEdit{{Path: target, OldContent: "before", NewContent: "after"}}
	tx, err := m.reviewWatch.begin(edits)
	if err != nil {
		t.Fatal(err)
	}
	ops := NewFileOpsService(root, nil, nil)
	ops.commitWorkspaceEdit = func(from, to string) error {
		if err := fileio.CommitAtomicFile(from, to); err != nil {
			return err
		}
		m.NotifyWatchedFilesChanged(context.Background(), unrelated, 2)
		return nil
	}
	result := ops.applyWorkspaceEdit(edits, nil, func() error { return nil }, m, tx)
	if result.Err == nil || result.NoEffect || result.Detail != "workspace edit result needs inspection" {
		t.Fatalf("unrelated change during commit was reported as success: %+v", result)
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != "after" {
		t.Fatalf("committed target was lost: %q, %v", data, err)
	}
	m.replayReviewedWatch(tx)
}

func drainReviewedWatch(registry *reviewedWatchRegistry, tx *reviewedWatchTxn) []reviewedWatchEvent {
	if !registry.finish(tx) {
		return nil
	}
	var out []reviewedWatchEvent
	for {
		event, ok := registry.nextReplay()
		if !ok {
			return out
		}
		out = append(out, event)
	}
}
