package wm

import (
	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/theme"
)

// RenderDecoration draws borders, title bar, and close button into the
// window surface. This is called by FloatingWindow.Render when the window
// is decorated.
func RenderDecoration(fw *FloatingWindow, surf *surface.Surface, t *theme.Theme) {
	if t == nil {
		return
	}

	// Determine colors from the theme.
	borderFg := cell.NewTrueColor(0x30, 0x36, 0x3d) // theme border color
	borderBg := cell.NoColor
	titleFg := cell.NewTrueColor(0xe6, 0xed, 0xf3) // bright foreground for title

	// If the window is focused, use accent colors for the title bar.
	if fw.focused {
		borderFg = cell.NewTrueColor(0xf0, 0x88, 0x3e) // accent
		titleFg = cell.NewTrueColor(0xf0, 0x88, 0x3e)
	}

	// Draw the border around the perimeter.
	DrawBorder(surf.Buffer(), fw.borderStyle, borderFg, borderBg)

	// Draw the title bar text in the top border row.
	DrawTitleBar(surf.Buffer(), fw.Title, fw.titleAlign, fw.closable, titleFg, borderBg, fw.w)

	// If closable, draw the close button.
	if fw.closable {
		DrawCloseButton(surf.Buffer(), fw.w, titleFg, borderBg)
	}
}

// DrawBorder draws a border around the perimeter of a CellBuffer using
// the specified border style and colors.
//
// The border occupies the outermost row/column of the buffer:
//   - Corners at (0,0), (w-1,0), (0,h-1), (w-1,h-1)
//   - Horizontal borders on rows 0 and h-1
//   - Vertical borders on columns 0 and w-1
func DrawBorder(buf *surface.CellBuffer, style BorderStyle, fg, bg cell.Color) {
	chars := BorderCharsFor(style)
	w := buf.Width()
	h := buf.Height()

	if w < 2 || h < 2 {
		// Too small to draw a border; just set corners if possible.
		if w >= 1 && h >= 1 {
			buf.Set(0, 0, cell.NewCell(chars.TopLeft, fg, bg, 0))
		}
		return
	}

	// Corners.
	buf.Set(0, 0, cell.NewCell(chars.TopLeft, fg, bg, 0))
	buf.Set(w-1, 0, cell.NewCell(chars.TopRight, fg, bg, 0))
	buf.Set(0, h-1, cell.NewCell(chars.BottomLeft, fg, bg, 0))
	buf.Set(w-1, h-1, cell.NewCell(chars.BottomRight, fg, bg, 0))

	// Top and bottom horizontal borders.
	horizCell := cell.NewCell(chars.Horizontal, fg, bg, 0)
	for x := 1; x < w-1; x++ {
		buf.Set(x, 0, horizCell)
		buf.Set(x, h-1, horizCell)
	}

	// Left and right vertical borders.
	vertCell := cell.NewCell(chars.Vertical, fg, bg, 0)
	for y := 1; y < h-1; y++ {
		buf.Set(0, y, vertCell)
		buf.Set(w-1, y, vertCell)
	}
}

// DrawTitleBar draws a title in the top border row (y=0) of a CellBuffer,
// between the left border character (x=1) and the right border character
// (x=titleW-2).
//
// If closable, 3 cells are reserved at the right for the close button.
// The title is aligned according to the given alignment.
// If the title is too long for the available space, it is truncated with "…".
// Remaining space is filled with the horizontal border character.
func DrawTitleBar(buf *surface.CellBuffer, title string, align TextAlign, closable bool, fg, bg cell.Color, titleW int) {
	// If titleW is too small, skip title drawing.
	if titleW < 4 {
		return
	}

	// Available width for title + close button.
	availW := titleW - 2 // exclude left and right border chars

	// Reserve space for the close button if closable.
	closeW := 0
	if closable {
		closeW = 3 // " ✕ "
	}

	// The title area is between x=1 and x=titleW-2 (exclusive of borders).
	// The close button occupies the rightmost closeW cells of the title area.
	titleAreaW := availW - closeW
	if titleAreaW < 1 {
		return
	}

	// Fill the title bar area with spaces (the border chars have already been
	// drawn by DrawBorder; we overwrite the inner part of the top row).
	for x := 1; x < titleW-1; x++ {
		buf.Set(x, 0, cell.NewCell(' ', fg, bg, 0))
	}

	// Truncate title if too long.
	titleText := title
	titleRunes := []rune(titleText)
	if len(titleRunes) > titleAreaW {
		if titleAreaW > 1 {
			titleRunes = titleRunes[:titleAreaW-1]
			titleText = string(titleRunes) + "…"
		} else {
			titleText = "…"
		}
		titleRunes = []rune(titleText)
	}

	// Ensure title fits.
	if len(titleRunes) > titleAreaW {
		titleRunes = titleRunes[:titleAreaW]
		titleText = string(titleRunes)
	}

	// Calculate title start position based on alignment.
	titleLen := len(titleRunes)
	var startX int
	switch align {
	case AlignCenter:
		startX = 1 + (titleAreaW-titleLen)/2
	case AlignRight:
		startX = 1 + titleAreaW - titleLen
	default: // AlignLeft
		startX = 1
	}

	// Draw the title text.
	for i, r := range titleRunes {
		x := startX + i
		if x >= 1 && x < titleW-1-closeW || (!closable && x < titleW-1) {
			buf.Set(x, 0, cell.NewCell(r, fg, bg, cell.AttrBold))
		}
	}
}

// DrawCloseButton draws a close button in the title bar at the right side.
// The button is represented as " ✕ " (space, multiplication X, space).
// The x parameter is the buffer width; the close button is placed at
// positions (x-4, x-3, x-2) within row 0.
func DrawCloseButton(buf *surface.CellBuffer, x int, fg, bg cell.Color) {
	// Close button at positions x-4, x-3, x-2 (leaving room for right border at x-1)
	startX := x - 4
	if startX < 1 {
		startX = 1
	}
	buf.Set(startX, 0, cell.NewCell(' ', fg, bg, 0))
	buf.Set(startX+1, 0, cell.NewCell('✕', fg, bg, 0))
	buf.Set(startX+2, 0, cell.NewCell(' ', fg, bg, 0))
}
