package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"crude/localhistory"
	ttypes "crude/types"
)

func historyServiceHarness(t *testing.T) (*LocalHistoryService, *localhistory.Store, string) {
	t.Helper()
	root := t.TempDir()
	store, err := localhistory.Open(t.TempDir(), root)
	if err != nil {
		t.Fatal(err)
	}
	service := NewLocalHistoryService(root, store, nopLogger{})
	t.Cleanup(func() { service.Stop(); _ = store.Close() })
	return service, store, root
}

func waitHistory(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timed out waiting for local history")
}

func TestHistoryServiceCapturesExternalAndReconciledChanges(t *testing.T) {
	service, store, root := historyServiceHarness(t)
	settings := ttypes.LocalHistorySettings{Enabled: "on", UseGitIgnore: "on", CaptureExternalChanges: "on", DebounceMS: 100, ReconcileIntervalSeconds: 5, MaxFileSizeMB: 10, RetentionMaxSnapshots: 50, StorageBudgetMB: 50}
	service.UpdateSettings(settings)
	waitHistory(t, func() bool { return service.Status().Ready })
	before, err := store.Timeline(100)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "external.txt"), []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	waitHistory(t, func() bool { records, _ := store.Timeline(100); return len(records) > len(before) })
	records, err := store.Timeline(100)
	if err != nil {
		t.Fatal(err)
	}
	if records[0].Kind != "external_change" {
		t.Fatalf("latest snapshot=%+v", records[0])
	}
}

func TestHistoryServiceMessageAttributionAndCollision(t *testing.T) {
	service, store, root := historyServiceHarness(t)
	service.settings = ttypes.LocalHistorySettings{Enabled: "on", UseGitIgnore: "on", MaxFileSizeMB: 10}
	ctx := context.Background()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("base"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := service.BeginMessage(ctx, "s", "m1"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	service.recordPath("a.txt")
	if err := service.EndMessage(ctx, "s", "m1"); err != nil {
		t.Fatal(err)
	}
	scope, ok, err := store.MessageScope("s", "m1")
	if err != nil || !ok || len(scope.ChangedPaths) != 1 || len(scope.CollisionPaths) != 0 {
		t.Fatalf("scope=%+v ok=%v err=%v", scope, ok, err)
	}

	if err := service.BeginMessage(ctx, "s", "m2"); err != nil {
		t.Fatal(err)
	}
	if err := service.BeginMessage(ctx, "other", "m3"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("ambiguous"), 0o644); err != nil {
		t.Fatal(err)
	}
	service.recordPath("a.txt")
	if err := service.EndMessage(ctx, "s", "m2"); err != nil {
		t.Fatal(err)
	}
	if err := service.EndMessage(ctx, "other", "m3"); err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]string{{"s", "m2"}, {"other", "m3"}} {
		scope, ok, err := store.MessageScope(pair[0], pair[1])
		if err != nil || !ok || len(scope.CollisionPaths) != 1 {
			t.Fatalf("collision scope=%+v ok=%v err=%v", scope, ok, err)
		}
	}
}
