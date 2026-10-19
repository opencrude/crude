// Package diffview presents immutable, read-only text comparisons without owning
// document buffers or filesystem state.
package diffview

import (
	"context"
	"crude/textdiff"
	"crude/tui/ui"
	"fmt"
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
	beforeSource, afterSource SourceLines
}

type syntaxSpan struct {
	start, end int
	kind       chroma.TokenType
}

// Keep lexer context across the immutable source, but retain token identities,
// not palette bytes. Theme changes therefore need neither diffing nor lexing.
const SyntaxByteLimit = 256 << 10

const ComparisonLineLimit = 250_000

// Bound derived row memory before SplitAfter allocates source-sized slices.
// Oversized evidence remains owned by the caller; it is never silently clipped.
func ValidateComparison(before, after string) error {
	for _, source := range []string{before, after} {
		lines := strings.Count(source, "\n")
		if source != "" && !strings.HasSuffix(source, "\n") {
			lines++
		}
		if lines > ComparisonLineLimit {
			return fmt.Errorf("comparison exceeds the %d-line per-version display limit", ComparisonLineLimit)
		}
	}
	return nil
}

// Preparation workers are shared by Git, commit and local comparisons. A
// cancelled queued request never admits source-sized work.
var preparationWorkers = make(chan struct{}, 2)

func AcquirePreparation(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case preparationWorkers <- struct{}{}:
		return func() { <-preparationWorkers }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func prepareSyntax(ctx context.Context, path, source string) ([][]syntaxSpan, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(source) > SyntaxByteLimit {
		return nil, nil
	}
	l := lexers.Match(path)
	if l == nil || l.Config().Name == "fallback" {
		return nil, nil // Plain source needs no per-character fallback token projection.
	}
	it, err := l.Tokenise(&chroma.TokeniseOptions{State: "root", EnsureLF: false}, source)
	if err != nil {
		return nil, err
	}
	lines := make([][]syntaxSpan, len(textdiff.Lines(source)))
	line, offset := 0, 0
	for token := it(); token != chroma.EOF; token = it() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
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
	return lines, ctx.Err()
}

func (p Prepared) Rows() []Row    { return append([]Row(nil), p.rows...) }
func (p Prepared) HunkCount() int { return len(p.hunks) }

// Prepare performs diffing and width derivation. Call it off the UI goroutine.
func Prepare(c Comparison) Prepared {
	p, _ := PrepareContext(context.Background(), c)
	return p
}

func PrepareContext(ctx context.Context, c Comparison) (p Prepared, err error) {
	release, err := AcquirePreparation(ctx)
	if err != nil {
		return Prepared{}, err
	}
	defer release()
	defer func() {
		if recovered := recover(); recovered != nil {
			p, err = Prepared{}, fmt.Errorf("comparison preparation failed: %v", recovered)
		}
	}()
	p = Prepared{Comparison: c}
	if err := ValidateComparison(c.Before, c.After); err != nil {
		return Prepared{}, err
	}
	if p.beforeSyntax, err = prepareSyntax(ctx, c.Path, c.Before); err != nil {
		return Prepared{}, err
	}
	if p.afterSyntax, err = prepareSyntax(ctx, c.Path, c.After); err != nil {
		return Prepared{}, err
	}
	before, after := textdiff.Lines(c.Before), textdiff.Lines(c.After)
	if p.beforeSource, err = PrepareSourceLinesContext(ctx, before); err != nil {
		return Prepared{}, err
	}
	if p.afterSource, err = PrepareSourceLinesContext(ctx, after); err != nil {
		return Prepared{}, err
	}
	p.beforeWidth, p.afterWidth = p.beforeSource.Width(), p.afterSource.Width()
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
	for _, h := range textdiff.ReviewHunks(c.Before, c.After) {
		if err := ctx.Err(); err != nil {
			return Prepared{}, err
		}
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
	return p, ctx.Err()
}
