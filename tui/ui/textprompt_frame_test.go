package ui

import (
	"testing"

	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/wm"
)

func TestBuildTextPromptFrame_PaintFillsSurface(t *testing.T) {
	th := theme.DefaultDark()
	st := NewTextPromptState(TextPromptNewFile, "New file", "Create in /tmp", "")
	cw := BuildTextPromptFrame(th, st)
	w, h := cw.Measure(80, 24)
	if w < 40 || h < 5 {
		t.Fatalf("unexpected measure %dx%d", w, h)
	}
	surf := surface.NewSurface("prompt", w, h, cell.TransparentBlank())
	ctx := &wm.RenderContext{Theme: th, Focused: true}
	cw.Render(surf, ctx)
	// Title bar should have non-blank cells
	buf := surf.Buffer()
	nonBlank := 0
	for x := 0; x < w; x++ {
		c := buf.Get(x, 0)
		if c.Char != 0 && c.Char != ' ' {
			nonBlank++
		}
	}
	if nonBlank == 0 {
		t.Fatal("expected title/border cells on row 0")
	}
}
