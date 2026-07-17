package widgets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/rendering"
	"crude/tui/theme"
)

// TestFilePicker_HitTest_EntryClick verifies that the file picker's
// click hit-test maps popup-local coordinates to the correct entry
// index. This is the fix for "click on file picker entry does
// nothing" — the picker now supports left-click to select an entry.
func TestFilePicker_HitTest_EntryClick(t *testing.T) {
	th := theme.DefaultDark()

	// Build a temp directory with a few entries.
	dir := t.TempDir()
	for _, name := range []string{"a.txt", "b.txt", "c.txt", "subdir"} {
		path := filepath.Join(dir, name)
		if name == "subdir" {
			if err := os.Mkdir(path, 0o755); err != nil {
				t.Fatal(err)
			}
		} else {
			if err := os.WriteFile(path, []byte("hi"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}

	fp := NewFilePicker(th)
	fp.Open(dir)

	// Force a render so lastEntryPageSize is populated. We render
	// into a reasonable size — the actual values are computed in
	// ViewInner. We just need to call it once.
	_ = fp.ViewInner(50, 20, 0)

	// Layout (after render):
	// localY 0: top border
	// localY 1: path
	// localY 2: filter
	// localY 3..3+pageSize-1: entry rows
	// Hit-test localY=3 (first entry).
	idx := fp.HitTest(5, 3)
	if idx < 0 {
		t.Fatalf("expected entry hit on first row, got %d", idx)
	}
	if idx >= len(fp.entries) {
		t.Fatalf("entry index %d out of range (have %d)", idx, len(fp.entries))
	}

	// Hit-test on the path/filter rows should return -1.
	if got := fp.HitTest(5, 1); got != -1 {
		t.Errorf("expected no hit on path row, got %d", got)
	}
	if got := fp.HitTest(5, 2); got != -1 {
		t.Errorf("expected no hit on filter row, got %d", got)
	}

	// Hit-test below the entry list should return -1.
	if got := fp.HitTest(5, 50); got != -1 {
		t.Errorf("expected no hit below entries, got %d", got)
	}
}

// TestFilePicker_ClickSelectsEntry verifies that a left-click on
// an entry row updates the picker's selection. The actual open-file
// action is handled via Enter (or second click, in future work).
func TestFilePicker_ClickSelectsEntry(t *testing.T) {
	th := theme.DefaultDark()

	dir := t.TempDir()
	for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	fp := NewFilePicker(th)
	fp.Open(dir)
	_ = fp.ViewInner(50, 20, 0)

	// Click on the second entry (localY=4).
	fp.selected = 0
	updated, _, _, _ := fp.Update(tea.MouseClickMsg{X: 5, Y: 4, Button: tea.MouseLeft})
	if updated.selected != 1 {
		t.Errorf("expected selected=1 after clicking row 2, got %d", updated.selected)
	}
}

func TestFolderPicker_MouseActionSelectsCurrentFolder(t *testing.T) {
	fp := NewFilePicker(theme.DefaultDark())
	dir := t.TempDir()
	fp.OpenFolder(dir)
	_ = fp.ViewInner(60, 18, 0)

	_, path, action, _ := fp.Update(tea.MouseClickMsg{
		X: fp.actionX0, Y: fp.lastHeight - 1, Button: tea.MouseLeft,
	})
	if action != FilePickerSelect || path != dir {
		t.Fatalf("mouse action = (%v, %q), want select %q", action, path, dir)
	}
}

func TestFilePickerUsesCommandSurfaceComposition(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main"), 0o644); err != nil {
		t.Fatal(err)
	}
	fp := NewFilePicker(theme.DefaultDark())
	fp.Open(dir)
	lines := fp.ViewInner(60, 16, 0)
	plain := rendering.StripAnsi(strings.Join(lines, "\n"))
	if !strings.Contains(plain, "> Search files█") {
		t.Fatalf("missing command-style search prompt:\n%s", plain)
	}
	if !strings.Contains(plain, "2 items") || !strings.Contains(plain, "src") || !strings.Contains(plain, "main.go") {
		t.Fatalf("missing breadcrumb count or entries:\n%s", plain)
	}
	if !strings.Contains(plain, "Attach") || !strings.Contains(plain, "Cancel") {
		t.Fatalf("missing consistent action buttons:\n%s", plain)
	}
	if len(lines) != 16 {
		t.Fatalf("rendered %d lines, want 16", len(lines))
	}
}

func TestFilePickerMouseHoverAndCancel(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	fp := NewFilePicker(theme.DefaultDark())
	fp.Open(dir)
	_ = fp.ViewInner(60, 16, 0)

	updated, _, _, _ := fp.Update(tea.MouseMotionMsg{X: 4, Y: 3})
	if updated.Hover.Index != 0 {
		t.Fatalf("hover index = %d, want 0", updated.Hover.Index)
	}
	updated = fp
	updated, _, action, _ := updated.Update(tea.MouseClickMsg{X: updated.cancelX0, Y: updated.lastHeight - 1, Button: tea.MouseLeft})
	if action != FilePickerCancel {
		t.Fatalf("cancel click action = %v, want FilePickerCancel", action)
	}
}
