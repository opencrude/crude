//go:build cgo

package terminalvt

/*
#include "ghostty_bridge.h"
*/
import "C"

import "unsafe"

// Snapshot returns the active screen and its scrollback as VT sequences:
// text with its colors and attributes, no modes, palette or cursor state. A
// new engine that is fed the snapshot shows the same rows, so a terminal can
// come back with its buffer after its process is gone. One parser owns both
// directions; the bytes are ordinary terminal output.
func (e *Engine) Snapshot() ([]byte, error) {
	if e == nil || e.closed {
		return nil, ErrClosed
	}
	var options C.GhosttyFormatterTerminalOptions
	options.size = C.size_t(unsafe.Sizeof(options))
	options.emit = C.GHOSTTY_FORMATTER_FORMAT_VT
	options.trim = true
	options.extra.size = C.size_t(unsafe.Sizeof(options.extra))
	options.extra.screen.size = C.size_t(unsafe.Sizeof(options.extra.screen))
	var formatter C.GhosttyFormatter
	if C.ghostty_formatter_terminal_new(nil, &formatter, e.term, options) != C.GHOSTTY_SUCCESS {
		return nil, ErrUnavailable
	}
	defer C.ghostty_formatter_free(formatter)
	var ptr *C.uint8_t
	var size C.size_t
	if C.ghostty_formatter_format_alloc(formatter, nil, &ptr, &size) != C.GHOSTTY_SUCCESS {
		return nil, ErrUnavailable
	}
	if ptr == nil {
		return nil, nil
	}
	defer C.ghostty_free(nil, ptr, size)
	return C.GoBytes(unsafe.Pointer(ptr), C.int(size)), nil
}
