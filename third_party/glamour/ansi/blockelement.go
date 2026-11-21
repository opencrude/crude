package ansi

import (
	"bytes"
	"fmt"
	"io"
	"strings"

	"charm.land/lipgloss/v2"
)

// BlockElement provides a render buffer for children of a block element.
// After all children have been rendered into it, it applies indentation and
// margins around them and writes everything to the parent rendering buffer.
type BlockElement struct {
	Block   *bytes.Buffer
	Style   StyleBlock
	Margin  bool
	Newline bool

	direction blockDirection
	marker    string
}

// Render renders a BlockElement.
func (e *BlockElement) Render(w io.Writer, ctx RenderContext) error {
	bs := ctx.blockStack
	bs.Push(*e)

	_, _ = renderText(w, bs.Parent().Style.StylePrimitive, e.Style.BlockPrefix)
	_, _ = renderText(bs.Current().Block, bs.Current().Style.StylePrimitive, e.Style.Prefix)
	return nil
}

// Finish finishes rendering a BlockElement.
func (e *BlockElement) Finish(w io.Writer, ctx RenderContext) error {
	bs := ctx.blockStack

	if e.Margin { //nolint: nestif
		current := bs.Current()
		value := current.Block.String()
		if current.marker != "" {
			// An outside marker belongs on the first line of the item's
			// actual box, not the separator before a quote or a code block.
			value = strings.Trim(value, "\n")
		}
		s := lipgloss.Wrap(
			value,
			int(bs.Width(ctx)), //nolint: gosec
			" ,.;-+|",
		)

		mw := NewMarginWriter(ctx, w, current.Style)
		mw.iw.direction = current.direction
		mw.iw.first = current.marker
		defer mw.Close() //nolint:errcheck
		if s == "" && current.marker != "" {
			if err := mw.iw.writeIndent(); err != nil {
				return err
			}
		}
		if _, err := io.WriteString(mw, s); err != nil {
			return fmt.Errorf("glamour: error writing to writer: %w", err)
		}

		if e.Newline {
			if _, err := io.WriteString(mw, "\n"); err != nil {
				return fmt.Errorf("glamour: error writing to writer: %w", err)
			}
		}
	} else {
		_, err := bs.Parent().Block.Write(bs.Current().Block.Bytes())
		if err != nil {
			return fmt.Errorf("glamour: error writing to writer: %w", err)
		}
	}

	_, _ = renderText(w, bs.Current().Style.StylePrimitive, e.Style.Suffix)
	_, _ = renderText(w, bs.Parent().Style.StylePrimitive, e.Style.BlockSuffix)

	bs.Current().Block.Reset()
	bs.Pop()
	return nil
}
