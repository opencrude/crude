//go:build cgo

package terminalvt

/*
#include "ghostty_bridge.h"
*/
import "C"

import (
	"runtime"
	"unsafe"
)

// EncodeKey converts a Bubble Tea key press into PTY bytes using Ghostty's
// terminal-aware encoder (application cursor, keypad, Kitty flags, …).
func (e *Engine) EncodeKey(msg Key) []byte {
	if e == nil || e.term == nil || e.keyEnc == nil || e.keyEv == nil || e.closed {
		return nil
	}
	if printable := printableKeyText(msg); printable != "" {
		return []byte(printable)
	}
	press := e.encodeKeyAction(msg, C.GHOSTTY_KEY_ACTION_PRESS)
	if C.crude_kitty_flags(e.term)&C.GHOSTTY_KITTY_KEY_REPORT_EVENTS == 0 {
		return press
	}
	rel := e.encodeKeyAction(msg, C.GHOSTTY_KEY_ACTION_RELEASE)
	if len(rel) == 0 {
		return press
	}
	out := make([]byte, 0, len(press)+len(rel))
	out = append(out, press...)
	return append(out, rel...)
}

func printableKeyText(msg Key) string {
	if msg.Mod.Contains(ModCtrl) || msg.Mod.Contains(ModAlt) || msg.Mod.Contains(ModSuper) {
		return ""
	}
	if msg.Text == "" || containsC0(msg.Text) {
		return ""
	}
	switch msg.Code {
	case KeyEnter, KeyTab, KeyBackspace, KeyEscape,
		KeyUp, KeyDown, KeyLeft, KeyRight,
		KeyHome, KeyEnd, KeyPgUp, KeyPgDown,
		KeyInsert, KeyDelete,
		KeyF1, KeyF2, KeyF3, KeyF4, KeyF5, KeyF6,
		KeyF7, KeyF8, KeyF9, KeyF10, KeyF11, KeyF12:
		return ""
	}
	return msg.Text
}

func (e *Engine) encodeKeyAction(msg Key, action C.GhosttyKeyAction) []byte {
	C.ghostty_key_encoder_setopt_from_terminal(e.keyEnc, e.term)
	C.ghostty_key_event_set_action(e.keyEv, action)
	C.ghostty_key_event_set_key(e.keyEv, mapGhosttyKey(msg.Code))
	C.ghostty_key_event_set_mods(e.keyEv, mapGhosttyMods(msg.Mod))
	var codepoint C.uint32_t
	if msg.Code >= 0x20 && msg.Code <= 0x10ffff {
		codepoint = C.uint32_t(unshiftedKeyCode(msg.Code))
	}
	C.ghostty_key_event_set_unshifted_codepoint(e.keyEv, codepoint)
	text := msg.Text
	if containsC0(text) {
		text = ""
	}
	// Terminal protocols often provide only Code for modified printable keys.
	// Ghostty requires their pre-Control text, not the resulting C0 byte. In
	// particular Ctrl+I/M/[ are ambiguous without it. Keep encoding with the
	// terminal-aware owner (including Kitty/modifyOtherKeys), not a C0 fallback.
	if text == "" && msg.Code >= 0x20 && msg.Code <= 0x7e {
		logical := msg.Code
		if msg.Mod.Contains(ModShift) && logical >= 'a' && logical <= 'z' {
			logical = logical - 'a' + 'A'
		}
		text = string(logical)
	}
	if action != C.GHOSTTY_KEY_ACTION_PRESS {
		text = ""
	}
	var utf8 []byte
	if text != "" {
		utf8 = []byte(text)
		C.ghostty_key_event_set_utf8(e.keyEv, (*C.char)(unsafe.Pointer(&utf8[0])), C.size_t(len(utf8)))
	} else {
		C.ghostty_key_event_set_utf8(e.keyEv, nil, 0)
	}
	buf := make([]byte, 128)
	var written C.size_t
	rc := C.ghostty_key_encoder_encode(e.keyEnc, e.keyEv, (*C.char)(unsafe.Pointer(&buf[0])), C.size_t(len(buf)), &written)
	if rc == C.GHOSTTY_OUT_OF_SPACE && written > 0 {
		buf = make([]byte, int(written))
		rc = C.ghostty_key_encoder_encode(e.keyEnc, e.keyEv, (*C.char)(unsafe.Pointer(&buf[0])), C.size_t(len(buf)), &written)
	}
	runtime.KeepAlive(utf8)
	if rc != C.GHOSTTY_SUCCESS || written == 0 {
		return nil
	}
	out := make([]byte, int(written))
	copy(out, buf[:written])
	return out
}

func containsC0(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] == 0x7f {
			return true
		}
	}
	return false
}

func mapGhosttyMods(mod Modifiers) C.GhosttyMods {
	var m C.GhosttyMods
	if mod.Contains(ModShift) {
		m |= C.GHOSTTY_MODS_SHIFT
	}
	if mod.Contains(ModCtrl) {
		m |= C.GHOSTTY_MODS_CTRL
	}
	if mod.Contains(ModAlt) {
		m |= C.GHOSTTY_MODS_ALT
	}
	if mod.Contains(ModSuper) {
		m |= C.GHOSTTY_MODS_SUPER
	}
	return m
}

func mapGhosttyKey(code rune) C.GhosttyKey {
	code = unshiftedKeyCode(code)
	switch code {
	case KeyEnter:
		return C.GHOSTTY_KEY_ENTER
	case KeyTab:
		return C.GHOSTTY_KEY_TAB
	case KeyBackspace:
		return C.GHOSTTY_KEY_BACKSPACE
	case KeyEscape:
		return C.GHOSTTY_KEY_ESCAPE
	case KeyUp:
		return C.GHOSTTY_KEY_ARROW_UP
	case KeyDown:
		return C.GHOSTTY_KEY_ARROW_DOWN
	case KeyLeft:
		return C.GHOSTTY_KEY_ARROW_LEFT
	case KeyRight:
		return C.GHOSTTY_KEY_ARROW_RIGHT
	case KeyHome:
		return C.GHOSTTY_KEY_HOME
	case KeyEnd:
		return C.GHOSTTY_KEY_END
	case KeyPgUp:
		return C.GHOSTTY_KEY_PAGE_UP
	case KeyPgDown:
		return C.GHOSTTY_KEY_PAGE_DOWN
	case KeyInsert:
		return C.GHOSTTY_KEY_INSERT
	case KeyDelete:
		return C.GHOSTTY_KEY_DELETE
	case KeySpace:
		return C.GHOSTTY_KEY_SPACE
	case KeyF1:
		return C.GHOSTTY_KEY_F1
	case KeyF2:
		return C.GHOSTTY_KEY_F2
	case KeyF3:
		return C.GHOSTTY_KEY_F3
	case KeyF4:
		return C.GHOSTTY_KEY_F4
	case KeyF5:
		return C.GHOSTTY_KEY_F5
	case KeyF6:
		return C.GHOSTTY_KEY_F6
	case KeyF7:
		return C.GHOSTTY_KEY_F7
	case KeyF8:
		return C.GHOSTTY_KEY_F8
	case KeyF9:
		return C.GHOSTTY_KEY_F9
	case KeyF10:
		return C.GHOSTTY_KEY_F10
	case KeyF11:
		return C.GHOSTTY_KEY_F11
	case KeyF12:
		return C.GHOSTTY_KEY_F12
	case '`':
		return C.GHOSTTY_KEY_BACKQUOTE
	case '-':
		return C.GHOSTTY_KEY_MINUS
	case '=':
		return C.GHOSTTY_KEY_EQUAL
	case '[':
		return C.GHOSTTY_KEY_BRACKET_LEFT
	case ']':
		return C.GHOSTTY_KEY_BRACKET_RIGHT
	case '\\':
		return C.GHOSTTY_KEY_BACKSLASH
	case ';':
		return C.GHOSTTY_KEY_SEMICOLON
	case '\'':
		return C.GHOSTTY_KEY_QUOTE
	case ',':
		return C.GHOSTTY_KEY_COMMA
	case '.':
		return C.GHOSTTY_KEY_PERIOD
	case '/':
		return C.GHOSTTY_KEY_SLASH
	}
	if code >= '0' && code <= '9' {
		return C.GHOSTTY_KEY_DIGIT_0 + C.GhosttyKey(code-'0')
	}
	if code >= 'a' && code <= 'z' {
		return C.GHOSTTY_KEY_A + C.GhosttyKey(code-'a')
	}
	if code >= 'A' && code <= 'Z' {
		return C.GHOSTTY_KEY_A + C.GhosttyKey(code-'A')
	}
	return C.GHOSTTY_KEY_UNIDENTIFIED
}

// Logical shifted ASCII key values arrive from terminal protocols without a
// physical key. Map that value back to its key; the modifier and UTF-8 payload
// above retain the original logical symbol for Ghostty's protocol decisions.
func unshiftedKeyCode(code rune) rune {
	if code >= 'A' && code <= 'Z' {
		return code - 'A' + 'a'
	}
	const shifted = "~!@#$%^&*()_+{}|:\"<>?"
	const base = "`1234567890-=[]\\;',./"
	for i, c := range shifted {
		if code == c {
			return rune(base[i])
		}
	}
	return code
}

// EncodeFocus encodes a focus in/out report (CSI I / CSI O) when the child
// enabled focus reporting (DEC mode 1004). Returns nil otherwise.
func (e *Engine) EncodeFocus(focused bool) []byte {
	if e == nil || e.term == nil || e.closed || !e.FocusReporting() {
		return nil
	}
	ev := C.GhosttyFocusEvent(C.GHOSTTY_FOCUS_LOST)
	if focused {
		ev = C.GHOSTTY_FOCUS_GAINED
	}
	buf := make([]byte, 16)
	var written C.size_t
	if C.ghostty_focus_encode(ev, (*C.char)(unsafe.Pointer(&buf[0])), C.size_t(len(buf)), &written) != C.GHOSTTY_SUCCESS || written == 0 {
		return nil
	}
	out := make([]byte, int(written))
	copy(out, buf[:written])
	return out
}

// EncodePaste prepares clipboard text using Ghostty paste rules and
// bracketed-paste wrapping when DEC mode 2004 is on.
func (e *Engine) EncodePaste(data []byte) []byte {
	if e == nil || e.closed || len(data) == 0 {
		return nil
	}
	bracketed := e.BracketedPaste()
	in := append([]byte(nil), data...)
	buf := make([]byte, len(data)+32)
	var written C.size_t
	rc := C.ghostty_paste_encode(
		(*C.char)(unsafe.Pointer(&in[0])),
		C.size_t(len(in)),
		C.bool(bracketed),
		(*C.char)(unsafe.Pointer(&buf[0])),
		C.size_t(len(buf)),
		&written,
	)
	if rc == C.GHOSTTY_OUT_OF_SPACE && written > 0 {
		in = append([]byte(nil), data...)
		buf = make([]byte, int(written))
		rc = C.ghostty_paste_encode(
			(*C.char)(unsafe.Pointer(&in[0])),
			C.size_t(len(in)),
			C.bool(bracketed),
			(*C.char)(unsafe.Pointer(&buf[0])),
			C.size_t(len(buf)),
			&written,
		)
	}
	runtime.KeepAlive(in)
	if rc != C.GHOSTTY_SUCCESS {
		return append([]byte(nil), data...)
	}
	out := make([]byte, int(written))
	copy(out, buf[:written])
	return out
}

// EncodeMouse encodes a mouse event when tracking is on. Returns nil if
// tracking is off (caller should scroll viewport for wheels).
func (e *Engine) EncodeMouse(evt Mouse) []byte {
	if e == nil || e.closed || !e.MouseTracking() {
		return nil
	}
	C.ghostty_mouse_encoder_setopt_from_terminal(e.mouseEnc, e.term)
	var size C.GhosttyMouseEncoderSize
	size.size = C.size_t(C.sizeof_GhosttyMouseEncoderSize)
	size.screen_width = C.uint32_t(e.cols * 8)
	size.screen_height = C.uint32_t(e.rows * 16)
	size.cell_width = 8
	size.cell_height = 16
	C.ghostty_mouse_encoder_setopt(e.mouseEnc, C.GHOSTTY_MOUSE_ENCODER_OPT_SIZE, unsafe.Pointer(&size))
	action := C.GhosttyMouseAction(C.GHOSTTY_MOUSE_ACTION_PRESS)
	if evt.Action == MouseRelease {
		action = C.GHOSTTY_MOUSE_ACTION_RELEASE
	}
	if evt.Action == MouseMotion {
		action = C.GHOSTTY_MOUSE_ACTION_MOTION
	}
	C.ghostty_mouse_event_set_action(e.mouseEv, action)
	switch evt.Button {
	case MouseLeft:
		C.ghostty_mouse_event_set_button(e.mouseEv, C.GHOSTTY_MOUSE_BUTTON_LEFT)
	case MouseMiddle:
		C.ghostty_mouse_event_set_button(e.mouseEv, C.GHOSTTY_MOUSE_BUTTON_MIDDLE)
	case MouseRight:
		C.ghostty_mouse_event_set_button(e.mouseEv, C.GHOSTTY_MOUSE_BUTTON_RIGHT)
	case MouseWheelUp:
		C.ghostty_mouse_event_set_button(e.mouseEv, C.GHOSTTY_MOUSE_BUTTON_FOUR)
	case MouseWheelDown:
		C.ghostty_mouse_event_set_button(e.mouseEv, C.GHOSTTY_MOUSE_BUTTON_FIVE)
	default:
		C.ghostty_mouse_event_clear_button(e.mouseEv)
	}
	C.ghostty_mouse_event_set_mods(e.mouseEv, mapGhosttyMods(evt.Mod))
	var pos C.GhosttyMousePosition
	pos.x = C.float(evt.X*8 + 4)
	pos.y = C.float(evt.Y*16 + 8)
	C.ghostty_mouse_event_set_position(e.mouseEv, pos)
	buf := make([]byte, 64)
	var n C.size_t
	if C.ghostty_mouse_encoder_encode(e.mouseEnc, e.mouseEv, (*C.char)(unsafe.Pointer(&buf[0])), 64, &n) != C.GHOSTTY_SUCCESS {
		return nil
	}
	return buf[:n]
}
