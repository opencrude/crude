package editor

import (
	"image/color"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/scrollbar"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/widget"
)

type ActionType int

const (
	ActInsert ActionType = iota
	ActDelete
)

type Change struct {
	Type         ActionType
	Text         []byte
	StartPos     Position
	EndPos       Position
	CursorBefore Position
}

type Editor struct {
	th       *theme.Theme
	buf      *PieceTable
	filePath string
	title    string

	cursor  Position
	scrollX int
	scrollY int

	goalCol int
	goalSet bool

	viewW      int
	viewH      int
	scrollbar  scrollbar.ScrollBar
	sbTrackH   int

	modified bool

	changes     []Change
	changeIndex int
	maxHistory  int

	open bool

	cursorVisible bool
	lastActivity  time.Time
	lastToggle    time.Time
	blinkActive   bool
}

const (
	cursorBlinkInterval = 530 * time.Millisecond
	tickInterval        = 100 * time.Millisecond
	scrollMargin        = 3
)

type CursorBlinkMsg time.Time

func New(th *theme.Theme) *Editor {
	return &Editor{
		th:            th,
		buf:           NewPieceTable(),
		scrollbar:     scrollbar.New(0),
		maxHistory:    500,
		cursorVisible: true,
	}
}

func (e *Editor) Open(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	e.filePath = abs
	e.title = filepath.Base(abs)

	buf := NewPieceTable()
	err = buf.Load(abs)
	if err != nil {
		return err
	}
	e.buf = buf

	e.cursor = Position{Line: 0, Col: 0}
	e.scrollX = 0
	e.scrollY = 0
	e.modified = false
	e.changes = nil
	e.changeIndex = 0
	e.scrollbar = scrollbar.New(0)
	e.open = true
	e.cursorVisible = true
	e.blinkActive = false
	e.lastActivity = time.Now()

	return nil
}

func (e *Editor) Close() {
	e.open = false
	e.filePath = ""
	e.title = ""
}

func (e *Editor) IsOpen() bool   { return e.open }
func (e *Editor) FilePath() string { return e.filePath }
func (e *Editor) Title() string {
	if e.modified {
		return e.title + " ●"
	}
	return e.title
}
func (e *Editor) Modified() bool { return e.modified }
func (e *Editor) Value() string   { return string(e.buf.Bytes()) }
func (e *Editor) CursorLine() int  { return e.cursor.Line }
func (e *Editor) CursorCol() int   { return e.cursor.Col }
func (e *Editor) LineCount() int   { return e.buf.LineCount() }

func (e *Editor) Measure(availW, availH int) (int, int) {
	return availW, availH
}

func (e *Editor) Render(surf *surface.Surface, ctx *widget.RenderContext) {
	if !e.open {
		return
	}
	w, h := surf.Width(), surf.Height()
	e.viewW = w
	e.viewH = h
	if w < 10 || h < 4 {
		return
	}

	bgColor := e.cellColor(e.th.Colors.Bg)
	blankCell := cell.NewCell(' ', bgColor, bgColor, 0)
	sw := e.gutterWidth()
	textW := w - sw
	if textW < 1 {
		textW = 1
	}
	bodyH := h - 1

	buf := surf.Buffer()
	for y := 0; y < bodyH; y++ {
		bufY := y
		if bufY >= h {
			break
		}

		lineIdx := e.scrollY + y
		var lineStr string
		if lineIdx >= 0 && lineIdx < e.buf.LineCount() {
			b, err := e.buf.Line(lineIdx)
			if err == nil {
				lineStr = string(b)
			}
		}

		for x := 0; x < w; x++ {
			if x < sw-1 {
				numW := sw - 1
				numStr := e.formatLineNum(lineIdx+1, numW)
				numEnd := len(numStr)
				if x >= 0 && x < numEnd {
					ci := x
					if ci < len(numStr) {
						buf.Set(x, bufY, cell.NewCell(rune(numStr[ci]), e.cellColor(e.th.Colors.Muted), e.cellColor(e.th.Colors.Bg), 0))
					}
				} else {
					buf.Set(x, bufY, blankCell)
				}
			} else if x == sw-1 {
				buf.Set(x, bufY, cell.NewCell('│', e.cellColor(e.th.Colors.Border), bgColor, 0))
			} else {
				textX := x - sw
				vCol := e.scrollX + textX
				runes := []rune(lineStr)

				if vCol >= 0 && vCol < len(runes) {
					ch := runes[vCol]
					isCursor := ctx.Focused && lineIdx == e.cursor.Line && vCol == e.cursor.Col
					if isCursor {
						buf.Set(x, bufY, cell.NewCell(ch, e.cellColor(e.th.Colors.Bg), e.cellColor(e.th.Colors.Accent), cell.AttrBold))
					} else {
						buf.Set(x, bufY, cell.NewCell(ch, e.cellColor(e.th.Colors.Fg), bgColor, 0))
					}
				} else if vCol >= 0 && vCol == len(runes) && ctx.Focused && lineIdx == e.cursor.Line && vCol == e.cursor.Col {
					buf.Set(x, bufY, cell.NewCell(' ', e.cellColor(e.th.Colors.Bg), e.cellColor(e.th.Colors.Accent), cell.AttrBold))
				} else {
					buf.Set(x, bufY, blankCell)
				}
			}
		}
	}

	statusY := h - 1
	if statusY >= 0 {
		statusStr := e.renderStatusLine(w)
		for x := 0; x < w; x++ {
			if x < len(statusStr) {
				buf.Set(x, statusY, cell.NewCell(rune(statusStr[x]), e.cellColor(e.th.Colors.Muted), e.cellColor(e.th.Colors.Border), 0))
			} else {
				buf.Set(x, statusY, blankCell)
			}
		}
	}
}

func (e *Editor) View(width, height int, padLeft, padRight int) []string {
	if !e.open || width < 10 || height < 4 {
		return nil
	}
	e.viewW = width - padLeft - padRight
	e.viewH = height

	sw := e.gutterWidth()
	sbW := scrollbar.ScrollbarWidth
	textW := e.viewW - sw - sbW
	if textW < 1 {
		textW = 1
	}
	bodyH := height

	// Update scrollbar
	lc := e.buf.LineCount()
	e.sbTrackH = bodyH
	e.scrollbar.SetTrackHeight(bodyH)
	maxScroll := lc - bodyH
	if maxScroll < 0 {
		maxScroll = 0
	}
	e.scrollbar.SetScrollInfo(lc, bodyH, e.scrollY)
	sbView := e.scrollbar.View()
	sbLines := strings.Split(sbView, "\n")

	lines := make([]string, 0, bodyH)

	padL := strings.Repeat(" ", padLeft)
	padR := strings.Repeat(" ", padRight)
	gutterStyle := lipgloss.NewStyle().Foreground(e.th.Colors.Muted)
	sepStyle := lipgloss.NewStyle().Foreground(e.th.Colors.Border)
	fgStyle := lipgloss.NewStyle().Foreground(e.th.Colors.Fg)
	showCursor := e.cursorVisible
	cursorFg := lipgloss.NewStyle().Foreground(e.th.Colors.Bg).Background(e.th.Colors.Accent).Bold(true)
	blankStyle := lipgloss.NewStyle().Foreground(e.th.Colors.Muted)
	sbBgStyle := lipgloss.NewStyle().Foreground(e.th.Colors.Border)

	for y := 0; y < bodyH; y++ {
		lineIdx := e.scrollY + y
		var lineStr string
		if lineIdx >= 0 && lineIdx < e.buf.LineCount() {
			b, err := e.buf.Line(lineIdx)
			if err == nil {
				lineStr = string(b)
			}
		}

		numStr := e.formatLineNum(lineIdx+1, sw-1)
		numPadded := strings.Repeat(" ", sw-1-len(numStr)) + numStr
		gutter := gutterStyle.Render(numPadded)
		sep := sepStyle.Render("│")

		isCursorLine := lineIdx == e.cursor.Line && showCursor

		var textPart string
		runes := []rune(lineStr)
		vCol := e.scrollX
		for i := 0; i < textW; i++ {
			ci := vCol + i
			if ci >= 0 && ci < len(runes) {
				ch := string(runes[ci])
				if ci == e.cursor.Col && isCursorLine {
					textPart += cursorFg.Render(ch)
				} else {
					textPart += fgStyle.Render(ch)
				}
			} else if ci == e.cursor.Col && isCursorLine {
				textPart += cursorFg.Render(" ")
			} else {
				textPart += blankStyle.Render(" ")
			}
		}

		sbChar := " "
		if y < len(sbLines) && sbLines[y] != "" {
			sbChar = sbLines[y]
		}
		renderedSB := sbBgStyle.Render(sbChar)

		innerLine := gutter + sep + textPart + renderedSB
		innerVisW := rendering.VisibleWidth(innerLine)
		rightPad := e.viewW - innerVisW
		if rightPad < 0 {
			rightPad = 0
		}
		line := padL + innerLine + strings.Repeat(" ", rightPad) + padR
		lines = append(lines, line)
	}

	return lines
}

func (e *Editor) gutterWidth() int {
	lineCount := e.buf.LineCount()
	digits := 1
	for n := lineCount; n >= 10; n /= 10 {
		digits++
	}
	if digits < 2 {
		digits = 2
	}
	return digits + 1
}

func (e *Editor) formatLineNum(n int, width int) string {
	s := e.itoa(n)
	if len(s) > width {
		s = s[len(s)-width:]
	}
	return s
}

func (e *Editor) itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}

func (e *Editor) renderStatusLine(w int) string {
	lc := e.buf.LineCount()
	left := e.title
	if e.modified {
		left += " ●"
	}
	right := e.itoa(e.cursor.Line+1) + ":" + e.itoa(e.cursor.Col+1) + "  " + e.itoa(lc) + "L"
	padding := w - len(left) - len(right) - 2
	if padding < 1 {
		padding = 1
	}
	return " " + left + strings.Repeat(" ", padding) + right + " "
}

func (e *Editor) cellColor(c color.Color) cell.Color {
	return rendering.ThemeColorToCell(c)
}

func (e *Editor) HandleEvent(msg tea.Msg) tea.Cmd {
	if !e.open {
		return nil
	}

	switch msg := msg.(type) {
	case CursorBlinkMsg:
		idle := time.Since(e.lastActivity)
		if idle < cursorBlinkInterval {
			e.blinkActive = false
			e.cursorVisible = true
		} else if !e.blinkActive {
			e.blinkActive = true
			e.cursorVisible = false
			e.lastToggle = time.Now()
		} else if time.Since(e.lastToggle) >= cursorBlinkInterval {
			e.cursorVisible = !e.cursorVisible
			e.lastToggle = time.Now()
		}
		return e.blinkCmd()
	case tea.KeyPressMsg:
		e.cursorVisible = true
		e.blinkActive = false
		e.lastActivity = time.Now()
		return e.handleKey(msg)
	case tea.MouseWheelMsg:
		e.cursorVisible = true
		e.blinkActive = false
		e.lastActivity = time.Now()
		e.handleMouseWheel(msg)
	case tea.MouseClickMsg:
		e.cursorVisible = true
		e.blinkActive = false
		e.lastActivity = time.Now()
		e.handleMouseClick(msg)
	case tea.MouseMotionMsg:
		sb, cmd := e.scrollbar.Update(msg)
		e.scrollbar = sb
		if cmd != nil && sb.GetScrollY() != e.scrollY {
			e.scrollY = sb.GetScrollY()
		}
	case tea.MouseReleaseMsg:
		sb, cmd := e.scrollbar.Update(msg)
		e.scrollbar = sb
		if sb.GetScrollY() != e.scrollY {
			e.scrollY = sb.GetScrollY()
		}
		if cmd != nil {
			return cmd
		}
	}
	return nil
}

func (e *Editor) handleMouseWheel(msg tea.MouseWheelMsg) {
	scrollLines := 3
	bodyH := e.viewH
	if bodyH < 1 {
		bodyH = 1
	}
	switch msg.Button {
	case tea.MouseWheelUp:
		e.scrollY -= scrollLines
		if e.scrollY < 0 {
			e.scrollY = 0
		}
	case tea.MouseWheelDown:
		e.scrollY += scrollLines
		maxScroll := e.buf.LineCount() - bodyH
		if maxScroll < 0 {
			maxScroll = 0
		}
		if e.scrollY > maxScroll {
			e.scrollY = maxScroll
		}
	}
	lc := e.buf.LineCount()
	e.scrollbar.SetTrackHeight(bodyH)
	e.scrollbar.SetScrollInfo(lc, bodyH, e.scrollY)
}

func (e *Editor) handleMouseClick(msg tea.MouseClickMsg) {
	if msg.Button != tea.MouseLeft {
		return
	}
	e.clearGoal()
	mouse := msg.Mouse()
	sw := e.gutterWidth()
	sbW := scrollbar.ScrollbarWidth
	textW := e.viewW - sw - sbW
	if textW < 1 {
		textW = 1
	}

	sbX := sw + textW
	if mouse.X >= sbX {
		sb, cmd := e.scrollbar.Update(msg)
		e.scrollbar = sb
		if cmd != nil {
			if sb.GetScrollY() != e.scrollY {
				e.scrollY = sb.GetScrollY()
			}
		}
		return
	}

	lineIdx := e.scrollY + mouse.Y
	if lineIdx >= e.buf.LineCount() {
		lineIdx = e.buf.LineCount() - 1
	}
	if lineIdx < 0 {
		lineIdx = 0
	}

	col := e.scrollX + mouse.X - sw
	if col < 0 {
		col = 0
	}
	b, err := e.buf.Line(lineIdx)
	if err == nil {
		lineLen := utf8.RuneCount(b)
		if col > lineLen {
			col = lineLen
		}
	}

	e.cursor = Position{Line: lineIdx, Col: col}
	e.ensureCursorVisible()
}

func (e *Editor) handleKey(key tea.KeyPressMsg) tea.Cmd {
	switch key.Code {
	case tea.KeyUp:
		e.moveUp()
	case tea.KeyDown:
		e.moveDown()
	case tea.KeyLeft:
		e.moveLeft()
	case tea.KeyRight:
		e.moveRight()
	case tea.KeyHome:
		e.moveToLineStart()
	case tea.KeyEnd:
		e.moveToLineEnd()
	case tea.KeyPgUp:
		e.pageUp()
	case tea.KeyPgDown:
		e.pageDown()
	case tea.KeyBackspace:
		e.deleteBackward()
	case tea.KeyDelete:
		e.deleteForward()
	case tea.KeyEnter:
		e.insertNewline()
	case tea.KeyTab:
		e.insertTab()
	case tea.KeyEsc:
		return e.closeCmd()
	default:
		switch key.String() {
		case "ctrl+s":
			e.save()
		case "ctrl+z":
			e.undo()
		case "ctrl+y":
			e.redo()
		default:
			if len(key.Text) > 0 {
				for _, r := range key.Text {
					if r >= 32 {
						e.insertRune(r)
					}
				}
			}
		}
	}
	return nil
}

func (e *Editor) closeCmd() tea.Cmd {
	return func() tea.Msg {
		return EditorCloseMsg{FilePath: e.filePath}
	}
}

func (e *Editor) blinkCmd() tea.Cmd {
	return tea.Tick(tickInterval, func(t time.Time) tea.Msg {
		return CursorBlinkMsg(t)
	})
}

func (e *Editor) BlinkCmd() tea.Cmd {
	return e.blinkCmd()
}

type EditorCloseMsg struct {
	FilePath string
}

func (e *Editor) moveUp() {
	if e.cursor.Line <= 0 {
		return
	}
	if !e.goalSet {
		e.goalCol = e.cursor.Col
		e.goalSet = true
	}
	e.cursor.Line--
	e.applyGoalCol()
	e.ensureCursorVisible()
}

func (e *Editor) moveDown() {
	maxLine := e.buf.LineCount() - 1
	if maxLine < 0 {
		maxLine = 0
	}
	if e.cursor.Line >= maxLine {
		return
	}
	if !e.goalSet {
		e.goalCol = e.cursor.Col
		e.goalSet = true
	}
	e.cursor.Line++
	e.applyGoalCol()
	e.ensureCursorVisible()
}

func (e *Editor) applyGoalCol() {
	b, err := e.buf.Line(e.cursor.Line)
	if err != nil {
		return
	}
	lineLen := utf8.RuneCount(b)
	if e.goalCol > lineLen {
		e.cursor.Col = lineLen
	} else {
		e.cursor.Col = e.goalCol
	}
}

func (e *Editor) clearGoal() {
	e.goalSet = false
	e.goalCol = 0
}

func (e *Editor) moveLeft() {
	e.clearGoal()
	if e.cursor.Col > 0 {
		e.cursor.Col--
	} else if e.cursor.Line > 0 {
		e.cursor.Line--
		b, err := e.buf.Line(e.cursor.Line)
		if err == nil {
			e.cursor.Col = utf8.RuneCount(b)
		}
	}
	e.ensureCursorVisible()
}

func (e *Editor) moveRight() {
	e.clearGoal()
	b, err := e.buf.Line(e.cursor.Line)
	if err == nil {
		lineLen := utf8.RuneCount(b)
		if e.cursor.Col < lineLen {
			e.cursor.Col++
		} else if e.cursor.Line < e.buf.LineCount()-1 {
			e.cursor.Line++
			e.cursor.Col = 0
		}
	}
	e.ensureCursorVisible()
}

func (e *Editor) moveToLineStart() {
	e.clearGoal()
	b, err := e.buf.Line(e.cursor.Line)
	if err != nil {
		e.cursor.Col = 0
		e.ensureCursorVisible()
		return
	}
	runes := []rune(string(b))
	if e.cursor.Col > 0 {
		firstNonWS := 0
		for i, r := range runes {
			if r != ' ' && r != '\t' {
				firstNonWS = i
				break
			}
		}
		if e.cursor.Col != firstNonWS {
			e.cursor.Col = firstNonWS
		} else {
			e.cursor.Col = 0
		}
	}
	e.ensureCursorVisible()
}

func (e *Editor) moveToLineEnd() {
	e.clearGoal()
	b, err := e.buf.Line(e.cursor.Line)
	if err == nil {
		e.cursor.Col = utf8.RuneCount(b)
	}
	e.ensureCursorVisible()
}

func (e *Editor) pageUp() {
	e.clearGoal()
	pageSize := e.viewH - 2
	if pageSize < 1 {
		pageSize = 1
	}
	e.cursor.Line -= pageSize
	if e.cursor.Line < 0 {
		e.cursor.Line = 0
	}
	e.clampCol(e.cursor.Line)
	e.ensureCursorVisible()
}

func (e *Editor) pageDown() {
	e.clearGoal()
	pageSize := e.viewH - 2
	if pageSize < 1 {
		pageSize = 1
	}
	e.cursor.Line += pageSize
	lastLine := e.buf.LineCount() - 1
	if lastLine < 0 {
		lastLine = 0
	}
	if e.cursor.Line >= lastLine {
		e.cursor.Line = lastLine
	}
	e.clampCol(e.cursor.Line)
	e.ensureCursorVisible()
}

func (e *Editor) clampCol(lineIdx int) {
	if lineIdx < 0 {
		return
	}
	b, err := e.buf.Line(lineIdx)
	if err == nil {
		lineLen := utf8.RuneCount(b)
		if e.cursor.Col > lineLen {
			e.cursor.Col = lineLen
		}
	}
}

func (e *Editor) insertRune(r rune) {
	e.clearGoal()
	cursorBefore := e.cursor
	runeBytes := make([]byte, utf8.RuneLen(r))
	utf8.EncodeRune(runeBytes, r)

	e.buf.Insert(e.cursor, runeBytes)

	cursorAfter := e.cursor
	if r == '\n' {
		cursorAfter.Line++
		cursorAfter.Col = 0
	} else {
		cursorAfter.Col++
	}
	e.cursor = cursorAfter
	e.modified = true

	e.recordChange(Change{
		Type:         ActInsert,
		Text:         runeBytes,
		StartPos:     cursorBefore,
		EndPos:       cursorAfter,
		CursorBefore: cursorBefore,
	})
	e.ensureCursorVisible()
}

func (e *Editor) insertNewline() {
	e.insertRune('\n')
}

func (e *Editor) insertTab() {
	e.insertRune('\t')
}

func (e *Editor) deleteBackward() {
	e.clearGoal()
	if e.cursor.Col > 0 {
		cursorBefore := e.cursor
		lineBytes, err := e.buf.Line(e.cursor.Line)
		if err != nil {
			return
		}
		runes := []rune(string(lineBytes))
		if e.cursor.Col-1 >= 0 && e.cursor.Col-1 < len(runes) {
			ch := runes[e.cursor.Col-1]
			start := Position{Line: e.cursor.Line, Col: e.cursor.Col - 1}
			end := e.cursor

			e.buf.Delete(start, end)
			e.cursor.Col--
			e.modified = true

			e.recordChange(Change{
				Type:         ActDelete,
				Text:         []byte(string(ch)),
				StartPos:     start,
				EndPos:       end,
				CursorBefore: cursorBefore,
			})
		}
	} else if e.cursor.Line > 0 {
		cursorBefore := e.cursor
		prevLine := e.cursor.Line - 1
		prevLineBytes, err := e.buf.Line(prevLine)
		if err != nil {
			return
		}
		prevLineLen := utf8.RuneCount(prevLineBytes)

		start := Position{Line: prevLine, Col: prevLineLen}
		end := Position{Line: e.cursor.Line, Col: 0}

		e.buf.Delete(start, end)
		e.cursor.Line = prevLine
		e.cursor.Col = prevLineLen
		e.modified = true

		e.recordChange(Change{
			Type:         ActDelete,
			Text:         []byte("\n"),
			StartPos:     start,
			EndPos:       end,
			CursorBefore: cursorBefore,
		})
	}
	e.ensureCursorVisible()
}

func (e *Editor) deleteForward() {
	e.clearGoal()
	lineBytes, err := e.buf.Line(e.cursor.Line)
	if err != nil {
		return
	}
	runes := []rune(string(lineBytes))
	lineLen := len(runes)

	if e.cursor.Col < lineLen {
		cursorBefore := e.cursor
		ch := runes[e.cursor.Col]
		start := e.cursor
		end := Position{Line: e.cursor.Line, Col: e.cursor.Col + 1}

		e.buf.Delete(start, end)
		e.modified = true

		e.recordChange(Change{
			Type:         ActDelete,
			Text:         []byte(string(ch)),
			StartPos:     start,
			EndPos:       end,
			CursorBefore: cursorBefore,
		})
	} else if e.cursor.Line < e.buf.LineCount()-1 {
		cursorBefore := e.cursor
		start := e.cursor
		end := Position{Line: e.cursor.Line + 1, Col: 0}

		e.buf.Delete(start, end)
		e.modified = true

		e.recordChange(Change{
			Type:         ActDelete,
			Text:         []byte("\n"),
			StartPos:     start,
			EndPos:       end,
			CursorBefore: cursorBefore,
		})
	}
	e.ensureCursorVisible()
}

func (e *Editor) recordChange(c Change) {
	if e.changeIndex < len(e.changes) {
		e.changes = e.changes[:e.changeIndex]
	}
	e.changes = append(e.changes, c)
	if len(e.changes) > e.maxHistory {
		e.changes = e.changes[len(e.changes)-e.maxHistory:]
	}
	e.changeIndex = len(e.changes)
}

func (e *Editor) undo() {
	if e.changeIndex <= 0 {
		return
	}
	e.changeIndex--
	c := e.changes[e.changeIndex]

	switch c.Type {
	case ActInsert:
		e.buf.Delete(c.StartPos, c.EndPos)
		e.cursor = c.CursorBefore
	case ActDelete:
		e.buf.Insert(c.StartPos, c.Text)
		e.cursor = c.CursorBefore
	}
	e.modified = true
	e.ensureCursorVisible()
}

func (e *Editor) redo() {
	if e.changeIndex >= len(e.changes) {
		return
	}
	c := e.changes[e.changeIndex]
	e.changeIndex++

	switch c.Type {
	case ActInsert:
		e.buf.Insert(c.StartPos, c.Text)
		e.cursor = c.EndPos
	case ActDelete:
		e.buf.Delete(c.StartPos, c.EndPos)
		e.cursor = c.EndPos
	}
	e.modified = true
	e.ensureCursorVisible()
}

func (e *Editor) save() {
	err := e.buf.Save(e.filePath)
	if err == nil {
		e.modified = false
	}
}

func (e *Editor) ensureCursorVisible() {
	sw := e.gutterWidth()
	sbW := scrollbar.ScrollbarWidth
	availW := e.viewW - sw - sbW
	if availW < 1 {
		availW = 1
	}
	availH := e.viewH
	if availH < 1 {
		availH = 1
	}

	margin := scrollMargin
	if margin > availH/2 {
		margin = availH / 2
	}
	if margin < 1 {
		margin = 1
	}

	if e.cursor.Line < e.scrollY+margin {
		e.scrollY = e.cursor.Line - margin
	}
	if e.cursor.Line >= e.scrollY+availH-margin {
		e.scrollY = e.cursor.Line - availH + margin + 1
	}
	if e.scrollY < 0 {
		e.scrollY = 0
	}
	maxScroll := e.buf.LineCount() - availH
	if maxScroll < 0 {
		maxScroll = 0
	}
	if e.scrollY > maxScroll {
		e.scrollY = maxScroll
	}

	hMargin := 5
	if hMargin > availW/2 {
		hMargin = availW / 2
	}
	if hMargin < 1 {
		hMargin = 1
	}

	if e.cursor.Col < e.scrollX+hMargin {
		e.scrollX = e.cursor.Col - hMargin
	}
	if e.cursor.Col >= e.scrollX+availW-hMargin {
		e.scrollX = e.cursor.Col - availW + hMargin + 1
	}
	if e.scrollX < 0 {
		e.scrollX = 0
	}

	lc := e.buf.LineCount()
	e.sbTrackH = e.viewH
	e.scrollbar.SetTrackHeight(e.viewH)
	e.scrollbar.SetScrollInfo(lc, e.viewH, e.scrollY)
}


