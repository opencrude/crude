package ui

import (
	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
)

// BannerHeight is the row count of the application wordmark.
const BannerHeight = 6

// bannerRows is the single owner of the application wordmark. Every page the
// user meets outside a project (introduction, project selection, and future
// detached pages) paints it through PaintBanner so the identity is identical.
var bannerRows = [BannerHeight]string{
	" ██████╗██████╗ ██╗   ██╗██████╗ ███████╗",
	"██╔════╝██╔══██╗██║   ██║██╔══██╗██╔════╝",
	"██║     ██████╔╝██║   ██║██║  ██║█████╗  ",
	"██║     ██╔══██╗██║   ██║██║  ██║██╔══╝  ",
	"╚██████╗██║  ██║╚██████╔╝██████╔╝███████╗",
	" ╚═════╝╚═╝  ╚═╝ ╚═════╝ ╚═════╝ ╚══════╝",
}

// BannerText returns the wordmark rows for measurement and tests.
func BannerText() [BannerHeight]string { return bannerRows }

// PaintBanner paints the application wordmark at the absolute cell coordinate
// (x, y), clipped to width columns of the given background. Narrow widths keep
// the leading columns and mark the cut with an ellipsis.
func PaintBanner(buf *surface.CellBuffer, x, y, width int, tokens *TokenSet, bg cell.Color) {
	if buf == nil || tokens == nil || width <= 0 {
		return
	}
	fg := tokens.Color(ColorAccent)
	for i, line := range bannerRows {
		buf.WriteString(x, y+i, rendering.TruncateVisual(line, width, "…"), fg, bg, cell.AttrBold)
	}
}
