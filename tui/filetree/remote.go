package filetree

import (
	"context"
	"maps"
	"path/filepath"

	tea "charm.land/bubbletea/v2"
)

// SetRemoteGitStatuses installs one complete core snapshot on Update. Empty
// snapshots clear stale marks; retained metadata also applies to later pages.
// No resolver, watcher or filesystem access is involved.
func (m *Model) SetRemoteGitStatuses(statuses map[string]GitStatus) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.noLocalIO {
		return
	}
	m.remoteGitStatuses = maps.Clone(statuses)
	if m.remoteGitStatuses == nil {
		m.remoteGitStatuses = make(map[string]GitStatus)
	}
	for path, node := range m.nodes {
		node.GitStatus = m.remoteGitStatuses[path]
	}
	m.recomputeAggregateStatuses()
}

// NewRemoteModel constructs a pure projection. loader must return a command,
// not perform I/O itself. Root requests are explicit via RequestRemoteDirectory.
func NewRemoteModel(root string, loader func(context.Context, string) tea.Cmd) *Model {
	m := NewFileTreeModelWithOptions(root, ModelOptions{NoLocalIO: true})
	m.Root.Loading = false
	m.SetDirectoryLoader(loader)
	return m
}

// RemoteDirectoryMsg must be installed on Update. Model identity and sequence
// fence replacements and refreshes; old results never overwrite newer entries.
type RemoteDirectoryMsg struct {
	Model      *Model
	Path       string
	Generation uint64
	Entries    []DirectoryEntry
	Err        error
}

// RemoteDirectoryCommand captures a read without mutating the tree on workers.
func RemoteDirectoryCommand(ctx context.Context, model *Model, path string, generation uint64, browse func(context.Context, string) ([]DirectoryEntry, error)) tea.Cmd {
	return func() tea.Msg {
		entries, err := browse(ctx, path)
		return RemoteDirectoryMsg{Model: model, Path: path, Generation: generation, Entries: entries, Err: err}
	}
}

// RequestRemoteDirectory provides the async refresh hook for core file events
// and initial root load. No client watcher or Git process is created.
func (m *Model) RequestRemoteDirectory(path string) tea.Cmd {
	return m.requestRemoteDirectory(context.Background(), path, true)
}

// RefreshRemoteDirectories renews requested listings without expanding them.
// A nil set resyncs all cached or pending listings, even under collapsed rows.
// A folder known only from its parent's listing needs no read or watch.
func (m *Model) RefreshRemoteDirectories(parents map[string]bool) tea.Cmd {
	m.mu.RLock()
	var paths []string
	add := func(node *TreeNode) {
		if node != nil && node.IsDir && (node == m.Root || node.Children != nil || node.Loading || node.IsExpanded) {
			paths = append(paths, node.Path)
		}
	}
	if parents == nil {
		for _, node := range m.nodes {
			add(node)
		}
	} else {
		for path := range parents {
			add(m.nodes[path])
		}
	}
	m.mu.RUnlock()

	var cmds []tea.Cmd
	for _, path := range paths {
		cmds = append(cmds, m.RequestRemoteDirectory(path))
	}
	return tea.Batch(cmds...)
}

// RequestRemoteDirectoryContext reads without changing expansion/loading state.
// It is used by an owned reveal which installs the result only while current.
func (m *Model) RequestRemoteDirectoryContext(ctx context.Context, path string) tea.Cmd {
	return m.requestRemoteDirectory(ctx, path, false)
}

func (m *Model) requestRemoteDirectory(ctx context.Context, path string, markLoading bool) tea.Cmd {
	m.mu.Lock()
	path = filepath.Clean(path)
	node := m.nodes[path]
	loader := m.directoryLoader
	if node == nil || !node.IsDir || loader == nil {
		m.mu.Unlock()
		return nil
	}
	if markLoading {
		node.Loading = true
	}
	m.mu.Unlock()
	return loader(ctx, path)
}

// InstallRemoteDirectory rejects results for another model or intent sequence.
// The shell owns the sequence per path and its workspace cancellation context.
func (m *Model) InstallRemoteDirectory(msg RemoteDirectoryMsg, generation uint64) bool {
	if msg.Model != m || msg.Generation != generation {
		return false
	}
	if msg.Err != nil {
		return m.SetDirectoryLoadError(msg.Path)
	}
	return m.SetDirectoryEntries(msg.Path, msg.Entries)
}
