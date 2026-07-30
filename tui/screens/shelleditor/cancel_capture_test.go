package shelleditor

import (
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/editor"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/tui/widget"
)

func TestScreenCancelPointerCapture_ForwardsToEditor(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.txt")
	if err := os.WriteFile(path, []byte("a\nb\nc\nd\ne\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	th := theme.DefaultDark()
	ed := editor.New(th)
	if err := ed.Open(path); err != nil {
		t.Fatal(err)
	}
	// Establish view geometry so mouseToPosition works.
	surf := surface.NewSurface("geom", 40, 12, cell.TransparentBlank())
	ed.Render(surf, &widget.RenderContext{Theme: th, Focused: true})

	_ = ed.HandleEvent(tea.MouseClickMsg{X: 5, Y: 0, Button: tea.MouseLeft})
	_ = ed.HandleEvent(tea.MouseMotionMsg{X: 5, Y: 2, Button: tea.MouseLeft})
	if !ed.IsMouseDragging() {
		t.Fatal("setup: expected mouse drag")
	}

	screen := New(th)
	screen.Bind(ed)
	_ = ui.NewHost(th, screen) // ensure Screen remains a Host root candidate

	screen.CancelPointerCapture()
	if ed.IsMouseDragging() {
		t.Fatal("Screen.CancelPointerCapture must clear editor mouseDragging")
	}
	if ed.HasSelection() {
		t.Fatal("Screen.CancelPointerCapture must drop in-progress mouse selection")
	}
}
