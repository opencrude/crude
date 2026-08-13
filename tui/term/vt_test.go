package term

import (
	"strings"
	"testing"

	"crude/tui/cell"
	"crude/tui/surface"
)

func TestEmulatorPrintAndNewline(t *testing.T) {
	e := NewEmulator()
	e.Resize(20, 4)
	e.Feed([]byte("hi\r\nthere"))
	if got := cellText(e, 0, 0); got != "hi" {
		t.Fatalf("row0=%q want hi", got)
	}
	if got := cellText(e, 0, 1); !strings.HasPrefix(got, "there") {
		t.Fatalf("row1=%q want there", got)
	}
	x, y, _ := e.Cursor()
	if y != 1 || x != 5 {
		t.Fatalf("cursor=(%d,%d) want (5,1)", x, y)
	}
}

func TestEmulatorSGRAndCUP(t *testing.T) {
	e := NewEmulator()
	e.Resize(10, 3)
	e.Feed([]byte("\x1b[31mR\x1b[0m\x1b[2;3H!"))
	c := e.buf().at(0, 0)
	if c.Char != 'R' || c.FG.Palette != 1 {
		t.Fatalf("cell=%+v want red R", c)
	}
	x, y, _ := e.Cursor()
	if x != 3 || y != 1 {
		t.Fatalf("cursor=(%d,%d) want (3,1) after CUP 2;3", x, y)
	}
	if e.buf().at(2, 1).Char != '!' {
		t.Fatalf("CUP write landed on %q", string(e.buf().at(2, 1).Char))
	}
}

func TestEmulatorAltScreen(t *testing.T) {
	e := NewEmulator()
	e.Resize(8, 2)
	e.Feed([]byte("keep"))
	e.Feed([]byte("\x1b[?1049hALT"))
	if got := cellText(e, 0, 0); !strings.HasPrefix(got, "ALT") {
		t.Fatalf("alt=%q", got)
	}
	e.Feed([]byte("\x1b[?1049l"))
	if got := cellText(e, 0, 0); !strings.HasPrefix(got, "keep") {
		t.Fatalf("primary after alt=%q", got)
	}
}

func TestEmulatorOSCTitle(t *testing.T) {
	e := NewEmulator()
	var title string
	e.SetTitleHandler(func(t string) { title = t })
	e.Feed([]byte("\x1b]0;ghostty-pin\x07"))
	if title != "ghostty-pin" && e.Title() != "ghostty-pin" {
		t.Fatalf("title=%q handler=%q", e.Title(), title)
	}
}

func TestEmulatorPaint(t *testing.T) {
	e := NewEmulator()
	e.Resize(4, 1)
	e.Feed([]byte("ab"))
	buf := surface.NewCellBuffer(4, 1)
	e.Paint(buf, 0, 0, cell.White, cell.Black)
	if buf.Get(0, 0).Char != 'a' || buf.Get(1, 0).Char != 'b' {
		t.Fatalf("paint %q%q", string(buf.Get(0, 0).Char), string(buf.Get(1, 0).Char))
	}
}

func cellText(e *Emulator, x, y int) string {
	b := e.buf()
	var sb strings.Builder
	for col := x; col < b.cols; col++ {
		c := b.at(col, y)
		if c.Char == 0 || c.Char == ' ' {
			if sb.Len() == 0 {
				continue
			}
			break
		}
		if c.Width == 0 {
			continue
		}
		sb.WriteRune(c.Char)
	}
	return sb.String()
}
