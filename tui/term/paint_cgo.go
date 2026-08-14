//go:build cgo

package term

/*
#include "ghostty_bridge.h"
*/
import "C"

import (
	"unsafe"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
)

// Paint writes the visible Ghostty render-state grid into buf at origin.
// Default-colored cells use fg/bg so the pane fills instead of punching holes.
func (e *Emulator) Paint(buf *surface.CellBuffer, originX, originY int, fg, bg cell.Color) {
	if e == nil || e.term == nil || e.render == nil || buf == nil || e.closed {
		return
	}
	if e.paintDirty || e.paintCache == nil || e.paintCache.Width() != e.cols || e.paintCache.Height() != e.rows || e.paintFG != fg || e.paintBG != bg {
		e.rebuildPaintCache(fg, bg)
	}
	if e.paintCache == nil {
		return
	}
	cw, ch := e.paintCache.Width(), e.paintCache.Height()
	for y := 0; y < ch; y++ {
		for x := 0; x < cw; x++ {
			buf.Set(originX+x, originY+y, e.paintCache.Get(x, y))
		}
	}
}

func (e *Emulator) rebuildPaintCache(fg, bg cell.Color) {
	C.ghostty_render_state_update(e.render, e.term)

	var cols, rows C.uint16_t
	var curX, curY C.uint16_t
	var curVis, curInView C.bool
	var curStyle C.GhosttyRenderStateCursorVisualStyle = C.GHOSTTY_RENDER_STATE_CURSOR_VISUAL_STYLE_BLOCK
	C.ghostty_render_state_get(e.render, C.GHOSTTY_RENDER_STATE_DATA_COLS, unsafe.Pointer(&cols))
	C.ghostty_render_state_get(e.render, C.GHOSTTY_RENDER_STATE_DATA_ROWS, unsafe.Pointer(&rows))
	C.ghostty_render_state_get(e.render, C.GHOSTTY_RENDER_STATE_DATA_CURSOR_VISIBLE, unsafe.Pointer(&curVis))
	C.ghostty_render_state_get(e.render, C.GHOSTTY_RENDER_STATE_DATA_CURSOR_VIEWPORT_HAS_VALUE, unsafe.Pointer(&curInView))
	C.ghostty_render_state_get(e.render, C.GHOSTTY_RENDER_STATE_DATA_CURSOR_VIEWPORT_X, unsafe.Pointer(&curX))
	C.ghostty_render_state_get(e.render, C.GHOSTTY_RENDER_STATE_DATA_CURSOR_VIEWPORT_Y, unsafe.Pointer(&curY))
	C.ghostty_render_state_get(e.render, C.GHOSTTY_RENDER_STATE_DATA_CURSOR_VISUAL_STYLE, unsafe.Pointer(&curStyle))

	w, h := int(cols), int(rows)
	if w < 1 {
		w = e.cols
	}
	if h < 1 {
		h = e.rows
	}
	if e.paintCache == nil || e.paintCache.Width() != w || e.paintCache.Height() != h {
		e.paintCache = surface.NewCellBuffer(w, h)
	}
	blank := cell.Cell{Char: ' ', Width: 1, FG: fg, BG: bg}
	e.paintCache.Fill(blank)

	var rowIt C.GhosttyRenderStateRowIterator
	if C.ghostty_render_state_row_iterator_new(nil, &rowIt) != C.GHOSTTY_SUCCESS {
		return
	}
	defer C.ghostty_render_state_row_iterator_free(rowIt)
	if C.ghostty_render_state_get(e.render, C.GHOSTTY_RENDER_STATE_DATA_ROW_ITERATOR, unsafe.Pointer(&rowIt)) != C.GHOSTTY_SUCCESS {
		return
	}

	var cells C.GhosttyRenderStateRowCells
	if C.ghostty_render_state_row_cells_new(nil, &cells) != C.GHOSTTY_SUCCESS {
		return
	}
	defer C.ghostty_render_state_row_cells_free(cells)

	utf8 := make([]byte, 64)
	y := 0
	for C.ghostty_render_state_row_iterator_next(rowIt) {
		if y >= h {
			break
		}
		if C.ghostty_render_state_row_get(rowIt, C.GHOSTTY_RENDER_STATE_ROW_DATA_CELLS, unsafe.Pointer(&cells)) != C.GHOSTTY_SUCCESS {
			y++
			continue
		}
		rowSel := C.crude_row_selection(rowIt)
		x := 0
		prevWide := false
		wideFG, wideBG := fg, bg
		for C.ghostty_render_state_row_cells_next(cells) {
			if x >= w {
				break
			}
			c := paintCell(cells, utf8, fg, bg)
			if prevWide {
				cont := cell.ContinuationCell
				cont.FG, cont.BG = wideFG, wideBG
				c = cont
			}
			selected := bool(rowSel.has) && x >= int(rowSel.start_x) && x <= int(rowSel.end_x)
			if selected {
				c.Attr ^= cell.AttrReverse
			}
			if showCursor(bool(curVis), bool(curInView), x, y, int(curX), int(curY)) && e.cursorPhaseVisible() {
				c = applyCursorStyle(c, curStyle, e.curFocused)
			}
			e.paintCache.Set(x, y, c)
			if c.Width == 2 {
				prevWide = true
				wideFG, wideBG = c.FG, c.BG
			} else {
				prevWide = false
			}
			x++
		}
		y++
	}

	dirty := C.GHOSTTY_RENDER_STATE_DIRTY_FALSE
	C.ghostty_render_state_set(e.render, C.GHOSTTY_RENDER_STATE_OPTION_DIRTY, unsafe.Pointer(&dirty))
	e.paintFG, e.paintBG = fg, bg
	e.paintDirty = false
}

func showCursor(vis, inView bool, x, y, cx, cy int) bool {
	return vis && inView && x == cx && y == cy
}

// cursorPhaseVisible reports whether the blink phase shows the cursor.
// Unfocused panes always show (as a dimmed hollow-style cursor); focused
// panes hide it on the off phase when the style requests blinking.
func (e *Emulator) cursorPhaseVisible() bool {
	if !e.curFocused {
		return true
	}
	if !e.CursorBlink() {
		return true
	}
	return e.curBlinkOn
}

// applyCursorStyle renders the cursor within the cell model: block styles
// invert the cell, bar/underline styles underline it (the closest cell-level
// approximation of a thin caret). Unfocused panes dim the cursor cell — the
// cell-grid stand-in for a hollow outline.
func applyCursorStyle(c cell.Cell, style C.GhosttyRenderStateCursorVisualStyle, focused bool) cell.Cell {
	switch style {
	case C.GHOSTTY_RENDER_STATE_CURSOR_VISUAL_STYLE_BAR,
		C.GHOSTTY_RENDER_STATE_CURSOR_VISUAL_STYLE_UNDERLINE:
		c.Attr |= cell.AttrUnderline
		if !focused {
			c.Attr |= cell.AttrDim
		}
		return c
	default:
		// Toggle reverse relative to the cell's existing application or
		// selection style. Do not also swap FG/BG: AttrReverse emits SGR 7
		// later, and pre-swapping would make the output pipeline reverse the
		// colors twice, leaving a blank cursor visually invisible.
		c.Attr ^= cell.AttrReverse
		if !focused {
			c.Attr |= cell.AttrDim
		}
		return c
	}
}

func paintCell(cells C.GhosttyRenderStateRowCells, utf8 []byte, defFG, defBG cell.Color) cell.Cell {
	snap := C.crude_cell_style_get(cells)
	fg, bg := defFG, defBG
	if snap.result == C.GHOSTTY_SUCCESS {
		if snap.style.has_foreground {
			fg = cell.NewTrueColor(uint8(snap.style.foreground.r), uint8(snap.style.foreground.g), uint8(snap.style.foreground.b))
		}
		if snap.style.has_background {
			bg = cell.NewTrueColor(uint8(snap.style.background.r), uint8(snap.style.background.g), uint8(snap.style.background.b))
		}
	}
	var attr cell.Attrs
	if snap.style.bold {
		attr |= cell.AttrBold
	}
	if snap.style.faint {
		attr |= cell.AttrDim
	}
	if snap.style.italic {
		attr |= cell.AttrItalic
	}
	if snap.style.underline {
		attr |= cell.AttrUnderline
	}
	if snap.style.strikethrough {
		attr |= cell.AttrStrikethrough
	}
	if snap.style.inverse {
		attr |= cell.AttrReverse
	}

	enc := C.crude_cell_utf8(cells, (*C.uint8_t)(unsafe.Pointer(&utf8[0])), C.size_t(len(utf8)))
	text := ""
	if enc.result == C.GHOSTTY_SUCCESS && enc.len > 0 {
		text = C.GoStringN((*C.char)(unsafe.Pointer(&utf8[0])), C.int(enc.len))
	}
	if text == "" {
		return cell.Cell{Char: ' ', Width: 1, FG: fg, BG: bg, Attr: attr}
	}
	w := rendering.PlainWidth(text)
	if w < 1 {
		w = 1
	}
	if w > 2 {
		w = 2
	}
	return cell.NewClusterCell(text, w, fg, bg, attr)
}
