package chatprojection

import (
	"strings"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/ui"
)

// RowSpan is one styled text run within a PaintLine. Colors normally resolve
// through ui.SemanticColor at paint time — no ANSI in the projection path. For bridged ANSI content that carries explicit colours
// that do not correspond to a theme role (e.g. Chroma syntax-highlight
// 24-bit colours), FGColor/BGColor provide an optional direct override that
// is painted verbatim; when FGSet/BGSet is false, the Role/BgRole is resolved
// through TokenSet as usual. Known theme colours are mapped back to their
// SemanticColor role by the bridge so they follow theme preset switches.
type RowSpan struct {
	Text   string
	Role   ui.SemanticColor
	BgRole ui.SemanticColor
	BgSet  bool
	Attr   cell.Attrs
	// FGColor, when FGSet is true, overrides Role at paint time.
	FGColor cell.Color
	FGSet   bool
	// BGColor, when BGSet is true, overrides BgRole at paint time.
	BGColor cell.Color
	BGSet   bool
}

// PaintLine is a single terminal row of semantic spans with optional subpane
// background applied at compose/paint time (not stored as ANSI).
//
// ANSIFallback marks "no native row — paint Lines[i] via the ANSI path".
// An empty-but-native spacer uses Spans with empty text (see buildPlainBodyLine)
// and ANSIFallback=false so blank lines never collide with the fallback signal.
type PaintLine struct {
	Spans        []RowSpan
	ANSIFallback bool
	HeaderRule   bool // Extend the faint message header rule through the chat margins.
	TextTide     bool // Paint-only animation, independent of Markdown/source caches.
	TideForward  bool // Confirmed reasoning travels left-to-right; provisional reverses.
	TideFrame    int
}

// PlainText returns visible text without styling for selection and width math.
func (pl PaintLine) PlainText() string {
	if len(pl.Spans) == 0 {
		return ""
	}
	var b strings.Builder
	for _, s := range pl.Spans {
		b.WriteString(s.Text)
	}
	return b.String()
}

// VisibleWidth returns the grapheme width of PlainText().
func (pl PaintLine) VisibleWidth() int {
	return rendering.VisibleWidth(pl.PlainText())
}

// IsZero reports whether the line carries no paintable spans.
// Prefer PreferANSI() when deciding native vs ANSI paint — blank native
// spacers are non-zero (empty-text span) while ANSI fallback may also be empty.
func (pl PaintLine) IsZero() bool {
	return len(pl.Spans) == 0 && !pl.ANSIFallback
}

// PreferANSI reports whether shellchat must paint the parallel ANSI/string line.
func (pl PaintLine) PreferANSI() bool {
	return pl.ANSIFallback
}

// SubPaneLine is one row emitted by subpane projection. Native lines paint
// directly into CellBuffer; Text holds the plain-text mirror (set by
// NativeLine) used by selection/copy. The legacy ANSI-only path (ANSILine)
// has been removed — every row carries a Native PaintLine.
type SubPaneLine struct {
	Native         *PaintLine
	Text           string
	FrameSensitive bool
}

// PlainText returns selection/plain text for this row.
func (sl SubPaneLine) PlainText() string {
	if sl.Native != nil {
		return sl.Native.PlainText()
	}
	return sl.Text
}

// NativeLine wraps a PaintLine for subpane callbacks. Text is set to the
// plain-text mirror for selection/copy consumers.
func NativeLine(pl PaintLine) SubPaneLine {
	t := pl.PlainText()
	return SubPaneLine{Native: &pl, Text: t}
}
