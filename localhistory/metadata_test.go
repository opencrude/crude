package localhistory

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func recordAgentFileOperation(t *testing.T, store *Store, sessionID, messageID, agentID, source, path string, before, after []byte) {
	t.Helper()
	err := store.RecordAgentOperation(context.Background(), AgentOperation{
		ID: sessionID + "-" + messageID + "-" + agentID + "-" + sha256Hex(after), SessionID: sessionID, MessageID: messageID, AgentID: agentID, Source: source,
		Paths: []OperationPath{{Path: path, Before: &OperationState{Type: EntryFile, Mode: 0o644, Data: before}, After: &OperationState{Type: EntryFile, Mode: 0o644, Data: after}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteAgentOperation(sessionID+"-"+messageID+"-"+agentID+"-"+sha256Hex(after), true); err != nil {
		t.Fatal(err)
	}
}

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
	recordAgentFileOperation(t, store, "session", "msg-1", "root-agent", "write_file", "a.txt", []byte("before"), []byte("after"))
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

func TestRetentionBoundsClosedMessageSnapshots(t *testing.T) {
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
	writeTestFile(t, workspace, "a", []byte{'b'}, 0o644)
	recordAgentFileOperation(t, store, "s", "m", "root-agent", "write_file", "a", []byte{'a'}, []byte{'b'})
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
	if _, err := store.LoadManifest(snapshots[len(snapshots)-1].ID); err != nil {
		t.Fatalf("latest snapshot removed: %v", err)
	}
	if _, err := store.LoadManifest(snapshots[0].ID); err == nil {
		t.Fatal("closed message snapshot bypassed retention limit")
	}
	if scopes, err := store.AgentUndoableScopes("s", "root-agent", 10); err != nil || len(scopes) != 0 {
		t.Fatalf("pruned scope remained queryable: scopes=%+v err=%v", scopes, err)
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
	recordAgentFileOperation(t, store, "s", "m", "agent-a", "edit_file", "owned.txt", []byte("head\nbefore\ntail\n"), []byte("head\nmessage\ntail\n"))
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
	recordAgentFileOperation(t, store, "s", "m", "agent-a", "write_file", "binary", []byte{0, 1}, []byte{0, 2})
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

func TestAgentRestorePlanExcludesExternalAndOtherAgentPaths(t *testing.T) {
	store, workspace := newTestStore(t)
	for _, path := range []string{"alpha.txt", "beta.txt", "external.txt"} {
		writeTestFile(t, workspace, path, []byte("before"), 0o644)
	}
	before, err := store.Snapshot(context.Background(), SnapshotOptions{Kind: "before_message"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.BeginMessageScope("session", "msg", before.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, workspace, "alpha.txt", []byte("alpha changed"), 0o644)
	recordAgentFileOperation(t, store, "session", "msg", "agent-alpha", "write_file", "alpha.txt", []byte("before"), []byte("alpha changed"))
	writeTestFile(t, workspace, "beta.txt", []byte("beta changed"), 0o644)
	recordAgentFileOperation(t, store, "session", "msg", "agent-beta", "write_file", "beta.txt", []byte("before"), []byte("beta changed"))
	writeTestFile(t, workspace, "external.txt", []byte("external changed"), 0o644)
	after, err := store.Snapshot(context.Background(), SnapshotOptions{Kind: "after_message", ParentID: before.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CloseMessageScope("session", "msg", after.ID, []string{"alpha.txt", "beta.txt", "external.txt"}, nil, time.Now()); err != nil {
		t.Fatal(err)
	}

	alphaScopes, err := store.AgentUndoableScopes("session", "agent-alpha", 10)
	if err != nil || len(alphaScopes) != 1 || alphaScopes[0].MessageID != "msg" {
		t.Fatalf("alpha scopes=%+v err=%v", alphaScopes, err)
	}
	alphaPlan, err := store.PlanAgentRestore(context.Background(), "session", "msg", "agent-alpha", SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if alphaPlan.AgentID != "agent-alpha" || len(alphaPlan.Changes) != 1 || alphaPlan.Changes[0].Path != "alpha.txt" {
		t.Fatalf("alpha plan=%+v", alphaPlan)
	}
	messagePlan, err := store.PlanMessageRestore(context.Background(), "session", "msg", SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(messagePlan.Changes) != 2 || messagePlan.Changes[0].Path != "alpha.txt" || messagePlan.Changes[1].Path != "beta.txt" {
		t.Fatalf("message plan=%+v", messagePlan.Changes)
	}
	result, err := store.ApplyRestore(context.Background(), alphaPlan.ID, []string{"alpha.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if result.SessionID != "session" || result.MessageID != "msg" || result.AgentID != "agent-alpha" {
		t.Fatalf("restore owner=%+v", result)
	}
	if got := string(readTestFile(t, workspace, "alpha.txt")); got != "before" {
		t.Fatalf("alpha restore=%q", got)
	}
	if got := string(readTestFile(t, workspace, "beta.txt")); got != "beta changed" {
		t.Fatalf("agent restore changed beta=%q", got)
	}
	if got := string(readTestFile(t, workspace, "external.txt")); got != "external changed" {
		t.Fatalf("agent restore changed external=%q", got)
	}
}

func TestAgentOperationChangedAfterToolRequiresExplicitConflict(t *testing.T) {
	store, workspace := newTestStore(t)
	writeTestFile(t, workspace, "shared.txt", []byte("before"), 0o644)
	before, err := store.Snapshot(context.Background(), SnapshotOptions{Kind: "before_message"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.BeginMessageScope("session", "msg", before.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, workspace, "shared.txt", []byte("agent result"), 0o644)
	recordAgentFileOperation(t, store, "session", "msg", "agent", "write_file", "shared.txt", []byte("before"), []byte("agent result"))
	writeTestFile(t, workspace, "shared.txt", []byte("external result"), 0o644)
	after, err := store.Snapshot(context.Background(), SnapshotOptions{Kind: "after_message", ParentID: before.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CloseMessageScope("session", "msg", after.ID, []string{"shared.txt"}, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	scopes, err := store.AgentUndoableScopes("session", "agent", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(scopes) != 1 {
		t.Fatalf("agent scope missing guarded conflict: %+v", scopes)
	}
	plan, err := store.PlanAgentRestore(context.Background(), "session", "msg", "agent", SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Changes) != 1 || plan.Changes[0].ConflictReason == "" || !plan.Changes[0].ExactAllowed || plan.Changes[0].AutoMerged {
		t.Fatalf("post-tool overwrite was not guarded: %+v", plan.Changes)
	}
}

func TestLaterAgentWriteRequiresExplicitCollisionChoice(t *testing.T) {
	store, workspace := newTestStore(t)
	writeTestFile(t, workspace, "shared.txt", []byte("before"), 0o644)
	before, err := store.Snapshot(context.Background(), SnapshotOptions{Kind: "before_message"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.BeginMessageScope("session", "msg", before.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, workspace, "shared.txt", []byte("first agent"), 0o644)
	recordAgentFileOperation(t, store, "session", "msg", "agent-one", "write_file", "shared.txt", []byte("before"), []byte("first agent"))
	writeTestFile(t, workspace, "shared.txt", []byte("second agent"), 0o644)
	recordAgentFileOperation(t, store, "session", "msg", "agent-two", "write_file", "shared.txt", []byte("first agent"), []byte("second agent"))
	after, err := store.Snapshot(context.Background(), SnapshotOptions{Kind: "after_message", ParentID: before.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CloseMessageScope("session", "msg", after.ID, []string{"shared.txt"}, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	if scopes, err := store.AgentUndoableScopes("session", "agent-one", 10); err != nil || len(scopes) != 1 {
		t.Fatalf("superseded agent scope=%+v err=%v", scopes, err)
	}
	firstPlan, err := store.PlanAgentRestore(context.Background(), "session", "msg", "agent-one", SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(firstPlan.Changes) != 1 || firstPlan.Changes[0].ConflictReason == "" || firstPlan.Changes[0].ExactAllowed {
		t.Fatalf("earlier agent was allowed to overwrite later ownership: %+v", firstPlan.Changes)
	}
	plan, err := store.PlanAgentRestore(context.Background(), "session", "msg", "agent-two", SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Changes) != 1 || plan.Changes[0].ConflictReason != "" || plan.Changes[0].Target == nil {
		t.Fatalf("shared-write plan=%+v", plan.Changes)
	}
}

func TestAgentUndoRestoresOperationPreimageNotMessageBaseline(t *testing.T) {
	store, workspace := newTestStore(t)
	writeTestFile(t, workspace, "shared.txt", []byte("a=0\nb=0\n"), 0o644)
	before, err := store.Snapshot(context.Background(), SnapshotOptions{Kind: "before_message"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.BeginMessageScope("session", "msg", before.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, workspace, "shared.txt", []byte("a=0\nb=external\n"), 0o644)
	if err := store.RecordAgentOperation(context.Background(), AgentOperation{
		ID: "preserve-external", SessionID: "session", MessageID: "msg", AgentID: "agent", Source: "edit_file",
		Paths: []OperationPath{{Path: "shared.txt", Before: &OperationState{Type: EntryFile, Mode: 0o644, Data: []byte("a=0\nb=external\n")}, After: &OperationState{Type: EntryFile, Mode: 0o644, Data: []byte("a=agent\nb=external\n")}}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteAgentOperation("preserve-external", true); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, workspace, "shared.txt", []byte("a=agent\nb=external\n"), 0o644)
	after, err := store.Snapshot(context.Background(), SnapshotOptions{Kind: "after_message", ParentID: before.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CloseMessageScope("session", "msg", after.ID, []string{"shared.txt"}, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	plan, err := store.PlanAgentRestore(context.Background(), "session", "msg", "agent", SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Changes) != 1 || plan.Changes[0].ConflictReason != "" {
		t.Fatalf("agent plan=%+v", plan.Changes)
	}
	if _, err := store.ApplyRestore(context.Background(), plan.ID, []string{"shared.txt"}); err != nil {
		t.Fatal(err)
	}
	if got := string(readTestFile(t, workspace, "shared.txt")); got != "a=0\nb=external\n" {
		t.Fatalf("undo destroyed pre-agent external edit: %q", got)
	}
}

func TestAgentUndoMoveRestoresIgnoredSource(t *testing.T) {
	store, workspace := newTestStore(t)
	writeTestFile(t, workspace, ".gitignore", []byte("scratch.bin\n"), 0o644)
	writeTestFile(t, workspace, "scratch.bin", []byte{0, 1, 2, 3}, 0o600)
	before, err := store.Snapshot(context.Background(), SnapshotOptions{Kind: "before_message", UseGitIgnore: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.BeginMessageScope("session", "msg", before.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	data := []byte{0, 1, 2, 3}
	if err := store.RecordAgentOperation(context.Background(), AgentOperation{
		ID: "move-ignored", SessionID: "session", MessageID: "msg", AgentID: "agent", Source: "move_file",
		Paths: []OperationPath{
			{Path: "scratch.bin", Before: &OperationState{Type: EntryFile, Mode: 0o600, Data: data}},
			{Path: "result.bin", After: &OperationState{Type: EntryFile, Mode: 0o600, Data: data}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteAgentOperation("move-ignored", true); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(workspace, "scratch.bin"), filepath.Join(workspace, "result.bin")); err != nil {
		t.Fatal(err)
	}
	after, err := store.Snapshot(context.Background(), SnapshotOptions{Kind: "after_message", ParentID: before.ID, UseGitIgnore: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CloseMessageScope("session", "msg", after.ID, []string{"result.bin"}, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	plan, err := store.PlanAgentRestore(context.Background(), "session", "msg", "agent", SnapshotOptions{UseGitIgnore: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Changes) != 2 {
		t.Fatalf("move plan=%+v", plan.Changes)
	}
	if _, err := store.ApplyRestore(context.Background(), plan.ID, []string{"scratch.bin"}); err == nil || !strings.Contains(err.Error(), "atomic operation") {
		t.Fatalf("partial move restore error=%v", err)
	}
	if _, err := store.ApplyRestore(context.Background(), plan.ID, []string{"scratch.bin", "result.bin"}); err != nil {
		t.Fatal(err)
	}
	if got := readTestFile(t, workspace, "scratch.bin"); string(got) != string(data) {
		t.Fatalf("ignored source=%v", got)
	}
	if _, err := os.Lstat(filepath.Join(workspace, "result.bin")); !os.IsNotExist(err) {
		t.Fatalf("move destination remains: %v", err)
	}
}

func TestRepeatedUndoAdvancesToOlderOperationScope(t *testing.T) {
	store, workspace := newTestStore(t)
	writeTestFile(t, workspace, "value", []byte("base"), 0o644)
	parent, err := store.Snapshot(context.Background(), SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for index, transition := range []struct{ before, after string }{{"base", "one"}, {"one", "two"}} {
		messageID := fmt.Sprintf("msg-%d", index)
		if err := store.BeginMessageScope("session", messageID, parent.ID, time.Now()); err != nil {
			t.Fatal(err)
		}
		recordAgentFileOperation(t, store, "session", messageID, "agent", "write_file", "value", []byte(transition.before), []byte(transition.after))
		writeTestFile(t, workspace, "value", []byte(transition.after), 0o644)
		after, err := store.Snapshot(context.Background(), SnapshotOptions{ParentID: parent.ID})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.CloseMessageScope("session", messageID, after.ID, []string{"value"}, nil, time.Now()); err != nil {
			t.Fatal(err)
		}
		parent = after
	}
	scopes, err := store.AgentUndoableScopes("session", "agent", 10)
	if err != nil || len(scopes) != 2 || scopes[0].MessageID != "msg-1" {
		t.Fatalf("initial scopes=%+v err=%v", scopes, err)
	}
	plan, err := store.PlanAgentRestore(context.Background(), "session", "msg-1", "agent", SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ApplyRestore(context.Background(), plan.ID, []string{"value"}); err != nil {
		t.Fatal(err)
	}
	scopes, err = store.AgentUndoableScopes("session", "agent", 10)
	if err != nil || len(scopes) != 1 || scopes[0].MessageID != "msg-0" {
		t.Fatalf("scopes after latest undo=%+v err=%v", scopes, err)
	}
}

func TestAgentUndoRemovesOnlyEmptyParentsCreatedByOperation(t *testing.T) {
	store, workspace := newTestStore(t)
	before, err := store.Snapshot(context.Background(), SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.BeginMessageScope("session", "msg", before.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	data := []byte("created")
	if err := store.RecordAgentOperation(context.Background(), AgentOperation{
		ID: "nested-create", SessionID: "session", MessageID: "msg", AgentID: "agent", Source: "write_file",
		Paths: []OperationPath{
			{Path: "new", After: &OperationState{Type: EntryDirectory, Mode: 0o755}},
			{Path: "new/nested", After: &OperationState{Type: EntryDirectory, Mode: 0o755}},
			{Path: "new/nested/file.txt", After: &OperationState{Type: EntryFile, Mode: 0o644, Data: data}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteAgentOperation("nested-create", true); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, workspace, "new/nested/file.txt", data, 0o644)
	after, err := store.Snapshot(context.Background(), SnapshotOptions{ParentID: before.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CloseMessageScope("session", "msg", after.ID, []string{"new", "new/nested", "new/nested/file.txt"}, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	plan, err := store.PlanAgentRestore(context.Background(), "session", "msg", "agent", SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Changes) != 3 {
		t.Fatalf("nested create plan=%+v", plan.Changes)
	}
	if _, err := store.ApplyRestore(context.Background(), plan.ID, []string{"new", "new/nested", "new/nested/file.txt"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(workspace, "new")); !os.IsNotExist(err) {
		t.Fatalf("empty operation-created parents remain: %v", err)
	}
}

func TestAbortedPreparedOperationIsNeverUndoable(t *testing.T) {
	store, workspace := newTestStore(t)
	writeTestFile(t, workspace, "file", []byte("before"), 0o644)
	before, err := store.Snapshot(context.Background(), SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.BeginMessageScope("session", "message", before.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordAgentOperation(context.Background(), AgentOperation{
		ID: "aborted", SessionID: "session", MessageID: "message", AgentID: "agent", Source: "write_file",
		Paths: []OperationPath{{Path: "file", Before: &OperationState{Type: EntryFile, Mode: 0o644, Data: []byte("before")}, After: &OperationState{Type: EntryFile, Mode: 0o644, Data: []byte("never committed")}}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteAgentOperation("aborted", false); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, workspace, "file", []byte("other actor"), 0o644)
	after, err := store.Snapshot(context.Background(), SnapshotOptions{ParentID: before.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CloseMessageScope("session", "message", after.ID, []string{"file"}, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	if scopes, err := store.AgentUndoableScopes("session", "agent", 10); err != nil || len(scopes) != 0 {
		t.Fatalf("aborted operation became undoable: scopes=%+v err=%v", scopes, err)
	}
}

func TestMoveChainConflictBlocksEveryEndpoint(t *testing.T) {
	store, workspace := newTestStore(t)
	writeTestFile(t, workspace, "A", []byte("old"), 0o644)
	before, err := store.Snapshot(context.Background(), SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.BeginMessageScope("session", "message", before.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	old, external := []byte("old"), []byte("external")
	if err := store.RecordAgentOperation(context.Background(), AgentOperation{ID: "move-one", SessionID: "session", MessageID: "message", AgentID: "agent", Source: "move_file", Paths: []OperationPath{
		{Path: "A", Before: &OperationState{Type: EntryFile, Mode: 0o644, Data: old}},
		{Path: "B", After: &OperationState{Type: EntryFile, Mode: 0o644, Data: old}},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteAgentOperation("move-one", true); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(workspace, "A"), filepath.Join(workspace, "B")); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, workspace, "B", external, 0o644)
	if err := store.RecordAgentOperation(context.Background(), AgentOperation{ID: "move-two", SessionID: "session", MessageID: "message", AgentID: "agent", Source: "move_file", Paths: []OperationPath{
		{Path: "B", Before: &OperationState{Type: EntryFile, Mode: 0o644, Data: external}},
		{Path: "C", After: &OperationState{Type: EntryFile, Mode: 0o644, Data: external}},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteAgentOperation("move-two", true); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(workspace, "B"), filepath.Join(workspace, "C")); err != nil {
		t.Fatal(err)
	}
	after, err := store.Snapshot(context.Background(), SnapshotOptions{ParentID: before.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CloseMessageScope("session", "message", after.ID, []string{"A", "C"}, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	plan, err := store.PlanAgentRestore(context.Background(), "session", "message", "agent", SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Changes) != 3 {
		t.Fatalf("move chain plan=%+v", plan.Changes)
	}
	for _, change := range plan.Changes {
		if change.ConflictReason == "" || change.ExactAllowed {
			t.Fatalf("move endpoint was independently selectable: %+v", change)
		}
	}
	if _, err := store.ApplyRestoreChoices(context.Background(), plan.ID, nil); err == nil {
		t.Fatal("move chain conflict applied automatically")
	}
	if got := string(readTestFile(t, workspace, "C")); got != "external" {
		t.Fatalf("blocked move chain changed C: %q", got)
	}
}

func TestLaterAgentInAnotherScopeBlocksOlderExactUndo(t *testing.T) {
	store, workspace := newTestStore(t)
	writeTestFile(t, workspace, "shared", []byte("base"), 0o644)
	parent, err := store.Snapshot(context.Background(), SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range []struct{ message, agent, before, after string }{{"m1", "agent-a", "base", "a"}, {"m2", "agent-b", "a", "b"}} {
		if err := store.BeginMessageScope("session", step.message, parent.ID, time.Now()); err != nil {
			t.Fatal(err)
		}
		recordAgentFileOperation(t, store, "session", step.message, step.agent, "write_file", "shared", []byte(step.before), []byte(step.after))
		writeTestFile(t, workspace, "shared", []byte(step.after), 0o644)
		after, err := store.Snapshot(context.Background(), SnapshotOptions{ParentID: parent.ID})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.CloseMessageScope("session", step.message, after.ID, []string{"shared"}, nil, time.Now()); err != nil {
			t.Fatal(err)
		}
		parent = after
	}
	plan, err := store.PlanAgentRestore(context.Background(), "session", "m1", "agent-a", SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Changes) != 1 || plan.Changes[0].ConflictReason == "" || plan.Changes[0].ExactAllowed {
		t.Fatalf("later scope ownership was ignored: %+v", plan.Changes)
	}
	laterPlan, err := store.PlanAgentRestore(context.Background(), "session", "m2", "agent-b", SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ApplyRestore(context.Background(), laterPlan.ID, []string{"shared"}); err != nil {
		t.Fatal(err)
	}
	plan, err = store.PlanAgentRestore(context.Background(), "session", "m1", "agent-a", SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Changes) != 1 || plan.Changes[0].ConflictReason != "" {
		t.Fatalf("older ownership stayed blocked after later undo: %+v", plan.Changes)
	}
}

func TestInternalTimelineScanIsNotCappedAtTenThousand(t *testing.T) {
	store, _ := newTestStore(t)
	tx, err := store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	stmt, err := tx.Prepare(`INSERT INTO history_snapshots(snapshot_id,parent_snapshot_id,created_at,kind,label,logical_bytes) VALUES(?,?,?,?,?,0)`)
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 10001; index++ {
		if _, err := stmt.Exec(fmt.Sprintf("%064x", index+1), "", int64(index+1), "test", "", 0); err != nil {
			t.Fatal(err)
		}
	}
	if err := stmt.Close(); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	records, err := store.Timeline(-1)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 10001 {
		t.Fatalf("timeline records=%d", len(records))
	}
}
