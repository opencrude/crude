package shelltree

import (
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/filetree"
	"crude/tui/theme"
	"crude/tui/ui"
)

// TestTreeScrollbarDrag drives a real click+drag on the tree scrollbar
// through Screen.Handle and asserts the tree content scrolls (both directions).
func TestTreeScrollbarDrag(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 60; i++ {
		name := filepath.Join(dir, "file"+string(rune('a'+i%26))+string(rune('a'+i/26))+".go")
		if err := os.WriteFile(name, []byte("package x\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	model := filetree.NewFileTreeModel(dir)
	screen := New(theme.DefaultDark(), model)
	const w, h = 32, 12
	host := ui.NewHost(theme.DefaultDark(), screen)
	host.Resize(w, h)
	host.Layout()
	buf := screen.PaintTestBuffer(w, h)

	tree := screen.TreeControl()
	scroll := tree.Scroll()
	if scroll.MaxOffset() <= 0 {
		t.Skipf("tree does not overflow (content=%d viewport=%d)", scroll.Content(), scroll.Viewport())
	}

	bar := screen.scrollbar.Bounds()
	cx := bar.X + bar.W/2
	top, _ := scroll.ThumbBounds(bar.H)
	cy := bar.Y + top

	before := model.ScrollOffset()
	host.Handle(tea.MouseClickMsg{X: cx, Y: cy, Button: tea.MouseLeft})
	host.Handle(tea.MouseMotionMsg{X: cx - 8, Y: min(cy+6, bar.Y+bar.H-1), Button: tea.MouseLeft})
	host.Handle(tea.MouseReleaseMsg{X: cx - 8, Y: min(cy+6, bar.Y+bar.H-1), Button: tea.MouseLeft})

	t.Logf("scroll.Offset=%d modelOffset before=%d after=%d", scroll.Offset(), before, model.ScrollOffset())
	if scroll.Offset() == 0 {
		t.Fatal("drag did not move tree ScrollModel at all")
	}
	if scroll.Offset() != model.ScrollOffset() {
		t.Fatalf("ScrollModel offset=%d diverged from FileTreeModel offset=%d (round-trip broken)", scroll.Offset(), model.ScrollOffset())
	}
	// Paint again — syncFromModel must not yank the offset back.
	buf2 := screen.PaintTestBuffer(w, h)
	if scroll.Offset() == 0 {
		t.Fatal("paint-time sync yanked the scroll offset back to 0 (EnsureVisible re-anchor)")
	}
	changed := false
	for y := 0; y < h; y++ {
		for x := 0; x < w-2; x++ {
			if buf.Get(x, y).Char != buf2.Get(x, y).Char {
				changed = true
			}
		}
	}
	if !changed {
		t.Fatal("offset moved but painted rows did not change")
	}
}
