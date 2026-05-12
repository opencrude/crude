package rendering

import (
	"strings"

	"github.com/mattn/go-runewidth"
)

func Max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func Min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func Clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func WrapText(text string, width int) []string {
	if width <= 0 {
		return []string{text}
	}
	var result []string
	for _, para := range strings.Split(text, "\n") {
		if runewidth.StringWidth(para) <= width {
			result = append(result, para)
			continue
		}
		words := strings.Fields(para)
		if len(words) == 0 {
			result = append(result, "")
			continue
		}
		current := words[0]
		for _, w := range words[1:] {
			if runewidth.StringWidth(current)+1+runewidth.StringWidth(w) <= width {
				current += " " + w
			} else {
				result = append(result, current)
				current = w
			}
		}
		result = append(result, current)
	}
	if len(result) == 0 {
		result = []string{""}
	}
	return result
}

func HardWrap(s string, maxWidth int) []string {
	if maxWidth <= 0 {
		return []string{s}
	}
	var result []string
	var cur strings.Builder
	curW := 0
	for _, r := range s {
		rw := runewidth.RuneWidth(r)
		if curW+rw > maxWidth && curW > 0 {
			result = append(result, cur.String())
			cur.Reset()
			curW = 0
		}
		cur.WriteRune(r)
		curW += rw
	}
	if cur.Len() > 0 || len(result) == 0 {
		result = append(result, cur.String())
	}
	return result
}

func WordWrap(s string, maxWidth int) []string {
	if maxWidth <= 0 {
		return []string{s}
	}
	runes := []rune(s)
	n := len(runes)
	if n == 0 {
		return []string{""}
	}

	var result []string
	i := 0
	for i < n {
		if len(result) > 0 {
			for i < n && runes[i] == ' ' {
				i++
			}
			if i >= n {
				break
			}
		}

		if runewidth.StringWidth(string(runes[i:])) <= maxWidth {
			result = append(result, string(runes[i:]))
			break
		}

		lineEnd := i + 1
		lastSpace := -1
		w := runewidth.RuneWidth(runes[i])

		for j := i + 1; j < n; j++ {
			rw := runewidth.RuneWidth(runes[j])
			if w+rw > maxWidth {
				break
			}
			w += rw
			lineEnd = j + 1
			if runes[j] == ' ' {
				lastSpace = j
			}
		}

		if lastSpace >= i {
			result = append(result, string(runes[i:lastSpace]))
			i = lastSpace + 1
		} else {
			result = append(result, string(runes[i:lineEnd]))
			i = lineEnd
		}
	}

	if len(result) == 0 {
		return []string{""}
	}
	return result
}

func TruncateLine(s string, max int) string {
	if runewidth.StringWidth(s) <= max {
		return s
	}
	return runewidth.Truncate(s, max, "…")
}

func FindSegmentStart(logLine []rune, textW int, segmentIdx int) int {
	if segmentIdx <= 0 {
		return 0
	}
	s := string(logLine)
	runes := []rune(s)
	n := len(runes)

	i := 0
	currentSeg := 0
	for currentSeg < segmentIdx && i < n {
		if currentSeg > 0 {
			for i < n && runes[i] == ' ' {
				i++
			}
			if i >= n {
				break
			}
		}

		if runewidth.StringWidth(string(runes[i:])) <= textW {
			currentSeg++
			break
		}

		lineEnd := i + 1
		lastSpace := -1
		w := runewidth.RuneWidth(runes[i])

		for j := i + 1; j < n; j++ {
			rw := runewidth.RuneWidth(runes[j])
			if w+rw > textW {
				break
			}
			w += rw
			lineEnd = j + 1
			if runes[j] == ' ' {
				lastSpace = j
			}
		}

		currentSeg++

		if lastSpace >= i {
			i = lastSpace + 1
		} else {
			i = lineEnd
		}
	}

	if segmentIdx > 0 {
		for i < n && runes[i] == ' ' {
			i++
		}
	}
	return i
}
