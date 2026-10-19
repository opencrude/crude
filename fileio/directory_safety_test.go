package fileio

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCheckDirectoryNoSymlinks(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "nested", "directory")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := CheckDirectoryNoSymlinks(dir); err != nil {
		t.Fatalf("ordinary directory rejected: %v", err)
	}
	if err := CheckDirectoryNoSymlinks(filepath.Join(dir, "missing")); !os.IsNotExist(err) {
		t.Fatalf("missing directory accepted: %v", err)
	}
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := CheckDirectoryNoSymlinks(file); err == nil {
		t.Fatal("regular file accepted as a directory")
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(filepath.Join(root, "nested"), alias); err != nil {
		t.Skipf("directory symlink unavailable: %v", err)
	}
	if err := CheckDirectoryNoSymlinks(filepath.Join(alias, "directory")); err == nil {
		t.Fatal("symlink parent accepted")
	}
}
