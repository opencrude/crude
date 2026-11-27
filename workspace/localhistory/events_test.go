package localhistory

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// The History panel lists what each point changed and who changed it. Before
// this index the timeline only named the capture mechanism ("Before agent
// changes"), and a user had to prepare a restore to see any file.
func TestEventsListWhatEachPointChangedAndWho(t *testing.T) {
	store, ws := newTestStore(t)
	ctx := context.Background()
	writeTestFile(t, ws, "a.txt", []byte("one\n"), 0o644)
	writeTestFile(t, ws, "b.txt", []byte("keep\n"), 0o644)
	baseline, err := store.Snapshot(ctx, SnapshotOptions{Kind: "baseline"})
	if err != nil {
		t.Fatal(err)
	}
	before, err := store.Snapshot(ctx, SnapshotOptions{Kind: "before_message"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.BeginMessageScope("s", "m", before.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, ws, "a.txt", []byte("two\n"), 0o644)
	writeTestFile(t, ws, "dir/c.txt", []byte("new\n"), 0o644)
	recordAgentFileOperation(t, store, "s", "m", "agent-a", "edit_file", "a.txt", []byte("one\n"), []byte("two\n"))
	after, err := store.Snapshot(ctx, SnapshotOptions{Kind: "after_message"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CloseMessageScope("s", "m", after.ID, []string{"a.txt"}, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	// A safety copy before a delete observes only its own path; it must not
	// become the base that later full snapshots are compared with.
	if err := store.KeepBeforeDelete(ctx, filepath.Join(ws, "b.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(ws, "b.txt")); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, ws, "image.bin", []byte{0x89, 'P', 'N', 'G', 0}, 0o644)
	outside, err := store.Snapshot(ctx, SnapshotOptions{Kind: "external_change"})
	if err != nil {
		t.Fatal(err)
	}

	events, err := store.Events(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]Event{}
	for _, event := range events {
		byID[event.ID] = event
	}
	if len(events) != 5 || events[0].ID != outside.ID || events[len(events)-1].ID != baseline.ID {
		t.Fatalf("events are not newest first: %+v", events)
	}
	if got := byID[baseline.ID]; got.BaseID != "" || len(got.Paths) != 0 {
		t.Fatalf("the first point has nothing to compare with: %+v", got)
	}
	if got := byID[after.ID]; !reflect.DeepEqual(got.Paths, []string{"a.txt", "dir", "dir/c.txt"}) ||
		got.BaseID != before.ID || got.SessionID != "s" || got.MessageID != "m" || !reflect.DeepEqual(got.AgentIDs, []string{"agent-a"}) {
		t.Fatalf("agent turn = %+v", got)
	}
	if got := byID[outside.ID]; !reflect.DeepEqual(got.Paths, []string{"b.txt", "image.bin"}) || got.BaseID != after.ID {
		t.Fatalf("a later full point compared with the safety copy, not the last full snapshot: %+v", got)
	}
	file, err := store.Events(ctx, "dir")
	if err != nil || len(file) != 1 || file[0].ID != after.ID {
		t.Fatalf("folder history = %+v %v", file, err)
	}

	page, err := store.EventChanges(ctx, EventQuery{SnapshotID: after.ID})
	if err != nil || len(page.Files) != 3 || page.After != "" {
		t.Fatalf("agent turn files = %+v %v", page, err)
	}
	a, folder, created := page.Files[0], page.Files[1], page.Files[2]
	if string(a.Before) != "one\n" || string(a.After) != "two\n" || !a.BeforePresent || !a.AfterPresent || a.Binary {
		t.Fatalf("edited file = %+v", a)
	}
	if folder.Note != "Folder" || folder.BeforePresent || !folder.AfterPresent {
		t.Fatalf("new folder = %+v", folder)
	}
	if created.BeforePresent || string(created.After) != "new\n" {
		t.Fatalf("new file = %+v", created)
	}
	page, err = store.EventChanges(ctx, EventQuery{SnapshotID: outside.ID})
	if err != nil || len(page.Files) != 2 {
		t.Fatalf("outside files = %+v %v", page, err)
	}
	if deleted := page.Files[0]; deleted.AfterPresent || string(deleted.Before) != "keep\n" {
		t.Fatalf("deleted file = %+v", deleted)
	}
	if binary := page.Files[1]; !binary.Binary || binary.BeforePresent {
		t.Fatalf("binary file = %+v", binary)
	}
	if _, err := store.EventChanges(ctx, EventQuery{SnapshotID: "missing"}); err == nil {
		t.Fatal("an unknown point was read")
	}
}

// A point that changed many files is read a page at a time; every file stays
// reachable and no page repeats one.
func TestEventChangesPagesEveryFile(t *testing.T) {
	store, ws := newTestStore(t)
	ctx := context.Background()
	if _, err := store.Snapshot(ctx, SnapshotOptions{Kind: "baseline"}); err != nil {
		t.Fatal(err)
	}
	for i := range eventPageFiles + 5 {
		writeTestFile(t, ws, fmt.Sprintf("f%03d.txt", i), []byte("x\n"), 0o644)
	}
	point, err := store.Snapshot(ctx, SnapshotOptions{Kind: "external_change"})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	query := EventQuery{SnapshotID: point.ID}
	for pages := 0; ; pages++ {
		page, err := store.EventChanges(ctx, query)
		if err != nil || pages > 2 {
			t.Fatalf("paging did not end: %v", err)
		}
		for _, file := range page.Files {
			if seen[file.Path] {
				t.Fatalf("page repeated %s", file.Path)
			}
			seen[file.Path] = true
		}
		if page.After == "" {
			break
		}
		query.After = page.After
	}
	if len(seen) != eventPageFiles+5 {
		t.Fatalf("read %d of %d files", len(seen), eventPageFiles+5)
	}
}

// The Agent changes preview needs both recorded versions of one receipt.
func TestAgentReceiptCarriesBothRecordedVersions(t *testing.T) {
	store, _ := newTestStore(t)
	before, err := store.Snapshot(context.Background(), SnapshotOptions{Kind: "before_message"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.BeginMessageScope("s", "m", before.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	recordAgentFileOperation(t, store, "s", "m", "agent", "edit_file", "notes.txt", []byte("old\n"), []byte("new\n"))
	page, err := store.AgentReceipts(context.Background(), AgentReceiptQuery{SessionID: "s", AgentID: "agent"})
	if err != nil || len(page.Changes) != 1 || page.Changes[0].File != nil {
		t.Fatalf("list = %+v %v", page, err)
	}
	change := page.Changes[0]
	page, err = store.AgentReceipts(context.Background(), AgentReceiptQuery{SessionID: "s", AgentID: "agent", OperationID: change.OperationID, Path: change.Path})
	if err != nil || len(page.Changes) != 1 || page.Changes[0].File == nil {
		t.Fatalf("detail = %+v %v", page, err)
	}
	if file := page.Changes[0].File; string(file.Before) != "old\n" || string(file.After) != "new\n" || !file.BeforePresent || !file.AfterPresent {
		t.Fatalf("versions = %+v", file)
	}
}
