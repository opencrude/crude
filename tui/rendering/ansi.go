package rendering

import (
	"encoding/hex"
	"strings"
	"sync/atomic"
	"unicode"
	"unicode/utf8"

	"github.com/mattn/go-runewidth"
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

// VisibleWidth returns the terminal cell width of s, stripping ANSI escape
// sequences and measuring with runewidth. This is the correct way to measure
// styled strings for screen-space padding — lipgloss.Width undercounts wide
// Unicode characters (emojis, │, ●, etc.) by 1 cell each.
func VisibleWidth(s string) int {
	return runewidth.StringWidth(StripAnsi(s))
}

func ApplySelection(line string, startCol, endCol int) string {
	if startCol >= endCol || startCol < 0 {
		return line
	}
	var buf strings.Builder
	col := 0
	i := 0
	inSelect := false

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

		r, size := utf8.DecodeRuneInString(line[i:])
		w := runewidth.RuneWidth(r)

		shouldSelect := col >= startCol && col < endCol

		if shouldSelect && !inSelect {
			buf.WriteString("\x1b[7m")
			inSelect = true
		} else if !shouldSelect && inSelect {
			buf.WriteString("\x1b[27m")
			inSelect = false
		}

		buf.WriteString(line[i : i+size])
		col += w
		i += size
	}

	if inSelect {
		buf.WriteString("\x1b[27m")
	}

	return buf.String()
}

func ExtractVisibleRange(s string, startCol, endCol int) string {
	var buf strings.Builder
	col := 0
	for _, r := range s {
		w := runewidth.RuneWidth(r)
		if endCol >= 0 && col >= endCol {
			break
		}
		if col+w > startCol {
			buf.WriteRune(r)
		}
		col += w
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
		if i == startLine {
			buf.WriteString(ExtractVisibleRange(plain, startCol, -1))
		} else if i == endLine {
			buf.WriteString(ExtractVisibleRange(plain, 0, endCol))
		} else {
			buf.WriteString(plain)
		}
		if i < endLine {
			buf.WriteString("\n")
		}
	}
	return buf.String()
}
