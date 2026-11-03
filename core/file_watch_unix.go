//go:build !windows

package core

import (
	"path/filepath"

	"github.com/fsnotify/fsnotify"
)

func newCoreWatcher(_ string) (*fsnotify.Watcher, error) {
	return fsnotify.NewWatcher()
}

func coreWatchPathKey(path string) string { return filepath.Clean(path) }

func addCoreWatchParent(w *fsnotify.Watcher, root string) bool {
	parent := filepath.Dir(root)
	return parent != root && w.Add(parent) == nil
}
