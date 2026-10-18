package rendering

import "unicode/utf8"

// GraphemeBoundary snaps a rune offset to the start of its complete source
// cluster. End-of-text is a valid insertion boundary. Width never substitutes
// for source offsets (combining/ZWJ clusters may contain many runes).
func GraphemeBoundary(runes []rune, pos int) int {
	pos = min(len(runes), max(0, pos))
	if pos == 0 || pos == len(runes) {
		return pos
	}
	if asciiSourceUnit(runes[pos-1]) && asciiSourceUnit(runes[pos]) {
		return pos
	}
	start := 0
	for rest := string(runes); rest != ""; {
		cluster, _ := FirstGrapheme(rest)
		if cluster == "" {
			break
		}
		end := start + utf8.RuneCountInString(cluster)
		if pos < end {
			return start
		}
		if pos == end {
			return end
		}
		start, rest = end, rest[len(cluster):]
	}
	return len(runes)
}

func PreviousGrapheme(runes []rune, pos int) int {
	pos = min(len(runes), max(0, pos))
	if pos == 0 {
		return 0
	}
	if asciiSourceUnit(runes[pos-1]) && (pos == 1 || asciiSourceUnit(runes[pos-2])) && (pos == len(runes) || asciiSourceUnit(runes[pos])) {
		return pos - 1
	}
	return GraphemeBoundary(runes, pos-1)
}

func NextGrapheme(runes []rune, pos int) int {
	pos = min(len(runes), max(0, pos))
	if pos == len(runes) {
		return pos
	}
	if asciiSourceUnit(runes[pos]) && (pos+1 == len(runes) || asciiSourceUnit(runes[pos+1])) {
		return pos + 1
	}
	start := 0
	for rest := string(runes); rest != ""; {
		cluster, _ := FirstGrapheme(rest)
		if cluster == "" {
			break
		}
		end := start + utf8.RuneCountInString(cluster)
		if pos < end {
			return end
		}
		start, rest = end, rest[len(cluster):]
	}
	return len(runes)
}

func asciiSourceUnit(r rune) bool { return r >= 32 && r < utf8.RuneSelf }
