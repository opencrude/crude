package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWorkspaceBrowseDirectoryReturnsNeutralEntries(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws := &Workspace{RootPath: root}
	listing, err := ws.BrowseDirectory(root)
	if err != nil {
		t.Fatal(err)
	}
	if listing.Path != root || len(listing.Entries) != 2 {
		t.Fatalf("listing=%#v", listing)
	}
	got := map[string]bool{}
	for _, entry := range listing.Entries {
		got[entry.Name] = entry.IsDir
	}
	if !got["src"] || got["main.go"] {
		t.Fatalf("entry metadata=%#v", got)
	}
}
