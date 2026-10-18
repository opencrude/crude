package filetree

import "crude/textsearch"

// FuzzyMatch reports whether pattern is a case-insensitive subsequence of
// name and returns the rune positions in name that were consumed. Positions
// are ascending and one per pattern rune (fzf alignment). An empty
// pattern matches with no positions.
func FuzzyMatch(name, pattern string) ([]int, bool) {
	positions, _, ok := textsearch.Fuzzy(name, pattern, false)
	return positions, ok
}
