package ui

import (
	"strings"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
)

// AtomicRange is a flat [Start, End) rune range that is treated as an
// indivisible token by the editor (e.g. @reference chips). The cursor
// snaps across it and backspace/delete removes it as a unit.
type AtomicRange struct {
	ID    string
	Start int
	End   int
}

// TextArea is the native multi-line text editor component. It owns a
// rune-safe line/col editing engine (the same algorithm that powered
// components.TextInput) and renders directly into a CellBuffer via Paint.
//
// TextArea lives in the foundation package (tui/ui) alongside the
// single-line TextEdit. Higher layers (components.TextInput, composer,
// text overlay, chat input) migrate onto this primitive. The engine
// supports wrap-aware vertical navigation, goal-column preservation,
// multi-line selection, atomic ranges, and the full legacy key set.
//
// This is the Phase 6 multi-line input primitive. It satisfies ui.Component.
type TextArea struct {
	Base
	tokens     *TokenSet
	edit       *textAreaEngine
	placeholder string
	prompt     string
	submitID   string
	multiLine  bool
	bg        cell.Color
	bgSet     bool
	// textWidth is the wrap width for visual line calculation; set during
	// Layout based on the allocated width.
	textWidth int
	// scroll tracks the first visible visual row when content overflows.
	scroll int
	// viewH is the allocated body height, used for scroll clamping.
	viewH int
	// lastInputTime tracks the last edit for cursor solid-phase rendering.
	lastInputTime time.Time
}

// NewTextArea creates a multi-line text area. multiLine controls Enter
// behavior: when true, Enter inserts a newline (use the submit action for
// the explicit submit key, typically Ctrl+Enter); when false, Enter emits
// the submit action.
func NewTextArea(id ComponentID, tokens *TokenSet, multiLine bool) *TextArea {
	t := &TextArea{
		Base:      NewBase(id),
		tokens:    tokens,
		edit:      newTextAreaEngine(),
		multiLine: multiLine,
		submitID:  "input.submit",
	}
	return t
}

// SetTokens updates the theme token set.
func (t *TextArea) SetTokens(ts *TokenSet) { t.tokens = ts }

// SetPlaceholder sets empty-state hint text.
func (t *TextArea) SetPlaceholder(s string) { t.placeholder = s }

// SetPrompt sets a leading prompt glyph drawn before the first line.
func (t *TextArea) SetPrompt(s string) { t.prompt = s }

// SetBackgroundColor overrides the field background fill.
func (t *TextArea) SetBackgroundColor(c cell.Color) { t.bg, t.bgSet = c, true }

// SetSubmitAction sets the action ID emitted on Enter (single-line mode) or
// Ctrl+Enter (multi-line mode).
func (t *TextArea) SetSubmitAction(id string) { t.submitID = id }

// SetMultiLine controls whether Enter inserts a newline or submits.
func (t *TextArea) SetMultiLine(v bool) { t.multiLine = v }

// Value returns the full text content.
func (t *TextArea) Value() string { return t.edit.Value() }

// SetValue replaces the content and places the cursor at the end.
func (t *TextArea) SetValue(s string) {
	t.edit.SetValue(s)
	t.lastInputTime = time.Now()
}

// Clear empties the buffer and resets the cursor.
func (t *TextArea) Clear() {
	t.edit.Clear()
	t.scroll = 0
	t.lastInputTime = time.Now()
}

// Len returns the total rune count including newlines.
func (t *TextArea) Len() int { return t.edit.Len() }

// CursorFlat returns the flat rune cursor offset.
func (t *TextArea) CursorFlat() int { return t.edit.CursorFlat() }

// SetCursorFlat sets the cursor by flat rune offset.
func (t *TextArea) SetCursorFlat(pos int) { t.edit.SetCursorFlat(pos) }

// CurLine returns the current logical line index.
func (t *TextArea) CurLine() int { return t.edit.CurLine() }

// CurCol returns the current column within the logical line.
func (t *TextArea) CurCol() int { return t.edit.CurCol() }

// CursorVisualLine returns the wrap-aware visual row of the cursor.
func (t *TextArea) CursorVisualLine() int { return t.edit.CursorVisualLine() }

// TotalVisualLines returns the total number of visual rows (wrap-aware).
func (t *TextArea) TotalVisualLines() int { return t.edit.TotalVisualLines() }

// VisualLineCount returns the visual row count for a given wrap width.
func (t *TextArea) VisualLineCount(textW int) int { return t.edit.VisualLineCount(textW) }

// Height returns the visual line count for the current textWidth.
func (t *TextArea) Height() int {
	if t.textWidth < 1 {
		return 1
	}
	return t.edit.VisualLineCount(t.textWidth)
}

// Lines returns the raw rune slices per logical line.
func (t *TextArea) Lines() [][]rune { return t.edit.Lines() }

// InsertString inserts a string at the cursor, handling newlines.
func (t *TextArea) InsertString(s string) {
	t.edit.InsertString(s)
	t.lastInputTime = time.Now()
}

// SetTextWidth sets the wrap width used for visual line calculation.
func (t *TextArea) SetTextWidth(w int) { t.textWidth = w }

// SelectionRange returns the normalized [start, end) flat offsets.
func (t *TextArea) SelectionRange() (int, int) { return t.edit.SelectionRange() }

// HasActiveSelection reports a non-empty selection.
func (t *TextArea) HasActiveSelection() bool { return t.edit.HasActiveSelection() }

// IsSelecting reports whether a mouse-drag selection is in progress.
func (t *TextArea) IsSelecting() bool { return t.edit.IsSelecting() }

// SetSelection sets the selection anchor and end.
func (t *TextArea) SetSelection(anchor, end int) { t.edit.SetSelection(anchor, end) }

// UpdateSelectionEnd moves the selection end (for drag).
func (t *TextArea) UpdateSelectionEnd(end int) { t.edit.UpdateSelectionEnd(end) }

// ClearSelection drops the selection.
func (t *TextArea) ClearSelection() { t.edit.ClearSelection() }

// SelectedText returns the selected substring.
func (t *TextArea) SelectedText() string { return t.edit.SelectedText() }

// SetSelecting sets the selecting flag directly.
func (t *TextArea) SetSelecting(v bool) { t.edit.SetSelecting(v) }

// FlatToLineCol converts a flat offset to (line, col).
func (t *TextArea) FlatToLineCol(pos int) (int, int) { return t.edit.FlatToLineCol(pos) }

// DeleteRange deletes a flat [start, end) range.
func (t *TextArea) DeleteRange(start, end int) { t.edit.DeleteRange(start, end) }

// SetAtomicRanges sets the atomic (untouchable) ranges for reference chips.
func (t *TextArea) SetAtomicRanges(ranges []AtomicRange) { t.edit.SetAtomicRanges(ranges) }

// NormalizeCursorAtomic snaps the cursor out of any atomic range.
func (t *TextArea) NormalizeCursorAtomic() { t.edit.NormalizeCursorAtomic() }

// MouseToFlatPos translates a local (x, y) within the painted area to a flat
// rune cursor offset. Returns the end offset if y is below the content.
func (t *TextArea) MouseToFlatPos(localX, localY int) int {
	if t.textWidth < 1 {
		return 0
	}
	return t.edit.visualToFlat(t.textWidth, localX, localY+t.scroll)
}

// Measure: fill available width, height = visual line count (clamped).
func (t *TextArea) Measure(_ LayoutContext, cs Constraints) Size {
	h := t.Height()
	if h < 1 {
		h = 1
	}
	return cs.ClampSize(Size{W: cs.MaxW, H: h})
}

// Layout stores bounds and recomputes the wrap width.
func (t *TextArea) Layout(_ LayoutContext, r Rect) {
	t.SetBounds(r)
	tw := r.W
	if tw < 1 {
		tw = 1
	}
	t.textWidth = tw
	t.viewH = r.H
	t.edit.SetTextWidth(tw)
	t.clampScroll()
}

func (t *TextArea) clampScroll() {
	if t.scroll < 0 {
		t.scroll = 0
	}
	maxScroll := t.TotalVisualLines() - t.viewH
	if maxScroll < 0 {
		maxScroll = 0
	}
	if t.scroll > maxScroll {
		t.scroll = maxScroll
	}
}

// EnsureCursorVisible scrolls the viewport so the cursor's visual row is visible.
func (t *TextArea) EnsureCursorVisible() {
	curVis := t.edit.CursorVisualLine()
	if curVis < t.scroll {
		t.scroll = curVis
	}
	if t.viewH > 0 && curVis >= t.scroll+t.viewH {
		t.scroll = curVis - t.viewH + 1
	}
	t.clampScroll()
}

// Paint renders the text area into the cell buffer.
func (t *TextArea) Paint(buf *surface.CellBuffer, originX, originY int, ctx PaintContext) {
	r := t.Bounds()
	if r.Empty() {
		return
	}
	ox := originX + r.X
	oy := originY + r.Y

	fg, bg := cell.NoColor, cell.NoColor
	if t.tokens != nil {
		st := ResolveControlState(false, ctx.Focused, false, false, ctx.Hovered)
		fg, bg, _ = t.tokens.InputColors(st)
	}
	if t.bgSet {
		bg = t.bg
	}
	if !bg.IsDefault() {
		FillRect(buf, ox, oy, r, cell.BlankCell(bg))
	}

	// Prompt on the first visual row only
	promptW := rendering.VisibleWidth(t.prompt)
	if t.prompt != "" {
		pf := fg
		if t.tokens != nil {
			pf = t.tokens.Color(ColorAccent)
		}
		buf.WriteString(ox, oy, t.prompt, pf, bg, 0)
	}
	textX := ox + promptW
	textW := t.textWidth
	if textW < 1 {
		textW = 1
	}

	// Placeholder when empty
	if t.edit.Len() == 0 && t.placeholder != "" {
		phFG := fg
		if t.tokens != nil {
			phFG = t.tokens.Color(ColorMuted)
		}
		ph := t.placeholder
		if rendering.VisibleWidth(ph) > textW {
			runes := []rune(ph)
			if len(runes) > textW {
				runes = runes[:textW]
			}
			ph = string(runes)
		}
		buf.WriteString(textX, oy, ph, phFG, bg, cell.AttrItalic)
		if ctx.Focused {
			t.paintBlockCursor(buf, textX, oy, fg, bg, ' ')
		}
		return
	}

	cursorSolid := ctx.Focused && time.Since(t.lastInputTime) < 800*time.Millisecond
	selStart, selEnd := int(-1), int(-1)
	if t.edit.IsSelecting() {
		selStart, selEnd = t.edit.SelectionRange()
	}
	selBG := bg
	if t.tokens != nil {
		selBG = t.tokens.Color(ColorSelection)
	}

	lines := t.edit.Lines()
	visRow := 0
	flatOffset := 0
	for li, logLine := range lines {
		segs := rendering.WordWrap(string(logLine), textW)
		for si, seg := range segs {
			if visRow < t.scroll {
				visRow++
				flatOffset += 0 // handled after loop
				continue
			}
			screenY := oy + visRow - t.scroll
			if screenY >= oy+r.H {
				return
			}
			rowX := textX
			if li == 0 && si == 0 {
				rowX = textX // prompt already drawn at ox
			}

			segRunes := []rune(seg)
			segStart := rendering.FindSegmentStart(logLine, textW, si)
			lineTokenStart := segStart
			lineTokenEnd := segStart + len(segRunes)
			isCursorSeg := li == t.edit.CurLine() &&
				si == segmentIdxFor(t.edit.CurLine(), t.edit.CurCol(), lines, textW)

			t.paintSegment(buf, rowX, screenY, textW, logLine, flatOffset,
				lineTokenStart, lineTokenEnd, isCursorSeg,
				selStart, selEnd, cursorSolid, ctx.Focused,
				fg, bg, selBG)

			visRow++
		}
		flatOffset += len(logLine) + 1
	}

	// Empty buffer: draw a block cursor at the origin
	if visRow == 0 && ctx.Focused {
		if oy < oy+r.H {
			t.paintBlockCursor(buf, textX, oy, fg, bg, ' ')
		}
	}
}

func (t *TextArea) paintSegment(buf *surface.CellBuffer, x, y, width int, line []rune,
	flatOffset, start, end int, isCursorSeg bool, selStart, selEnd int,
	cursorSolid, focused bool, fg, bg, selBG cell.Color) {

	cursorPos := t.edit.CursorFlat()
	col := 0
	pos := start
	for pos < end && col < width {
		flatPos := flatOffset + pos
		ch := line[pos]
		chW := rendering.VisibleWidth(string(ch))
		if chW < 1 {
			chW = 1
		}
		inSel := selStart >= 0 && selStart != selEnd && flatPos >= selStart && flatPos < selEnd
		cellFG, cellBG := fg, bg
		if inSel {
			cellBG = selBG
		}
		// Inverse cursor cell on the cursor segment
		if focused && isCursorSeg && cursorPos == flatPos {
			cellFG = bg
			cellBG = fg
			if cellFG.IsDefault() {
				cellFG = cell.NewTrueColor(13, 17, 23)
			}
			if cellBG.IsDefault() {
				cellBG = cell.NewTrueColor(88, 166, 255)
			}
		}
		buf.Set(x+col, y, cell.NewCell(ch, cellFG, cellBG, 0))
		col += chW
		pos++
	}
	// End-of-line block cursor (after the last char on the cursor segment)
	if focused && isCursorSeg && cursorPos == flatOffset+end {
		if col < width {
			t.paintBlockCursor(buf, x+col, y, fg, bg, ' ')
		}
	}
}

func (t *TextArea) paintBlockCursor(buf *surface.CellBuffer, x, y int, fg, bg cell.Color, ch rune) {
	accent := fg
	if t.tokens != nil {
		accent = t.tokens.Color(ColorAccent)
	}
	invFG := bg
	if invFG.IsDefault() {
		invFG = cell.NewTrueColor(13, 17, 23)
	}
	buf.Set(x, y, cell.NewCell(ch, invFG, accent, 0))
}

// Handle processes keyboard, paste, and mouse events.
func (t *TextArea) Handle(ctx EventContext, msg tea.Msg) EventResult {
	switch m := msg.(type) {
	case tea.KeyPressMsg:
		return t.handleKey(m)
	case tea.PasteMsg:
		t.edit.InsertString(m.Content)
		t.lastInputTime = time.Now()
		t.EnsureCursorVisible()
		return EventResult{Handled: true, Invalidate: InvalidatePaint}
	case tea.MouseClickMsg:
		if m.Button != tea.MouseLeft {
			return EventResult{}
		}
		pos := t.MouseToFlatPos(ctx.LocalX, ctx.LocalY)
		if pos >= 0 {
			t.edit.SetSelection(pos, pos)
			t.edit.SetCursorFlat(pos)
			t.edit.SetSelecting(true)
		}
		return EventResult{Handled: true, Invalidate: InvalidatePaint | InvalidateFocus}
	case tea.MouseMotionMsg:
		if t.edit.IsSelecting() {
			pos := t.MouseToFlatPos(ctx.LocalX, ctx.LocalY)
			if pos >= 0 {
				t.edit.UpdateSelectionEnd(pos)
			}
		}
		return EventResult{Handled: true, Invalidate: InvalidatePaint}
	case tea.MouseReleaseMsg:
		if t.edit.IsSelecting() {
			t.edit.SetSelecting(false)
		}
		return EventResult{Handled: true, Invalidate: InvalidatePaint}
	}
	return EventResult{}
}

func (t *TextArea) handleKey(m tea.KeyPressMsg) EventResult {
	// Ctrl+Enter always submits (even in multi-line mode)
	if m.Mod.Contains(tea.ModCtrl) && m.Code == tea.KeyEnter {
		if t.submitID != "" {
			return EventResult{
				Handled:    true,
				Action:     &Action{ID: t.submitID, Payload: t.edit.Value()},
				Invalidate: InvalidatePaint,
			}
		}
	}
	// Esc clears selection or falls through
	if m.Code == tea.KeyEsc {
		if t.edit.HasActiveSelection() {
			t.edit.ClearSelection()
			return EventResult{Handled: true, Invalidate: InvalidatePaint}
		}
		return EventResult{}
	}
	// Single-line Enter submits
	if m.Code == tea.KeyEnter && !t.multiLine {
		if t.submitID == "" {
			return EventResult{Handled: true}
		}
		return EventResult{
			Handled:    true,
			Action:     &Action{ID: t.submitID, Payload: t.edit.Value()},
			Invalidate: InvalidatePaint,
		}
	}
	// All other keys (including Enter in multi-line mode) go to the engine
	t.edit.handleKey(m)
	t.lastInputTime = time.Now()
	t.EnsureCursorVisible()
	return EventResult{Handled: true, Invalidate: InvalidatePaint}
}

// Semantics returns the accessibility/test metadata.
func (t *TextArea) Semantics() Semantics {
	return Semantics{
		Role:  RoleTextInput,
		Label: t.placeholder,
		Value: t.edit.Value(),
	}
}

// Update applies a message to the engine, mirroring the legacy
// components.TextInput.Update signature for drop-in migration. Returns
// the tea.Cmd (always nil for key events; the engine never emits cmds
// since submit is handled by the caller via the submit action).
func (t *TextArea) Update(msg tea.Msg, disabled bool) tea.Cmd {
	if disabled {
		return nil
	}
	if key, ok := msg.(tea.KeyPressMsg); ok {
		t.edit.handleKey(key)
		t.lastInputTime = time.Now()
		t.EnsureCursorVisible()
		return nil
	}
	if paste, ok := msg.(tea.PasteMsg); ok {
		t.edit.InsertString(paste.Content)
		t.lastInputTime = time.Now()
		t.EnsureCursorVisible()
		return nil
	}
	return nil
}

// View returns an ANSI-styled string rendering for legacy consumers that
// still route through the View()-string pipeline. The native Paint path
// is preferred for component-tree consumers.
func (t *TextArea) View(width, spinFrame int, cursorSolidAlways bool) string {
	if width < 1 {
		width = 1
	}
	tw := width
	if t.textWidth > 0 && t.textWidth < tw {
		tw = t.textWidth
	}
	if tw < 1 {
		tw = 1
	}
	t.edit.SetTextWidth(tw)

	selStart, selEnd := int(-1), int(-1)
	if t.edit.IsSelecting() {
		selStart, selEnd = t.edit.SelectionRange()
	}

	var allLines []string
	lines := t.edit.Lines()
	flatOffset := 0
	for li, line := range lines {
		segs := rendering.WordWrap(string(line), tw)
		for si, seg := range segs {
			segRunes := []rune(seg)
			segStart := rendering.FindSegmentStart(line, tw, si)
			lineTokenStart := segStart
			lineTokenEnd := segStart + len(segRunes)
			isCursorSeg := li == t.edit.CurLine() &&
				si == segmentIdxFor(t.edit.CurLine(), t.edit.CurCol(), lines, tw)
			rendered := t.renderSegmentTokens(line, flatOffset, lineTokenStart, lineTokenEnd,
				isCursorSeg, selStart, selEnd, cursorSolidAlways, tw)
			allLines = append(allLines, rendered)
		}
		flatOffset += len(line) + 1
	}
	if len(allLines) == 0 {
		allLines = append(allLines, "")
	}
	return strings.Join(allLines, "\n")
}

func (t *TextArea) renderSegmentTokens(line []rune, flatOffset, start, end int,
	isCursorSeg bool, selStart, selEnd int, cursorSolidAlways bool, textW int) string {
	cursorPos := t.edit.CursorFlat()
	var b strings.Builder
	pos := start
	for pos < end {
		flatPos := flatOffset + pos
		if isCursorSeg && cursorPos == flatPos && pos < len(line) {
			ch := string(line[pos])
			if cursorSolidAlways {
				// reverse video via ANSI
				b.WriteString("\x1b[7m" + ch + "\x1b[0m")
			} else {
				b.WriteString(ch)
			}
			pos++
			continue
		}
		b.WriteString(string(line[pos]))
		pos++
	}
	if isCursorSeg && cursorPos == flatOffset+end {
		b.WriteString("\x1b[38;2;88;166;255m█\x1b[0m")
	}
	return b.String()
}

func segmentIdxFor(curLine, curCol int, buf [][]rune, textW int) int {
	if curLine >= len(buf) {
		return 0
	}
	runes := buf[curLine]
	segs := rendering.WordWrap(string(runes), textW)
	for si := range segs {
		segStart := rendering.FindSegmentStart(runes, textW, si)
		segEnd := segStart + len([]rune(segs[si]))
		if curCol <= segEnd || si == len(segs)-1 {
			return si
		}
	}
	return 0
}

var _ Component = (*TextArea)(nil)

// ---------------------------------------------------------------------------
// textAreaEngine — the multi-line editing model
//
// This is the engine from components/text.go, relocated into the foundation
// package (tui/ui) so TextArea can own it natively without an import cycle.
// The algorithm is preserved exactly: rune-safe line/col, wrap-aware
// vertical navigation with goal column, atomic ranges, multi-line selection.
// ---------------------------------------------------------------------------
type textAreaEngine struct {
	buf           [][]rune
	curLine       int
	curCol        int
	lastVisCol    int
	selecting     bool
	selAnchor     int
	selEnd        int
	lastInputTime time.Time
	frame         int
	textWidth     int
	atomicRanges  []AtomicRange
}

func newTextAreaEngine() *textAreaEngine { return &textAreaEngine{buf: [][]rune{{}} } }

func (e *textAreaEngine) handleKey(msg tea.KeyPressMsg) {
	switch msg.Code {
	case tea.KeyEnter:
		e.splitLineAt(e.curLine, e.curCol)
		e.curLine++
		e.curCol = 0
	case tea.KeyBackspace:
		pos := e.lineColToFlat(e.curLine, e.curCol)
		if pos > 0 {
			if ar, ok := e.atomicRangeAround(pos - 1); ok {
				e.deleteRange(ar.Start, ar.End)
				e.setCursorFlat(ar.Start)
				break
			}
			e.deleteRuneAt(pos)
			e.curLine, e.curCol = e.flatToLineCol(pos - 1)
		}
	case tea.KeyDelete:
		pos := e.lineColToFlat(e.curLine, e.curCol)
		if pos < e.Len() {
			if ar, ok := e.atomicRangeAround(pos); ok {
				e.deleteRange(ar.Start, ar.End)
				e.setCursorFlat(ar.Start)
				break
			}
			e.deleteForwardAt(pos)
		}
	case tea.KeyUp:
		_, visCol := e.cursorVisualInfo()
		e.lastVisCol = visCol
		e.cursorUpVisual()
	case tea.KeyDown:
		_, visCol := e.cursorVisualInfo()
		e.lastVisCol = visCol
		e.cursorDownVisual()
	case tea.KeyLeft:
		if msg.Mod.Contains(tea.ModCtrl) {
			e.setCursorFlat(e.cursorPrevWord())
		} else if e.curCol > 0 {
			e.curCol--
		} else if e.curLine > 0 {
			e.curLine--
			e.curCol = len(e.buf[e.curLine])
		}
		e.snapCursorAcrossAtomic(false)
		e.lastVisCol = -1
	case tea.KeyRight:
		if msg.Mod.Contains(tea.ModCtrl) {
			e.setCursorFlat(e.cursorNextWord())
		} else if e.curCol < len(e.buf[e.curLine]) {
			e.curCol++
		} else if e.curLine < len(e.buf)-1 {
			e.curLine++
			e.curCol = 0
		}
		e.snapCursorAcrossAtomic(true)
		e.lastVisCol = -1
	case tea.KeyHome:
		if msg.Mod.Contains(tea.ModCtrl) {
			e.curLine = 0
		}
		e.curCol = 0
		e.lastVisCol = -1
	case tea.KeyEnd:
		if msg.Mod.Contains(tea.ModCtrl) {
			e.curLine = len(e.buf) - 1
			if e.curLine < 0 {
				e.curLine = 0
			}
		}
		e.curCol = len(e.buf[e.curLine])
		e.lastVisCol = -1
	default:
		if msg.Mod.Contains(tea.ModCtrl) && len(msg.Text) == 0 {
			switch msg.Code {
			case 'a':
				e.curCol = 0
				e.lastVisCol = -1
			case 'e':
				e.curCol = len(e.buf[e.curLine])
				e.lastVisCol = -1
			case 'u':
				start := e.cursorLineStart()
				cf := e.lineColToFlat(e.curLine, e.curCol)
				if start < cf {
					e.deleteRange(start, cf)
					e.setCursorFlat(start)
				}
			case 'k':
				end := e.cursorLineEnd()
				cf := e.lineColToFlat(e.curLine, e.curCol)
				if end > cf {
					e.deleteRange(cf, end)
				}
			case 'w':
				if e.lineColToFlat(e.curLine, e.curCol) > 0 {
					pos := e.cursorPrevWord()
					cf := e.lineColToFlat(e.curLine, e.curCol)
					e.deleteRange(pos, cf)
					e.setCursorFlat(pos)
				}
			}
		} else if msg.Mod.Contains(tea.ModAlt) && msg.Code == '\\' {
			e.splitLineAt(e.curLine, e.curCol)
			e.curLine++
			e.curCol = 0
		} else {
			for _, r := range msg.Text {
				if unicode.IsPrint(r) {
					pos := e.lineColToFlat(e.curLine, e.curCol)
					e.insertRuneAt(pos, r)
					e.curCol++
				}
			}
		}
	}
	e.lastInputTime = time.Now()
	e.frame++
}

func (e *textAreaEngine) Value() string {
	parts := make([]string, len(e.buf))
	for i, line := range e.buf {
		parts[i] = string(line)
	}
	return strings.Join(parts, "\n")
}

func (e *textAreaEngine) SetValue(s string) {
	lines := strings.Split(s, "\n")
	e.buf = make([][]rune, len(lines))
	for i, l := range lines {
		e.buf[i] = []rune(l)
	}
	if len(e.buf) == 0 {
		e.buf = [][]rune{{}}
	}
	e.curLine = len(e.buf) - 1
	if e.curLine < 0 {
		e.curLine = 0
	}
	e.curCol = len(e.buf[e.curLine])
}

func (e *textAreaEngine) InsertString(s string) {
	for _, r := range s {
		if r == '\r' {
			continue
		}
		pos := e.lineColToFlat(e.curLine, e.curCol)
		if r == '\n' {
			e.splitLineAt(e.curLine, e.curCol)
			e.curLine++
			e.curCol = 0
			continue
		}
		e.insertRuneAt(pos, r)
		e.curCol++
	}
	e.lastInputTime = time.Now()
	e.frame++
}

func (e *textAreaEngine) Clear() {
	e.buf = [][]rune{{}}
	e.curLine = 0
	e.curCol = 0
	e.selecting = false
	e.selAnchor = 0
	e.selEnd = 0
}

func (e *textAreaEngine) Len() int {
	n := 0
	for _, line := range e.buf {
		n += len(line)
	}
	if len(e.buf) > 0 {
		n += len(e.buf) - 1
	}
	return n
}

func (e *textAreaEngine) cursorPrevWord() int {
	pos := e.lineColToFlat(e.curLine, e.curCol)
	if pos <= 0 {
		return 0
	}
	buf := []rune(e.Value())
	pos--
	for pos > 0 && buf[pos] == ' ' {
		pos--
	}
	for pos > 0 && buf[pos-1] != ' ' {
		pos--
	}
	return pos
}

func (e *textAreaEngine) cursorNextWord() int {
	pos := e.lineColToFlat(e.curLine, e.curCol)
	buf := []rune(e.Value())
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

func (e *textAreaEngine) cursorLineStart() int { return e.lineColToFlat(e.curLine, 0) }
func (e *textAreaEngine) cursorLineEnd() int   { return e.lineColToFlat(e.curLine, len(e.buf[e.curLine])) }

func (e *textAreaEngine) deleteRange(start, end int) {
	if start == end || start > end {
		start, end = end, start
	}
	if start < 0 {
		start = 0
	}
	totalLen := e.Len()
	if end > totalLen {
		end = totalLen
	}
	if start >= end {
		return
	}
	sLine, sCol := e.flatToLineCol(start)
	eLine, eCol := e.flatToLineCol(end)
	if sLine == eLine {
		newLine := make([]rune, len(e.buf[sLine])-(eCol-sCol))
		copy(newLine, e.buf[sLine][:sCol])
		copy(newLine[sCol:], e.buf[sLine][eCol:])
		e.buf[sLine] = newLine
	} else {
		firstPart := e.buf[sLine][:sCol]
		lastPart := e.buf[eLine][eCol:]
		merged := make([]rune, len(firstPart)+len(lastPart))
		copy(merged, firstPart)
		copy(merged[len(firstPart):], lastPart)
		newBuf := make([][]rune, 0, len(e.buf)-(eLine-sLine))
		newBuf = append(newBuf, e.buf[:sLine]...)
		newBuf = append(newBuf, merged)
		newBuf = append(newBuf, e.buf[eLine+1:]...)
		e.buf = newBuf
	}
}

func (e *textAreaEngine) CursorFlat() int { return e.lineColToFlat(e.curLine, e.curCol) }
func (e *textAreaEngine) CurLine() int    { return e.curLine }
func (e *textAreaEngine) CurCol() int     { return e.curCol }
func (e *textAreaEngine) Lines() [][]rune { return e.buf }

func (e *textAreaEngine) CursorVisualLine() int {
	visLine, _ := e.cursorVisualInfo()
	return visLine
}

func (e *textAreaEngine) TotalVisualLines() int { return e.visualLineCount() }

func (e *textAreaEngine) VisualLineCount(textW int) int {
	if textW < 1 {
		textW = 1
	}
	count := 0
	for _, l := range e.buf {
		segments := rendering.WordWrap(string(l), textW)
		count += len(segments)
	}
	if count < 1 {
		count = 1
	}
	return count
}

func (e *textAreaEngine) SelectionRange() (start, end int) {
	if e.selAnchor <= e.selEnd {
		return e.selAnchor, e.selEnd
	}
	return e.selEnd, e.selAnchor
}

func (e *textAreaEngine) HasActiveSelection() bool {
	if !e.selecting {
		return false
	}
	s, e2 := e.SelectionRange()
	return s != e2
}

func (e *textAreaEngine) IsSelecting() bool { return e.selecting }

func (e *textAreaEngine) SetSelection(anchor, end int) {
	e.selecting = true
	e.selAnchor = anchor
	e.selEnd = end
}

func (e *textAreaEngine) UpdateSelectionEnd(end int) { e.selEnd = end }

func (e *textAreaEngine) ClearSelection() {
	e.selecting = false
	e.selAnchor = 0
	e.selEnd = 0
}

func (e *textAreaEngine) SelectedText() string {
	if !e.selecting {
		return ""
	}
	s, e2 := e.SelectionRange()
	runes := []rune(e.Value())
	if e2 > len(runes) {
		e2 = len(runes)
	}
	if s >= e2 {
		return ""
	}
	return string(runes[s:e2])
}

func (e *textAreaEngine) SetSelecting(v bool) { e.selecting = v }

func (e *textAreaEngine) FlatToLineCol(pos int) (int, int) { return e.flatToLineCol(pos) }
func (e *textAreaEngine) SetCursorFlat(pos int)             { e.setCursorFlat(pos) }
func (e *textAreaEngine) DeleteRange(start, end int)       { e.deleteRange(start, end) }

func (e *textAreaEngine) SetAtomicRanges(ranges []AtomicRange) {
	e.atomicRanges = ranges
	for i := range e.atomicRanges {
		if e.atomicRanges[i].Start > e.atomicRanges[i].End {
			e.atomicRanges[i].Start, e.atomicRanges[i].End = e.atomicRanges[i].End, e.atomicRanges[i].Start
		}
	}
}

func (e *textAreaEngine) NormalizeCursorAtomic() { e.snapCursorAcrossAtomic(true) }

func (e *textAreaEngine) SetTextWidth(w int) { e.textWidth = w }

func (e *textAreaEngine) atomicRangeStartingAt(pos int) (AtomicRange, bool) {
	for _, ar := range e.atomicRanges {
		if ar.Start == pos && ar.End > ar.Start {
			return ar, true
		}
	}
	return AtomicRange{}, false
}

func (e *textAreaEngine) atomicRangeAround(pos int) (AtomicRange, bool) {
	for _, ar := range e.atomicRanges {
		if ar.End <= ar.Start {
			continue
		}
		if pos >= ar.Start && pos < ar.End {
			return ar, true
		}
	}
	return AtomicRange{}, false
}

func (e *textAreaEngine) snapCursorAcrossAtomic(forward bool) {
	pos := e.CursorFlat()
	for _, ar := range e.atomicRanges {
		if ar.End <= ar.Start || pos <= ar.Start || pos >= ar.End {
			continue
		}
		if forward {
			e.setCursorFlat(ar.End)
		} else {
			e.setCursorFlat(ar.Start)
		}
		return
	}
}

func (e *textAreaEngine) flatToLineCol(pos int) (line, col int) {
	if pos <= 0 {
		return 0, 0
	}
	remaining := pos
	for li, lineRunes := range e.buf {
		if remaining <= len(lineRunes) {
			return li, remaining
		}
		remaining -= len(lineRunes) + 1
	}
	if len(e.buf) == 0 {
		return 0, 0
	}
	last := len(e.buf) - 1
	return last, len(e.buf[last])
}

func (e *textAreaEngine) lineColToFlat(line, col int) int {
	if len(e.buf) == 0 {
		return 0
	}
	if line < 0 {
		line = 0
	}
	if line >= len(e.buf) {
		line = len(e.buf) - 1
	}
	pos := 0
	for i := 0; i < line; i++ {
		pos += len(e.buf[i]) + 1
	}
	if col > len(e.buf[line]) {
		col = len(e.buf[line])
	}
	return pos + col
}

func (e *textAreaEngine) insertRuneAt(pos int, r rune) {
	if len(e.buf) == 0 {
		e.buf = [][]rune{{r}}
		return
	}
	line, col := e.flatToLineCol(pos)
	if line >= len(e.buf) {
		line = len(e.buf) - 1
	}
	newLine := make([]rune, len(e.buf[line])+1)
	copy(newLine, e.buf[line][:col])
	newLine[col] = r
	copy(newLine[col+1:], e.buf[line][col:])
	e.buf[line] = newLine
}

func (e *textAreaEngine) deleteRuneAt(pos int) {
	if pos <= 0 {
		return
	}
	line, col := e.flatToLineCol(pos)
	if col > 0 {
		newLine := make([]rune, len(e.buf[line])-1)
		copy(newLine, e.buf[line][:col-1])
		copy(newLine[col-1:], e.buf[line][col:])
		e.buf[line] = newLine
	} else if line > 0 {
		prevLen := len(e.buf[line-1])
		merged := make([]rune, prevLen+len(e.buf[line]))
		copy(merged, e.buf[line-1])
		copy(merged[prevLen:], e.buf[line])
		e.buf = append(e.buf[:line-1], e.buf[line:]...)
		e.buf[line-1] = merged
	}
}

func (e *textAreaEngine) deleteForwardAt(pos int) {
	line, col := e.flatToLineCol(pos)
	if line >= len(e.buf) {
		return
	}
	lineLen := len(e.buf[line])
	if col < lineLen {
		newLine := make([]rune, lineLen-1)
		copy(newLine, e.buf[line][:col])
		copy(newLine[col:], e.buf[line][col+1:])
		e.buf[line] = newLine
	} else if line+1 < len(e.buf) {
		merged := make([]rune, lineLen+len(e.buf[line+1]))
		copy(merged, e.buf[line])
		copy(merged[lineLen:], e.buf[line+1])
		e.buf = append(e.buf[:line], e.buf[line+1:]...)
		e.buf[line] = merged
	}
}

func (e *textAreaEngine) setCursorFlat(pos int) {
	e.curLine, e.curCol = e.flatToLineCol(pos)
}

func (e *textAreaEngine) splitLineAt(line, col int) {
	if line < 0 || line >= len(e.buf) {
		return
	}
	runes := e.buf[line]
	left := make([]rune, col)
	copy(left, runes[:col])
	right := make([]rune, len(runes)-col)
	copy(right, runes[col:])
	newLines := make([][]rune, len(e.buf)+1)
	copy(newLines, e.buf[:line])
	newLines[line] = left
	newLines[line+1] = right
	copy(newLines[line+2:], e.buf[line+1:])
	e.buf = newLines
}

func (e *textAreaEngine) visualLineCount() int {
	if e.textWidth < 1 {
		return len(e.buf)
	}
	count := 0
	for _, l := range e.buf {
		segs := rendering.WordWrap(string(l), e.textWidth)
		count += len(segs)
	}
	if count < 1 {
		count = 1
	}
	return count
}

func (e *textAreaEngine) cursorVisualInfo() (visualLine, visualCol int) {
	if e.textWidth < 1 {
		return e.curLine, e.curCol
	}
	lines := e.buf
	vis := 0
	for li := 0; li < e.curLine; li++ {
		segs := rendering.WordWrap(string(lines[li]), e.textWidth)
		vis += len(segs)
	}
	segs := rendering.WordWrap(string(lines[e.curLine]), e.textWidth)
	for si := range segs {
		segStart := rendering.FindSegmentStart(lines[e.curLine], e.textWidth, si)
		segEnd := segStart + len([]rune(segs[si]))
		if e.curCol <= segEnd || si == len(segs)-1 {
			colW := rendering.RuneIdxToCol(lines[e.curLine][segStart:e.curCol], e.curCol-segStart)
			return vis + si, colW
		}
	}
	last := len(segs) - 1
	segStart := rendering.FindSegmentStart(lines[e.curLine], e.textWidth, last)
	colW := rendering.RuneIdxToCol(lines[e.curLine][segStart:], len(lines[e.curLine])-segStart)
	return vis + last, colW
}

func (e *textAreaEngine) cursorUpVisual() {
	_, visCol := e.cursorVisualInfo()
	targetVisCol := e.lastVisCol
	if targetVisCol < 0 {
		targetVisCol = visCol
	}
	visLine, _ := e.cursorVisualInfo()
	if visLine <= 0 {
		e.curCol = 0
		e.lastVisCol = -1
		return
	}
	e.setCursorFromVisualLine(visLine-1, targetVisCol)
}

func (e *textAreaEngine) cursorDownVisual() {
	_, visCol := e.cursorVisualInfo()
	targetVisCol := e.lastVisCol
	if targetVisCol < 0 {
		targetVisCol = visCol
	}
	visLine, _ := e.cursorVisualInfo()
	total := e.visualLineCount()
	if visLine >= total-1 {
		last := len(e.buf) - 1
		if last < 0 {
			last = 0
		}
		e.curCol = len(e.buf[last])
		e.lastVisCol = -1
		return
	}
	e.setCursorFromVisualLine(visLine+1, targetVisCol)
}

func (e *textAreaEngine) setCursorFromVisualLine(targetVisLine, targetVisCol int) {
	if e.textWidth < 1 {
		if targetVisLine < len(e.buf) {
			e.curLine = targetVisLine
			if targetVisCol < len(e.buf[targetVisLine]) {
				e.curCol = targetVisCol
			} else {
				e.curCol = len(e.buf[targetVisLine])
			}
		}
		return
	}
	lines := e.buf
	vis := 0
	for li, logLine := range lines {
		segs := rendering.WordWrap(string(logLine), e.textWidth)
		for si := range segs {
			if vis == targetVisLine {
				e.curLine = li
				segStart := rendering.FindSegmentStart(logLine, e.textWidth, si)
				segRunes := []rune(segs[si])
				segRuneIdx := rendering.ColToRuneIdx(segRunes, targetVisCol)
				e.curCol = segStart + segRuneIdx
				if e.curCol > len(lines[li]) {
					e.curCol = len(lines[li])
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
	e.curLine = last
	e.curCol = len(lines[last])
}

// visualToFlat converts a visual (x, y) position to a flat rune offset,
// used for mouse-to-cursor mapping. x is relative to the text column
// (prompt already subtracted), y is the visual row from the top of content.
func (e *textAreaEngine) visualToFlat(textW, x, y int) int {
	if textW < 1 {
		textW = 1
	}
	if y < 0 {
		return 0
	}
	lines := e.buf
	vis := 0
	for li, logLine := range lines {
		segs := rendering.WordWrap(string(logLine), textW)
		for si := range segs {
			if vis == y {
				segStart := rendering.FindSegmentStart(logLine, textW, si)
				segRunes := []rune(segs[si])
				segRuneIdx := rendering.ColToRuneIdx(segRunes, x)
				return e.lineColToFlat(li, segStart+segRuneIdx)
			}
			vis++
		}
	}
	return e.Len()
}