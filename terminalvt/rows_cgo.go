//go:build cgo

package terminalvt

/*
#include "ghostty_bridge.h"
*/
import "C"
import "unsafe"

// Rows returns a bounded page starting at the oldest retained history row.
// start=-1 requests the active grid. A reader never leaves the engine scrolled.
func (e *Engine) Rows(request RowRequest) (result Projection, err error) {
	start, count := request.Start, request.Count
	if e == nil || e.closed {
		return Projection{}, ErrClosed
	}
	if count < 0 || count > MaxSnapshotRows || start < -1 || request.ViewportStart < -1 {
		return Projection{}, ErrBounds
	}
	e.readSequence++
	defer func() { result.ReadSequence = e.readSequence }()
	e.expireSynchronizedOutput()
	if frozen := e.synchronized; frozen != nil {
		p := *frozen
		start = requestStart(request, p)
		if request.HistoryEpoch != 0 && request.HistoryEpoch != p.HistoryEpoch {
			start, count = p.Start, p.GridRows
		}
		if start == -1 {
			start = p.Start
		}
		start = min(start, p.Total)
		if count == 0 {
			count = p.GridRows
		}
		end := min(start+count, p.Total, p.Start+p.GridRows)
		start = max(start, p.Start)
		if end <= start {
			// A historical request can race the begin boundary. Return the
			// usable completed grid, not an empty page that would blank a new
			// viewer. Detached cached history remains client-owned until commit.
			start, end = p.Start, p.Start+p.GridRows
		}
		p.Start = start
		if end > start {
			page, wrapped, err := e.viewportRows()
			if err != nil {
				return Projection{}, err
			}
			p.Cells = page[start-frozen.Start : end-frozen.Start]
			p.Wrapped = wrapped[start-frozen.Start : end-frozen.Start]
		}
		return p, nil
	}
	p, err := e.rowMetadata(start)
	if err != nil {
		return Projection{}, err
	}
	start = requestStart(request, p)
	if start == -1 {
		start = max(0, p.Total-p.GridRows)
	}
	start = min(start, p.Total)
	p.Start = start
	if request.Start >= 0 && request.HistoryEpoch != 0 && request.HistoryEpoch != p.HistoryEpoch {
		// An old selection/page request cannot publish unrelated new history.
		// Recover once with a complete clamped viewport and explicit new identity.
		count = 0
		if request.Count > 0 {
			start = request.ViewportStart
			if start < 0 {
				start = max(0, p.Total-p.GridRows)
			}
		}
	}
	if count == 0 {
		count = e.rows
		// A viewport request can race a reset/reflow that shortens history.
		// Return a complete clamped viewport, never an empty/partial bottom page.
		start = min(start, max(0, p.Total-e.rows))
		p.Start = start
	}
	count = min(count, p.Total-start)
	_, retained, _ := e.Scrollbar()
	base := retained - p.Total
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

func requestStart(request RowRequest, p Projection) int {
	if request.Start < 0 {
		return -1
	}
	if request.HistoryEpoch == p.HistoryEpoch && request.HistoryBase <= p.HistoryBase {
		shift := p.HistoryBase - request.HistoryBase
		if shift >= uint64(request.Start) {
			return 0
		}
		return request.Start - int(shift)
	}
	return request.Start
}

func (e *Engine) rowMetadata(start int) (Projection, error) {
	C.crude_scroll_bottom(e.term)
	_, retained, _ := e.Scrollbar()
	total := min(retained, e.scrollback+e.rows)
	base := retained - total
	if err := e.syncHistoryIdentity(base, total); err != nil {
		return Projection{}, err
	}
	if start == -1 {
		start = max(0, total-e.rows)
	}
	start = min(start, total)
	x, y, v := e.Cursor()
	p := Projection{Cols: e.cols, GridRows: e.rows, Start: start, Total: total, Cursor: Cursor{x, y, v, e.CursorBlink()}, Modes: Modes{e.BracketedPaste(), e.MouseTracking(), e.AltScreen(), e.FocusReporting()}}
	p.HistoryEpoch = e.historyEpoch
	p.HistoryBase = e.historyBase
	return p, nil
}

// Track the oldest published row through Ghostty page-list mutations. Unlike
// text comparisons or total-row deltas, this detects page recycling and resets.
func (e *Engine) syncHistoryIdentity(base, total int) error {
	alt := e.AltScreen()
	if e.historyRef != nil {
		var at, tail C.GhosttyPointCoordinate
		oldestOK := C.ghostty_tracked_grid_ref_point(e.historyRef, C.GHOSTTY_POINT_TAG_SCREEN, &at) == C.GHOSTTY_SUCCESS && int(at.x) == 0
		tailOK := e.historyTail != nil && C.ghostty_tracked_grid_ref_point(e.historyTail, C.GHOSTTY_POINT_TAG_SCREEN, &tail) == C.GHOSTTY_SUCCESS && int(tail.x) == 0
		shift := e.historyTotal - 1 - int(tail.y) + base
		if alt != e.historyAlt || !tailOK || shift < 0 || oldestOK && shift != base-int(at.y) || !oldestOK && shift == 0 {
			e.historyEpoch++
			e.historyBase = 0
		} else if shift > 0 {
			e.historyBase += uint64(shift)
		}
		C.ghostty_tracked_grid_ref_free(e.historyRef)
		C.ghostty_tracked_grid_ref_free(e.historyTail)
		e.historyRef = nil
		e.historyTail = nil
	}
	// GhosttyPoint contains a C union; resolve its coordinate layout in C memory.
	var point C.GhosttyPoint
	point.tag = C.GHOSTTY_POINT_TAG_SCREEN
	coord := (*C.GhosttyPointCoordinate)(unsafe.Pointer(&point.value))
	coord.y = C.uint32_t(base)
	if C.ghostty_terminal_grid_ref_track(e.term, point, &e.historyRef) != C.GHOSTTY_SUCCESS {
		return ErrUnavailable
	}
	coord.y = C.uint32_t(base + total - 1)
	if C.ghostty_terminal_grid_ref_track(e.term, point, &e.historyTail) != C.GHOSTTY_SUCCESS {
		return ErrUnavailable
	}
	e.historyAlt = alt
	e.historyTotal = total
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
