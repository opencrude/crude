package rendering

import (
	"crude/syntax"

	"github.com/alecthomas/chroma/v2"
)

// SyntaxToken uses the shared source palette without making native analysis
// depend on Chroma, theme generations or terminal rendering.
func SyntaxToken(kind syntax.Kind) chroma.TokenType {
	switch kind {
	case syntax.Keyword:
		return chroma.Keyword
	case syntax.Type, syntax.Builtin:
		return chroma.NameBuiltin
	case syntax.Function:
		return chroma.NameFunction
	case syntax.Property, syntax.Variable:
		return chroma.NameProperty
	case syntax.String:
		return chroma.String
	case syntax.Number, syntax.Constant:
		return chroma.Number
	case syntax.Comment:
		return chroma.Comment
	case syntax.Operator:
		return chroma.Operator
	case syntax.Punctuation:
		return chroma.Punctuation
	case syntax.Tag:
		return chroma.NameTag
	case syntax.Attribute:
		return chroma.NameAttribute
	default:
		return chroma.Text
	}
}
