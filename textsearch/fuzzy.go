package textsearch

import (
	"slices"
	"sort"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/junegunn/fzf/src/algo"
	"github.com/junegunn/fzf/src/util"
)

var fuzzyInit sync.Once
var fuzzySlabs = sync.Pool{New: func() any {
	return &fuzzyScratch{slab: util.MakeSlab(16<<10, 2<<10)}
}}

type fuzzyScratch struct {
	slab *util.Slab
	text []byte
}

func (s *fuzzyScratch) chars(value string) util.Chars {
	// Reuse input conversion for ordinary source lines. A very long line
	// still matches, but cannot leave a source-sized buffer in the pool.
	if len(value) > 16<<10 {
		return util.ToChars([]byte(value))
	}
	s.text = append(s.text[:0], value...)
	return util.ToChars(s.text)
}

// FuzzyMatcher retains one immutable discovery query. Match is safe to use
// concurrently; each call borrows its own bounded fzf scratch space.
type FuzzyMatcher struct {
	patterns      [][]rune
	caseSensitive bool
	valid         bool
}

func CompileFuzzy(query string, caseSensitive bool) *FuzzyMatcher {
	m := &FuzzyMatcher{caseSensitive: caseSensitive, valid: !strings.ContainsRune(query, 0)}
	for _, term := range strings.Fields(query) {
		pattern := []rune(term)
		foldPattern(pattern, caseSensitive)
		m.patterns = append(m.patterns, pattern)
	}
	return m
}

// Fuzzy ranks project discovery using fzf's established V2 matcher, with its
// bounded V1 path for large inputs. A query follows fzf's extended-search term
// rule: whitespace separates terms, and every term must match. Each term must
// match inside one whitespace-delimited token of value, so a phrase never
// assembles a hit from letters scattered across unrelated prose words. Exact
// substrings always qualify; scattered suggestions must earn at least fzf's
// neutral ungapped score (16 per rune), so gap penalties can reject weak hits.
// The score is the sum of the term scores. Positions are sorted, distinct source
// rune indices, never layout columns or replacement spans.
func Fuzzy(value, query string, caseSensitive bool) (positions []int, score int, ok bool) {
	if strings.ContainsRune(query, 0) {
		return nil, 0, false
	}
	return matchFuzzy(value, strings.Fields(query), nil, caseSensitive)
}

func (m *FuzzyMatcher) Match(value string) (positions []int, score int, ok bool) {
	if !m.valid {
		return nil, 0, false
	}
	return matchFuzzy(value, nil, m.patterns, m.caseSensitive)
}

// The convenience function prepares short-lived patterns on the stack; project
// scans supply retained patterns. Both use the same term/token matching path.
func matchFuzzy(value string, terms []string, patterns [][]rune, caseSensitive bool) (positions []int, score int, ok bool) {
	count := max(len(terms), len(patterns))
	if count == 0 {
		return nil, 0, true
	}
	if strings.ContainsRune(value, 0) {
		return nil, 0, false
	}
	fuzzyInit.Do(func() { algo.Init("default") })
	scratch := fuzzySlabs.Get().(*fuzzyScratch)
	defer fuzzySlabs.Put(scratch)
	for i := range count {
		var pattern []rune
		if patterns != nil {
			pattern = patterns[i]
		} else {
			pattern = []rune(terms[i])
			foldPattern(pattern, caseSensitive)
		}
		termPositions, termScore, matched := fuzzyTerm(value, pattern, caseSensitive, scratch)
		if !matched {
			return nil, 0, false
		}
		score += termScore
		positions = append(positions, termPositions...)
	}
	sort.Ints(positions)
	return slices.Compact(positions), score, true
}

func foldPattern(pattern []rune, caseSensitive bool) {
	if !caseSensitive {
		for i, r := range pattern {
			pattern[i] = unicode.ToLower(r)
		}
	}
}

// fuzzyTerm returns the best match of one term inside a single token of value.
func fuzzyTerm(value string, pattern []rune, caseSensitive bool, scratch *fuzzyScratch) (positions []int, score int, ok bool) {
	if !strings.ContainsFunc(value, unicode.IsSpace) {
		return fuzzyToken(value, pattern, caseSensitive, scratch)
	}
	// A term which cannot match the complete line cannot match any token.
	// Use fzf's V1 scan without positions as a negative filter before
	// tokenization. Positive candidates still take the unchanged V2/scoring
	// path below, so this cannot create cross-word suggestions.
	input := scratch.chars(value)
	candidate, _ := algo.FuzzyMatchV1(caseSensitive, false, true, &input, pattern, false, scratch.slab)
	if candidate.Start < 0 {
		return nil, 0, false
	}
	best := -1
	runeOffset := 0
	for rest := value; rest != ""; {
		r, size := utf8.DecodeRuneInString(rest)
		if unicode.IsSpace(r) {
			runeOffset++
			rest = rest[size:]
			continue
		}
		end := strings.IndexFunc(rest, unicode.IsSpace)
		if end < 0 {
			end = len(rest)
		}
		pos, n, matched := fuzzyToken(rest[:end], pattern, caseSensitive, scratch)
		if matched && n > best {
			positions = pos
			for i := range positions {
				positions[i] += runeOffset
			}
			score, best, ok = n, n, true
		}
		runeOffset += utf8.RuneCountInString(rest[:end])
		rest = rest[end:]
	}
	return positions, score, ok
}

func fuzzyToken(value string, pattern []rune, caseSensitive bool, scratch *fuzzyScratch) (positions []int, score int, ok bool) {
	if len(value) < len(pattern) {
		return nil, 0, false
	}
	input := scratch.chars(value)
	result, indices := algo.FuzzyMatchV2(caseSensitive, false, true, &input, pattern, true, scratch.slab)
	if result.Start < 0 || indices == nil || len(*indices) != len(pattern) {
		return nil, 0, false
	}
	positions = append([]int(nil), (*indices)...)
	sort.Ints(positions)
	contiguous := true
	for i := 1; i < len(positions); i++ {
		if positions[i] != positions[i-1]+1 {
			contiguous = false
			break
		}
	}
	if !contiguous && result.Score < 16*len(pattern) {
		return nil, 0, false
	}
	return positions, result.Score, true
}
