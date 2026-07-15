package editor

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"crude/tui/theme"
)

func TestEditorWatcherReportsExternalEditAndStopsCleanly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ed := New(theme.DefaultDark())
	if err := ed.Open(path); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte("package main\n\nfunc main() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for !ed.DiskChanged() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if time.Now().After(deadline) {
		t.Fatal("editor watcher did not report external edit")
	}

	// Repeated shutdown used to race watchLoop's e.watcher dereference and
	// could panic after the field was set to nil.
	ed.Close()
	ed.Close()
	if ed.DiskChanged() {
		t.Fatal("closed editor should not retain disk-change signals")
	}
}

func TestEditorWatcherCanRestartRepeatedly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ed := New(theme.DefaultDark())
	ed.filePath = path
	for i := 0; i < 25; i++ {
		ed.startWatching()
		ed.stopWatching()
	}
}
