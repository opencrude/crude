package ansi

import (
	"bytes"
	"io"
	"strconv"

	"github.com/charmbracelet/x/ansi"
)

// itemBox sizes the marker in the established style and reserves its width for
// every child row. Only the first row displays the marker. The box uses the
// enclosing list's direction (CSS Lists 3, marker-side: match-parent).
type itemBox struct {
	marker ElementRenderer
}

func (e *itemBox) Render(_ io.Writer, ctx RenderContext) error {
	var marker bytes.Buffer
	if err := e.marker.Render(&marker, ctx); err != nil {
		return err
	}
	width := uint(ansi.StringWidth(marker.String())) //nolint:gosec
	block := BlockElement{
		Block:     &bytes.Buffer{},
		Style:     StyleBlock{StylePrimitive: ctx.blockStack.Current().Style.StylePrimitive, Indent: &width},
		direction: ctx.blockStack.direction(),
		marker:    marker.String(),
	}
	// The stack owns the measured box and marker until Finish.
	ctx.blockStack.Push(block)
	return nil
}

func (e *itemBox) Finish(w io.Writer, ctx RenderContext) error {
	// Inherited text colors do not make the parent's block suffix the
	// item's suffix as well.
	return (&BlockElement{Margin: true}).Finish(w, ctx)
}

// An ItemElement is used to render items inside a list.
type ItemElement struct {
	IsOrdered   bool
	Enumeration uint
}

// Render renders an ItemElement.
func (e *ItemElement) Render(w io.Writer, ctx RenderContext) error {
	var el *BaseElement
	if e.IsOrdered {
		el = &BaseElement{
			Style:  ctx.options.Styles.Enumeration,
			Prefix: strconv.FormatInt(int64(e.Enumeration), 10), //nolint: gosec
		}
	} else {
		el = &BaseElement{
			Style: ctx.options.Styles.Item,
		}
	}

	return el.Render(w, ctx)
}
