package chatprojection

import (
	"strings"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/ui"
)

// RowSpan is one styled text run within a PaintLine. Colors resolve through
// ui.SemanticColor at paint time — no ANSI or lipgloss in the projection path.
type RowSpan struct {
	Text   string
	Role   ui.SemanticColor
	BgRole ui.SemanticColor
	BgSet  bool
	Attr   cell.Attrs
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
// directly into CellBuffer; Text holds ANSI fallback when Native is nil.
type SubPaneLine struct {
	Native *PaintLine
	Text   string
}

// PlainText returns selection/plain text for this row.
func (sl SubPaneLine) PlainText() string {
	if sl.Native != nil {
		return sl.Native.PlainText()
	}
	if sl.Text == "" {
		return ""
	}
	return rendering.StripAnsi(sl.Text)
}

// NativeLine wraps a PaintLine for subpane callbacks.
func NativeLine(pl PaintLine) SubPaneLine {
	t := pl.PlainText()
	return SubPaneLine{Native: &pl, Text: t}
}

// ANSILine wraps a legacy ANSI row (thinking MessageList content).
func ANSILine(s string) SubPaneLine {
	return SubPaneLine{Text: s}
}
