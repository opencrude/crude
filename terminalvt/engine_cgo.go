//go:build cgo

package terminalvt

/*
#cgo CFLAGS: -I${SRCDIR}/../deps/libghostty-vt/include -DGHOSTTY_STATIC
#cgo linux LDFLAGS: ${SRCDIR}/../deps/libghostty-vt/lib/libghostty-vt.a -lm -lpthread -ldl
#cgo darwin LDFLAGS: ${SRCDIR}/../deps/libghostty-vt/lib/libghostty-vt.a -lm -lpthread
#cgo windows LDFLAGS: -L${SRCDIR}/../deps/libghostty-vt/lib -lghostty-vt-static -lntdll -lkernel32 -lws2_32 -ladvapi32 -lgcc
#include "ghostty_bridge.h"
*/
import "C"

import (
	"fmt"
	"runtime"
	"runtime/cgo"
	"strings"
	"time"
	"unsafe"
)

// Engine owns parsing and protocol state, never client selection or presentation.
// Its caller must serialize all methods.
type Engine struct {
	term         C.GhosttyTerminal
	render       C.GhosttyRenderState
	keyEnc       C.GhosttyKeyEncoder
	keyEv        C.GhosttyKeyEvent
	mouseEnc     C.GhosttyMouseEncoder
	mouseEv      C.GhosttyMouseEvent
	handle       cgo.Handle
	cols, rows   int
	scrollback   int
	closed       bool
	effects      Effects
	historyRef   C.GhosttyTrackedGridRef
	historyTail  C.GhosttyTrackedGridRef
	historyBase  uint64
	historyTotal int
	historyAlt   bool
	historyEpoch uint64
	// render holds exactly the completed active grid while DEC 2026 is set.
	synchronized *Projection
	syncStarted  time.Time
	now          func() time.Time
	readSequence uint64
	// streamGrid owns detached immutable rows shared by all paced viewers. Only
	// Ghostty dirty rows are exported again; ordinary row-page reads invalidate it.
	streamGrid *Projection
	// Pointer-free C transfer scratch for one actual-width row. Published rows
	// own an immutable copied string; neither C nor clients retain this storage.
	rowCells []C.crude_export_cell
	rowText  []byte
}

func New(opts Options) (*Engine, error) {
	if err := validateOptions(&opts); err != nil {
		return nil, err
	}
	e := &Engine{cols: opts.Cols, rows: opts.Rows}
	e.scrollback = opts.ScrollbackLines
	if rc := C.ghostty_terminal_new(nil, &e.term, C.uint16_t(opts.Cols), C.uint16_t(opts.Rows)); rc != C.GHOSTTY_SUCCESS {
		return nil, fmt.Errorf("%w: terminal_new: %d", ErrUnavailable, int(rc))
	}
	if rc := C.ghostty_terminal_resize(e.term, C.uint16_t(opts.Cols), C.uint16_t(opts.Rows), C.uint32_t(CellPixelWidth), C.uint32_t(CellPixelHeight)); rc != C.GHOSTTY_SUCCESS {
		C.ghostty_terminal_free(e.term)
		return nil, fmt.Errorf("%w: terminal_resize: %d", ErrUnavailable, int(rc))
	}
	if rc := C.ghostty_render_state_new(nil, &e.render); rc != C.GHOSTTY_SUCCESS {
		C.ghostty_terminal_free(e.term)
		return nil, fmt.Errorf("%w: render_state_new: %d", ErrUnavailable, int(rc))
	}
	if rc := C.ghostty_key_encoder_new(nil, &e.keyEnc); rc != C.GHOSTTY_SUCCESS {
		C.ghostty_render_state_free(e.render)
		C.ghostty_terminal_free(e.term)
		return nil, fmt.Errorf("%w: key_encoder_new: %d", ErrUnavailable, int(rc))
	}
	if rc := C.ghostty_key_event_new(nil, &e.keyEv); rc != C.GHOSTTY_SUCCESS {
		C.ghostty_key_encoder_free(e.keyEnc)
		C.ghostty_render_state_free(e.render)
		C.ghostty_terminal_free(e.term)
		return nil, fmt.Errorf("%w: key_event_new: %d", ErrUnavailable, int(rc))
	}
	if rc := C.ghostty_mouse_encoder_new(nil, &e.mouseEnc); rc != C.GHOSTTY_SUCCESS {
		C.ghostty_key_event_free(e.keyEv)
		C.ghostty_key_encoder_free(e.keyEnc)
		C.ghostty_render_state_free(e.render)
		C.ghostty_terminal_free(e.term)
		return nil, fmt.Errorf("%w: mouse_encoder_new: %d", ErrUnavailable, int(rc))
	}
	if rc := C.ghostty_mouse_event_new(nil, &e.mouseEv); rc != C.GHOSTTY_SUCCESS {
		C.ghostty_mouse_encoder_free(e.mouseEnc)
		C.ghostty_key_event_free(e.keyEv)
		C.ghostty_key_encoder_free(e.keyEnc)
		C.ghostty_render_state_free(e.render)
		C.ghostty_terminal_free(e.term)
		return nil, fmt.Errorf("%w: mouse_event_new: %d", ErrUnavailable, int(rc))
	}
	e.handle = cgo.NewHandle(e)
	e.historyEpoch = 1 // zero identifies a reader without a history publication
	C.crude_set_userdata(e.term, C.uintptr_t(e.handle))
	C.crude_set_write_pty(e.term)
	C.crude_set_title_changed(e.term)
	C.crude_set_bell(e.term)
	C.crude_set_pwd_changed(e.term)
	C.crude_set_clipboard_write(e.term)
	C.crude_set_xtversion(e.term)
	C.crude_set_size(e.term)
	C.crude_set_device_attributes(e.term)
	C.crude_set_terminfo_name(e.term)
	C.crude_set_scrollback_lines(e.term, C.size_t(opts.ScrollbackLines))
	// Ghostty's line limit is page-granular. Bound backing storage separately;
	// Rows publishes an exact line-capped window over its retained pages.
	C.crude_set_scrollback_bytes(e.term, C.size_t(MaxScrollbackBytes))
	// Default cursor blinks (DECSCUSR 0), like mainstream terminals; steady
	// DECSCUSR styles (2/4/6) turn it off per program request.
	C.crude_set_default_cursor_blink(e.term, true)

	return e, nil
}
func (e *Engine) Close() {
	if e == nil || e.closed {
		return
	}
	e.closed = true
	e.rowCells = nil
	e.rowText = nil
	C.ghostty_tracked_grid_ref_free(e.historyRef)
	C.ghostty_tracked_grid_ref_free(e.historyTail)
	e.historyRef = nil
	e.historyTail = nil
	if e.mouseEv != nil {
		C.ghostty_mouse_event_free(e.mouseEv)
		e.mouseEv = nil
	}
	if e.mouseEnc != nil {
		C.ghostty_mouse_encoder_free(e.mouseEnc)
		e.mouseEnc = nil
	}
	if e.keyEv != nil {
		C.ghostty_key_event_free(e.keyEv)
		e.keyEv = nil
	}
	if e.keyEnc != nil {
		C.ghostty_key_encoder_free(e.keyEnc)
		e.keyEnc = nil
	}
	if e.render != nil {
		C.ghostty_render_state_free(e.render)
		e.render = nil
	}
	if e.term != nil {
		C.ghostty_terminal_free(e.term)
		e.term = nil
	}
	if e.handle != 0 {
		e.handle.Delete()
		e.handle = 0
	}
}

func emulatorFromUserdata(ud unsafe.Pointer) *Engine {
	if ud == nil {
		return nil
	}
	return cgo.Handle(uintptr(ud)).Value().(*Engine)
}

//export neutralgoWritePtyTrampoline
func neutralgoWritePtyTrampoline(_ C.GhosttyTerminal, userdata unsafe.Pointer, data *C.uint8_t, length C.size_t) {
	e := emulatorFromUserdata(userdata)
	if e == nil || length == 0 || data == nil {
		return
	}
	buf := C.GoBytes(unsafe.Pointer(data), C.int(length))
	e.effects.Replies = append(e.effects.Replies, buf)
}

func goGhosttyString(s C.GhosttyString) string {
	if s.ptr == nil || s.len == 0 {
		return ""
	}
	return C.GoStringN((*C.char)(unsafe.Pointer(s.ptr)), C.int(s.len))
}

//export neutralgoTitleChangedTrampoline
func neutralgoTitleChangedTrampoline(term C.GhosttyTerminal, userdata unsafe.Pointer) {
	e := emulatorFromUserdata(userdata)
	if e == nil {
		return
	}
	var s C.GhosttyString
	if C.ghostty_terminal_get(term, C.GHOSTTY_TERMINAL_DATA_TITLE, unsafe.Pointer(&s)) != C.GHOSTTY_SUCCESS {
		return
	}
	// libghostty invokes this synchronously inside vt_write. Only record the
	// title here; Feed delivers it to onTitle after the C call returns so
	// handlers never re-enter libghostty while it is mid-parse.
	e.effects.Title = goGhosttyString(s)
	e.effects.TitleChanged = true
}

//export neutralgoBellTrampoline
func neutralgoBellTrampoline(_ C.GhosttyTerminal, userdata unsafe.Pointer) {
	if e := emulatorFromUserdata(userdata); e != nil {
		e.effects.Bell = true
	}
}

//export neutralgoPwdChangedTrampoline
func neutralgoPwdChangedTrampoline(term C.GhosttyTerminal, userdata unsafe.Pointer) {
	e := emulatorFromUserdata(userdata)
	if e == nil {
		return
	}
	var s C.GhosttyString
	if C.ghostty_terminal_get(term, C.GHOSTTY_TERMINAL_DATA_PWD, unsafe.Pointer(&s)) != C.GHOSTTY_SUCCESS {
		return
	}
	e.effects.CWD = goGhosttyString(s)
	e.effects.CWDChanged = true
}

//export neutralgoClipboardWriteTrampoline
func neutralgoClipboardWriteTrampoline(_ C.GhosttyTerminal, userdata unsafe.Pointer, write *C.GhosttyClipboardWrite) C.GhosttyClipboardWriteResult {
	e := emulatorFromUserdata(userdata)
	if e == nil || write == nil || write.contents_len == 0 {
		return C.GHOSTTY_CLIPBOARD_WRITE_RESULT_SUCCESS
	}
	// OSC 52 / OSC 1337 writes are normalized to decoded MIME entries.
	// Prefer text/plain; otherwise take the first representation.
	contents := unsafe.Slice(write.contents, int(write.contents_len))
	text := ""
	for i := range contents {
		mime := goGhosttyString(contents[i].mime)
		if strings.HasPrefix(mime, "text/plain") {
			text = goGhosttyString(contents[i].data)
			break
		}
		if i == 0 {
			text = goGhosttyString(contents[i].data)
		}
	}
	e.effects.Clipboard = append(e.effects.Clipboard, []byte(text))
	return C.GHOSTTY_CLIPBOARD_WRITE_RESULT_SUCCESS
}

func (e *Engine) Feed(data []byte) Effects {
	if e == nil || e.closed || len(data) == 0 {
		return Effects{}
	}
	e.effects = Effects{}
	e.expireSynchronizedOutput()
	for at := 0; at < len(data); {
		at += int(C.crude_write_visual_boundary(e.term, (*C.uint8_t)(unsafe.Pointer(&data[at])), C.size_t(len(data)-at)))
		if bool(C.crude_mode_value(e.term, C.GHOSTTY_MODE_SYNC_OUTPUT)) {
			if e.synchronized == nil {
				// A transition is observed before any following bytes are fed,
				// even when end/begin and another erase share one PTY chunk.
				p, err := e.rowMetadata(-1)
				if err == nil && C.ghostty_render_state_update(e.render, e.term) == C.GHOSTTY_SUCCESS {
					p.Synchronized = true
					e.synchronized = &p
					e.syncStarted = e.clock()
				} else {
					C.crude_end_sync_output(e.term)
				}
			}
		} else {
			e.synchronized = nil
		}
	}
	runtime.KeepAlive(data)
	out := e.effects
	e.effects = Effects{}
	return out
}
func (e *Engine) Resize(cols, rows int) error {
	opts := Options{Cols: cols, Rows: rows}
	if err := validateOptions(&opts); err != nil {
		return err
	}
	cols, rows = opts.Cols, opts.Rows
	if e == nil || e.closed {
		return ErrClosed
	}
	if cols == e.cols && rows == e.rows {
		return nil
	}
	if rc := C.ghostty_terminal_resize(e.term, C.uint16_t(cols), C.uint16_t(rows), 8, 16); rc != C.GHOSTTY_SUCCESS {
		return fmt.Errorf("resize: %d", rc)
	}
	e.cols, e.rows = cols, rows
	e.rowCells = nil
	e.rowText = nil
	e.streamGrid = nil
	e.synchronized = nil // Ghostty resize releases DEC 2026 as specified.
	C.ghostty_tracked_grid_ref_free(e.historyRef)
	C.ghostty_tracked_grid_ref_free(e.historyTail)
	e.historyRef = nil
	e.historyTail = nil
	e.historyBase = 0
	e.historyEpoch++
	return nil
}

func (e *Engine) clock() time.Time {
	if e.now != nil {
		return e.now()
	}
	return time.Now()
}

func (e *Engine) expireSynchronizedOutput() {
	if e.synchronized != nil && e.clock().Sub(e.syncStarted) >= SynchronizedOutputTimeout {
		C.crude_end_sync_output(e.term)
		e.synchronized = nil
	}
}

// BracketedPaste reports DEC mode 2004.
func (e *Engine) BracketedPaste() bool {
	if e == nil || e.term == nil {
		return false
	}
	return bool(C.crude_mode_value(e.term, C.GHOSTTY_MODE_BRACKETED_PASTE))
}

// MouseTracking reports whether the child enabled any mouse tracking mode.
func (e *Engine) MouseTracking() bool {
	if e == nil || e.term == nil {
		return false
	}
	var mode C.GhosttyMouseTrackingMode
	if C.ghostty_terminal_get(e.term, C.GHOSTTY_TERMINAL_DATA_MOUSE_TRACKING, unsafe.Pointer(&mode)) != C.GHOSTTY_SUCCESS {
		return false
	}
	return mode != C.GHOSTTY_MOUSE_TRACKING_NONE
}

// AltScreen reports whether the alternate screen is active.
func (e *Engine) AltScreen() bool {
	if e == nil || e.term == nil {
		return false
	}
	return bool(C.crude_active_screen_alt(e.term))
}

// FocusReporting reports DEC mode 1004 (focus in/out events).
func (e *Engine) FocusReporting() bool {
	if e == nil || e.term == nil {
		return false
	}
	return bool(C.crude_mode_value(e.term, C.GHOSTTY_MODE_FOCUS_EVENT))
}

// CursorBlink reports whether the terminal cursor style requests blinking
// (DECSCUSR blink styles / DEC mode 12).
func (e *Engine) CursorBlink() bool {
	if e == nil || e.term == nil {
		return false
	}
	return bool(C.crude_mode_value(e.term, C.GHOSTTY_MODE_CURSOR_BLINKING))
}

// Cursor reports the 0-based viewport cursor and whether it should show.
func (e *Engine) Cursor() (x, y int, visible bool) {
	if e == nil || e.term == nil {
		return 0, 0, false
	}
	var cx, cy C.uint16_t
	var vis C.bool
	C.ghostty_terminal_get(e.term, C.GHOSTTY_TERMINAL_DATA_CURSOR_X, unsafe.Pointer(&cx))
	C.ghostty_terminal_get(e.term, C.GHOSTTY_TERMINAL_DATA_CURSOR_Y, unsafe.Pointer(&cy))
	C.ghostty_terminal_get(e.term, C.GHOSTTY_TERMINAL_DATA_CURSOR_VISIBLE, unsafe.Pointer(&vis))
	return int(cx), int(cy), bool(vis)
}

// Scrollbar is offset/total/visible rows for a scrollbar.
func (e *Engine) Scrollbar() (offset, total, visible int) {
	if e == nil || e.term == nil {
		return 0, 0, 0
	}
	var bar C.GhosttyTerminalScrollbar
	if C.ghostty_terminal_get(e.term, C.GHOSTTY_TERMINAL_DATA_SCROLLBAR, unsafe.Pointer(&bar)) != C.GHOSTTY_SUCCESS {
		return 0, e.rows, e.rows
	}
	return int(bar.offset), int(bar.total), int(bar.len)
}
