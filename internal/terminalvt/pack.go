package terminalvt

import (
	"encoding/binary"
	"errors"
	"unicode/utf8"
)

// Packed rows are the grid's wire and replica form: runs of cells that share
// one style, with the common cell (width 1, one printable ASCII byte) stored
// as that single byte. A 200-column row of plain text is about 200 bytes, and
// decoding it allocates nothing per cell.
//
//	row  := flags byte · uvarint runs · run*
//	run  := style · uvarint cells · cell*
//	style:= attr byte · [fg r g b] · [bg r g b]
//	cell := 0x20..0x7e              width 1, that ASCII character
//	      | 0x01                    width 0 spacer (wide-character tail)
//	      | 0x00 · width · uvarint n · n UTF-8 bytes
const (
	rowWrapped byte = 1 << 0

	attrFG        byte = 1 << 0
	attrBG        byte = 1 << 1
	attrBold      byte = 1 << 2
	attrFaint     byte = 1 << 3
	attrItalic    byte = 1 << 4
	attrUnderline byte = 1 << 5
	attrStrike    byte = 1 << 6
	attrInverse   byte = 1 << 7

	cellGeneral byte = 0x00
	cellSpacer  byte = 0x01
)

var ErrPacked = errors.New("terminalvt: malformed packed row")

func styleAttr(c Cell) byte {
	var a byte
	if c.FG.Explicit {
		a |= attrFG
	}
	if c.BG.Explicit {
		a |= attrBG
	}
	s := c.Style
	for _, f := range [...]struct {
		on  bool
		bit byte
	}{{s.Bold, attrBold}, {s.Faint, attrFaint}, {s.Italic, attrItalic}, {s.Underline, attrUnderline}, {s.Strikethrough, attrStrike}, {s.Inverse, attrInverse}} {
		if f.on {
			a |= f.bit
		}
	}
	return a
}

func sameStyle(a, b *Cell) bool {
	return a.FG.Explicit == b.FG.Explicit && a.FG.R == b.FG.R && a.FG.G == b.FG.G && a.FG.B == b.FG.B && a.BG.Explicit == b.BG.Explicit && a.BG.R == b.BG.R && a.BG.G == b.BG.G && a.BG.B == b.BG.B && a.Style == b.Style
}

// AppendRow packs one row of cells.
func AppendRow(dst []byte, cells []Cell, wrapped bool) []byte {
	var flags byte
	if wrapped {
		flags |= rowWrapped
	}
	dst = append(dst, flags)
	// MaxCols fits two varint bytes. Reserve once, then compact the header;
	// counting runs must not make a second full-cell pass over every frame.
	at := len(dst)
	dst = append(dst, 0, 0)
	runs := uint64(0)
	for i := 0; i < len(cells); {
		runs++
		j := i + 1
		for j < len(cells) && sameStyle(&cells[i], &cells[j]) {
			j++
		}
		c := cells[i]
		attr := styleAttr(c)
		dst = append(dst, attr)
		if attr&attrFG != 0 {
			dst = append(dst, c.FG.R, c.FG.G, c.FG.B)
		}
		if attr&attrBG != 0 {
			dst = append(dst, c.BG.R, c.BG.G, c.BG.B)
		}
		dst = binary.AppendUvarint(dst, uint64(j-i))
		for k := i; k < j; k++ {
			dst = appendCell(dst, &cells[k])
		}
		i = j
	}
	if runs < 128 {
		dst[at] = byte(runs)
		copy(dst[at+1:], dst[at+2:])
		dst = dst[:len(dst)-1]
	} else {
		dst[at] = byte(runs) | 128
		dst[at+1] = byte(runs >> 7)
	}
	return dst
}

func appendCell(dst []byte, c *Cell) []byte {
	switch {
	case c.Width == 1 && len(c.Text) == 1 && c.Text[0] >= 0x20 && c.Text[0] <= 0x7e:
		return append(dst, c.Text[0])
	case c.Width == 0 && c.Text == "":
		return append(dst, cellSpacer)
	}
	dst = append(dst, cellGeneral, c.Width)
	dst = binary.AppendUvarint(dst, uint64(len(c.Text)))
	return append(dst, c.Text...)
}

// asciiText interns every single-byte cell string, so decoding plain text
// never allocates.
var asciiText = func() (out [0x80]string) {
	for i := range out {
		out[i] = string(rune(i))
	}
	return
}()

// DecodeRow unpacks one row into dst (reusing its capacity) and returns the
// cells, the wrap flag and the unread remainder of src. Graphemes beyond ASCII
// are the only allocations.
func DecodeRow(dst []Cell, src []byte) ([]Cell, bool, []byte, error) {
	return decodeRow(dst, src, nil)
}

func decodeRow(dst []Cell, src []byte, decoder *RowDecoder) ([]Cell, bool, []byte, error) {
	if len(src) < 1 || src[0]&^(rowWrapped|2) != 0 || src[0]&2 != 0 && decoder == nil {
		return nil, false, nil, ErrPacked
	}
	wrapped := src[0]&rowWrapped != 0
	dictionary := src[0]&2 != 0
	runs, n := binary.Uvarint(src[1:])
	if n <= 0 || runs > uint64(len(src)) || runs > MaxCols {
		return nil, false, nil, ErrPacked
	}
	src = src[1+n:]
	dst = dst[:0]
	for range runs {
		if len(src) < 1 {
			return nil, false, nil, ErrPacked
		}
		var style Cell
		var id uint64
		if dictionary {
			var n int
			id, n = binary.Uvarint(src)
			if n <= 0 || id > uint64(len(decoder.Styles)) {
				return nil, false, nil, ErrPacked
			}
			src = src[n:]
		}
		if id > 0 {
			style = decoder.Styles[id-1]
		} else {
			if len(src) < 1 {
				return nil, false, nil, ErrPacked
			}
			attr := src[0]
			src = src[1:]
			if attr&attrFG != 0 {
				if len(src) < 3 {
					return nil, false, nil, ErrPacked
				}
				style.FG = Color{true, src[0], src[1], src[2]}
				src = src[3:]
			}
			if attr&attrBG != 0 {
				if len(src) < 3 {
					return nil, false, nil, ErrPacked
				}
				style.BG = Color{true, src[0], src[1], src[2]}
				src = src[3:]
			}
			style.Style = Style{attr&attrBold != 0, attr&attrFaint != 0, attr&attrItalic != 0, attr&attrUnderline != 0, attr&attrStrike != 0, attr&attrInverse != 0}
		}
		count, n := binary.Uvarint(src)
		if n <= 0 || count == 0 || (!dictionary && count > uint64(len(src)-n)) || count > uint64(MaxCols-len(dst)) {
			return nil, false, nil, ErrPacked
		}
		src = src[n:]
		for done := uint64(0); done < count; {
			repeat := uint64(1)
			if dictionary && len(src) > 0 && src[0] == 2 {
				var n int
				repeat, n = binary.Uvarint(src[1:])
				if n <= 0 || repeat == 0 || repeat > count-done {
					return nil, false, nil, ErrPacked
				}
				src = src[1+n:]
			}
			if len(src) < 1 {
				return nil, false, nil, ErrPacked
			}
			c := style
			switch b := src[0]; {
			case b >= 0x20 && b <= 0x7e:
				c.Width, c.Text = 1, asciiText[b]
				src = src[1:]
			case b == cellSpacer:
				src = src[1:]
			case b == cellGeneral:
				if len(src) < 2 {
					return nil, false, nil, ErrPacked
				}
				c.Width = src[1]
				size, n := binary.Uvarint(src[2:])
				if n <= 0 || size > uint64(len(src)-2-n) || c.Width > 2 {
					return nil, false, nil, ErrPacked
				}
				text := src[2+n : 2+n+int(size)]
				if !utf8.Valid(text) || c.Width == 0 && len(text) != 0 {
					return nil, false, nil, ErrPacked
				}
				for _, r := range string(text) {
					if r < 0x20 || r >= 0x7f && r <= 0x9f {
						return nil, false, nil, ErrPacked
					}
				}
				if len(text) == 1 && text[0] < 0x80 {
					c.Text = asciiText[text[0]]
				} else {
					if decoder != nil {
						c.Text = decoder.text(text)
					} else {
						c.Text = string(text)
					}
				}
				src = src[2+n+int(size):]
			default:
				return nil, false, nil, ErrPacked
			}
			for range repeat {
				dst = append(dst, c)
			}
			done += repeat
		}
	}
	if len(dst) > MaxCols {
		return nil, false, nil, ErrPacked
	}
	return dst, wrapped, src, nil
}

// RowEncoder retains the style dictionary for one viewer session. Short color
// runs stay inline so truecolor video cannot fill the dictionary per pixel.
type RowEncoder struct {
	styles      map[Cell]uint64
	Definitions []Cell
}

func (e *RowEncoder) Append(dst []byte, cells []Cell, wrapped bool) []byte {
	if e.styles == nil {
		e.styles = make(map[Cell]uint64)
	}
	// The ordinary text row has a single default style. Validate and append
	// ASCII in one pass rather than scanning styles and then scanning glyphs.
	start := len(dst)
	if len(cells) > 0 {
		id := e.styles[Cell{}]
		if id == 0 && len(e.Definitions) < 4096 {
			e.Definitions = append(e.Definitions, Cell{})
			id = uint64(len(e.Definitions))
			e.styles[Cell{}] = id
		}
		if id != 0 {
			flags := byte(2)
			if wrapped {
				flags |= rowWrapped
			}
			dst = append(dst, flags, 1)
			dst = binary.AppendUvarint(dst, id)
			dst = binary.AppendUvarint(dst, uint64(len(cells)))
			plain := true
			for i := range cells {
				c := &cells[i]
				if c.FG.Explicit || c.BG.Explicit || c.Style != (Style{}) || c.Width != 1 || len(c.Text) != 1 || c.Text[0] < 0x20 || c.Text[0] > 0x7e {
					plain = false
					break
				}
				if i >= 3 && len(cells[i-1].Text) == 1 && c.Text[0] == cells[i-1].Text[0] && len(cells[i-2].Text) == 1 && c.Text[0] == cells[i-2].Text[0] && len(cells[i-3].Text) == 1 && c.Text[0] == cells[i-3].Text[0] {
					plain = false
					break
				}
				dst = append(dst, c.Text[0])
			}
			if plain {
				return dst
			}
			dst = dst[:start]
		}
	}
	flags := byte(2)
	if wrapped {
		flags |= rowWrapped
	}
	dst = append(dst, flags)
	at := len(dst)
	dst = append(dst, 0, 0)
	runs := uint64(0)
	for i := 0; i < len(cells); {
		runs++
		j := i + 1
		for j < len(cells) && sameStyle(&cells[i], &cells[j]) {
			j++
		}
		style := cells[i]
		style.Text = ""
		style.Width = 0
		id := e.styles[style]
		if id == 0 && j-i >= 3 && len(e.Definitions) < 4096 {
			e.Definitions = append(e.Definitions, style)
			id = uint64(len(e.Definitions))
			e.styles[style] = id
		}
		dst = binary.AppendUvarint(dst, id)
		if id == 0 {
			dst = appendPackedStyle(dst, style)
		}
		dst = binary.AppendUvarint(dst, uint64(j-i))
		// Plain text normally has no adjacent repeats. Encode ASCII directly
		// without copying a full Cell or entering the general UTF-8 encoder.
		for k := i; k < j; {
			end := k + 1
			for end < j && cells[end].Text == cells[k].Text && cells[end].Width == cells[k].Width {
				end++
			}
			if end-k >= 4 {
				dst = append(dst, 2)
				dst = binary.AppendUvarint(dst, uint64(end-k))
				dst = appendCell(dst, &cells[k])
			} else {
				for n := k; n < end; n++ {
					c := &cells[n]
					if c.Width == 1 && len(c.Text) == 1 && c.Text[0] >= 0x20 && c.Text[0] <= 0x7e {
						dst = append(dst, c.Text[0])
					} else {
						dst = appendCell(dst, c)
					}
				}
			}
			k = end
		}
		i = j
	}
	if runs < 128 {
		dst[at] = byte(runs)
		copy(dst[at+1:], dst[at+2:])
		dst = dst[:len(dst)-1]
	} else {
		dst[at] = byte(runs) | 128
		dst[at+1] = byte(runs >> 7)
	}
	return dst
}

func appendPackedStyle(dst []byte, c Cell) []byte {
	a := styleAttr(c)
	dst = append(dst, a)
	if a&attrFG != 0 {
		dst = append(dst, c.FG.R, c.FG.G, c.FG.B)
	}
	if a&attrBG != 0 {
		dst = append(dst, c.BG.R, c.BG.G, c.BG.B)
	}
	return dst
}

// RowDecoder owns interned immutable grapheme strings, not views of the receive
// buffer. Clearing a bounded interning index never invalidates published cells.
type RowDecoder struct {
	Styles []Cell
	intern map[string]string
	bytes  int
}

func (d *RowDecoder) text(src []byte) string {
	if s, ok := d.intern[string(src)]; ok {
		return s
	}
	if d.bytes+len(src) > 1<<20 {
		d.intern = nil
		d.bytes = 0
	}
	if d.intern == nil {
		d.intern = make(map[string]string)
	}
	s := string(src)
	d.intern[s] = s
	d.bytes += len(s)
	return s
}

func (d *RowDecoder) Decode(dst []Cell, src []byte) ([]Cell, bool, []byte, error) {
	return decodeRow(dst, src, d)
}
