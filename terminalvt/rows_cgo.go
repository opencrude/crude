//go:build cgo

package terminalvt

/*
#include "ghostty_bridge.h"
*/
import "C"
import "unsafe"

// Rows returns a bounded page starting at the oldest retained history row.
// start=-1 requests the active grid. A reader never leaves the engine scrolled.
func (e *Engine) Rows(start, count int) (Projection, error) {
	if e == nil || e.closed {
		return Projection{}, ErrClosed
	}
	if count < 0 || count > MaxSnapshotRows || start < -1 {
		return Projection{}, ErrBounds
	}
	C.crude_scroll_bottom(e.term)
	_, retained, _ := e.Scrollbar()
	total := min(retained, e.scrollback+e.rows)
	base := retained - total
	if err := e.syncHistoryIdentity(base); err != nil {
		return Projection{}, err
	}
	if start == -1 {
		start = max(0, total-e.rows)
	}
	if start > total {
		return Projection{}, ErrBounds
	}
	if count == 0 {
		count = e.rows
	}
	count = min(count, total-start)
	x, y, v := e.Cursor()
	p := Projection{Cols: e.cols, GridRows: e.rows, Start: start, Total: total, Cursor: Cursor{x, y, v, e.CursorBlink()}, Modes: Modes{e.BracketedPaste(), e.MouseTracking(), e.AltScreen(), e.FocusReporting()}}
	p.HistoryEpoch = e.historyEpoch
	defer C.crude_scroll_bottom(e.term)
	for len(p.Cells) < count {
		at := base + start + len(p.Cells)
		top := min(at, max(0, retained-e.rows))
		C.crude_scroll_top(e.term)
		C.crude_scroll_delta(e.term, C.intptr_t(top))
		if C.ghostty_render_state_update(e.render, e.term) != C.GHOSTTY_SUCCESS {
			return Projection{}, ErrUnavailable
		}
		page, wrapped, err := e.viewportRows()
		if err != nil {
			return Projection{}, err
		}
		offset := at - top
		n := min(count-len(p.Cells), len(page)-offset)
		if n <= 0 {
			break
		}
		p.Cells = append(p.Cells, page[offset:offset+n]...)
		p.Wrapped = append(p.Wrapped, wrapped[offset:offset+n]...)
	}
	return p, nil
}

// Track the oldest published row through Ghostty page-list mutations. Unlike
// text comparisons or total-row deltas, this detects page recycling and resets.
func (e *Engine) syncHistoryIdentity(base int) error {
	alt := e.AltScreen()
	if e.historyRef != nil {
		var at C.GhosttyPointCoordinate
		if alt != e.historyAlt || C.ghostty_tracked_grid_ref_point(e.historyRef, C.GHOSTTY_POINT_TAG_SCREEN, &at) != C.GHOSTTY_SUCCESS || int(at.y) != base || int(at.x) != 0 {
			e.historyEpoch++
		}
		C.ghostty_tracked_grid_ref_free(e.historyRef)
		e.historyRef = nil
	}
	// GhosttyPoint contains a C union; resolve its coordinate layout in C memory.
	var point C.GhosttyPoint
	point.tag = C.GHOSTTY_POINT_TAG_SCREEN
	coord := (*C.GhosttyPointCoordinate)(unsafe.Pointer(&point.value))
	coord.y = C.uint32_t(base)
	if C.ghostty_terminal_grid_ref_track(e.term, point, &e.historyRef) != C.GHOSTTY_SUCCESS {
		return ErrUnavailable
	}
	e.historyAlt = alt
	return nil
}
func (e *Engine) viewportRows() ([][]Cell, []bool, error) {
	var it C.GhosttyRenderStateRowIterator
	if C.ghostty_render_state_row_iterator_new(nil, &it) != C.GHOSTTY_SUCCESS {
		return nil, nil, ErrUnavailable
	}
	defer C.ghostty_render_state_row_iterator_free(it)
	if C.ghostty_render_state_get(e.render, C.GHOSTTY_RENDER_STATE_DATA_ROW_ITERATOR, unsafe.Pointer(&it)) != C.GHOSTTY_SUCCESS {
		return nil, nil, ErrUnavailable
	}
	var cells C.GhosttyRenderStateRowCells
	if C.ghostty_render_state_row_cells_new(nil, &cells) != C.GHOSTTY_SUCCESS {
		return nil, nil, ErrUnavailable
	}
	defer C.ghostty_render_state_row_cells_free(cells)
	out := make([][]Cell, 0, e.rows)
	wrapped := make([]bool, 0, e.rows)
	buf := make([]byte, 64)
	for len(out) < e.rows && bool(C.ghostty_render_state_row_iterator_next(it)) {
		var rawRow C.GhosttyRow
		var wrap C.bool
		if C.ghostty_render_state_row_get(it, C.GHOSTTY_RENDER_STATE_ROW_DATA_RAW, unsafe.Pointer(&rawRow)) != C.GHOSTTY_SUCCESS || C.ghostty_row_get(rawRow, C.GHOSTTY_ROW_DATA_WRAP, unsafe.Pointer(&wrap)) != C.GHOSTTY_SUCCESS {
			return nil, nil, ErrUnavailable
		}
		if C.ghostty_render_state_row_get(it, C.GHOSTTY_RENDER_STATE_ROW_DATA_CELLS, unsafe.Pointer(&cells)) != C.GHOSTTY_SUCCESS {
			return nil, nil, ErrUnavailable
		}
		row := make([]Cell, 0, e.cols)
		for len(row) < e.cols && bool(C.ghostty_render_state_row_cells_next(cells)) {
			var raw C.GhosttyCell
			var wide C.GhosttyCellWide
			C.ghostty_render_state_row_cells_get(cells, C.GHOSTTY_RENDER_STATE_ROW_CELLS_DATA_RAW, unsafe.Pointer(&raw))
			C.ghostty_cell_get(raw, C.GHOSTTY_CELL_DATA_WIDE, unsafe.Pointer(&wide))
			c := Cell{Width: 1, Text: " "}
			if wide == C.GHOSTTY_CELL_WIDE_WIDE {
				c.Width = 2
			}
			if wide == C.GHOSTTY_CELL_WIDE_SPACER_TAIL || wide == C.GHOSTTY_CELL_WIDE_SPACER_HEAD {
				c.Width = 0
				c.Text = ""
			}
			enc := C.crude_cell_utf8(cells, (*C.uint8_t)(unsafe.Pointer(&buf[0])), C.size_t(len(buf)))
			if enc.result == C.GHOSTTY_OUT_OF_SPACE && enc.len <= 65536 {
				buf = make([]byte, int(enc.len))
				enc = C.crude_cell_utf8(cells, (*C.uint8_t)(unsafe.Pointer(&buf[0])), C.size_t(len(buf)))
			}
			if enc.result == C.GHOSTTY_OUT_OF_SPACE {
				return nil, nil, ErrBounds
			}
			if enc.result == C.GHOSTTY_SUCCESS && enc.len > 0 && c.Width != 0 {
				c.Text = C.GoStringN((*C.char)(unsafe.Pointer(&buf[0])), C.int(enc.len))
			}
			s := C.crude_cell_style_get(cells)
			if s.result != C.GHOSTTY_SUCCESS {
				return nil, nil, ErrUnavailable
			}
			c.FG = Color{bool(s.style.has_foreground), uint8(s.style.foreground.r), uint8(s.style.foreground.g), uint8(s.style.foreground.b)}
			c.BG = Color{bool(s.style.has_background), uint8(s.style.background.r), uint8(s.style.background.g), uint8(s.style.background.b)}
			c.Style = Style{bool(s.style.bold), bool(s.style.faint), bool(s.style.italic), bool(s.style.underline), bool(s.style.strikethrough), bool(s.style.inverse)}
			row = append(row, c)
		}
		out = append(out, row)
		wrapped = append(wrapped, bool(wrap))
	}
	return out, wrapped, nil
}
