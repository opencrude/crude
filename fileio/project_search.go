package fileio

import (
	"bytes"
	"context"
	"crude/textsearch"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const (
	maxProjectSearchResults  = 1000
	maxProjectSearchFileSize = 4 << 20
	projectSearchContext     = 6
)

// ProjectSearchOptions controls recursive filename and content matching.
type ProjectSearchOptions struct {
	CaseSensitive bool
	Regex         bool
	WholeWord     bool
	IncludeHidden bool
}

// ProjectSearchMatch is one selectable Find in Files result.
type ProjectSearchMatch struct {
	Path         string
	RelativePath string
	Line         int // 1-based; zero means filename-only
	EndLine      int // 1-based; zero denotes the same line
	Column       int // rune index, zero-based
	EndColumn    int // rune index, exclusive
	LineText     string
	NameOnly     bool
	NameMatched  bool
	FuzzyOnly    bool  // discovery suggestion, never a replacement target
	PathMatches  []int // rune positions in RelativePath
	TextMatches  []int // rune positions in LineText
	ContextStart int   // 1-based first line in Context
	Context      string
	Score        int
}

// SearchProject searches literal/regex content and fuzzy relative filenames.
// Every exact source occurrence is a distinct result. The walk is
// cancellation-aware and skips generated/dependency directories consistently
// with GlobFiles and GrepFiles.
func SearchProject(ctx context.Context, root, query string, opts ProjectSearchOptions) ([]ProjectSearchMatch, error) {
	if query == "" {
		return nil, nil
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}

	var re *regexp.Regexp
	matcher, err := textsearch.Compile(query, textsearch.Options{CaseSensitive: opts.CaseSensitive, WholeWord: opts.WholeWord, Regex: opts.Regex})
	if err != nil {
		return nil, fmt.Errorf("invalid query: %w", err)
	}
	if opts.Regex {
		pattern := query
		if !opts.CaseSensitive {
			pattern = "(?i)" + pattern
		}
		re, err = regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("invalid regex: %w", err)
		}
	}

	results := make([]ProjectSearchMatch, 0, 128)
	suggestions := make([]ProjectSearchMatch, 0, 64)
	err = filepath.WalkDir(absRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if entry.IsDir() {
			if path != absRoot && shouldSkipDir(entry.Name(), opts.IncludeHidden) {
				return fs.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		if !opts.IncludeHidden && strings.HasPrefix(entry.Name(), ".") {
			return nil
		}

		rel, relErr := filepath.Rel(absRoot, path)
		if relErr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		pathPositions, pathScore, nameMatched := projectSearchMatch(rel, query, opts.CaseSensitive, re)

		info, statErr := entry.Info()
		if statErr != nil || info.Size() > maxProjectSearchFileSize {
			if nameMatched && len(suggestions) < maxProjectSearchResults {
				suggestions = append(suggestions, filenameProjectSearchMatch(path, rel, pathPositions, pathScore))
			}
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil || bytes.IndexByte(data[:min(len(data), 512)], 0) >= 0 {
			if nameMatched && len(suggestions) < maxProjectSearchResults {
				suggestions = append(suggestions, filenameProjectSearchMatch(path, rel, pathPositions, pathScore))
			}
			return nil
		}

		text := string(data)
		lines := strings.Split(text, "\n")
		starts := make([]int, len(lines))
		for i := 1; i < len(lines); i++ {
			starts[i] = starts[i-1] + len(lines[i-1]) + 1
		}
		contentMatches := 0
		exactLines := make(map[int]bool)
		for _, hit := range matcher.FindAll(text, maxProjectSearchResults-len(results)) {
			if len(results) >= maxProjectSearchResults {
				return fs.SkipAll
			}
			if contentMatches%128 == 0 {
				select {
				case <-ctx.Done():
					return ctx.Err()
				default:
				}
			}
			lineIdx := max(0, sort.Search(len(starts), func(i int) bool { return starts[i] > hit.Start })-1)
			endLine := max(0, sort.Search(len(starts), func(i int) bool { return starts[i] > hit.End })-1)
			line := lines[lineIdx]
			column := len([]rune(text[starts[lineIdx]:hit.Start]))
			endColumn := len([]rune(text[starts[endLine]:hit.End]))
			positions := make([]int, 0)
			firstEnd := min(hit.End, starts[lineIdx]+len(line))
			for i := column; i < column+len([]rune(text[hit.Start:firstEnd])); i++ {
				positions = append(positions, i)
			}
			start, end := contextBounds(lineIdx, len(lines))
			end = max(end, min(len(lines), endLine+2))
			results = append(results, ProjectSearchMatch{
				Path:         filepath.ToSlash(path),
				RelativePath: rel,
				Line:         lineIdx + 1,
				EndLine:      endLine + 1,
				Column:       column,
				EndColumn:    endColumn,
				LineText:     line,
				NameMatched:  nameMatched,
				PathMatches:  append([]int(nil), pathPositions...),
				TextMatches:  positions,
				ContextStart: start + 1,
				Context:      strings.Join(lines[start:end], "\n"),
				Score:        9000 + boolScore(nameMatched, 250),
			})
			contentMatches++
			exactLines[lineIdx] = true
		}
		// Fuzzy content is project discovery only. Exact hits remain authoritative
		// and every suggestion is explicitly non-replaceable.
		if !opts.Regex && !opts.WholeWord && !strings.ContainsAny(query, "\r\n\t") {
			for lineIdx, line := range lines {
				if len(suggestions) >= maxProjectSearchResults {
					break
				}
				if exactLines[lineIdx] {
					continue
				}
				if lineIdx%128 == 0 {
					if err := ctx.Err(); err != nil {
						return err
					}
				}
				positions, score, ok := textsearch.Fuzzy(line, query, opts.CaseSensitive)
				if !ok || len(positions) == 0 {
					continue
				}
				start, end := contextBounds(lineIdx, len(lines))
				suggestions = append(suggestions, ProjectSearchMatch{Path: filepath.ToSlash(path), RelativePath: rel, Line: lineIdx + 1, EndLine: lineIdx + 1, Column: positions[0], EndColumn: positions[len(positions)-1] + 1, LineText: line, FuzzyOnly: true, NameMatched: nameMatched, PathMatches: append([]int(nil), pathPositions...), TextMatches: positions, ContextStart: start + 1, Context: strings.Join(lines[start:end], "\n"), Score: score})
				contentMatches++
			}
		}
		if nameMatched && contentMatches == 0 && len(suggestions) < maxProjectSearchResults {
			suggestions = append(suggestions, filenameProjectSearchMatch(path, rel, pathPositions, pathScore))
		}
		return stopProjectSearchIfFull(results)
	})
	if err != nil && err != fs.SkipAll {
		return nil, err
	}

	results = append(results, suggestions...)
	sort.SliceStable(results, func(i, j int) bool {
		iExact, jExact := !results[i].NameOnly && !results[i].FuzzyOnly, !results[j].NameOnly && !results[j].FuzzyOnly
		if iExact != jExact {
			return iExact
		}
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		if results[i].RelativePath != results[j].RelativePath {
			return results[i].RelativePath < results[j].RelativePath
		}
		if results[i].Line != results[j].Line {
			return results[i].Line < results[j].Line
		}
		return results[i].Column < results[j].Column
	})
	return results[:min(maxProjectSearchResults, len(results))], nil
}

func filenameProjectSearchMatch(path, rel string, positions []int, score int) ProjectSearchMatch {
	return ProjectSearchMatch{
		Path:         filepath.ToSlash(path),
		RelativePath: rel,
		NameOnly:     true,
		NameMatched:  true,
		PathMatches:  append([]int(nil), positions...),
		Score:        score + 500,
	}
}

func stopProjectSearchIfFull(results []ProjectSearchMatch) error {
	if len(results) >= maxProjectSearchResults {
		return fs.SkipAll
	}
	return nil
}

func contextBounds(lineIdx, lineCount int) (int, int) {
	start := lineIdx - projectSearchContext
	if start < 0 {
		start = 0
	}
	end := lineIdx + projectSearchContext + 1
	if end > lineCount {
		end = lineCount
	}
	return start, end
}

func boolScore(v bool, score int) int {
	if v {
		return score
	}
	return 0
}

func projectSearchMatch(text, query string, caseSensitive bool, re *regexp.Regexp) ([]int, int, bool) {
	if re != nil {
		loc := re.FindStringIndex(text)
		if loc == nil {
			return nil, 0, false
		}
		start := len([]rune(text[:loc[0]]))
		end := start + len([]rune(text[loc[0]:loc[1]]))
		positions := make([]int, 0, max(1, end-start))
		for i := start; i < end; i++ {
			positions = append(positions, i)
		}
		return positions, 9000 - start, true
	}
	return fuzzyRuneMatch(text, query, caseSensitive)
}

func fuzzyRuneMatch(text, query string, caseSensitive bool) ([]int, int, bool) {
	return textsearch.Fuzzy(text, query, caseSensitive)
}
