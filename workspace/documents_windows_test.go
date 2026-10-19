package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"crude/document"
	"golang.org/x/sys/windows"
)

func TestDocumentServiceWindowsShortPath(t *testing.T) {
	root := filepath.Join(t.TempDir(), "ordinary long directory name")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	ptr, err := windows.UTF16PtrFromString(root)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]uint16, 32768)
	n, err := windows.GetShortPathName(ptr, &buf[0], uint32(len(buf)))
	if err != nil || n == 0 || int(n) >= len(buf) {
		t.Fatalf("short path: %d %v", n, err)
	}
	short := windows.UTF16ToString(buf[:n])
	if strings.EqualFold(root, short) {
		t.Skip("8.3 names disabled on this volume")
	}
	path := filepath.Join(short, "document.txt")
	if err := os.WriteFile(path, []byte("before"), 0600); err != nil {
		t.Fatal(err)
	}
	saves := NewDocumentSaveService()
	defer saves.Close()
	docs := NewDocumentService(short, saves, nil)
	defer docs.Close()
	owner := document.Owner{ClientID: "short-name", Epoch: 1}
	snap, err := docs.Open(owner, path)
	if err != nil {
		t.Fatal(err)
	}
	r, err := docs.Apply(document.Change{Owner: owner, DocumentID: snap.ID, RequestID: "edit", BaseRevision: snap.Revision, Edits: []document.Edit{{Start: 0, End: 6, Text: "after"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := docs.Save(context.Background(), owner, snap.ID, r.Revision, DocumentSaveManual); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "document.txt"))
	if err != nil || string(data) != "after" {
		t.Fatalf("short-name open/edit/save lost bytes: %q %v", data, err)
	}
}
