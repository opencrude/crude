package ansi

import (
	"bytes"
	"fmt"
	"io"
	"sync"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
)

const (
	// The chroma style theme name used for rendering.
	chromaStyleTheme = "charm"

	// The chroma formatter name used for rendering.
	chromaFormatter = "terminal256"
)

// mutex for synchronizing access to the chroma style registry.
// Related https://github.com/alecthomas/chroma/pull/650
var mutex = sync.Mutex{}

// codeLexers memoizes chroma's lexer lookup by fence language. For a name it
// does not know as a lexer name or alias, such as the "" of an unlabeled
// fence, lexers.Get globs every registered file pattern twice (about 3 ms) on
// each render. The registry is immutable after init, so the answer, nil
// included, never changes; the table is bounded because labels are free text.
var codeLexers struct {
	sync.Mutex
	byLanguage map[string]chroma.Lexer
}

const codeLexerLimit = 1024

func codeLexer(language string) chroma.Lexer {
	codeLexers.Lock()
	lexer, known := codeLexers.byLanguage[language]
	codeLexers.Unlock()
	if known {
		return lexer
	}
	lexer = lexers.Get(language)
	codeLexers.Lock()
	defer codeLexers.Unlock()
	if codeLexers.byLanguage == nil || len(codeLexers.byLanguage) >= codeLexerLimit {
		codeLexers.byLanguage = make(map[string]chroma.Lexer)
	}
	codeLexers.byLanguage[language] = lexer
	return lexer
}

// highlight is quick.Highlight with the memoized lexer lookup: a known lexer,
// else analysis of the code, else plain text.
func highlight(w io.Writer, source, language, formatter, style string) error {
	lexer := codeLexer(language)
	if lexer == nil {
		lexer = lexers.Analyse(source)
	}
	if lexer == nil {
		lexer = lexers.Fallback
	}
	lexer = chroma.Coalesce(lexer)
	format := formatters.Get(formatter)
	if format == nil {
		format = formatters.Fallback
	}
	theme := styles.Get(style)
	if theme == nil {
		theme = styles.Fallback
	}
	tokens, err := lexer.Tokenise(nil, source)
	if err != nil {
		return err
	}
	return format.Format(w, theme, tokens)
}

// A CodeBlockElement is used to render code blocks.
type CodeBlockElement struct {
	Code     string
	Language string
}

func chromaStyle(style StylePrimitive) string {
	var s string

	if style.Color != nil {
		s = *style.Color
	}
	if style.BackgroundColor != nil {
		if s != "" {
			s += " "
		}
		s += "bg:" + *style.BackgroundColor
	}
	if style.Italic != nil && *style.Italic {
		if s != "" {
			s += " "
		}
		s += "italic"
	}
	if style.Bold != nil && *style.Bold {
		if s != "" {
			s += " "
		}
		s += "bold"
	}
	if style.Underline != nil && *style.Underline {
		if s != "" {
			s += " "
		}
		s += "underline"
	}

	return s
}

// Render renders a CodeBlockElement.
func (e *CodeBlockElement) Render(w io.Writer, ctx RenderContext) error {
	bs := ctx.blockStack

	var indentation uint
	var margin uint
	formatter := chromaFormatter
	rules := ctx.options.Styles.CodeBlock
	if rules.Indent != nil {
		indentation = *rules.Indent
	}
	if rules.Margin != nil {
		margin = *rules.Margin
	}
	if len(ctx.options.ChromaFormatter) > 0 {
		formatter = ctx.options.ChromaFormatter
	}
	theme := rules.Theme

	if rules.Chroma != nil {
		theme = chromaStyleTheme
		mutex.Lock()
		// Don't register the style if it's already registered.
		_, ok := styles.Registry[theme]
		if !ok {
			styles.Register(chroma.MustNewStyle(theme,
				chroma.StyleEntries{
					chroma.Text:                chromaStyle(rules.Chroma.Text),
					chroma.Error:               chromaStyle(rules.Chroma.Error),
					chroma.Comment:             chromaStyle(rules.Chroma.Comment),
					chroma.CommentPreproc:      chromaStyle(rules.Chroma.CommentPreproc),
					chroma.Keyword:             chromaStyle(rules.Chroma.Keyword),
					chroma.KeywordReserved:     chromaStyle(rules.Chroma.KeywordReserved),
					chroma.KeywordNamespace:    chromaStyle(rules.Chroma.KeywordNamespace),
					chroma.KeywordType:         chromaStyle(rules.Chroma.KeywordType),
					chroma.Operator:            chromaStyle(rules.Chroma.Operator),
					chroma.Punctuation:         chromaStyle(rules.Chroma.Punctuation),
					chroma.Name:                chromaStyle(rules.Chroma.Name),
					chroma.NameBuiltin:         chromaStyle(rules.Chroma.NameBuiltin),
					chroma.NameTag:             chromaStyle(rules.Chroma.NameTag),
					chroma.NameAttribute:       chromaStyle(rules.Chroma.NameAttribute),
					chroma.NameClass:           chromaStyle(rules.Chroma.NameClass),
					chroma.NameConstant:        chromaStyle(rules.Chroma.NameConstant),
					chroma.NameDecorator:       chromaStyle(rules.Chroma.NameDecorator),
					chroma.NameException:       chromaStyle(rules.Chroma.NameException),
					chroma.NameFunction:        chromaStyle(rules.Chroma.NameFunction),
					chroma.NameOther:           chromaStyle(rules.Chroma.NameOther),
					chroma.Literal:             chromaStyle(rules.Chroma.Literal),
					chroma.LiteralNumber:       chromaStyle(rules.Chroma.LiteralNumber),
					chroma.LiteralDate:         chromaStyle(rules.Chroma.LiteralDate),
					chroma.LiteralString:       chromaStyle(rules.Chroma.LiteralString),
					chroma.LiteralStringEscape: chromaStyle(rules.Chroma.LiteralStringEscape),
					chroma.GenericDeleted:      chromaStyle(rules.Chroma.GenericDeleted),
					chroma.GenericEmph:         chromaStyle(rules.Chroma.GenericEmph),
					chroma.GenericInserted:     chromaStyle(rules.Chroma.GenericInserted),
					chroma.GenericStrong:       chromaStyle(rules.Chroma.GenericStrong),
					chroma.GenericSubheading:   chromaStyle(rules.Chroma.GenericSubheading),
					chroma.Background:          chromaStyle(rules.Chroma.Background),
				}))
		}
		mutex.Unlock()
	}

	iw := NewIndentWriter(w, int(indentation+margin), func(_ io.Writer) { //nolint:gosec
		_, _ = renderText(w, bs.Current().Style.StylePrimitive, " ")
	}, ctx.options.Source)
	defer iw.Close() //nolint:errcheck

	if len(theme) > 0 {
		_, _ = renderText(iw, bs.Current().Style.StylePrimitive, rules.BlockPrefix)

		var highlighted bytes.Buffer
		output := io.Writer(iw)
		if ctx.options.Source != nil {
			output = &highlighted
		}
		err := highlight(output, e.Code, e.Language, formatter, theme)
		if err != nil {
			return fmt.Errorf("glamour: error highlighting code: %w", err)
		}
		if ctx.options.Source != nil {
			_, _ = io.WriteString(iw, ctx.options.Source.Code(highlighted.String(), e.Code, e.Language))
		}
		_, _ = renderText(iw, bs.Current().Style.StylePrimitive, rules.BlockSuffix)
		return nil
	}

	// fallback rendering
	el := &BaseElement{
		Token:     e.Code,
		Presented: true, // Literal code is not Markdown escape syntax.
		Style:     rules.StylePrimitive,
	}

	if ctx.options.Source == nil {
		return el.Render(iw, ctx)
	}
	var plain bytes.Buffer
	untraced := ctx
	untraced.options.Source = nil
	if err := el.Render(&plain, untraced); err != nil {
		return err
	}
	_, err := io.WriteString(iw, ctx.options.Source.Code(plain.String(), e.Code, e.Language))
	return err
}
