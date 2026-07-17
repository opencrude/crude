package widgets

import (
	"testing"

	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/tui/widget"
)

func TestBuildDirtyCloseFrame_PaintsAndFocusesCancel(t *testing.T) {
	th := theme.DefaultDark()
	cw := BuildDirtyCloseFrame(th, "doc.go", 2)
	w, h := cw.Measure(80, 40)
	if w < 50 || h < 6 {
		t.Fatalf("unexpected size %dx%d", w, h)
	}
	surf := surface.NewSurface("dirty", w, h, cell.TransparentBlank())
	cw.Render(surf, &widget.RenderContext{Theme: th, Focused: true})
	buf := surf.Buffer()
	nonBlank := 0
	for x := 0; x < w; x++ {
		c := buf.Get(x, 0)
		if c.Char != 0 && c.Char != ' ' {
			nonBlank++
		}
	}
	if nonBlank == 0 {
		t.Fatal("expected title/border on row 0")
	}
	if cw.Host().Focused() != ConfirmButtonID(2) {
		t.Fatalf("expected Cancel focused, got %q", cw.Host().Focused())
	}
}

func TestBuildTreeDeleteFrame_DefaultNo(t *testing.T) {
	th := theme.DefaultDark()
	cw := BuildTreeDeleteFrame(th, `Delete "x"?`, "/tmp/x", 1)
	if cw.Host().Focused() != ConfirmButtonID(1) {
		t.Fatalf("expected No focused, got %q", cw.Host().Focused())
	}
	// Hit-test Yes button region exists
	w, h := cw.Measure(80, 40)
	cw.Host().Resize(w, h)
	cw.Host().Layout()
	found := false
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if ConfirmButtonIndexFromID(cw.Host().HitTest(x, y)) == 0 {
				found = true
				break
			}
		}
	}
	if !found {
		t.Fatal("expected Yes button hit region")
	}
}

func TestConfirmButtonIndexFromID(t *testing.T) {
	if ConfirmButtonIndexFromID(ConfirmButtonID(0)) != 0 {
		t.Fatal("btn 0")
	}
	if ConfirmButtonIndexFromID(ui.ComponentID("confirm/btn-2")) != 2 {
		t.Fatal("btn 2")
	}
	if ConfirmButtonIndexFromID(ui.ComponentID("other")) != -1 {
		t.Fatal("other")
	}
}
