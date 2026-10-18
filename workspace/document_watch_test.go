package workspace

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"crude/document"
)

func TestDocumentWatchCleanAtomicReplacementDirtyConflictAndJoin(t *testing.T) {
	s, snap, a, _ := documentFixture(t, "original")
	tmp := filepath.Join(filepath.Dir(snap.Path), "replacement")
	if err := os.WriteFile(tmp, []byte("external"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, snap.Path); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		current, err := s.Snapshot(snap.ID)
		if err != nil {
			t.Fatal(err)
		}
		if string(current.Text) == "external" {
			if current.Dirty || current.Revision != 2 {
				t.Fatal(current)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("watcher did not reconcile atomic replacement")
		}
		time.Sleep(time.Millisecond)
	}
	documentEdit(t, s, a, snap.ID, "local", 2, 0, 8, "unsaved")
	if err := os.WriteFile(snap.Path, []byte("peer disk"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.ReconcileDisk(context.Background(), snap.ID); err != nil {
		t.Fatal(err)
	}
	current := documentText(t, s, snap.ID, "unsaved")
	if !current.Conflict || !current.Dirty || string(current.Saved) != "external" {
		t.Fatal(current)
	}
	s.Close()
	select {
	case <-s.watch.done:
	default:
		t.Fatal("close did not join watcher")
	}
}

func TestDocumentWatchRemovedDirectoryReleasesAndReopens(t *testing.T) {
	for _, rename := range []bool{false, true} {
		t.Run(map[bool]string{false: "remove", true: "rename"}[rename], func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, "folder")
			path := filepath.Join(dir, "text.txt")
			if err := os.Mkdir(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("original"), 0o600); err != nil {
				t.Fatal(err)
			}
			s := NewDocumentService(root, nil, nil)
			t.Cleanup(s.Close)
			owner := document.Owner{ClientID: "author", Epoch: 1}
			snap, err := s.Open(owner, path)
			if err != nil {
				t.Fatal(err)
			}
			if rename {
				if err := os.Rename(dir, filepath.Join(root, "moved")); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(dir); err != nil {
					t.Fatal(err)
				}
			}
			deadline := time.Now().Add(3 * time.Second)
			for {
				current := documentText(t, s, snap.ID, "original")
				s.watch.mu.Lock()
				watching := slices.Contains(s.watch.watcher.WatchList(), dir)
				s.watch.mu.Unlock()
				if !watching && current.Conflict {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("removed directory retained a stale watch or healthy descendant: watching=%v conflict=%v", watching, current.Conflict)
				}
				time.Sleep(time.Millisecond)
			}
			if err := os.Mkdir(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("original"), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Open(owner, path); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("fresh watcher content"), 0o600); err != nil {
				t.Fatal(err)
			}
			deadline = time.Now().Add(3 * time.Second)
			for {
				current, err := s.Snapshot(snap.ID)
				if err != nil {
					t.Fatal(err)
				}
				if string(current.Text) == "fresh watcher content" && !current.Conflict {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("reopened directory did not regain a working watch: %+v", current)
				}
				time.Sleep(time.Millisecond)
			}
		})
	}
}

func TestDocumentServiceFormatIsOwnUndoAndNeverSaves(t *testing.T) {
	s, _, a, _ := documentFixture(t, "unused")
	path := filepath.Join(s.root, "source.go")
	input := []byte("package p\nfunc f(){println(1)}\n")
	if err := os.WriteFile(path, input, 0600); err != nil {
		t.Fatal(err)
	}
	snap, err := s.Open(a, path)
	if err != nil {
		t.Fatal(err)
	}
	formatted, err := s.Format(context.Background(), a, snap.ID, "format-exact", snap.Revision)
	if err != nil || !formatted.Dirty || formatted.Revision != 2 {
		t.Fatal(formatted, err)
	}
	if _, err = s.Undo(a, snap.ID, "undo-format", formatted.Revision); err != nil {
		t.Fatal(err)
	}
	documentText(t, s, snap.ID, string(input))
	disk, err := os.ReadFile(path)
	if err != nil || string(disk) != string(input) {
		t.Fatal(string(disk), err)
	}
}
