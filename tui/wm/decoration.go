package wm

import (
	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/theme"
)

// windowColors holds all color decisions for a single decoration pass.
// It is populated from the theme before rendering begins.
type windowColors struct {
	borderFg   cell.Color
	borderBg   cell.Color
	titleFg    cell.Color
	titleBg    cell.Color
	bodyBg     cell.Color
	closeFg    cell.Color
}

func collectWindowColors(fw *FloatingWindow, t *theme.Theme) windowColors {
	if fw.focused {
		return windowColors{
			borderFg: rendering.ThemeColorToCell(t.Colors.Border),
			borderBg: cell.NoColor,
			titleFg:  rendering.ThemeColorToCell(t.Colors.Fg),
			titleBg:  rendering.ThemeColorToCell(t.Colors.SubPaneBg),
			bodyBg:   rendering.ThemeColorToCell(t.Colors.SubPaneBg),
			closeFg:  rendering.ThemeColorToCell(t.Colors.Error),
		}
	}
	return windowColors{
		borderFg: rendering.ThemeColorToCell(t.Colors.Border),
		borderBg: cell.NoColor,
		titleFg:  rendering.ThemeColorToCell(t.Colors.Muted),
		titleBg:  rendering.ThemeColorToCell(t.Colors.SubPaneBg),
		bodyBg:   rendering.ThemeColorToCell(t.Colors.SubPaneBg),
		closeFg:  rendering.ThemeColorToCell(t.Colors.Muted),
	}
}

// RenderDecoration draws background, title bar, and close button into the
// window surface. This is called by FloatingWindow.Render when the window
// is decorated.
//
// For BorderNone windows: the background fill IS the visual boundary —
// no border characters are drawn. The title bar spans the full width and
// the close button sits at the right edge.
//
// For bordered windows (BorderSingle, BorderRounded, etc.): the traditional
// border characters are drawn around the perimeter, and the title bar and
// close button sit between the border chars.
func RenderDecoration(fw *FloatingWindow, surf *surface.Surface, t *theme.Theme) {
	if t == nil {
		return
	}

	wc := collectWindowColors(fw, t)

	// For BorderNone: fill the entire window surface with background color.
	// The background IS the visual boundary — no border chars needed.
	switch fw.borderStyle {
	case BorderNone:
		DrawBackground(surf.Buffer(), wc.bodyBg)
	case BorderLShape:
		DrawBackground(surf.Buffer(), wc.bodyBg)
		// L-border uses body bg for both fg and bg — the border lines are
		// structural only (column/row reserved for layout), not visually colored.
		// The background fill IS the visual boundary.
		DrawLBorder(surf.Buffer(), wc.bodyBg, wc.bodyBg, fw.x)
	default:
		// For bordered windows, draw the border around the perimeter.
		DrawBorder(surf.Buffer(), fw.borderStyle, wc.borderFg, wc.borderBg, fw.x)
	}

	// Draw the title bar text in the top row.
	DrawTitleBar(surf.Buffer(), fw.Title, fw.titleAlign, fw.closable, wc.titleFg, wc.titleBg, fw.w, fw.borderStyle, fw.x)

	// If closable, draw the close button.
	if fw.closable {
		DrawCloseButton(surf.Buffer(), fw.w, wc.closeFg, wc.titleBg, fw.borderStyle)
	}
}

// DrawBackground fills the entire window surface with a background color.
// This provides the visual boundary for BorderNone windows — the bg color
// defines the window's extent without any border characters.
func DrawBackground(buf *surface.CellBuffer, bg cell.Color) {
	w := buf.Width()
	h := buf.Height()
	bgCell := cell.NewCell(' ', cell.NoColor, bg, 0)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			buf.Set(x, y, bgCell)
		}
	}
}

// DrawLBorder draws an L-shaped border (left + bottom edges only) with the
// given foreground color on top of the existing background fill.
// The left vertical goes from row 1 to row h-2 (between title bar and bottom).
// The bottom horizontal spans the full width. Corners are drawn at bottom-left
// and bottom-right.
func DrawLBorder(buf *surface.CellBuffer, fg, bg cell.Color, screenX int) {
	w := buf.Width()
	h := buf.Height()

	if w < 2 || h < 3 {
		return // Too small for L-border + title bar
	}

	rendering.Log("DrawLBorder screenCols: left=%d bottomRight=%d w=%d h=%d",
		screenX, screenX+w-1, w, h)

	// Bottom-left corner
	buf.Set(0, h-1, cell.NewCell('└', fg, bg, 0))
	// Bottom-right: just end the horizontal line (no right vertical meets it)
	buf.Set(w-1, h-1, cell.NewCell('─', fg, bg, 0))

	// Bottom horizontal (between corners)
	horizCell := cell.NewCell('─', fg, bg, 0)
	for x := 1; x < w-1; x++ {
		buf.Set(x, h-1, horizCell)
	}

	// Left vertical (from row 1 to row h-2, between title bar and bottom)
	vertCell := cell.NewCell('│', fg, bg, 0)
	for y := 1; y < h-1; y++ {
		buf.Set(0, y, vertCell)
	}
}

// DrawBorder draws a border around the perimeter of a CellBuffer using
// the specified border style and colors.
//
// For BorderNone, this is a no-op — the background fill handles the visual
// boundary instead.
//
// For other styles, the border occupies the outermost row/column:
//   - Corners at (0,0), (w-1,0), (0,h-1), (w-1,h-1)
//   - Horizontal borders on rows 0 and h-1
//   - Vertical borders on columns 0 and w-1
func DrawBorder(buf *surface.CellBuffer, style BorderStyle, fg, bg cell.Color, screenX int) {
	if style == BorderNone {
		return // No border chars drawn — background fill is the visual boundary
	}

	chars := BorderCharsFor(style)
	w := buf.Width()
	h := buf.Height()

	rendering.Log("DrawBorder style=%v screenCols: left=%d top=%d right=%d bottom=%d w=%d h=%d",
		style, screenX, screenX, screenX+w-1, screenX+h-1, w, h)

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

// DrawTitleBar draws a title in the top row (y=0) of a CellBuffer.
//
// For BorderNone: the title bar spans the FULL width (x=0 to x=titleW-1).
// The title text starts at x=1 (1 cell left padding). 3 cells are reserved
// at the right for the close button if closable.
//
// For bordered windows: the title bar sits between the left and right border
// characters (x=1 to x=titleW-2), with 1-cell left padding starting at x=2.
// 3 cells are reserved at the right for the close button if closable.
//
// If the title is too long for the available space, it is truncated with "…".
// The title row is filled with the title bar background color.
func DrawTitleBar(buf *surface.CellBuffer, title string, align TextAlign, closable bool, fg, bg cell.Color, titleW int, borderStyle BorderStyle, screenX int) {
	// If titleW is too small, skip title drawing.
	if titleW < 4 {
		return
	}

	screenStartX := screenX
	switch borderStyle {
	case BorderNone:
		screenStartX = screenX + 0
	case BorderLShape:
		screenStartX = screenX + 1
	default:
		screenStartX = screenX + 1
	}
	rendering.Log("DrawTitleBar title=%q titleW=%d borderStyle=%v closable=%v screenStartX=%d",
		title, titleW, borderStyle, closable, screenStartX)

	var startX, endX int // title bar fill range (inclusive)
	var textStartX int  // where title text begins (after left pad)
	var availW int      // available width for title + close button

	if borderStyle == BorderNone {
		// Title bar spans the full width — no border chars.
		startX = 0
		endX = titleW - 1
		availW = titleW
		textStartX = 1 // left pad
	} else if borderStyle == BorderLShape {
		// Title bar between left border and right edge (no right border).
		startX = 1      // after left border
		endX = titleW - 1
		availW = titleW - 1 // exclude left border column
		textStartX = 2      // left pad + left border
	} else {
		// Title bar sits between border chars.
		startX = 1
		endX = titleW - 2
		availW = titleW - 2 // exclude left and right border chars
		textStartX = 2      // left pad + 1 border char
	}

	// Reserve space for the close button if closable.
	closeW := 0
	if closable {
		closeW = 3 // " ✕ "
	}

	// Reserve 1 cell of left padding for a cleaner title appearance.
	const leftPad = 1

	titleAreaW := availW - closeW - leftPad
	if titleAreaW < 1 {
		return
	}

	rendering.Log("DrawTitleBar layout screenStartX=%d screenEndX=%d textScreenStartX=%d titleAreaW=%d closable=%v borderStyle=%v",
		screenX+startX, screenX+endX, screenX+textStartX, titleAreaW, closable, borderStyle)

	// Fill the title row with the title bar background color.
	for x := startX; x <= endX; x++ {
		buf.Set(x, 0, cell.NewCell(' ', fg, bg, 0))
	}

	// Truncate title if too long.
	titleText := title
	titleRW := rendering.VisibleWidth(titleText)
	if titleRW > titleAreaW {
		titleText = rendering.TruncateLine(titleText, titleAreaW)
	}

	// Calculate title start position based on alignment (with left padding offset).
	titleRW = rendering.VisibleWidth(titleText)
	var titleX int
	switch align {
	case AlignCenter:
		titleX = textStartX + (titleAreaW-titleRW)/2
	case AlignRight:
		titleX = textStartX + titleAreaW - titleRW
	default: // AlignLeft
		titleX = textStartX
	}

	// Determine the right boundary for title text (before close button area).
	var textEndX int
	if borderStyle == BorderNone || borderStyle == BorderLShape {
		// No right border — close button flush with right edge.
		if closable {
			textEndX = titleW - closeW - 1
		} else {
			textEndX = titleW - 1
		}
	} else {
		if closable {
			textEndX = titleW - closeW - 2
		} else {
			textEndX = titleW - 2
		}
	}

	rendering.Log("DrawTitleBar render title=%q screenTitleX=%d screenTextEndX=%d titleRW=%d screenStartX=%d screenEndX=%d",
		titleText, screenX+titleX, screenX+textEndX, titleRW, screenX+startX, screenX+endX)

	// Draw the title text, advancing by each character's visual width.
	x := titleX
	for _, r := range titleText {
		if x > textEndX {
			break
		}
		rw := rendering.VisibleWidth(string(r))
		if x >= startX && x <= textEndX {
			buf.Set(x, 0, cell.NewCell(r, fg, bg, cell.AttrBold))
			if rw == 2 {
				buf.Set(x+1, 0, cell.Cell{Char: 0, Width: 0, FG: fg, BG: bg, Attr: cell.AttrBold})
			}
		}
		x += rw
	}
}

// DrawCloseButton draws a close button in the title bar at the right side.
// The button is represented as " ✕ " (space, multiplication X, space).
//
// For BorderNone: the close button occupies positions (w-3, w-2, w-1),
// flush with the right edge of the window.
//
// For bordered windows: the close button occupies positions (w-4, w-3, w-2),
// leaving position (w-1) for the right border character.
func DrawCloseButton(buf *surface.CellBuffer, w int, fg, bg cell.Color, borderStyle BorderStyle) {
	startX := w - 4
	if borderStyle == BorderNone || borderStyle == BorderLShape {
		// No right border — close button flush with right edge,
		// aligned with the scrollbar column.
		startX = w - 3
	}
	if startX < 1 {
		startX = 1
	}
	buf.Set(startX, 0, cell.NewCell(' ', fg, bg, 0))
	buf.Set(startX+1, 0, cell.NewCell('✕', fg, bg, cell.AttrBold))
	buf.Set(startX+2, 0, cell.NewCell(' ', fg, bg, 0))
}
