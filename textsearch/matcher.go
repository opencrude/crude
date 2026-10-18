// Package textsearch owns source-byte matching and replacement expansion for
// document and project search. It has no renderer or persistence authority.
package textsearch

import (
	"regexp"
	"unicode"
	"unicode/utf8"
)

type Options struct{ CaseSensitive, WholeWord, Regex bool }

type Match struct {
	Start, End int
	Indices    []int
}

type Matcher struct {
	re      *regexp.Regexp
	options Options
}

func Compile(query string, options Options) (*Matcher, error) {
	m := &Matcher{options: options}
	if query == "" {
		return m, nil
	}
	pattern := query
	if !options.Regex {
		pattern = regexp.QuoteMeta(pattern)
	}
	flags := "m"
	if !options.CaseSensitive {
		flags += "i"
	}
	re, err := regexp.Compile("(?" + flags + ")" + pattern)
	if err != nil {
		return nil, err
	}
	m.re = re
	return m, nil
}

// FindAll returns non-overlapping matches against original UTF-8 bytes. A
// nonpositive limit means unlimited. Zero-width regex hits remain real matches.
func (m *Matcher) FindAll(text string, limit int) []Match {
	if m == nil || m.re == nil {
		return nil
	}
	var matches []Match
	n := -1
	if limit > 0 && !m.options.WholeWord {
		n = limit
	}
	for _, indices := range m.re.FindAllStringSubmatchIndex(text, n) {
		start, end := indices[0], indices[1]
		if m.options.WholeWord && !wholeWord(text, start, end) {
			continue
		}
		matches = append(matches, Match{Start: start, End: end, Indices: indices})
		if limit > 0 && len(matches) >= limit {
			break
		}
	}
	return matches
}

func (m *Matcher) Expand(text string, match Match, replacement string) string {
	if m == nil || !m.options.Regex {
		return replacement
	}
	return string(m.re.ExpandString(nil, replacement, text, match.Indices))
}

func wholeWord(text string, start, end int) bool {
	word := func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r) || r == '_' }
	if start > 0 {
		r, _ := utf8.DecodeLastRuneInString(text[:start])
		if word(r) {
			return false
		}
	}
	if end < len(text) {
		r, _ := utf8.DecodeRuneInString(text[end:])
		if word(r) {
			return false
		}
	}
	return true
}
