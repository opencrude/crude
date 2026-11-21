package ansi

import (
	"bytes"
	"fmt"
	"io"
	"slices"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
	astext "github.com/yuin/goldmark/extension/ast"
)

// A TableElement is used to render tables.
type TableElement struct {
	lipgloss *table.Table
	table    *astext.Table
	header   []string
	row      []string
	source   []byte

	tableImages []tableLink
	tableLinks  []tableLink

	headerRTL, headerStrong bool
	rowCells                []tracedCell
	cells                   [][]tracedCell // header, then body rows, in layout order
}

type tracedCell struct {
	id  int
	rtl bool
}

// A TableRowElement is used to render a single row in a table.
type TableRowElement struct{}

// A TableHeadElement is used to render a table's head element.
type TableHeadElement struct{}

// A TableCellElement is used to render a single cell in a row.
type TableCellElement struct {
	Children []ElementRenderer
	Head     bool
}

// Render renders a TableElement.
func (e *TableElement) Render(w io.Writer, ctx RenderContext) error {
	bs := ctx.blockStack

	var indentation uint
	var margin uint
	rules := ctx.options.Styles.Table
	if rules.Indent != nil {
		indentation = *rules.Indent
	}
	if rules.Margin != nil {
		margin = *rules.Margin
	}

	iw := NewIndentWriter(w, int(indentation+margin), func(_ io.Writer) { //nolint:gosec
		_, _ = renderText(w, bs.Current().Style.StylePrimitive, " ")
	})
	defer iw.Close() //nolint:errcheck

	style := bs.With(rules.StylePrimitive)

	_, _ = renderText(iw, bs.Current().Style.StylePrimitive, rules.BlockPrefix)
	_, _ = renderText(iw, style, rules.Prefix)
	width := int(ctx.blockStack.Width(ctx)) //nolint: gosec

	wrap := true
	if ctx.options.TableWrap != nil {
		wrap = *ctx.options.TableWrap
	}
	ctx.table.lipgloss = table.New().Width(width).Wrap(wrap)
	if source := ctx.options.Source; source != nil {
		source.BeginTable()
		ctx.table.lipgloss.CellRenderFunc(func(row, col int, style lipgloss.Style, content string) string {
			cell := ctx.table.cells[row+1][col]
			return source.TableCellBox(cell.id, style.RenderContent(source.TableCellContent, content))
		})
	}

	if err := e.collectLinksAndImages(ctx); err != nil {
		return err
	}

	return nil
}

func (e *TableElement) setStyles(ctx RenderContext) {
	docRules := ctx.options.Styles.Document
	if docRules.BackgroundColor != nil {
		baseStyle := lipgloss.NewStyle().Background(lipgloss.Color(*docRules.BackgroundColor))
		ctx.table.lipgloss.BaseStyle(baseStyle)
	}

	ctx.table.lipgloss = ctx.table.lipgloss.StyleFunc(func(row, col int) lipgloss.Style {
		st := lipgloss.NewStyle().Inline(false)
		// Default Styles
		st = st.Margin(0, 1)

		// Override with custom styles
		if m := ctx.options.Styles.Table.Margin; m != nil {
			st = st.Padding(0, int(*m)) //nolint: gosec
		}
		sourceCol := col
		if ctx.table.headerRTL {
			sourceCol = len(e.table.Alignments) - 1 - col
		}
		switch e.table.Alignments[sourceCol] {
		case astext.AlignLeft:
			st = st.Align(lipgloss.Left).PaddingRight(0)
		case astext.AlignCenter:
			st = st.Align(lipgloss.Center)
		case astext.AlignRight:
			st = st.Align(lipgloss.Right).PaddingLeft(0)
		case astext.AlignNone:
			if ctx.options.Source != nil && ctx.table.cells[row+1][col].rtl {
				st = st.Align(lipgloss.Right)
			}
		}

		return st
	})
}

func (e *TableElement) setBorders(ctx RenderContext) {
	rules := ctx.options.Styles.Table
	border := lipgloss.NormalBorder()

	if rules.RowSeparator != nil && rules.ColumnSeparator != nil {
		border = lipgloss.Border{
			Top:    *rules.RowSeparator,
			Bottom: *rules.RowSeparator,
			Left:   *rules.ColumnSeparator,
			Right:  *rules.ColumnSeparator,
			Middle: *rules.CenterSeparator,
		}
	}
	ctx.table.lipgloss.Border(border)
	ctx.table.lipgloss.BorderTop(false)
	ctx.table.lipgloss.BorderLeft(false)
	ctx.table.lipgloss.BorderRight(false)
	ctx.table.lipgloss.BorderBottom(false)
}

// Finish finishes rendering a TableElement.
func (e *TableElement) Finish(_ io.Writer, ctx RenderContext) error {
	defer func() {
		*ctx.table = TableElement{}
	}()

	if ctx.options.Source != nil {
		ctx.options.Source.EndTable()
	}
	rules := ctx.options.Styles.Table

	e.setStyles(ctx)
	e.setBorders(ctx)

	ow := ctx.blockStack.Current().Block
	if _, err := ow.WriteString(ctx.table.lipgloss.String()); err != nil {
		return fmt.Errorf("glamour: error writing to buffer: %w", err)
	}

	_, _ = renderText(ow, ctx.blockStack.With(rules.StylePrimitive), rules.Suffix)
	_, _ = renderText(ow, ctx.blockStack.Current().Style.StylePrimitive, rules.BlockSuffix)

	e.printTableLinks(ctx)

	return nil
}

// Finish finishes rendering a TableRowElement.
func (e *TableRowElement) Finish(_ io.Writer, ctx RenderContext) error {
	if ctx.table.lipgloss == nil {
		return nil
	}

	ctx.table.finishRow(ctx.table.row)
	ctx.table.lipgloss.Row(ctx.table.row...)
	ctx.table.row = []string{}
	return nil
}

// Finish finishes rendering a TableHeadElement.
func (e *TableHeadElement) Finish(_ io.Writer, ctx RenderContext) error {
	if ctx.table.lipgloss == nil {
		return nil
	}

	if ctx.options.Source != nil && !ctx.table.headerStrong {
		ctx.table.headerRTL = ctx.blockStack.direction() == rightToLeft
	}
	ctx.table.finishRow(ctx.table.header)
	ctx.table.lipgloss.Headers(ctx.table.header...)
	ctx.table.header = []string{}
	return nil
}

func (e *TableElement) finishRow(row []string) {
	if e.rowCells == nil {
		return
	}
	if e.headerRTL {
		slices.Reverse(row)
		slices.Reverse(e.rowCells)
	}
	e.cells = append(e.cells, e.rowCells)
	e.rowCells = nil
}

// Render renders a TableCellElement.
func (e *TableCellElement) Render(_ io.Writer, ctx RenderContext) error {
	var b bytes.Buffer
	style := ctx.options.Styles.Table.StylePrimitive
	for _, child := range e.Children {
		if r, ok := child.(StyleOverriderElementRenderer); ok {
			if err := r.StyleOverrideRender(&b, ctx, style); err != nil {
				return fmt.Errorf("glamour: error rendering with style: %w", err)
			}
		} else {
			var bb bytes.Buffer
			if err := child.Render(&bb, ctx); err != nil {
				return fmt.Errorf("glamour: error rendering: %w", err)
			}
			el := &BaseElement{
				Token:     bb.String(),
				Presented: true,
				Style:     style,
			}
			if err := el.Render(&b, ctx); err != nil {
				return err
			}
		}
	}

	if source := ctx.options.Source; source != nil {
		id, rtl, strong := source.EndTableCell()
		ctx.table.rowCells = append(ctx.table.rowCells, tracedCell{id: id, rtl: rtl})
		if e.Head && !ctx.table.headerStrong && strong {
			ctx.table.headerStrong = true
			ctx.table.headerRTL = rtl
		}
	}
	if e.Head {
		ctx.table.header = append(ctx.table.header, b.String())
	} else {
		ctx.table.row = append(ctx.table.row, b.String())
	}

	return nil
}
