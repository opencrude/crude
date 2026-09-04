package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestScanBrowserPathsReturnsBoundedPickerMetadata(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{
		filepath.Join(root, "src", "nested"),
		filepath.Join(root, "node_modules", "package"),
		filepath.Join(root, ".hidden"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{
		filepath.Join(root, "src", "nested", "main.go"),
		filepath.Join(root, "node_modules", "package", "index.js"),
		filepath.Join(root, ".hidden", "secret.txt"),
	} {
		if err := os.WriteFile(path, []byte("content must not be read"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	cat, err := ScanBrowserPaths(context.Background(), BrowserPathRequest{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	paths := make(map[string]bool, len(cat.Entries))
	for _, entry := range cat.Entries {
		paths[entry.Relative] = true
	}
	if !paths["src/nested/main.go"] {
		t.Fatalf("missing nested file from catalog: %#v", paths)
	}
	for _, unwanted := range []string{"node_modules/package/index.js", ".hidden/secret.txt"} {
		if paths[unwanted] {
			t.Fatalf("unexpected filtered path %q: %#v", unwanted, paths)
		}
	}
}

func TestScanBrowserPathsDirectoriesOnly(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "src")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	cat, err := ScanBrowserPaths(context.Background(), BrowserPathRequest{Root: root, DirectoriesOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(cat.Entries) != 1 || !cat.Entries[0].IsDir || cat.Entries[0].Relative != "src" {
		t.Fatalf("directory catalog=%#v", cat.Entries)
	}
}
