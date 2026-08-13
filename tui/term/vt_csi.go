package term

import (
	"github.com/charmbracelet/x/ansi"
)

func (e *Emulator) handleCSI(cmd ansi.Cmd, params ansi.Params) {
	b := e.buf()
	prefix := cmd.Prefix()
	final := cmd.Final()
	n := func(i, def int) int {
		v, _, ok := params.Param(i, def)
		if !ok {
			return def
		}
		if v == 0 {
			return def
		}
		return v
	}
	switch {
	case prefix == '?' && (final == 'h' || final == 'l'):
		e.setPrivateMode(params, final == 'h')
		return
	case final == 'h' || final == 'l':
		return
	}
	switch final {
	case 'A': // CUU
		b.wrapNext = false
		b.curY -= n(0, 1)
		b.clampCursor()
	case 'B': // CUD
		b.wrapNext = false
		b.curY += n(0, 1)
		b.clampCursor()
	case 'C': // CUF
		b.wrapNext = false
		b.curX += n(0, 1)
		b.clampCursor()
	case 'D': // CUB
		b.wrapNext = false
		b.curX -= n(0, 1)
		b.clampCursor()
	case 'E': // CNL
		b.wrapNext = false
		b.curX = 0
		b.curY += n(0, 1)
		b.clampCursor()
	case 'F': // CPL
		b.wrapNext = false
		b.curX = 0
		b.curY -= n(0, 1)
		b.clampCursor()
	case 'G': // CHA
		b.wrapNext = false
		b.curX = n(0, 1) - 1
		b.clampCursor()
	case 'H', 'f': // CUP / HVP
		b.wrapNext = false
		row, col := n(0, 1), n(1, 1)
		b.curY = row - 1
		b.curX = col - 1
		if e.origin {
			b.curY += b.scrollTop
		}
		b.clampCursor()
	case 'J': // ED
		e.eraseDisplay(n(0, 0))
	case 'K': // EL
		e.eraseLine(n(0, 0))
	case 'L': // IL
		e.insertLines(n(0, 1))
	case 'M': // DL
		e.deleteLines(n(0, 1))
	case 'P': // DCH
		e.deleteChars(n(0, 1))
	case '@': // ICH
		e.insertChars(n(0, 1))
	case 'S': // SU
		e.scrollUp(n(0, 1))
	case 'T': // SD
		e.scrollDown(n(0, 1))
	case 'd': // VPA
		b.wrapNext = false
		b.curY = n(0, 1) - 1
		b.clampCursor()
	case 'r': // DECSTBM
		top := n(0, 1) - 1
		bot := n(1, b.rows) - 1
		if top < 0 {
			top = 0
		}
		if bot >= b.rows {
			bot = b.rows - 1
		}
		if top >= bot {
			return
		}
		b.scrollTop, b.scrollBot = top, bot
		b.curX, b.curY = 0, 0
		if e.origin {
			b.curY = top
		}
	case 'm':
		e.handleSGR(params)
	case 'n':
		// DSR — ignore (no PTY reply from the emulator itself)
	}
}

func (e *Emulator) setPrivateMode(params ansi.Params, enable bool) {
	params.ForEach(0, func(_, param int, _ bool) {
		switch param {
		case 25:
			e.cursorOn = enable
		case 7:
			e.wrap = enable
		case 6:
			e.origin = enable
		case 1049, 1047, 47:
			e.setAlt(enable)
		}
	})
}

func (e *Emulator) setAlt(on bool) {
	if on == e.altOn {
		return
	}
	e.altOn = on
	if on {
		e.alt.clearAll()
		e.alt.curX, e.alt.curY = 0, 0
		e.alt.wrapNext = false
	}
}

func (e *Emulator) eraseDisplay(mode int) {
	b := e.buf()
	blank := b.blank()
	switch mode {
	case 0: // cursor to end
		for x := b.curX; x < b.cols; x++ {
			b.set(x, b.curY, blank)
		}
		for y := b.curY + 1; y < b.rows; y++ {
			for x := 0; x < b.cols; x++ {
				b.set(x, y, blank)
			}
		}
	case 1: // start to cursor
		for y := 0; y < b.curY; y++ {
			for x := 0; x < b.cols; x++ {
				b.set(x, y, blank)
			}
		}
		for x := 0; x <= b.curX && x < b.cols; x++ {
			b.set(x, b.curY, blank)
		}
	default: // entire
		b.clearAll()
	}
}

func (e *Emulator) eraseLine(mode int) {
	b := e.buf()
	blank := b.blank()
	switch mode {
	case 0:
		for x := b.curX; x < b.cols; x++ {
			b.set(x, b.curY, blank)
		}
	case 1:
		for x := 0; x <= b.curX && x < b.cols; x++ {
			b.set(x, b.curY, blank)
		}
	default:
		for x := 0; x < b.cols; x++ {
			b.set(x, b.curY, blank)
		}
	}
}

func (e *Emulator) insertLines(n int) {
	b := e.buf()
	if n < 1 {
		n = 1
	}
	bot := b.scrollBot
	for y := bot; y >= b.curY+n; y-- {
		for x := 0; x < b.cols; x++ {
			b.set(x, y, b.at(x, y-n))
		}
	}
	blank := b.blank()
	for y := b.curY; y < b.curY+n && y <= bot; y++ {
		for x := 0; x < b.cols; x++ {
			b.set(x, y, blank)
		}
	}
}

func (e *Emulator) deleteLines(n int) {
	b := e.buf()
	if n < 1 {
		n = 1
	}
	bot := b.scrollBot
	for y := b.curY; y <= bot-n; y++ {
		for x := 0; x < b.cols; x++ {
			b.set(x, y, b.at(x, y+n))
		}
	}
	blank := b.blank()
	for y := bot - n + 1; y <= bot; y++ {
		if y < b.curY {
			continue
		}
		for x := 0; x < b.cols; x++ {
			b.set(x, y, blank)
		}
	}
}

func (e *Emulator) deleteChars(n int) {
	b := e.buf()
	if n < 1 {
		n = 1
	}
	blank := b.blank()
	for x := b.curX; x < b.cols; x++ {
		src := x + n
		if src < b.cols {
			b.set(x, b.curY, b.at(src, b.curY))
		} else {
			b.set(x, b.curY, blank)
		}
	}
}

func (e *Emulator) insertChars(n int) {
	b := e.buf()
	if n < 1 {
		n = 1
	}
	blank := b.blank()
	for x := b.cols - 1; x >= b.curX+n; x-- {
		b.set(x, b.curY, b.at(x-n, b.curY))
	}
	for x := b.curX; x < b.curX+n && x < b.cols; x++ {
		b.set(x, b.curY, blank)
	}
}
