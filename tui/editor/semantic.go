package editor

import (
	"errors"
	"sort"

	"crude/types"
	"github.com/alecthomas/chroma/v2"
)

// SetSemanticTokens transfers an immutable, source-qualified result to this
// private view. The core validated every span off Update; the shell must not
// mutate the result afterwards. No source, caret, diagnostics or history changes.
func (e *Editor) SetSemanticTokens(set *types.LSPSemanticSet) error {
	if set == nil || set.ViewID == "" || set.Sequence == 0 || !e.MatchesInsightSource(set.Boundary.Source) {
		return errors.New("semantic colors do not match current source")
	}
	e.semantic = set
	clear(e.chromaLineCache)
	clear(e.chromaLineScratch)
	return nil
}
func (e *Editor) ClearSemanticTokens() {
	if e.semantic == nil {
		return
	}
	e.semantic = nil
	clear(e.chromaLineCache)
	clear(e.chromaLineScratch)
}
func (e *Editor) semanticCurrent() bool {
	return e.semantic != nil && e.MatchesInsightSource(e.semantic.Boundary.Source)
}

// semanticKind maps standard protocol classifications to the existing source
// palette. Unknown types deliberately retain lexical ink instead of inventing a
// language-specific rule or replacing text with a default color.
func semanticKind(name string) (chroma.TokenType, bool) {
	switch name {
	case "namespace":
		return chroma.NameNamespace, true
	case "type", "class", "enum", "interface", "struct", "typeParameter":
		return chroma.NameBuiltin, true
	case "parameter", "variable", "property", "enumMember", "event":
		return chroma.NameVariable, true
	case "function", "method", "macro", "decorator":
		return chroma.NameFunction, true
	case "keyword", "modifier":
		return chroma.Keyword, true
	case "comment":
		return chroma.Comment, true
	case "string", "regexp":
		return chroma.String, true
	case "number":
		return chroma.Number, true
	case "operator":
		return chroma.Operator, true
	default:
		return chroma.Text, false
	}
}

// semanticLineToken returns a monotonic lookup closure for one visible line.
// Its bytes share the same grapheme/tab projection as lexical source colors.
func (e *Editor) semanticLineToken(start int, lexical func(int) chroma.TokenType) func(int) chroma.TokenType {
	if !e.semanticCurrent() || len(e.semantic.Spans) == 0 {
		return lexical
	}
	set := e.semantic
	index := sort.Search(len(set.Spans), func(i int) bool { return set.Spans[i].End > start })
	return func(offset int) chroma.TokenType {
		for index < len(set.Spans) && set.Spans[index].End <= offset {
			index++
		}
		if index < len(set.Spans) {
			span := set.Spans[index]
			if span.Start <= offset && offset < span.End && span.Type >= 0 && span.Type < len(set.Types) {
				if kind, ok := semanticKind(set.Types[span.Type]); ok {
					return kind
				}
			}
		}
		return lexical(offset)
	}
}

// ensureSemanticFallback paints semantic spans even when a file has no local
// grammar. Missing token kinds still use the ordinary plain-text fallback.
func (e *Editor) ensureSemanticFallback(from, to int) {
	if !e.semanticCurrent() || len(e.semantic.Spans) == 0 {
		return
	}
	e.buf.ensureCache()
	from, to = max(0, from), min(len(e.buf.lineOffsets), to)
	if e.chromaLineCache == nil {
		e.chromaLineCache = make(map[int]syntaxLine)
	}
	for line := from; line < to; line++ {
		if e.chromaLineCache[line].colours != nil {
			continue
		}
		raw, err := e.buf.lineView(line)
		if err != nil {
			continue
		}
		start := e.buf.lineOffsets[line]
		e.chromaLineCache[line] = e.projectSyntaxLine(string(raw), start, func(int) chroma.TokenType { return chroma.Text })
	}
	e.syntaxBuffer, e.syntaxRevision = e.buf, e.buf.Revision()
}
