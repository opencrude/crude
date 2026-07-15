// Package filetree provides the file tree sidebar widget for the Crude CLI.
//
// This file implements the file system watcher using fsnotify,
// providing debounced file change events for the tree model.
package filetree

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// ---------------------------------------------------------------------------
// FileEvent
// ---------------------------------------------------------------------------

// FileEvent represents the type of change that occurred to a file or directory.
type FileEvent int

const (
	// FileCreated is emitted when a new file or directory is created.
	FileCreated FileEvent = iota
	// FileModified is emitted when an existing file or directory is modified.
	FileModified
	// FileDeleted is emitted when a file or directory is removed.
	FileDeleted
	// FileRenamed is emitted when a file or directory is renamed/moved.
	FileRenamed
)

// String returns a human-readable name for the event type.
func (e FileEvent) String() string {
	switch e {
	case FileCreated:
		return "FileCreated"
	case FileModified:
		return "FileModified"
	case FileDeleted:
		return "FileDeleted"
	case FileRenamed:
		return "FileRenamed"
	default:
		return "Unknown"
	}
}

// ---------------------------------------------------------------------------
// FileChangeEvent
// ---------------------------------------------------------------------------

// FileChangeEvent describes a single file system change.
type FileChangeEvent struct {
	// Type is the kind of change (created, modified, deleted, renamed).
	Type FileEvent

	// Path is the absolute path of the changed file or directory.
	Path string

	// OldPath is the previous path for renames; empty for other event types.
	OldPath string
}

// ---------------------------------------------------------------------------
// Watcher
// ---------------------------------------------------------------------------

// Watcher monitors a directory tree for file system changes and emits
// debounced FileChangeEvent values on a channel.
type Watcher struct {
	watcher  *fsnotify.Watcher
	events   chan FileChangeEvent
	done     chan struct{}
	rootPath string

	mu          sync.Mutex
	watchedDirs map[string]bool
	debounceMs  int
	stopOnce    sync.Once

	debounceTimer *time.Timer
	pendingEvents map[string]FileChangeEvent
}

// NewWatcher creates a new Watcher for the given root path. The watcher
// is not started until Start() is called.
func NewWatcher(rootPath string) *Watcher {
	return &Watcher{
		rootPath:      rootPath,
		events:        make(chan FileChangeEvent, 64),
		done:          make(chan struct{}),
		watchedDirs:   make(map[string]bool),
		pendingEvents: make(map[string]FileChangeEvent),
		debounceMs:    100,
	}
}

// Start begins watching the directory tree rooted at rootPath.
// It walks all subdirectories, adds them to the fsnotify watch list,
// and launches the event processing goroutine.
func (w *Watcher) Start() error {
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	w.watcher = fw

	// Walk the directory tree and add all directories to the watch list.
	if err := filepath.Walk(w.rootPath, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			// Permission denied or similar — skip silently.
			return nil
		}
		if !fi.IsDir() {
			return nil
		}
		// Skip hidden directories (starting with ".").
		if isHiddenDir(path) {
			return filepath.SkipDir
		}
		return w.addWatchLocked(path)
	}); err != nil {
		fw.Close()
		return err
	}

	// Launch the event processing goroutine.
	go w.eventLoop()

	return nil
}

// Stop performs a clean shutdown: signals the event loop to exit first,
// then cleans up the fsnotify watcher. Does not close the events channel
// to avoid panic from flushPendingEvents writing after close.
func (w *Watcher) Stop() {
	w.stopOnce.Do(func() {
		// Signal event loop to exit first.
		close(w.done)

		w.mu.Lock()
		if w.debounceTimer != nil {
			w.debounceTimer.Stop()
			w.debounceTimer = nil
		}
		w.mu.Unlock()

		// Close fsnotify watcher (unblocks event loop select).
		if w.watcher != nil {
			_ = w.watcher.Close()
		}
		// Don't close w.events: a debounce callback may already be flushing.
	})
}

// Events returns a read-only channel of debounced file change events.
func (w *Watcher) Events() <-chan FileChangeEvent {
	return w.events
}

// Done is closed when the watcher stops.
func (w *Watcher) Done() <-chan struct{} {
	return w.done
}

// AddWatch adds a directory to the watch list. This is useful when a new
// directory is discovered or created after Start() has been called.
// Thread-safe.
func (w *Watcher) AddWatch(dir string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.addWatchLocked(dir)
}

// addWatchLocked adds a directory to the watch list.
// Must be called with w.mu held.
func (w *Watcher) addWatchLocked(dir string) error {
	if w.watcher == nil {
		return nil
	}
	if w.watchedDirs[dir] {
		return nil // already watching
	}
	if err := w.watcher.Add(dir); err != nil {
		return err
	}
	w.watchedDirs[dir] = true
	return nil
}

// RemoveWatch stops watching a directory. This is useful when a directory
// is deleted. Thread-safe.
func (w *Watcher) RemoveWatch(dir string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.removeWatchLocked(dir)
}

// removeWatchLocked stops watching a directory.
// Must be called with w.mu held.
func (w *Watcher) removeWatchLocked(dir string) error {
	if w.watcher == nil {
		return nil
	}
	if !w.watchedDirs[dir] {
		return nil // not being watched
	}
	if err := w.watcher.Remove(dir); err != nil {
		return err
	}
	delete(w.watchedDirs, dir)
	return nil
}

// ---------------------------------------------------------------------------
// Event processing goroutine
// ---------------------------------------------------------------------------

// eventLoop reads raw fsnotify events, batched/deduplicates them within the
// debounce window, and sends the result to the events channel.
func (w *Watcher) eventLoop() {
	for {
		select {
		case <-w.done:
			return

		case _, ok := <-w.watcher.Errors:
			if !ok {
				return
			}
			// Log the error but don't crash.
			// (in production, use proper logging)

		case rawEvent, ok := <-w.watcher.Events:
			if !ok {
				return
			}
			w.handleRawEvent(rawEvent)
		}
	}
}

// handleRawEvent processes a single raw fsnotify event, applying debouncing.
func (w *Watcher) handleRawEvent(rawEvent fsnotify.Event) {
	// Skip irrelevant events (Chmod).
	eventType := determineEventType(rawEvent.Op, rawEvent.Name)
	if eventType == -1 {
		return
	}

	w.mu.Lock()

	// For Create events on directories, auto-add to the watch list.
	if rawEvent.Op&fsnotify.Create != 0 {
		if isDir(rawEvent.Name) {
			_ = w.addWatchLocked(rawEvent.Name)
		}
	}

	// For Delete events, auto-remove from the watch list.
	if rawEvent.Op&fsnotify.Remove != 0 {
		_ = w.removeWatchLocked(rawEvent.Name)
	}

	// Store or update the pending event for this path.
	w.pendingEvents[rawEvent.Name] = FileChangeEvent{
		Type: eventType,
		Path: rawEvent.Name,
	}
	w.resetDebounceTimerLocked()
	w.mu.Unlock()
}

// resetDebounceTimerLocked stops the current debounce timer (if any) and
// resets it. Must be called with w.mu held.
func (w *Watcher) resetDebounceTimerLocked() {
	if w.debounceTimer != nil {
		w.debounceTimer.Stop()
	}
	w.debounceTimer = time.AfterFunc(
		time.Duration(w.debounceMs)*time.Millisecond,
		w.flushPendingEvents,
	)
}

// flushPendingEvents is called when the debounce timer fires. It sends all
// accumulated events to the events channel.
func (w *Watcher) flushPendingEvents() {
	select {
	case <-w.done:
		return
	default:
	}

	w.mu.Lock()
	events := w.pendingEvents
	w.pendingEvents = make(map[string]FileChangeEvent)
	w.debounceTimer = nil
	w.mu.Unlock()

	for _, evt := range events {
		select {
		case w.events <- evt:
		default:
			// Channel full — drop the event rather than blocking.
		}
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// determineEventType maps an fsnotify.Op to a FileEvent.
// Returns -1 for events that should be ignored (e.g. Chmod).
func determineEventType(op fsnotify.Op, path string) FileEvent {
	// fsnotify v1 uses Op constants that are bitflags.
	// Check the operation bits in order of priority.
	switch {
	case op&fsnotify.Create != 0:
		return FileCreated
	case op&fsnotify.Write != 0:
		return FileModified
	case op&fsnotify.Remove != 0:
		return FileDeleted
	case op&fsnotify.Rename != 0:
		// fsnotify emits Rename for the old-path side of a rename/move.
		// We can't reliably correlate with the subsequent Create,
		// so treat it as a deletion. The Create will produce FileCreated.
		return FileDeleted
	case op&fsnotify.Chmod != 0:
		// Permission changes are not relevant for the file tree.
		return -1
	default:
		return -1
	}
}

// isDir returns true if the path exists and is a directory.
func isDir(path string) bool {
	fi, err := os.Stat(path)
	if err != nil {
		return false
	}
	return fi.IsDir()
}

// isHiddenDir returns true if the path's base name starts with ".".
func isHiddenDir(path string) bool {
	base := filepath.Base(path)
	return strings.HasPrefix(base, ".") && base != "." && base != ".."
}
