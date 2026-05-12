package widget

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"crude/clipboard"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"
)

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
			} else {
				j++
			}
			i = j
		} else {
			rn, size := utf8.DecodeRuneInString(s[i:])
			if !unicode.IsSpace(rn) {
				lastNonSpaceEnd = i + size
			}
			i += size
		}
	}
	if lastNonSpaceEnd == 0 {
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
			} else {
				j++
			}
			i = j
		} else {
			buf.WriteByte(s[i])
			i++
		}
	}
	return buf.String()
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

func CopyToClipboardCmd(text string) tea.Cmd {
	return func() tea.Msg {
		if err := clipboard.WriteText(text); err != nil {
			return clipboardErrorMsg("failed to copy: " + err.Error())
		}
		return clipboardCopiedMsg{}
	}
}

type clipboardErrorMsg string
type clipboardCopiedMsg struct{}
