package editor

import (
	"crude/syntax"
	"crude/types"
	"github.com/alecthomas/chroma/v2"
	"sort"
)

// Structural editing uses only an exact, completed source result. Mapped ink is
// useful while typing but is not authority to move or rewrite source ranges.
func (e *Editor) structuralSource() (*syntax.Result, *syntaxDocument) {
	e.finishSyntax()
	if e.canonicalSyntaxSource() {
		view, p := e.canonicalInsight, e.documentProjection
		if view != nil && view.State == types.InsightCurrent && view.Syntax != nil && view.Path == e.filePath && view.Revision == p.snapshot.Revision && view.Syntax.Revision == view.Revision {
			return view.Syntax, nil
		}
	}
	if e.syntaxDocument != nil && e.syntaxBuffer == e.buf && e.syntaxRevision == e.Revision() {
		return nil, e.syntaxDocument
	}
	return nil, nil
}

func structuralExcluded(kind chroma.TokenType) bool {
	return kind.InCategory(chroma.Comment) || kind.InSubCategory(chroma.String)
}

func (e *Editor) structuralRanges() ([]syntax.Range, []syntax.Bracket) {
	if native, lexical := e.structuralSource(); native != nil {
		return native.Selections, native.Brackets
	} else if lexical != nil {
		return lexical.selections, lexical.brackets
	}
	return nil, nil
}

func (e *Editor) matchingBracket() (from, to Position, ok bool) {
	_, brackets := e.structuralRanges()
	if len(brackets) == 0 {
		return Position{}, Position{}, false
	}
	offset := e.buf.positionToOffset(e.cursor)
	for _, candidate := range []int{offset, offset - 1} {
		if candidate < 0 {
			continue
		}
		i := sort.Search(len(brackets), func(i int) bool { return brackets[i].Offset >= uint32(candidate) })
		if i < len(brackets) && brackets[i].Offset == uint32(candidate) {
			pair := brackets[i]
			from, to = e.buf.positionAtOffset(int(pair.Offset)), e.buf.positionAtOffset(int(pair.Mate))
			// A protocol result can never manufacture an interior grapheme boundary.
			if e.buf.positionToOffset(from) == int(pair.Offset) && e.buf.positionToOffset(to) == int(pair.Mate) {
				return from, to, true
			}
		}
	}
	return Position{}, Position{}, false
}

func (e *Editor) moveToMatchingBracket(extend bool) {
	_, to, ok := e.matchingBracket()
	if !ok {
		return
	}
	e.actionMotion(extend, func() { e.cursor = to })
}
