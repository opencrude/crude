// Package term is a Ghostty-pinned VT emulator painted into Crude's CellBuffer.
// Sequence parsing uses the vendored xansi parser (the same DEC/xterm family
// Ghostty 1.3 implements). In-app PTYs set TERM=xterm-ghostty.
package term

import (
	"strings"

	"github.com/charmbracelet/x/ansi"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
)

// Emulator is a single terminal screen (primary + alternate buffers).
type Emulator struct {
	parser *ansi.Parser
	cols   int
	rows   int

	primary  *buffer
	alt      *buffer
	altOn    bool
	cursorOn bool
	origin   bool // DECOM: cursor origin in scroll region
	wrap     bool
	title    string

	onTitle func(string)
}

type buffer struct {
	cells      []cell.Cell
	cols, rows int
	curX, curY int
	savedX     int
	savedY     int
	savedPen   pen
	pen        pen
	scrollTop  int
	scrollBot  int
	wrapNext   bool
}

type pen struct {
	fg, bg cell.Color
	attr   cell.Attrs
}

// NewEmulator creates an 80x24 emulator.
func NewEmulator() *Emulator {
	e := &Emulator{
		parser:   ansi.NewParser(),
		cursorOn: true,
		wrap:     true,
	}
	e.parser.SetHandler(ansi.Handler{
		Print:     e.print,
		Execute:   e.execute,
		HandleCsi: e.handleCSI,
		HandleEsc: e.handleESC,
		HandleOsc: e.handleOSC,
	})
	e.Resize(80, 24)
	return e
}

// SetTitleHandler is called when OSC 0/2 sets the window title.
func (e *Emulator) SetTitleHandler(fn func(string)) { e.onTitle = fn }

// Title returns the last OSC title.
func (e *Emulator) Title() string { return e.title }

// Cols and Rows are the current grid size.
func (e *Emulator) Cols() int { return e.cols }
func (e *Emulator) Rows() int { return e.rows }

// Cursor reports the 0-based cursor and whether it should blink/show.
func (e *Emulator) Cursor() (x, y int, visible bool) {
	b := e.buf()
	return b.curX, b.curY, e.cursorOn
}

// Resize changes the grid. Content in the overlapping region is kept.
func (e *Emulator) Resize(cols, rows int) {
	if cols < 2 {
		cols = 2
	}
	if rows < 1 {
		rows = 1
	}
	if e.primary == nil {
		e.primary = newBuffer(cols, rows)
		e.alt = newBuffer(cols, rows)
		e.cols, e.rows = cols, rows
		return
	}
	e.primary.resize(cols, rows)
	e.alt.resize(cols, rows)
	e.cols, e.rows = cols, rows
}

// Feed parses bytes from the PTY.
func (e *Emulator) Feed(data []byte) {
	if e == nil || len(data) == 0 {
		return
	}
	e.parser.Parse(data)
}

// Paint writes the visible grid into buf at origin. Default-colored cells use
// fg/bg so the terminal fills the pane instead of punching holes.
func (e *Emulator) Paint(buf *surface.CellBuffer, originX, originY int, fg, bg cell.Color) {
	if e == nil || buf == nil {
		return
	}
	b := e.buf()
	cx, cy, show := e.Cursor()
	for y := 0; y < b.rows; y++ {
		for x := 0; x < b.cols; x++ {
			c := b.at(x, y)
			if c.FG.IsDefault() {
				c.FG = fg
			}
			if c.BG.IsDefault() {
				c.BG = bg
			}
			if c.Attr.Has(cell.AttrReverse) {
				c.FG, c.BG = c.BG, c.FG
			}
			if show && x == cx && y == cy {
				c.Attr |= cell.AttrReverse
				c.FG, c.BG = c.BG, c.FG
			}
			if c.Char == 0 && c.Width == 0 {
				continue
			}
			if c.Char == 0 {
				c.Char = ' '
				c.Width = 1
			}
			buf.Set(originX+x, originY+y, c)
		}
	}
}

func (e *Emulator) buf() *buffer {
	if e.altOn {
		return e.alt
	}
	return e.primary
}

func newBuffer(cols, rows int) *buffer {
	b := &buffer{cols: cols, rows: rows, scrollBot: rows - 1}
	b.cells = make([]cell.Cell, cols*rows)
	b.clearAll()
	return b
}

func (b *buffer) at(x, y int) cell.Cell {
	if x < 0 || y < 0 || x >= b.cols || y >= b.rows {
		return cell.BlankCell(cell.NoColor)
	}
	return b.cells[y*b.cols+x]
}

func (b *buffer) set(x, y int, c cell.Cell) {
	if x < 0 || y < 0 || x >= b.cols || y >= b.rows {
		return
	}
	b.cells[y*b.cols+x] = c
}

func (b *buffer) blank() cell.Cell {
	return cell.Cell{Char: ' ', Width: 1, FG: b.pen.fg, BG: b.pen.bg, Attr: b.pen.attr}
}

func (b *buffer) clearAll() {
	blank := b.blank()
	for i := range b.cells {
		b.cells[i] = blank
	}
}

func (b *buffer) resize(cols, rows int) {
	next := make([]cell.Cell, cols*rows)
	blank := cell.BlankCell(cell.NoColor)
	for i := range next {
		next[i] = blank
	}
	copyRows := min(b.rows, rows)
	copyCols := min(b.cols, cols)
	for y := 0; y < copyRows; y++ {
		for x := 0; x < copyCols; x++ {
			next[y*cols+x] = b.at(x, y)
		}
	}
	b.cells = next
	b.cols, b.rows = cols, rows
	if b.curX >= cols {
		b.curX = cols - 1
	}
	if b.curY >= rows {
		b.curY = rows - 1
	}
	b.scrollTop = 0
	b.scrollBot = rows - 1
}

func (e *Emulator) print(r rune) {
	b := e.buf()
	w := rendering.PlainWidth(string(r))
	if w <= 0 {
		if b.curX > 0 {
			prev := b.at(b.curX-1, b.curY)
			if prev.Content == "" {
				prev.Content = string(prev.Char) + string(r)
			} else {
				prev.Content += string(r)
			}
			b.set(b.curX-1, b.curY, prev)
		}
		return
	}
	if b.wrapNext {
		b.curX = 0
		b.wrapNext = false
		e.index()
	}
	if e.wrap && b.curX+w > b.cols {
		b.curX = 0
		e.index()
	}
	c := cell.Cell{
		Char: r, Content: string(r), Width: uint8(w),
		FG: b.pen.fg, BG: b.pen.bg, Attr: b.pen.attr,
	}
	b.set(b.curX, b.curY, c)
	if w == 2 && b.curX+1 < b.cols {
		b.set(b.curX+1, b.curY, cell.ContinuationCell)
	}
	b.curX += w
	if b.curX >= b.cols {
		if e.wrap {
			b.curX = b.cols - 1
			b.wrapNext = true
		} else {
			b.curX = b.cols - 1
		}
	}
}

func (e *Emulator) execute(b byte) {
	buf := e.buf()
	switch b {
	case ansi.BEL:
		return
	case ansi.BS:
		buf.wrapNext = false
		if buf.curX > 0 {
			buf.curX--
		}
	case ansi.HT:
		buf.wrapNext = false
		buf.curX = ((buf.curX / 8) + 1) * 8
		if buf.curX >= buf.cols {
			buf.curX = buf.cols - 1
		}
	case ansi.LF, ansi.VT, ansi.FF:
		buf.wrapNext = false
		e.index()
	case ansi.CR:
		buf.wrapNext = false
		buf.curX = 0
	}
}

func (e *Emulator) handleESC(cmd ansi.Cmd) {
	buf := e.buf()
	switch cmd.Final() {
	case '7':
		buf.savedX, buf.savedY, buf.savedPen = buf.curX, buf.curY, buf.pen
	case '8':
		buf.curX, buf.curY, buf.pen = buf.savedX, buf.savedY, buf.savedPen
		buf.clampCursor()
	case 'D':
		e.index()
	case 'M':
		e.reverseIndex()
	case 'E':
		buf.curX = 0
		e.index()
	case 'c':
		e.reset()
	}
}

func (e *Emulator) handleOSC(cmd int, data []byte) {
	switch cmd {
	case 0, 1, 2:
		title := string(data)
		if i := strings.IndexByte(title, ';'); i >= 0 {
			title = title[i+1:]
		}
		e.title = title
		if e.onTitle != nil {
			e.onTitle(title)
		}
	}
}

func (e *Emulator) reset() {
	e.altOn = false
	e.cursorOn = true
	e.origin = false
	e.wrap = true
	e.primary = newBuffer(e.cols, e.rows)
	e.alt = newBuffer(e.cols, e.rows)
}

func (e *Emulator) index() {
	b := e.buf()
	if b.curY == b.scrollBot {
		e.scrollUp(1)
		return
	}
	if b.curY < b.rows-1 {
		b.curY++
	}
}

func (e *Emulator) reverseIndex() {
	b := e.buf()
	if b.curY == b.scrollTop {
		e.scrollDown(1)
		return
	}
	if b.curY > 0 {
		b.curY--
	}
}

func (e *Emulator) scrollUp(n int) {
	b := e.buf()
	if n < 1 {
		n = 1
	}
	top, bot := b.scrollTop, b.scrollBot
	height := bot - top + 1
	if n > height {
		n = height
	}
	for y := top; y <= bot-n; y++ {
		for x := 0; x < b.cols; x++ {
			b.set(x, y, b.at(x, y+n))
		}
	}
	blank := b.blank()
	for y := bot - n + 1; y <= bot; y++ {
		for x := 0; x < b.cols; x++ {
			b.set(x, y, blank)
		}
	}
}

func (e *Emulator) scrollDown(n int) {
	b := e.buf()
	if n < 1 {
		n = 1
	}
	top, bot := b.scrollTop, b.scrollBot
	height := bot - top + 1
	if n > height {
		n = height
	}
	for y := bot; y >= top+n; y-- {
		for x := 0; x < b.cols; x++ {
			b.set(x, y, b.at(x, y-n))
		}
	}
	blank := b.blank()
	for y := top; y < top+n; y++ {
		for x := 0; x < b.cols; x++ {
			b.set(x, y, blank)
		}
	}
}

func (b *buffer) clampCursor() {
	if b.curX < 0 {
		b.curX = 0
	}
	if b.curY < 0 {
		b.curY = 0
	}
	if b.curX >= b.cols {
		b.curX = b.cols - 1
	}
	if b.curY >= b.rows {
		b.curY = b.rows - 1
	}
}

