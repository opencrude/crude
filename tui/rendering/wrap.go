package rendering

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

func SanitizeForTerminal(s string) string {
	if s == "" {
		return s
	}
	if utf8.ValidString(s) && !strings.ContainsRune(s, '\r') && !needsSanitization(s) {
		return s
	}
	if !utf8.ValidString(s) {
		logInvalidUTF8("SanitizeForTerminal", s)
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "")
	var b strings.Builder
	b.Grow(len(s))
	i := 0
	for i < len(s) {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			b.WriteByte('?')
			i++
			continue
		}
		switch {
		case r == '\n' || r == '\t':
			b.WriteRune(r)
		case unicode.IsControl(r):
			b.WriteByte(' ')
		default:
			b.WriteRune(r)
		}
		i += size
	}
	return b.String()
}

func needsSanitization(s string) bool {
	for _, r := range s {
		if r < ' ' && r != '\n' && r != '\t' {
			return true
		}
		if r == 0x7F {
			return true
		}
	}
	return false
}

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
		if VisibleWidth(para) <= width {
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
			if VisibleWidth(current)+1+VisibleWidth(w) <= width {
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
	rest := s
	var state byte
	for len(rest) > 0 {
		cluster, cw, _, newState := ansi.DecodeSequence(rest, state, nil)
		if curW+cw > maxWidth && curW > 0 {
			result = append(result, cur.String())
			cur.Reset()
			curW = 0
		}
		cur.WriteString(cluster)
		curW += cw
		rest = rest[len(cluster):]
		state = newState
	}
	if cur.Len() > 0 || len(result) == 0 {
		result = append(result, cur.String())
	}
	return result
}

// ExpandTabs expands horizontal tabs to spaces at fixed tab stops while
// measuring intervening text through the canonical grapheme-width path.
func ExpandTabs(s string, tabWidth int) string {
	if tabWidth < 1 {
		tabWidth = 4
	}
	if !strings.ContainsRune(s, '\t') {
		return s
	}
	var out strings.Builder
	out.Grow(len(s) + tabWidth)
	col := 0
	rest := s
	for len(rest) > 0 {
		if rest[0] == '\t' {
			advance := tabWidth - col%tabWidth
			out.WriteString(strings.Repeat(" ", advance))
			col += advance
			rest = rest[1:]
			continue
		}
		cluster, width := FirstGrapheme(rest)
		if cluster == "" {
			break
		}
		out.WriteString(cluster)
		col += width
		rest = rest[len(cluster):]
	}
	return out.String()
}

func WordWrap(s string, maxWidth int) []string {
	if maxWidth <= 0 {
		return []string{s}
	}
	if VisibleWidth(s) <= maxWidth {
		return []string{s}
	}

	type cluster struct {
		str   string
		width int
	}
	var clusters []cluster
	rest := s
	var state byte
	for len(rest) > 0 {
		c, cw, _, newState := ansi.DecodeSequence(rest, state, nil)
		clusters = append(clusters, cluster{str: c, width: cw})
		rest = rest[len(c):]
		state = newState
	}
	n := len(clusters)
	remainingW := 0
	for _, cluster := range clusters {
		remainingW += cluster.width
	}

	var result []string
	i := 0
	for i < n {
		if len(result) > 0 {
			for i < n && clusters[i].str == " " {
				remainingW -= clusters[i].width
				i++
			}
			if i >= n {
				break
			}
		}

		if remainingW <= maxWidth {
			var line strings.Builder
			for k := i; k < n; k++ {
				line.WriteString(clusters[k].str)
			}
			result = append(result, line.String())
			break
		}

		lineEnd := i + 1
		lastSpace := -1
		w := clusters[i].width

		for j := i + 1; j < n; j++ {
			rw := clusters[j].width
			if w+rw > maxWidth {
				break
			}
			w += rw
			lineEnd = j + 1
			if clusters[j].str == " " {
				lastSpace = j
			}
		}
		if lastSpace >= i {
			var line strings.Builder
			for k := i; k < lastSpace; k++ {
				line.WriteString(clusters[k].str)
				remainingW -= clusters[k].width
			}
			remainingW -= clusters[lastSpace].width
			result = append(result, line.String())
			i = lastSpace + 1
		} else {
			var line strings.Builder
			for k := i; k < lineEnd; k++ {
				line.WriteString(clusters[k].str)
				remainingW -= clusters[k].width
			}
			result = append(result, line.String())
			i = lineEnd
		}
	}

	if len(result) == 0 {
		return []string{""}
	}
	return result
}

func TruncateLine(s string, max int) string {
	plain := StripAnsi(s)
	if VisibleWidth(plain) <= max {
		return s
	}
	tailW := VisibleWidth("…")
	target := max - tailW
	if target < 0 {
		target = 0
	}
	rest := plain
	var result strings.Builder
	w := 0
	var state byte
	for len(rest) > 0 {
		cluster, cw, _, newState := ansi.DecodeSequence(rest, state, nil)
		if w+cw > target {
			break
		}
		result.WriteString(cluster)
		w += cw
		rest = rest[len(cluster):]
		state = newState
	}
	result.WriteString("…")
	return result.String()
}

func FindSegmentStart(logLine []rune, textW int, segmentIdx int) int {
	if segmentIdx <= 0 {
		return 0
	}
	s := string(logLine)

	type cluster struct {
		runeStart int
		runeEnd   int
		width     int
		str       string
	}
	var clusters []cluster
	rest := s
	var state byte
	runePos := 0
	for len(rest) > 0 {
		c, cw, _, newState := ansi.DecodeSequence(rest, state, nil)
		cRunes := len([]rune(c))
		clusters = append(clusters, cluster{
			runeStart: runePos,
			runeEnd:   runePos + cRunes,
			width:     cw,
			str:       c,
		})
		runePos += cRunes
		rest = rest[len(c):]
		state = newState
	}
	n := len(clusters)

	ci := 0
	currentSeg := 0
	for currentSeg < segmentIdx && ci < n {
		if currentSeg > 0 {
			for ci < n && clusters[ci].str == " " {
				ci++
			}
			if ci >= n {
				break
			}
		}

		remainingW := 0
		for k := ci; k < n; k++ {
			remainingW += clusters[k].width
		}
		if remainingW <= textW {
			// All remaining content fits: this is the final segment.
			break
		}

		lineEnd := ci + 1
		lastSpace := -1
		w := clusters[ci].width

		for j := ci + 1; j < n; j++ {
			rw := clusters[j].width
			if w+rw > textW {
				break
			}
			w += rw
			lineEnd = j + 1
			if clusters[j].str == " " {
				lastSpace = j
			}
		}

		currentSeg++

		if lastSpace >= ci {
			ci = lastSpace + 1
		} else {
			ci = lineEnd
		}
	}

	if segmentIdx > 0 {
		for ci < n && clusters[ci].str == " " {
			ci++
		}
	}

	if ci < n {
		return clusters[ci].runeStart
	}
	return len(logLine)
}
