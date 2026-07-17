package fileio

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"
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
	IncludeHidden bool
}

// ProjectSearchMatch is one selectable Find in Files result.
type ProjectSearchMatch struct {
	Path         string
	RelativePath string
	Line         int // 1-based; zero means filename-only
	Column       int // rune index, zero-based
	EndColumn    int // rune index, exclusive
	LineText     string
	NameOnly     bool
	NameMatched  bool
	PathMatches  []int // rune positions in RelativePath
	TextMatches  []int // rune positions in LineText
	ContextStart int   // 1-based first line in Context
	Context      string
	Score        int
}

// SearchProject recursively fuzzy-searches relative filenames and text lines.
// Regex mode replaces fuzzy matching with Go regular expressions. The walk is
// cancellation-aware and skips generated/dependency directories consistently
// with GlobFiles and GrepFiles.
func SearchProject(ctx context.Context, root, query string, opts ProjectSearchOptions) ([]ProjectSearchMatch, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}

	var re *regexp.Regexp
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
			if nameMatched {
				results = append(results, filenameProjectSearchMatch(path, rel, pathPositions, pathScore))
			}
			return stopProjectSearchIfFull(results)
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil || bytes.IndexByte(data[:min(len(data), 512)], 0) >= 0 {
			if nameMatched {
				results = append(results, filenameProjectSearchMatch(path, rel, pathPositions, pathScore))
			}
			return stopProjectSearchIfFull(results)
		}

		lines := strings.Split(string(data), "\n")
		contentMatches := 0
		for lineIdx, line := range lines {
			if len(results) >= maxProjectSearchResults {
				return fs.SkipAll
			}
			if lineIdx%128 == 0 {
				select {
				case <-ctx.Done():
					return ctx.Err()
				default:
				}
			}
			positions, score, matched := projectSearchMatch(line, query, opts.CaseSensitive, re)
			if !matched {
				continue
			}
			start, end := contextBounds(lineIdx, len(lines))
			column, endColumn := matchColumns(positions)
			results = append(results, ProjectSearchMatch{
				Path:         filepath.ToSlash(path),
				RelativePath: rel,
				Line:         lineIdx + 1,
				Column:       column,
				EndColumn:    endColumn,
				LineText:     line,
				NameMatched:  nameMatched,
				PathMatches:  append([]int(nil), pathPositions...),
				TextMatches:  positions,
				ContextStart: start + 1,
				Context:      strings.Join(lines[start:end], "\n"),
				Score:        score + boolScore(nameMatched, 250),
			})
			contentMatches++
		}
		if nameMatched && contentMatches == 0 && len(results) < maxProjectSearchResults {
			results = append(results, filenameProjectSearchMatch(path, rel, pathPositions, pathScore))
		}
		return stopProjectSearchIfFull(results)
	})
	if err != nil && err != fs.SkipAll {
		return nil, err
	}

	sort.SliceStable(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		if results[i].RelativePath != results[j].RelativePath {
			return results[i].RelativePath < results[j].RelativePath
		}
		return results[i].Line < results[j].Line
	})
	return results, nil
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

func matchColumns(positions []int) (int, int) {
	if len(positions) == 0 {
		return 0, 0
	}
	return positions[0], positions[len(positions)-1] + 1
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
	textRunes := []rune(text)
	queryRunes := []rune(query)
	if len(queryRunes) == 0 {
		return nil, 0, true
	}
	cmpText := append([]rune(nil), textRunes...)
	cmpQuery := append([]rune(nil), queryRunes...)
	if !caseSensitive {
		for i := range cmpText {
			cmpText[i] = unicode.ToLower(cmpText[i])
		}
		for i := range cmpQuery {
			cmpQuery[i] = unicode.ToLower(cmpQuery[i])
		}
	}
	// Prefer a real contiguous occurrence over the subsequence fallback. A
	// greedy subsequence can otherwise latch onto an earlier character and
	// highlight a scattered match even when an exact substring exists later.
	if byteStart := strings.Index(string(cmpText), string(cmpQuery)); byteStart >= 0 {
		start := len([]rune(string(cmpText)[:byteStart]))
		positions := make([]int, len(cmpQuery))
		for i := range positions {
			positions[i] = start + i
		}
		score := 7000 - start - len(cmpText)
		if len(cmpText) == len(cmpQuery) {
			score += 7000
		}
		if start == 0 || isSearchBoundary(textRunes[start-1]) {
			score += 200
		}
		return positions, score, true
	}

	positions := make([]int, 0, len(cmpQuery))
	qi := 0
	for i, r := range cmpText {
		if qi < len(cmpQuery) && r == cmpQuery[qi] {
			positions = append(positions, i)
			qi++
		}
	}
	if qi != len(cmpQuery) {
		return nil, 0, false
	}

	score := 3000 - len(cmpText)
	if len(cmpText) == len(cmpQuery) {
		score += 7000
	}
	contiguous := true
	for i, pos := range positions {
		if i > 0 {
			if pos == positions[i-1]+1 {
				score += 120
			} else {
				contiguous = false
			}
		}
		if pos == 0 || isSearchBoundary(textRunes[pos-1]) {
			score += 80
		}
	}
	if contiguous {
		score += 3000 - positions[0]
	}
	return positions, score, true
}

func isSearchBoundary(r rune) bool {
	return r == '/' || r == '\\' || r == '_' || r == '-' || r == '.' || unicode.IsSpace(r)
}
