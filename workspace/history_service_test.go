package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"crude/fileio"
	"crude/localhistory"
	"crude/session"
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

func TestDisabledHistoryIsDistinctFromCaptureFailure(t *testing.T) {
	service, _, _ := historyServiceHarness(t)
	service.settings = ttypes.LocalHistorySettings{Enabled: "off"}
	if err := service.BeginMessage(context.Background(), "session", "message"); !errors.Is(err, session.ErrHistoryDisabled) {
		t.Fatalf("disabled history error=%v", err)
	}
}

func TestRestoreAdmissionWaitsForWakeContinuationLease(t *testing.T) {
	service, store, root := historyServiceHarness(t)
	if err := os.WriteFile(filepath.Join(root, "file"), []byte("target"), 0o644); err != nil {
		t.Fatal(err)
	}
	target, err := store.Snapshot(context.Background(), localhistory.SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "file"), []byte("current"), 0o644); err != nil {
		t.Fatal(err)
	}
	plan, err := store.PlanRestore(context.Background(), target.ID, localhistory.SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.BeginContinuation(context.Background(), "session", "message"); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := service.ApplyRestore(context.Background(), plan.ID, []string{"file"})
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("restore crossed active continuation lease: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	if err := service.EndContinuation(context.Background(), "session", "message"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("restore did not proceed after continuation ended")
	}
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

func prepareHistoryWrite(t *testing.T, service *LocalHistoryService, sessionID, messageID, agentID, path, before, after string) {
	t.Helper()
	beforeState := fileio.MutationState{Kind: fileio.MutationRegular, Bytes: []byte(before), Mode: 0o644}
	if before == "" {
		beforeState = fileio.MutationState{Kind: fileio.MutationAbsent}
	}
	err := service.RecordAgentMutation(context.Background(), session.HistoryMutation{
		SessionID: sessionID, MessageID: messageID, AgentID: agentID,
		Receipt: fileio.MutationReceipt{OperationID: sessionID + "-" + messageID + "-" + agentID + "-" + filepath.Base(path), OperationName: "write_file", Paths: []fileio.MutationPathState{{
			Path: path, Before: beforeState, After: fileio.MutationState{Kind: fileio.MutationRegular, Bytes: []byte(after), Mode: 0o644},
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	committed := true
	if err := service.RecordAgentMutation(context.Background(), session.HistoryMutation{
		SessionID: sessionID, MessageID: messageID, AgentID: agentID,
		Receipt: fileio.MutationReceipt{OperationID: sessionID + "-" + messageID + "-" + agentID + "-" + filepath.Base(path), OperationName: "write_file"}, Committed: &committed,
	}); err != nil {
		t.Fatal(err)
	}
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
	prepareHistoryWrite(t, service, "s", "m1", "agent-1", filepath.Join(root, "a.txt"), "base", "changed")
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := service.EndMessage(ctx, "s", "m1"); err != nil {
		t.Fatal(err)
	}
	scope, ok, err := store.MessageScope("s", "m1")
	if err != nil || !ok || len(scope.ChangedPaths) != 1 || len(scope.CollisionPaths) != 0 {
		t.Fatalf("scope=%+v ok=%v err=%v", scope, ok, err)
	}
	if err := service.BeginMessage(ctx, "s", "m1"); !errors.Is(err, session.ErrHistoryRecorded) {
		t.Fatalf("Continue attempted a duplicate immutable history scope: %v", err)
	}
	if retained, _, err := store.MessageScope("s", "m1"); err != nil || retained.BeforeSnapshot != scope.BeforeSnapshot || retained.AfterSnapshot != scope.AfterSnapshot {
		t.Fatal("Continue replaced the original message safety snapshots")
	}

	if err := service.BeginMessage(ctx, "s", "m2"); err != nil {
		t.Fatal(err)
	}
	if err := service.BeginMessage(ctx, "other", "m3"); err != nil {
		t.Fatal(err)
	}
	prepareHistoryWrite(t, service, "s", "m2", "agent-2", filepath.Join(root, "a.txt"), "changed", "ambiguous")
	prepareHistoryWrite(t, service, "other", "m3", "agent-3", filepath.Join(root, "a.txt"), "changed", "ambiguous")
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("ambiguous"), 0o644); err != nil {
		t.Fatal(err)
	}
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

func TestHistoryServiceUndoUsesAgentProvenanceNotWatcherScope(t *testing.T) {
	service, _, root := historyServiceHarness(t)
	service.settings = ttypes.LocalHistorySettings{Enabled: "on", UseGitIgnore: "on", MaxFileSizeMB: 10}
	ctx := context.Background()
	for _, path := range []string{"agent.txt", "external.txt"} {
		if err := os.WriteFile(filepath.Join(root, path), []byte("before"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := service.BeginMessage(ctx, "session", "message"); err != nil {
		t.Fatal(err)
	}
	prepareHistoryWrite(t, service, "session", "message", "agent", filepath.Join(root, "agent.txt"), "before", "agent changed")
	if err := os.WriteFile(filepath.Join(root, "agent.txt"), []byte("agent changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "external.txt"), []byte("external changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := service.EndMessage(ctx, "session", "message"); err != nil {
		t.Fatal(err)
	}
	plan, err := service.PlanAgentRestore(ctx, "session", "message", "agent")
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Changes) != 1 || plan.Changes[0].Path != "agent.txt" {
		t.Fatalf("agent undo plan includes unowned paths: %+v", plan.Changes)
	}
}
