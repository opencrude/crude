package localhistory

import (
	"fmt"
	"testing"
	"time"
)

func BenchmarkOpenIndexedHistory(b *testing.B) {
	root, project := b.TempDir(), b.TempDir()
	s, err := Open(root, project)
	if err != nil {
		b.Fatal(err)
	}
	entries := make(map[string]Entry, 1400)
	for i := range 1400 {
		entries[fmt.Sprintf("src/file-%d", i)] = Entry{Type: EntryDirectory, Mode: 0755}
	}
	for i := range 500 {
		m := Manifest{Version: FormatVersion, CreatedAt: int64(i), Kind: "test", Entries: entries}
		if err := s.saveManifest(&m); err != nil {
			b.Fatal(err)
		}
		if err := s.recordSnapshot(m); err != nil {
			b.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		s, err := Open(root, project)
		if err != nil {
			b.Fatal(err)
		}
		if err := s.Close(); err != nil {
			b.Fatal(err)
		}
	}
}

func TestIndexedHistoryOpenDoesNotRewriteEntries(t *testing.T) {
	root, project := t.TempDir(), t.TempDir()
	s, err := Open(root, project)
	if err != nil {
		t.Fatal(err)
	}
	m := Manifest{Version: FormatVersion, CreatedAt: time.Now().UnixNano(), Kind: "test", Entries: map[string]Entry{"src": {Type: EntryDirectory, Mode: 0755}}}
	if err := s.saveManifest(&m); err != nil {
		t.Fatal(err)
	}
	if err := s.recordSnapshot(m); err != nil {
		t.Fatal(err)
	}
	// A committed snapshot row and its entries are one transaction. Reopening
	// must not replay those immutable entries, even with INSERT OR IGNORE.
	if _, err := s.db.Exec(`CREATE TRIGGER reject_entry_replay BEFORE INSERT ON history_snapshot_entries BEGIN SELECT RAISE(ABORT, 'replayed indexed history'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(root, project)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if _, err := s.LoadManifest(m.ID); err != nil {
		t.Fatalf("immutable manifest no longer readable: %v", err)
	}
}
