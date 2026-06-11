package editor

import (
	"path/filepath"

	"github.com/fsnotify/fsnotify"
)

func (e *Editor) DiskChanged() bool {
	if e.watchChanged == nil {
		return false
	}
	select {
	case <-e.watchChanged:
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
		w.Close()
		return
	}
	e.watcher = w
	e.watchDir = dir
	e.watchChanged = make(chan struct{}, 1)
	e.watchDone = make(chan struct{})
	go e.watchLoop()
}

func (e *Editor) stopWatching() {
	if e.watcher == nil {
		return
	}
	close(e.watchDone)
	e.watcher.Close()
	e.watcher = nil
	e.watchDir = ""
	e.watchChanged = nil
}

func (e *Editor) watchLoop() {
	filename := filepath.Base(e.filePath)
	for {
		select {
		case event, ok := <-e.watcher.Events:
			if !ok {
				return
			}
			if filepath.Base(event.Name) == filename {
				select {
				case e.watchChanged <- struct{}{}:
				default:
				}
			}
		case _, ok := <-e.watcher.Errors:
			if !ok {
				return
			}
		case <-e.watchDone:
			return
		}
	}
}
