package findinfiles

import (
	"context"
	"strings"

	"crude/tui/editor"
	"crude/workspace/fileio"
)

// FilePreview holds one complete source and the results that adorn it.
type FilePreview struct {
	document *editor.PreviewDocument
	hits     map[int]int
	matches  []editor.SearchMatch
}

// PrepareFilePreview indexes search ranges on the file worker. The result slice
// belongs to this query and is never modified by a selection or a new search.
func PrepareFilePreview(ctx context.Context, ed *editor.PreviewDocument, path string, results []fileio.ProjectSearchMatch) (*FilePreview, error) {
	out := &FilePreview{document: ed, hits: map[int]int{}}
	for i, r := range results {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		line := r.Line - 1
		if r.Path != path || r.NameOnly || line < 0 || line >= ed.LineCount() {
			continue
		}
		raw := ed.LineText(line)
		from, to, valid := r.SourceColumns(raw)
		if !valid || r.FuzzyOnly && raw != strings.TrimSuffix(r.LineText, "\r") {
			continue
		}
		column := func(text string, n int) int { return editor.RuneIndexToVisualColumn(ed.TabWidth(), text, n) }
		out.hits[i] = len(out.matches)
		if r.FuzzyOnly {
			if len(r.TextMatches) == 0 {
				delete(out.hits, i)
			}
			for _, n := range r.TextMatches {
				sc, ec := column(raw, n), column(raw, n+1)
				out.matches = append(out.matches, editor.SearchMatch{Start: editor.Position{Line: line, Col: sc}, End: editor.Position{Line: line, Col: max(sc+1, ec)}})
			}
			continue
		}
		endLine := max(line, r.EndLine-1)
		if endLine >= ed.LineCount() {
			delete(out.hits, i)
			continue
		}
		sc, ec := column(raw, from), 0
		if endLine == line {
			ec = column(raw, to)
		} else {
			ec = column(ed.LineText(endLine), r.EndColumn)
		}
		if line == endLine {
			ec = max(ec, sc+1)
		}
		out.matches = append(out.matches, editor.SearchMatch{Start: editor.Position{Line: line, Col: sc}, End: editor.Position{Line: endLine, Col: ec}})
	}
	return out, nil
}
