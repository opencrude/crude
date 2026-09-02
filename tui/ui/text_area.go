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

// ChipTone selects the display state of an AtomicRange chip. Zero value is
// the normal attached state; Error marks a chip whose backing reference or
// file vanished (deleted elsewhere, moved, or missing on disk).
type ChipTone uint8

const (
	ChipToneNormal ChipTone = iota
	ChipToneError
)

// AtomicRange is a flat [Start, End) rune range that is treated as an
// indivisible token by the editor (e.g. @reference chips). The cursor
// snaps across it and backspace/delete removes it as a unit.
type AtomicRange struct {
	ID    string
	Start int
	End   int
	Tone  ChipTone
}

// TextAreaSubmitEvent is emitted by the legacy Update bridge when Enter is
// pressed in single-line mode. Component-tree callers should prefer the
// input.submit Action returned by Handle.
type TextAreaSubmitEvent struct {
	Text string
}

// TextArea is the native multi-line text editor component. It owns a
// rune-safe line/col editing engine (the same algorithm that powered
// components.TextInput) and renders directly into a CellBuffer via Paint.
//
// TextArea lives in the foundation package (tui/ui) alongside the
// single-line TextEdit. Higher layers (composer, text overlay, chat input)
// migrate onto this primitive. The engine supports wrap-aware vertical
// navigation, goal-column preservation, multi-line selection, atomic
// ranges, and the full legacy key set.
//
// Chat input has no leading prompt glyph. Callers may set a custom prefix
// via SetPrompt; an empty value paints text from column 0. Wrapped rows
// indent only when a prompt is configured.
//
// This is the Phase 6 multi-line input primitive. It satisfies ui.Component.
type TextArea struct {
	Base
	tokens      *TokenSet
	edit        *textAreaEngine
	placeholder string
	prompt      string
	submitID    string
	multiLine   bool
	readOnly    bool
	bg          cell.Color
	bgSet       bool
	// textWidth is the wrap width for visual line calculation; set during
	// Layout based on the allocated width.
	textWidth int
	// scroll tracks the first visible visual row when content overflows.
	scroll int
	// viewH is the allocated body height, used for scroll clamping.
	viewH int
	// lastInputTime tracks the last edit for cursor solid-phase rendering.
	lastInputTime time.Time
	// hoverFlat is the flat rune index under the pointer (-1 = none). It
	// drives chip hover highlighting; updated on every mouse motion over
	// the text body, independent of selection drags.
	hoverFlat     int
	lastClickTime time.Time
	lastClickX    int
	lastClickY    int
	clickCount    int
}

// NewTextArea creates a multi-line text area. multiLine controls Enter
// behavior: when true, Enter inserts a newline (use the submit action for
// the explicit submit key, typically Ctrl+Enter); when false, Enter emits
// the submit action.
//
// There is no default prompt glyph. Use SetPrompt only for labeled
// fields that need a visible prefix.
func NewTextArea(id ComponentID, tokens *TokenSet, multiLine bool) *TextArea {
	t := &TextArea{
		Base:      NewBase(id),
		tokens:    tokens,
		edit:      newTextAreaEngine(),
		multiLine: multiLine,
		submitID:  "input.submit",
		hoverFlat: -1,
	}
	return t
}

// SetTokens updates the theme token set.
func (t *TextArea) SetTokens(ts *TokenSet) { t.tokens = ts }

// SetPlaceholder sets empty-state hint text.
func (t *TextArea) SetPlaceholder(s string) { t.placeholder = s }

// SetPrompt sets a leading prompt drawn before the first line. An empty
// prompt paints no prefix and no leading space.
func (t *TextArea) SetPrompt(s string) {
	t.prompt = s
	if bounds := t.Bounds(); !bounds.Empty() {
		t.textWidth = bounds.W - t.promptPrefixWidth()
		if t.textWidth < 1 {
			t.textWidth = 1
		}
		t.edit.SetTextWidth(t.textWidth)
		t.clampScroll()
		t.EnsureCursorVisible()
	}
}

// promptChar returns the configured prefix, or empty when none is set.
func (t *TextArea) promptChar() string {
	return t.prompt
}

// promptPrefix returns the full prefix string (glyph + trailing space)
// used for the first visual row, or the continuation indent otherwise.
func (t *TextArea) promptPrefix(isFirst bool) string {
	pc := t.promptChar()
	if pc == "" {
		return ""
	}
	if isFirst {
		return pc + " "
	}
	return strings.Repeat(" ", rendering.VisibleWidth(pc+" "))
}

// PromptPrefixWidth is the visible width of the first-row prefix, or 0
// when the chat input has no prompt glyph.
func (t *TextArea) PromptPrefixWidth() int {
	return t.promptPrefixWidth()
}

func (t *TextArea) promptPrefixWidth() int {
	return rendering.VisibleWidth(t.promptPrefix(true))
}

// SetBackgroundColor overrides the field background fill.
func (t *TextArea) SetBackgroundColor(c cell.Color) { t.bg, t.bgSet = c, true }

// BackgroundColor reports the explicit fill override, if one is set. Deck
// contracts use it to lock flat chrome (no raised popup fill) on embedded
// prompt areas.
func (t *TextArea) BackgroundColor() (cell.Color, bool) { return t.bg, t.bgSet }

// SetSubmitAction sets the action ID emitted on Enter (single-line mode) or
// Ctrl+Enter (multi-line mode).
func (t *TextArea) SetSubmitAction(id string) { t.submitID = id }

// SetMultiLine controls whether Enter inserts a newline or submits.
func (t *TextArea) SetMultiLine(v bool) { t.multiLine = v }

// SetReadOnly freezes the editor: keyboard, paste, and mouse edits are
// ignored while read-only.
func (t *TextArea) SetReadOnly(v bool) { t.readOnly = v }
func (t *TextArea) PasteText(text string) {
	t.edit.InsertString(text)
	t.EnsureCursorVisible()
}
func (t *TextArea) PasteActive() bool { return !t.readOnly }

// Value returns the full text content.
func (t *TextArea) Value() string { return t.edit.Value() }

// SetValue replaces the content and places the cursor at the end.
func (t *TextArea) SetValue(s string) {
	t.edit.SetValue(s)
	t.lastInputTime = time.Now()
	t.EnsureCursorVisible()
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
func (t *TextArea) SetCursorFlat(pos int) {
	t.edit.SetCursorFlat(pos)
	t.EnsureCursorVisible()
}

// CurLine returns the current logical line index.
func (t *TextArea) CurLine() int { return t.edit.CurLine() }

// CurCol returns the current column within the logical line.
func (t *TextArea) CurCol() int { return t.edit.CurCol() }

// CursorVisualLine returns the wrap-aware visual row of the cursor.
func (t *TextArea) CursorVisualLine() int { return t.edit.CursorVisualLine() }

// TotalVisualLines returns the total number of visual rows (wrap-aware).
func (t *TextArea) TotalVisualLines() int { return t.edit.TotalVisualLines() }

func (t *TextArea) ScrollOffset() int { return t.scroll }

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
	t.EnsureCursorVisible()
}

// SetTextWidth sets the wrap width used by both layout queries and the
// editing engine's visual cursor navigation. Keeping these in sync is
// required when keyboard input arrives before the next paint pass.
func (t *TextArea) SetTextWidth(w int) {
	t.textWidth = w
	t.edit.SetTextWidth(w)
	t.clampScroll()
}

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
// SetSelecting sets the selecting flag directly.
func (t *TextArea) SetSelecting(v bool) { t.edit.SetSelecting(v) }

// StopSelecting clears the drag flag but keeps the selection range so
// Ctrl+C can copy after a mouse-release. The selection is cleared on
// the next non-shift cursor movement.
func (t *TextArea) StopSelecting() { t.edit.StopSelecting() }

// FlatToLineCol converts a flat offset to (line, col).
func (t *TextArea) FlatToLineCol(pos int) (int, int) { return t.edit.FlatToLineCol(pos) }

// DeleteRange deletes a flat [start, end) range.
func (t *TextArea) DeleteRange(start, end int) { t.edit.DeleteRange(start, end) }

// SetAtomicRanges sets the atomic (untouchable) ranges for reference chips.
func (t *TextArea) SetAtomicRanges(ranges []AtomicRange) { t.edit.SetAtomicRanges(ranges) }

// AtomicRanges returns a copy of the currently registered atomic ranges,
// including display tone. Attachment flows use it to verify chip state.
func (t *TextArea) AtomicRanges() []AtomicRange {
	return append([]AtomicRange(nil), t.edit.atomicRanges...)
}

// NormalizeCursorAtomic snaps the cursor out of any atomic range.
func (t *TextArea) NormalizeCursorAtomic() { t.edit.NormalizeCursorAtomic() }

// MouseToFlatPos translates a local (x, y) within the painted area to a flat
// rune cursor offset. Returns the end offset if y is below the content.
func (t *TextArea) MouseToFlatPos(localX, localY int) int {
	if t.textWidth < 1 {
		return 0
	}
	// Mouse coordinates are relative to the whole control; the editor
	// text starts after the prompt/continuation prefix.
	textX := localX - t.promptPrefixWidth()
	if textX < 0 {
		textX = 0
	}
	return t.edit.visualToFlat(t.textWidth, textX, localY+t.scroll)
}

// Measure: fill available width, with height based on the available text
// column after the prompt prefix. This must not rely on a previous Layout
// pass: component hosts measure before they assign final bounds.
func (t *TextArea) Measure(_ LayoutContext, cs Constraints) Size {
	tw := cs.MaxW - t.promptPrefixWidth()
	if tw < 1 {
		tw = 1
	}
	h := t.edit.VisualLineCount(tw)
	if h < 1 {
		h = 1
	}
	return cs.ClampSize(Size{W: cs.MaxW, H: h})
}

// Layout stores bounds and recomputes the wrap width.
//
// The wrap width is reduced by the prompt-prefix width (glyph + space,
// or continuation indent) so wrapped rows align with the text column,
// not the prompt glyph. This matches the legacy components.TextInput
// which deducted padLR*2 + 2 from the available width.
func (t *TextArea) Layout(_ LayoutContext, r Rect) {
	t.SetBounds(r)
	prefW := t.promptPrefixWidth()
	tw := r.W - prefW
	if tw < 1 {
		tw = 1
	}
	t.textWidth = tw
	t.viewH = r.H
	t.edit.SetTextWidth(tw)
	t.clampScroll()
	t.EnsureCursorVisible()
}

func (t *TextArea) clampScroll() {
	if t.scroll < 0 {
		t.scroll = 0
	}
	maxScroll := t.maxScroll()
	if t.scroll > maxScroll {
		t.scroll = maxScroll
	}
}

func (t *TextArea) maxScroll() int { return max(0, t.TotalVisualLines()-t.viewH) }

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
//
// Rendering mirrors the legacy components.TextInput.View pipeline:
//   - First visual row: prompt glyph (accent) + space, then text.
//   - Continuation/wrapped rows: 2-space indent, then text.
//   - Text is painted with InputText (Fg) foreground.
//   - Atomic ranges (@reference chips) are painted bold Accent2.
//   - The cursor is an overlay-only reverse-video block: solid for 800ms
//     after input, then blinking by BlinkOnTime. It preserves the grapheme
//     under the cursor and never shifts text or consumes layout width.
//   - Selection is painted with a Selection background.
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
		// FillRect adds r.X/r.Y internally; pass the PAINT origin (not the
		// already-translated ox/oy) so the bg fills the bounds exactly once.
		// Passing ox/oy double-counts the origin and shifts the fill below
		// and right (the textarea's white bg painted over the status bar).
		FillRect(buf, originX, originY, r, cell.BlankCell(bg))
	}

	textW := t.textWidth
	if textW < 1 {
		textW = 1
	}
	prefW := t.promptPrefixWidth()
	textX := ox + prefW
	cursorVisible := ctx.Focused &&
		(time.Since(t.lastInputTime) < 800*time.Millisecond || BlinkOnTime(time.Now()))

	// Placeholder when empty
	if t.edit.Len() == 0 && t.placeholder != "" {
		// First-row prompt glyph
		t.paintPromptPrefix(buf, ox, oy, fg, bg, true)
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
		if cursorVisible {
			PaintBlockCursor(buf, textX, oy, fg, bg)
		}
		return
	}

	selStart, selEnd := int(-1), int(-1)
	if t.edit.HasActiveSelection() {
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
			isFirst := li == 0 && si == 0
			if visRow < t.scroll {
				visRow++
				continue
			}
			screenY := oy + visRow - t.scroll
			if screenY >= oy+r.H {
				return
			}
			// Prompt glyph / continuation indent at the start of the row
			t.paintPromptPrefix(buf, ox, screenY, fg, bg, isFirst)

			segRunes := []rune(seg)
			segStart := rendering.FindSegmentStart(logLine, textW, si)
			lineTokenStart := segStart
			lineTokenEnd := segStart + len(segRunes)
			isCursorSeg := li == t.edit.CurLine() &&
				si == segmentIdxFor(t.edit.CurLine(), t.edit.CurCol(), lines, textW)

			t.paintSegment(buf, textX, screenY, textW, logLine, flatOffset,
				lineTokenStart, lineTokenEnd, isCursorSeg,
				selStart, selEnd, cursorVisible, ctx.Focused,
				fg, bg, selBG)

			visRow++
		}
		flatOffset += len(logLine) + 1
	}

	// Empty buffer: draw prompt + block cursor at the origin
	if visRow == 0 && ctx.Focused {
		if oy < oy+r.H {
			t.paintPromptPrefix(buf, ox, oy, fg, bg, true)
			if cursorVisible {
				PaintBlockCursor(buf, textX, oy, fg, bg)
			}
		}
	}
}

// paintPromptPrefix paints a configured prompt (or continuation indent)
// at the start of a visual row. Empty prompts paint nothing.
func (t *TextArea) paintPromptPrefix(buf *surface.CellBuffer, x, y int, fg, bg cell.Color, isFirst bool) {
	pref := t.promptPrefix(isFirst)
	if pref == "" {
		return
	}
	pf := fg
	if isFirst {
		if pc := t.promptChar(); pc != "" {
			if t.tokens != nil {
				pf = t.tokens.Color(ColorAccent)
			}
			buf.WriteString(x, y, pc, pf, bg, 0)
			// trailing space keeps default fg (or bg fill)
			buf.Set(x+rendering.VisibleWidth(pc), y, cell.NewCell(' ', fg, bg, 0))
			return
		}
	}
	// continuation indent: two spaces
	buf.WriteString(x, y, pref, fg, bg, 0)
}

func (t *TextArea) paintSegment(buf *surface.CellBuffer, x, y, width int, line []rune,
	flatOffset, start, end int, isCursorSeg bool, selStart, selEnd int,
	cursorVisible, focused bool, fg, bg, selBG cell.Color) {

	cursorPos := t.edit.CursorFlat()
	col := 0
	pos := start
	for pos < end && col < width {
		flatPos := flatOffset + pos

		// Atomic range chip: render the whole token at once, then advance.
		if ar, ok := t.edit.atomicRangeStartingAt(flatPos); ok && ar.End <= flatOffset+end {
			labelRunes := line[pos : pos+(ar.End-ar.Start)]
			label := string(labelRunes)
			cursorInChip := focused && isCursorSeg && cursorPos >= ar.Start && cursorPos < ar.End
			col += t.paintAtomicChip(buf, x+col, y, label, ar, cursorInChip,
				cursorVisible, selStart, selEnd, fg, bg, selBG)
			pos += ar.End - ar.Start
			continue
		}

		cluster, clusterW := rendering.FirstGrapheme(string(line[pos:end]))
		if len(cluster) == 0 {
			break
		}
		clusterLen := len([]rune(cluster))
		if clusterLen == 0 {
			break
		}
		inSel := selStart >= 0 && selStart != selEnd &&
			flatPos < selEnd && flatPos+clusterLen > selStart
		cellFG, cellBG := fg, bg
		if inSel {
			cellBG = selBG
		}
		attr := cell.Attrs(0)
		if cursorVisible && focused && isCursorSeg && cursorPos >= flatPos && cursorPos < flatPos+clusterLen {
			attr |= cell.AttrReverse
		}
		buf.WriteString(x+col, y, cluster, cellFG, cellBG, attr)
		if clusterW > 0 {
			col += clusterW
		}
		pos += clusterLen
	}
	// End-of-line block cursor (after the last char on the cursor segment)
	if cursorVisible && focused && isCursorSeg && cursorPos == flatOffset+end {
		if col < width {
			PaintBlockCursor(buf, x+col, y, fg, bg)
		}
	}
}

// paintAtomicChip renders a @reference chip as a filled pill: a Selection
// background across the whole token with bold Accent2 text, so attachments
// read as blocks rather than plain prose. Error-tone chips (backing file or
// reference gone) switch to ColorError text. Hovering highlights the pill
// background. A visible text cursor reverses only the grapheme under it.
func (t *TextArea) paintAtomicChip(buf *surface.CellBuffer, x, y int, label string,
	ar AtomicRange, cursorInChip, cursorVisible bool,
	selStart, selEnd int, fg, bg, selBG cell.Color) int {
	chipFG := fg
	chipBG := bg
	if t.tokens != nil {
		chipFG = t.tokens.Color(ColorAccent2)
		chipBG = t.tokens.Color(ColorSelection)
		if ar.Tone == ChipToneError {
			chipFG = t.tokens.Color(ColorError)
		}
	}
	if t.hoverFlat >= ar.Start && t.hoverFlat < ar.End && t.tokens != nil {
		chipBG = t.tokens.Color(ColorHover)
	}
	cursorPos := t.edit.CursorFlat()
	col := 0
	pos := 0
	for len(label) > 0 {
		cluster, clusterW := rendering.FirstGrapheme(label)
		if len(cluster) == 0 {
			break
		}
		clusterLen := len([]rune(cluster))
		cellFG, cellBG := chipFG, chipBG
		if selStart >= 0 && selStart != selEnd &&
			ar.Start+pos < selEnd && ar.Start+pos+clusterLen > selStart {
			cellBG = selBG
		}
		attr := cell.AttrBold
		if cursorVisible && cursorInChip &&
			cursorPos >= ar.Start+pos && cursorPos < ar.Start+pos+clusterLen {
			attr |= cell.AttrReverse
		}
		buf.WriteString(x+col, y, cluster, cellFG, cellBG, attr)
		if clusterW > 0 {
			col += clusterW
		}
		pos += clusterLen
		label = label[len(cluster):]
	}
	return col
}

// Handle processes keyboard, paste, and mouse events.
func (t *TextArea) Handle(ctx EventContext, evt Event) EventResult {
	if t.readOnly {
		return EventResult{}
	}
	switch evt.Kind {
	case EventKey:
		return t.handleKey(evt.Key)
	case EventCustom:
		if paste, ok := evt.Custom.(tea.PasteMsg); ok {
			t.edit.InsertString(paste.Content)
			t.lastInputTime = time.Now()
			t.EnsureCursorVisible()
			return EventResult{Handled: true, Invalidate: InvalidatePaint}
		}
	case EventMouseClick:
		if evt.MouseClick.Button != tea.MouseLeft {
			return EventResult{}
		}
		pos := t.MouseToFlatPos(ctx.LocalX, ctx.LocalY)
		if pos >= 0 {
			now := time.Now()
			if now.Sub(t.lastClickTime) <= 500*time.Millisecond &&
				intAbs(ctx.LocalX-t.lastClickX) <= 1 && intAbs(ctx.LocalY-t.lastClickY) <= 1 {
				t.clickCount++
			} else {
				t.clickCount = 1
			}
			t.lastClickTime, t.lastClickX, t.lastClickY = now, ctx.LocalX, ctx.LocalY
			switch {
			case t.clickCount >= 3:
				t.edit.selectLineAt(pos)
				t.clickCount = 0
			case t.clickCount == 2:
				t.edit.selectWordAt(pos)
			case evt.MouseClick.Mod.Contains(tea.ModShift):
				if !t.edit.IsSelecting() {
					t.edit.SetSelection(t.edit.CursorFlat(), pos)
				} else {
					t.edit.UpdateSelectionEnd(pos)
				}
				t.edit.SetCursorFlat(pos)
			default:
				t.edit.SetSelection(pos, pos)
				t.edit.SetCursorFlat(pos)
			}
			t.edit.SetSelecting(true)
		}
		return EventResult{Handled: true, Invalidate: InvalidatePaint | InvalidateFocus}
	case EventMouseMotion:
		if t.edit.IsSelecting() {
			if ctx.LocalY < 0 && t.scroll > 0 {
				t.scroll--
				ctx.LocalY = 0
			} else if ctx.LocalY >= t.viewH && t.scroll < t.maxScroll() {
				t.scroll++
				ctx.LocalY = max(0, t.viewH-1)
			}
		}
		pos := t.MouseToFlatPos(ctx.LocalX, ctx.LocalY)
		if pos != t.hoverFlat {
			t.hoverFlat = pos
		}
		if pos >= 0 && t.edit.IsSelecting() {
			t.edit.UpdateSelectionEnd(pos)
		}
		return EventResult{Handled: true, Invalidate: InvalidatePaint}
	case EventMouseRelease:
		if t.edit.IsSelecting() {
			t.edit.StopSelecting()
		}
		return EventResult{Handled: true, Invalidate: InvalidatePaint}
	case EventMouseWheel:
		before := t.scroll
		switch evt.MouseWheel.Button {
		case tea.MouseWheelUp:
			t.scroll = max(0, t.scroll-3)
		case tea.MouseWheelDown:
			t.scroll = min(t.maxScroll(), t.scroll+3)
		default:
			return EventResult{}
		}
		return EventResult{Handled: before != t.scroll, Invalidate: InvalidatePaint}
	}
	return EventResult{}
}

func (t *TextArea) CancelPointerCapture() { t.edit.StopSelecting() }

func (t *TextArea) handleKey(m tea.KeyPressMsg) EventResult {
	primary := m.Mod.Contains(tea.ModCtrl) || m.Mod.Contains(tea.ModSuper)
	if primary {
		switch m.Code {
		case 'c':
			if selected := t.edit.SelectedText(); selected != "" {
				return EventResult{Handled: true, Cmd: rendering.CopyToClipboardCmd(selected)}
			}
			return EventResult{}
		case 'x':
			if !t.edit.HasActiveSelection() {
				return EventResult{Handled: true}
			}
			selected := t.edit.SelectedText()
			t.edit.deleteActiveSelectionWithHistory()
			t.EnsureCursorVisible()
			return EventResult{Handled: true, Cmd: rendering.CopyToClipboardCmd(selected), Invalidate: InvalidatePaint}
		case 'v':
			return EventResult{Handled: true, Action: &Action{ID: ActionTextPaste, Payload: ClipboardPasteTarget(t)}}
		}
	}
	if m.Code == tea.KeyPgUp || m.Code == tea.KeyPgDown {
		delta := max(1, t.viewH-1)
		if m.Code == tea.KeyPgUp {
			delta = -delta
		}
		t.edit.moveVisualRows(delta, m.Mod.Contains(tea.ModShift))
		t.EnsureCursorVisible()
		return EventResult{Handled: true, Invalidate: InvalidatePaint}
	}
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
// components.TextInput.Update signature for bridge callers. Single-line
// Enter returns TextAreaSubmitEvent; multi-line Enter inserts a newline.
func (t *TextArea) Update(msg tea.Msg, disabled bool) tea.Cmd {
	if disabled {
		return nil
	}
	if key, ok := msg.(tea.KeyPressMsg); ok {
		if key.Code == tea.KeyEnter && !t.multiLine && !key.Mod.Contains(tea.ModAlt) {
			return func() tea.Msg { return TextAreaSubmitEvent{Text: t.Value()} }
		}
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
	undo          []textAreaSnapshot
	redo          []textAreaSnapshot
}

type textAreaSnapshot struct {
	value               string
	cursor, anchor, end int
	selecting           bool
}

func newTextAreaEngine() *textAreaEngine { return &textAreaEngine{buf: [][]rune{{}}} }

// beginSelectionIfShift starts or extends a selection when Shift is held.
// When no selection is active and Shift is pressed, it anchors at the
// current cursor position. When a selection is already active and Shift
// is pressed, the anchor is preserved and only the cursor moves.
func (e *textAreaEngine) beginSelectionIfShift(mod tea.KeyMod) {
	if !mod.Contains(tea.ModShift) {
		return
	}
	if !e.selecting {
		pos := e.CursorFlat()
		e.selecting = true
		e.selAnchor = pos
		e.selEnd = pos
	}
}

// finishMovementIfNoShift clears the selection when no Shift modifier is
// held, so plain arrow movements collapse any active selection.
func (e *textAreaEngine) finishMovementIfNoShift(mod tea.KeyMod) {
	if !mod.Contains(tea.ModShift) && e.selecting {
		e.selecting = false
		e.selAnchor = 0
		e.selEnd = 0
	}
}

func (e *textAreaEngine) handleKey(msg tea.KeyPressMsg) {
	shift := msg.Mod.Contains(tea.ModShift)
	primary := msg.Mod.Contains(tea.ModCtrl) || msg.Mod.Contains(tea.ModSuper)
	word := primary || msg.Mod.Contains(tea.ModAlt)
	if primary && msg.Code == 'z' {
		if shift {
			e.Redo()
		} else {
			e.Undo()
		}
		return
	}
	if primary && msg.Code == 'y' {
		e.Redo()
		return
	}
	before := e.snapshot()
	beforeValue := before.value
	if e.HasActiveSelection() && (msg.Code == tea.KeyEnter || (!primary && msg.Text != "")) {
		start, end := e.SelectionRange()
		e.deleteRange(start, end)
		e.setCursorFlat(start)
		e.ClearSelection()
	}

	switch msg.Code {
	case tea.KeyEnter:
		if shift {
			// Shift+Enter: insert newline (same as plain Enter in multi-line)
			e.splitLineAt(e.curLine, e.curCol)
			e.curLine++
			e.curCol = 0
			break
		}
		e.selecting = false
		e.selAnchor = 0
		e.selEnd = 0
		e.splitLineAt(e.curLine, e.curCol)
		e.curLine++
		e.curCol = 0
	case tea.KeyBackspace:
		if word && !e.HasActiveSelection() {
			pos := e.cursorPrevWord()
			cf := e.CursorFlat()
			e.deleteRange(pos, cf)
			e.setCursorFlat(pos)
			break
		}
		if e.selecting && !shift {
			s, end := e.SelectionRange()
			e.deleteRange(s, end)
			e.setCursorFlat(s)
			e.selecting = false
			e.selAnchor = 0
			e.selEnd = 0
			break
		}
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
		e.selecting = false
		e.selAnchor = 0
		e.selEnd = 0
	case tea.KeyDelete:
		if word && !e.HasActiveSelection() {
			pos := e.CursorFlat()
			e.deleteRange(pos, e.cursorNextDeleteWord())
			e.setCursorFlat(pos)
			break
		}
		if e.selecting && !shift {
			s, end := e.SelectionRange()
			e.deleteRange(s, end)
			e.setCursorFlat(s)
			e.selecting = false
			e.selAnchor = 0
			e.selEnd = 0
			break
		}
		pos := e.lineColToFlat(e.curLine, e.curCol)
		if pos < e.Len() {
			if ar, ok := e.atomicRangeAround(pos); ok {
				e.deleteRange(ar.Start, ar.End)
				e.setCursorFlat(ar.Start)
				break
			}
			e.deleteForwardAt(pos)
		}
		e.selecting = false
		e.selAnchor = 0
		e.selEnd = 0
	case tea.KeyUp:
		_, visCol := e.cursorVisualInfo()
		e.lastVisCol = visCol
		e.beginSelectionIfShift(msg.Mod)
		e.cursorUpVisual()
		if e.selecting {
			e.selEnd = e.CursorFlat()
		}
		e.finishMovementIfNoShift(msg.Mod)
	case tea.KeyDown:
		_, visCol := e.cursorVisualInfo()
		e.lastVisCol = visCol
		e.beginSelectionIfShift(msg.Mod)
		e.cursorDownVisual()
		if e.selecting {
			e.selEnd = e.CursorFlat()
		}
		e.finishMovementIfNoShift(msg.Mod)
	case tea.KeyLeft:
		e.beginSelectionIfShift(msg.Mod)
		if word {
			e.setCursorFlat(e.cursorPrevWord())
		} else if e.curCol > 0 {
			e.curCol--
		} else if e.curLine > 0 {
			e.curLine--
			e.curCol = len(e.buf[e.curLine])
		}
		e.snapCursorAcrossAtomic(false)
		e.lastVisCol = -1
		if e.selecting {
			e.selEnd = e.CursorFlat()
		}
		e.finishMovementIfNoShift(msg.Mod)
	case tea.KeyRight:
		e.beginSelectionIfShift(msg.Mod)
		if word {
			e.setCursorFlat(e.cursorNextWord())
		} else if e.curCol < len(e.buf[e.curLine]) {
			e.curCol++
		} else if e.curLine < len(e.buf)-1 {
			e.curLine++
			e.curCol = 0
		}
		e.snapCursorAcrossAtomic(true)
		e.lastVisCol = -1
		if e.selecting {
			e.selEnd = e.CursorFlat()
		}
		e.finishMovementIfNoShift(msg.Mod)
	case tea.KeyHome:
		e.beginSelectionIfShift(msg.Mod)
		if primary {
			e.curLine = 0
		}
		e.curCol = 0
		e.lastVisCol = -1
		if e.selecting {
			e.selEnd = e.CursorFlat()
		}
		e.finishMovementIfNoShift(msg.Mod)
	case tea.KeyEnd:
		e.beginSelectionIfShift(msg.Mod)
		if primary {
			e.curLine = len(e.buf) - 1
			if e.curLine < 0 {
				e.curLine = 0
			}
		}
		e.curCol = len(e.buf[e.curLine])
		e.lastVisCol = -1
		if e.selecting {
			e.selEnd = e.CursorFlat()
		}
		e.finishMovementIfNoShift(msg.Mod)
	default:
		if primary && len(msg.Text) == 0 {
			switch msg.Code {
			case 'a':
				// Ctrl+A: select all (unless Shift also held — in that
				// case the legacy binding to go to line start still
				// applies, so honour shift+ctrl+a as select-all too).
				total := e.Len()
				e.selecting = true
				e.selAnchor = 0
				e.selEnd = total
				e.setCursorFlat(total)
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
				e.selecting = false
				e.selAnchor = 0
				e.selEnd = 0
			case 'k':
				end := e.cursorLineEnd()
				cf := e.lineColToFlat(e.curLine, e.curCol)
				if end > cf {
					e.deleteRange(cf, end)
				}
				e.selecting = false
				e.selAnchor = 0
				e.selEnd = 0
			case 'w':
				if e.lineColToFlat(e.curLine, e.curCol) > 0 {
					pos := e.cursorPrevWord()
					cf := e.lineColToFlat(e.curLine, e.curCol)
					e.deleteRange(pos, cf)
					e.setCursorFlat(pos)
				}
				e.selecting = false
				e.selAnchor = 0
				e.selEnd = 0
			}
		} else if msg.Mod.Contains(tea.ModAlt) && msg.Code == '\\' {
			e.splitLineAt(e.curLine, e.curCol)
			e.curLine++
			e.curCol = 0
		} else {
			for _, r := range msg.Text {
				if isEditableRune(r) {
					pos := e.lineColToFlat(e.curLine, e.curCol)
					e.insertRuneAt(pos, r)
					e.curCol++
				}
			}
			e.selecting = false
			e.selAnchor = 0
			e.selEnd = 0
		}
	}
	e.lastInputTime = time.Now()
	e.frame++
	if e.Value() != beforeValue {
		e.undo = append(e.undo, before)
		if len(e.undo) > 200 {
			e.undo = e.undo[len(e.undo)-200:]
		}
		e.redo = nil
	}
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
	e.selecting = false
	e.selAnchor, e.selEnd = 0, 0
	e.undo = nil
	e.redo = nil
}

func (e *textAreaEngine) InsertString(s string) {
	before := e.snapshot()
	if e.HasActiveSelection() {
		start, end := e.SelectionRange()
		e.deleteRange(start, end)
		e.setCursorFlat(start)
		e.ClearSelection()
	}
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
	if e.Value() != before.value {
		e.undo = append(e.undo, before)
		e.redo = nil
	}
}

func (e *textAreaEngine) Clear() {
	e.buf = [][]rune{{}}
	e.curLine = 0
	e.curCol = 0
	e.selecting = false
	e.selAnchor = 0
	e.selEnd = 0
	e.undo = nil
	e.redo = nil
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

func (e *textAreaEngine) cursorNextDeleteWord() int {
	pos := e.CursorFlat()
	buf := []rune(e.Value())
	if pos >= len(buf) {
		return len(buf)
	}
	if unicode.IsSpace(buf[pos]) {
		for pos < len(buf) && unicode.IsSpace(buf[pos]) {
			pos++
		}
		return pos
	}
	for pos < len(buf) && !unicode.IsSpace(buf[pos]) {
		pos++
	}
	return pos
}

func (e *textAreaEngine) cursorLineStart() int { return e.lineColToFlat(e.curLine, 0) }
func (e *textAreaEngine) cursorLineEnd() int {
	return e.lineColToFlat(e.curLine, len(e.buf[e.curLine]))
}

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
	s, e2 := e.SelectionRange()
	if s == e2 {
		return ""
	}
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

func (e *textAreaEngine) snapshot() textAreaSnapshot {
	return textAreaSnapshot{
		value: e.Value(), cursor: e.CursorFlat(), anchor: e.selAnchor,
		end: e.selEnd, selecting: e.selecting,
	}
}

func (e *textAreaEngine) restore(snapshot textAreaSnapshot) {
	lines := strings.Split(snapshot.value, "\n")
	e.buf = make([][]rune, len(lines))
	for i, line := range lines {
		e.buf[i] = []rune(line)
	}
	if len(e.buf) == 0 {
		e.buf = [][]rune{{}}
	}
	e.setCursorFlat(snapshot.cursor)
	e.selAnchor, e.selEnd, e.selecting = snapshot.anchor, snapshot.end, snapshot.selecting
}

func (e *textAreaEngine) Undo() bool {
	if len(e.undo) == 0 {
		return false
	}
	e.redo = append(e.redo, e.snapshot())
	last := len(e.undo) - 1
	e.restore(e.undo[last])
	e.undo = e.undo[:last]
	return true
}

func (e *textAreaEngine) Redo() bool {
	if len(e.redo) == 0 {
		return false
	}
	e.undo = append(e.undo, e.snapshot())
	last := len(e.redo) - 1
	e.restore(e.redo[last])
	e.redo = e.redo[:last]
	return true
}

func (e *textAreaEngine) deleteActiveSelectionWithHistory() bool {
	if !e.HasActiveSelection() {
		return false
	}
	e.undo = append(e.undo, e.snapshot())
	e.redo = nil
	start, end := e.SelectionRange()
	e.deleteRange(start, end)
	e.setCursorFlat(start)
	e.ClearSelection()
	return true
}

func (e *textAreaEngine) selectWordAt(pos int) {
	runes := []rune(e.Value())
	if len(runes) == 0 {
		e.SetSelection(0, 0)
		return
	}
	pos = min(max(0, pos), len(runes)-1)
	class := runeClass(runes[pos])
	start, end := pos, pos+1
	for start > 0 && runeClass(runes[start-1]) == class {
		start--
	}
	for end < len(runes) && runeClass(runes[end]) == class {
		end++
	}
	e.SetSelection(start, end)
	e.setCursorFlat(end)
}

func (e *textAreaEngine) selectLineAt(pos int) {
	line, _ := e.flatToLineCol(pos)
	start := e.lineColToFlat(line, 0)
	end := e.lineColToFlat(line, len(e.buf[line]))
	if line < len(e.buf)-1 {
		end++
	}
	e.SetSelection(start, end)
	e.setCursorFlat(end)
}

func (e *textAreaEngine) moveVisualRows(delta int, extend bool) {
	if delta == 0 {
		return
	}
	if extend && !e.selecting {
		pos := e.CursorFlat()
		e.SetSelection(pos, pos)
	}
	_, col := e.cursorVisualInfo()
	e.lastVisCol = col
	for range intAbs(delta) {
		if delta < 0 {
			e.cursorUpVisual()
		} else {
			e.cursorDownVisual()
		}
	}
	if e.selecting {
		e.selEnd = e.CursorFlat()
	}
	if !extend {
		e.ClearSelection()
	}
}

// StopSelecting keeps the selection range but clears the mouse-drag
// flag, so the selection survives a mouse release and Ctrl+C can copy it.
// The selection is cleared on the next non-shift cursor movement.
func (e *textAreaEngine) StopSelecting() { e.selecting = false }

func (e *textAreaEngine) FlatToLineCol(pos int) (int, int) { return e.flatToLineCol(pos) }
func (e *textAreaEngine) SetCursorFlat(pos int)            { e.setCursorFlat(pos) }
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
