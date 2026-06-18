package wm

import (
	"testing"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
)

func TestDrawBackground(t *testing.T) {
	buf := surface.NewCellBuffer(5, 4)
	bg := cell.NewTrueColor(20, 30, 40)
	DrawBackground(buf, bg)

	for y := 0; y < 4; y++ {
		for x := 0; x < 5; x++ {
			c := buf.Get(x, y)
			if c.Char != ' ' {
				t.Errorf("cell(%d,%d) char = %q, want ' '", x, y, c.Char)
			}
			if !c.BG.Equal(bg) {
				t.Errorf("cell(%d,%d) BG = %v, want %v", x, y, c.BG, bg)
			}
		}
	}
}

func TestDrawBorderSingle(t *testing.T) {
	buf := surface.NewCellBuffer(6, 4)
	fg := cell.NewTrueColor(255, 0, 0)
	bg := cell.NoColor
	DrawBorder(buf, BorderSingle, fg, bg)

	chars := BorderCharsFor(BorderSingle)
	// Corners
	if c := buf.Get(0, 0); c.Char != chars.TopLeft {
		t.Errorf("top-left = %q, want %q", c.Char, chars.TopLeft)
	}
	if c := buf.Get(5, 0); c.Char != chars.TopRight {
		t.Errorf("top-right = %q, want %q", c.Char, chars.TopRight)
	}
	if c := buf.Get(0, 3); c.Char != chars.BottomLeft {
		t.Errorf("bottom-left = %q, want %q", c.Char, chars.BottomLeft)
	}
	if c := buf.Get(5, 3); c.Char != chars.BottomRight {
		t.Errorf("bottom-right = %q, want %q", c.Char, chars.BottomRight)
	}
	// Horizontal borders
	if c := buf.Get(1, 0); c.Char != chars.Horizontal {
		t.Errorf("top horizontal = %q, want %q", c.Char, chars.Horizontal)
	}
	if c := buf.Get(1, 3); c.Char != chars.Horizontal {
		t.Errorf("bottom horizontal = %q, want %q", c.Char, chars.Horizontal)
	}
	// Vertical borders
	if c := buf.Get(0, 1); c.Char != chars.Vertical {
		t.Errorf("left vertical = %q, want %q", c.Char, chars.Vertical)
	}
	if c := buf.Get(5, 1); c.Char != chars.Vertical {
		t.Errorf("right vertical = %q, want %q", c.Char, chars.Vertical)
	}
	// Inside (should be transparent/blank, not overwritten)
	if c := buf.Get(2, 1); c.Char != 0 && c.Char != ' ' {
		t.Errorf("interior = %q, want blank", c.Char)
	}
}

func TestDrawBorderRounded(t *testing.T) {
	buf := surface.NewCellBuffer(6, 4)
	fg := cell.NewTrueColor(255, 0, 0)
	bg := cell.NoColor
	DrawBorder(buf, BorderRounded, fg, bg)

	chars := BorderCharsFor(BorderRounded)
	if c := buf.Get(0, 0); c.Char != chars.TopLeft {
		t.Errorf("top-left = %q, want %q", c.Char, chars.TopLeft)
	}
	if c := buf.Get(0, 3); c.Char != chars.BottomLeft {
		t.Errorf("bottom-left = %q, want %q", c.Char, chars.BottomLeft)
	}
}

func TestDrawBorderLShape(t *testing.T) {
	buf := surface.NewCellBuffer(6, 4)
	fg := cell.NewTrueColor(100, 100, 100)
	bg := cell.NoColor

	DrawLBorder(buf, fg, bg)

	// Bottom-left corner
	if c := buf.Get(0, 3); c.Char != '└' {
		t.Errorf("bottom-left = %q, want '└'", c.Char)
	}
	// Bottom-right (horizontal end)
	if c := buf.Get(5, 3); c.Char != '─' {
		t.Errorf("bottom-right = %q, want '─'", c.Char)
	}
	// Bottom horizontal
	if c := buf.Get(1, 3); c.Char != '─' {
		t.Errorf("bottom horizontal = %q, want '─'", c.Char)
	}
	// Left vertical (row 1, not row 0 which is title bar)
	if c := buf.Get(0, 1); c.Char != '│' {
		t.Errorf("left vertical = %q, want '│'", c.Char)
	}
}

func TestDrawBorderNone(t *testing.T) {
	buf := surface.NewCellBuffer(6, 4)
	fg := cell.NewTrueColor(255, 0, 0)
	bg := cell.NoColor
	DrawBorder(buf, BorderNone, fg, bg)

	// BorderNone should be a no-op
	for y := 0; y < 4; y++ {
		for x := 0; x < 6; x++ {
			c := buf.Get(x, y)
			if c.Char != 0 && c.Char != ' ' {
				t.Errorf("BorderNone left cell(%d,%d) = %q, want blank", x, y, c.Char)
			}
		}
	}
}

func TestDrawBorderTooSmall(t *testing.T) {
	buf := surface.NewCellBuffer(1, 1)
	fg := cell.NewTrueColor(255, 0, 0)
	bg := cell.NoColor
	DrawBorder(buf, BorderSingle, fg, bg)

	chars := BorderCharsFor(BorderSingle)
	if c := buf.Get(0, 0); c.Char != chars.TopLeft {
		t.Errorf("tiny buffer top-left = %q, want %q", c.Char, chars.TopLeft)
	}
}

func TestDrawTitleBarBorderSingle(t *testing.T) {
	buf := surface.NewCellBuffer(10, 5)
	t.Cleanup(func() {})
	fg := cell.NewTrueColor(200, 200, 200)
	bg := cell.NewTrueColor(50, 50, 50)
	DrawTitleBar(buf, "Test", AlignLeft, true, fg, bg, 10, BorderSingle)

	// Title bar between border chars (x=1 to x=8), text starts at x=2
	// Close button at x=6,7,8 (w-4, w-3, w-2)
	// "Test" should be at x=2 (left aligned, after left pad and left border)
	if c := buf.Get(2, 0); c.Char != 'T' {
		t.Errorf("title char at (2,0) = %q, want 'T'", c.Char)
	}
	if c := buf.Get(3, 0); c.Char != 'e' {
		t.Errorf("title char at (3,0) = %q, want 'e'", c.Char)
	}
	if c := buf.Get(4, 0); c.Char != 's' {
		t.Errorf("title char at (4,0) = %q, want 's'", c.Char)
	}
	if c := buf.Get(5, 0); c.Char != 't' {
		t.Errorf("title char at (5,0) = %q, want 't'", c.Char)
	}
}

func TestDrawTitleBarAlignCenter(t *testing.T) {
	buf := surface.NewCellBuffer(12, 5)
	fg := cell.NewTrueColor(200, 200, 200)
	bg := cell.NewTrueColor(50, 50, 50)
	DrawTitleBar(buf, "Title", AlignCenter, false, fg, bg, 12, BorderSingle)

	// Title sits between x=1 and x=10, left pad at x=2
	// Available width: 10 (12-2), closeW=0, titleAreaW = 10-1 = 9
	// "Title" has 5 chars, centered: (9-5)/2 = 2, so starts at textStartX+2 = 2+2 = 4
	if c := buf.Get(4, 0); c.Char != 'T' {
		t.Errorf("centered title at (4,0) = %q, want 'T'", c.Char)
	}
}

func TestDrawTitleBarAlignRight(t *testing.T) {
	buf := surface.NewCellBuffer(14, 5)
	fg := cell.NewTrueColor(200, 200, 200)
	bg := cell.NewTrueColor(50, 50, 50)
	DrawTitleBar(buf, "Name", AlignRight, false, fg, bg, 14, BorderSingle)

	// Title sits between x=1 and x=12, left pad at x=2, availW=12, titleAreaW=11
	// "Name" has 4 chars, right-aligned: starts at 2 + 11 - 4 = 9
	if c := buf.Get(9, 0); c.Char != 'N' {
		t.Errorf("right-aligned title at (9,0) = %q, want 'N'", c.Char)
	}
}

func TestDrawTitleBarTruncated(t *testing.T) {
	buf := surface.NewCellBuffer(8, 5)
	fg := cell.NewTrueColor(200, 200, 200)
	bg := cell.NewTrueColor(50, 50, 50)
	DrawTitleBar(buf, "VeryLongTitle", AlignLeft, true, fg, bg, 8, BorderSingle)

	// titleW=8, availW=6 (8-2 for borders), closeW=3, titleAreaW = 6-3-1 = 2
	// Should be truncated to "…" or "V…"
	hasEllipsis := false
	for x := 1; x < 5; x++ {
		if c := buf.Get(x, 0); c.Char == '…' {
			hasEllipsis = true
		}
	}
	if !hasEllipsis {
		t.Error("truncated title should contain '…'")
	}
}

func TestDrawTitleBarBorderNone(t *testing.T) {
	buf := surface.NewCellBuffer(10, 5)
	fg := cell.NewTrueColor(200, 200, 200)
	bg := cell.NewTrueColor(50, 50, 50)
	DrawTitleBar(buf, "Test", AlignLeft, true, fg, bg, 10, BorderNone)

	// For BorderNone: title bar spans full width (0-9), text starts at x=1
	// Close button at x=7,8,9 (w-3, w-2, w-1)
	if c := buf.Get(1, 0); c.Char != 'T' {
		t.Errorf("BorderNone title at (1,0) = %q, want 'T'", c.Char)
	}
}

func TestDrawTitleBarBorderLShape(t *testing.T) {
	buf := surface.NewCellBuffer(10, 5)
	fg := cell.NewTrueColor(200, 200, 200)
	bg := cell.NewTrueColor(50, 50, 50)
	DrawTitleBar(buf, "Test", AlignLeft, true, fg, bg, 10, BorderLShape)

	// For LShape: title bar from x=1 to x=9, text starts at x=2
	// Close button at x=7,8,9 (w-3, w-2, w-1)
	if c := buf.Get(2, 0); c.Char != 'T' {
		t.Errorf("LShape title at (2,0) = %q, want 'T'", c.Char)
	}
}

func TestDrawTitleBarTooSmall(t *testing.T) {
	buf := surface.NewCellBuffer(3, 5)
	fg := cell.NewTrueColor(200, 200, 200)
	bg := cell.NewTrueColor(50, 50, 50)
	DrawTitleBar(buf, "X", AlignLeft, false, fg, bg, 3, BorderSingle)

	// titleW < 4, should be no-op
	for x := 0; x < 3; x++ {
		c := buf.Get(x, 0)
		if c.Char != 0 && c.Char != ' ' {
			t.Errorf("tiny title bar cell (%d,0) = %q, want blank", x, c.Char)
		}
	}
}

func TestDrawCloseButtonBorderSingle(t *testing.T) {
	buf := surface.NewCellBuffer(10, 5)
	fg := cell.NewTrueColor(255, 0, 0)
	bg := cell.NewTrueColor(50, 50, 50)
	DrawCloseButton(buf, 10, fg, bg, BorderSingle)

	// For bordered: button at (w-4, w-3, w-2) = (6,7,8)
	if c := buf.Get(6, 0); c.Char != ' ' {
		t.Errorf("close btn left pad at 6 = %q, want ' '", c.Char)
	}
	if c := buf.Get(7, 0); c.Char != '✕' {
		t.Errorf("close btn char at 7 = %q, want '✕'", c.Char)
	}
	if c := buf.Get(8, 0); c.Char != ' ' {
		t.Errorf("close btn right pad at 8 = %q, want ' '", c.Char)
	}
}

func TestDrawCloseButtonBorderNone(t *testing.T) {
	buf := surface.NewCellBuffer(10, 5)
	fg := cell.NewTrueColor(255, 0, 0)
	bg := cell.NewTrueColor(50, 50, 50)
	DrawCloseButton(buf, 10, fg, bg, BorderNone)

	// For BorderNone: button at (w-3, w-2, w-1) = (7,8,9)
	if c := buf.Get(7, 0); c.Char != ' ' {
		t.Errorf("close btn left pad at 7 = %q, want ' '", c.Char)
	}
	if c := buf.Get(8, 0); c.Char != '✕' {
		t.Errorf("close btn char at 8 = %q, want '✕'", c.Char)
	}
	if c := buf.Get(9, 0); c.Char != ' ' {
		t.Errorf("close btn right pad at 9 = %q, want ' '", c.Char)
	}
}

func TestDrawCloseButtonBorderLShape(t *testing.T) {
	buf := surface.NewCellBuffer(10, 5)
	fg := cell.NewTrueColor(255, 0, 0)
	bg := cell.NewTrueColor(50, 50, 50)
	DrawCloseButton(buf, 10, fg, bg, BorderLShape)

	// For BorderLShape: same as BorderNone — (w-3, w-2, w-1) = (7,8,9)
	if c := buf.Get(8, 0); c.Char != '✕' {
		t.Errorf("LShape close btn at 8 = %q, want '✕'", c.Char)
	}
}

func TestRenderDecorationFocusedColors(t *testing.T) {
	fw := NewFloatingWindow("dec-foc", "Test", testWidget{}, 12, 6,
		WithBorderStyle(BorderSingle))
	fw.Focus()

	surf := surface.NewSurface("test", 12, 6, cell.TransparentBlank())
	th := testTheme()
	RenderDecoration(fw, surf, th)

	buf := surf.Buffer()

	expectedBorderFg := rendering.ThemeColorToCell(th.Colors.Border)
	topLeft := buf.Get(0, 0)
	if !topLeft.FG.Equal(expectedBorderFg) {
		t.Errorf("focused border FG = %v, want %v", topLeft.FG, expectedBorderFg)
	}

	expectedTitleFg := rendering.ThemeColorToCell(th.Colors.Fg)
	if title := buf.Get(2, 0); !title.FG.Equal(expectedTitleFg) {
		t.Errorf("focused title FG = %v, want %v", title.FG, expectedTitleFg)
	}
}

func TestRenderDecorationUnfocusedColors(t *testing.T) {
	fw := NewFloatingWindow("dec-unfoc", "Test", testWidget{}, 12, 6,
		WithBorderStyle(BorderSingle))

	surf := surface.NewSurface("test", 12, 6, cell.TransparentBlank())
	th := testTheme()
	RenderDecoration(fw, surf, th)

	buf := surf.Buffer()

	expectedBorderFg := rendering.ThemeColorToCell(th.Colors.Border)
	topLeft := buf.Get(0, 0)
	if !topLeft.FG.Equal(expectedBorderFg) {
		t.Errorf("unfocused border FG = %v, want %v", topLeft.FG, expectedBorderFg)
	}
}

func TestRenderDecorationBorderNone(t *testing.T) {
	fw := NewFloatingWindow("dec-bn", "Test", testWidget{}, 10, 5,
		WithBorderStyle(BorderNone))

	surf := surface.NewSurface("test", 10, 5, cell.TransparentBlank())
	th := testTheme()
	RenderDecoration(fw, surf, th)

	buf := surf.Buffer()
	// Should have body background fill
	bodyBg := rendering.ThemeColorToCell(th.Colors.SubPaneBg)
	if c := buf.Get(0, 0); !c.BG.Equal(bodyBg) {
		t.Errorf("BorderNone fill BG = %v, want %v", c.BG, bodyBg)
	}
}

func TestRenderDecorationLShape(t *testing.T) {
	fw := NewFloatingWindow("dec-ls", "Test", testWidget{}, 10, 5,
		WithBorderStyle(BorderLShape))

	surf := surface.NewSurface("test", 10, 5, cell.TransparentBlank())
	th := testTheme()
	RenderDecoration(fw, surf, th)

	buf := surf.Buffer()
	// Bottom-left corner
	if c := buf.Get(0, 4); c.Char != '└' {
		t.Errorf("LShape bottom-left = %q, want '└'", c.Char)
	}
}

func TestRenderDecorationWithCloseButton(t *testing.T) {
	fw := NewFloatingWindow("dec-cb", "Test", testWidget{}, 12, 6,
		WithBorderStyle(BorderSingle))
	fw.Focus()

	surf := surface.NewSurface("test", 12, 6, cell.TransparentBlank())
	th := testTheme()
	RenderDecoration(fw, surf, th)

	buf := surf.Buffer()
	// Close button at x=8 (w-4 = 12-4 = 8)
	if c := buf.Get(9, 0); c.Char != '✕' {
		t.Errorf("close button char = %q, want '✕'", c.Char)
	}
}

func TestRenderDecorationNoCloseButton(t *testing.T) {
	fw := NewFloatingWindow("dec-ncb", "Test", testWidget{}, 12, 6,
		WithClosable(false))

	surf := surface.NewSurface("test", 12, 6, cell.TransparentBlank())
	th := testTheme()
	RenderDecoration(fw, surf, th)

	buf := surf.Buffer()
	// Should not have close button
	for x := 0; x < 12; x++ {
		if c := buf.Get(x, 0); c.Char == '✕' {
			t.Errorf("unexpected close button at x=%d", x)
		}
	}
}

func TestRenderDecorationNilTheme(t *testing.T) {
	fw := NewFloatingWindow("dec-nil", "Test", testWidget{}, 10, 5)
	surf := surface.NewSurface("test", 10, 5, cell.TransparentBlank())
	RenderDecoration(fw, surf, nil)
}

func TestCollectWindowColorsFocused(t *testing.T) {
	fw := NewFloatingWindow("cc-foc", "Test", testWidget{}, 10, 5)
	fw.Focus()
	th := testTheme()
	wc := collectWindowColors(fw, th)

	expectedBorder := rendering.ThemeColorToCell(th.Colors.Border)
	if !wc.borderFg.Equal(expectedBorder) {
		t.Error("focused borderFg should be border color (same as unfocused)")
	}
	expectedFg := rendering.ThemeColorToCell(th.Colors.Fg)
	if !wc.titleFg.Equal(expectedFg) {
		t.Error("focused titleFg should be Fg")
	}
}

func TestCollectWindowColorsUnfocused(t *testing.T) {
	fw := NewFloatingWindow("cc-unfoc", "Test", testWidget{}, 10, 5)
	th := testTheme()
	wc := collectWindowColors(fw, th)

	expectedBorder := rendering.ThemeColorToCell(th.Colors.Border)
	if !wc.borderFg.Equal(expectedBorder) {
		t.Error("unfocused borderFg should be border color")
	}
}

func TestDrawLBorderTooSmall(t *testing.T) {
	buf := surface.NewCellBuffer(1, 2)
	fg := cell.NewTrueColor(100, 100, 100)
	bg := cell.NoColor
	DrawLBorder(buf, fg, bg)

	for y := 0; y < 2; y++ {
		for x := 0; x < 1; x++ {
			c := buf.Get(x, y)
			if c.Char != 0 && c.Char != ' ' {
				t.Errorf("too-small LShape cell(%d,%d) = %q, want blank", x, y, c.Char)
			}
		}
	}
}

func TestDrawTitleBarNoCloseButton(t *testing.T) {
	buf := surface.NewCellBuffer(10, 5)
	fg := cell.NewTrueColor(200, 200, 200)
	bg := cell.NewTrueColor(50, 50, 50)
	DrawTitleBar(buf, "Hello", AlignLeft, false, fg, bg, 10, BorderSingle)

	// Without close button, title has more space
	// textStartX = 2, titleAreaW = 10-2-1 = 7
	if c := buf.Get(2, 0); c.Char != 'H' {
		t.Errorf("title char = %q, want 'H'", c.Char)
	}
}
