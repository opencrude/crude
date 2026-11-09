package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"crude/document"
	"crude/localhistory"
	"crude/textencoding"
)

func TestFormatProjectFormatsSavedFilesAndSkipsOpenDocuments(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module project\n\ngo 1.22\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	saved := filepath.Join(root, "saved.go")
	if err := os.WriteFile(saved, []byte("package project\n\nfunc  Saved( )  int { return 1 }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	openPath := filepath.Join(root, "open.go")
	if err := os.WriteFile(openPath, []byte("package project\n\nfunc  Open( )  int { return 2 }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	docs := NewDocumentService(root, nil, nil)
	t.Cleanup(docs.Close)
	owner := document.Owner{ClientID: "project-format", Epoch: 1}
	if _, err := docs.Open(owner, openPath); err != nil {
		t.Fatal(err)
	}
	store, err := localhistory.Open(filepath.Join(t.TempDir(), "history"), root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	w := &Workspace{RootPath: root, LiveDocuments: docs, LocalHistory: store, FileOps: NewFileOpsService(root, nil, nil)}
	result, err := w.FormatProject(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Changed != 1 || result.SkippedOpen != 1 || result.SafetySnapshotID == "" ||
		len(result.Files) != 1 || result.Files[0] != "saved.go" {
		t.Fatalf("project format result = %+v", result)
	}
	got, err := os.ReadFile(saved)
	if err != nil || string(got) != "package project\n\nfunc Saved() int { return 1 }\n" {
		t.Fatalf("saved file = %q, %v", got, err)
	}
	open, err := os.ReadFile(openPath)
	if err != nil || string(open) != "package project\n\nfunc  Open( )  int { return 2 }\n" {
		t.Fatalf("open file changed = %q, %v", open, err)
	}
}

func TestFormatProjectPreservesEncodingAndContinuesPastUnreadableFiles(t *testing.T) {
	root := t.TempDir()
	source := []byte("package project\n\nfunc  Saved( )  int { return 1 }\n")
	raw, err := textencoding.EncodeText(source, textencoding.UTF16BE)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "saved.go")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(root, "unreadable.txt")
	if err := os.WriteFile(bad, []byte{0xff, 0x80}, 0o600); err != nil {
		t.Fatal(err)
	}
	docs := NewDocumentService(root, nil, nil)
	defer docs.Close()
	store, err := localhistory.Open(filepath.Join(t.TempDir(), "history"), root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	w := &Workspace{RootPath: root, LiveDocuments: docs, LocalHistory: store, FileOps: NewFileOpsService(root, nil, nil)}
	result, err := w.FormatProject(t.Context())
	if err != nil || result.Changed != 1 || result.SkippedUnreadable != 1 {
		t.Fatalf("an unreadable file prevented unrelated formatting: %+v %v", result, err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text, encoding, err := textencoding.DecodeText(after)
	if err != nil || encoding != textencoding.UTF16BE || string(text) != "package project\n\nfunc Saved() int { return 1 }\n" {
		t.Fatalf("format changed the disk encoding: %q %s %v", text, encoding, err)
	}
	if data, err := os.ReadFile(bad); err != nil || string(data) != string([]byte{0xff, 0x80}) {
		t.Fatal("format modified the unreadable file")
	}
}
