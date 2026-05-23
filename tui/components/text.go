package components

import (
	"strings"
	"time"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"crude/tui/rendering"
	"crude/tui/theme"
)

type SubmitEvent struct {
	Text string
}

type TextInput struct {
	buf           [][]rune
	curLine       int
	curCol        int
	lastVisCol    int
	selecting     bool
	selAnchor     int
	selEnd        int
	prompt        string
	theme         *theme.Theme
	lastInputTime time.Time
	frame         int
	textWidth     int
}

func NewTextInput(t *theme.Theme) TextInput {
	return TextInput{
		buf:   [][]rune{{}},
		theme: t,
	}
}

func (m TextInput) Update(msg tea.Msg, disabled bool) (TextInput, tea.Cmd) {
	if disabled {
		return m, nil
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyEnter:
			if msg.Alt {
				m.splitLineAt(m.curLine, m.curCol)
				m.curLine++
				m.curCol = 0
			} else {
				return m, func() tea.Msg {
					return SubmitEvent{Text: m.Value()}
				}
			}
		case tea.KeyBackspace:
			pos := m.lineColToFlat(m.curLine, m.curCol)
			if pos > 0 {
				m.deleteRuneAt(pos)
				m.curLine, m.curCol = m.flatToLineCol(pos - 1)
			}
		case tea.KeyDelete:
			pos := m.lineColToFlat(m.curLine, m.curCol)
			totalLen := m.Len()
			if pos < totalLen {
				m.deleteForwardAt(pos)
			}
		case tea.KeyUp:
			_, visCol := m.cursorVisualInfo()
			m.lastVisCol = visCol
			m.cursorUpVisual()
		case tea.KeyDown:
			_, visCol := m.cursorVisualInfo()
			m.lastVisCol = visCol
			m.cursorDownVisual()
		case tea.KeyLeft:
			if m.curCol > 0 {
				m.curCol--
			} else if m.curLine > 0 {
				m.curLine--
				m.curCol = len(m.buf[m.curLine])
			}
			m.lastVisCol = -1
		case tea.KeyRight:
			if m.curCol < len(m.buf[m.curLine]) {
				m.curCol++
			} else if m.curLine < len(m.buf)-1 {
				m.curLine++
				m.curCol = 0
			}
			m.lastVisCol = -1
		case tea.KeyHome:
			m.curCol = 0
			m.lastVisCol = -1
		case tea.KeyEnd:
			m.curCol = len(m.buf[m.curLine])
			m.lastVisCol = -1
		case tea.KeyCtrlLeft:
			m.setCursorFlat(m.cursorPrevWord())
			m.lastVisCol = -1
		case tea.KeyCtrlRight:
			m.setCursorFlat(m.cursorNextWord())
			m.lastVisCol = -1
		case tea.KeyCtrlA:
			m.curCol = 0
			m.lastVisCol = -1
		case tea.KeyCtrlE:
			m.curCol = len(m.buf[m.curLine])
			m.lastVisCol = -1
		case tea.KeyCtrlU:
			start := m.cursorLineStart()
			cf := m.lineColToFlat(m.curLine, m.curCol)
			if start < cf {
				m.deleteRange(start, cf)
				m.setCursorFlat(start)
			}
		case tea.KeyCtrlK:
			end := m.cursorLineEnd()
			cf := m.lineColToFlat(m.curLine, m.curCol)
			if end > cf {
				m.deleteRange(cf, end)
			}
		case tea.KeyCtrlW:
			if m.lineColToFlat(m.curLine, m.curCol) > 0 {
				pos := m.cursorPrevWord()
				cf := m.lineColToFlat(m.curLine, m.curCol)
				m.deleteRange(pos, cf)
				m.setCursorFlat(pos)
			}
		case tea.KeyCtrlHome:
			m.curLine = 0
			m.curCol = 0
			m.lastVisCol = -1
		case tea.KeyCtrlEnd:
			m.curLine = len(m.buf) - 1
			if m.curLine < 0 {
				m.curLine = 0
			}
			m.curCol = len(m.buf[m.curLine])
			m.lastVisCol = -1
		default:
			for _, r := range msg.Runes {
				if unicode.IsPrint(r) {
					pos := m.lineColToFlat(m.curLine, m.curCol)
					m.insertRuneAt(pos, r)
					m.curCol++
				}
			}
		}
		m.lastInputTime = time.Now()
		m.frame++
	}

	return m, nil
}

func (m TextInput) View(width int, spinFrame int, cursorSolidAlways bool) string {
	if width < 1 {
		width = 1
	}
	padLR := m.theme.Spacing.PadLR
	textW := width - padLR*2 - 2
	if textW < 1 {
		textW = 1
	}
	m.textWidth = textW

	selStart, selEnd := -1, -1
	if m.selecting {
		selStart, selEnd = m.SelectionRange()
	}

	var allLines []string
	promptStyle := m.theme.Styles.InputPrompt
	textStyle := m.theme.Styles.InputText

	flatOffset := 0
	for li, line := range m.buf {
		segments := rendering.WordWrap(string(line), textW)
		for si, seg := range segments {
			var prefix string
			if li == 0 && si == 0 {
				prefix = promptStyle.Render(m.promptChar()) + " "
			} else {
				prefix = "  "
			}

			segRunes := []rune(seg)
			segStart := rendering.FindSegmentStart(line, textW, si)
			segFlatStart := flatOffset + segStart
			segFlatEnd := segFlatStart + len(segRunes)

			isCursorSeg := li == m.curLine && si == segmentIdx(m.curLine, m.curCol, m.buf, textW)

			if isCursorSeg {
				cursorInSeg := m.curCol - segStart
				if cursorInSeg < 0 {
					cursorInSeg = 0
				}
				if cursorInSeg > len(segRunes) {
					cursorInSeg = len(segRunes)
				}

				left := string(segRunes[:cursorInSeg])
				var cursorChar string
				cursorSolid := cursorSolidAlways || time.Since(m.lastInputTime) < 800*time.Millisecond
				if cursorInSeg < len(segRunes) {
					right := string(segRunes[cursorInSeg+1:])
					if cursorSolid {
						cursorChar = lipgloss.NewStyle().Reverse(true).Render(string(segRunes[cursorInSeg]))
					} else if BlinkOnTime(time.Now()) {
						cursorChar = lipgloss.NewStyle().
							Background(m.theme.Colors.Accent).
							Foreground(m.theme.Colors.Bg).
							Render(string(segRunes[cursorInSeg]))
					} else {
						cursorChar = textStyle.Render(string(segRunes[cursorInSeg]))
					}
					rendered := textStyle.Render(left) + cursorChar + textStyle.Render(right)
					if selStart >= 0 && selStart != selEnd && segFlatStart < selEnd && segFlatEnd > selStart {
						rendered = m.applySelectionToRendered(rendered, segFlatStart, segFlatEnd, selStart, selEnd)
					}
					allLines = append(allLines, prefix+rendered)
				} else {
					if cursorSolid {
						cursorChar = lipgloss.NewStyle().Foreground(m.theme.Colors.Accent).Render("█")
					} else {
						cursorChar = BlockCursorTime(m.theme.Colors.Accent)
					}
					rendered := textStyle.Render(left) + cursorChar
					if selStart >= 0 && selStart != selEnd && segFlatStart < selEnd && segFlatEnd > selStart {
						rendered = m.applySelectionToRendered(rendered, segFlatStart, segFlatEnd, selStart, selEnd)
					}
					allLines = append(allLines, prefix+rendered)
				}
			} else {
				rendered := textStyle.Render(seg)
				if selStart >= 0 && selStart != selEnd && segFlatStart < selEnd && segFlatEnd > selStart {
					rendered = m.applySelectionToRendered(rendered, segFlatStart, segFlatEnd, selStart, selEnd)
				}
				allLines = append(allLines, prefix+rendered)
			}
		}
		flatOffset += len(line) + 1
	}

	if len(allLines) == 0 {
		promptStyle := m.theme.Styles.InputPrompt
		cursorSolid := cursorSolidAlways || time.Since(m.lastInputTime) < 800*time.Millisecond
		var cursorChar string
		if cursorSolid {
			cursorChar = lipgloss.NewStyle().Foreground(m.theme.Colors.Accent).Render("█")
		} else {
			cursorChar = BlockCursorTime(m.theme.Colors.Accent)
		}
		allLines = append(allLines, promptStyle.Render(m.promptChar())+" "+cursorChar)
	}

	return strings.Join(allLines, "\n")
}

func (m TextInput) applySelectionToRendered(line string, segFlatStart, segFlatEnd, selStart, selEnd int) string {
	relStart := selStart - segFlatStart
	if relStart < 0 {
		relStart = 0
	}
	relEnd := selEnd - segFlatStart
	if relEnd > segFlatEnd-segFlatStart {
		relEnd = segFlatEnd - segFlatStart
	}
	if relStart >= relEnd {
		return line
	}

	colStart := m.runeIdxToCol(line, relStart)
	colEnd := m.runeIdxToCol(line, relEnd)
	return rendering.ApplySelection(line, colStart, colEnd)
}

func (m TextInput) runeIdxToCol(line string, runeIdx int) int {
	plain := rendering.StripAnsi(line)
	runes := []rune(plain)
	return rendering.RuneIdxToCol(runes, runeIdx)
}

func segmentIdx(line int, col int, buf [][]rune, textW int) int {
	if line >= len(buf) {
		return 0
	}
	runes := buf[line]
	segments := rendering.WordWrap(string(runes), textW)
	for si := range segments {
		segStart := rendering.FindSegmentStart(runes, textW, si)
		segEnd := segStart + len([]rune(segments[si]))
		if col <= segEnd || si == len(segments)-1 {
			return si
		}
	}
	return 0
}

func (m TextInput) Value() string {
	var parts []string
	for _, line := range m.buf {
		parts = append(parts, string(line))
	}
	return strings.Join(parts, "\n")
}

func (m *TextInput) SetValue(s string) {
	lines := strings.Split(s, "\n")
	m.buf = make([][]rune, len(lines))
	for i, l := range lines {
		m.buf[i] = []rune(l)
	}
	if len(m.buf) == 0 {
		m.buf = [][]rune{{}}
	}
	m.curLine = len(m.buf) - 1
	if m.curLine < 0 {
		m.curLine = 0
	}
	m.curCol = len(m.buf[m.curLine])
}

func (m *TextInput) Clear() {
	m.buf = [][]rune{{}}
	m.curLine = 0
	m.curCol = 0
	m.selecting = false
	m.selAnchor = 0
	m.selEnd = 0
}

func (m TextInput) Len() int {
	n := 0
	for _, line := range m.buf {
		n += len(line)
	}
	if len(m.buf) > 0 {
		n += len(m.buf) - 1
	}
	return n
}

func (m TextInput) cursorPrevWord() int {
	pos := m.lineColToFlat(m.curLine, m.curCol)
	if pos <= 0 {
		return 0
	}
	buf := []rune(m.Value())
	pos--
	for pos > 0 && buf[pos] == ' ' {
		pos--
	}
	for pos > 0 && buf[pos-1] != ' ' {
		pos--
	}
	return pos
}

func (m TextInput) cursorNextWord() int {
	pos := m.lineColToFlat(m.curLine, m.curCol)
	buf := []rune(m.Value())
	total := len(buf)
	if pos >= total {
		return total
	}
	for pos < total && buf[pos] == ' ' {
		pos++
	}
	for pos < total && buf[pos] != ' ' {
		pos++
	}
	return pos
}

func (m TextInput) cursorLineStart() int {
	return m.lineColToFlat(m.curLine, 0)
}

func (m TextInput) cursorLineEnd() int {
	return m.lineColToFlat(m.curLine, len(m.buf[m.curLine]))
}

func (m *TextInput) deleteRange(start, end int) {
	if start == end || start > end {
		start, end = end, start
	}
	if start < 0 {
		start = 0
	}
	totalLen := m.Len()
	if end > totalLen {
		end = totalLen
	}
	if start >= end {
		return
	}

	// Convert to line/col
	sLine, sCol := m.flatToLineCol(start)
	eLine, eCol := m.flatToLineCol(end)

	if sLine == eLine {
		newLine := make([]rune, len(m.buf[sLine])-(eCol-sCol))
		copy(newLine, m.buf[sLine][:sCol])
		copy(newLine[sCol:], m.buf[sLine][eCol:])
		m.buf[sLine] = newLine
	} else {
		// First and last lines
		firstPart := m.buf[sLine][:sCol]
		lastPart := m.buf[eLine][eCol:]

		// Merge first+last, removing middle lines
		merged := make([]rune, len(firstPart)+len(lastPart))
		copy(merged, firstPart)
		copy(merged[len(firstPart):], lastPart)

		newBuf := make([][]rune, 0, len(m.buf)-(eLine-sLine))
		newBuf = append(newBuf, m.buf[:sLine]...)
		newBuf = append(newBuf, merged)
		newBuf = append(newBuf, m.buf[eLine+1:]...)
		m.buf = newBuf
	}
}

func (m TextInput) CursorFlat() int {
	return m.lineColToFlat(m.curLine, m.curCol)
}

func (m TextInput) CurLine() int {
	return m.curLine
}

func (m TextInput) CurCol() int {
	return m.curCol
}

func (m TextInput) Lines() [][]rune {
	return m.buf
}

func (m *TextInput) CursorVisualLine() int {
	visLine, _ := m.cursorVisualInfo()
	return visLine
}

func (m *TextInput) TotalVisualLines() int {
	return m.visualLineCount()
}

func (m TextInput) VisualLineCount(textW int) int {
	if textW < 1 {
		textW = 1
	}
	count := 0
	for _, l := range m.buf {
		segments := rendering.WordWrap(string(l), textW)
		count += len(segments)
	}
	if count < 1 {
		count = 1
	}
	return count
}

func (m TextInput) Height(textW int) int {
	return m.VisualLineCount(textW)
}

func (m TextInput) SelectionRange() (start, end int) {
	if m.selAnchor <= m.selEnd {
		return m.selAnchor, m.selEnd
	}
	return m.selEnd, m.selAnchor
}

func (m TextInput) HasActiveSelection() bool {
	if !m.selecting {
		return false
	}
	s, e := m.SelectionRange()
	return s != e
}

func (m TextInput) IsSelecting() bool {
	return m.selecting
}

func (m *TextInput) SetSelection(anchor, end int) {
	m.selecting = true
	m.selAnchor = anchor
	m.selEnd = end
}

func (m *TextInput) UpdateSelectionEnd(end int) {
	m.selEnd = end
}

func (m *TextInput) ClearSelection() {
	m.selecting = false
	m.selAnchor = 0
	m.selEnd = 0
}

func (m TextInput) SelectedText() string {
	if !m.selecting {
		return ""
	}
	s, e := m.SelectionRange()
	runes := []rune(m.Value())
	if e > len(runes) {
		e = len(runes)
	}
	if s >= e {
		return ""
	}
	return string(runes[s:e])
}

func (m *TextInput) SetTextWidth(w int) {
	m.textWidth = w
}

func (m *TextInput) SetPrompt(p string) {
	m.prompt = p
}

func (m TextInput) promptChar() string {
	if m.prompt != "" {
		return m.prompt
	}
	return "❯"
}

func (m *TextInput) SetSelecting(selecting bool) {
	m.selecting = selecting
}

func (m TextInput) FlatToLineCol(pos int) (line, col int) {
	return m.flatToLineCol(pos)
}

func (m *TextInput) SetCursorFlat(pos int) {
	m.setCursorFlat(pos)
}

func (m TextInput) flatToLineCol(pos int) (line, col int) {
	if pos <= 0 {
		return 0, 0
	}
	remaining := pos
	for li, lineRunes := range m.buf {
		if remaining <= len(lineRunes) {
			return li, remaining
		}
		remaining -= len(lineRunes) + 1
	}
	if len(m.buf) == 0 {
		return 0, 0
	}
	last := len(m.buf) - 1
	return last, len(m.buf[last])
}

func (m TextInput) lineColToFlat(line, col int) int {
	if line < 0 {
		line = 0
	}
	if line >= len(m.buf) {
		line = len(m.buf) - 1
	}
	pos := 0
	for i := 0; i < line; i++ {
		pos += len(m.buf[i]) + 1
	}
	if col > len(m.buf[line]) {
		col = len(m.buf[line])
	}
	return pos + col
}

func (m *TextInput) insertRuneAt(pos int, r rune) {
	if len(m.buf) == 0 {
		m.buf = [][]rune{{r}}
		return
	}
	line, col := m.flatToLineCol(pos)
	if line >= len(m.buf) {
		line = len(m.buf) - 1
	}
	newLine := make([]rune, len(m.buf[line])+1)
	copy(newLine, m.buf[line][:col])
	newLine[col] = r
	copy(newLine[col+1:], m.buf[line][col:])
	m.buf[line] = newLine
}

func (m *TextInput) deleteRuneAt(pos int) {
	if pos <= 0 {
		return
	}
	line, col := m.flatToLineCol(pos)
	if col > 0 {
		newLine := make([]rune, len(m.buf[line])-1)
		copy(newLine, m.buf[line][:col-1])
		copy(newLine[col-1:], m.buf[line][col:])
		m.buf[line] = newLine
	} else if line > 0 {
		prevLen := len(m.buf[line-1])
		merged := make([]rune, prevLen+len(m.buf[line]))
		copy(merged, m.buf[line-1])
		copy(merged[prevLen:], m.buf[line])
		m.buf = append(m.buf[:line-1], m.buf[line:]...)
		m.buf[line-1] = merged
	}
}

func (m *TextInput) deleteForwardAt(pos int) {
	line, col := m.flatToLineCol(pos)
	if line >= len(m.buf) {
		return
	}
	lineLen := len(m.buf[line])
	if col < lineLen {
		newLine := make([]rune, lineLen-1)
		copy(newLine, m.buf[line][:col])
		copy(newLine[col:], m.buf[line][col+1:])
		m.buf[line] = newLine
	} else if line+1 < len(m.buf) {
		merged := make([]rune, lineLen+len(m.buf[line+1]))
		copy(merged, m.buf[line])
		copy(merged[lineLen:], m.buf[line+1])
		m.buf = append(m.buf[:line], m.buf[line+1:]...)
		m.buf[line] = merged
	}
}

func (m *TextInput) setCursorFlat(pos int) {
	m.curLine, m.curCol = m.flatToLineCol(pos)
}

func (m *TextInput) splitLineAt(line, col int) {
	if line < 0 || line >= len(m.buf) {
		return
	}
	runes := m.buf[line]
	left := make([]rune, col)
	copy(left, runes[:col])
	right := make([]rune, len(runes)-col)
	copy(right, runes[col:])

	newLines := make([][]rune, len(m.buf)+1)
	copy(newLines, m.buf[:line])
	newLines[line] = left
	newLines[line+1] = right
	copy(newLines[line+2:], m.buf[line+1:])
	m.buf = newLines
}

func (m *TextInput) visualLineCount() int {
	if m.textWidth < 1 {
		return len(m.buf)
	}
	count := 0
	for _, l := range m.buf {
		segs := rendering.WordWrap(string(l), m.textWidth)
		count += len(segs)
	}
	if count < 1 {
		count = 1
	}
	return count
}

// cursorVisualInfo returns the visual line index and the column offset within
// the visual line for the cursor's current position.
func (m *TextInput) cursorVisualInfo() (visualLine, visualCol int) {
	if m.textWidth < 1 {
		return m.curLine, m.curCol
	}
	lines := m.buf
	vis := 0
	for li := 0; li < m.curLine; li++ {
		segs := rendering.WordWrap(string(lines[li]), m.textWidth)
		vis += len(segs)
	}

	segs := rendering.WordWrap(string(lines[m.curLine]), m.textWidth)
	for si := range segs {
		segStart := rendering.FindSegmentStart(lines[m.curLine], m.textWidth, si)
		segEnd := segStart + len([]rune(segs[si]))
		if m.curCol <= segEnd || si == len(segs)-1 {
			colW := rendering.RuneIdxToCol(lines[m.curLine][segStart:m.curCol], m.curCol-segStart)
			return vis + si, colW
		}
	}
	last := len(segs) - 1
	segStart := rendering.FindSegmentStart(lines[m.curLine], m.textWidth, last)
	colW := rendering.RuneIdxToCol(lines[m.curLine][segStart:], len(lines[m.curLine])-segStart)
	return vis + last, colW
}

func (m *TextInput) cursorUpVisual() {
	_, visCol := m.cursorVisualInfo()
	targetVisCol := m.lastVisCol
	if targetVisCol < 0 {
		targetVisCol = visCol
	}
	visLine, _ := m.cursorVisualInfo()
	if visLine <= 0 {
		m.curCol = 0
		m.lastVisCol = -1
		return
	}
	m.setCursorFromVisualLine(visLine-1, targetVisCol)
}

func (m *TextInput) cursorDownVisual() {
	_, visCol := m.cursorVisualInfo()
	targetVisCol := m.lastVisCol
	if targetVisCol < 0 {
		targetVisCol = visCol
	}
	visLine, _ := m.cursorVisualInfo()
	total := m.visualLineCount()
	if visLine >= total-1 {
		last := len(m.buf) - 1
		if last < 0 {
			last = 0
		}
		m.curCol = len(m.buf[last])
		m.lastVisCol = -1
		return
	}
	m.setCursorFromVisualLine(visLine+1, targetVisCol)
}

// setCursorFromVisualLine sets the cursor position to the given visual line,
// preserving the target visual column as closely as possible.
func (m *TextInput) setCursorFromVisualLine(targetVisLine, targetVisCol int) {
	if m.textWidth < 1 {
		if targetVisLine < len(m.buf) {
			m.curLine = targetVisLine
			if targetVisCol < len(m.buf[targetVisLine]) {
				m.curCol = targetVisCol
			} else {
				m.curCol = len(m.buf[targetVisLine])
			}
		}
		return
	}
	lines := m.buf
	vis := 0
	for li, logLine := range lines {
		segs := rendering.WordWrap(string(logLine), m.textWidth)
		for si := range segs {
			if vis == targetVisLine {
				m.curLine = li
				segStart := rendering.FindSegmentStart(logLine, m.textWidth, si)
				segRunes := []rune(segs[si])
				segRuneIdx := rendering.ColToRuneIdx(segRunes, targetVisCol)
				m.curCol = segStart + segRuneIdx
				if m.curCol > len(lines[li]) {
					m.curCol = len(lines[li])
				}
				return
			}
			vis++
		}
	}
	last := len(lines) - 1
	if last < 0 {
		last = 0
	}
	m.curLine = last
	m.curCol = len(lines[last])
}
