package ansi

import "io"

// A CodeSpanElement is used to render codespan.
type CodeSpanElement struct {
	Text  string
	Style StylePrimitive
}

// Render renders a CodeSpanElement.
func (e *CodeSpanElement) Render(w io.Writer, ctx RenderContext) error {
	if ctx.options.Source == nil {
		_, _ = renderText(w, e.Style, e.Style.Prefix+e.Text+e.Style.Suffix)
		return nil
	}
	_, _ = renderText(w, e.Style, e.Style.Prefix)
	_, _ = renderSourceText(w, e.Style, e.Text, ctx.options.Source)
	_, _ = renderText(w, e.Style, e.Style.Suffix)
	return nil
}
