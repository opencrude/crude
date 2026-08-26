package history

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func at(mins int) time.Time {
	return time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC).Add(time.Duration(mins) * time.Minute)
}

func TestNormalizeCollapsesCaseAndWhitespace(t *testing.T) {
	got := Normalize("  Fix   THE\tbug\nplease  ")
	if want := "fix the bug please"; got != want {
		t.Fatalf("Normalize = %q, want %q", got, want)
	}
	if Normalize("   ") != "" {
		t.Fatal("whitespace-only text should normalize to empty")
	}
}

func TestRecordFoldsExactRepeatsIntoMRUTail(t *testing.T) {
	s := New()
	if !s.Record("hello", at(0)) {
		t.Fatal("first hello should create an entry")
	}
	s.Record("fix the login bug", at(1))
	if s.Record("  HELLO   ", at(2)) {
		t.Fatal("exact repeat must merge, not append")
	}
	entries := s.Entries()
	if len(entries) != 2 {
		t.Fatalf("len = %d, want 2", len(entries))
	}
	last := entries[len(entries)-1]
	if last.Text != "HELLO" || last.Count != 2 {
		t.Fatalf("tail entry = %+v, want merged HELLO with count 2", last)
	}
	if entries[0].Text != "fix the login bug" {
		t.Fatalf("order broken: %+v", entries[0])
	}
	recall := s.Recall()
	if recall[len(recall)-1] != "HELLO" || recall[0] != "fix the login bug" {
		t.Fatalf("Recall order = %v", recall)
	}
}

func TestRecordCollapsesRetryWindowNearDuplicates(t *testing.T) {
	s := New()
	base := "please check why some of the history messages dont get persisted properly"
	if !s.Record(base, at(0)) {
		t.Fatal("first prompt should create entry")
	}
	retry := "Please check why some of the history messages dont get persisted properlly! "
	if s.Record(retry, at(1)) { // typo-fixed resubmit one minute later
		t.Fatal("near-identical retry within window must merge")
	}
	entries := s.Entries()
	if len(entries) != 1 || entries[0].Count != 2 || entries[0].Text != strings.TrimSpace(retry) {
		t.Fatalf("entries = %+v", entries)
	}
}

func TestRecordKeepsDistinctPromptsOutsideWindow(t *testing.T) {
	s := New()
	a := "refactor the session deck to use passive panels for background jobs"
	b := "refactor the settings deck to use inline forms for provider keys"
	s.Record(a, at(0))
	if !s.Record(b, at(30)) {
		t.Fatal("similar but distinct prompt after the window must be a new entry")
	}
	if s.Len() != 2 {
		t.Fatalf("len = %d, want 2", s.Len())
	}
}

func TestRecordShortTextsNeedExactMatch(t *testing.T) {
	s := New()
	s.Record("hi", at(0))
	if !s.Record("no", at(0)) {
		t.Fatal("short distinct texts must not fuzzy-match")
	}
	s.Record("hi", at(10)) // exact repeat far later still folds
	if s.Len() != 2 {
		t.Fatalf("len = %d, want 2 (folded hi, kept no)", s.Len())
	}
}

func TestRecordSkipsEmptyAndOversized(t *testing.T) {
	s := New()
	s.Record("   \n\t  ", at(0))
	s.Record(strings.Repeat("x", maxRecordBytes+1), at(0))
	if s.Len() != 0 {
		t.Fatalf("len = %d, want 0", s.Len())
	}
}

func TestEvictionDropsLowestScoreFirst(t *testing.T) {
	s := New()
	s.max = 3
	old := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	s.Record("stale one-off prompt about nothing", old)     // count 1, very old
	s.Record("filler prompt alpha about routing", at(-200)) // count 1, aging
	s.Record("filler prompt beta about themes", at(-150))   // count 1, less old
	for range 3 {
		s.Record("workhorse prompt used every single day", at(-10)) // folds to count 3
	}
	// Over cap now: evicts stale (oldest, lowest score), then alpha.
	s.Record("recent one-off prompt about widgets", at(-5))
	if s.Len() > 3 {
		t.Fatalf("cap not enforced: len = %d", s.Len())
	}
	for _, e := range s.Entries() {
		if e.Text == "stale one-off prompt about nothing" || e.Text == "filler prompt alpha about routing" {
			t.Fatalf("low-score entry survived eviction: %+v", e)
		}
	}
	found := false
	for _, e := range s.Entries() {
		if e.Text == "workhorse prompt used every single day" && e.Count == 3 {
			found = true
		}
	}
	if !found {
		t.Fatal("high-count workhorse entry was evicted")
	}
}

func TestSaveLoadRoundTripV1(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	s := New()
	s.Record("hello", at(0))
	s.Record("build me a fibonacci script in python here", at(1))
	s.Record("hello", at(2))
	if err := s.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatal("temp file should be renamed away")
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if fi.Mode().Perm() != 0600 {
		t.Fatalf("perms = %v, want 0600", fi.Mode().Perm())
	}
	loaded, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	want := []string{"build me a fibonacci script in python here", "hello"}
	got := loaded.Recall()
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("Recall = %v, want %v", got, want)
	}
	if loaded.Entries()[1].Count != 2 {
		t.Fatalf("count lost on round trip: %+v", loaded.Entries()[1])
	}
}

func TestLoadFileMigratesLegacyArray(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	legacy := []string{"hello", "", "make a fibonacci python script here", "hello"}
	data, _ := json.Marshal(legacy)
	stamp := time.Date(2026, 7, 5, 13, 21, 0, 0, time.UTC)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	s, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if s.Len() != 3 {
		t.Fatalf("len = %d, want 3 (blank dropped)", s.Len())
	}
	entries := s.Entries()
	for _, e := range entries {
		if e.Count != 1 {
			t.Fatalf("migrated count = %d, want 1: %+v", e.Count, e)
		}
		if e.LastSeen > stamp.Unix() {
			t.Fatalf("migrated entry stamped after mtime: %+v", e)
		}
	}
	if entries[2].Text != "hello" {
		t.Fatalf("legacy order not preserved: %+v", entries)
	}
}

func TestLoadFileMissingAndCorrupt(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope.json")
	s, err := LoadFile(missing)
	if err != nil || s.Len() != 0 {
		t.Fatalf("missing file: store=%+v err=%v", s, err)
	}
	corrupt := filepath.Join(t.TempDir(), "bad.json")
	os.WriteFile(corrupt, []byte("{not json"), 0600)
	if _, err := LoadFile(corrupt); err == nil {
		t.Fatal("corrupt file should error")
	}
}
