//go:build unix

package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	model "crude/types"
	"golang.org/x/sys/unix"
)

func TestAssemblyDiscoveryIgnoreFIFODoesNotWaitForWriter(t *testing.T) {
	root := t.TempDir()
	writeTree(t, filepath.Join(root, "main.go"), "package main")
	if err := unix.Mkfifo(filepath.Join(root, ".gitignore"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan *assemblyDiscovery, 1)
	go func() { done <- discoverProjectLanguages(ctx, root, model.DefaultLSPSettings()) }()
	select {
	case found := <-done:
		if found.err != nil || found.languages["go"] == nil {
			t.Fatalf("special ignore input changed source evidence: %+v", found)
		}
	case <-ctx.Done():
		t.Fatal("discovery waited for a FIFO writer")
	}
}

func TestAssemblyDiscoveryUnreadableSubtreeIsNotComplete(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permissions")
	}
	root := t.TempDir()
	subtree := filepath.Join(root, "private")
	writeTree(t, filepath.Join(subtree, "only.php"), "<?php")
	if err := os.Chmod(subtree, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(subtree, 0700); err != nil {
			t.Error(err)
		}
	})
	found := discoverProjectLanguages(context.Background(), root, model.DefaultLSPSettings())
	if found.err == nil {
		t.Fatalf("unreadable subtree claimed complete discovery: %+v", found)
	}
}
