package fileio

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSharedDirectoryOpenDoesNotChangeRegularFileOpen(t *testing.T) {
	dir := t.TempDir()
	// Without backup semantics Windows rejects an ordinary file open on a
	// directory. Preserve that behavior for callers that read regular files.
	file, err := OpenReadSharedDelete(dir)
	if err == nil {
		_ = file.Close()
		t.Fatal("regular file open unexpectedly accepted a directory")
	}
	scan, err := OpenDirectorySharedDelete(dir)
	if err != nil {
		t.Fatalf("open directory for scanning: %v", err)
	}
	defer func() { _ = scan.Close() }()
	if info, err := scan.Stat(); err != nil || !info.IsDir() {
		t.Fatalf("directory handle: info=%v error=%v", info, err)
	}

	source, target := filepath.Join(dir, "source"), filepath.Join(dir, "target")
	if err := os.WriteFile(source, []byte("content"), 0600); err != nil {
		t.Fatal(err)
	}
	file, err = OpenReadSharedDelete(source)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	if err := os.Rename(source, target); err != nil {
		t.Fatalf("shared regular file read blocked rename: %v", err)
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != "content" {
		t.Fatalf("renamed file: content=%q error=%v", data, err)
	}
}

// An atomic save writes a temporary file and renames it into place. A file
// watcher sees the temporary file first, so a background reader (search, the
// text index) may be reading it at that moment; the rename must still work.
func TestReadFileSharedDeleteNeverBlocksRenamingTheReadFile(t *testing.T) {
	dir := t.TempDir()
	temp := filepath.Join(dir, "replacement.tmp")
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
				_, _ = ReadFileSharedDelete(temp)
			}
		}
	}()
	failures := 0
	var first error
	for i := range 300 {
		if err := os.WriteFile(temp, []byte(strings.Repeat("package main\n", 4096)), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(temp, filepath.Join(dir, fmt.Sprintf("saved%d.go", i))); err != nil {
			if first == nil {
				first = err
			}
			failures++
			_ = os.Remove(temp)
		}
	}
	close(stop)
	<-done
	if failures != 0 {
		t.Fatalf("%d of 300 renames failed while the file was read; first: %v", failures, first)
	}
}
