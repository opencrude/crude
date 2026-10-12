package session

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"crude/agent"
	"crude/collaboration"
	"crude/fileio"
	"crude/safety"
	"crude/tools"
)

func TestSuccessfulFileToolReportsScopedHistoryMutation(t *testing.T) {
	project := t.TempDir()
	sess, err := NewWithSessionDir("history-mutation", testConfig(), t.TempDir(), filepath.Join(t.TempDir(), "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	sess.SetWorkDir(project)
	var got HistoryMutation
	committed := false
	sess.SetHistoryMutationHook(func(_ context.Context, mutation HistoryMutation) error {
		if mutation.Committed == nil {
			got = mutation
		} else {
			committed = *mutation.Committed
		}
		return nil
	})
	ctx := withHistoryScope(context.Background(), "session", "msg-1", "root-agent")
	if _, err := sess.ExecuteToolForTest(ctx, "write_file", map[string]any{"path": "tracked.txt", "content": "tracked"}); err != nil {
		t.Fatal(err)
	}
	if got.SessionID != "session" || got.MessageID != "msg-1" || got.AgentID != "root-agent" || got.Receipt.OperationName != "write_file" || len(got.Receipt.Paths) != 1 || got.Receipt.Paths[0].Path != filepath.Join(project, "tracked.txt") || got.Receipt.Paths[0].Before.Kind != fileio.MutationAbsent || string(got.Receipt.Paths[0].After.Bytes) != "tracked" {
		t.Fatalf("history mutation=%+v", got)
	}
	if !committed {
		t.Fatal("successful root mutation was not committed")
	}
}

func TestSubAgentToolPreservesMessageScopeAndReplacesActor(t *testing.T) {
	project := t.TempDir()
	sess, err := NewWithSessionDir("history-child", testConfig(), t.TempDir(), filepath.Join(t.TempDir(), "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	sess.SetWorkDir(project)
	store := attachDelegationTestCollaboration(t, sess)
	if _, err := sess.registerCollaborator(t.Context(), "root-agent", "root", sess.sessionID, "", CategoryDefault, sess.db); err != nil {
		t.Fatal(err)
	}
	registered, err := sess.registerCollaborator(t.Context(), "child-agent", "child", "child-session", "root-agent", CategoryCrafter, sess.db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.StartRun(t.Context(), collaboration.StartRunRequest{AgentID: registered.ID, RuntimeEpoch: registered.RuntimeEpoch, RequestID: "file-assignment", Origin: collaboration.OriginPeer}); err != nil {
		t.Fatal(err)
	}
	var got HistoryMutation
	committed := false
	sess.SetHistoryMutationHook(func(_ context.Context, mutation HistoryMutation) error {
		if mutation.Committed == nil {
			got = mutation
		} else {
			committed = *mutation.Committed
		}
		return nil
	})
	runner := NewSubAgentRunner(sess, 2)
	runner.SetParentToolFn(sess.newToolFn(true))
	policy := safety.DefaultPolicy()
	policy.AllowedCapabilities[safety.CanUpdate] = true
	spec := agent.SubAgentSpec{ID: "child-agent", Name: "child", PolicyName: "workspace-write", Policy: policy}
	toolFn := runner.buildProxiedToolFn(spec, nil, nil)
	ctx := withHistoryScope(context.Background(), "session", "root-message", "root-agent")
	ctx = context.WithValue(ctx, tools.ExecutionIdentityKey{}, tools.ExecutionIdentity{AgentID: "child-agent", ToolCallID: "file-effect"})
	if _, err := toolFn(ctx, "write_file", map[string]any{"path": "child.txt", "content": "child data"}); err != nil {
		t.Fatal(err)
	}
	if got.SessionID != "session" || got.MessageID != "root-message" || got.AgentID != "child-agent" || got.Receipt.OperationName != "write_file" {
		t.Fatalf("child provenance=%+v", got)
	}
	if len(got.Receipt.Paths) != 1 || got.Receipt.Paths[0].Path != filepath.Join(project, "child.txt") {
		t.Fatalf("child paths=%+v", got.Receipt.Paths)
	}
	if !committed {
		t.Fatal("successful child mutation was not committed")
	}
}

func TestSessionFileMutationFailsClosedWithoutEditor(t *testing.T) {
	project := t.TempDir()
	sess, err := NewWithSessionDir("history-no-editor", testConfig(), t.TempDir(), filepath.Join(t.TempDir(), "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	sess.SetWorkDir(project)
	sess.editor = nil
	result, err := sess.ExecuteToolForTest(context.Background(), "write_file", map[string]any{"path": "unsafe.txt", "content": "data"})
	if err == nil || !strings.Contains(result.Content, "mutation receipt requires an editor") {
		t.Fatalf("result=%q err=%v", result.Content, err)
	}
	if _, err := os.Lstat(filepath.Join(project, "unsafe.txt")); !os.IsNotExist(err) {
		t.Fatalf("degraded dispatcher mutated workspace: %v", err)
	}
}

func TestHistoryWALFailureAbortsAgentFileMutation(t *testing.T) {
	project := t.TempDir()
	sess, err := NewWithSessionDir("history-wal-failure", testConfig(), t.TempDir(), filepath.Join(t.TempDir(), "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	sess.SetWorkDir(project)
	wantErr := errors.New("history database unavailable")
	sess.SetHistoryMutationHook(func(context.Context, HistoryMutation) error { return wantErr })
	ctx := withHistoryScope(context.Background(), "session", "message", "agent")
	result, err := sess.ExecuteToolForTest(ctx, "write_file", map[string]any{"path": "blocked.txt", "content": "must not land"})
	if err == nil || !strings.Contains(err.Error(), wantErr.Error()) || !strings.Contains(result.Content, wantErr.Error()) {
		t.Fatalf("result=%q err=%v", result.Content, err)
	}
	if _, err := os.Lstat(filepath.Join(project, "blocked.txt")); !os.IsNotExist(err) {
		t.Fatalf("file mutation escaped failed WAL: %v", err)
	}
}

func TestFileMutationWorksWhenHistoryIsNotConfigured(t *testing.T) {
	project := t.TempDir()
	sess, err := NewWithSessionDir("history-not-configured", testConfig(), t.TempDir(), filepath.Join(t.TempDir(), "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	sess.SetWorkDir(project)
	if _, err := sess.ExecuteToolForTest(context.Background(), "write_file", map[string]any{"path": "plain.txt", "content": "plain"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(project, "plain.txt"))
	if err != nil || string(data) != "plain" {
		t.Fatalf("file=%q err=%v", data, err)
	}
}

func TestRestoreActivityGateIncludesChildAndHistoryFinalizer(t *testing.T) {
	sess, err := NewWithSessionDir("history-liveness", testConfig(), t.TempDir(), filepath.Join(t.TempDir(), "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	runner := NewSubAgentRunner(sess, 1)
	sess.subAgentSup = runner
	runner.beginTurnChild(7)
	if !sess.HasActiveTurns() || !sess.IsTurnActive() {
		t.Fatal("live child did not block restore")
	}
	runner.finishTurnChild(7)
	sess.historyFinalizers.Add(1)
	if !sess.HasActiveTurns() || !sess.IsTurnActive() {
		t.Fatal("history finalizer did not block restore")
	}
	sess.historyFinalizers.Add(-1)
	if sess.HasActiveTurns() {
		t.Fatal("restore gate stayed active after child and finalizer completed")
	}
}

func TestAgentMutationsMayTargetOutsideWorkspace(t *testing.T) {
	project, outside := t.TempDir(), t.TempDir()
	sess, err := NewWithSessionDir("history-confine", testConfig(), t.TempDir(), filepath.Join(t.TempDir(), "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	sess.SetWorkDir(project)
	outsidePath := filepath.Join(outside, "escaped.txt")
	if result, err := sess.ExecuteToolForTest(context.Background(), "write_file", map[string]any{"path": outsidePath, "content": "outside"}); err != nil {
		t.Fatalf("outside write result=%q err=%v", result.Content, err)
	}
	if data, err := os.ReadFile(outsidePath); err != nil || string(data) != "outside" {
		t.Fatalf("outside file=%q err=%v", data, err)
	}
}
