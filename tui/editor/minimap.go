package editor

import (
	"crude/tui/ui"
	"unicode/utf8"
)

// The terminal miniature samples four displayed rows per cell and the first
// 64 source columns. It is an orientation aid, not an exhaustive source reader.
// Its work/storage depend on the pane, never the document's total byte count.
type minimapKey struct {
	buffer                              *PieceTable
	revision, fold                      uint64
	tab, width, height, rows, wrapWidth int
	wrapped                             bool
	inlays                              *editorInlayProjection
	ghost                               *editorGhost
	ghostText                           string
}
type minimapProjection struct {
	key     minimapKey
	image   *ui.ScrollbarOverview
	samples uint64
}

func (e *Editor) minimapWidth() int {
	if !e.minimap {
		return 0
	}
	available := e.viewW - e.gutterWidth() - e.scrollbarStrokeWidth()
	if available < 44 {
		return 0
	} // retain at least forty source columns in narrow panes
	return min(8, available/10)
}
func (e *Editor) setMinimap(on bool) {
	if e.minimap == on {
		return
	}
	visible := e.inlayCaretVisible()
	e.stopPointerSelection()
	e.CancelPointerCapture()
	e.resetSecondaryGoals()
	e.clearGoal()
	e.minimap = on
	e.mini = minimapProjection{}
	if visible {
		e.pendingReveal = true
	}
}
func (e *Editor) prepareMinimap(height int) *ui.ScrollbarOverview {
	width := e.minimapWidth()
	if width == 0 || height < 1 {
		return nil
	}
	rows := e.visualRowCount()
	wrapped := e.wrapsRows()
	key := minimapKey{buffer: e.buf, revision: e.Revision(), fold: e.folds.hiddenGeneration, tab: e.TabWidth(), width: width, height: height, rows: rows, wrapped: wrapped, wrapWidth: e.rows.width, inlays: e.currentInlays(), ghost: e.currentGhost()}
	if key.ghost != nil {
		key.ghostText = key.ghost.text
	}
	if e.mini.image != nil && e.mini.key == key {
		return e.mini.image
	}
	image := &ui.ScrollbarOverview{Width: width, Height: height, Cells: make([]rune, width*height)}
	// Braille's two columns of four dots preserve indentation and line density.
	dots := [4][2]rune{{1, 8}, {2, 16}, {4, 32}, {64, 128}}
	samples := min(rows, height*4)
	for sample := 0; sample < samples; sample++ {
		index := sample * (rows - 1) / max(1, samples-1)
		pixelRow := sample * (height*4 - 1) / max(1, samples-1)
		row := e.rowAt(index)
		raw, err := e.buf.lineView(row.line)
		if err != nil {
			continue
		}
		// A legal enormous combining cluster must not make a miniature scan a whole
		// line. Prefix sampling is display-only and does not publish source positions.
		n := min(len(raw), 1024)
		for n < len(raw) && n > 0 && !utf8.RuneStart(raw[n]) {
			n--
		}
		text := string(raw[:n])
		e.mini.samples++
		start, end := 0, width*8
		if wrapped {
			start, end = row.start, min(row.end, row.start+width*8)
		}
		off, col, display := 0, 0, 0
		for off < len(text) && display < end {
			n, w := firstSourceUnit(e.TabWidth(), text[off:], col)
			if n == 0 {
				break
			}
			display = col
			if wrapped {
				display = e.displayColumn(row.line, col)
			}
			if text[off] != ' ' && text[off] != '\t' && text[off] != '\r' {
				for column := max(start, display); column < min(end, display+w); column++ {
					dot := (column - start) / 4
					if dot < width*2 {
						at := (pixelRow/4)*width + dot/2
						image.Cells[at] |= dots[pixelRow%4][dot%2]
					}
				}
			}
			off, col = off+n, col+w
		}
		if n < len(raw) && display < start {
			// A source prefix budget must not make a later wrapped row look
			// empty. Mark the unsampled continuation, without scanning it.
			image.Cells[(pixelRow/4)*width] |= 1 << 8
		}
	}
	for i, bits := range image.Cells {
		switch {
		case bits&255 != 0:
			image.Cells[i] = 0x2800 + (bits & 255)
		case bits != 0:
			image.Cells[i] = '…'
		default:
			image.Cells[i] = ' '
		}
	}
	e.mini.key, e.mini.image = key, image
	return image
}
