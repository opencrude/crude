package fileio

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenRootRegularConfinesAndRejectsLinks(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "file"), []byte("contents"), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	file, err := OpenRootRegular(t.Context(), root, "file")
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(file)
	_ = file.Close()
	if err != nil || string(data) != "contents" {
		t.Fatalf("regular file: %q %v", data, err)
	}
	if _, err := OpenRootRegular(t.Context(), root, "../outside"); err == nil {
		t.Fatal("parent traversal escaped the root")
	}
	if err := os.Symlink("file", filepath.Join(dir, "link")); err == nil {
		if _, err := OpenRootRegular(t.Context(), root, "link"); !errors.Is(err, ErrNotRegular) {
			t.Fatalf("a link was accepted: %v", err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := OpenRootRegular(ctx, root, "file"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled open: %v", err)
	}
}
