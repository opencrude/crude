package rendering

import (
	"charm.land/glamour/v2"
	"charm.land/glamour/v2/ansi"
	"charm.land/glamour/v2/styles"
)

func MakeMdRenderer(width int) *glamour.TermRenderer {
	w := width - 4 - 16
	if w < 20 {
		w = 20
	}
	style := styles.DarkStyleConfig
	style.Document.Margin = uintPtr(0)
	style.CodeBlock.Margin = uintPtr(0)
	style.CodeBlock.Chroma = &ansi.Chroma{
		Text:                ansi.StylePrimitive{Color: strPtr("#e6edf3")},
		Error:               ansi.StylePrimitive{Color: strPtr("#ff7b72")},
		Comment:             ansi.StylePrimitive{Color: strPtr("#8b949e")},
		CommentPreproc:      ansi.StylePrimitive{Color: strPtr("#79c0ff")},
		Keyword:             ansi.StylePrimitive{Color: strPtr("#ff7b72")},
		KeywordReserved:     ansi.StylePrimitive{Color: strPtr("#ff7b72")},
		KeywordNamespace:    ansi.StylePrimitive{Color: strPtr("#ff7b72")},
		KeywordType:         ansi.StylePrimitive{Color: strPtr("#79c0ff")},
		Operator:            ansi.StylePrimitive{Color: strPtr("#ff7b72")},
		Punctuation:         ansi.StylePrimitive{Color: strPtr("#e6edf3")},
		Name:                ansi.StylePrimitive{Color: strPtr("#e6edf3")},
		NameBuiltin:         ansi.StylePrimitive{Color: strPtr("#79c0ff")},
		NameTag:             ansi.StylePrimitive{Color: strPtr("#7ee787")},
		NameAttribute:       ansi.StylePrimitive{Color: strPtr("#79c0ff")},
		NameClass:           ansi.StylePrimitive{Color: strPtr("#79c0ff")},
		NameConstant:        ansi.StylePrimitive{Color: strPtr("#79c0ff")},
		NameDecorator:       ansi.StylePrimitive{Color: strPtr("#d2a8ff")},
		NameFunction:        ansi.StylePrimitive{Color: strPtr("#d2a8ff")},
		LiteralNumber:       ansi.StylePrimitive{Color: strPtr("#79c0ff")},
		LiteralString:       ansi.StylePrimitive{Color: strPtr("#a5d6ff")},
		LiteralStringEscape: ansi.StylePrimitive{Color: strPtr("#79c0ff")},
		GenericDeleted:      ansi.StylePrimitive{Color: strPtr("#ff7b72")},
		GenericInserted:     ansi.StylePrimitive{Color: strPtr("#7ee787")},
		GenericStrong:       ansi.StylePrimitive{Color: strPtr("#e6edf3")},
		GenericSubheading:   ansi.StylePrimitive{Color: strPtr("#e6edf3")},
	}
	style.CodeBlock.StylePrimitive.Color = strPtr("#e6edf3")
	style.CodeBlock.StylePrimitive.BackgroundColor = strPtr("#161b22")
	style.CodeBlock.BlockPrefix = ""
	style.Paragraph.Margin = uintPtr(0)
	style.Heading.Margin = uintPtr(0)
	style.List.Margin = uintPtr(0)
	style.Table.Margin = uintPtr(0)

	r, err := glamour.NewTermRenderer(
		glamour.WithStyles(style),
		glamour.WithWordWrap(w),
	)
	if err == nil {
		return r
	}
	return nil
}

func uintPtr(u uint) *uint { return &u }
func strPtr(s string) *string { return &s }
