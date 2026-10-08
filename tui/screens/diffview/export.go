package diffview

import (
	"github.com/alecthomas/chroma/v2"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/ui"
)

// SyntaxSpan is one lexer token range on a source line, in line-local bytes.
type SyntaxSpan struct {
	Start, End int
	Kind       chroma.TokenType
}

// PrepareSyntax lexes a whole source once (keeping multi-line lexer context)
// and returns per-line token spans. Call it off the UI goroutine. Nil means
// the path has no specific lexer and paints as plain text.
func PrepareSyntax(path, source string) [][]SyntaxSpan {
	lines := prepareSyntax(path, source)
	if lines == nil {
		return nil
	}
	out := make([][]SyntaxSpan, len(lines))
	for index, spans := range lines {
		if len(spans) == 0 {
			continue
		}
		converted := make([]SyntaxSpan, len(spans))
		for position, span := range spans {
			converted[position] = SyntaxSpan{Start: span.start, End: span.end, Kind: span.kind}
		}
		out[index] = converted
	}
	return out
}

// SourceUnit is one painted grapheme: its source bytes, display text (tabs
// expanded, controls replaced), and its byte offset and display columns.
type SourceUnit struct {
	Raw, Display          string
	Offset, Column, Width int
}

// SourceUnits is the shared byte/column mapping every comparison painter uses.
func SourceUnits(text string) []SourceUnit {
	units := sourceUnits(text)
	out := make([]SourceUnit, len(units))
	for index, unit := range units {
		out[index] = SourceUnit{Raw: unit.raw, Display: unit.display, Offset: unit.offset, Column: unit.column, Width: unit.width}
	}
	return out
}

// SourceWidth is the display width of one source line.
func SourceWidth(text string) int { return sourceWidth(text) }

// SyntaxInk resolves token kinds to theme ink, caching per theme version so
// theme changes never require re-lexing prepared sources.
type SyntaxInk struct {
	style   *chroma.Style
	ink     map[chroma.TokenType]cell.Color
	tokens  *ui.TokenSet
	version uint64
}

// Ink returns the token color and whether the style defines one.
func (palette *SyntaxInk) Ink(tokens *ui.TokenSet, kind chroma.TokenType) (cell.Color, bool) {
	if tokens == nil {
		return cell.NoColor, false
	}
	if palette.style == nil || palette.tokens == nil || palette.tokens.Theme() != tokens.Theme() || palette.version != tokens.ColorVersion {
		palette.style = rendering.SourceStyle(tokens.Theme())
		palette.ink = make(map[chroma.TokenType]cell.Color)
		palette.tokens, palette.version = tokens, tokens.ColorVersion
	}
	if ink, ok := palette.ink[kind]; ok {
		return ink, !ink.IsDefault()
	}
	ink := cell.NoColor
	if entry := palette.style.Get(kind); entry.Colour.IsSet() {
		ink = rendering.LipglossColorToCell(entry.Colour.String())
	}
	palette.ink[kind] = ink
	return ink, !ink.IsDefault()
}
