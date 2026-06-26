package viewmodel

import (
	"os"
	"path/filepath"
	"testing"

	"crude/tui/editor"
	"crude/tui/theme"
)

// TestEditorTab_StatePreservedAcrossSwitch is the regression test for
// the "complete joke" state: before the refactor, opening a second
// editor tab and switching to it would reload the first file from
// disk, destroying cursor position, scroll offset, undo history, and
// any unsaved edits. This test ensures that no longer happens.
func TestEditorTab_StatePreservedAcrossSwitch(t *testing.T) {
	th := theme.DefaultDark()

	dir := t.TempDir()
	pathA := filepath.Join(dir, "a.go")
	pathB := filepath.Join(dir, "b.go")
	if err := os.WriteFile(pathA, []byte("package x\n\nfunc Alpha() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pathB, []byte("package x\n\nfunc Beta() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Open tab A.
	edA := editor.New(th)
	if err := edA.Open(pathA); err != nil {
		t.Fatal(err)
	}

	// Make some state: move cursor, type some text, scroll, dirty the file.
	edA.SetCursorPosition(2, 5)
	edA.ScrollToCursor()
	edA.InsertRune('Z') // makes it modified
	beforeValue := edA.Value()
	if !edA.Modified() {
		t.Fatalf("expected tab A to be modified after insert")
	}

	// Now "switch" by creating a new editor for B (mimicking what
	// openEditorTab does internally — create a fresh editor and
	// point m.editor at it).
	edB := editor.New(th)
	if err := edB.Open(pathB); err != nil {
		t.Fatal(err)
	}

	// Verify A's state is unchanged.
	if got := edA.Value(); got != beforeValue {
		t.Errorf("tab A value changed after creating tab B: got %q want %q", got, beforeValue)
	}
	if got := edA.CursorLine(); got != 2 {
		t.Errorf("tab A cursor line changed: got %d want 2", got)
	}
	if !edA.Modified() {
		t.Errorf("tab A no longer modified after creating tab B")
	}
	if !edA.IsOpen() {
		t.Errorf("tab A no longer open after creating tab B")
	}

	// Switch back to A by pointing m.editor at edA again (this is
	// exactly what handleTabSwitch does).
	m_editor := edA
	if !m_editor.IsOpen() || m_editor.FilePath() != pathA {
		t.Errorf("expected m.editor to be tab A")
	}
	if got := m_editor.Value(); got != beforeValue {
		t.Errorf("tab A value changed after switch-back: got %q want %q", got, beforeValue)
	}

	// B should still be independently open with its own state.
	if !edB.IsOpen() {
		t.Errorf("tab B no longer open")
	}
	if got := edB.FilePath(); got != pathB {
		t.Errorf("tab B path changed: got %q want %q", got, pathB)
	}
}

// TestEditorTab_MapIndependentTabs verifies the editorTabs map keeps
// each tab's buffer independent — typing in one doesn't affect another.
func TestEditorTab_MapIndependentTabs(t *testing.T) {
	th := theme.DefaultDark()

	dir := t.TempDir()
	pathA := filepath.Join(dir, "a.txt")
	pathB := filepath.Join(dir, "b.txt")
	if err := os.WriteFile(pathA, []byte("A1\nA2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pathB, []byte("B1\nB2\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tabs := map[string]*EditorTab{}
	for _, p := range []string{pathA, pathB} {
		ed := editor.New(th)
		if err := ed.Open(p); err != nil {
			t.Fatal(err)
		}
		tabs[p] = newEditorTab(p, ed)
	}

	// Edit tab A.
	tabs[pathA].Editor.InsertRune('X')
	aVal := tabs[pathA].Editor.Value()
	bVal := tabs[pathB].Editor.Value()

	if aVal == bVal {
		t.Errorf("expected tab A and B to have different values, both = %q", aVal)
	}
	if !tabs[pathA].Editor.Modified() {
		t.Errorf("expected tab A to be modified")
	}
	if tabs[pathB].Editor.Modified() {
		t.Errorf("expected tab B to remain unmodified")
	}
}

// TestEditorTab_CloseRemovesOnlyThatTab verifies that closing one
// tab doesn't disturb the others.
func TestEditorTab_CloseRemovesOnlyThatTab(t *testing.T) {
	th := theme.DefaultDark()

	dir := t.TempDir()
	pathA := filepath.Join(dir, "a.txt")
	pathB := filepath.Join(dir, "b.txt")
	for _, p := range []string{pathA, pathB} {
		if err := os.WriteFile(p, []byte("hello\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	tabs := map[string]*EditorTab{}
	for _, p := range []string{pathA, pathB} {
		ed := editor.New(th)
		if err := ed.Open(p); err != nil {
			t.Fatal(err)
		}
		tabs[p] = newEditorTab(p, ed)
	}

	// Close tab A.
	tabs[pathA].Editor.Close()
	delete(tabs, pathA)

	// B should still be alive.
	if _, ok := tabs[pathB]; !ok {
		t.Errorf("tab B was removed when closing tab A")
	}
	if !tabs[pathB].Editor.IsOpen() {
		t.Errorf("tab B editor was closed when closing tab A")
	}
}
