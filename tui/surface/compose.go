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
	bounds := Rect{X: dstX, Y: dstY, W: srcW, H: srcH}.Intersect(FullRect(dstW, dstH))
	if clip != nil {
		bounds = bounds.Intersect(clip.Active())
	}
	if bounds.Empty() {
		return
	}

	for dy := bounds.Y; dy < bounds.Y+bounds.H; dy++ {
		source := src.Row(dy - dstY)
		destination := dst.Row(dy)
		// Replacing a whole opaque row cannot leave a partially occluded old
		// grapheme. Validate the actual cells (not a stale surface flag), then
		// copy values in bulk. Default backgrounds and malformed spans retain
		// the inheritance/normalization path below.
		if dstX == 0 && len(source) == len(destination) && bounds.X == 0 && bounds.W == dstW && opaqueCellRow(source) {
			copy(destination, source)
			continue
		}
		for dx := bounds.X; dx < bounds.X+bounds.W; dx++ {
			srcCell := source[dx-dstX]

			// Skip continuation cells (width=0); they are handled by
			// the primary wide cell that wrote them.
			if srcCell.Width == 0 {
				continue
			}

			// Skip transparent cells: don't overwrite the destination
			if srcCell.Transparent {
				continue
			}

			span := max(1, int(srcCell.Width))
			if span > 1 && (dx+span > dstW || !spanInsideClip(clip, dx, dy, span)) {
				continue
			}

			// Write the primary cell
			// If the source cell has no explicit background (NoColor), preserve
			// the destination's background color instead of overwriting it.
			// This ensures text rendered without an explicit background color
			// inherits the background of whatever container it's composited into
			// (e.g., a floating window's SubPaneBg), rather than reverting to the
			// terminal's default background.
			dstCell := destination[dx]
			if srcCell.BG.IsDefault() {
				srcCell.BG = dstCell.BG
			}
			if srcCell.Width == 1 && dstCell.Width == 1 {
				destination[dx] = srcCell
				continue
			}
			replaceCellSpan(dst, dx, dy, srcCell)
		}
	}
}

func opaqueCellRow(row []cell.Cell) bool {
	for x := 0; x < len(row); {
		c := &row[x]
		width := int(c.Width)
		if c.Transparent || c.BG.IsDefault() || width == 0 || x+width > len(row) {
			return false
		}
		end := x + width
		if width == 1 {
			x++
			continue
		}
		continuation := cell.Cell{FG: c.FG, BG: c.BG, Attr: c.Attr}
		for x++; x < end; x++ {
			if row[x] != continuation {
				return false
			}
		}
	}
	return true
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
			if srcCell.Width == 0 {
				continue
			}
			if dx+max(1, int(srcCell.Width)) > dstW {
				continue
			}
			replaceCellSpan(dst, dx, dy, srcCell)
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
			// Skip continuation cells: handled by primary
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

			if dx+max(1, int(result.Width)) > dstW {
				continue
			}
			replaceCellSpan(dst, dx, dy, result)
		}
	}
}

// MergeDown merges all cells from src into dst at the given offset.
// Cells marked as transparent in src are skipped, letting dst show through.
// Non-transparent cells in src overwrite the corresponding dst cells.
// This implements the "painter's algorithm": later writes win.
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

			// Skip continuation cells: handled by primary
			if srcCell.Width == 0 {
				continue
			}

			// Skip transparent source cells
			if srcCell.Transparent {
				continue
			}

			if dx+max(1, int(srcCell.Width)) > dstW {
				continue
			}

			// Overwrite destination with one atomic grapheme span.
			replaceCellSpan(dst, dx, dy, srcCell)
		}
	}
}

func spanInsideClip(clip *ClipStack, x, y, width int) bool {
	if clip == nil {
		return true
	}
	for offset := 0; offset < width; offset++ {
		if !clip.Contains(x+offset, y) {
			return false
		}
	}
	return true
}

// replaceCellSpan atomically occludes destination graphemes before writing a
// new primary and its continuation cells. This prevents a floating border from
// leaving half of an underlying emoji alive in the composited row.
func replaceCellSpan(dst *CellBuffer, x, y int, source cell.Cell) {
	dst.SetSpan(x, y, source)
}

func clearIntersectingSpans(dst *CellBuffer, x, y, width int) {
	end := min(dst.Width(), x+width)
	for column := max(0, x); column < end; column++ {
		start, primary, ok := destinationSpanAt(dst, column, y)
		if !ok {
			continue
		}
		blank := primary
		blank.Char = ' '
		blank.Content = " "
		blank.Width = 1
		for offset := 0; offset < int(primary.Width); offset++ {
			dst.setRaw(start+offset, y, blank)
		}
	}
}

func destinationSpanAt(dst *CellBuffer, x, y int) (int, cell.Cell, bool) {
	current := dst.Get(x, y)
	if current.Width > 1 {
		return x, current, true
	}
	if current.Width != 0 {
		return 0, cell.Cell{}, false
	}
	for start := x - 1; start >= 0; start-- {
		candidate := dst.Get(start, y)
		if candidate.Width > 1 && start+int(candidate.Width) > x {
			return start, candidate, true
		}
		if candidate.Width > 0 {
			break
		}
	}
	// Orphan continuations from older buffers are normalized as one blank.
	return x, cell.Cell{
		Char:        ' ',
		Content:     " ",
		Width:       1,
		FG:          current.FG,
		BG:          current.BG,
		Attr:        current.Attr,
		Transparent: current.Transparent,
	}, true
}

// ClearRegion fills a rectangular region with transparent blank cells.
func ClearRegion(buf *CellBuffer, r Rect) {
	buf.FillRect(r, cell.TransparentBlank())
}
