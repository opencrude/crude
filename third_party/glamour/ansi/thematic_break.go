package ansi

import (
	"io"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// A parsed rule is block decoration, not semantic source text. Use the
// same container width Glamour gives a paragraph at this nesting level.
type thematicBreakElement struct{}

func (thematicBreakElement) Render(w io.Writer, ctx RenderContext) error {
	const glyph = "─"
	width, step := int(ctx.blockStack.Width(ctx)), max(1, ansi.StringWidth(glyph)) //nolint:gosec
	rule := strings.Repeat(glyph, width/step) + strings.Repeat(" ", width%step)
	_, err := renderText(w, ctx.blockStack.With(ctx.options.Styles.HorizontalRule), rule)
	return err
}
