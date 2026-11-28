//go:build windows

package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"golang.org/x/sys/windows"
)

var mapVirtualKey = windows.NewLazySystemDLL("user32.dll").NewProc("MapVirtualKeyW")

// Control key state bits of a win32-input-mode record.
const (
	stateShift = 0x10
	stateCtrl  = 0x08
	stateAlt   = 0x02
)

// keyRecord encodes one win32-input-mode record: CSI Vk;Sc;Uc;Kd;Cs;Rc _
func keyRecord(vk, uc uint16, down bool, state uint32) string {
	kd := 0
	if down {
		kd = 1
	}
	sc, _, _ := mapVirtualKey.Call(uintptr(vk), 0)
	return fmt.Sprintf("\x1b[%d;%d;%d;%d;%d;1_", vk, sc, uc, kd, state)
}

var namedKeys = map[string]uint16{
	"return": 0x0D, "enter": 0x0D, "escape": 0x1B, "esc": 0x1B, "tab": 0x09, "backspace": 0x08,
	"space": 0x20, "left": 0x25, "up": 0x26, "right": 0x27, "down": 0x28, "home": 0x24, "end": 0x23,
	"pageup": 0x21, "pagedown": 0x22, "delete": 0x2E, "insert": 0x2D,
	"f1": 0x70, "f2": 0x71, "f3": 0x72, "f4": 0x73, "f5": 0x74, "f6": 0x75, "f7": 0x76, "f8": 0x77,
	"f9": 0x78, "f10": 0x79, "f11": 0x7A, "f12": 0x7B,
	"`": 0xC0, "-": 0xBD, "=": 0xBB, "[": 0xDB, "]": 0xDD, "\\": 0xDC, ";": 0xBA, "'": 0xDE,
	",": 0xBC, ".": 0xBE, "/": 0xBF,
}

// key presses a key spec such as "ctrl+alt+y", "shift+Left" or "Return".
func (r *recorder) key(spec string) {
	parts := strings.Split(spec, "+")
	name := parts[len(parts)-1]
	if name == "" && strings.HasSuffix(spec, "++") {
		name = "+"
	}
	var state uint32
	for _, m := range parts[:len(parts)-1] {
		switch strings.ToLower(m) {
		case "ctrl":
			state |= stateCtrl
		case "alt":
			state |= stateAlt
		case "shift":
			state |= stateShift
		}
	}
	var vk, uc uint16
	if v, ok := namedKeys[strings.ToLower(name)]; ok {
		vk = v
		switch vk {
		case 0x0D:
			uc = '\r'
		case 0x1B, 0x09, 0x08, 0x20:
			uc = vk
		}
		if len(name) == 1 {
			uc = uint16(name[0])
		}
	} else if len(name) == 1 {
		c := name[0]
		switch {
		case c >= 'a' && c <= 'z':
			vk, uc = uint16(c-'a'+'A'), uint16(c)
		case c >= 'A' && c <= 'Z':
			vk, uc = uint16(c), uint16(c)
			state |= stateShift
		case c >= '0' && c <= '9':
			vk, uc = uint16(c), uint16(c)
		}
	} else {
		fmt.Fprintln(os.Stderr, "castrec: unknown key", spec)
		return
	}
	if state&stateCtrl != 0 && state&stateAlt == 0 && vk >= 'A' && vk <= 'Z' {
		uc = vk - 'A' + 1
	}
	if state&stateCtrl != 0 && state&stateAlt != 0 {
		uc = 0
	}
	var b strings.Builder
	mods := []struct {
		flag uint32
		vk   uint16
	}{{stateCtrl, 0x11}, {stateAlt, 0x12}, {stateShift, 0x10}}
	held := uint32(0)
	for _, m := range mods {
		if state&m.flag != 0 {
			held |= m.flag
			b.WriteString(keyRecord(m.vk, 0, true, held))
		}
	}
	b.WriteString(keyRecord(vk, uc, true, state))
	b.WriteString(keyRecord(vk, uc, false, state))
	for i := len(mods) - 1; i >= 0; i-- {
		if state&mods[i].flag != 0 {
			held &^= mods[i].flag
			b.WriteString(keyRecord(mods[i].vk, 0, false, held))
		}
	}
	r.send(b.String())
}

// typeText types text one character at a time with gap between characters.
func (r *recorder) typeText(text string, gap time.Duration) {
	if gap <= 0 {
		gap = 4 * time.Millisecond
	}
	for _, c := range text {
		var b strings.Builder
		for _, u := range utf16.Encode([]rune{c}) {
			vk, state := uint16(0), uint32(0)
			switch {
			case c >= 'a' && c <= 'z':
				vk = uint16(c - 'a' + 'A')
			case c >= 'A' && c <= 'Z':
				vk, state = uint16(c), stateShift
			case c >= '0' && c <= '9' || c == ' ':
				vk = uint16(c)
			}
			b.WriteString(keyRecord(vk, u, true, state))
			b.WriteString(keyRecord(vk, u, false, state))
		}
		r.send(b.String())
		time.Sleep(gap)
	}
}

// mouse sends SGR mouse sequences; conhost turns them into mouse records.
// Coordinates are zero-based cells.
func (r *recorder) mouse(op, arg string) {
	var n []int
	for _, p := range strings.Split(arg, ",") {
		v, _ := strconv.Atoi(strings.TrimSpace(p))
		n = append(n, v)
	}
	need := map[string]int{"move": 2, "click": 2, "rclick": 2, "drag": 4, "wheel": 3}[op]
	if len(n) < need {
		fmt.Fprintln(os.Stderr, "castrec: bad pointer step", op, arg)
		return
	}
	sgr := func(code, col, row int, press bool) {
		end := 'M'
		if !press {
			end = 'm'
		}
		r.send(fmt.Sprintf("\x1b[<%d;%d;%d%c", code, col+1, row+1, end))
	}
	switch op {
	case "move":
		sgr(35, n[0], n[1], true)
	case "click", "rclick":
		btn := 0
		if op == "rclick" {
			btn = 2
		}
		sgr(35, n[0], n[1], true)
		time.Sleep(40 * time.Millisecond)
		sgr(btn, n[0], n[1], true)
		time.Sleep(60 * time.Millisecond)
		sgr(btn, n[0], n[1], false)
	case "drag":
		sgr(0, n[0], n[1], true)
		const steps = 24
		for i := 1; i <= steps; i++ {
			time.Sleep(25 * time.Millisecond)
			sgr(32, n[0]+(n[2]-n[0])*i/steps, n[1]+(n[3]-n[1])*i/steps, true)
		}
		time.Sleep(60 * time.Millisecond)
		sgr(0, n[2], n[3], false)
	case "wheel":
		code, count := 65, n[2]
		if count < 0 {
			code, count = 64, -count
		}
		for i := 0; i < count; i++ {
			sgr(code, n[0], n[1], true)
			time.Sleep(30 * time.Millisecond)
		}
	}
}
