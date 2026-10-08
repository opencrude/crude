// Package diffview presents immutable, read-only text comparisons without owning
// document buffers or filesystem state.
package diffview

import (
	"crude/textdiff"
	"crude/tui/ui"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
)

type Comparison struct{ Path, BeforeLabel, AfterLabel, Before, After string }

// Row numbers are one-based source lines; zero denotes absent alignment space.
type Row struct {
	BeforeLine, AfterLine int
	Before, After         string
	Kind                  ui.SemanticColor
}
type Prepared struct {
	Comparison                Comparison
	rows, unified             []Row
	hunks, unifiedHunks       []int
	beforeWidth, afterWidth   int
	beforeSyntax, afterSyntax [][]syntaxSpan
}

type syntaxSpan struct {
	start, end int
	kind       chroma.TokenType
}

// Keep lexer context across the immutable source, but retain token identities,
// not palette bytes. Theme changes therefore need neither diffing nor lexing.
func prepareSyntax(path, source string) [][]syntaxSpan {
	l := lexers.Match(path)
	if l == nil || l.Config().Name == "fallback" {
		return nil // Plain source needs no per-character fallback token projection.
	}
	it, err := chroma.Coalesce(l).Tokenise(&chroma.TokeniseOptions{State: "root", EnsureLF: false}, source)
	if err != nil {
		return nil
	}
	lines := make([][]syntaxSpan, len(textdiff.Lines(source)))
	line, offset := 0, 0
	for token := it(); token != chroma.EOF; token = it() {
		parts := strings.SplitAfter(token.Value, "\n")
		for _, part := range parts {
			if line >= len(lines) {
				break
			}
			if part != "" {
				lines[line] = append(lines[line], syntaxSpan{offset, offset + len(part), token.Type})
			}
			offset += len(part)
			if strings.HasSuffix(part, "\n") {
				line++
				offset = 0
			}
		}
	}
	return lines
}

func (p Prepared) Rows() []Row    { return append([]Row(nil), p.rows...) }
func (p Prepared) HunkCount() int { return len(p.hunks) }

// Prepare performs diffing and width derivation. Call it off the UI goroutine.
func Prepare(c Comparison) Prepared {
	p := Prepared{Comparison: c}
	p.beforeSyntax = prepareSyntax(c.Path, c.Before)
	p.afterSyntax = prepareSyntax(c.Path, c.After)
	before, after := textdiff.Lines(c.Before), textdiff.Lines(c.After)
	for _, line := range before {
		p.beforeWidth = max(p.beforeWidth, sourceWidth(line))
	}
	for _, line := range after {
		p.afterWidth = max(p.afterWidth, sourceWidth(line))
	}
	old, next := 0, 0
	equal := func(end int) {
		for old < end {
			r := Row{BeforeLine: old + 1, AfterLine: next + 1, Before: before[old], After: after[next], Kind: ui.ColorFg}
			p.rows = append(p.rows, r)
			p.unified = append(p.unified, r)
			old++
			next++
		}
	}
	for _, h := range textdiff.Hunks(c.Before, c.After) {
		equal(h.OldStart)
		p.hunks = append(p.hunks, len(p.rows))
		p.unifiedHunks = append(p.unifiedHunks, len(p.unified))
		for i := 0; i < max(len(h.OldLines), len(h.NewLines)); i++ {
			r := Row{Kind: ui.ColorDiffHunk}
			if i < len(h.OldLines) {
				r.BeforeLine = old + i + 1
				r.Before = h.OldLines[i]
			}
			if i < len(h.NewLines) {
				r.AfterLine = next + i + 1
				r.After = h.NewLines[i]
			}
			p.rows = append(p.rows, r)
		}
		for i, line := range h.OldLines {
			p.unified = append(p.unified, Row{BeforeLine: old + i + 1, Before: line, Kind: ui.ColorDiffDeletion})
		}
		for i, line := range h.NewLines {
			p.unified = append(p.unified, Row{AfterLine: next + i + 1, After: line, Kind: ui.ColorDiffAddition})
		}
		old += len(h.OldLines)
		next += len(h.NewLines)
	}
	equal(len(before))
	return p
}
