package pathwalk

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestScanAppliesPickerBoundsAndDirectoryPolicy(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{
		filepath.Join(root, "src", "nested"),
		filepath.Join(root, "node_modules", "pkg"),
		filepath.Join(root, ".hidden"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{
		filepath.Join(root, "src", "nested", "main.go"),
		filepath.Join(root, "node_modules", "pkg", "index.js"),
		filepath.Join(root, ".hidden", "secret.txt"),
	} {
		if err := os.WriteFile(path, []byte("not read"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	cat, err := Scan(context.Background(), Request{
		Root:            root,
		DirectoriesOnly: true,
		MaxWorkers:      99,
		MaxEntries:      20,
		SkipDir: func(_, name string) bool {
			return name == "node_modules"
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	paths := make(map[string]bool, len(cat.Entries))
	for _, entry := range cat.Entries {
		paths[entry.Relative] = true
		if !entry.IsDir {
			t.Fatalf("non-directory entry in directory-only scan: %#v", entry)
		}
	}
	if !paths["src"] || !paths["src/nested"] {
		t.Fatalf("missing nested directories: %#v", paths)
	}
	for _, unwanted := range []string{".hidden", "node_modules", "src/nested/main.go"} {
		if paths[unwanted] {
			t.Fatalf("unexpected path %q in catalog: %#v", unwanted, paths)
		}
	}
}

func TestScanStopsAtEntryAndPathBounds(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 10; i++ {
		path := filepath.Join(root, "file-"+string(rune('a'+i))+".txt")
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cat, err := Scan(context.Background(), Request{Root: root, MaxEntries: 3})
	if err != nil {
		t.Fatal(err)
	}
	if !cat.Truncated || len(cat.Entries) != 3 {
		t.Fatalf("catalog=%#v, want exactly three truncated entries", cat)
	}
}

func TestWalkContextCancelsCallbacksAndWorkers(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 20; i++ {
		path := filepath.Join(root, "file-"+string(rune('a'+i))+".txt")
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	seen := 0
	err := WalkContext(ctx, &Config{NumWorkers: 2}, root, func(_ string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		seen++
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("WalkContext error=%v, want context cancellation", err)
	}
	if seen == 0 {
		t.Fatal("cancellation callback was never invoked")
	}
}
