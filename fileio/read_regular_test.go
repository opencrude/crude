package fileio

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestReadRegularNoFollowAdmissionBoundsAndCancellation(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "rules")
	if err := os.WriteFile(path, []byte("abc"), 0600); err != nil {
		t.Fatal(err)
	}
	if data, err := ReadRegularNoFollow(context.Background(), path, 3); err != nil || string(data) != "abc" {
		t.Fatalf("regular read = %q, %v", data, err)
	}
	if _, err := ReadRegularNoFollow(context.Background(), path, 2); !errors.Is(err, ErrReadLimit) {
		t.Fatalf("oversized read = %v", err)
	}
	if _, err := ReadRegularNoFollow(context.Background(), root, 10); !errors.Is(err, ErrNotRegular) {
		t.Fatalf("directory read = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ReadRegularNoFollow(ctx, path, 3); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled read = %v", err)
	}
}

func TestReadRegularNoFollowRejectsSymlink(t *testing.T) {
	root := t.TempDir()
	target, link := filepath.Join(root, "target"), filepath.Join(root, "link")
	if err := os.WriteFile(target, []byte("external rules"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("native symlinks unavailable: %v", err)
	}
	if _, err := ReadRegularNoFollow(context.Background(), link, 1024); !errors.Is(err, ErrNotRegular) {
		t.Fatalf("link was followed: %v", err)
	}
}
