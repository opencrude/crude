package rendering

import (
	"crude/tui/theme"
	"fmt"
	"github.com/alecthomas/chroma/v2"
	"image/color"
)

// SourceStyle is the shared editor/comparison syntax palette. Lexing remains
// source-owned; all token subtypes resolve through this theme-derived style.
func SourceStyle(th *theme.Theme) *chroma.Style {
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
	add(chroma.LiteralString, cc.String)

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
