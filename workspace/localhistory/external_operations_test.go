package localhistory

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestExternalOperationMatrixRestoresWriteDeleteMoveAndCopy(t *testing.T) {
	store, _ := newTestStore(t)
	external := t.TempDir()
	files := map[string]string{
		"overwrite": "old overwrite",
		"delete":    "old delete",
		"move-src":  "move content",
		"copy-src":  "copy source",
		"copy-dst":  "old copy destination",
	}
	for name, content := range files {
		writeTestFile(t, external, name, []byte(content), 0o644)
	}
	before, err := store.Snapshot(context.Background(), SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.BeginMessageScope("session", "message", before.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	record := func(id, source string, paths []OperationPath, mutate func() error) {
		t.Helper()
		if err := store.RecordAgentOperation(context.Background(), AgentOperation{ID: id, SessionID: "session", MessageID: "message", AgentID: "agent", Source: source, Paths: paths}); err != nil {
			t.Fatal(err)
		}
		if err := mutate(); err != nil {
			t.Fatal(err)
		}
		if err := store.CompleteAgentOperation(id, true); err != nil {
			t.Fatal(err)
		}
	}
	state := func(content string) *OperationState {
		return &OperationState{Type: EntryFile, Mode: 0o644, Data: []byte(content)}
	}
	overwrite := filepath.Join(external, "overwrite")
	record("external-overwrite", "write_file", []OperationPath{{Path: overwrite, Before: state("old overwrite"), After: state("new overwrite")}}, func() error {
		return os.WriteFile(overwrite, []byte("new overwrite"), 0o644)
	})
	deleted := filepath.Join(external, "delete")
	record("external-delete", "delete_file", []OperationPath{{Path: deleted, Before: state("old delete")}}, func() error {
		return os.Remove(deleted)
	})
	moveSource, moveDestination := filepath.Join(external, "move-src"), filepath.Join(external, "move-dst")
	record("external-move", "move_file", []OperationPath{
		{Path: moveSource, Before: state("move content")},
		{Path: moveDestination, After: state("move content")},
	}, func() error { return os.Rename(moveSource, moveDestination) })
	copyDestination := filepath.Join(external, "copy-dst")
	record("external-copy", "copy_file", []OperationPath{{Path: copyDestination, Before: state("old copy destination"), After: state("copy source")}}, func() error {
		return os.WriteFile(copyDestination, []byte("copy source"), 0o644)
	})
	after, err := store.Snapshot(context.Background(), SnapshotOptions{ParentID: before.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CloseMessageScope("session", "message", after.ID, nil, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	plan, err := store.PlanAgentRestore(context.Background(), "session", "message", "agent", SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Changes) != 5 {
		t.Fatalf("external operation plan=%+v", plan.Changes)
	}
	paths := make([]string, 0, len(plan.Changes))
	for _, change := range plan.Changes {
		if !filepath.IsAbs(filepath.FromSlash(change.Path)) || change.ConflictReason != "" || change.UnsafeReason != "" {
			t.Fatalf("external change=%+v", change)
		}
		paths = append(paths, change.Path)
	}
	if _, err := store.ApplyRestore(context.Background(), plan.ID, paths); err != nil {
		t.Fatal(err)
	}
	for name, want := range files {
		data, err := os.ReadFile(filepath.Join(external, name))
		if err != nil || string(data) != want {
			t.Fatalf("%s=%q err=%v", name, data, err)
		}
	}
	if _, err := os.Lstat(moveDestination); !os.IsNotExist(err) {
		t.Fatalf("move destination remains: %v", err)
	}
}
