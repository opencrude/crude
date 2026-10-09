package filetree

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

func TestRemoteModelNeverLoadsClientDisk(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "local-only"), []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	requests := 0
	m := NewRemoteModel(root, func(string) tea.Cmd { requests++; return func() tea.Msg { return nil } })
	if len(m.VisibleNodes()) != 0 || !m.Root.ModTime.IsZero() || m.GitResolver() != nil {
		t.Fatal("constructor read local metadata")
	}
	if err := m.StartWatcher(); err != nil || m.Watcher() != nil {
		t.Fatal("remote watcher started", err)
	}
	m.SetFilter("local")
	m.ExpandAll()
	m.SetDiagnostics(filepath.Join(root, "local-only"), 1, 0, 0)
	m.ApplyLocalMutation(FileChangeEvent{Type: FileCreated, Path: filepath.Join(root, "local-only")})
	if m.NodeByPath(filepath.Join(root, "local-only")) != nil {
		t.Fatal("local disk entered remote projection")
	}
	m.SetFilter("")
	if m.RequestRemoteDirectory(root) == nil || requests != 1 {
		t.Fatal("async request missing")
	}
	stamp := time.Unix(100, 0)
	msg := RemoteDirectoryMsg{Model: m, Path: root, Generation: 2, Entries: []DirectoryEntry{{Name: "remote folder", Path: filepath.Join(root, "remote folder"), IsDir: true, ModTime: stamp}}}
	if m.InstallRemoteDirectory(msg, 1) {
		t.Fatal("stale result installed")
	}
	if !m.InstallRemoteDirectory(msg, 2) || len(m.VisibleNodes()) != 1 {
		t.Fatal("remote rows absent")
	}
	n := m.VisibleNodes()[0]
	if !n.ModTime.Equal(stamp) || n.Depth != 0 || n.Parent != m.Root {
		t.Fatal("tree geometry metadata lost", n)
	}
	if m.Expand(n.Path) == nil || requests != 2 {
		t.Fatal("child load not async")
	}
	if !m.SetDirectoryEntries(n.Path, []DirectoryEntry{{Name: "leaf", Path: filepath.Join(n.Path, "leaf")}}) {
		t.Fatal("child installation failed")
	}
	if len(m.VisibleNodes()) != 2 || m.VisibleNodes()[1].Depth != 1 {
		t.Fatal("ancestor projection failed")
	}
}
