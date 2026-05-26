package surface

import (
	"crude/tui/cell"
)

// Blit copies cells from a source buffer to a destination buffer at the given
// offset. Transparent source cells do NOT overwrite destination cells.
// Wide characters are handled: the continuation cell is copied along with the
// primary.
//
// If clip is non-nil, cells outside the clip region are skipped.
// This is the core compositing operation used by the Compositor.
func Blit(dst *CellBuffer, dstX, dstY int, src *CellBuffer, clip *ClipStack) {
	srcW, srcH := src.Width(), src.Height()
	dstW, dstH := dst.Width(), dst.Height()

	for sy := 0; sy < srcH; sy++ {
		dy := dstY + sy
		if dy < 0 || dy >= dstH {
			continue
		}
		for sx := 0; sx < srcW; sx++ {
			dx := dstX + sx
			if dx < 0 || dx >= dstW {
				continue
			}
			// Clip check
			if clip != nil && !clip.Contains(dx, dy) {
				continue
			}

			srcCell := src.Get(sx, sy)

			// Skip continuation cells (width=0) — they are handled by
			// the primary wide cell that wrote them.
			if srcCell.Width == 0 {
				continue
			}

			// Skip transparent cells — don't overwrite the destination
			if srcCell.Transparent {
				continue
			}

			// Write the primary cell
			// If the source cell has no explicit background (NoColor), preserve
			// the destination's background color instead of overwriting it.
			// This ensures text rendered without an explicit background color
			// inherits the background of whatever container it's composited into
			// (e.g., a floating window's SubPaneBg), rather than reverting to the
			// terminal's default background.
			if srcCell.BG.IsDefault() {
				dstCell := dst.Get(dx, dy)
				srcCell.BG = dstCell.BG
			}
			dst.Set(dx, dy, srcCell)

			// If this is a wide cell, also write the continuation cell
			if srcCell.Width == 2 {
				contCell := cell.Cell{
					Char:  0,
					Width: 0,
					FG:    srcCell.FG,
					BG:    srcCell.BG,
					Attr:  srcCell.Attr,
				}
				dst.Set(dx+1, dy, contCell)
			}
		}
	}
}

// BlitOpaque copies all cells from source to destination, overwriting
// everything (including transparent cells). Used when the destination
// should be fully replaced.
func BlitOpaque(dst *CellBuffer, dstX, dstY int, src *CellBuffer) {
	srcW, srcH := src.Width(), src.Height()
	dstW, dstH := dst.Width(), dst.Height()

	for sy := 0; sy < srcH; sy++ {
		dy := dstY + sy
		if dy < 0 || dy >= dstH {
			continue
		}
		for sx := 0; sx < srcW; sx++ {
			dx := dstX + sx
			if dx < 0 || dx >= dstW {
				continue
			}
			srcCell := src.Get(sx, sy)
			dst.Set(dx, dy, srcCell)
		}
	}
}

// BlendAlpha composites source cells over destination with alpha blending.
// For each cell: if src.Transparent → skip. Otherwise blend colors.
// The alpha parameter controls the opacity of the source (0.0=invisible,
// 1.0=fully opaque).
func BlendAlpha(dst *CellBuffer, dstX, dstY int, src *CellBuffer, alpha float32) {
	srcW, srcH := src.Width(), src.Height()
	dstW, dstH := dst.Width(), dst.Height()

	for sy := 0; sy < srcH; sy++ {
		dy := dstY + sy
		if dy < 0 || dy >= dstH {
			continue
		}
		for sx := 0; sx < srcW; sx++ {
			dx := dstX + sx
			if dx < 0 || dx >= dstW {
				continue
			}

			srcCell := src.Get(sx, sy)

			// Skip transparent source cells
			if srcCell.Transparent {
				continue
			}
			// Skip continuation cells — handled by primary
			if srcCell.Width == 0 {
				continue
			}

			dstCell := dst.Get(dx, dy)

			// Blend foreground color
			blendedFG := dstCell.FG.Blend(srcCell.FG, alpha)
			// Blend background color
			blendedBG := dstCell.BG.Blend(srcCell.BG, alpha)

			result := srcCell // Start with source cell structure
			result.FG = blendedFG
			result.BG = blendedBG

			dst.Set(dx, dy, result)

			// Handle wide char continuation
			if srcCell.Width == 2 {
				contCell := cell.Cell{
					Char:  0,
					Width: 0,
					FG:    blendedFG,
					BG:    blendedBG,
					Attr:  srcCell.Attr,
				}
				dst.Set(dx+1, dy, contCell)
			}
		}
	}
}

// MergeDown merges all cells from src into dst at the given offset.
// Cells marked as transparent in src are skipped, letting dst show through.
// Non-transparent cells in src overwrite the corresponding dst cells.
// This implements the "painter's algorithm" — later writes win.
func MergeDown(dst *CellBuffer, dstX, dstY int, src *CellBuffer) {
	srcW, srcH := src.Width(), src.Height()
	dstW, dstH := dst.Width(), dst.Height()

	for sy := 0; sy < srcH; sy++ {
		dy := dstY + sy
		if dy < 0 || dy >= dstH {
			continue
		}
		for sx := 0; sx < srcW; sx++ {
			dx := dstX + sx
			if dx < 0 || dx >= dstW {
				continue
			}

			srcCell := src.Get(sx, sy)

			// Skip continuation cells — handled by primary
			if srcCell.Width == 0 {
				continue
			}

			// Skip transparent source cells
			if srcCell.Transparent {
				continue
			}

			// Overwrite destination with source cell
			dst.Set(dx, dy, srcCell)

			// Handle wide char continuation
			if srcCell.Width == 2 {
				contCell := cell.Cell{
					Char:  0,
					Width: 0,
					FG:    srcCell.FG,
					BG:    srcCell.BG,
					Attr:  srcCell.Attr,
				}
				dst.Set(dx+1, dy, contCell)
			}
		}
	}
}

// ClearRegion fills a rectangular region with transparent blank cells.
func ClearRegion(buf *CellBuffer, r Rect) {
	buf.FillRect(r, cell.TransparentBlank())
}
