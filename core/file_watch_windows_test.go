package core

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"crude/core/coreprotocol"
	"crude/workspace/fileio"
	"github.com/fsnotify/fsnotify"
)

func TestCoreFileWatchWindowsAtomicReplacementSequence(t *testing.T) {
	root := t.TempDir()
	target, stage, marker := filepath.Join(root, "target"), filepath.Join(root, "stage"), filepath.Join(root, "marker")
	for path, text := range map[string]string{target: "before", stage: "after"} {
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	w, err := newCoreWatcher(root)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if err := fileio.CommitAtomicFile(stage, target); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, nil, 0600); err != nil {
		t.Fatal(err)
	}
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	var sequence []fsnotify.Op
	for {
		select {
		case event := <-w.Events:
			t.Logf("native replacement event: %s %s", event.Op, filepath.Base(event.Name))
			if event.Name == target {
				sequence = append(sequence, event.Op)
			}
			if event.Name == marker && event.Op&fsnotify.Create != 0 {
				if !slices.Equal(sequence, []fsnotify.Op{fsnotify.Remove, fsnotify.Create}) {
					t.Fatalf("native atomic replacement sequence = %v; reviewed edits must retain remove then create", sequence)
				}
				data, err := os.ReadFile(target)
				if err != nil || string(data) != "after" {
					t.Fatalf("replacement postimage = %q, %v", data, err)
				}
				return
			}
		case err := <-w.Errors:
			t.Fatal(err)
		case <-timer.C:
			t.Fatal("replacement and ordered marker were not observed")
		}
	}
}

func TestCoreFileWatchWindowsRootCaseLossStaysFenced(t *testing.T) {
	for _, caseOnly := range []bool{false, true} {
		t.Run(fmt.Sprint(caseOnly), func(t *testing.T) {
			parent := t.TempDir()
			root := filepath.Join(parent, "MiXeD")
			if err := os.Mkdir(root, 0700); err != nil {
				t.Fatal(err)
			}
			states := make(chan bool, 32)
			events := make(chan coreprotocol.FileChange, 64)
			w, err := startCoreFileWatch(strings.ToLower(root), func(v coreprotocol.FileChange) {
				events <- v
			}, nil, func(v bool) { states <- v })
			if err != nil {
				t.Fatal(err)
			}
			defer w.Close()
			<-w.ready
			if first, second := <-states, <-states; first || !second {
				t.Fatalf("startup reliability = %v %v", first, second)
			}
			dest := filepath.Join(parent, "elsewhere")
			if caseOnly {
				dest = filepath.Join(parent, "MIXED")
			}
			if err := os.Rename(root, dest); err != nil {
				t.Fatal(err)
			}
			timer := time.NewTimer(5 * time.Second)
			defer timer.Stop()
			for {
				select {
				case event := <-events:
					if event.Removed && strings.EqualFold(event.Path, root) {
						w.Close() // Joins handling of the removal, including recovery.
						select {
						case reliable := <-states:
							if reliable {
								t.Fatal("root loss did not fence coverage")
							}
						default:
							t.Fatal("root loss emitted no unreliable edge")
						}
						for len(states) > 0 {
							if <-states {
								t.Fatal("root loss restored reliability without restart")
							}
						}
						return
					}
				case <-timer.C:
					t.Fatal("no native root removal event")
				}
			}
		})
	}
}

func TestCoreFileWatchWindowsSiblingDescendantsStayOutsideKernelScope(t *testing.T) {
	parent := t.TempDir()
	root, sibling := filepath.Join(parent, "project"), filepath.Join(parent, "other", "nested")
	for _, path := range []string{root, sibling} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	w, err := newCoreWatcher(root)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	// Do not drain events while generating enough sibling records to exhaust
	// a recursive parent's kernel buffer. None belongs to this observation.
	for i := 0; i < 1200; i++ {
		if err := os.WriteFile(filepath.Join(sibling, fmt.Sprintf("%04d-%s", i, strings.Repeat("x", 80))), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	marker := filepath.Join(root, "marker")
	if err := os.WriteFile(marker, nil, 0600); err != nil {
		t.Fatal(err)
	}
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case event := <-w.Events:
			if event.Name != marker {
				t.Fatalf("unrelated sibling reached native observation: %+v", event)
			}
			if event.Op&fsnotify.Create != 0 {
				return
			}
		case err := <-w.Errors:
			t.Fatalf("sibling activity damaged project coverage: %v", err)
		case <-timer.C:
			t.Fatal("root-local marker not observed")
		}
	}
}

func TestCoreFileWatchWindowsRootKernelOverflowIsReportedAndCloseJoins(t *testing.T) {
	root := t.TempDir()
	w, err := newCoreWatcher(root)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	// Keep the public channel undrained so its reader cannot re-arm while
	// the root's native 64-KiB queue receives a much larger name burst.
	for i := 0; i < 1200; i++ {
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("%04d-%s", i, strings.Repeat("x", 80))), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-w.Events:
		case err := <-w.Errors:
			if err != fsnotify.ErrEventOverflow {
				t.Fatalf("native overflow = %v", err)
			}
			done := make(chan struct{})
			go func() { _ = w.Close(); close(done) }()
			select {
			case <-done:
				return
			case <-timer.C:
				t.Fatal("close did not cancel and join both native readers")
			}
		case <-timer.C:
			t.Fatal("root-local kernel overflow was not reported")
		}
	}
}

func TestCoreWatchDirectoryReadAllowsConcurrentRemoval(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "transient")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(dir, "file")
	if err := os.WriteFile(child, []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	handle, err := (coreWatchWalkFS{root: root}).open("transient")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = handle.Close() }()
	entries, err := handle.ReadDir(-1)
	if err != nil || len(entries) != 1 || entries[0].Name() != "file" {
		t.Fatalf("read directory = %v, %v", entries, err)
	}
	if err := os.Remove(child); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(dir); err != nil {
		t.Fatalf("the watch scan blocked removal of its directory: %v", err)
	}
	if _, err := os.Lstat(dir); !os.IsNotExist(err) {
		t.Fatalf("removed directory remains visible: %v", err)
	}
}

func TestCoreWatchDirectoryReadAllowsConcurrentRename(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	destination := filepath.Join(root, "destination")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	handle, err := (coreWatchWalkFS{root: root}).open("source")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = handle.Close() }()
	if _, err := handle.ReadDir(-1); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(source, destination); err != nil {
		t.Fatalf("the watch scan blocked rename of its directory: %v", err)
	}
	if info, err := os.Stat(destination); err != nil || !info.IsDir() {
		t.Fatalf("renamed directory is unavailable: %v", err)
	}
}

func TestCoreWatchWalkRejectsNonlocalNames(t *testing.T) {
	walk := coreWatchWalkFS{root: t.TempDir()}
	for _, name := range []string{"../outside", `..\outside`, `C:\outside`} {
		if _, err := walk.Open(name); !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("Open(%q) = %v, want invalid path", name, err)
		}
	}
}

func TestCoreWatchWalkVisitsLongWindowsPaths(t *testing.T) {
	root := t.TempDir()
	dir := root
	for i := 0; i < 6; i++ {
		dir = filepath.Join(dir, strings.Repeat("a", 50))
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "source.go")
	if err := os.WriteFile(target, []byte("package source\n"), 0600); err != nil {
		t.Fatal(err)
	}
	found := false
	err := walkCoreWatch(root, func(path string, _ fs.DirEntry, err error) error {
		if path == target {
			found = true
		}
		return err
	})
	if err != nil || !found {
		t.Fatalf("long path walk: found=%v, error=%v", found, err)
	}
}
