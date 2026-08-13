//go:build cgo

package term

/*
#include "ghostty_bridge.h"
*/
import "C"

import (
	"unsafe"
)

// SelectionPress feeds a left-button press into the selection gesture at
// viewport cell (x, y). timeNS drives multi-click detection (double click =
// word, triple = line). Reports whether the visible selection changed.
func (e *Emulator) SelectionPress(x, y int, timeNS uint64) bool {
	if e == nil || e.term == nil || e.selGesture == nil || e.selPress == nil || e.closed {
		return false
	}
	x, y = e.clampCell(x, y)
	e.selAnchorX, e.selAnchorY = x, y
	had := bool(C.crude_has_selection(e.term))
	// 0.4: left of the 60% inclusion threshold, so the anchored cell joins
	// forward selections (matching how a pointer press usually lands).
	if C.crude_gesture_event_prepare(e.selPress, e.term, C.uint16_t(x), C.uint32_t(y),
		0.4, C.uint64_t(timeNS), C.uint32_t(e.cols), C.uint32_t(e.rows), false) != C.GHOSTTY_SUCCESS {
		return false
	}
	out := C.crude_gesture_apply(e.selGesture, e.term, e.selPress)
	if out.result != C.GHOSTTY_SUCCESS {
		return false
	}
	if out.has {
		C.crude_install_selection(e.term, &out.sel)
		e.invalidatePaint()
		return true
	}
	// Single press anchors a new gesture: clear any previous selection.
	if had {
		C.crude_install_selection(e.term, nil)
		e.invalidatePaint()
		return true
	}
	return false
}

// SelectionDrag extends the active gesture to viewport cell (x, y). Rows
// outside the viewport scroll the view one line in that direction first.
func (e *Emulator) SelectionDrag(x, y int) bool {
	if e == nil || e.term == nil || e.selGesture == nil || e.selDrag == nil || e.closed {
		return false
	}
	if y < 0 {
		e.ScrollDelta(-1)
	} else if y >= e.rows {
		e.ScrollDelta(1)
	}
	x, y = e.clampCell(x, y)
	// Cell grids have no sub-cell pointer: bias the drag position past the
	// 60% inclusion threshold in the drag direction so the cell under the
	// pointer is always part of the selection.
	frac := C.double(0.9)
	if y < e.selAnchorY || (y == e.selAnchorY && x < e.selAnchorX) {
		frac = 0.1
	}
	if C.crude_gesture_event_prepare(e.selDrag, e.term, C.uint16_t(x), C.uint32_t(y),
		frac, 0, C.uint32_t(e.cols), C.uint32_t(e.rows), true) != C.GHOSTTY_SUCCESS {
		return false
	}
	out := C.crude_gesture_apply(e.selGesture, e.term, e.selDrag)
	if out.result != C.GHOSTTY_SUCCESS || !out.has {
		return false
	}
	C.crude_install_selection(e.term, &out.sel)
	e.invalidatePaint()
	return true
}

// SelectionRelease ends the active gesture and reports whether a selection
// remains installed.
func (e *Emulator) SelectionRelease(x, y int) bool {
	if e == nil || e.term == nil || e.selGesture == nil || e.selRelease == nil || e.closed {
		return e.HasSelection()
	}
	x, y = e.clampCell(x, y)
	if C.crude_gesture_event_prepare(e.selRelease, e.term, C.uint16_t(x), C.uint32_t(y),
		0.5, 0, C.uint32_t(e.cols), C.uint32_t(e.rows), false) == C.GHOSTTY_SUCCESS {
		C.crude_gesture_apply(e.selGesture, e.term, e.selRelease)
	}
	return e.HasSelection()
}

// HasSelection reports whether the terminal has an active selection.
func (e *Emulator) HasSelection() bool {
	if e == nil || e.term == nil || e.closed {
		return false
	}
	return bool(C.crude_has_selection(e.term))
}

// ClearSelection removes the active selection.
func (e *Emulator) ClearSelection() {
	if e == nil || e.term == nil || e.closed {
		return
	}
	if !bool(C.crude_has_selection(e.term)) {
		return
	}
	C.crude_install_selection(e.term, nil)
	e.invalidatePaint()
}

// SelectAll selects the whole screen including scrollback.
func (e *Emulator) SelectAll() {
	if e == nil || e.term == nil || e.closed {
		return
	}
	if C.crude_select_all(e.term) == C.GHOSTTY_SUCCESS {
		e.invalidatePaint()
	}
}

// SelectionText returns the active selection as plain text using Ghostty's
// clipboard semantics (soft wraps unwrapped, trailing whitespace trimmed).
func (e *Emulator) SelectionText() string {
	if e == nil || e.term == nil || e.closed {
		return ""
	}
	out := C.crude_selection_text(e.term)
	if out.result != C.GHOSTTY_SUCCESS || out.ptr == nil || out.len == 0 {
		return ""
	}
	text := C.GoStringN((*C.char)(unsafe.Pointer(out.ptr)), C.int(out.len))
	C.crude_free(unsafe.Pointer(out.ptr), out.len)
	return text
}

func (e *Emulator) clampCell(x, y int) (int, int) {
	if x < 0 {
		x = 0
	}
	if x >= e.cols {
		x = e.cols - 1
	}
	if y < 0 {
		y = 0
	}
	if y >= e.rows {
		y = e.rows - 1
	}
	return x, y
}
