package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"crude/document"
	"crude/localhistory"
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
