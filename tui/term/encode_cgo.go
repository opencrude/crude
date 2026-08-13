//go:build cgo

package term

/*
#include "ghostty_bridge.h"
*/
import "C"

import (
	"runtime"
	"unsafe"

	tea "charm.land/bubbletea/v2"

	"crude/tui/ui"
)

// EncodeKey converts a Bubble Tea key press into PTY bytes using Ghostty's
// terminal-aware encoder (application cursor, keypad, Kitty flags, …).
func (e *Emulator) EncodeKey(msg tea.KeyPressMsg) []byte {
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

func printableKeyText(msg tea.KeyPressMsg) string {
	if msg.Mod.Contains(tea.ModCtrl) || msg.Mod.Contains(tea.ModAlt) || msg.Mod.Contains(tea.ModSuper) {
		return ""
	}
	if msg.Text == "" || containsC0(msg.Text) {
		return ""
	}
	switch msg.Code {
	case tea.KeyEnter, tea.KeyTab, tea.KeyBackspace, tea.KeyEscape,
		tea.KeyUp, tea.KeyDown, tea.KeyLeft, tea.KeyRight,
		tea.KeyHome, tea.KeyEnd, tea.KeyPgUp, tea.KeyPgDown,
		tea.KeyInsert, tea.KeyDelete,
		tea.KeyF1, tea.KeyF2, tea.KeyF3, tea.KeyF4, tea.KeyF5, tea.KeyF6,
		tea.KeyF7, tea.KeyF8, tea.KeyF9, tea.KeyF10, tea.KeyF11, tea.KeyF12:
		return ""
	}
	return msg.Text
}

func (e *Emulator) encodeKeyAction(msg tea.KeyPressMsg, action C.GhosttyKeyAction) []byte {
	C.ghostty_key_encoder_setopt_from_terminal(e.keyEnc, e.term)
	C.ghostty_key_event_set_action(e.keyEv, action)
	C.ghostty_key_event_set_key(e.keyEv, mapGhosttyKey(msg.Code))
	C.ghostty_key_event_set_mods(e.keyEv, mapGhosttyMods(msg.Mod))
	text := msg.Text
	if action != C.GHOSTTY_KEY_ACTION_PRESS || msg.Mod.Contains(tea.ModCtrl) || containsC0(text) {
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

func mapGhosttyMods(mod tea.KeyMod) C.GhosttyMods {
	var m C.GhosttyMods
	if mod.Contains(tea.ModShift) {
		m |= C.GHOSTTY_MODS_SHIFT
	}
	if mod.Contains(tea.ModCtrl) {
		m |= C.GHOSTTY_MODS_CTRL
	}
	if mod.Contains(tea.ModAlt) {
		m |= C.GHOSTTY_MODS_ALT
	}
	if mod.Contains(tea.ModSuper) {
		m |= C.GHOSTTY_MODS_SUPER
	}
	return m
}

func mapGhosttyKey(code rune) C.GhosttyKey {
	switch code {
	case tea.KeyEnter:
		return C.GHOSTTY_KEY_ENTER
	case tea.KeyTab:
		return C.GHOSTTY_KEY_TAB
	case tea.KeyBackspace:
		return C.GHOSTTY_KEY_BACKSPACE
	case tea.KeyEscape:
		return C.GHOSTTY_KEY_ESCAPE
	case tea.KeyUp:
		return C.GHOSTTY_KEY_ARROW_UP
	case tea.KeyDown:
		return C.GHOSTTY_KEY_ARROW_DOWN
	case tea.KeyLeft:
		return C.GHOSTTY_KEY_ARROW_LEFT
	case tea.KeyRight:
		return C.GHOSTTY_KEY_ARROW_RIGHT
	case tea.KeyHome:
		return C.GHOSTTY_KEY_HOME
	case tea.KeyEnd:
		return C.GHOSTTY_KEY_END
	case tea.KeyPgUp:
		return C.GHOSTTY_KEY_PAGE_UP
	case tea.KeyPgDown:
		return C.GHOSTTY_KEY_PAGE_DOWN
	case tea.KeyInsert:
		return C.GHOSTTY_KEY_INSERT
	case tea.KeyDelete:
		return C.GHOSTTY_KEY_DELETE
	case tea.KeySpace:
		return C.GHOSTTY_KEY_SPACE
	case tea.KeyF1:
		return C.GHOSTTY_KEY_F1
	case tea.KeyF2:
		return C.GHOSTTY_KEY_F2
	case tea.KeyF3:
		return C.GHOSTTY_KEY_F3
	case tea.KeyF4:
		return C.GHOSTTY_KEY_F4
	case tea.KeyF5:
		return C.GHOSTTY_KEY_F5
	case tea.KeyF6:
		return C.GHOSTTY_KEY_F6
	case tea.KeyF7:
		return C.GHOSTTY_KEY_F7
	case tea.KeyF8:
		return C.GHOSTTY_KEY_F8
	case tea.KeyF9:
		return C.GHOSTTY_KEY_F9
	case tea.KeyF10:
		return C.GHOSTTY_KEY_F10
	case tea.KeyF11:
		return C.GHOSTTY_KEY_F11
	case tea.KeyF12:
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

// EncodeFocus encodes a focus in/out report (CSI I / CSI O) when the child
// enabled focus reporting (DEC mode 1004). Returns nil otherwise.
func (e *Emulator) EncodeFocus(focused bool) []byte {
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
func (e *Emulator) EncodePaste(data []byte) []byte {
	if e == nil || len(data) == 0 {
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
		return data
	}
	out := make([]byte, int(written))
	copy(out, buf[:written])
	return out
}

// EncodeMouse encodes a mouse event when tracking is on. Returns nil if
// tracking is off (caller should scroll viewport for wheels).
func (e *Emulator) EncodeMouse(evt ui.Event, cellX, cellY int) []byte {
	if e == nil || e.term == nil || e.mouseEnc == nil || e.mouseEv == nil || e.closed {
		return nil
	}
	if !e.MouseTracking() {
		return nil
	}
	C.ghostty_mouse_encoder_setopt_from_terminal(e.mouseEnc, e.term)
	var size C.GhosttyMouseEncoderSize
	size.size = C.size_t(C.sizeof_GhosttyMouseEncoderSize)
	size.screen_width = C.uint32_t(e.cols * CellPixelWidth)
	size.screen_height = C.uint32_t(e.rows * CellPixelHeight)
	size.cell_width = C.uint32_t(CellPixelWidth)
	size.cell_height = C.uint32_t(CellPixelHeight)
	C.ghostty_mouse_encoder_setopt(e.mouseEnc, C.GHOSTTY_MOUSE_ENCODER_OPT_SIZE, unsafe.Pointer(&size))

	var action C.GhosttyMouseAction
	var button C.GhosttyMouseButton
	var mods C.GhosttyMods
	hasButton := true
	switch evt.Kind {
	case ui.EventMouseClick:
		action = C.GHOSTTY_MOUSE_ACTION_PRESS
		button = mapMouseButton(evt.MouseClick.Button)
		mods = mapGhosttyMods(evt.MouseClick.Mod)
	case ui.EventMouseRelease:
		action = C.GHOSTTY_MOUSE_ACTION_RELEASE
		button = mapMouseButton(evt.MouseRelease.Button)
		mods = mapGhosttyMods(evt.MouseRelease.Mod)
	case ui.EventMouseMotion:
		action = C.GHOSTTY_MOUSE_ACTION_MOTION
		button = mapMouseButton(evt.MouseMotion.Button)
		mods = mapGhosttyMods(evt.MouseMotion.Mod)
		if button == C.GHOSTTY_MOUSE_BUTTON_UNKNOWN {
			hasButton = false
		}
	case ui.EventMouseWheel:
		action = C.GHOSTTY_MOUSE_ACTION_PRESS
		mods = mapGhosttyMods(evt.MouseWheel.Mod)
		switch evt.MouseWheel.Button {
		case tea.MouseWheelUp:
			button = C.GHOSTTY_MOUSE_BUTTON_FOUR
		case tea.MouseWheelDown:
			button = C.GHOSTTY_MOUSE_BUTTON_FIVE
		default:
			return nil
		}
	default:
		return nil
	}
	C.ghostty_mouse_event_set_action(e.mouseEv, action)
	if hasButton {
		C.ghostty_mouse_event_set_button(e.mouseEv, button)
	} else {
		C.ghostty_mouse_event_clear_button(e.mouseEv)
	}
	C.ghostty_mouse_event_set_mods(e.mouseEv, mods)
	var pos C.GhosttyMousePosition
	pos.x = C.float(cellX*CellPixelWidth + CellPixelWidth/2)
	pos.y = C.float(cellY*CellPixelHeight + CellPixelHeight/2)
	C.ghostty_mouse_event_set_position(e.mouseEv, pos)

	buf := make([]byte, 64)
	var written C.size_t
	rc := C.ghostty_mouse_encoder_encode(e.mouseEnc, e.mouseEv, (*C.char)(unsafe.Pointer(&buf[0])), C.size_t(len(buf)), &written)
	if rc != C.GHOSTTY_SUCCESS || written == 0 {
		return nil
	}
	out := make([]byte, int(written))
	copy(out, buf[:written])
	return out
}

func mapMouseButton(b tea.MouseButton) C.GhosttyMouseButton {
	switch b {
	case tea.MouseLeft:
		return C.GHOSTTY_MOUSE_BUTTON_LEFT
	case tea.MouseRight:
		return C.GHOSTTY_MOUSE_BUTTON_RIGHT
	case tea.MouseMiddle:
		return C.GHOSTTY_MOUSE_BUTTON_MIDDLE
	default:
		return C.GHOSTTY_MOUSE_BUTTON_UNKNOWN
	}
}
