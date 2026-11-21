package ansi

import (
	"fmt"
	"io"
	"net/url"
	"strings"

	east "github.com/yuin/goldmark-emoji/ast"
	"github.com/yuin/goldmark/ast"
	astext "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/util"
)

// SourceTrace observes semantic text before layout adds margins or wraps it.
// Mark returns an annotated styled string; MarkCode does the same for the
// text of an inline code span (Crude). Begin separates logical blocks.
// Pause and Resume bracket generated padding. Code traces a code block and
// its optional trailing action. Decoration marks fixed runs outside prose.
// BoxPrefix marks the inline-start gutter of a semantic container.
// Table hooks retain source order, report direction, and mark existing
// layout boundaries without parsing the result.
type SourceTrace interface {
	Begin(separator string)
	Mark(styled, text string) string
	MarkCode(styled, text string) string
	Code(styled, text, language string) string
	Pause() string
	Resume() string
	Decoration(start bool) string
	BoxPrefix(start, rtl bool) string
	Direction(text string) (rtl, strong bool)
	BeginTable()
	EndTable()
	BeginTableCell(separator string)
	EndTableCell() (id int, rtl, strong bool)
	TableCellBox(id int, styled string) string
	TableCellContent(styled string) string
}

// Options is used to configure an ANSIRenderer.
type Options struct {
	Source           SourceTrace
	BaseURL          string
	WordWrap         int
	TableWrap        *bool
	InlineTableLinks bool
	PreserveNewLines bool
	Styles           StyleConfig
	ChromaFormatter  string
}

// ANSIRenderer renders markdown content as ANSI escaped sequences.
type ANSIRenderer struct { //nolint: revive
	context    RenderContext
	directions map[ast.Node]blockDirection
}

// NewRenderer returns a new ANSIRenderer with style and options set.
func NewRenderer(options Options) *ANSIRenderer {
	return &ANSIRenderer{
		context: NewRenderContext(options),
	}
}

// RegisterFuncs implements NodeRenderer.RegisterFuncs.
func (r *ANSIRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	// blocks
	reg.Register(ast.KindDocument, r.renderNode)
	reg.Register(ast.KindHeading, r.renderNode)
	reg.Register(ast.KindBlockquote, r.renderNode)
	reg.Register(ast.KindCodeBlock, r.renderNode)
	reg.Register(ast.KindFencedCodeBlock, r.renderNode)
	reg.Register(ast.KindHTMLBlock, r.renderNode)
	reg.Register(ast.KindList, r.renderNode)
	reg.Register(ast.KindListItem, r.renderNode)
	reg.Register(ast.KindParagraph, r.renderNode)
	reg.Register(ast.KindTextBlock, r.renderNode)
	reg.Register(ast.KindThematicBreak, r.renderNode)

	// inlines
	reg.Register(ast.KindAutoLink, r.renderNode)
	reg.Register(ast.KindCodeSpan, r.renderNode)
	reg.Register(ast.KindEmphasis, r.renderNode)
	reg.Register(ast.KindImage, r.renderNode)
	reg.Register(ast.KindLink, r.renderNode)
	reg.Register(ast.KindRawHTML, r.renderNode)
	reg.Register(ast.KindText, r.renderNode)
	reg.Register(ast.KindString, r.renderNode)

	// tables
	reg.Register(astext.KindTable, r.renderNode)
	reg.Register(astext.KindTableHeader, r.renderNode)
	reg.Register(astext.KindTableRow, r.renderNode)
	reg.Register(astext.KindTableCell, r.renderNode)

	// definitions
	reg.Register(astext.KindDefinitionList, r.renderNode)
	reg.Register(astext.KindDefinitionTerm, r.renderNode)
	reg.Register(astext.KindDefinitionDescription, r.renderNode)

	// footnotes
	reg.Register(astext.KindFootnote, r.renderNode)
	reg.Register(astext.KindFootnoteList, r.renderNode)
	reg.Register(astext.KindFootnoteLink, r.renderNode)
	reg.Register(astext.KindFootnoteBacklink, r.renderNode)

	// checkboxes
	reg.Register(astext.KindTaskCheckBox, r.renderNode)

	// strikethrough
	reg.Register(astext.KindStrikethrough, r.renderNode)

	// emoji
	reg.Register(east.KindEmoji, r.renderNode)
}

func (r *ANSIRenderer) renderNode(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	writeTo := io.Writer(w)
	bs := r.context.blockStack

	// children get rendered by their parent
	if isChild(node) {
		return ast.WalkContinue, nil
	}

	if entering && r.context.options.Source != nil {
		switch node.Kind() {
		case ast.KindParagraph, ast.KindHeading, ast.KindCodeBlock, ast.KindFencedCodeBlock:
			r.context.options.Source.Begin("\n\n")
		case ast.KindTextBlock:
			r.context.options.Source.Begin("\n")
		case astext.KindTableCell:
			separator := "\t"
			if node.PreviousSibling() == nil {
				separator = "\n"
			}
			r.context.options.Source.BeginTableCell(separator)
		case astext.KindTable, astext.KindTableHeader, astext.KindTableRow:
			// The table owns its semantic text, including empty cells.
		default:
			// Goldmark also supplies extension-owned blocks (for example
			// definition terms). Their boundary is semantic, not a paint gap.
			if node.Type() == ast.TypeBlock {
				r.context.options.Source.Begin("\n")
			}
		}
	}
	e := r.NewElement(node, source)
	if entering { //nolint: nestif
		if trace := r.context.options.Source; trace != nil {
			switch node.Kind() {
			case ast.KindDocument:
				r.directions = nil
			case ast.KindBlockquote, ast.KindList:
				if r.directions == nil {
					r.directions = make(map[ast.Node]blockDirection)
				}
				if _, known := r.directions[node]; !known {
					scanContainerDirections(node, source, trace, r.directions)
				}
				dir := r.directions[node]
				if dir == noBlockDirection {
					dir = bs.direction()
				}
				e.Renderer.(*BlockElement).direction = dir
			}
		}
		// everything below the Document element gets rendered into a block buffer
		if bs.Len() > 0 {
			writeTo = io.Writer(bs.Current().Block)
		}

		_, _ = io.WriteString(writeTo, e.Entering)
		if e.Renderer != nil {
			err := e.Renderer.Render(writeTo, r.context)
			if err != nil {
				return ast.WalkStop, fmt.Errorf("glamour: error rendering: %w", err)
			}
		}
	} else {
		// everything below the Document element gets rendered into a block buffer
		if bs.Len() > 0 {
			writeTo = io.Writer(bs.Parent().Block)
		}

		// if we're finished rendering the entire document,
		// flush to the real writer
		if node.Type() == ast.TypeDocument {
			r.directions = nil
			writeTo = w
		}

		if e.Finisher != nil {
			err := e.Finisher.Finish(writeTo, r.context)
			if err != nil {
				return ast.WalkStop, fmt.Errorf("glamour: error finishing render: %w", err)
			}
		}

		_, _ = io.WriteString(bs.Current().Block, e.Exiting)
	}

	return ast.WalkContinue, nil
}

func isChild(node ast.Node) bool {
	for n := node.Parent(); n != nil; n = n.Parent() {
		// These types are already rendered by their parent
		switch n.Kind() {
		case ast.KindCodeSpan, ast.KindAutoLink, ast.KindLink, ast.KindImage, ast.KindEmphasis, astext.KindStrikethrough, astext.KindTableCell:
			return true
		}
	}

	return false
}

func resolveRelativeURL(baseURL string, rel string) string {
	u, err := url.Parse(rel)
	if err != nil {
		return rel
	}
	if u.IsAbs() {
		return rel
	}
	u.Path = strings.TrimPrefix(u.Path, "/")

	base, err := url.Parse(baseURL)
	if err != nil {
		return rel
	}
	return base.ResolveReference(u).String()
}
