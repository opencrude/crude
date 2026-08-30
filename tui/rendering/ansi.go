package rendering

import (
	"encoding/hex"
	"strings"
	"sync/atomic"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

var invalidUTF8Logger atomic.Pointer[func(tag, msg string, pairs ...any)]

func SetInvalidUTF8Logger(fn func(tag, msg string, pairs ...any)) {
	invalidUTF8Logger.Store(&fn)
}

func logInvalidUTF8(source string, s string) {
	fn := invalidUTF8Logger.Load()
	if fn == nil {
		return
	}
	badBytes := []byte{}
	badOff := -1
	for i := 0; i < len(s); i++ {
		if s[i] < 0x80 {
			continue
		}
		_, size := utf8.DecodeRuneInString(s[i:])
		if size == 1 {
			if badOff == -1 {
				badOff = i
			}
			end := i + 8
			if end > len(s) {
				end = len(s)
			}
			badBytes = append(badBytes, s[i:end]...)
		}
		i += size - 1
	}
	ctx := ""
	if badOff >= 0 {
		start := badOff - 16
		if start < 0 {
			start = 0
		}
		end := badOff + 32
		if end > len(s) {
			end = len(s)
		}
		ctx = s[start:end]
	}
	(*fn)("UTF8", "invalid_runes_in_render", "source", source, "string_len", len(s), "first_bad_offset", badOff, "bad_bytes_hex", hex.EncodeToString(badBytes), "context", ctx)
}

func TrimRightAnsiSpaces(s string) string {
	lastNonSpaceEnd := 0
	i := 0
	for i < len(s) {
		if s[i] == '\x1b' {
			j := i + 1
			if j < len(s) && s[j] == '[' {
				j++
				for j < len(s) {
					if s[j] >= 0x40 && s[j] <= 0x7e {
						j++
						break
					}
					j++
				}
			} else if j < len(s) && s[j] >= 0x40 && s[j] <= 0x5f {
				j++
			} else if j < len(s) {
				j++
			} else {
				break
			}
			i = j
		} else {
			rn, size := utf8.DecodeRuneInString(s[i:])
			if size == 0 {
				break
			}
			if rn == utf8.RuneError {
				lastNonSpaceEnd = i + size
			} else if !unicode.IsSpace(rn) {
				lastNonSpaceEnd = i + size
			}
			i += size
		}
	}
	if lastNonSpaceEnd == 0 {
		if !utf8.ValidString(s) {
			logInvalidUTF8("TrimRightAnsiSpaces", s)
		}
		return ""
	}
	return s[:lastNonSpaceEnd] + "\x1b[0m"
}

func StripAnsi(s string) string {
	// Fast path: no escape byte means no ANSI to strip. Plain text (the
	// overwhelming case in label/list/button paint paths) returns unchanged
	// with zero allocations.
	if !strings.ContainsRune(s, '\x1b') {
		return s
	}
	var buf strings.Builder
	i := 0
	for i < len(s) {
		if s[i] == '\x1b' {
			j := i + 1
			if j < len(s) && s[j] == '[' {
				j++
				for j < len(s) {
					if s[j] >= 0x40 && s[j] <= 0x7e {
						j++
						break
					}
					j++
				}
			} else if j < len(s) && s[j] == ']' {
				j++
				for j < len(s) && s[j] != '\x07' && s[j] != '\x1b' {
					j++
				}
				if j < len(s) && s[j] == '\x07' {
					j++
				}
			} else if j < len(s) && s[j] >= 0x40 && s[j] <= 0x5f {
				j++
			} else if j < len(s) {
				j++
			} else {
				break
			}
			i = j
		} else {
			buf.WriteByte(s[i])
			i++
		}
	}
	return buf.String()
}

// VisibleWidth is the single point of truth for terminal column width.
// Strips ANSI escape sequences, then measures under the active width
// method. Must match Bubble Tea / ultraviolet's cellbuf.Method exactly.
func VisibleWidth(s string) int {
	return WidthMethod.StringWidth(StripAnsi(s))
}

// SkipVisualPrefix strips the first n visual characters from an ANSI string,
// preserving ANSI escape sequences. If n exceeds the string width, an empty
// string is returned.
func SkipVisualPrefix(s string, n int) string {
	if n <= 0 {
		return s
	}
	plain := StripAnsi(s)
	srcW := VisibleWidth(plain)
	if n >= srcW {
		return ""
	}

	var state byte
	skipped := 0
	i := 0
	runes := []rune(s)
	for i < len(runes) && skipped < n {
		if runes[i] == '\x1b' {
			// Skip ANSI escape sequence
			j := i + 1
			for j < len(runes) && runes[j] != 'm' {
				j++
			}
			if j < len(runes) {
				i = j + 1
			} else {
				i = j
			}
			continue
		}
		cluster, _, _, newState := ansi.DecodeSequence(string(runes[i:]), state, nil)
		state = newState
		skipped += VisibleWidth(cluster)
		i += len([]rune(cluster))
	}

	if i >= len(runes) {
		return ""
	}
	return string(runes[i:])
}

func TruncateVisual(s string, maxW int, tail string) string {
	plain := StripAnsi(s)
	if VisibleWidth(plain) <= maxW {
		return s
	}
	tailW := VisibleWidth(tail)
	target := maxW - tailW
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
	result.WriteString(tail)
	return result.String()
}

// TruncateMiddleVisual elides the center of plain display text while
// preserving both its identifying prefix and useful suffix (basename,
// duration, counts). It uses the same calibrated grapheme widths as layout.
func TruncateMiddleVisual(s string, maxW int, marker string) string {
	plain := StripAnsi(s)
	if maxW <= 0 {
		return ""
	}
	if VisibleWidth(plain) <= maxW {
		return plain
	}
	markerW := VisibleWidth(marker)
	if markerW >= maxW {
		return TruncateVisual(marker, maxW, "")
	}
	type segment struct {
		text  string
		width int
	}
	var segments []segment
	rest := plain
	var state byte
	for len(rest) > 0 {
		cluster, width, _, nextState := ansi.DecodeSequence(rest, state, nil)
		if cluster == "" {
			break
		}
		segments = append(segments, segment{text: cluster, width: width})
		rest = rest[len(cluster):]
		state = nextState
	}
	available := maxW - markerW
	leftBudget := (available + 1) / 2
	rightBudget := available - leftBudget
	var left, right strings.Builder
	leftWidth := 0
	leftEnd := 0
	for leftEnd < len(segments) && leftWidth+segments[leftEnd].width <= leftBudget {
		left.WriteString(segments[leftEnd].text)
		leftWidth += segments[leftEnd].width
		leftEnd++
	}
	rightStart := len(segments)
	rightWidth := 0
	for rightStart > leftEnd && rightWidth+segments[rightStart-1].width <= rightBudget {
		rightStart--
		rightWidth += segments[rightStart].width
	}
	for i := rightStart; i < len(segments); i++ {
		right.WriteString(segments[i].text)
	}
	return left.String() + marker + right.String()
}

func ApplySelection(line string, startCol, endCol int) string {
	if startCol >= endCol || startCol < 0 {
		return line
	}
	var buf strings.Builder
	col := 0
	inSelect := false

	i := 0
	for i < len(line) {
		if line[i] == '\x1b' && i+1 < len(line) {
			j := i + 1
			if line[j] == '[' {
				j++
				seqEnd := j
				for seqEnd < len(line) {
					if line[seqEnd] >= 0x40 && line[seqEnd] <= 0x7e {
						seqEnd++
						break
					}
					seqEnd++
				}
				if seqEnd > len(line) {
					seqEnd = len(line)
				}
				cmd := line[i:seqEnd]

				params := line[i+2 : seqEnd-1]
				finalChar := line[seqEnd-1]
				isReset := finalChar == 'm' && (params == "0" || params == "")

				buf.WriteString(cmd)
				if isReset && inSelect {
					buf.WriteString("\x1b[7m")
				}
				i = seqEnd
				continue
			} else if line[j] == ']' {
				j++
				for j < len(line) && line[j] != '\x07' && line[j] != '\x1b' {
					j++
				}
				if j < len(line) && line[j] == '\x07' {
					j++
				}
				buf.WriteString(line[i:j])
				i = j
				continue
			} else if line[j] >= 0x40 && line[j] <= 0x5f {
				j++
				buf.WriteString(line[i:j])
				i = j
				continue
			}
		}

		segment := line[i:]
		cluster, cw, n, _ := ansi.DecodeSequence(segment, 0, nil)
		nextCol := col + cw

		shouldSelect := col >= startCol || nextCol > startCol
		shouldNotSelect := nextCol <= startCol || col >= endCol

		shouldSelectFull := col >= startCol && col < endCol

		if shouldSelectFull && !inSelect {
			buf.WriteString("\x1b[7m")
			inSelect = true
		} else if !shouldSelectFull && inSelect {
			if !shouldSelect || col >= endCol {
				buf.WriteString("\x1b[27m")
				inSelect = false
			}
		}

		buf.WriteString(cluster)
		col = nextCol
		i += n
		_ = shouldNotSelect
	}

	if inSelect {
		buf.WriteString("\x1b[27m")
	}

	return buf.String()
}

func ExtractVisibleRange(s string, startCol, endCol int) string {
	var buf strings.Builder
	col := 0
	rest := s
	var state byte
	for len(rest) > 0 {
		cluster, cw, _, newState := ansi.DecodeSequence(rest, state, nil)
		if endCol >= 0 && col >= endCol {
			break
		}
		nextCol := col + cw
		if nextCol > startCol {
			buf.WriteString(cluster)
		}
		col = nextCol
		rest = rest[len(cluster):]
		state = newState
	}
	return buf.String()
}

func ExtractSelectedText(allLines []string, startLine, startCol, endLine, endCol int) string {
	if startLine == endLine {
		if startLine < 0 || startLine >= len(allLines) {
			return ""
		}
		plain := StripAnsi(allLines[startLine])
		return ExtractVisibleRange(plain, startCol, endCol)
	}

	var buf strings.Builder
	for i := startLine; i <= endLine; i++ {
		if i < 0 || i >= len(allLines) {
			continue
		}
		plain := StripAnsi(allLines[i])
		switch i {
		case startLine:
			buf.WriteString(ExtractVisibleRange(plain, startCol, -1))
		case endLine:
			buf.WriteString(ExtractVisibleRange(plain, 0, endCol))
		default:
			buf.WriteString(plain)
		}
		if i < endLine {
			buf.WriteString("\n")
		}
	}
	return buf.String()
}

func RuneIdxToCol(runes []rune, idx int) int {
	if idx <= 0 {
		return 0
	}
	if idx >= len(runes) {
		idx = len(runes)
	}
	return VisibleWidth(string(runes[:idx]))
}

func ColToRuneIdx(runes []rune, col int) int {
	if col <= 0 || len(runes) == 0 {
		return 0
	}
	rest := string(runes)
	runeIdx := 0
	curCol := 0
	var state byte
	for len(rest) > 0 {
		cluster, cw, _, newState := ansi.DecodeSequence(rest, state, nil)
		if curCol+cw > col {
			return runeIdx
		}
		curCol += cw
		runeIdx += len([]rune(cluster))
		rest = rest[len(cluster):]
		state = newState
	}
	return runeIdx
}
