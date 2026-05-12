package widget

import (
	"strings"
	"time"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
)

type SubmitEvent struct {
	Text string
}

type TextInput struct {
	buf        [][]rune
	curLine    int
	curCol     int
	lastVisCol int
	selecting  bool
	selAnchor  int
	selEnd     int
	prompt     string
	theme      *Theme
	lastInputTime time.Time
	frame      int
}

func NewTextInput(t *Theme) TextInput {
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
			if m.curLine > 0 {
				m.curLine--
				if m.curCol > len(m.buf[m.curLine]) {
					m.curCol = len(m.buf[m.curLine])
				}
			}
		case tea.KeyDown:
			if m.curLine < len(m.buf)-1 {
				m.curLine++
				if m.curCol > len(m.buf[m.curLine]) {
					m.curCol = len(m.buf[m.curLine])
				}
			}
		case tea.KeyLeft:
			if m.curCol > 0 {
				m.curCol--
			} else if m.curLine > 0 {
				m.curLine--
				m.curCol = len(m.buf[m.curLine])
			}
		case tea.KeyRight:
			if m.curCol < len(m.buf[m.curLine]) {
				m.curCol++
			} else if m.curLine < len(m.buf)-1 {
				m.curLine++
				m.curCol = 0
			}
		case tea.KeyHome:
			m.curCol = 0
		case tea.KeyEnd:
			m.curCol = len(m.buf[m.curLine])
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

func (m TextInput) View(width int) string {
	if width < 1 {
		width = 1
	}
	textW := width - 4
	if textW < 1 {
		textW = 1
	}

	var allLines []string
	promptStyle := m.theme.Styles.InputPrompt
	textStyle := m.theme.Styles.InputText

	for li, line := range m.buf {
		segments := WordWrap(string(line), textW)
		for si, seg := range segments {
			var prefix string
			if li == 0 && si == 0 {
				prefix = " " + promptStyle.Render("❯")
			} else {
				prefix = "  "
			}

			segRunes := []rune(seg)
			isCursorSeg := li == m.curLine && si == segmentIdx(m.curLine, m.curCol, m.buf, textW)

			if isCursorSeg {
				segStart := FindSegmentStart(line, textW, si)
				cursorInSeg := m.curCol - segStart
				if cursorInSeg < 0 {
					cursorInSeg = 0
				}
				if cursorInSeg > len(segRunes) {
					cursorInSeg = len(segRunes)
				}

				left := string(segRunes[:cursorInSeg])
				var cursorChar string
				if cursorInSeg < len(segRunes) {
					cursorChar = BlockCursor(m.frame, m.theme.Colors.Accent)
					right := string(segRunes[cursorInSeg:])
					allLines = append(allLines, prefix+textStyle.Render(left)+cursorChar+textStyle.Render(right))
				} else {
					cursorChar = BlockCursor(m.frame, m.theme.Colors.Accent)
					allLines = append(allLines, prefix+textStyle.Render(left)+cursorChar)
				}
			} else {
				allLines = append(allLines, prefix+textStyle.Render(seg))
			}
		}
	}

	if len(allLines) == 0 {
		promptStyle := m.theme.Styles.InputPrompt
		cursorChar := BlockCursor(m.frame, m.theme.Colors.Accent)
		allLines = append(allLines, " "+promptStyle.Render("❯")+"  "+cursorChar)
	}

	return strings.Join(allLines, "\n")
}

func segmentIdx(line int, col int, buf [][]rune, textW int) int {
	if line >= len(buf) {
		return 0
	}
	runes := buf[line]
	s := string(runes)
	segments := WordWrap(s, textW)
	segStart := 0
	for si, seg := range segments {
		segLen := len([]rune(seg))
		if col >= segStart && col <= segStart+segLen {
			return si
		}
		segStart += segLen
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

func (m TextInput) CursorFlat() int {
	return m.lineColToFlat(m.curLine, m.curCol)
}

func (m TextInput) VisualLineCount() int {
	maxW := 80
	count := 0
	for _, l := range m.buf {
		segments := WordWrap(string(l), maxW)
		count += len(segments)
	}
	if count < 1 {
		count = 1
	}
	return count
}

func (m TextInput) Height() int {
	return m.VisualLineCount()
}

func (m TextInput) SelectionRange() (start, end int) {
	if m.selAnchor <= m.selEnd {
		return m.selAnchor, m.selEnd
	}
	return m.selEnd, m.selAnchor
}

func (m *TextInput) SetSelecting(selecting bool) {
	m.selecting = selecting
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
