package fileio

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMutationLeaseSerializesCrudeWriters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file.txt")
	unlock := LockFileMutation(path)
	done := make(chan error, 1)
	go func() { done <- WriteFile(path, []byte("after"), 0o600) }()
	select {
	case err := <-done:
		t.Fatalf("writer escaped lease: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	unlock()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "after" {
		t.Fatalf("data=%q", data)
	}
}

func TestTreeMutationLeaseBlocksFileCommit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file.txt")
	unlock := LockTreeMutation()
	done := make(chan error, 1)
	go func() { done <- WriteFile(path, []byte("after"), 0o600) }()
	select {
	case err := <-done:
		t.Fatalf("writer escaped tree lease: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	unlock()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestMutationLeaseWaitHonorsCancellation(t *testing.T) {
	unlock := LockTreeMutation()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if acquired, err := LockMutationContext(ctx); err == nil || acquired != nil {
		t.Fatalf("acquired=%v err=%v", acquired != nil, err)
	}
	unlock()
}
