package viewmodel

import (
	"context"
	"path/filepath"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"crude/coreprotocol"
	"crude/shellport"
	"crude/tui/filetree"
	"crude/workspace"
)

// newClientFileTree never constructs a client Git resolver or disk watcher for
// a connected workspace. Sequence state belongs to this exact model lifetime.
func newClientFileTree(m *App) *filetree.FileTreeModel {
	files, ok := m.ws.(shellport.ClientFiles)
	if !ok {
		return filetree.NewPlaceholderFileTreeModel(m.ws.RootPath())
	}
	var model *filetree.FileTreeModel
	var mu sync.Mutex
	sequences := make(map[string]uint64)
	generation := m.workspaceSwitchGen.Load()
	model = filetree.NewRemoteModel(m.ws.RootPath(), func(path string) tea.Cmd {
		mu.Lock()
		sequences[path]++
		sequence := sequences[path]
		mu.Unlock()
		return func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			entries, err := files.BrowseDirectory(ctx, path)
			converted := make([]filetree.DirectoryEntry, len(entries))
			for i, e := range entries {
				converted[i] = filetree.DirectoryEntry{Name: e.Name, Path: e.Path, IsDir: e.IsDir, IsSymlink: e.IsSymlink, ModTime: e.ModTime}
			}
			return clientTreeDirectoryMsg{generation: generation, latest: func() uint64 { mu.Lock(); defer mu.Unlock(); return sequences[path] }, result: filetree.RemoteDirectoryMsg{Model: model, Path: path, Generation: sequence, Entries: converted, Err: err}}
		}
	})
	return model
}

type clientTreeDirectoryMsg struct {
	generation uint64
	latest     func() uint64
	result     filetree.RemoteDirectoryMsg
}

func (m *App) applyClientTreeDirectory(msg clientTreeDirectoryMsg) {
	if m.asyncIOStopped.Load() || msg.generation != m.workspaceSwitchGen.Load() || m.Tree == nil || m.Tree.Model != msg.result.Model {
		return
	}
	if msg.latest != nil {
		msg.result.Model.InstallRemoteDirectory(msg.result, msg.latest())
	}
}

func (m *App) beginClientFileTreeLoad() tea.Cmd {
	if _, ok := m.ws.(shellport.ClientFiles); !ok || m.Tree == nil || m.Tree.Model == nil {
		return nil
	}
	return m.Tree.Model.RequestRemoteDirectory(m.ws.RootPath())
}

// applyCoreFileChanges refreshes metadata using the core read port. The feed
// never authorizes disk reads, tree watchers or local Git subprocesses.
func (m *App) applyCoreFileChanges(changes []coreprotocol.FileChange) tea.Cmd {
	if _, ok := m.ws.(shellport.ClientFiles); !ok || m.Tree == nil || m.Tree.Model == nil {
		return nil
	}
	paths := make(map[string]bool)
	for _, change := range changes {
		if change.Resync {
			paths[m.ws.RootPath()] = true
			for _, node := range m.Tree.Model.VisibleNodes() {
				if node.IsDir {
					paths[node.Path] = true
				}
			}
		}
		if change.Path != "" {
			paths[filepath.Dir(change.Path)] = true
		}
		if change.Git || change.Resync {
			m.requestGitSnapshot(time.Now())
		}
	}
	var commands []tea.Cmd
	for path := range paths {
		if cmd := m.Tree.Model.RequestRemoteDirectory(path); cmd != nil {
			commands = append(commands, cmd)
		}
	}
	return tea.Batch(commands...)
}

func clientDirectoryListing(path string, entries []shellport.ClientDirectoryEntry) workspace.DirectoryListing {
	listing := workspace.DirectoryListing{Path: path, Entries: make([]workspace.DirectoryEntry, len(entries))}
	for i, e := range entries {
		listing.Entries[i] = workspace.DirectoryEntry{Name: e.Name, Path: e.Path, IsDir: e.IsDir}
	}
	return listing
}
