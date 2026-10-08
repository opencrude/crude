//go:build cgo

package term

/*
#cgo CFLAGS: -I${SRCDIR}/../../deps/libghostty-vt/include -DGHOSTTY_STATIC
#cgo linux LDFLAGS: ${SRCDIR}/../../deps/libghostty-vt/lib/libghostty-vt.a -lm -lpthread -ldl
#cgo darwin LDFLAGS: ${SRCDIR}/../../deps/libghostty-vt/lib/libghostty-vt.a -lm -lpthread
#cgo windows LDFLAGS: -L${SRCDIR}/../../deps/libghostty-vt/lib -lghostty-vt-static -lntdll -lkernel32 -lws2_32 -ladvapi32 -lgcc
#include "ghostty_bridge.h"
*/
import "C"

import (
	"fmt"
	"runtime"
	"runtime/cgo"
	"strings"
	"unsafe"

	"crude/tui/cell"
	"crude/tui/surface"
	"crude/types"
)

// Emulator is a libghostty-vt terminal painted into Crude's CellBuffer.
// All methods on one instance must be serialized (Bubble Tea's update loop).
type Emulator struct {
	term            C.GhosttyTerminal
	render          C.GhosttyRenderState
	keyEnc          C.GhosttyKeyEncoder
	keyEv           C.GhosttyKeyEvent
	mouseEnc        C.GhosttyMouseEncoder
	mouseEv         C.GhosttyMouseEvent
	selGesture      C.GhosttySelectionGesture
	selPress        C.GhosttySelectionGestureEvent
	selDrag         C.GhosttySelectionGestureEvent
	selRelease      C.GhosttySelectionGestureEvent
	selAnchorX      int
	selAnchorY      int
	handle          cgo.Handle
	cols            int
	rows            int
	title           string
	pwd             string
	onTitle         func(string)
	onReply         func([]byte)
	onBell          func()
	onPwd           func(string)
	onClipboard     func(string)
	closed          bool
	pendingTitle    string
	hasPendingTitle bool
	pendingBell     bool
	pendingPwd      string
	hasPendingPwd   bool
	pendingClip     string
	hasPendingClip  bool
	paintCache      *surface.CellBuffer
	paintDirty      bool
	paintFG         cell.Color
	paintBG         cell.Color
	defColors       bool
	defFG           cell.Color
	defBG           cell.Color
	curFocused      bool
	curBlinkOn      bool
	curColor        cell.Color
	selFG           cell.Color
	selBG           cell.Color
}

// NewEmulator creates an 80x24 libghostty-vt terminal.
func NewEmulator() (*Emulator, error) {
	e := &Emulator{cols: 80, rows: 24, curFocused: true, curBlinkOn: true}
	if rc := C.ghostty_terminal_new(nil, &e.term, 80, 24); rc != C.GHOSTTY_SUCCESS {
		return nil, fmt.Errorf("%w: terminal_new: %d", ErrUnavailable, int(rc))
	}
	if rc := C.ghostty_terminal_resize(e.term, 80, 24, C.uint32_t(CellPixelWidth), C.uint32_t(CellPixelHeight)); rc != C.GHOSTTY_SUCCESS {
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
	// Selection gesture state machine + reusable events. Failures leave the
	// pointers nil; selection methods no-op.
	C.ghostty_selection_gesture_new(nil, &e.selGesture)
	C.ghostty_selection_gesture_event_new(nil, &e.selPress, C.GHOSTTY_SELECTION_GESTURE_EVENT_TYPE_PRESS)
	C.ghostty_selection_gesture_event_new(nil, &e.selDrag, C.GHOSTTY_SELECTION_GESTURE_EVENT_TYPE_DRAG)
	C.ghostty_selection_gesture_event_new(nil, &e.selRelease, C.GHOSTTY_SELECTION_GESTURE_EVENT_TYPE_RELEASE)

	e.handle = cgo.NewHandle(e)
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
	C.crude_set_scrollback_lines(e.term, C.size_t(defaultScrollbackLines))
	// Default cursor blinks (DECSCUSR 0), like mainstream terminals; steady
	// DECSCUSR styles (2/4/6) turn it off per program request.
	C.crude_set_default_cursor_blink(e.term, true)
	_ = types.GhosttyTermName

	runtime.SetFinalizer(e, (*Emulator).finalize)
	return e, nil
}

func (e *Emulator) finalize() { e.Close() }

// Close releases libghostty-vt handles. Safe to call more than once.
func (e *Emulator) Close() {
	if e == nil || e.closed {
		return
	}
	e.closed = true
	runtime.SetFinalizer(e, nil)
	if e.selRelease != nil {
		C.ghostty_selection_gesture_event_free(e.selRelease)
		e.selRelease = nil
	}
	if e.selDrag != nil {
		C.ghostty_selection_gesture_event_free(e.selDrag)
		e.selDrag = nil
	}
	if e.selPress != nil {
		C.ghostty_selection_gesture_event_free(e.selPress)
		e.selPress = nil
	}
	if e.selGesture != nil {
		C.ghostty_selection_gesture_free(e.selGesture, e.term)
		e.selGesture = nil
	}
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

func emulatorFromUserdata(ud unsafe.Pointer) *Emulator {
	if ud == nil {
		return nil
	}
	return cgo.Handle(uintptr(ud)).Value().(*Emulator)
}

//export goWritePtyTrampoline
func goWritePtyTrampoline(_ C.GhosttyTerminal, userdata unsafe.Pointer, data *C.uint8_t, length C.size_t) {
	e := emulatorFromUserdata(userdata)
	if e == nil || e.onReply == nil || length == 0 || data == nil {
		return
	}
	buf := C.GoBytes(unsafe.Pointer(data), C.int(length))
	e.onReply(buf)
}

func goGhosttyString(s C.GhosttyString) string {
	if s.ptr == nil || s.len == 0 {
		return ""
	}
	return C.GoStringN((*C.char)(unsafe.Pointer(s.ptr)), C.int(s.len))
}

//export goTitleChangedTrampoline
func goTitleChangedTrampoline(term C.GhosttyTerminal, userdata unsafe.Pointer) {
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
	e.pendingTitle = goGhosttyString(s)
	e.hasPendingTitle = true
}

//export goBellTrampoline
func goBellTrampoline(_ C.GhosttyTerminal, userdata unsafe.Pointer) {
	if e := emulatorFromUserdata(userdata); e != nil {
		e.pendingBell = true
	}
}

//export goPwdChangedTrampoline
func goPwdChangedTrampoline(term C.GhosttyTerminal, userdata unsafe.Pointer) {
	e := emulatorFromUserdata(userdata)
	if e == nil {
		return
	}
	var s C.GhosttyString
	if C.ghostty_terminal_get(term, C.GHOSTTY_TERMINAL_DATA_PWD, unsafe.Pointer(&s)) != C.GHOSTTY_SUCCESS {
		return
	}
	e.pendingPwd = goGhosttyString(s)
	e.hasPendingPwd = true
}

//export goClipboardWriteTrampoline
func goClipboardWriteTrampoline(_ C.GhosttyTerminal, userdata unsafe.Pointer, write *C.GhosttyClipboardWrite) C.GhosttyClipboardWriteResult {
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
	e.pendingClip = text
	e.hasPendingClip = true
	return C.GHOSTTY_CLIPBOARD_WRITE_RESULT_SUCCESS
}

// SetTitleHandler is called when OSC 0/2 sets the window title.
func (e *Emulator) SetTitleHandler(fn func(string)) {
	if e != nil {
		e.onTitle = fn
	}
}

// SetReplyHandler receives terminal-generated PTY replies (DA, DSR, size, …).
// The callback must not call Feed on this emulator.
func (e *Emulator) SetReplyHandler(fn func([]byte)) {
	if e != nil {
		e.onReply = fn
	}
}

// SetBellHandler is called after Feed when the child rang BEL.
func (e *Emulator) SetBellHandler(fn func()) {
	if e != nil {
		e.onBell = fn
	}
}

// SetPwdHandler is called after Feed when OSC 7 reports a new working dir.
func (e *Emulator) SetPwdHandler(fn func(string)) {
	if e != nil {
		e.onPwd = fn
	}
}

// SetClipboardHandler is called after Feed when the child wrote the
// clipboard via OSC 52 / OSC 1337 (decoded text).
func (e *Emulator) SetClipboardHandler(fn func(string)) {
	if e != nil {
		e.onClipboard = fn
	}
}

// PWD returns the last OSC 7 working directory (may be a file:// URL).
func (e *Emulator) PWD() string {
	if e == nil {
		return ""
	}
	return e.pwd
}

// Title returns the last OSC title.
func (e *Emulator) Title() string {
	if e == nil {
		return ""
	}
	return e.title
}

// Cols and Rows are the current grid size.
func (e *Emulator) Cols() int {
	if e == nil {
		return 0
	}
	return e.cols
}
func (e *Emulator) Rows() int {
	if e == nil {
		return 0
	}
	return e.rows
}

// Cursor reports the 0-based viewport cursor and whether it should show.
func (e *Emulator) Cursor() (x, y int, visible bool) {
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

// Resize changes the grid. Primary-screen content reflows when wrap is on.
func (e *Emulator) Resize(cols, rows int) {
	if e == nil || e.term == nil {
		return
	}
	if cols < 2 {
		cols = 2
	}
	if rows < 1 {
		rows = 1
	}
	if cols == e.cols && rows == e.rows {
		return
	}
	C.ghostty_terminal_resize(e.term, C.uint16_t(cols), C.uint16_t(rows),
		C.uint32_t(CellPixelWidth), C.uint32_t(CellPixelHeight))
	e.cols, e.rows = cols, rows
	e.invalidatePaint()
}

// Feed parses bytes from the PTY into libghostty-vt.
func (e *Emulator) Feed(data []byte) {
	if e == nil || e.term == nil || e.closed || len(data) == 0 {
		return
	}
	C.ghostty_terminal_vt_write(e.term, (*C.uint8_t)(unsafe.Pointer(&data[0])), C.size_t(len(data)))
	runtime.KeepAlive(data)
	runtime.KeepAlive(e)
	e.invalidatePaint()
	e.flushEffects()
}

// flushEffects delivers title/bell/pwd/clipboard effects collected during
// vt_write after the C call has fully returned, so handlers never re-enter
// libghostty while it is mid-parse.
func (e *Emulator) flushEffects() {
	if e.hasPendingTitle {
		t := e.pendingTitle
		e.hasPendingTitle = false
		e.pendingTitle = ""
		if e.title != t {
			e.title = t
			if e.onTitle != nil {
				e.onTitle(t)
			}
		}
	}
	if e.pendingBell {
		e.pendingBell = false
		if e.onBell != nil {
			e.onBell()
		}
	}
	if e.hasPendingPwd {
		p := e.pendingPwd
		e.hasPendingPwd = false
		e.pendingPwd = ""
		if e.pwd != p {
			e.pwd = p
			if e.onPwd != nil {
				e.onPwd(p)
			}
		}
	}
	if e.hasPendingClip {
		c := e.pendingClip
		e.hasPendingClip = false
		e.pendingClip = ""
		if e.onClipboard != nil {
			e.onClipboard(c)
		}
	}
}

// BracketedPaste reports DEC mode 2004.
func (e *Emulator) BracketedPaste() bool {
	if e == nil || e.term == nil {
		return false
	}
	return bool(C.crude_mode_value(e.term, C.GHOSTTY_MODE_BRACKETED_PASTE))
}

// MouseTracking reports whether the child enabled any mouse tracking mode.
func (e *Emulator) MouseTracking() bool {
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
func (e *Emulator) AltScreen() bool {
	if e == nil || e.term == nil {
		return false
	}
	return bool(C.crude_active_screen_alt(e.term))
}

// FocusReporting reports DEC mode 1004 (focus in/out events).
func (e *Emulator) FocusReporting() bool {
	if e == nil || e.term == nil {
		return false
	}
	return bool(C.crude_mode_value(e.term, C.GHOSTTY_MODE_FOCUS_EVENT))
}

// ViewportPinned reports whether the viewport follows the active area
// (false while the user is scrolled into history).
func (e *Emulator) ViewportPinned() bool {
	if e == nil || e.term == nil {
		return true
	}
	return bool(C.crude_viewport_pinned(e.term))
}

// CursorBlink reports whether the terminal cursor style requests blinking
// (DECSCUSR blink styles / DEC mode 12).
func (e *Emulator) CursorBlink() bool {
	if e == nil || e.term == nil {
		return false
	}
	return bool(C.crude_mode_value(e.term, C.GHOSTTY_MODE_CURSOR_BLINKING))
}

// SetCursorState drives cursor presentation: unfocused panes render a dim
// hollow-style cursor; focused panes hide the cursor on the off blink phase
// (only when the terminal's cursor style requests blinking).
// It reports whether the visible cursor state changed.
func (e *Emulator) SetCursorState(focused, blinkOn bool) bool {
	if e == nil {
		return false
	}
	if e.curFocused == focused && e.curBlinkOn == blinkOn {
		return false
	}
	blinkRelevant := focused && e.CursorBlink()
	changed := e.curFocused != focused || (blinkRelevant && e.curBlinkOn != blinkOn)
	e.curFocused = focused
	e.curBlinkOn = blinkOn
	if changed {
		e.invalidatePaint()
	}
	return changed
}

// ScrollDelta moves the viewport (negative is up).
func (e *Emulator) ScrollDelta(n int) {
	if e == nil || e.term == nil || n == 0 {
		return
	}
	C.crude_scroll_delta(e.term, C.intptr_t(n))
	e.invalidatePaint()
}

// ScrollTop moves the viewport to the oldest scrollback.
func (e *Emulator) ScrollTop() {
	if e == nil || e.term == nil {
		return
	}
	C.crude_scroll_top(e.term)
	e.invalidatePaint()
}

// ScrollBottom moves the viewport to the active area.
func (e *Emulator) ScrollBottom() {
	if e == nil || e.term == nil {
		return
	}
	C.crude_scroll_bottom(e.term)
	e.invalidatePaint()
}

// Scrollbar is offset/total/visible rows for a scrollbar.
func (e *Emulator) Scrollbar() (offset, total, visible int) {
	if e == nil || e.term == nil {
		return 0, 0, 0
	}
	var bar C.GhosttyTerminalScrollbar
	if C.ghostty_terminal_get(e.term, C.GHOSTTY_TERMINAL_DATA_SCROLLBAR, unsafe.Pointer(&bar)) != C.GHOSTTY_SUCCESS {
		return 0, e.rows, e.rows
	}
	return int(bar.offset), int(bar.total), int(bar.len)
}

// SetDefaultColors updates Ghostty's embedder default fg/bg (theme).
// Unchanged colors are a no-op: theme tokens are re-applied on every window
// render, and needlessly writing defaults invalidates the paint cache every
// frame (60 Hz churn while a floating terminal is otherwise idle).
func (e *Emulator) SetDefaultColors(fg, bg cell.Color) {
	if e == nil || e.term == nil {
		return
	}
	if e.defColors && e.defFG == fg && e.defBG == bg {
		return
	}
	e.defColors = true
	e.defFG = fg
	e.defBG = bg
	fr, fgG, fb := colorRGB(fg)
	br, bgG, bb := colorRGB(bg)
	C.crude_set_default_fg(e.term, C.uint8_t(fr), C.uint8_t(fgG), C.uint8_t(fb))
	C.crude_set_default_bg(e.term, C.uint8_t(br), C.uint8_t(bgG), C.uint8_t(bb))
	e.invalidatePaint()
}
func (e *Emulator) SetCursorColor(color cell.Color) {
	if e == nil || e.curColor.Equal(color) {
		return
	}
	e.curColor = color
	e.invalidatePaint()
}

// SetSelectionColors sets the embedder's text-range ink and fill without
// changing Ghostty's selection or the child's terminal colors.
func (e *Emulator) SetSelectionColors(fg, bg cell.Color) {
	if e == nil || (e.selFG.Equal(fg) && e.selBG.Equal(bg)) {
		return
	}
	e.selFG, e.selBG = fg, bg
	e.invalidatePaint()
}

func (e *Emulator) invalidatePaint() {
	if e != nil {
		e.paintDirty = true
	}
}

func colorRGB(c cell.Color) (r, g, b uint8) {
	switch c.Type {
	case cell.ColorTrueColor:
		return c.R, c.G, c.B
	case cell.ColorPalette:
		return paletteRGB(c.Palette)
	default:
		return 200, 200, 200
	}
}

func paletteRGB(idx uint8) (r, g, b uint8) {
	switch idx {
	case 0:
		return 0, 0, 0
	case 1:
		return 205, 0, 0
	case 2:
		return 0, 205, 0
	case 3:
		return 205, 205, 0
	case 4:
		return 0, 0, 205
	case 5:
		return 205, 0, 205
	case 6:
		return 0, 205, 205
	case 7:
		return 229, 229, 229
	case 8:
		return 127, 127, 127
	case 9:
		return 255, 0, 0
	case 10:
		return 0, 255, 0
	case 11:
		return 255, 255, 0
	case 12:
		return 0, 0, 255
	case 13:
		return 255, 0, 255
	case 14:
		return 255, 255, 255
	case 15:
		return 255, 255, 255
	}
	if idx >= 232 {
		v := uint8(8 + 10*(int(idx)-232))
		return v, v, v
	}
	i := int(idx) - 16
	ri, gi, bi := (i/36)*40, ((i%36)/6)*40, (i%6)*40
	if ri > 0 {
		ri += 55
	}
	if gi > 0 {
		gi += 55
	}
	if bi > 0 {
		bi += 55
	}
	return uint8(ri), uint8(gi), uint8(bi)
}
