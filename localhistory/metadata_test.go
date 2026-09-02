package localhistory

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

func TestMetadataTimelineAndMessageScopeSurviveReopen(t *testing.T) {
	workspace := t.TempDir()
	storeRoot := t.TempDir()
	store, err := Open(storeRoot, workspace)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, workspace, "a.txt", []byte("before"), 0o644)
	before, err := store.Snapshot(context.Background(), SnapshotOptions{Kind: "before_message", Label: "message"})
	if err != nil {
		t.Fatal(err)
	}
	opened := time.Now()
	if err := store.BeginMessageScope("session", "msg-1", before.ID, opened); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, workspace, "a.txt", []byte("after"), 0o644)
	after, err := store.Snapshot(context.Background(), SnapshotOptions{Kind: "after_message", ParentID: before.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CloseMessageScope("session", "msg-1", after.ID, []string{"a.txt"}, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	store, err = Open(storeRoot, workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	scope, ok, err := store.LatestUndoableScope("session")
	if err != nil || !ok || scope.MessageID != "msg-1" || len(scope.ChangedPaths) != 1 {
		t.Fatalf("scope=%+v ok=%v err=%v", scope, ok, err)
	}
	timeline, err := store.Timeline(10)
	if err != nil || len(timeline) != 2 {
		t.Fatalf("timeline=%+v err=%v", timeline, err)
	}
	stats, err := store.Stats()
	if err != nil || stats.Snapshots != 2 || stats.LogicalBytes < int64(len("before")+len("after")) {
		t.Fatalf("stats=%+v err=%v", stats, err)
	}
}

func TestInterruptedScopesRecoverAtOpenBoundary(t *testing.T) {
	store, workspace := newTestStore(t)
	writeTestFile(t, workspace, "a", []byte("a"), 0o644)
	snapshot, err := store.Snapshot(context.Background(), SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.BeginMessageScope("s", "m", snapshot.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkInterruptedScopes(time.Now()); err != nil {
		t.Fatal(err)
	}
	scope, ok, err := store.MessageScope("s", "m")
	if err != nil || !ok || scope.State != "interrupted" {
		t.Fatalf("scope=%+v ok=%v err=%v", scope, ok, err)
	}
}

func TestOpenRecoversCommittedManifestMissingFromIndex(t *testing.T) {
	workspace := t.TempDir()
	root := t.TempDir()
	store, err := Open(root, workspace)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, workspace, "a", []byte("a"), 0o644)
	snapshot, err := store.Snapshot(context.Background(), SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`DELETE FROM history_snapshots WHERE snapshot_id=?`, snapshot.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(root, workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	timeline, err := store.Timeline(10)
	if err != nil || len(timeline) != 1 || timeline[0].ID != snapshot.ID {
		t.Fatalf("recovered timeline=%+v err=%v", timeline, err)
	}
}

func TestMetadataKeepsOnlyEarliestBaselineClassification(t *testing.T) {
	workspace := t.TempDir()
	root := t.TempDir()
	store, err := Open(root, workspace)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, workspace, "a", []byte("one"), 0o644)
	first, err := store.Snapshot(context.Background(), SnapshotOptions{Kind: "baseline", Label: "Workspace baseline"})
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, workspace, "a", []byte("two"), 0o644)
	second, err := store.Snapshot(context.Background(), SnapshotOptions{Kind: "baseline", Label: "Workspace baseline"})
	if err != nil {
		t.Fatal(err)
	}
	_ = store.Close()
	store, err = Open(root, workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	records, err := store.Timeline(10)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]string{}
	for _, r := range records {
		kinds[r.ID] = r.Kind
	}
	if kinds[first.ID] != "baseline" || kinds[second.ID] != "startup_reconcile" {
		t.Fatalf("baseline kinds=%v", kinds)
	}
}

func TestRetentionPreservesMessageAndRestoreSnapshots(t *testing.T) {
	store, workspace := newTestStore(t)
	var snapshots []Manifest
	for i := 0; i < 6; i++ {
		writeTestFile(t, workspace, "a", []byte{byte('a' + i)}, 0o644)
		snapshot, err := store.Snapshot(context.Background(), SnapshotOptions{Kind: "timeline"})
		if err != nil {
			t.Fatal(err)
		}
		snapshots = append(snapshots, snapshot)
	}
	if err := store.BeginMessageScope("s", "m", snapshots[0].ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := store.CloseMessageScope("s", "m", snapshots[1].ID, []string{"a"}, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	result, err := store.Prune(context.Background(), RetentionPolicy{MaxSnapshots: 2})
	if err != nil {
		t.Fatal(err)
	}
	if result.RemovedSnapshots == 0 || result.RemovedObjects == 0 {
		t.Fatalf("prune result=%+v", result)
	}
	for _, id := range []string{snapshots[0].ID, snapshots[1].ID, snapshots[len(snapshots)-1].ID} {
		if _, err := store.LoadManifest(id); err != nil {
			t.Fatalf("protected snapshot %s removed: %v", id, err)
		}
	}
	if err := store.VerifyAll(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestMessageRestorePlanFiltersPathsAndRequiresExactForLaterChanges(t *testing.T) {
	store, workspace := newTestStore(t)
	writeTestFile(t, workspace, "owned.txt", []byte("head\nbefore\ntail\n"), 0o644)
	writeTestFile(t, workspace, "unrelated.txt", []byte("u0"), 0o644)
	before, err := store.Snapshot(context.Background(), SnapshotOptions{Kind: "before_message"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.BeginMessageScope("s", "m", before.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, workspace, "owned.txt", []byte("head\nmessage\ntail\n"), 0o644)
	writeTestFile(t, workspace, "unrelated.txt", []byte("u1"), 0o644)
	after, err := store.Snapshot(context.Background(), SnapshotOptions{Kind: "after_message", ParentID: before.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CloseMessageScope("s", "m", after.ID, []string{"owned.txt"}, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, workspace, "owned.txt", []byte("head\nmessage\ntail\nlater addition\n"), 0o644)
	writeTestFile(t, workspace, "unrelated.txt", []byte("u2"), 0o644)
	plan, err := store.PlanMessageRestore(context.Background(), "s", "m", SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Changes) != 1 || plan.Changes[0].Path != "owned.txt" || !plan.Changes[0].AutoMerged || plan.Changes[0].ConflictReason != "" {
		t.Fatalf("message plan=%+v", plan.Changes)
	}
	if !strings.Contains(plan.Changes[0].TextDiff, "owned.txt") || !strings.Contains(plan.Changes[0].TextDiff, "before") {
		t.Fatalf("diff=%q", plan.Changes[0].TextDiff)
	}
	if _, err := store.ApplyRestore(context.Background(), plan.ID, []string{"owned.txt"}); err != nil {
		t.Fatal(err)
	}
	if got := string(readTestFile(t, workspace, "owned.txt")); got != "head\nbefore\ntail\nlater addition\n" {
		t.Fatalf("merged restore=%q", got)
	}
	if got := string(readTestFile(t, workspace, "unrelated.txt")); got != "u2" {
		t.Fatalf("unrelated path changed=%q", got)
	}
}

func TestBinaryMessageConflictRequiresExactChoice(t *testing.T) {
	store, workspace := newTestStore(t)
	writeTestFile(t, workspace, "binary", []byte{0, 1}, 0o644)
	before, _ := store.Snapshot(context.Background(), SnapshotOptions{})
	_ = store.BeginMessageScope("s", "m", before.ID, time.Now())
	writeTestFile(t, workspace, "binary", []byte{0, 2}, 0o644)
	after, _ := store.Snapshot(context.Background(), SnapshotOptions{})
	_ = store.CloseMessageScope("s", "m", after.ID, []string{"binary"}, nil, time.Now())
	writeTestFile(t, workspace, "binary", []byte{0, 3}, 0o644)
	plan, err := store.PlanMessageRestore(context.Background(), "s", "m", SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Changes) != 1 || plan.Changes[0].ConflictReason == "" || !plan.Changes[0].ExactAllowed {
		t.Fatalf("plan=%+v", plan.Changes)
	}
	if _, err = store.ApplyRestore(context.Background(), plan.ID, []string{"binary"}); err == nil {
		t.Fatal("binary conflict restored without exact choice")
	}
	if _, err = store.ApplyRestoreChoices(context.Background(), plan.ID, []RestoreChoice{{Path: "binary", Exact: true}}); err != nil {
		t.Fatal(err)
	}
	if got := readTestFile(t, workspace, "binary"); !bytes.Equal(got, []byte{0, 1}) {
		t.Fatalf("exact binary=%v", got)
	}
}
