package tea

import (
	"bytes"
	"image"
	"image/color"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/ansi/sixel"
)

// sixelScreen replays renderer output the way a Sixel terminal such as
// Windows Terminal presents it: text written or erased in a cell removes the
// image pixels there, and a Sixel DCS paints pixels from the cursor cell.
// Each Write is one flushed frame; check runs after it, so a test sees exactly
// what the user sees between frames. Unknown cursor or line operations fail
// the test instead of being guessed.
type sixelScreen struct {
	t              testing.TB
	width, height  int
	cellW, cellH   int
	text           []string
	pixels         []sixelScreenCell
	x, y           int
	wrap, insert   bool // pending autowrap; insert mode (IRM)
	savedX, savedY int
	last           rune
	parser         *ansi.Parser
	payloads       []image.Image
	frames         int
	check          func(*sixelScreen)
}

// sixelScreenCell locates the top-left pixel of a cell in a received payload.
// Payload is one-based; zero means the cell shows text, not image pixels.
type sixelScreenCell struct {
	payload, x, y int
}

func newSixelScreen(t testing.TB, width, height, cellW, cellH int) *sixelScreen {
	s := &sixelScreen{t: t, width: width, height: height, cellW: cellW, cellH: cellH,
		text: make([]string, width*height), pixels: make([]sixelScreenCell, width*height)}
	for i := range s.text {
		s.text[i] = " "
	}
	s.parser = ansi.NewParser()
	s.parser.SetDataSize(0)
	s.parser.SetHandler(ansi.Handler{
		Print:     s.print,
		Execute:   s.execute,
		HandleCsi: s.csi,
		HandleEsc: s.esc,
		HandleDcs: s.dcs,
	})
	return s
}

func (s *sixelScreen) Write(p []byte) (int, error) {
	for _, b := range p {
		s.parser.Advance(b)
	}
	s.frames++
	if s.check != nil {
		s.check(s)
	}
	return len(p), nil
}

func (s *sixelScreen) cell(x, y int) *sixelScreenCell { return &s.pixels[y*s.width+x] }

// shows reports whether the cell currently shows any image pixels.
func (s *sixelScreen) shows(x, y int) bool { return s.cell(x, y).payload != 0 }

func (s *sixelScreen) textAt(x, y int) string { return s.text[y*s.width+x] }

// pixel returns the color the terminal shows at a pixel inside a cell.
func (s *sixelScreen) pixel(x, y, px, py int) (color.NRGBA, bool) {
	c := s.cell(x, y)
	if c.payload == 0 {
		return color.NRGBA{}, false
	}
	return color.NRGBAModel.Convert(s.payloads[c.payload-1].At(c.x+px, c.y+py)).(color.NRGBA), true
}

func (s *sixelScreen) erase(x0, x1, y int) {
	if y < 0 || y >= s.height {
		return
	}
	for x := max(0, x0); x < min(x1, s.width); x++ {
		s.text[y*s.width+x] = " "
		*s.cell(x, y) = sixelScreenCell{}
	}
}

func (s *sixelScreen) moveTo(x, y int) {
	s.x, s.y, s.wrap = min(max(0, x), s.width-1), min(max(0, y), s.height-1), false
}

func (s *sixelScreen) print(r rune) {
	if s.wrap {
		if s.y == s.height-1 {
			s.t.Fatal("terminal output wrapped past the bottom row and would scroll")
		}
		s.x, s.y, s.wrap = 0, s.y+1, false
	}
	if s.insert {
		// Insert mode shifts the rest of the line; drop its pixels.
		row := s.text[s.y*s.width : (s.y+1)*s.width]
		copy(row[s.x+1:], row[s.x:])
		for x := s.x; x < s.width; x++ {
			*s.cell(x, s.y) = sixelScreenCell{}
		}
	}
	s.text[s.y*s.width+s.x] = string(r)
	*s.cell(s.x, s.y) = sixelScreenCell{}
	s.last = r
	if s.x == s.width-1 {
		s.wrap = true
	} else {
		s.x++
	}
}

func (s *sixelScreen) execute(b byte) {
	switch b {
	case '\r':
		s.moveTo(0, s.y)
	case '\n':
		if s.y == s.height-1 {
			s.t.Fatal("line feed on the bottom row would scroll images")
		}
		s.moveTo(s.x, s.y+1)
	case '\b':
		s.moveTo(s.x-1, s.y)
	case '\t':
		s.moveTo((s.x/8+1)*8, s.y)
	}
}

func (s *sixelScreen) csi(cmd ansi.Cmd, params ansi.Params) {
	n := func(i, def int) int {
		v, _, _ := params.Param(i, def)
		if v == 0 {
			return def
		}
		return v
	}
	if cmd.Prefix() != 0 || cmd.Intermediate() != 0 {
		return // private modes, queries, cursor style and keyboard flags
	}
	if final := cmd.Final(); final == 'h' || final == 'l' {
		params.ForEach(0, func(_, mode int, _ bool) {
			if mode == 4 {
				s.insert = final == 'h'
			}
		})
		return
	}
	switch cmd.Final() {
	case 'H', 'f':
		s.moveTo(n(1, 1)-1, n(0, 1)-1)
	case 'A':
		s.moveTo(s.x, s.y-n(0, 1))
	case 'B', 'e':
		s.moveTo(s.x, s.y+n(0, 1))
	case 'C', 'a':
		s.moveTo(s.x+n(0, 1), s.y)
	case 'D':
		s.moveTo(s.x-n(0, 1), s.y)
	case 'E':
		s.moveTo(0, s.y+n(0, 1))
	case 'F':
		s.moveTo(0, s.y-n(0, 1))
	case 'G', '`':
		s.moveTo(n(0, 1)-1, s.y)
	case 'd':
		s.moveTo(s.x, n(0, 1)-1)
	case 'X':
		s.erase(s.x, s.x+n(0, 1), s.y)
	case 'K':
		mode, _, _ := params.Param(0, 0)
		switch mode {
		case 0:
			s.erase(s.x, s.width, s.y)
		case 1:
			s.erase(0, s.x+1, s.y)
		default:
			s.erase(0, s.width, s.y)
		}
	case 'J':
		mode, _, _ := params.Param(0, 0)
		from, to := 0, s.height
		switch mode {
		case 0:
			s.erase(s.x, s.width, s.y)
			from = s.y + 1
		case 1:
			s.erase(0, s.x+1, s.y)
			to = s.y
		}
		for y := from; y < to; y++ {
			s.erase(0, s.width, y)
		}
	case 'b':
		for range n(0, 1) {
			s.print(s.last)
		}
	case '@', 'P':
		// Shifted cells move their text; conservatively drop shifted pixels.
		for x := s.x; x < s.width; x++ {
			*s.cell(x, s.y) = sixelScreenCell{}
		}
		row := s.text[s.y*s.width : (s.y+1)*s.width]
		count := min(n(0, 1), s.width-s.x)
		if cmd.Final() == '@' {
			copy(row[s.x+count:], row[s.x:])
			for x := s.x; x < s.x+count; x++ {
				row[x] = " "
			}
		} else {
			copy(row[s.x:], row[s.x+count:])
			for x := s.width - count; x < s.width; x++ {
				row[x] = " "
			}
		}
	case 'm', 'n', 'c', 't', 'r':
		// Attributes, modes, reports and window operations move no cells.
	default:
		s.t.Fatalf("test terminal does not model CSI %q", cmd.Final())
	}
}

func (s *sixelScreen) esc(cmd ansi.Cmd) {
	switch cmd.Final() {
	case '7':
		s.savedX, s.savedY = s.x, s.y
	case '8':
		s.moveTo(s.savedX, s.savedY)
	case 'M': // reverse index: a cursor move unless it scrolls at the top
		if s.y == 0 {
			s.t.Fatal("reverse index on the top row would scroll images")
		}
		s.moveTo(s.x, s.y-1)
	case 'D', 'E':
		if s.y == s.height-1 {
			s.t.Fatal("index on the bottom row would scroll images")
		}
		x := s.x
		if cmd.Final() == 'E' {
			x = 0
		}
		s.moveTo(x, s.y+1)
	case '\\':
	default:
		s.t.Fatalf("test terminal does not model ESC %q", cmd.Final())
	}
}

func (s *sixelScreen) dcs(cmd ansi.Cmd, _ ansi.Params, data []byte) {
	if cmd.Final() != 'q' {
		return
	}
	img, err := new(sixel.Decoder).Decode(bytes.NewReader(data))
	if err != nil {
		s.t.Fatal(err)
	}
	s.payloads = append(s.payloads, img)
	b := img.Bounds()
	columns := (b.Dx() + s.cellW - 1) / s.cellW
	rows := (b.Dy() + s.cellH - 1) / s.cellH
	for y := range rows {
		for x := range columns {
			if s.x+x < s.width && s.y+y < s.height {
				*s.cell(s.x+x, s.y+y) = sixelScreenCell{payload: len(s.payloads), x: b.Min.X + x*s.cellW, y: b.Min.Y + y*s.cellH}
			}
		}
	}
}

// payloadSizes returns the pixel size of each payload received since the
// given payload count.
func (s *sixelScreen) payloadSizes(since int) []image.Point {
	var sizes []image.Point
	for _, img := range s.payloads[since:] {
		sizes = append(sizes, img.Bounds().Size())
	}
	return sizes
}
