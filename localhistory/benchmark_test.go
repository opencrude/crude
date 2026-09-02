package localhistory

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func BenchmarkSnapshotAfterOneSmallEdit(b *testing.B) {
	workspace := b.TempDir()
	store, err := Open(b.TempDir(), workspace)
	if err != nil {
		b.Fatal(err)
	}
	defer store.Close()
	for i := 0; i < 1000; i++ {
		path := filepath.Join(workspace, fmt.Sprintf("src/%04d.txt", i))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("stable content\n"), 0o644); err != nil {
			b.Fatal(err)
		}
	}
	if _, err := store.Snapshot(context.Background(), SnapshotOptions{}); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		path := filepath.Join(workspace, "src", "0001.txt")
		if err := os.WriteFile(path, []byte(fmt.Sprintf("edit %d\n", i)), 0o644); err != nil {
			b.Fatal(err)
		}
		if _, changed, err := store.SnapshotIfChanged(context.Background(), SnapshotOptions{}); err != nil || !changed {
			b.Fatalf("changed=%v err=%v", changed, err)
		}
	}
}
