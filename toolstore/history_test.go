package toolstore

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenRejectsRelativeRoot(t *testing.T) {
	if _, err := Open("tools"); err == nil {
		t.Fatal("Open accepted a relative root")
	}
	if _, err := Open(""); err == nil {
		t.Fatal("Open accepted an empty root")
	}
}

func TestOpenCreatesLayoutAndRemovesStaleStaging(t *testing.T) {
	store := newStore(t)
	stale := filepath.Join(store.root, stagingName, "install-123", "tool", "files")
	if err := os.MkdirAll(stale, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stale, "bin"), []byte("x"), 0o500); err != nil {
		t.Fatal(err)
	}
	if err := sealTree(filepath.Join(store.root, stagingName, "install-123")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store.root, usageTemp+"1.tmp"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(store.root); err != nil {
		t.Fatal(err)
	}
	requireEmptyStaging(t, store)
	if _, err := os.Stat(filepath.Join(store.root, usageTemp+"1.tmp")); !isNotExist(err) {
		t.Fatalf("stale usage file still exists: %v", err)
	}
}

func TestOpenFailsClosedOnDamagedUsage(t *testing.T) {
	store := newStore(t)
	if err := os.WriteFile(filepath.Join(store.root, usageName), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Open(store.root)
	requireErrorContains(t, err, "is damaged")
}

func TestHistoryPagesNewestFirst(t *testing.T) {
	store := newStore(t)
	page, total, err := store.History(0, 10)
	if err != nil || total != 0 || len(page) != 0 {
		t.Fatalf("empty history = %v, %d, %v", page, total, err)
	}
	for i := range 25 {
		store.mu.Lock()
		err := store.appendLocked(Event{Kind: EventUse, ToolID: fmt.Sprintf("t/%02d", i)})
		store.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
	}
	page, total, err = store.History(0, 10)
	if err != nil || total != 25 || len(page) != 10 {
		t.Fatalf("first page = %d events, total %d, %v", len(page), total, err)
	}
	if page[0].ToolID != "t/24" || page[9].ToolID != "t/15" {
		t.Fatalf("first page order = %s..%s", page[0].ToolID, page[9].ToolID)
	}
	page, _, err = store.History(20, 10)
	if err != nil || len(page) != 5 || page[4].ToolID != "t/00" {
		t.Fatalf("last page = %v, %v", page, err)
	}
	_, _, err = store.History(25, 10)
	requireErrorContains(t, err, "use an offset from 0 to 24")
	_, _, err = store.History(-1, 10)
	requireErrorContains(t, err, "out of range")
	_, _, err = store.History(0, 0)
	requireErrorContains(t, err, "use 1 to 1000")
	_, _, err = store.History(0, MaxHistoryPage+1)
	requireErrorContains(t, err, "use 1 to 1000")
}

func TestHistorySkipsTornFinalLineAndSealsIt(t *testing.T) {
	store := newStore(t)
	store.mu.Lock()
	if err := store.appendLocked(Event{Kind: EventInstall, ToolID: "a/1"}); err != nil {
		t.Fatal(err)
	}
	store.mu.Unlock()
	name := filepath.Join(store.root, historyName)
	handle, err := os.OpenFile(name, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := handle.WriteString(`{"time":"2026-11-02T10:00:00Z","kind":"rem`); err != nil {
		t.Fatal(err)
	}
	_ = handle.Close()

	page, total, err := store.History(0, 10)
	if err != nil || total != 1 || page[0].ToolID != "a/1" {
		t.Fatalf("history with a torn line = %v, %d, %v", page, total, err)
	}
	// A new append must start on its own line and stay readable.
	store.mu.Lock()
	if err := store.appendLocked(Event{Kind: EventRemove, ToolID: "a/1"}); err != nil {
		t.Fatal(err)
	}
	store.mu.Unlock()
	page, total, err = store.History(0, 10)
	if err != nil || total != 2 || page[0].Kind != EventRemove {
		t.Fatalf("history after a sealed torn line = %v, %d, %v", page, total, err)
	}
	// Reopen seals a torn tail too.
	handle, err = os.OpenFile(name, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = handle.WriteString(`{"kind":`)
	_ = handle.Close()
	if _, err := Open(store.root); err != nil {
		t.Fatal(err)
	}
	data := readFile(t, name)
	if data[len(data)-1] != '\n' {
		t.Fatal("Open did not seal the torn final line")
	}
	if _, total, _ := store.History(0, 10); total != 2 {
		t.Fatalf("total after reopen = %d, want 2", total)
	}
}
