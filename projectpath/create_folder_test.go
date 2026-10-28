package projectpath

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCreateWorkspaceFolderMakesOneChildWithoutOverwriting(t *testing.T) {
	parent := t.TempDir()
	want := filepath.Join(parent, "My project")
	got, err := CreateFolder(context.Background(), parent, "My project")
	if err != nil || got != want {
		t.Fatalf("create path=%q err=%v, want %q", got, err, want)
	}
	if info, err := os.Stat(want); err != nil || !info.IsDir() {
		t.Fatalf("created folder: %v %v", info, err)
	}
	existing := filepath.Join(want, "keep.txt")
	if err := os.WriteFile(existing, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateFolder(context.Background(), parent, "My project"); err == nil {
		t.Fatal("existing folder was accepted as a new folder")
	}
	if data, err := os.ReadFile(existing); err != nil || string(data) != "keep" {
		t.Fatalf("existing content changed: %q %v", data, err)
	}
	if err := os.WriteFile(filepath.Join(parent, "file"), []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateFolder(context.Background(), parent, "file"); err == nil {
		t.Fatal("existing file was accepted")
	}
	if data, err := os.ReadFile(filepath.Join(parent, "file")); err != nil || string(data) != "original" {
		t.Fatalf("existing file changed: %q %v", data, err)
	}
}

func TestCreateWorkspaceFolderRejectsPathsAndCancelledRequests(t *testing.T) {
	parent := t.TempDir()
	names := []string{"", " ", ".", "..", ".. ", "name.", "name ", "../escape", "nested/child", `nested\child`, "bad\x00name", "bad\nname", strings.Repeat("a", 1025)}
	if runtime.GOOS == "windows" {
		names = append(names, "CON", "NUL", "a:b")
	}
	for _, name := range names {
		if _, err := CreateFolder(context.Background(), parent, name); err == nil {
			t.Errorf("accepted invalid folder name %q", name)
		}
	}
	if _, err := CreateFolder(context.Background(), "relative", "child"); err == nil {
		t.Fatal("accepted a relative parent")
	}
	if _, err := CreateFolder(context.Background(), filepath.Join(parent, "missing"), "child"); err == nil {
		t.Fatal("created missing parents")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := CreateFolder(ctx, parent, "cancelled"); err == nil {
		t.Fatal("cancelled request created a folder")
	}
	entries, err := os.ReadDir(parent)
	if err != nil || len(entries) != 0 {
		t.Fatalf("invalid/cancelled requests changed parent: %v %v", entries, err)
	}
}
