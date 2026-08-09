package app

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/types"
)

// TestWatchConfigConcurrentStopAndReload exercises fsnotify callback + stop
// teardown under -race without flake (deterministic barrier after first reload).
func TestWatchConfigConcurrentStopAndReload(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	cfgPath := ConfigPath()
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	fc := DefaultFileConfig()
	fc.Theme = &types.ThemeSettings{Preset: "dark"}
	if err := SaveFileConfig(fc); err != nil {
		t.Fatal(err)
	}

	ch := make(chan tea.Msg, 64)
	stop, err := WatchConfig(func(msg tea.Msg) { ch <- msg })
	if err != nil {
		t.Fatal(err)
	}

	// One deterministic valid write so strict load emits before concurrent churn.
	fc.Theme = &types.ThemeSettings{Preset: "light"}
	if err := SaveFileConfig(fc); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for initial config reload")
	}

	var wg sync.WaitGroup
	const writers = 8
	wg.Add(writers)
	for i := 0; i < writers; i++ {
		go func(n int) {
			defer wg.Done()
			cfg := LoadFileConfig()
			if cfg.Theme == nil {
				cfg.Theme = &types.ThemeSettings{}
			}
			if n%2 == 0 {
				cfg.Theme.Preset = "light"
			} else {
				cfg.Theme.Preset = "high-contrast"
			}
			_ = SaveFileConfig(cfg)
		}(i)
	}

	wg.Wait()

	var stopWG sync.WaitGroup
	stopWG.Add(1)
	go func() {
		defer stopWG.Done()
		stop()
	}()
	wg.Wait()
	stopWG.Wait()
}
