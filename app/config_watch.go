package app

import (
	"path/filepath"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/fsnotify/fsnotify"

	"crude/types"
)

const configWatchDebounce = 150 * time.Millisecond

// WatchConfig watches the app config file and notifies the TUI when it
// changes so theme (and future live settings) can hot-reload without restart.
func WatchConfig(send func(tea.Msg)) (stop func(), err error) {
	path := ConfigPath()
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	dir := filepath.Dir(path)
	if err := watcher.Add(dir); err != nil {
		_ = watcher.Close()
		return nil, err
	}

	done := make(chan struct{})
	var stopOnce sync.Once
	go func() {
		defer func() { _ = watcher.Close() }()
		base := filepath.Base(path)
		var debounce *time.Timer
		var debounceC <-chan time.Time

		stopDebounce := func() {
			if debounce == nil {
				return
			}
			if !debounce.Stop() {
				select {
				case <-debounce.C:
				default:
				}
			}
			debounce = nil
			debounceC = nil
		}

		scheduleReload := func() {
			stopDebounce()
			debounce = time.NewTimer(configWatchDebounce)
			debounceC = debounce.C
		}

		emitReload := func() {
			select {
			case <-done:
				return
			default:
			}
			cfg, err := loadFileConfigFrom(path)
			if err != nil {
				return
			}
			select {
			case <-done:
				return
			default:
			}
			preset := "dark"
			if cfg.Theme != nil && cfg.Theme.Preset != "" {
				preset = cfg.Theme.Preset
			}
			if send != nil {
				send(types.ConfigReloadedMsg{ThemePreset: preset})
			}
		}

		for {
			select {
			case <-done:
				stopDebounce()
				return
			case event, ok := <-watcher.Events:
				if !ok {
					stopDebounce()
					return
				}
				if filepath.Base(event.Name) != base {
					continue
				}
				if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename) == 0 {
					continue
				}
				scheduleReload()
			case <-debounceC:
				debounce = nil
				debounceC = nil
				emitReload()
			case _, ok := <-watcher.Errors:
				if !ok {
					stopDebounce()
					return
				}
			}
		}
	}()

	return func() {
		stopOnce.Do(func() { close(done) })
	}, nil
}
