package textsearch

import (
	"sort"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/junegunn/fzf/src/algo"
	"github.com/junegunn/fzf/src/util"
)

var fuzzyInit sync.Once
var fuzzySlabs = sync.Pool{New: func() any { return util.MakeSlab(16<<10, 2<<10) }}

// Fuzzy ranks project discovery using fzf's established V2 matcher, with its
// bounded V1 path for large inputs. Every query rune must match in order. Exact
// substrings always qualify; scattered suggestions must earn at least fzf's
// neutral ungapped score (16 per rune), so gap penalties can reject weak hits.
// Positions are source rune indices, never layout columns or replacement spans.
func Fuzzy(value, query string, caseSensitive bool) (positions []int, score int, ok bool) {
	if query == "" {
		return nil, 0, true
	}
	if strings.ContainsRune(query, 0) || strings.ContainsRune(value, 0) {
		return nil, 0, false
	}
	fuzzyInit.Do(func() { algo.Init("default") })
	pattern := []rune(query)
	if !caseSensitive {
		for i, r := range pattern {
			pattern[i] = unicode.ToLower(r)
		}
	}
	slab := fuzzySlabs.Get().(*util.Slab)
	defer fuzzySlabs.Put(slab)
	// A single word query discovers an identifier/path, not an accidental
	// abbreviation assembled across unrelated prose words. Explicit whitespace
	// in a query permits phrase matching. fzf still owns alignment and scoring.
	if !strings.ContainsFunc(query, unicode.IsSpace) && strings.ContainsFunc(value, unicode.IsSpace) {
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
			pos, n, matched := fuzzyToken(rest[:end], pattern, caseSensitive, slab)
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
	return fuzzyToken(value, pattern, caseSensitive, slab)
}

func fuzzyToken(value string, pattern []rune, caseSensitive bool, slab *util.Slab) (positions []int, score int, ok bool) {
	input := util.ToChars([]byte(value))
	result, indices := algo.FuzzyMatchV2(caseSensitive, false, true, &input, pattern, true, slab)
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
