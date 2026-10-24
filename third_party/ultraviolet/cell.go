package uv

import (
	"strings"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
)

// EmptyCell is a cell with a single space, width of 1, and no style or link.
var EmptyCell = Cell{Content: " ", Width: 1}

// Cell represents a single cell in the terminal screen.
type Cell struct {
	// Content is the [Cell]'s content, which consists of a single grapheme
	// cluster. Most of the time, this will be a single rune as well, but it
	// can also be a combination of runes that form a grapheme cluster.
	Content string

	// The style of the cell. Nil style means no style. Zero value prints a
	// reset sequence.
	Style Style

	// Link is the hyperlink of the cell.
	Link Link

	// Width is the mono-spaced width of the grapheme cluster.
	Width int
}

// NewCell creates a new cell from the given string grapheme. It will only use
// the first grapheme in the string and ignore the rest. The width of the cell
// is determined using the given width method.
func NewCell(method WidthMethod, gr string) *Cell {
	if len(gr) == 0 {
		return &Cell{}
	}
	if gr == " " {
		return EmptyCell.Clone()
	}
	return &Cell{
		Content: gr,
		Width:   method.StringWidth(gr),
	}
}

// String returns the string content of the cell excluding any styles, links,
// and escape sequences.
func (c *Cell) String() string {
	return c.Content
}

// Equal returns whether the cell is equal to the other cell.
func (c *Cell) Equal(o *Cell) bool {
	return o != nil &&
		c.Width == o.Width &&
		c.Content == o.Content &&
		c.Style.Equal(&o.Style) &&
		c.Link.Equal(&o.Link)
}

// IsZero returns whether the cell is an empty cell.
func (c *Cell) IsZero() bool {
	return *c == Cell{}
}

// Clone returns a copy of the cell.
func (c *Cell) Clone() (n *Cell) {
	n = new(Cell)
	*n = *c
	return
}

// Empty makes the cell an empty cell by setting its content to a single space
// and width to 1.
func (c *Cell) Empty() {
	c.Content = " "
	c.Width = 1
}

// NewLink creates a new hyperlink with the given URL and parameters.
func NewLink(url string, params ...string) Link {
	return Link{
		URL:    url,
		Params: strings.Join(params, ":"),
	}
}

// Link represents a hyperlink in the terminal screen.
type Link struct {
	URL    string
	Params string
}

// String returns a string representation of the hyperlink.
func (h *Link) String() string {
	return h.URL
}

// Equal returns whether the hyperlink is equal to the other hyperlink.
func (h *Link) Equal(o *Link) bool {
	return o != nil && *h == *o
}

// IsZero returns whether the hyperlink is empty.
func (h *Link) IsZero() bool {
	return *h == Link{}
}

// These are the available text attributes that can be combined to create
// different styles.
const (
	AttrBold = 1 << iota
	AttrFaint
	AttrItalic
	AttrBlink
	AttrRapidBlink // Not widely supported
	AttrReverse
	AttrConceal
	AttrStrikethrough

	AttrReset = 0
)

// AttrSlowBlink is an alias for AttrBlink.
//
// Deprecated: Use [AttrBlink] instead.
const (
	AttrSlowBlink = AttrBlink
)

// Underline is the style of underline to use for text.
type Underline = ansi.Underline

// UnderlineStyle is the style of underline to use for text.
//
// Deprecated: Use [Underline] instead.
type UnderlineStyle = ansi.Underline

// These are the available underline styles.
const (
	UnderlineNone   = ansi.UnderlineNone
	UnderlineSingle = ansi.UnderlineSingle
	UnderlineDouble = ansi.UnderlineDouble
	UnderlineCurly  = ansi.UnderlineCurly
	UnderlineDotted = ansi.UnderlineDotted
	UnderlineDashed = ansi.UnderlineDashed
)

// These are the available underline styles.
//
// Deprecated: Use the constants from [Underline] instead.
const (
	UnderlineStyleNone   = ansi.UnderlineNone
	UnderlineStyleSingle = ansi.UnderlineSingle
	UnderlineStyleDouble = ansi.UnderlineDouble
	UnderlineStyleCurly  = ansi.UnderlineCurly
	UnderlineStyleDotted = ansi.UnderlineDotted
	UnderlineStyleDashed = ansi.UnderlineDashed
)

// ConvertLink converts a hyperlink to respect the given color profile.
func ConvertLink(h Link, p colorprofile.Profile) Link {
	if p == colorprofile.NoTTY {
		return Link{}
	}
	return h
}
