package filetree

import (
	"context"
	"path/filepath"

	tea "charm.land/bubbletea/v2"
)

// NewRemoteModel constructs a pure projection. loader must return a command,
// not perform I/O itself. Root requests are explicit via RequestRemoteDirectory.
func NewRemoteModel(root string, loader func(string) tea.Cmd) *FileTreeModel {
	m := NewFileTreeModelWithOptions(root, ModelOptions{NoLocalIO: true})
	m.Root.Loading = false
	m.SetDirectoryLoader(loader)
	return m
}

// RemoteDirectoryMsg must be installed on Update. Model identity and sequence
// fence replacements and refreshes; old results never overwrite newer entries.
type RemoteDirectoryMsg struct {
	Model      *FileTreeModel
	Path       string
	Generation uint64
	Entries    []DirectoryEntry
	Err        error
}

// RemoteDirectoryCommand captures a read without mutating the tree on workers.
func RemoteDirectoryCommand(ctx context.Context, model *FileTreeModel, path string, generation uint64, browse func(context.Context, string) ([]DirectoryEntry, error)) tea.Cmd {
	return func() tea.Msg {
		entries, err := browse(ctx, path)
		return RemoteDirectoryMsg{Model: model, Path: path, Generation: generation, Entries: entries, Err: err}
	}
}

// RequestRemoteDirectory provides the async refresh hook for core file events
// and initial root load. No client watcher or Git process is created.
func (m *FileTreeModel) RequestRemoteDirectory(path string) tea.Cmd {
	m.mu.Lock()
	path = filepath.Clean(path)
	node := m.nodes[path]
	loader := m.directoryLoader
	if node == nil || !node.IsDir || loader == nil {
		m.mu.Unlock()
		return nil
	}
	node.Loading = true
	m.mu.Unlock()
	return loader(path)
}

// InstallRemoteDirectory rejects results for another model or intent sequence.
// The shell owns the sequence per path and its workspace cancellation context.
func (m *FileTreeModel) InstallRemoteDirectory(msg RemoteDirectoryMsg, generation uint64) bool {
	if msg.Model != m || msg.Generation != generation {
		return false
	}
	if msg.Err != nil {
		return m.SetDirectoryLoadError(msg.Path)
	}
	return m.SetDirectoryEntries(msg.Path, msg.Entries)
}
