package uv

import (
	"image/color"
	"strconv"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
)

// Color is an immutable terminal color. Zero means the terminal default,
// distinct from black. Basic, indexed and RGB colors retain their SGR identity:
// a palette entry may be changed by the terminal independently of its RGB value.
// Keeping colors inline makes cell snapshots self-contained, without per-cell
// interface boxes, shared mutable colors, or a global color cache.
type Color uint32

const (
	colorBasic   Color = 1 << 24
	colorIndexed Color = 2 << 24
	colorRGB     Color = 3 << 24
	colorKind    Color = 0xff << 24
)

// RGBColor constructs an opaque 24-bit terminal color.
func RGBColor(r, g, b uint8) Color { return colorRGB | Color(r)<<16 | Color(g)<<8 | Color(b) }

// IndexedColor constructs a color in the terminal's 256-color palette.
func IndexedColor(index uint8) Color { return colorIndexed | Color(index) }

// BasicColor constructs a 3/4-bit ANSI color (0–15).
func BasicColor(index uint8) Color {
	if index > 15 {
		return 0
	}
	return colorBasic | Color(index)
}

// NewColor snapshots an external color at the terminal boundary. Terminal RGB
// uses the high eight bits of its premultiplied components, as ANSI SGR does;
// terminal cells have no alpha channel. The external object is never retained.
func NewColor(c color.Color) Color {
	switch c := c.(type) {
	case nil:
		return 0
	case Color:
		return c
	case ansi.BasicColor:
		return BasicColor(uint8(c))
	case ansi.IndexedColor:
		return IndexedColor(uint8(c))
	default:
		r, g, b, _ := c.RGBA()
		return RGBColor(uint8(r>>8), uint8(g>>8), uint8(b>>8))
	}
}

// RGBA implements color.Color for color inspection, not for testing defaultness.
func (c Color) RGBA() (r, g, b, a uint32) {
	switch c & colorKind {
	case colorBasic:
		return ansi.BasicColor(uint8(c)).RGBA()
	case colorIndexed:
		return ansi.IndexedColor(uint8(c)).RGBA()
	case colorRGB:
		r, g, b = uint32(c>>16)&255, uint32(c>>8)&255, uint32(c)&255
		return r * 257, g * 257, b * 257, 0xffff
	default:
		return 0, 0, 0, 0
	}
}

// terminalColor is used only at the color-profile downsampling boundary.
func (c Color) terminalColor() color.Color {
	switch c & colorKind {
	case colorBasic:
		return ansi.BasicColor(uint8(c))
	case colorIndexed:
		return ansi.IndexedColor(uint8(c))
	case colorRGB:
		return ansi.RGBColor{R: uint8(c >> 16), G: uint8(c >> 8), B: uint8(c)}
	default:
		return nil
	}
}

// Style represents the style of a cell.
type Style struct {
	Fg             Color
	Bg             Color
	UnderlineColor Color
	Underline      Underline
	Attrs          uint8
}

// Equal returns true if the style is equal to the other style.
func (s *Style) Equal(o *Style) bool {
	return *s == *o
}

// Styled wraps the given string with the style's ANSI sequences and resets.
func (s *Style) Styled(str string) string {
	if s.IsZero() {
		return str
	}
	return s.String() + str + ansi.ResetStyle
}

// String returns the ANSI SGR sequence for the style.
func (s *Style) String() string {
	var scratch [128]byte
	return string(s.appendTo(scratch[:0]))
}

func (s *Style) appendTo(dst []byte) []byte {
	if s.IsZero() {
		return append(dst, ansi.ResetStyle...)
	}

	b := sgrBuilder{buf: dst, start: len(dst)}

	if s.Attrs != 0 { //nolint:nestif
		if s.Attrs&AttrBold != 0 {
			b.attr("1")
		}
		if s.Attrs&AttrFaint != 0 {
			b.attr("2")
		}
		if s.Attrs&AttrItalic != 0 {
			b.attr("3")
		}
		if s.Attrs&AttrBlink != 0 {
			b.attr("5")
		}
		if s.Attrs&AttrRapidBlink != 0 {
			b.attr("6")
		}
		if s.Attrs&AttrReverse != 0 {
			b.attr("7")
		}
		if s.Attrs&AttrConceal != 0 {
			b.attr("8")
		}
		if s.Attrs&AttrStrikethrough != 0 {
			b.attr("9")
		}
	}
	if s.Underline != UnderlineStyleNone {
		switch s.Underline {
		case UnderlineStyleSingle:
			b.attr("4")
		case UnderlineStyleDouble:
			b.underline(UnderlineStyleDouble)
		case UnderlineStyleCurly:
			b.underline(UnderlineStyleCurly)
		case UnderlineStyleDotted:
			b.underline(UnderlineStyleDotted)
		case UnderlineStyleDashed:
			b.underline(UnderlineStyleDashed)
		}
	}
	if s.Fg != 0 {
		b.color(s.Fg, 38)
	}
	if s.Bg != 0 {
		b.color(s.Bg, 48)
	}
	if s.UnderlineColor != 0 {
		b.color(s.UnderlineColor, 58)
	}

	return b.finish()
}

// Diff returns the ANSI sequence that sets the style as a diff from
// another style.
func (s *Style) Diff(from *Style) string {
	return StyleDiff(from, s)
}

// StyleDiff returns the SGR ANSI sequence necessary to transition from the
// "from" style to the "to" style.
func StyleDiff(from, to *Style) string {
	var scratch [128]byte
	return string(AppendStyleDiff(scratch[:0], from, to))
}

// AppendStyleDiff appends the exact SGR transition into caller-owned storage.
// No color interfaces or intermediate attribute strings are created.
func AppendStyleDiff(dst []byte, from, to *Style) []byte {
	if from == nil && to == nil {
		return dst
	}
	if from != nil && to != nil && from.Equal(to) {
		return dst
	}
	if from == nil {
		return to.appendTo(dst)
	}
	if to == nil || to.IsZero() {
		// Resetting all styles is cheaper than calculating diffs.
		// "\x1b[m" is 3 bytes vs potentially much longer sequences.
		return append(dst, ansi.ResetStyle...)
	}

	// TODO: Optimize further by checking if a full reset is cheaper than
	// calculating diffs. Often, it might be cheaper to reset everything and
	// then set the desired styles rather than calculating diffs. Also more
	// compatible with terminals that have buggy SGR implementations.

	b := sgrBuilder{buf: dst, start: len(dst)}

	if from.Fg != to.Fg {
		b.color(to.Fg, 38)
	}

	if from.Bg != to.Bg {
		b.color(to.Bg, 48)
	}

	if from.UnderlineColor != to.UnderlineColor {
		b.color(to.UnderlineColor, 58)
	}

	fromBold := from.Attrs&AttrBold != 0
	fromFaint := from.Attrs&AttrFaint != 0
	fromItalic := from.Attrs&AttrItalic != 0
	fromUnderline := from.Underline != UnderlineStyleNone
	fromBlink := from.Attrs&AttrBlink != 0
	fromRapidBlink := from.Attrs&AttrRapidBlink != 0
	fromReverse := from.Attrs&AttrReverse != 0
	fromConceal := from.Attrs&AttrConceal != 0
	fromStrikethrough := from.Attrs&AttrStrikethrough != 0
	toBold := to.Attrs&AttrBold != 0
	toFaint := to.Attrs&AttrFaint != 0
	toItalic := to.Attrs&AttrItalic != 0
	toUnderline := to.Underline != UnderlineStyleNone
	toBlink := to.Attrs&AttrBlink != 0
	toRapidBlink := to.Attrs&AttrRapidBlink != 0
	toReverse := to.Attrs&AttrReverse != 0
	toConceal := to.Attrs&AttrConceal != 0
	toStrikethrough := to.Attrs&AttrStrikethrough != 0

	// We perform the resets first since they are single attributes and
	// shouldn't interfere with others being set.

	boldChanged := fromBold != toBold
	faintChanged := fromFaint != toFaint
	if boldChanged || faintChanged {
		if fromBold && !toBold || fromFaint && !toFaint {
			b.attr("22")
			boldChanged = true
			faintChanged = true
		}
	}

	italicChanged := fromItalic != toItalic
	if italicChanged && !toItalic {
		b.attr("23")
	}

	underlineChanged := fromUnderline != toUnderline || from.Underline != to.Underline
	if underlineChanged && !toUnderline {
		b.attr("24")
	}

	blinkChanged := fromBlink != toBlink
	rapidBlinkChanged := fromRapidBlink != toRapidBlink
	if blinkChanged || rapidBlinkChanged {
		if fromBlink && !toBlink || fromRapidBlink && !toRapidBlink {
			b.attr("25")
			blinkChanged = true
			rapidBlinkChanged = true
		}
	}

	reverseChanged := fromReverse != toReverse
	if reverseChanged && !toReverse {
		b.attr("27")
	}

	concealChanged := fromConceal != toConceal
	if concealChanged && !toConceal {
		b.attr("28")
	}

	strikethroughChanged := fromStrikethrough != toStrikethrough
	if strikethroughChanged && !toStrikethrough {
		b.attr("29")
	}

	if boldChanged && toBold {
		b.attr("1")
	}

	if faintChanged && toFaint {
		b.attr("2")
	}

	if italicChanged && toItalic {
		b.attr("3")
	}

	if underlineChanged && toUnderline && to.Underline == UnderlineStyleSingle {
		// We only handle single underline here since others require more
		// specific handling at the end.
		b.attr("4")
	}

	if blinkChanged && toBlink {
		b.attr("5")
	}

	if rapidBlinkChanged && toRapidBlink {
		b.attr("6")
	}

	if reverseChanged && toReverse {
		b.attr("7")
	}

	if concealChanged && toConceal {
		b.attr("8")
	}

	if strikethroughChanged && toStrikethrough {
		b.attr("9")
	}

	// Handle special underline styles.
	if underlineChanged && toUnderline && to.Underline > UnderlineStyleSingle {
		b.underline(to.Underline)
	}

	return b.finish()
}

// IsZero returns true if the style is empty.
func (s *Style) IsZero() bool {
	return *s == Style{}
}

// ConvertStyle converts a style to respect the given color profile.
func ConvertStyle(s Style, p colorprofile.Profile) Style {
	switch p {
	case colorprofile.TrueColor:
		return s
	case colorprofile.ANSI, colorprofile.ANSI256:
	case colorprofile.Ascii:
		s.Fg = 0
		s.Bg = 0
		s.UnderlineColor = 0
	case colorprofile.NoTTY:
		return Style{}
	}

	if s.Fg != 0 {
		s.Fg = NewColor(p.Convert(s.Fg.terminalColor()))
	}
	if s.Bg != 0 {
		s.Bg = NewColor(p.Convert(s.Bg.terminalColor()))
	}
	if s.UnderlineColor != 0 {
		s.UnderlineColor = NewColor(p.Convert(s.UnderlineColor.terminalColor()))
	}
	return s
}

// sgrBuilder writes parameters directly to the caller's retained buffer. The
// prefix and separators are emitted lazily, so no temporary strings are needed.
type sgrBuilder struct {
	buf   []byte
	start int
}

func (b *sgrBuilder) next() {
	if len(b.buf) == b.start {
		b.buf = append(b.buf, '\x1b', '[')
	} else {
		b.buf = append(b.buf, ';')
	}
}
func (b *sgrBuilder) attr(value string)   { b.next(); b.buf = append(b.buf, value...) }
func (b *sgrBuilder) number(value uint64) { b.buf = strconv.AppendUint(b.buf, value, 10) }
func (b *sgrBuilder) color(c Color, code uint64) {
	b.next()
	switch c & colorKind {
	case colorBasic:
		index := uint64(c & 255)
		if code != 58 {
			base := code - 8
			if index >= 8 {
				base += 60
				index -= 8
			}
			b.number(base + index)
			return
		}
		fallthrough
	case colorIndexed:
		b.number(code)
		b.buf = append(b.buf, ';', '5', ';')
		b.number(uint64(c & 255))
	case colorRGB:
		b.number(code)
		b.buf = append(b.buf, ';', '2', ';')
		b.number(uint64(c >> 16 & 255))
		b.buf = append(b.buf, ';')
		b.number(uint64(c >> 8 & 255))
		b.buf = append(b.buf, ';')
		b.number(uint64(c & 255))
	default:
		b.number(code + 1)
	}
}
func (b *sgrBuilder) underline(value Underline) {
	switch value {
	case UnderlineNone:
		b.attr("24")
	case UnderlineSingle:
		b.attr("4")
	case UnderlineDouble:
		b.attr("4:2")
	case UnderlineCurly:
		b.attr("4:3")
	case UnderlineDotted:
		b.attr("4:4")
	case UnderlineDashed:
		b.attr("4:5")
	}
}
func (b *sgrBuilder) finish() []byte {
	if len(b.buf) == b.start {
		return append(b.buf, ansi.ResetStyle...)
	}
	return append(b.buf, 'm')
}
