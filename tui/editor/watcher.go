package editor

import (
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
)

// saveSuppressWindow is the grace period after an editor-initiated save during
// which fsnotify events for the saved file are treated as self-triggered and
// ignored. fsnotify latency is typically <50ms on Linux; 200ms provides a
// comfortable margin without risking missed external writes.
const saveSuppressWindow = 200 * time.Millisecond

func (e *Editor) DiskChanged() bool {
	e.watchMu.Lock()
	changed := e.watchChanged
	e.watchMu.Unlock()
	if changed == nil {
		return false
	}
	select {
	case <-changed:
		return true
	default:
		return false
	}
}

func (e *Editor) startWatching() {
	e.stopWatching()
	if e.filePath == "" {
		return
	}
	dir := filepath.Dir(e.filePath)
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return
	}
	if err := w.Add(dir); err != nil {
		_ = w.Close()
		return
	}
	changed := make(chan struct{}, 1)
	done := make(chan struct{})
	e.watchMu.Lock()
	e.watcher = w
	e.watchDir = dir
	e.watchChanged = changed
	e.watchDone = done
	e.lastSaveTime = time.Time{}
	e.watchMu.Unlock()
	go e.watchLoop(w, done, changed, filepath.Base(e.filePath))
}

func (e *Editor) stopWatching() {
	e.watchMu.Lock()
	w := e.watcher
	done := e.watchDone
	e.watcher = nil
	e.watchDir = ""
	e.watchChanged = nil
	e.watchDone = nil
	e.watchMu.Unlock()
	if w == nil {
		return
	}
	close(done)
	_ = w.Close()
}

func (e *Editor) watchLoop(w *fsnotify.Watcher, done <-chan struct{}, changed chan<- struct{}, filename string) {
	for {
		select {
		case event, ok := <-w.Events:
			if !ok {
				return
			}
			if filepath.Base(event.Name) != filename {
				continue
			}
			// Suppress self-triggered events from our own save(). Without
			// this, every editor save fires fsnotify → watchChanged →
			// tick → Reload(), which needlessly rebuilds the buffer and
			// can clobber rapid typing between save and the next tick.
			e.watchMu.Lock()
			lastSaveTime := e.lastSaveTime
			e.watchMu.Unlock()
			if !lastSaveTime.IsZero() && time.Since(lastSaveTime) < saveSuppressWindow {
				continue
			}
			select {
			case changed <- struct{}{}:
			default:
			}
		case _, ok := <-w.Errors:
			if !ok {
				return
			}
		case <-done:
			return
		}
	}
}
