package fileio

import (
	"context"
	"crude/text/textencoding"
	"crude/text/textsearch"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	maxProjectSearchResults  = 1000
	maxProjectSearchFileSize = 4 << 20
)

// ProjectSearchOptions controls recursive filename and content matching.
type ProjectSearchOptions struct {
	CaseSensitive bool
	Regex         bool
	WholeWord     bool
	IncludeHidden bool
	// NamesOnly matches file paths only (Go to File): contents are not read.
	NamesOnly bool `json:"NamesOnly,omitempty"`
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

	Score int
}

// ProjectFile is one file that Find in Files visits. Text holds the decoded
// content when the caller already has it in memory; Load reads it otherwise.
// Large, Binary or Unreadable files are matched by name only.
type ProjectFile struct {
	Path, Rel  string
	Text       []byte
	Large      bool
	Binary     bool
	Unreadable bool
}

// ShouldSkipProjectDir reports a directory that Find in Files and the
// workspace text index never enter.
func ShouldSkipProjectDir(name string, includeHidden bool) bool {
	return shouldSkipDir(name, includeHidden)
}

// MaxProjectSearchFileSize is the largest file whose content is searched.
const MaxProjectSearchFileSize = maxProjectSearchFileSize

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
	search, err := newProjectSearch(query, opts)
	if err != nil {
		return nil, err
	}
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
		file := ProjectFile{Path: path, Rel: filepath.ToSlash(rel)}
		if opts.NamesOnly {
			return search.file(ctx, file)
		}
		info, statErr := entry.Info()
		if statErr != nil || info.Size() > maxProjectSearchFileSize {
			file.Large = true
		} else {
			file.Text, file.Binary, file.Unreadable = readProjectFile(path)
		}
		return search.file(ctx, file)
	})
	if err != nil && err != fs.SkipAll {
		return nil, err
	}
	return search.finish(), nil
}

// SearchProjectFiles is SearchProject over files that the caller already
// enumerated and loaded in walk order, such as the workspace text index.
// The results and their order are those of SearchProject on the same files.
func SearchProjectFiles(ctx context.Context, files []ProjectFile, query string, opts ProjectSearchOptions) ([]ProjectSearchMatch, error) {
	if query == "" {
		return nil, nil
	}
	search, err := newProjectSearch(query, opts)
	if err != nil {
		return nil, err
	}
	for i := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		file := files[i]
		if file.Text == nil && !file.Large && !file.Binary && !file.Unreadable && !opts.NamesOnly {
			file.Text, file.Binary, file.Unreadable = readProjectFile(file.Path)
		}
		if err := search.file(ctx, file); err != nil {
			if err == fs.SkipAll {
				break
			}
			return nil, err
		}
	}
	return search.finish(), nil
}

// ReadProjectFile reads and decodes one file the way Find in Files does.
func ReadProjectFile(path string) (text []byte, binary, unreadable bool) {
	return readProjectFile(path)
}

func readProjectFile(path string) (text []byte, binary, unreadable bool) {
	data, err := ReadFileSharedDelete(path)
	if err != nil {
		return nil, false, true
	}
	if IsBinary(data) {
		return nil, true, false
	}
	decoded, _, err := textencoding.DecodeText(data)
	if err != nil {
		return nil, false, true
	}
	if decoded == nil {
		decoded = []byte{}
	}
	return decoded, false, false
}

type projectSearch struct {
	query       string
	opts        ProjectSearchOptions
	re          *regexp.Regexp
	fuzzy       *textsearch.FuzzyMatcher
	matcher     *textsearch.Matcher
	results     []ProjectSearchMatch
	suggestions []ProjectSearchMatch
}

func newProjectSearch(query string, opts ProjectSearchOptions) (*projectSearch, error) {
	s := &projectSearch{query: query, opts: opts, fuzzy: textsearch.CompileFuzzy(query, opts.CaseSensitive),
		results: make([]ProjectSearchMatch, 0, 128), suggestions: make([]ProjectSearchMatch, 0, 64)}
	var err error
	s.matcher, err = textsearch.Compile(query, textsearch.Options{CaseSensitive: opts.CaseSensitive, WholeWord: opts.WholeWord, Regex: opts.Regex})
	if err != nil {
		return nil, fmt.Errorf("invalid query: %w", err)
	}
	if opts.Regex {
		pattern := query
		if !opts.CaseSensitive {
			pattern = "(?i)" + pattern
		}
		s.re, err = regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("invalid regex: %w", err)
		}
	}
	return s, nil
}

// file matches one file. It returns fs.SkipAll once results are full.
func (s *projectSearch) file(ctx context.Context, file ProjectFile) error {
	path, rel := file.Path, file.Rel
	pathPositions, pathScore, nameMatched := projectSearchMatch(rel, s.fuzzy, s.re)
	if s.opts.NamesOnly {
		if s.opts.WholeWord {
			hits := s.matcher.FindAll(rel, 1)
			nameMatched = len(hits) > 0
			if nameMatched {
				pathScore = 9000
				pathPositions = nil
				start := utf8.RuneCountInString(rel[:hits[0].Start])
				for n := range utf8.RuneCountInString(rel[hits[0].Start:hits[0].End]) {
					pathPositions = append(pathPositions, start+n)
				}
			}
		}
		if nameMatched {
			s.suggestions = append(s.suggestions, filenameProjectSearchMatch(path, rel, pathPositions, pathScore))
			if len(s.suggestions) >= 2*maxProjectSearchResults {
				s.suggestions = s.finish()
			}
		}
		return nil
	}
	if file.Large || file.Binary || file.Unreadable || file.Text == nil {
		if nameMatched && len(s.suggestions) < maxProjectSearchResults {
			s.suggestions = append(s.suggestions, filenameProjectSearchMatch(path, rel, pathPositions, pathScore))
		}
		return nil
	}
	text := string(file.Text)
	hits := s.matcher.FindAll(text, maxProjectSearchResults-len(s.results))
	fuzzyContent := !s.opts.Regex && !s.opts.WholeWord && !strings.ContainsAny(s.query, "\r\n\t") && len(s.suggestions) < maxProjectSearchResults
	// Most files have no exact hit. Once discovery capacity is full, do
	// not split or index their lines just to throw that work away.
	if len(hits) == 0 && !fuzzyContent {
		if nameMatched && len(s.suggestions) < maxProjectSearchResults {
			s.suggestions = append(s.suggestions, filenameProjectSearchMatch(path, rel, pathPositions, pathScore))
		}
		return nil
	}
	lines := strings.Split(text, "\n")
	starts := make([]int, len(lines))
	for i := 1; i < len(lines); i++ {
		starts[i] = starts[i-1] + len(lines[i-1]) + 1
	}
	contentMatches := 0
	exactLines := make(map[int]bool)
	for _, hit := range hits {
		if len(s.results) >= maxProjectSearchResults {
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
		s.results = append(s.results, ProjectSearchMatch{
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

			Score: 9000 + boolScore(nameMatched, 250),
		})
		contentMatches++
		exactLines[lineIdx] = true
	}
	// Fuzzy content is project discovery only. Exact hits remain authoritative
	// and every suggestion is explicitly non-replaceable.
	if fuzzyContent {
		for lineIdx, line := range lines {
			if len(s.suggestions) >= maxProjectSearchResults {
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
			positions, score, ok := s.fuzzy.Match(line)
			if !ok || len(positions) == 0 {
				continue
			}
			s.suggestions = append(s.suggestions, ProjectSearchMatch{Path: filepath.ToSlash(path), RelativePath: rel, Line: lineIdx + 1, EndLine: lineIdx + 1, Column: positions[0], EndColumn: positions[len(positions)-1] + 1, LineText: line, FuzzyOnly: true, NameMatched: nameMatched, PathMatches: append([]int(nil), pathPositions...), TextMatches: positions, Score: score})
			contentMatches++
		}
	}
	if nameMatched && contentMatches == 0 && len(s.suggestions) < maxProjectSearchResults {
		s.suggestions = append(s.suggestions, filenameProjectSearchMatch(path, rel, pathPositions, pathScore))
	}
	return stopProjectSearchIfFull(s.results)
}

// FilePathMatchClass compares slash-normalized names without guessing about
// case or filesystem identity. A basename alone does not prove a directory suffix.
func FilePathMatchClass(rel, query string) int {
	if rel == query || strings.HasSuffix(rel, "/"+query) {
		return 0
	}
	if path.Base(rel) == path.Base(query) {
		return 1
	}
	return 2
}

func (s *projectSearch) finish() []ProjectSearchMatch {
	results := append(s.results, s.suggestions...)
	sort.SliceStable(results, func(i, j int) bool {
		if s.opts.NamesOnly {
			a, b := FilePathMatchClass(results[i].RelativePath, s.query), FilePathMatchClass(results[j].RelativePath, s.query)
			if a != b {
				return a < b
			}
		}
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
	return results[:min(maxProjectSearchResults, len(results))]
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

func boolScore(v bool, score int) int {
	if v {
		return score
	}
	return 0
}

func projectSearchMatch(text string, fuzzy *textsearch.FuzzyMatcher, re *regexp.Regexp) ([]int, int, bool) {
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
	return fuzzy.Match(text)
}

func fuzzyRuneMatch(text, query string, caseSensitive bool) ([]int, int, bool) {
	return textsearch.Fuzzy(text, query, caseSensitive)
}

// SourceColumns locates a result in the current line. A changed buffer may
// still contain the same match, but an ambiguous or missing hit never moves it.
func (match ProjectSearchMatch) SourceColumns(current string) (int, int, bool) {
	from, to := match.Column, match.EndColumn
	if match.EndLine > match.Line {
		to = len([]rune(match.LineText))
	}
	if current != strings.TrimSuffix(match.LineText, "\r") {
		old := []rune(match.LineText)
		if from < 0 || to <= from || to > len(old) {
			return 0, 0, false
		}
		needle := string(old[from:to])
		live := []rune(current)
		if to > len(live) || string(live[from:to]) != needle {
			// Relocate only one exact occurrence on the same line. Never guess
			// between repeated hits or overwrite the live buffer with disk text.
			if strings.Count(current, needle) != 1 {
				return 0, 0, false
			}
			from = len([]rune(current[:strings.Index(current, needle)]))
			to = from + len([]rune(needle))
		}
	}
	return from, to, true
}
