package ui

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
)

// TextField is a single-line text input built on TextEdit.
// It is the canonical replacement for ad-hoc filter strings and hand-rolled
// prompt editors.
type TextField struct {
	Base
	edit        *TextEdit
	placeholder string
	prompt      string // optional prompt glyph/string (not part of value)
	promptFG    cell.Color
	promptFGSet bool
	promptAtEnd bool
	readOnly    bool
	disabled    bool
	password    bool
	submitID    string // action ID on Enter; empty = no submit action
	// horizontal viewport for long values
	viewStart int
	// viewReset shows the HEAD of a programmatically replaced value until the
	// user interacts. Without it, SetValue (cursor placed at end-of-value)
	// scrolls a long URL to its tail, hiding the changed part — e.g. a
	// provider switch looked like "the base URL just has a 1 in it".
	viewReset     bool
	bg            cell.Color
	bgSet         bool
	tokens        *TokenSet
	selecting     bool
	cursor        CursorBlinkState
	lastClickTime time.Time
	lastClickX    int
	clickCount    int
	// dragMoved records that the pointer actually moved during the active
	// drag, so copy-on-release only fires for a real drag (a click that
	// extends a keyboard selection keeps its selection).
	dragMoved bool
	// clipboardCopy builds the command that writes text to the system
	// clipboard. Hosts may inject their own write path; tests inject a
	// recorder so copy payloads are asserted without touching the clipboard.
	clipboardCopy func(string) tea.Cmd
}

// NewTextField creates a single-line field.
func NewTextField(id ComponentID, tokens *TokenSet) *TextField {
	return &TextField{
		Base:     NewBase(id),
		edit:     NewTextEdit(),
		submitID: "input.submit",
		tokens:   tokens,
	}
}

// SetTokens updates the theme token set.
func (t *TextField) SetTokens(ts *TokenSet) { t.tokens = ts }

// SetPlaceholder sets empty-state hint text.
func (t *TextField) SetPlaceholder(s string) { t.placeholder = s }

// SetPrompt sets a prompt glyph/string (not part of value). By default it
// paints at the start of the field; call SetPromptPlacement(PromptEnd) for
// trailing search glyphs.
func (t *TextField) SetPrompt(s string) { t.prompt = s }

// PromptPlacement selects whether the prompt paints before or after the value.
type PromptPlacement int

const (
	PromptStart PromptPlacement = iota
	PromptEnd
)

// SetPromptPlacement puts the prompt at the start (default) or end of the field.
func (t *TextField) SetPromptPlacement(p PromptPlacement) { t.promptAtEnd = p == PromptEnd }

// SetPromptColor overrides the prompt foreground color.
func (t *TextField) SetPromptColor(c cell.Color) { t.promptFG, t.promptFGSet = c, true }

// ClearPromptColor restores the default prompt foreground.
func (t *TextField) ClearPromptColor() { t.promptFG, t.promptFGSet = cell.NoColor, false }

// SetBackgroundColor overrides the field background fill.
func (t *TextField) SetBackgroundColor(c cell.Color) { t.bg, t.bgSet = c, true }

// ClearBackgroundColor removes the background fill override.
func (t *TextField) ClearBackgroundColor() { t.bg, t.bgSet = cell.NoColor, false }

// SetSubmitAction sets the action ID emitted on Enter.
func (t *TextField) SetSubmitAction(id string) { t.submitID = id }

// SetReadOnly toggles read-only mode. Read-only values display from their
// beginning rather than tracking the editing cursor at end-of-value.
func (t *TextField) SetReadOnly(v bool) {
	t.readOnly = v
	if v {
		t.viewStart = 0
	}
}

// SetDisabled toggles disabled mode.
func (t *TextField) SetDisabled(v bool) { t.disabled = v }

// SetPassword masks painted characters.
func (t *TextField) SetPassword(v bool) { t.password = v }

// SetValue replaces the field value and shows its start until the user
// interacts (typing, cursor motion, or click take over the viewport).
func (t *TextField) SetValue(s string) {
	t.edit.SetValue(s)
	t.viewStart = 0
	t.viewReset = true
	t.cursor.MarkActivity()
}

// Value returns the field value.
func (t *TextField) Value() string { return t.edit.Value() }

// SyncEditFrom copies value, cursor, and selection from ed into this field.
func (t *TextField) SyncEditFrom(ed *TextEdit) {
	if ed == nil {
		return
	}
	ed.CopyStateTo(t.edit)
	t.ensureCursorVisible()
}

// Edit exposes the underlying model for advanced callers/tests.
func (t *TextField) Edit() *TextEdit      { return t.edit }
func (t *TextField) HasSelection() bool   { return t.edit.HasSelection() }
func (t *TextField) SelectedText() string { return t.edit.SelectedText() }
func (t *TextField) IsSelecting() bool    { return t.selecting }
func (t *TextField) CancelPointerCapture() {
	t.selecting = false
	t.dragMoved = false
}
func (t *TextField) PasteText(text string) { t.edit.InsertRunes(text); t.ensureCursorVisible() }
func (t *TextField) PasteActive() bool     { return !t.disabled && !t.readOnly }
func (t *TextField) NeedsFrame() bool      { return !t.disabled }

// ViewStart returns the horizontal viewport offset (visible runes).
func (t *TextField) ViewStart() int { return t.viewStart }

// CursorVisible reports whether the editing cursor lies inside the
// horizontal viewport for the current bounds.
func (t *TextField) CursorVisible() bool {
	cw := t.contentWidth()
	if cw <= 0 {
		return true
	}
	cur := t.edit.Cursor()
	if cur < t.viewStart {
		return false
	}
	runes := t.displayRunes()
	cur = min(cur, len(runes))
	return rendering.VisibleWidth(string(runes[t.viewStart:cur])) < cw
}

// Measure: one row, full available width.
func (t *TextField) Measure(_ LayoutContext, cs Constraints) Size {
	return cs.ClampSize(Size{W: cs.MaxW, H: 1})
}

func (t *TextField) Layout(_ LayoutContext, r Rect) {
	t.SetBounds(r)
	if t.readOnly {
		t.viewStart = 0
		t.viewReset = false
	} else if !t.viewReset {
		t.ensureCursorVisible()
	}
}

func (t *TextField) promptWidth() int {
	return rendering.VisibleWidth(t.prompt)
}

func (t *TextField) contentX() int {
	if t.promptAtEnd {
		return 0
	}
	return t.promptWidth()
}

func (t *TextField) contentWidth() int {
	b := t.Bounds()
	w := b.W - t.promptWidth()
	if w < 0 {
		return 0
	}
	return w
}

func (t *TextField) displayRunes() []rune {
	if t.password {
		n := t.edit.Len()
		out := make([]rune, n)
		for i := range out {
			out[i] = '•'
		}
		return out
	}
	return []rune(t.edit.Value())
}

func (t *TextField) ensureCursorVisible() {
	cw := t.contentWidth()
	if cw <= 0 {
		t.viewStart = 0
		return
	}
	cur := t.edit.Cursor()
	if cur < t.viewStart {
		t.viewStart = cur
	}
	runes := t.displayRunes()
	cur = min(cur, len(runes))
	for t.viewStart < cur && rendering.VisibleWidth(string(runes[t.viewStart:cur])) >= cw {
		t.viewStart++
	}
	if t.viewStart < 0 {
		t.viewStart = 0
	}
	if t.viewStart > len(runes) {
		t.viewStart = len(runes)
	}
}

// Paint renders prompt, value or placeholder, selection, and inverse cursor.
func (t *TextField) Paint(buf *surface.CellBuffer, originX, originY int, ctx PaintContext) {
	r := t.Bounds()
	if r.Empty() {
		return
	}
	st := ResolveControlState(t.disabled, ctx.Focused, false, false, ctx.Hovered)
	fg, bg, _ := cell.NoColor, cell.NoColor, cell.NoColor
	if t.tokens != nil {
		fg, bg, _ = t.tokens.InputColors(st)
	}
	cursorFG := fg
	if t.tokens != nil {
		cursorFG = t.tokens.Color(ColorCursor)
	}
	if t.bgSet {
		bg = t.bg
	}
	if !bg.IsDefault() {
		FillRect(buf, originX, originY, r, cell.BlankCell(bg))
	}
	x := originX + r.X
	y := originY + r.Y
	promptW := t.promptWidth()
	contentX := x
	if t.prompt != "" && !t.promptAtEnd {
		contentX = x + promptW
	}

	if t.prompt != "" {
		pf := fg
		if t.promptFGSet {
			pf = t.promptFG
		} else if t.tokens != nil {
			pf = t.tokens.Color(ColorAccent)
		}
		px := x
		if t.promptAtEnd {
			px = x + max(0, r.W-promptW)
		}
		buf.WriteString(px, y, t.prompt, pf, bg, 0)
	}

	cw := t.contentWidth()
	if cw <= 0 {
		return
	}
	x = contentX

	// Placeholder when empty and not focused-with-cursor-only display
	if t.edit.Len() == 0 && t.placeholder != "" {
		ph := t.placeholder
		if rendering.VisibleWidth(ph) > cw {
			// crude truncate by runes
			runes := []rune(ph)
			if len(runes) > cw {
				runes = runes[:cw]
			}
			ph = string(runes)
		}
		phFG := fg
		if t.tokens != nil {
			phFG = t.tokens.Color(ColorMuted)
		}
		buf.WriteString(x, y, rendering.TruncateVisual(ph, cw, ""), phFG, bg, cell.AttrItalic)
		if ctx.Focused && !t.disabled && t.cursor.Visible(ctx.Frame) {
			PaintBlockCursor(buf, x, y, cursorFG, bg)
		}
		return
	}

	runes := t.displayRunes()
	if rendering.VisibleWidth(string(runes)) < cw {
		t.viewStart = 0
	}
	if t.viewStart > len(runes) {
		t.viewStart = len(runes)
	}
	selA, selB := t.edit.SelectionRange()
	selBG := bg
	if t.tokens != nil {
		selBG = t.tokens.Color(ColorSelection)
	}
	col := 0
	pos := t.viewStart
	for pos < len(runes) && col < cw {
		cluster, width := rendering.FirstGrapheme(string(runes[pos:]))
		if cluster == "" || width < 0 || col+width > cw {
			break
		}
		clusterLen := len([]rune(cluster))
		cellFG, cellBG := fg, bg
		inSel := pos < selB && pos+clusterLen > selA && selA != selB
		if inSel {
			cellBG = selBG
		}
		buf.WriteString(x+col, y, cluster, cellFG, cellBG, 0)
		col += width
		pos += clusterLen
	}
	if ctx.Focused && !t.disabled && t.cursor.Visible(ctx.Frame) {
		cur := min(t.edit.Cursor(), len(runes))
		cx := rendering.VisibleWidth(string(runes[t.viewStart:cur]))
		if cx >= 0 && cx < cw {
			PaintBlockCursor(buf, x+cx, y, cursorFG, bg)
		}
	}
}

// Handle implements keyboard and paste editing.
func (t *TextField) Handle(ctx EventContext, evt Event) EventResult {
	if t.disabled {
		return EventResult{}
	}
	// Any user interaction hands the viewport back to the cursor.
	t.viewReset = false
	switch evt.Kind {
	case EventKey:
		result := t.handleKey(evt.Key)
		if result.Handled {
			t.cursor.MarkActivity()
		}
		return result
	case EventCustom:
		if paste, ok := evt.Custom.(tea.PasteMsg); ok {
			if t.readOnly {
				return EventResult{Handled: true}
			}
			t.edit.InsertRunes(paste.Content)
			t.cursor.MarkActivity()
			t.ensureCursorVisible()
			return EventResult{Handled: true, Invalidate: InvalidatePaint}
		}
	case EventMouseClick:
		if evt.MouseClick.Button != tea.MouseLeft {
			return EventResult{}
		}
		lx := ctx.LocalX - t.contentX()
		if t.promptAtEnd && t.promptWidth() > 0 && ctx.LocalX >= t.Bounds().W-t.promptWidth() {
			lx = t.contentWidth()
		}
		if lx < 0 {
			lx = 0
		}
		idx := t.indexAtContentX(lx)
		now := time.Now()
		if now.Sub(t.lastClickTime) <= 500*time.Millisecond && intAbs(ctx.LocalX-t.lastClickX) <= 1 {
			t.clickCount++
		} else {
			t.clickCount = 1
		}
		t.lastClickTime, t.lastClickX = now, ctx.LocalX
		switch {
		case t.clickCount >= 3:
			t.edit.SelectAll()
			t.clickCount = 0
		case t.clickCount == 2:
			t.edit.SelectWordAt(idx)
		case evt.MouseClick.Mod.Contains(tea.ModShift):
			t.edit.SetCursor(idx, true)
		default:
			t.edit.SetCursor(idx, false)
			t.edit.SetCursor(idx, true)
		}
		t.selecting = true
		t.dragMoved = false
		t.cursor.MarkActivity()
		t.ensureCursorVisible()
		return EventResult{Handled: true, Action: &Action{ID: "ui.capture"}, Invalidate: InvalidatePaint | InvalidateFocus}
	case EventMouseMotion:
		if !t.selecting {
			return EventResult{}
		}
		t.dragMoved = true
		lx := ctx.LocalX - t.contentX()
		if lx < 0 && t.viewStart > 0 {
			t.viewStart--
			lx = 0
		} else if lx >= t.contentWidth() && t.viewStart < t.edit.Len() {
			t.viewStart++
			lx = max(0, t.contentWidth()-1)
		}
		t.edit.SetCursor(t.indexAtContentX(lx), true)
		t.ensureCursorVisible()
		return EventResult{Handled: true, Invalidate: InvalidatePaint}
	case EventMouseRelease:
		if !t.selecting {
			return EventResult{}
		}
		return EventResult{
			Handled:    true,
			Cmd:        t.EndMouseSelection(),
			Invalidate: InvalidatePaint,
		}
	}
	return EventResult{}
}

// TextValue returns the entire value (TextClipboardTarget).
func (t *TextField) TextValue() string { return t.edit.Value() }

// TextEditable reports whether edits are allowed (TextClipboardTarget).
func (t *TextField) TextEditable() bool { return !t.readOnly && !t.disabled }

// TextSecret reports a masked field: clipboard commands are refused so a
// rewritten key can never be exfiltrated through the clipboard
// (TextClipboardTarget).
func (t *TextField) TextSecret() bool { return t.password }

// ReplaceTextValue replaces the entire value, parking the cursor at the end
// (TextClipboardTarget). Read-only and disabled fields ignore the call.
func (t *TextField) ReplaceTextValue(v string) {
	if !t.TextEditable() {
		return
	}
	t.SetValue(v)
}

// TextSelection returns the active selected text (TextClipboardTarget).
func (t *TextField) TextSelection() (string, bool) {
	if !t.edit.HasSelection() {
		return "", false
	}
	return t.edit.SelectedText(), true
}

// ClearTextSelection drops the selection without touching the text
// (TextClipboardTarget).
func (t *TextField) ClearTextSelection() { t.edit.ClearSelection() }

// DeleteTextSelection deletes the active selection and reports whether
// anything was deleted (TextClipboardTarget).
func (t *TextField) DeleteTextSelection() bool {
	if !t.TextEditable() || !t.edit.DeleteSelection() {
		return false
	}
	t.ensureCursorVisible()
	return true
}

// EndMouseSelection finishes a pointer drag: a real drag copies the selected
// text to the clipboard and drops the selection (copy-on-release, matching the
// chat message area and the terminals). A press that never moved only stops
// the drag so keyboard-style extending keeps its selection.
func (t *TextField) EndMouseSelection() tea.Cmd {
	if !t.selecting {
		return nil
	}
	wasDrag := t.dragMoved
	t.selecting = false
	t.dragMoved = false
	if !wasDrag {
		return nil
	}
	selected, ok := t.TextSelection()
	if !ok {
		return nil
	}
	t.edit.ClearSelection()
	if t.TextSecret() {
		return nil
	}
	return t.clipboardCopyFn()(selected)
}

// SetClipboardCopy overrides how copy commands are built (tests inject a
// recorder; hosts may route copies through their own write path). Passing nil
// restores the system-clipboard writer.
func (t *TextField) SetClipboardCopy(fn func(string) tea.Cmd) {
	if fn == nil {
		fn = rendering.CopyToClipboardCmd
	}
	t.clipboardCopy = fn
}

// clipboardCopyFn returns the active clipboard writer.
func (t *TextField) clipboardCopyFn() func(string) tea.Cmd {
	if t.clipboardCopy == nil {
		return rendering.CopyToClipboardCmd
	}
	return t.clipboardCopy
}

func (t *TextField) indexAtContentX(x int) int {
	x = max(0, x)
	runes := t.displayRunes()
	if t.viewStart >= len(runes) {
		return t.edit.Len()
	}
	return min(t.edit.Len(), t.viewStart+rendering.ColToRuneIdx(runes[t.viewStart:], x))
}

func (t *TextField) handleKey(m tea.KeyPressMsg) EventResult {
	extend := m.Mod.Contains(tea.ModShift)
	primary := m.Mod.Contains(tea.ModCtrl) || m.Mod.Contains(tea.ModSuper)
	word := primary || m.Mod.Contains(tea.ModAlt)

	if res, handled := textClipboardChord(t, t.clipboardCopyFn(), m); handled {
		t.cursor.MarkActivity()
		return res
	}
	// A clipboard chord that fell through (Ctrl+C with nothing selected) stays
	// unhandled so the host owns the next rung of its ladder.
	if ClipboardChordFor(m) != ClipboardChordNone {
		return EventResult{}
	}
	if primary {
		switch m.Code {
		case 'v':
			if t.readOnly {
				return EventResult{Handled: true}
			}
			return EventResult{Handled: true, Action: &Action{ID: ActionTextPaste, Payload: ClipboardPasteTarget(t)}}
		case 'z':
			if t.readOnly {
				return EventResult{Handled: true}
			}
			if extend {
				t.edit.Redo()
			} else {
				t.edit.Undo()
			}
			t.ensureCursorVisible()
			return EventResult{Handled: true, Invalidate: InvalidatePaint}
		case 'y':
			if !t.readOnly {
				t.edit.Redo()
				t.ensureCursorVisible()
			}
			return EventResult{Handled: true, Invalidate: InvalidatePaint}
		}
	}

	switch m.Code {
	case tea.KeyEnter:
		if t.submitID == "" {
			return EventResult{Handled: true}
		}
		return EventResult{
			Handled:    true,
			Action:     &Action{ID: t.submitID, Payload: t.edit.Value()},
			Invalidate: InvalidatePaint,
		}
	case tea.KeyEsc:
		// An active selection was already dropped by the shared chord
		// handler; Esc is otherwise the host's key (popups close, forms
		// cancel, the chat composer clears its draft).
		return EventResult{}
	case tea.KeyLeft:
		if word {
			t.edit.MoveWordLeft(extend)
		} else {
			t.edit.MoveLeft(extend)
		}
	case tea.KeyRight:
		if word {
			t.edit.MoveWordRight(extend)
		} else {
			t.edit.MoveRight(extend)
		}
	case tea.KeyHome:
		t.edit.MoveHome(extend)
	case tea.KeyEnd:
		t.edit.MoveEnd(extend)
	case tea.KeyBackspace:
		if t.readOnly {
			return EventResult{Handled: true}
		}
		if word {
			t.edit.DeleteWordBack()
		} else {
			t.edit.Backspace()
		}
	case tea.KeyDelete:
		if t.readOnly {
			return EventResult{Handled: true}
		}
		if word {
			t.edit.DeleteWordForward()
		} else {
			t.edit.Delete()
		}
	default:
		if primary {
			switch m.Code {
			case 'a':
				t.edit.SelectAll()
			case 'u':
				if !t.readOnly {
					t.edit.DeleteToLineStart()
				}
			case 'k':
				if !t.readOnly {
					t.edit.DeleteToLineEnd()
				}
			case 'w':
				if !t.readOnly {
					t.edit.DeleteWordBack()
				}
			default:
				return EventResult{}
			}
		} else if m.Text != "" && !t.readOnly {
			t.edit.InsertRunes(m.Text)
		} else {
			return EventResult{}
		}
	}
	t.ensureCursorVisible()
	return EventResult{Handled: true, Invalidate: InvalidatePaint}
}

func (t *TextField) Semantics() Semantics {
	return Semantics{
		Role:     RoleTextInput,
		Label:    t.placeholder,
		Value:    t.edit.Value(),
		Disabled: t.disabled,
	}
}

var _ Component = (*TextField)(nil)
var _ TextClipboardTarget = (*TextField)(nil)
