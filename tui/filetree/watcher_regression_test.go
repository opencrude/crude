package filetree

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"crude/tui/rendering"
	"crude/tui/theme"
)

func TestTreeScrollbarHasContentGutter(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 20; i++ {
		path := filepath.Join(dir, fmt.Sprintf("file-%02d.go", i))
		if err := os.WriteFile(path, []byte("package sample\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	model := NewFileTreeModel(dir)
	renderer := NewTreeRenderer(model, model.IconResolver())
	renderer.ApplyTheme(theme.DefaultDark())
	renderer.SetScrollbarContentHeight(4)
	lines := renderer.RenderToLines(30, 4)
	if len(lines) != 4 {
		t.Fatalf("RenderToLines returned %d lines", len(lines))
	}
	for i, line := range lines {
		if got := rendering.VisibleWidth(line); got != 30 {
			t.Fatalf("line %d width=%d, want 30", i, got)
		}
		plain := []rune(rendering.StripAnsi(line))
		if len(plain) < 2 || plain[len(plain)-2] != ' ' {
			t.Fatalf("line %d missing gutter before scrollbar: %q", i, rendering.StripAnsi(line))
		}
		if plain[len(plain)-1] == ' ' {
			t.Fatalf("line %d expected visible scrollbar in final column: %q", i, rendering.StripAnsi(line))
		}
	}
}

func TestProcessWatcherEventCallsBackOutsideTreeLock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	model := NewFileTreeModel(dir)
	callbackDone := make(chan struct{})
	model.SetOnFileChanged(func(filePath string, changeType int) {
		if filePath != path || changeType != 2 {
			t.Errorf("callback=(%q,%d), want (%q,2)", filePath, changeType, path)
		}
		// These accessors acquire the same tree lock. This used to deadlock
		// because ProcessWatcherEvent invoked the callback under its write lock.
		_ = model.VisibleNodes()
		_ = model.RootPath()
		close(callbackDone)
	})

	go model.ProcessWatcherEvent(FileChangeEvent{Type: FileModified, Path: path})
	select {
	case <-callbackDone:
	case <-time.After(time.Second):
		t.Fatal("watcher callback deadlocked against tree readers")
	}
}

func TestWatcherStopIsIdempotent(t *testing.T) {
	w := NewWatcher(t.TempDir())
	if err := w.Start(); err != nil {
		t.Fatal(err)
	}
	w.Stop()
	w.Stop()
	select {
	case <-w.Done():
	case <-time.After(time.Second):
		t.Fatal("watcher Done channel was not closed")
	}
}
