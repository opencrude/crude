package rendering

import (
	"bytes"
	"fmt"
	"image/color"
	"reflect"
	"slices"

	"crude/tui/theme"

	"charm.land/glamour/v2/ansi"
	"charm.land/glamour/v2/styles"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/util"
)

// MarkdownRenderer is Glamour's ANSI node renderer over a goldmark parser that
// does not recognize raw HTML. Chat text is not a web page: models routinely
// write literal tags (<context>, Vec<T>), and Glamour sanitizes recognized HTML
// away, silently deleting the user's or model's words. With HTML parsing off,
// every angle bracket stays ordinary text and no escaping pass is needed.
type MarkdownRenderer struct {
	md goldmark.Markdown
}

// Render returns the ANSI-styled Markdown projection of in.
func (r *MarkdownRenderer) Render(in string) (string, error) {
	var buf bytes.Buffer
	err := r.md.Convert([]byte(in), &buf)
	return buf.String(), err
}

func newMarkdownRenderer(options ansi.Options) *MarkdownRenderer {
	withoutHTML := func(values []util.PrioritizedValue, html any) []util.PrioritizedValue {
		return slices.DeleteFunc(values, func(value util.PrioritizedValue) bool {
			return reflect.TypeOf(value.Value) == reflect.TypeOf(html)
		})
	}
	md := goldmark.New(
		goldmark.WithParser(parser.NewParser(
			parser.WithBlockParsers(withoutHTML(parser.DefaultBlockParsers(), parser.NewHTMLBlockParser())...),
			parser.WithInlineParsers(withoutHTML(parser.DefaultInlineParsers(), parser.NewRawHTMLParser())...),
			parser.WithParagraphTransformers(parser.DefaultParagraphTransformers()...),
		)),
		// Same extensions and options as glamour.NewTermRenderer.
		goldmark.WithExtensions(extension.GFM, extension.DefinitionList),
		goldmark.WithParserOptions(parser.WithAutoHeadingID()),
	)
	md.SetRenderer(renderer.NewRenderer(renderer.WithNodeRenderers(util.Prioritized(ansi.NewRenderer(options), 1000))))
	return &MarkdownRenderer{md: md}
}

func MakeMdRenderer(width int, th *theme.Theme) *MarkdownRenderer {
	w := width - 4 - 16
	if w < 20 {
		w = 20
	}
	style := styles.DarkStyleConfig
	c := th.Colors
	r, g, b, _ := c.Bg.RGBA()
	light := 299*r+587*g+114*b >= 500*65535
	if light {
		style = styles.LightStyleConfig
	}
	ink := func(value color.Color) *string {
		r, g, b, _ := value.RGBA()
		return strPtr(fmt.Sprintf("#%02x%02x%02x", r>>8, g>>8, b>>8))
	}
	style.Document.Color = ink(c.Fg)
	// Retain Glamour's established heading/link/inline-code hierarchy. A
	// blanket Text.Color overrides inherited heading colours, not just prose.
	if light {
		style.Code.Color = ink(c.Code.Keyword)
		style.Link.Color, style.LinkText.Color = ink(c.Accent2), ink(c.Success)
		style.Image.Color, style.ImageText.Color = ink(c.Code.Function), ink(c.Muted)
		style.HorizontalRule.Color = ink(c.Border)
	}
	style.Document.Margin = uintPtr(0)
	style.CodeBlock.Margin = uintPtr(0)
	style.CodeBlock.Chroma = &ansi.Chroma{
		Text:                ansi.StylePrimitive{Color: ink(c.Fg)},
		Error:               ansi.StylePrimitive{Color: ink(c.Error)},
		Comment:             ansi.StylePrimitive{Color: ink(c.Code.Comment)},
		CommentPreproc:      ansi.StylePrimitive{Color: ink(c.Code.Type)},
		Keyword:             ansi.StylePrimitive{Color: ink(c.Code.Keyword)},
		KeywordReserved:     ansi.StylePrimitive{Color: ink(c.Code.Keyword)},
		KeywordNamespace:    ansi.StylePrimitive{Color: ink(c.Code.Keyword)},
		KeywordType:         ansi.StylePrimitive{Color: ink(c.Code.Type)},
		Operator:            ansi.StylePrimitive{Color: ink(c.Code.Operator)},
		Punctuation:         ansi.StylePrimitive{Color: ink(c.Fg)},
		Name:                ansi.StylePrimitive{Color: ink(c.Fg)},
		NameBuiltin:         ansi.StylePrimitive{Color: ink(c.Code.Type)},
		NameTag:             ansi.StylePrimitive{Color: ink(c.Success)},
		NameAttribute:       ansi.StylePrimitive{Color: ink(c.Code.Property)},
		NameClass:           ansi.StylePrimitive{Color: ink(c.Code.Type)},
		NameConstant:        ansi.StylePrimitive{Color: ink(c.Code.Number)},
		NameDecorator:       ansi.StylePrimitive{Color: ink(c.Code.Function)},
		NameFunction:        ansi.StylePrimitive{Color: ink(c.Code.Function)},
		LiteralNumber:       ansi.StylePrimitive{Color: ink(c.Code.Number)},
		LiteralString:       ansi.StylePrimitive{Color: ink(c.Code.String)},
		LiteralStringEscape: ansi.StylePrimitive{Color: ink(c.Code.Type)},
		GenericDeleted:      ansi.StylePrimitive{Color: ink(c.Diff.Delete)},
		GenericInserted:     ansi.StylePrimitive{Color: ink(c.Diff.Add)},
		GenericStrong:       ansi.StylePrimitive{Color: ink(c.Fg)},
		GenericSubheading:   ansi.StylePrimitive{Color: ink(c.Fg)},
	}
	style.CodeBlock.Color = ink(c.Fg)
	style.CodeBlock.BackgroundColor = ink(c.Code.Bg)
	style.CodeBlock.BlockPrefix = ""
	style.Paragraph.Margin = uintPtr(0)
	style.Heading.Margin = uintPtr(0)
	style.List.Margin = uintPtr(0)
	style.Table.Margin = uintPtr(0)

	return newMarkdownRenderer(ansi.Options{WordWrap: w, Styles: style})
}

func uintPtr(u uint) *uint    { return &u }
func strPtr(s string) *string { return &s }
