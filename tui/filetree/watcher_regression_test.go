package filetree

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
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
	th := theme.DefaultDark()
	tokens := ui.NewTokenSet(th)
	cfg := PaintConfigFromTheme(th)
	tree := ui.NewTree("test-tree", tokens)
	tree.SetRowPainter(NewTreeRowPainter(model, model.IconResolver(), cfg))
	tree.SetItems(TreeItems(model))
	model.Update(TreeHeightMsg{Height: 4})
	SyncScrollModel(model, tree.Scroll(), 4)
	scroll := ui.NewScrollbarControl("sb", tree.Scroll(), tokens)

	panelW := 30
	contentW := SidebarContentWidth(panelW)
	tree.Layout(ui.LayoutContext{}, ui.Rect{W: contentW, H: 4})
	scroll.Layout(ui.LayoutContext{}, ui.Rect{X: panelW - 1, Y: 0, W: 1, H: 4})

	buf := surface.NewCellBuffer(panelW, 4)
	tree.Paint(buf, 0, 0, ui.PaintContext{Focused: true})
	scroll.Paint(buf, 0, 0, ui.PaintContext{})

	for y := 0; y < 4; y++ {
		gutter := buf.Get(contentW, y)
		if gutter.Char != ' ' && gutter.Char != 0 {
			t.Fatalf("row %d gutter col=%q, want blank", y, string(gutter.Char))
		}
		sb := buf.Get(contentW+1, y)
		if sb.Char == ' ' || sb.Char == 0 {
			t.Fatalf("row %d expected scrollbar glyph at col %d", y, contentW+1)
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
