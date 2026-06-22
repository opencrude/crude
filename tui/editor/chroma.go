package editor

import (
	"fmt"
	"image/color"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"

	"crude/tui/rendering"
	"crude/tui/theme"
)

func (e *Editor) initChroma() {
	e.chromaStyle = buildChromaStyle(e.th)
	path := e.filePath
	if path == "" {
		path = e.title
	}
	l := lexers.Match(path)
	if l == nil || l.Config().Name == "fallback" {
		l = lexers.Fallback
	}
	e.chromaLexer = chroma.Coalesce(l)
	e.chromaCache = nil
}

func (e *Editor) buildLineColorMap(lineStr string, lineVisW int) []string {
	if e.chromaLexer == nil || e.chromaStyle == nil || lineStr == "" || lineVisW <= 0 {
		return nil
	}
	if e.chromaCache == nil {
		e.chromaCache = make(map[string][]string)
	}
	if cached, ok := e.chromaCache[lineStr]; ok {
		return cached
	}
	it, err := e.chromaLexer.Tokenise(nil, lineStr)
	if err != nil {
		return nil
	}
	cm := make([]string, lineVisW)
	col := 0
	for tk := it(); tk != chroma.EOF; tk = it() {
		clr := e.chromaTokenColor(tk.Type)
		if clr == "" {
			col += rendering.VisibleWidth(tk.Value)
			continue
		}
		for _, r := range tk.Value {
			w := rendering.VisibleWidth(string(r))
			for c := col; c < col+w && c < lineVisW; c++ {
				cm[c] = clr
			}
			col += w
		}
	}
	e.chromaCache[lineStr] = cm
	return cm
}

func (e *Editor) buildLineColorMapWithRunes(lineStr string, lineVisW int, runes []rune, widths []int) []string {
	if e.chromaLexer == nil || e.chromaStyle == nil || lineStr == "" || lineVisW <= 0 {
		return nil
	}
	if e.chromaCache == nil {
		e.chromaCache = make(map[string][]string)
	}
	if cached, ok := e.chromaCache[lineStr]; ok {
		return cached
	}
	it, err := e.chromaLexer.Tokenise(nil, lineStr)
	if err != nil {
		return nil
	}
	cm := make([]string, lineVisW)
	col := 0
	runeIdx := 0
	for tk := it(); tk != chroma.EOF; tk = it() {
		if col >= lineVisW {
			break
		}
		clr := e.chromaTokenColor(tk.Type)
		tkRunes := []rune(tk.Value)
		if clr == "" {
			for range tkRunes {
				if runeIdx >= len(runes) {
					break
				}
				w := widths[runeIdx]
				col += w
				runeIdx++
				if col >= lineVisW {
					break
				}
			}
			continue
		}
		for range tkRunes {
			if runeIdx >= len(runes) {
				break
			}
			w := widths[runeIdx]
			for c := col; c < col+w && c < lineVisW; c++ {
				cm[c] = clr
			}
			col += w
			runeIdx++
			if col >= lineVisW {
				break
			}
		}
	}
	e.chromaCache[lineStr] = cm
	return cm
}

func (e *Editor) chromaTokenColor(tt chroma.TokenType) string {
	entry := e.chromaStyle.Get(tt)
	if entry.Colour.IsSet() {
		return entry.Colour.String()
	}
	return ""
}

func buildChromaStyle(th *theme.Theme) *chroma.Style {
	hex := func(c color.Color) string {
		if c == nil {
			return ""
		}
		r, g, b, _ := c.RGBA()
		return fmt.Sprintf("#%02x%02x%02x", r>>8, g>>8, b>>8)
	}

	b := chroma.NewStyleBuilder("editor")
	cc := th.Colors.Code
	co := th.Colors
	add := func(tt chroma.TokenType, c color.Color) {
		if h := hex(c); h != "" {
			b.Add(tt, h)
		}
	}

	// Core language tokens
	add(chroma.Keyword, cc.Keyword)
	add(chroma.KeywordType, cc.Keyword)
	add(chroma.KeywordDeclaration, cc.Keyword)
	add(chroma.KeywordNamespace, cc.Keyword)
	add(chroma.KeywordPseudo, cc.Keyword)
	add(chroma.KeywordReserved, cc.Keyword)
	add(chroma.KeywordConstant, cc.Keyword)

	add(chroma.NameFunction, cc.Function)
	add(chroma.NameFunctionMagic, cc.Function)
	add(chroma.NameDecorator, cc.Function)
	add(chroma.NameClass, cc.Function)
	add(chroma.NameNamespace, cc.Function)
	add(chroma.NameException, cc.Keyword)
	add(chroma.NameBuiltin, cc.Type)
	add(chroma.NameBuiltinPseudo, cc.Type)
	add(chroma.NameVariable, cc.Property)
	add(chroma.NameVariableMagic, cc.Property)
	add(chroma.NameLabel, cc.Property)
	add(chroma.NameProperty, cc.Property)
	add(chroma.NameConstant, cc.Number)
	add(chroma.NameOther, co.Fg)
	add(chroma.NameEntity, cc.Property)
	add(chroma.NameTag, cc.Keyword)
	add(chroma.NameAttribute, cc.Function)

	add(chroma.String, cc.String)
	add(chroma.StringDouble, cc.String)
	add(chroma.StringSingle, cc.String)
	add(chroma.StringBacktick, cc.String)
	add(chroma.LiteralStringBacktick, cc.String)
	add(chroma.StringDelimiter, cc.String)
	add(chroma.StringInterpol, cc.String)
	add(chroma.StringOther, cc.String)

	add(chroma.Number, cc.Number)
	add(chroma.NumberInteger, cc.Number)
	add(chroma.NumberFloat, cc.Number)
	add(chroma.NumberHex, cc.Number)
	add(chroma.NumberOct, cc.Number)
	add(chroma.LiteralDate, cc.Number)

	add(chroma.Comment, cc.Comment)
	add(chroma.CommentSingle, cc.Comment)
	add(chroma.CommentMultiline, cc.Comment)
	add(chroma.CommentSpecial, cc.Comment)
	add(chroma.CommentPreproc, cc.Comment)
	add(chroma.CommentHashbang, cc.Comment)

	add(chroma.Operator, cc.Operator)
	add(chroma.Punctuation, co.Muted)

	// Markdown-specific tokens
	add(chroma.GenericHeading, co.Accent)
	add(chroma.GenericSubheading, co.Accent2)
	add(chroma.GenericEmph, cc.Function)
	add(chroma.GenericStrong, co.Accent)
	add(chroma.GenericDeleted, co.Error)
	add(chroma.GenericInserted, co.Success)
	add(chroma.GenericTraceback, co.Error)
	add(chroma.GenericPrompt, co.Muted)
	add(chroma.GenericOutput, co.Muted)
	add(chroma.GenericError, co.Error)

	s, _ := b.Build()
	return s
}
