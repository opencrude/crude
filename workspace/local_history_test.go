package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"crude/localhistory"
)

func TestWorkspaceLocalHistoryCapturePreviewApply(t *testing.T) {
	project := t.TempDir()
	store, err := localhistory.Open(t.TempDir(), project)
	if err != nil {
		t.Fatal(err)
	}
	bus := NewEventBus()
	ws := &Workspace{RootPath: project, LocalHistory: store, eventBus: bus}
	path := filepath.Join(project, "a.txt")
	if err := os.WriteFile(path, []byte("before"), 0o644); err != nil {
		t.Fatal(err)
	}
	snapshot, err := ws.CaptureHistory(context.Background(), localhistory.SnapshotOptions{Kind: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("after"), 0o644); err != nil {
		t.Fatal(err)
	}
	plan, err := ws.PreviewHistoryRestore(context.Background(), snapshot.ID, localhistory.SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ws.ApplyHistoryRestore(context.Background(), plan.ID, nil); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "before" {
		t.Fatalf("restored content=%q", data)
	}
	events := bus.RecentEvents(1)
	if len(events) != 1 || !strings.Contains(events[0], "HistoryRestored") {
		t.Fatalf("restore event log=%v", events)
	}
}
