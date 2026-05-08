package tui

import (
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"crude/clipboard"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"
)

var selScrollInterval = 60 * time.Millisecond

func trimRightAnsiSpaces(s string) string {
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

func stripAnsi(s string) string {
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

func applySelectionReverse(line string, startCol, endCol int) string {
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

func extractVisibleRange(s string, startCol, endCol int) string {
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

func extractSelectedText(allLines []string, startLine, startCol, endLine, endCol int) string {
	if startLine == endLine {
		if startLine < 0 || startLine >= len(allLines) {
			return ""
		}
		plain := stripAnsi(allLines[startLine])
		return extractVisibleRange(plain, startCol, endCol)
	}

	var buf strings.Builder
	for i := startLine; i <= endLine; i++ {
		if i < 0 || i >= len(allLines) {
			continue
		}
		plain := stripAnsi(allLines[i])
		if i == startLine {
			buf.WriteString(extractVisibleRange(plain, startCol, -1))
		} else if i == endLine {
			buf.WriteString(extractVisibleRange(plain, 0, endCol))
		} else {
			buf.WriteString(plain)
		}
		if i < endLine {
			buf.WriteString("\n")
		}
	}
	return buf.String()
}

func copyToClipboardCmd(text string) tea.Cmd {
	return func() tea.Msg {
		if err := clipboard.WriteText(text); err != nil {
			return clipboardErrorMsg("failed to copy: " + err.Error())
		}
		return clipboardCopiedMsg{}
	}
}

func (m *Model) selectionRange() (startLine, startCol, endLine, endCol int) {
	if m.selAnchorLine < m.selEndLine ||
		(m.selAnchorLine == m.selEndLine && m.selAnchorCol <= m.selEndCol) {
		return m.selAnchorLine, m.selAnchorCol, m.selEndLine, m.selEndCol
	}
	return m.selEndLine, m.selEndCol, m.selAnchorLine, m.selAnchorCol
}

func (m *Model) mouseToAllLine(mouseY int) (allIdx int, ok bool) {
	if len(m.cachedAllLines) == 0 {
		return -1, false
	}
	chatH := m.chatAreaHeight()
	chatY := mouseY - 3
	if chatY < 0 || chatY >= chatH {
		return -1, false
	}

	total := len(m.cachedAllLines)
	maxOffset := total - chatH
	if maxOffset < 0 {
		maxOffset = 0
	}
	offset := m.chatOffset
	if offset > maxOffset {
		offset = maxOffset
	}
	endIdx := total - offset
	startIdx := endIdx - chatH
	if startIdx < 0 {
		startIdx = 0
	}

	visible := endIdx - startIdx
	emptyTop := chatH - visible
	if emptyTop < 0 {
		emptyTop = 0
	}

	if chatY < emptyTop {
		return -1, false
	}

	allIdx = startIdx + (chatY - emptyTop)
	if allIdx < 0 || allIdx >= total {
		return -1, false
	}
	return allIdx, true
}

func (m *Model) chatAreaContentBounds() (firstLine, lastLine int) {
	if len(m.cachedAllLines) == 0 {
		return -1, -1
	}
	chatH := m.chatAreaHeight()
	total := len(m.cachedAllLines)
	maxOffset := total - chatH
	if maxOffset < 0 {
		maxOffset = 0
	}
	offset := m.chatOffset
	if offset > maxOffset {
		offset = maxOffset
	}
	endIdx := total - offset
	startIdx := endIdx - chatH
	if startIdx < 0 {
		startIdx = 0
	}
	if endIdx > total {
		endIdx = total
	}
	return startIdx, endIdx - 1
}

func (m *Model) inputFirstLineY() int {
	y := m.chatAreaHeight() + 4
	if !m.settingsActive && !m.confirmForm.active && m.showHint && !m.thinking && m.inputLen() == 0 {
		y++
	}
	return y
}

func (m *Model) mouseToInputPos(mouseX, mouseY int) (runeIdx int, ok bool) {
	if m.thinking && !m.blocking {
		return -1, false
	}
	if m.settingsActive || m.confirmForm.active {
		return -1, false
	}

	firstY := m.inputFirstLineY()
	relY := mouseY - firstY
	if relY < 0 {
		return -1, false
	}

	textW := m.inputTextWidth()
	lines := m.inputLines()
	visIdx := 0
	for li, logLine := range lines {
		segments := hardWrap(string(logLine), textW)
		for si, seg := range segments {
			if visIdx == relY {
				col := mouseX - padLR - 2
				if col < 0 {
					col = 0
				}
				segW := runewidth.StringWidth(seg)
				if col > segW {
					col = segW
				}

				pos := 0
				for i := 0; i < li; i++ {
					pos += len(lines[i]) + 1
				}
				runesBefore := 0
				for j := 0; j < si; j++ {
					runesBefore += len([]rune(segments[j]))
				}
				colInRunes := 0
				colW := 0
				segRunes := []rune(seg)
				for colInRunes < len(segRunes) && colW < col {
					colW += runewidth.RuneWidth(segRunes[colInRunes])
					if colW > col {
						break
					}
					colInRunes++
				}
				pos += runesBefore + colInRunes
				return pos, true
			}
			visIdx++
		}
	}
	return -1, false
}

func (m *Model) inputSelectionRange() (start, end int) {
	if m.inputSelAnchor <= m.inputSelEnd {
		return m.inputSelAnchor, m.inputSelEnd
	}
	return m.inputSelEnd, m.inputSelAnchor
}

func (m *Model) inputSelVisibleRange(lineIdx int) (startCol, endCol int) {
	lines := m.inputLines()
	if lineIdx < 0 || lineIdx >= len(lines) {
		return -1, -1
	}

	lineStart := 0
	for i := 0; i < lineIdx; i++ {
		lineStart += len(lines[i]) + 1
	}
	lineLen := len(lines[lineIdx])

	selStart, selEnd := m.inputSelectionRange()

	lineSelStart := selStart
	if lineSelStart < lineStart {
		lineSelStart = lineStart
	}
	lineSelEnd := selEnd
	if lineSelEnd > lineStart+lineLen {
		lineSelEnd = lineStart + lineLen
	}

	if lineSelStart >= lineSelEnd {
		return -1, -1
	}

	contentOffset := padLR + 2

	startCol = contentOffset
	for j := 0; j < lineSelStart-lineStart; j++ {
		startCol += runewidth.RuneWidth(lines[lineIdx][j])
	}

	endCol = contentOffset
	for j := 0; j < lineSelEnd-lineStart; j++ {
		endCol += runewidth.RuneWidth(lines[lineIdx][j])
	}

	return startCol, endCol
}
