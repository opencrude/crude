package fileio

import (
	"crude/textsearch"
	"fmt"
	"os"
	"sort"
	"strings"
)

// ProjectFileReplacement is one file's planned content after project replace.
type ProjectFileReplacement struct {
	Path         string
	RelativePath string
	OldContent   string
	NewContent   string
	Count        int
}

// ReplaceableMatch reports whether a search hit is a contiguous content span
// that project replace can rewrite. Filename-only and fuzzy subsequence hits
// are skipped so replace cannot scatter characters across a line.
func ReplaceableMatch(m ProjectSearchMatch) bool {
	if m.NameOnly || m.FuzzyOnly || m.Line < 1 || (m.EndLine > 0 && m.EndLine < m.Line) || m.Column < 0 || m.EndColumn < 0 || ((m.EndLine == 0 || m.EndLine == m.Line) && m.EndColumn < m.Column) {
		return false
	}
	if len(m.TextMatches) >= 2 {
		for i := 1; i < len(m.TextMatches); i++ {
			if m.TextMatches[i] != m.TextMatches[i-1]+1 {
				return false
			}
		}
	}
	return true
}

// PlanProjectReplace reads each unique file in matches and builds replacement
// contents. It does not write disk. Skipped is the number of hits that were
// not contiguous content matches.
func PlanProjectReplace(matches []ProjectSearchMatch, query, replacement string, opts ProjectSearchOptions) ([]ProjectFileReplacement, int, error) {
	return PlanProjectReplaceWithReader(matches, query, replacement, opts, os.ReadFile)
}

// PlanProjectReplaceWithReader shares one exact planning contract with the core's
// canonical live documents. The caller supplies an immutable bounded read; a
// stale match never edits a different range just because its coordinates fit.
func PlanProjectReplaceWithReader(matches []ProjectSearchMatch, query, replacement string, opts ProjectSearchOptions, read func(string) ([]byte, error)) ([]ProjectFileReplacement, int, error) {
	if query == "" {
		return nil, 0, nil
	}
	matcher, err := textsearch.Compile(query, textsearch.Options{CaseSensitive: opts.CaseSensitive, WholeWord: opts.WholeWord, Regex: opts.Regex})
	if err != nil {
		return nil, 0, fmt.Errorf("invalid query: %w", err)
	}

	type fileHits struct {
		path, rel string
		hits      []ProjectSearchMatch
	}
	order := make([]string, 0)
	byPath := make(map[string]*fileHits)
	skipped := 0
	for _, m := range matches {
		if !ReplaceableMatch(m) {
			skipped++
			continue
		}
		g, ok := byPath[m.Path]
		if !ok {
			g = &fileHits{path: m.Path, rel: m.RelativePath}
			byPath[m.Path] = g
			order = append(order, m.Path)
		}
		g.hits = append(g.hits, m)
	}

	out := make([]ProjectFileReplacement, 0, len(order))
	total := 0
	for _, path := range order {
		g := byPath[path]
		data, err := read(path)
		if err != nil {
			return out, skipped, fmt.Errorf("read %s: %w", g.rel, err)
		}
		old := string(data)
		total += len(data)
		if total > 16<<20 {
			return nil, skipped, fmt.Errorf("replacement review exceeds 16 MiB; narrow the search")
		}
		next, n, err := applyReplacements(old, g.hits, replacement, matcher)
		if err != nil {
			return out, skipped, err
		}
		if n == 0 || next == old {
			continue
		}
		out = append(out, ProjectFileReplacement{
			Path:         path,
			RelativePath: g.rel,
			OldContent:   old,
			NewContent:   next,
			Count:        n,
		})
	}
	return out, skipped, nil
}

type byteSpan struct {
	start, end int
	match      textsearch.Match
}

func applyReplacements(content string, hits []ProjectSearchMatch, replacement string, matcher *textsearch.Matcher) (string, int, error) {
	exact := make(map[[2]int]textsearch.Match)
	for _, m := range matcher.FindAll(content, 0) {
		exact[[2]int{m.Start, m.End}] = m
	}
	seen := make(map[[2]int]bool)
	spans := make([]byteSpan, 0, len(hits))
	for _, m := range hits {
		start, end, ok := matchByteSpan(content, m)
		if !ok {
			return "", 0, fmt.Errorf("%s changed; search again before replacing", m.RelativePath)
		}
		key := [2]int{start, end}
		match, ok := exact[key]
		if !ok {
			return "", 0, fmt.Errorf("%s no longer matches the exact query", m.RelativePath)
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		spans = append(spans, byteSpan{start: start, end: end, match: match})
	}
	if len(spans) == 0 {
		return content, 0, nil
	}
	sort.Slice(spans, func(i, j int) bool {
		if spans[i].start != spans[j].start {
			return spans[i].start > spans[j].start
		}
		return spans[i].end > spans[j].end
	})
	next := content
	count := 0
	for _, sp := range spans {
		if sp.start < 0 || sp.end > len(next) || sp.start > sp.end {
			continue
		}
		seg := matcher.Expand(content, sp.match, replacement)
		next = next[:sp.start] + seg + next[sp.end:]
		count++
	}
	return next, count, nil
}

func matchByteSpan(content string, m ProjectSearchMatch) (int, int, bool) {
	off := 0
	line := 1
	for line < m.Line {
		i := strings.IndexByte(content[off:], '\n')
		if i < 0 {
			return 0, 0, false
		}
		off += i + 1
		line++
	}
	lineText := content[off:]
	if j := strings.IndexByte(lineText, '\n'); j >= 0 {
		lineText = lineText[:j]
	}
	if lineText != m.LineText {
		return 0, 0, false
	}
	runes := []rune(lineText)
	if m.Column < 0 || m.Column > len(runes) {
		return 0, 0, false
	}
	start := off + len(string(runes[:m.Column]))
	endLine := m.EndLine
	if endLine == 0 {
		endLine = m.Line
	}
	endOff := off
	for line < endLine {
		i := strings.IndexByte(content[endOff:], '\n')
		if i < 0 {
			return 0, 0, false
		}
		endOff += i + 1
		line++
	}
	endText := content[endOff:]
	if j := strings.IndexByte(endText, '\n'); j >= 0 {
		endText = endText[:j]
	}
	endRunes := []rune(endText)
	if m.EndColumn > len(endRunes) {
		return 0, 0, false
	}
	end := endOff + len(string(endRunes[:m.EndColumn]))
	if start < 0 || end > len(content) || start > end {
		return 0, 0, false
	}
	return start, end, true
}
