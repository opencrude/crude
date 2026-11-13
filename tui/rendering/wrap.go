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

// WrapRange is a byte interval in the original immutable text. Wrapping owns
// these boundaries; paint and source projection must not infer them afterward.
type WrapRange struct{ Start, End int }

func WordWrap(s string, maxWidth int) []string {
	if maxWidth <= 0 || VisibleWidth(s) <= maxWidth {
		return []string{s}
	}
	ranges := wordWrapRanges(s, maxWidth)
	lines := make([]string, len(ranges))
	for i, r := range ranges {
		lines[i] = s[r.Start:r.End]
	}
	return lines
}

// WordWrapRanges applies the existing word/grapheme wrap policy and returns
// source slices instead of rebuilding strings. Text, copy offsets and composer
// rune starts all derive from this one segmentation.
func WordWrapRanges(s string, maxWidth int) []WrapRange {
	if maxWidth <= 0 || VisibleWidth(s) <= maxWidth {
		return []WrapRange{{0, len(s)}}
	}
	return wordWrapRanges(s, maxWidth)
}

func wordWrapRanges(s string, maxWidth int) []WrapRange {
	type cluster struct{ end, width int }
	clusters := make([]cluster, 0, len(s))
	var state byte
	rest := s
	remainingW := 0
	for len(rest) > 0 {
		c, cw, _, next := ansi.DecodeSequence(rest, state, nil)
		rest = rest[len(c):]
		state = next
		clusters = append(clusters, cluster{end: len(s) - len(rest), width: cw})
		remainingW += cw
	}
	begin := func(i int) int {
		if i == 0 {
			return 0
		}
		return clusters[i-1].end
	}
	isSpace := func(i int) bool { return s[begin(i):clusters[i].end] == " " }
	n := len(clusters)
	var result []WrapRange
	for i := 0; i < n; {
		if len(result) > 0 {
			for i < n && isSpace(i) {
				remainingW -= clusters[i].width
				i++
			}
			if i >= n {
				break
			}
		}
		if remainingW <= maxWidth {
			result = append(result, WrapRange{begin(i), len(s)})
			break
		}
		lineEnd, lastSpace := i+1, -1
		width := clusters[i].width
		for j := i + 1; j < n; j++ {
			if width+clusters[j].width > maxWidth {
				break
			}
			width += clusters[j].width
			lineEnd = j + 1
			if isSpace(j) {
				lastSpace = j
			}
		}
		end, next := lineEnd, lineEnd
		if lastSpace >= i {
			end, next = lastSpace, lastSpace+1
		}
		result = append(result, WrapRange{begin(i), begin(end)})
		for k := i; k < next; k++ {
			remainingW -= clusters[k].width
		}
		i = next
	}
	if len(result) == 0 {
		return []WrapRange{{0, 0}}
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

// FindSegmentStarts returns the rune offset where each WordWrap segment of one
// logical line begins. It projects canonical byte segments to rune offsets in
// one walk. Asking for one start per segment
// re-parsed the whole line for every row, so a 1 MiB single-line paste spent
// minutes in cursor projection before the view could repaint.
func FindSegmentStarts(logLine []rune, textW int) []int {
	text := string(logLine)
	ranges := WordWrapRanges(text, textW)
	starts := make([]int, len(ranges))
	byteStart, runeStart := 0, 0
	for i, r := range ranges {
		runeStart += utf8.RuneCountInString(text[byteStart:r.Start])
		starts[i] = runeStart
		byteStart = r.Start
	}
	return starts
}
