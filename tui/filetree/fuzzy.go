package filetree

import "unicode"

// FuzzyMatch reports whether pattern is a case-insensitive subsequence of
// name and returns the rune positions in name that were consumed. Positions
// are ascending and one per pattern rune (greedy left-to-right). An empty
// pattern matches with no positions.
func FuzzyMatch(name, pattern string) ([]int, bool) {
	if pattern == "" {
		return nil, true
	}
	nameRunes := []rune(name)
	patRunes := []rune(pattern)
	positions := make([]int, 0, len(patRunes))
	pi := 0
	for ni := 0; ni < len(nameRunes) && pi < len(patRunes); ni++ {
		if unicode.ToLower(nameRunes[ni]) == unicode.ToLower(patRunes[pi]) {
			positions = append(positions, ni)
			pi++
		}
	}
	if pi < len(patRunes) {
		return nil, false
	}
	return positions, true
}
