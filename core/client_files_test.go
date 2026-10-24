package core

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"crude/coreprotocol"
)

func TestClientDirectoryMetadataAndAuthority(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "file with spaces.txt")
	if err := os.WriteFile(file, []byte("hello"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(file, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	entries, err := browseClientDirectory(context.Background(), root, root, false)
	if err != nil || len(entries) != 2 {
		t.Fatalf("entries=%v err=%v", entries, err)
	}
	if entries[0].Name != "file with spaces.txt" || entries[0].ModTime.IsZero() || !entries[1].IsSymlink {
		t.Fatal(entries)
	}
	outside := t.TempDir()
	if _, err := browseClientDirectory(context.Background(), root, outside, false); err == nil {
		t.Fatal("project escape accepted")
	}
	if _, err := browseClientDirectory(context.Background(), root, outside, true); err != nil {
		t.Fatal(err)
	}
	if _, err := externalReadPath("relative"); err == nil {
		t.Fatal("relative external authority accepted")
	}
	if _, err := externalReadPath(filepath.Join(root, "link")); err == nil {
		t.Fatal("external link accepted")
	}
}

func TestClientBoundedTransferChangesAndInvalidChunks(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "space name")
	want := bytes.Repeat([]byte("a"), coreprotocol.MaxFileChunk+13)
	if err := os.WriteFile(file, want, 0600); err != nil {
		t.Fatal(err)
	}
	got, err := readFileChunk(context.Background(), root, coreprotocol.FileReadRequest{Path: file})
	if err != nil || !bytes.Equal(want, got.Data) || !got.EOF {
		t.Fatal("incomplete immutable snapshot", err)
	}
}
