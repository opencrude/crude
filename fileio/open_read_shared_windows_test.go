package fileio

import (
	"os"
	"path/filepath"
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
