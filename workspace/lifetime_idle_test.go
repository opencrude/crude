package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"crude/document"
)

func TestIdleDocumentLeaseRefusesViewsAndRestoresOpen(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "retained.txt")
	if err := os.WriteFile(path, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	s := NewDocumentService(root, nil, nil)
	defer s.Close()
	owner := document.Owner{ClientID: "shell", Epoch: 1}
	d, err := s.Open(owner, path)
	if err != nil {
		t.Fatal(err)
	}
	if u, err := s.beginIdleLease(); err == nil {
		u()
		t.Fatal("open document accepted")
	} else if !strings.Contains(err.Error(), path) {
		t.Fatal(err)
	}
	if err := s.CloseView(owner, d.ID); err != nil {
		t.Fatal(err)
	}
	u, err := s.beginIdleLease()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Open(owner, path); err == nil {
		t.Fatal("document acquired during stop")
	}
	u()
	if _, err := s.Open(owner, path); err != nil {
		t.Fatal("refused stop broke open", err)
	}
}

func TestIdleStorageLeaseExcludesReadersAndRollsBack(t *testing.T) {
	u, err := lockWorkspaceStorage(t.Context(), false)
	if err != nil {
		t.Fatal(err)
	}
	if stop, ok := TryIdleStorageLease(); ok {
		stop()
		t.Fatal("catalog reader crossed stop")
	}
	u()
	stop, ok := TryIdleStorageLease()
	if !ok {
		t.Fatal("reader leaked")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if release, err := lockWorkspaceStorage(ctx, false); err == nil {
		release()
		t.Fatal("new reader crossed stop")
	}
	stop()
	u, err = lockWorkspaceStorage(t.Context(), false)
	if err != nil {
		t.Fatal(err)
	}
	u()
}

func TestIdleTerminalLeaseRefusesCreationAndControllers(t *testing.T) {
	s, _ := testTerminalService(t)
	u, err := s.beginIdleLease()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(TerminalCreateOpts{Command: "/bin/cat", Cols: 20, Rows: 4}); err == nil {
		t.Fatal("created terminal through idle fence")
	}
	u()
	info, err := s.Create(TerminalCreateOpts{Command: "/bin/cat", Cols: 20, Rows: 4})
	if err != nil {
		t.Fatal(err)
	}
	if u, err := s.beginIdleLease(); err == nil {
		u()
		t.Fatal("live terminal accepted")
	}
	lease, err := s.Acquire(info.ID, TerminalController{ClientID: "shell", Epoch: 1})
	if err != nil {
		t.Fatal("refusal mutated controller", err)
	}
	if err := s.Release(lease); err != nil {
		t.Fatal(err)
	}
}
