package ui

import (
	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/theme"
)

// Label is a non-interactive text primitive.
type Label struct {
	Base
	text          string
	fg            cell.Color
	bg            cell.Color
	attr          cell.Attrs
	role          SemanticRole
	label         string // a11y label override
	tokens        *TokenSet
	colorRole     SemanticColor
	semanticColor bool
}

// NewLabel creates a label with theme foreground.
func NewLabel(id ComponentID, text string, th *theme.Theme) *Label {
	l := &Label{
		Base: NewBase(id),
		text: text,
		fg:   cell.NoColor,
		bg:   cell.NoColor,
		role: RoleLabel,
	}
	if th != nil {
		l.tokens = NewTokenSet(th)
		l.colorRole = ColorFg
		l.semanticColor = true
	}
	return l
}

// SetText updates label text.
func (l *Label) SetText(s string) { l.text = s }

// Text returns the label text.
func (l *Label) Text() string { return l.text }

// SetStyle sets fg/bg/attr.
func (l *Label) SetStyle(fg, bg cell.Color, attr cell.Attrs) {
	l.fg = fg
	l.bg = bg
	l.attr = attr
	l.semanticColor = false
}

// SetTokens updates the active runtime theme tokens.
func (l *Label) SetTokens(tokens *TokenSet) { l.tokens = tokens }

// SetColorRole makes the label resolve its foreground from a semantic token.
func (l *Label) SetColorRole(role SemanticColor) {
	l.colorRole = role
	l.semanticColor = true
}

// Measure prefers text width x 1 row, clamped.
func (l *Label) Measure(_ LayoutContext, cs Constraints) Size {
	w := rendering.VisibleWidth(l.text)
	if w < 1 {
		w = 0
	}
	return cs.ClampSize(Size{W: w, H: 1})
}

// Layout stores bounds.
func (l *Label) Layout(_ LayoutContext, r Rect) {
	l.SetBounds(r)
}

// Paint writes text into the buffer at origin + bounds.
func (l *Label) Paint(buf *surface.CellBuffer, originX, originY int, _ PaintContext) {
	b := l.Bounds()
	if b.Empty() {
		return
	}
	// Optional background fill
	if !l.bg.IsDefault() {
		FillRect(buf, originX, originY, b, cell.BlankCell(l.bg))
	}
	x := originX + b.X
	y := originY + b.Y
	maxW := b.W
	// Truncate with ellipsis if needed
	text := rendering.TruncateLine(l.text, maxW)
	fg := l.fg
	if l.semanticColor && l.tokens != nil {
		fg = l.tokens.Color(l.colorRole)
	}
	buf.WriteString(x, y, text, fg, l.bg, l.attr)
}

// Semantics returns label semantics.
func (l *Label) Semantics() Semantics {
	lab := l.label
	if lab == "" {
		lab = l.text
	}
	return Semantics{Role: RoleLabel, Label: lab, Value: l.text}
}
