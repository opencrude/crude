package ui

import (
	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/theme"
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
	viewReset bool
	bg        cell.Color
	bgSet     bool
	tokens    *TokenSet
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
func (t *TextField) Edit() *TextEdit { return t.edit }

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
	if cur > t.viewStart+cw {
		t.viewStart = cur - cw
	}
	if t.viewStart < 0 {
		t.viewStart = 0
	}
	if maxStart := max(0, t.edit.Len()-cw); t.viewStart > maxStart {
		t.viewStart = maxStart
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
		buf.WriteString(x, y, ph, phFG, bg, cell.AttrItalic)
		// No cursor block over placeholder text: a block here obscures the
		// first placeholder glyph (e.g. the "F" in "Filter…") and reads as a
		// highlight. The cursor appears as soon as the user types.
		return
	}

	runes := t.displayRunes()
	if maxStart := max(0, len(runes)-cw); t.viewStart > maxStart {
		t.viewStart = maxStart
	}
	selA, selB := t.edit.SelectionRange()
	selBG := bg
	if t.tokens != nil {
		selBG = t.tokens.Color(ColorSelection)
	}
	// Visible slice
	end := t.viewStart + cw
	if end > len(runes) {
		end = len(runes)
	}
	vis := runes[t.viewStart:end]
	col := 0
	for i, rr := range vis {
		abs := t.viewStart + i
		cellFG, cellBG := fg, bg
		inSel := abs >= selA && abs < selB && selA != selB
		if inSel {
			cellBG = selBG
		}
		// Inverse cursor on grapheme under cursor when focused
		if ctx.Focused && !t.disabled && abs == t.edit.Cursor() && t.edit.Cursor() < t.edit.Len() {
			// Reverse video: text bg becomes fg, text fg becomes bg.
			cellFG = bg
			cellBG = fg
			if cellFG.IsDefault() {
				if t.tokens != nil {
					cellFG = t.tokens.Color(ColorBg)
				} else {
					cellFG = NewTokenSet(theme.DefaultDark()).Color(ColorBg)
				}
			}
			if cellBG.IsDefault() {
				if t.tokens != nil {
					cellBG = t.tokens.Color(ColorAccent)
				} else {
					cellBG = NewTokenSet(theme.DefaultDark()).Color(ColorAccent)
				}
			}
		}
		buf.Set(x+col, y, cell.NewCell(rr, cellFG, cellBG, 0))
		col++
		if col >= cw {
			break
		}
	}
	// End-of-line cursor
	if ctx.Focused && !t.disabled && t.edit.Cursor() >= t.edit.Len() {
		cx := t.edit.Cursor() - t.viewStart
		if cx >= 0 && cx < cw {
			t.paintCursor(buf, x+cx, y, bg)
		}
	}
}

func (t *TextField) paintCursor(buf *surface.CellBuffer, x, y int, bg cell.Color) {
	tokens := t.tokens
	if tokens == nil {
		tokens = NewTokenSet(theme.DefaultDark())
	}
	fg := tokens.Color(ColorAccent)
	// Block cursor
	cur := buf.Get(x, y)
	ch := cur.Char
	if ch == 0 || ch == ' ' {
		ch = ' '
	}
	// Inverse: accent bg, contrasting fg
	invFG := bg
	if invFG.IsDefault() {
		invFG = tokens.Color(ColorBg)
	}
	buf.Set(x, y, cell.NewCell(ch, invFG, fg, 0))
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
		return t.handleKey(evt.Key)
	case EventCustom:
		if paste, ok := evt.Custom.(tea.PasteMsg); ok {
			if t.readOnly {
				return EventResult{Handled: true}
			}
			t.edit.InsertRunes(paste.Content)
			t.ensureCursorVisible()
			return EventResult{Handled: true, Invalidate: InvalidatePaint}
		}
	case EventMouseClick:
		if evt.MouseClick.Button != tea.MouseLeft {
			return EventResult{}
		}
		// Position cursor from local X
		lx := ctx.LocalX - t.contentX()
		if t.promptAtEnd && t.promptWidth() > 0 && ctx.LocalX >= t.Bounds().W-t.promptWidth() {
			lx = t.contentWidth()
		}
		if lx < 0 {
			lx = 0
		}
		idx := t.viewStart + lx
		if idx > t.edit.Len() {
			idx = t.edit.Len()
		}
		t.edit.SetCursor(idx, false)
		t.ensureCursorVisible()
		return EventResult{Handled: true, Invalidate: InvalidatePaint | InvalidateFocus}
	}
	return EventResult{}
}

func (t *TextField) handleKey(m tea.KeyPressMsg) EventResult {
	extend := m.Mod.Contains(tea.ModShift)
	ctrl := m.Mod.Contains(tea.ModCtrl)

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
		if t.edit.HasSelection() {
			t.edit.ClearSelection()
			return EventResult{Handled: true, Invalidate: InvalidatePaint}
		}
		return EventResult{}
	case tea.KeyLeft:
		if ctrl {
			t.edit.MoveWordLeft(extend)
		} else {
			t.edit.MoveLeft(extend)
		}
	case tea.KeyRight:
		if ctrl {
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
		if ctrl {
			t.edit.DeleteWordBack()
		} else {
			t.edit.Backspace()
		}
	case tea.KeyDelete:
		if t.readOnly {
			return EventResult{Handled: true}
		}
		t.edit.Delete()
	default:
		if ctrl {
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
