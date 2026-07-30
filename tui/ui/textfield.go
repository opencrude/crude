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
	prompt      string // optional leading prompt e.g. ">"
	promptFG    cell.Color
	promptFGSet bool
	readOnly    bool
	disabled    bool
	password    bool
	submitID    string // action ID on Enter; empty = no submit action
	// horizontal viewport for long values
	viewStart int
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

// SetPrompt sets a leading prompt glyph/string (not part of value).
func (t *TextField) SetPrompt(s string) { t.prompt = s }

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

// SetValue replaces the field value.
func (t *TextField) SetValue(s string) {
	t.edit.SetValue(s)
	t.ensureCursorVisible()
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
	} else {
		t.ensureCursorVisible()
	}
}

func (t *TextField) contentX() int {
	return rendering.VisibleWidth(t.prompt)
}

func (t *TextField) contentWidth() int {
	b := t.Bounds()
	w := b.W - t.contentX()
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

	// Prompt
	if t.prompt != "" {
		pf := fg
		if t.promptFGSet {
			pf = t.promptFG
		} else if t.tokens != nil {
			pf = t.tokens.Color(ColorAccent)
		}
		buf.WriteString(x, y, t.prompt, pf, bg, 0)
		x += t.contentX()
	}

	cw := t.contentWidth()
	if cw <= 0 {
		return
	}

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
		// Cursor at start when focused
		if ctx.Focused && !t.disabled {
			t.paintCursor(buf, x, y, bg)
		}
		return
	}

	runes := t.displayRunes()
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
func (t *TextField) Handle(ctx EventContext, msg tea.Msg) EventResult {
	if t.disabled {
		return EventResult{}
	}
	switch m := msg.(type) {
	case tea.KeyPressMsg:
		return t.handleKey(m)
	case tea.PasteMsg:
		if t.readOnly {
			return EventResult{Handled: true}
		}
		t.edit.InsertRunes(m.Content)
		t.ensureCursorVisible()
		return EventResult{Handled: true, Invalidate: InvalidatePaint}
	case tea.MouseClickMsg:
		if m.Button != tea.MouseLeft {
			return EventResult{}
		}
		// Position cursor from local X
		lx := ctx.LocalX - t.contentX()
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
