package widgets

import (
	"strings"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/widget"
)

// PopupContent is a widget that displays pre-rendered ANSI string lines
// as content inside a FloatingWindow.  It uses the ANSI-to-surface parser
// (rendering.RenderToSurface) so lipgloss styles in the lines are correctly
// rendered as colored cells.
type PopupContent struct {
	lines []string
	dirty bool
}

// NewPopupContent creates a PopupContent from pre-rendered ANSI lines.
func NewPopupContent(lines []string) *PopupContent {
	return &PopupContent{lines: lines, dirty: true}
}

// SetLines updates the displayed lines and marks the widget dirty.
func (p *PopupContent) SetLines(lines []string) {
	p.lines = lines
	p.dirty = true
}

func (p *PopupContent) Measure(availW, availH int) (int, int) {
	if len(p.lines) == 0 {
		return 10, 3
	}
	return availW, len(p.lines)
}

func (p *PopupContent) Render(surf *surface.Surface, ctx *widget.RenderContext) {
	w, h := surf.Width(), surf.Height()
	if w < 1 || h < 1 {
		return
	}

	rendering.RenderToSurface(surf, strings.Join(p.lines, "\n"))

	// Fill any cells still with NoColor background with the popup background.
	// Without this, makeNoColorBgTransparent (called after Render by
	// FloatingWindow.Render) converts blank cells with NoColor BG to
	// transparent, creating visible holes in the popup.
	bg := rendering.ThemeColorToCell(ctx.Theme.Colors.Code.Bg)
	buf := surf.Buffer()
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := buf.Get(x, y)
			if c.BG.Type == cell.ColorDefault && c.IsBlank() {
				buf.Set(x, y, cell.Cell{Char: c.Char, Width: c.Width, FG: c.FG, BG: bg, Attr: c.Attr})
			}
		}
	}

	p.dirty = false
}
