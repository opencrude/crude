package workspace

import (
	"context"
	"crude/document"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDocumentEditorConfigRequiresLiveOwnerAndPreservesSource(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "file.txt")
	for p, text := range map[string]string{path: "\tword", filepath.Join(root, ".editorconfig"): "root=true\n[*]\ntab_width=8\n"} {
		if err := os.WriteFile(p, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	s := NewDocumentService(root, nil, nil)
	defer s.Close()
	owner := document.Owner{ClientID: "reader", Epoch: 1}
	snap, err := s.Open(owner, path)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.EditorConfig(t.Context(), document.Owner{ClientID: "other", Epoch: 1}, snap.ID); !errors.Is(err, document.ErrOwner) {
		t.Fatal("unowned document config was read", err)
	}
	got, c, err := s.EditorConfig(t.Context(), owner, snap.ID)
	if err != nil || got != path || c.TabWidth != 8 {
		t.Fatal(got, c, err)
	}
	after, err := s.Snapshot(snap.ID)
	if err != nil || after.Revision != snap.Revision || after.Dirty || string(after.Text) != string(snap.Text) {
		t.Fatal("reading policy mutated source")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := s.EditorConfig(ctx, owner, snap.ID); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled read succeeded", err)
	}
}
