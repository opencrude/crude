package core

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"crude/coreclient"
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
	read := func(ctx context.Context, in coreprotocol.FileReadRequest) (coreprotocol.FileChunk, error) {
		return readFileChunk(ctx, root, in)
	}
	got, err := coreclient.ReadBoundedClientFile(context.Background(), file, read)
	if err != nil || !bytes.Equal(want, got) {
		t.Fatalf("transfer err=%v len=%d", err, len(got))
	}
	calls := 0
	got, err = coreclient.ReadBoundedClientFile(context.Background(), file, func(ctx context.Context, in coreprotocol.FileReadRequest) (coreprotocol.FileChunk, error) {
		calls++
		if calls == 2 {
			if err := os.WriteFile(file, bytes.Repeat([]byte("b"), len(want)), 0600); err != nil {
				t.Fatal(err)
			}
		}
		return read(ctx, in)
	})
	if err == nil || got != nil {
		t.Fatal("changed continuation published partial data")
	}
	for _, bad := range []coreprotocol.FileChunk{
		{Path: file, Digest: "d", Total: coreprotocol.MaxFileRead + 1},
		{Path: file, Digest: "d", Total: 1},
		{Path: file, Digest: "d", Total: 1, Data: []byte("a"), NextOffset: 2, EOF: true},
	} {
		got, err := coreclient.ReadBoundedClientFile(context.Background(), file, func(context.Context, coreprotocol.FileReadRequest) (coreprotocol.FileChunk, error) { return bad, nil })
		if err == nil || got != nil {
			t.Fatal("invalid chunk accepted", bad)
		}
	}
}
