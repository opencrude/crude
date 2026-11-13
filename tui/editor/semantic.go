package editor

import (
	"errors"
	"slices"
	"sort"

	"crude/document"
	"crude/types"
	"github.com/alecthomas/chroma/v2"
)

// semanticToken is one provider span resolved to the source palette. Tokens
// hold resolved kinds, so partial results with their own legends can merge.
type semanticToken struct {
	start, end int // source bytes, end exclusive
	kind       chroma.TokenType
}

// semanticStore is a port of VS Code's SparseTokensStore and
// SparseMultilineTokensStorage (src/vs/editor/common/tokens). Provider colors
// are adjusted through every source edit and stay until a newer result
// replaces them; an edit or a scroll never drops them. Coordinates are source
// bytes: LSP tokens never span lines, so VS Code's per-line character
// arithmetic reduces to byte offsets.
type semanticStore struct {
	buffer   *PieceTable
	revision uint64 // buffer revision the tokens describe
	tokens   []semanticToken
}

// acceptEdit replaces [start, end) with text. It ports acceptDeleteRange
// followed by acceptInsertText at the same position.
func (s *semanticStore) acceptEdit(start, end int, text string) {
	if end > start {
		s.acceptDelete(start, end)
	}
	if text != "" {
		s.acceptInsert(start, text)
	}
}

func (s *semanticStore) acceptDelete(start, end int) {
	kept := s.tokens[:0]
	for _, t := range s.tokens {
		switch {
		case t.end <= start:
			// 1a. Completely before the deletion.
		case t.start < start:
			if t.end > end {
				t.end -= end - start // 1d. The deletion is inside the token.
			} else {
				t.end = start // 1b, 1c. The token shrinks to the deletion start.
			}
		case t.start == start:
			if t.end <= end {
				continue // 2a, 2b. The token is deleted.
			}
			t.end -= end - start // 2c.
		case t.start < end:
			if t.end <= end {
				continue // 3a, 3b. The token is inside the deletion.
			}
			t.start, t.end = start, start+t.end-end // 3c. It continues after the deletion.
		default:
			t.start, t.end = t.start-(end-start), t.end-(end-start) // 4. After the deletion.
		}
		kept = append(kept, t)
	}
	s.tokens = kept
}

func (s *semanticStore) acceptInsert(at int, text string) {
	// VS Code grows a touching token only for one ASCII word character.
	oneWordCharacter := len(text) == 1 && (text[0] >= '0' && text[0] <= '9' || text[0] >= 'A' && text[0] <= 'Z' || text[0] >= 'a' && text[0] <= 'z')
	newline := false
	for i := 0; i < len(text) && !newline; i++ {
		newline = text[i] == '\n' || text[i] == '\r'
	}
	for i := range s.tokens {
		t := &s.tokens[i]
		switch {
		case t.end < at:
			// 1. Completely before the insertion point.
		case t.end == at:
			// 2. Ends precisely at the insertion point.
			if oneWordCharacter {
				t.end++
			}
		case t.start < at:
			// 3. Contains the insertion point.
			if newline {
				t.end = at
			} else {
				t.end += len(text)
			}
		case t.start == at && oneWordCharacter:
			// 4. Starts precisely at the insertion point: keep its start.
		default:
			// 4, 5. Move and keep its size.
			t.start, t.end = t.start+len(text), t.end+len(text)
		}
	}
}

// setPartial ports SparseTokensStore.setPartial: tokens touching the covered
// range are replaced and every other token stays.
func (s *semanticStore) setPartial(start, end int, tokens []semanticToken) {
	if len(tokens) > 0 {
		start, end = min(start, tokens[0].start), max(end, tokens[len(tokens)-1].end)
	}
	first := sort.Search(len(s.tokens), func(i int) bool { return s.tokens[i].end >= start })
	last := first
	for last < len(s.tokens) && s.tokens[last].start <= end {
		last++
	}
	s.tokens = slices.Replace(s.tokens, first, last, tokens...)
}

// SetSemanticTokens transfers an immutable, source-qualified result to this
// private view. The core validated every span off Update; the shell resolves
// it into its own store and never mutates the result. A range result merges
// with colors outside its range. No source, caret, diagnostics or history changes.
func (e *Editor) SetSemanticTokens(set *types.LSPSemanticSet) error {
	if set == nil || set.ViewID == "" || set.Sequence == 0 || !e.MatchesInsightSource(set.Boundary.Source) {
		return errors.New("semantic colors do not match current source")
	}
	tokens := make([]semanticToken, 0, len(set.Spans))
	for _, span := range set.Spans {
		if span.Type < 0 || span.Type >= len(set.Types) || span.End <= span.Start {
			continue
		}
		if kind, ok := semanticKind(set.Types[span.Type]); ok {
			tokens = append(tokens, semanticToken{start: span.Start, end: span.End, kind: kind})
		}
	}
	store := &e.semantic
	if set.Full || !e.semanticCurrent() {
		store.tokens = tokens
	} else {
		start, end := e.semanticRangeBytes(set.Range)
		store.setPartial(start, end, tokens)
	}
	store.buffer, store.revision = e.buf, e.Revision()
	e.recolorSyntax()
	return nil
}

// semanticRangeBytes covers the complete requested lines; Crude requests whole
// lines, so a partial first line only widens the replaced range.
func (e *Editor) semanticRangeBytes(r types.LSPSemanticRange) (int, int) {
	e.buf.ensureCache()
	lines := len(e.buf.lineOffsets)
	first, last := min(max(0, r.Start.Line), lines-1), min(max(0, r.End.Line), lines-1)
	end := len(e.buf.cachedBytes)
	if last+1 < lines {
		end = e.buf.lineOffsets[last+1]
	}
	return e.buf.lineOffsets[first], end
}

// ClearSemanticTokens removes provider colors when their provider or source
// identity is gone. A pending edit, scroll or newer request is not a reason.
func (e *Editor) ClearSemanticTokens() {
	if e.semantic.buffer == nil {
		return
	}
	e.semantic = semanticStore{}
	e.recolorSyntax()
}

// semanticCurrent reports whether the store describes the current text. Edits
// adjust it synchronously; only a whole-source replacement leaves it behind.
func (e *Editor) semanticCurrent() bool {
	return e.semantic.buffer != nil && e.semantic.buffer == e.buf && e.semantic.revision == e.Revision()
}

// acceptSemanticEdits adjusts every view's provider colors through one exact
// simultaneous edit vector, last edit first so earlier offsets stay valid.
func (e *Editor) acceptSemanticEdits(before uint64, edits []document.Edit) {
	e.eachView(func(v *Editor) {
		s := &v.semantic
		if s.buffer != v.buf || s.revision != before {
			return
		}
		for i := len(edits) - 1; i >= 0; i-- {
			s.acceptEdit(edits[i].Start, edits[i].End, edits[i].Text)
		}
		s.revision = v.Revision()
	})
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
	if !e.semanticCurrent() || len(e.semantic.tokens) == 0 {
		return lexical
	}
	tokens := e.semantic.tokens
	index := sort.Search(len(tokens), func(i int) bool { return tokens[i].end > start })
	return func(offset int) chroma.TokenType {
		for index < len(tokens) && tokens[index].end <= offset {
			index++
		}
		if index < len(tokens) && tokens[index].start <= offset {
			return tokens[index].kind
		}
		return lexical(offset)
	}
}

// ensureSemanticFallback paints semantic spans even when a file has no local
// grammar. Missing token kinds still use the ordinary plain-text fallback.
func (e *Editor) ensureSemanticFallback(from, to int) {
	if !e.semanticCurrent() || len(e.semantic.tokens) == 0 {
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
