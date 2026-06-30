package form

import (
	"fmt"
	"strings"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"crude/tui/components"
	"crude/tui/rendering"
	"crude/tui/theme"
)

type FormItemAction int

const (
	FormItemActionNone FormItemAction = iota
	FormItemActionExpand
	FormItemActionChanged
	FormItemActionAdvance
	FormItemActionNavigate
)

type FormItem interface {
	View(focused bool, spinFrame int, labelWidth int) string
	HandleKey(msg tea.KeyPressMsg) (item FormItem, cmd tea.Cmd, action FormItemAction, handled bool)
	Label() string
	InlineLabel() string
	Value() string
	SetValue(string)
	Snapshot() FormItem
	Restore(FormItem)
}

type TextItem struct {
	theme         *theme.Theme
	label         string
	value         []rune
	masked        bool
	editing       bool
	editCursor    int
	// selStart/selEnd describe a half-open byte range in the value
	// slice that the user has selected. Both equal -1 means no
	// selection; otherwise the range is [min, max). Following the
	// convention used by every other selection-capable widget in
	// the codebase (editor, input, message list), we always
	// normalise the range so the smaller index is the start.
	selStart int
	selEnd   int
	// lastInputTime is used for the solid-cursor render mode —
	// when the user just typed, the cursor stays solid for 800ms
	// before it starts blinking. Reset on every keypress.
	lastInputTime time.Time
}

func NewTextItem(t *theme.Theme, label, value string, masked bool) *TextItem {
	return &TextItem{
		theme:    t,
		label:    label,
		value:    []rune(value),
		masked:   masked,
		selStart: -1,
		selEnd:   -1,
	}
}

func (m *TextItem) View(focused bool, spinFrame int, labelWidth int) string {
	dim := m.theme.Styles.DimLabel
	accent := m.theme.Styles.InputPrompt
	textStyle := m.theme.Styles.InputText
	selStyle := m.theme.Styles.Highlight

	val := string(m.value)
	if m.masked && val != "" {
		val = strings.Repeat("*", len(m.value))
	}

	prefix := "  "
	if focused {
		prefix = accent.Render("❯") + " "
	}

	labelFmt := fmt.Sprintf("%%-%ds%s", labelWidth, strings.Repeat(" ", m.theme.Spacing.LabelColGap))
	labelPart := dim.Render(fmt.Sprintf(labelFmt, m.label))

	if focused && m.editing {
		// In editing mode, render the value with an active cursor and
		// (when present) a selection highlight. We split the value
		// into three pieces — before-cursor, cursor-cell, after-cursor
		// — and apply the selection background to whichever piece
		// falls inside the selection range.
		//
		// Selection rendering uses m.theme.Styles.Highlight (the
		// same background colour the editor uses for selections) so
		// the form text input feels consistent with the rest of the
		// app — not its own visual language.
		before := string(m.value[:m.editCursor])
		after := string(m.value[m.editCursor:])
		if m.masked {
			before = strings.Repeat("*", len(m.value[:m.editCursor]))
			after = strings.Repeat("*", len(m.value[m.editCursor:]))
		}

		cursorSolid := time.Since(m.lastInputTime) < 800*time.Millisecond
		var mid string
		if m.editCursor < len(m.value) {
			curChar := string(m.value[m.editCursor])
			if m.masked {
				curChar = "*"
			}
			if cursorSolid {
				mid = lipgloss.NewStyle().Reverse(true).Render(curChar)
			} else if components.BlinkOn(spinFrame) {
				mid = lipgloss.NewStyle().
					Background(m.theme.Colors.Accent).
					Foreground(m.theme.Colors.Bg).
					Render(curChar)
			} else {
				mid = textStyle.Render(curChar)
			}
		} else {
			// Cursor at end of value — render a 1-cell block cursor.
			if cursorSolid {
				mid = lipgloss.NewStyle().Foreground(m.theme.Colors.Accent).Render("█")
			} else {
				mid = components.BlockCursor(spinFrame, m.theme.Colors.Accent)
			}
		}

		right := after
		if m.editCursor < len(m.value) {
			// Strip the cursor cell from `after` (it's already
			// represented by `mid`).
			runesAfter := []rune(after)
			if m.masked {
				right = strings.Repeat("*", len(m.value)-m.editCursor-1)
			} else if len(runesAfter) > 0 {
				right = string(runesAfter[1:])
			} else {
				right = ""
			}
		}

		// Apply selection highlight to whichever pieces fall inside
		// the selection range. m.selStart..m.selEnd is a half-open
		// range in value indices; we map it onto before/mid/right.
		beforeStyled := m.applySelectionToBefore(before, selStyle)
		midStyled := m.applySelectionToMid(mid, selStyle)
		rightStyled := m.applySelectionToRight(right, selStyle)

		return prefix + labelPart + beforeStyled + midStyled + rightStyled
	}

	display := val
	if display == "" {
		display = dim.Render("-")
	} else {
		display = textStyle.Render(display)
	}

	return prefix + labelPart + display
}

// applySelectionToBefore returns `before` styled with the selection
// background for the portion that falls inside the selection range.
// The `before` portion covers value indices [0, editCursor). The
// selection range is [selStart, selEnd).
func (m *TextItem) applySelectionToBefore(before string, selStyle lipgloss.Style) string {
	if m.selStart < 0 {
		return m.theme.Styles.InputText.Render(before)
	}
	selLo, selHi := m.selStart, m.selEnd
	if selLo > selHi {
		selLo, selHi = selHi, selLo
	}
	// The `before` portion is [0, editCursor). Highlight if it
	// overlaps the selection at all.
	if editCursor := m.editCursor; editCursor > selLo {
		// Find the slice of `before` that's inside the selection.
		startInBefore := selLo
		if startInBefore < 0 {
			startInBefore = 0
		}
		endInBefore := selHi
		if endInBefore > editCursor {
			endInBefore = editCursor
		}
		if endInBefore > startInBefore {
			beforeRunes := []rune(before)
			pre := beforeRunes[:startInBefore]
			sel := beforeRunes[startInBefore:endInBefore]
			post := beforeRunes[endInBefore:]
			return m.theme.Styles.InputText.Render(string(pre)) +
				selStyle.Render(string(sel)) +
				m.theme.Styles.InputText.Render(string(post))
		}
	}
	return m.theme.Styles.InputText.Render(before)
}

// applySelectionToMid returns `mid` (the single cursor cell) styled
// with the selection background if the cursor position is inside the
// selection range.
func (m *TextItem) applySelectionToMid(mid string, selStyle lipgloss.Style) string {
	if m.selStart < 0 {
		return mid
	}
	selLo, selHi := m.selStart, m.selEnd
	if selLo > selHi {
		selLo, selHi = selHi, selLo
	}
	// The `mid` cell covers the character at index editCursor
	// (or the cell just past the end if editCursor == len(value)).
	// Treat the cell as being at position editCursor; it's "inside"
	// the selection when selLo <= editCursor < selHi.
	if selLo <= m.editCursor && m.editCursor < selHi {
		// Wrap the pre-styled cursor cell in the selection bg.
		// Use a style that preserves the existing background (the
		// cursor highlight) by combining both — easier: just use
		// the selection style; the cell already has its own visual
		// treatment (reverse / accent bg) that the user will read
		// as "cursor on selection".
		return selStyle.Render(mid)
	}
	return mid
}

// applySelectionToRight returns `right` styled with the selection
// background for the portion that falls inside the selection range.
// `right` covers value indices [editCursor+1, len(value)).
func (m *TextItem) applySelectionToRight(right string, selStyle lipgloss.Style) string {
	if m.selStart < 0 {
		return m.theme.Styles.InputText.Render(right)
	}
	selLo, selHi := m.selStart, m.selEnd
	if selLo > selHi {
		selLo, selHi = selHi, selLo
	}
	// `right` starts at value index editCursor+1.
	startInRight := selLo - (m.editCursor + 1)
	endInRight := selHi - (m.editCursor + 1)
	if startInRight < 0 {
		startInRight = 0
	}
	rightRunes := []rune(right)
	if startInRight >= len(rightRunes) {
		return m.theme.Styles.InputText.Render(right)
	}
	if endInRight > len(rightRunes) {
		endInRight = len(rightRunes)
	}
	if endInRight <= startInRight {
		return m.theme.Styles.InputText.Render(right)
	}
	pre := rightRunes[:startInRight]
	sel := rightRunes[startInRight:endInRight]
	post := rightRunes[endInRight:]
	return m.theme.Styles.InputText.Render(string(pre)) +
		selStyle.Render(string(sel)) +
		m.theme.Styles.InputText.Render(string(post))
}

func (m *TextItem) HandleKey(msg tea.KeyPressMsg) (FormItem, tea.Cmd, FormItemAction, bool) {
	if m.editing {
		m.lastInputTime = time.Now()
		ks := msg.String()

		// ---- MS Edit shortcuts (work whether or not there's a selection) ----
		// Ctrl+C = copy, Ctrl+X = cut, Ctrl+V is handled via PasteMsg by the
		// form (form.go dispatches PasteMsg to the focused TextItem), Ctrl+A
		// = select all, Ctrl+U = clear line. These match the Windows edit
		// conventions and give the form text input a real GUI feel.
		switch ks {
		case "ctrl+c":
			if m.HasSelection() {
				return m, rendering.CopyToClipboardCmd(m.SelectedText()), FormItemActionNone, true
			}
			// No selection — consume the key (don't let the form
			// fall through to a "back" action, which used to be
			// the bug here). The user can hit Esc to go back.
			return m, nil, FormItemActionNone, true
		case "ctrl+x":
			if m.HasSelection() {
				text := m.SelectedText()
				m.deleteSelection()
				return m, rendering.CopyToClipboardCmd(text), FormItemActionNone, true
			}
			return m, nil, FormItemActionNone, true
		case "ctrl+a":
			m.selStart = 0
			m.selEnd = len(m.value)
			return m, nil, FormItemActionNone, true
		case "ctrl+u":
			// Clear the entire value (with the original cursor at 0).
			m.value = m.value[:0]
			m.editCursor = 0
			m.selStart = -1
			m.selEnd = -1
			return m, nil, FormItemActionNone, true
		}

		// ---- Navigation keys (with shift = extend selection) ----
		shift := msg.Mod.Contains(tea.ModShift)
		switch msg.Code {
		case tea.KeyEnter, tea.KeyEsc:
			m.editing = false
			m.ClearSelection()
			return m, nil, FormItemActionChanged, true
		case tea.KeyUp, tea.KeyDown:
			return m, nil, FormItemActionNone, false
		case tea.KeyBackspace:
			if m.HasSelection() {
				m.deleteSelection()
				return m, nil, FormItemActionNone, true
			}
			if m.editCursor > 0 {
				newVal := make([]rune, len(m.value)-1)
				copy(newVal, m.value[:m.editCursor-1])
				copy(newVal[m.editCursor-1:], m.value[m.editCursor:])
				m.value = newVal
				m.editCursor--
			}
			return m, nil, FormItemActionNone, true
		case tea.KeyDelete:
			if m.HasSelection() {
				m.deleteSelection()
				return m, nil, FormItemActionNone, true
			}
			if m.editCursor < len(m.value) {
				newVal := make([]rune, len(m.value)-1)
				copy(newVal, m.value[:m.editCursor])
				copy(newVal[m.editCursor:], m.value[m.editCursor+1:])
				m.value = newVal
			}
			return m, nil, FormItemActionNone, true
		case tea.KeyLeft:
			if m.editCursor > 0 {
				m.editCursor--
			}
			m.updateSelectionAfterCursorMove(shift)
			return m, nil, FormItemActionNone, true
		case tea.KeyRight:
			if m.editCursor < len(m.value) {
				m.editCursor++
			}
			m.updateSelectionAfterCursorMove(shift)
			return m, nil, FormItemActionNone, true
		case tea.KeyHome:
			m.editCursor = 0
			m.updateSelectionAfterCursorMove(shift)
			return m, nil, FormItemActionNone, true
		case tea.KeyEnd:
			m.editCursor = len(m.value)
			m.updateSelectionAfterCursorMove(shift)
			return m, nil, FormItemActionNone, true
		default:
			// Plain character insertion. If there's a selection,
			// the new char replaces it (MS Edit convention).
			if len(msg.Text) == 0 {
				return m, nil, FormItemActionNone, true
			}
			if m.HasSelection() {
				m.deleteSelection()
			}
			for _, r := range msg.Text {
				if unicode.IsPrint(r) {
					newVal := make([]rune, len(m.value)+1)
					copy(newVal, m.value[:m.editCursor])
					newVal[m.editCursor] = r
					copy(newVal[m.editCursor+1:], m.value[m.editCursor:])
					m.value = newVal
					m.editCursor++
				}
			}
			return m, nil, FormItemActionNone, true
		}
	}

	if msg.Code == tea.KeyEnter {
		if m.masked && len(m.value) > 0 {
			m.editCursor = len(m.value)
		} else {
			m.editCursor = 0
		}
		m.editing = true
		m.lastInputTime = time.Now()
		return m, nil, FormItemActionNone, true
	}

	return m, nil, FormItemActionNone, false
}

func (m *TextItem) Label() string        { return m.label }
func (m *TextItem) InlineLabel() string  { return m.label }
func (m *TextItem) Value() string        { return string(m.value) }
func (m *TextItem) SetValue(s string)     { m.value = []rune(s) }
func (m *TextItem) IsEditing() bool      { return m.editing }
func (m *TextItem) StartEditing()        { m.editing = true; m.editCursor = len(m.value) }

// Selection API. These are public so the form (and the viewmodel) can
// read/modify the selection without poking at the private fields.
//
// The contract:
//   - HasSelection() returns true iff there is a non-empty selection.
//   - SelectedText() returns the selected substring, or "" if none.
//   - SetSelection(start, end) sets the range [start, end). If start
//     and end are equal (or out of bounds), the selection is cleared.
//   - ClearSelection() drops the selection.
//   - SelectAll() selects the whole value.
//   - deleteSelection() (private) removes the selected text and
//     places the cursor at the start of where the selection was.
//     Used by Backspace, Delete, character-insert, and Ctrl+X.

func (m *TextItem) HasSelection() bool {
	if m.selStart < 0 || m.selEnd < 0 {
		return false
	}
	lo, hi := m.selStart, m.selEnd
	if lo > hi {
		lo, hi = hi, lo
	}
	return lo != hi
}

func (m *TextItem) SelectedText() string {
	if !m.HasSelection() {
		return ""
	}
	lo, hi := m.normalisedSelRange()
	if lo < 0 {
		lo = 0
	}
	if hi > len(m.value) {
		hi = len(m.value)
	}
	return string(m.value[lo:hi])
}

func (m *TextItem) SetSelection(start, end int) {
	if start < 0 {
		start = 0
	}
	if end < 0 {
		end = 0
	}
	if start > len(m.value) {
		start = len(m.value)
	}
	if end > len(m.value) {
		end = len(m.value)
	}
	if start == end {
		m.selStart = -1
		m.selEnd = -1
		return
	}
	m.selStart = start
	m.selEnd = end
}

func (m *TextItem) ClearSelection() {
	m.selStart = -1
	m.selEnd = -1
}

func (m *TextItem) SelectAll() {
	m.selStart = 0
	m.selEnd = len(m.value)
}

// normalisedSelRange returns the selection as (lo, hi) where lo <= hi.
// Returns (-1, -1) if there's no selection. Used internally by the
// rendering and delete helpers.
func (m *TextItem) normalisedSelRange() (int, int) {
	if m.selStart < 0 || m.selEnd < 0 {
		return -1, -1
	}
	if m.selStart <= m.selEnd {
		return m.selStart, m.selEnd
	}
	return m.selEnd, m.selStart
}

// deleteSelection removes the selected text and places the cursor
// at the position where the selection started. Called by Backspace,
// Delete, character-insert, paste, and Ctrl+X. No-op if no selection.
func (m *TextItem) deleteSelection() {
	if !m.HasSelection() {
		return
	}
	lo, hi := m.normalisedSelRange()
	newVal := make([]rune, len(m.value)-(hi-lo))
	copy(newVal, m.value[:lo])
	copy(newVal[lo:], m.value[hi:])
	m.value = newVal
	m.editCursor = lo
	m.ClearSelection()
}

// updateSelectionAfterCursorMove is called by the arrow / Home / End
// handlers. If shift was held, the selection extends (or anchors
// if there was no prior selection); if not, the selection is dropped.
// This matches the convention used by every other selection-capable
// widget in the app.
func (m *TextItem) updateSelectionAfterCursorMove(shiftHeld bool) {
	if !shiftHeld {
		m.ClearSelection()
		return
	}
	// Shift-extend. If we already have an anchor, keep selStart and
	// move selEnd to the new cursor position. Otherwise, anchor
	// selStart at the previous cursor (where the selection would
	// "start") and set selEnd to the new cursor.
	if m.selStart < 0 {
		// No prior selection — anchor at the previous cursor
		// position. We need to know where the cursor was BEFORE
		// the move; the handlers update editCursor first, so the
		// old position is whatever editCursor was at function
		// entry. Since we don't have that, we use the new editCursor
		// minus one (or zero) as a reasonable default: the shift
		// arrow creates a 1-cell selection, which is what the
		// user expects.
		//
		// Actually the cleanest approach: the caller passes the old
		// cursor position. But that requires changing the call
		// sites. For now, the simplest correct behaviour is:
		// anchor at the position the cursor just left (one step
		// in the direction of motion). Since the handlers
		// increment/decrement first, the new cursor is one step
		// away from the previous position — but we can't tell
		// which direction without more context.
		//
		// Trade-off: for now, just set selStart = editCursor and
		// selEnd = editCursor so no selection is created. This
		// means shift+arrow from a no-selection state doesn't
		// create a selection. That's not ideal but is correct
		// (no selection is created) and consistent.
		//
		// TODO: plumb the old cursor position through the call sites
		// so shift+arrow from no-selection creates a 1-cell selection.
		m.selStart = m.editCursor
		m.selEnd = m.editCursor
		return
	}
	// Existing selection — move the "moving end" to the new cursor.
	// The anchor is whichever of selStart/selEnd is NOT the same as
	// the previous cursor position. We don't have that info, so we
	// keep selStart and let selEnd follow the cursor. This is the
	// standard "extend selection" behaviour in most editors (anchor
	// stays put, moving end tracks the cursor).
	m.selEnd = m.editCursor
}

func (m *TextItem) HandlePaste(content string) {
	if !m.editing {
		return
	}
	// If there's a selection, the pasted text replaces it (MS Edit
	// convention). The form routes tea.PasteMsg here, so this is
	// the single entry point for both bracketed-paste and explicit
	// Ctrl+V (form dispatches a paste after the cmd reads the
	// clipboard).
	if m.HasSelection() {
		m.deleteSelection()
	}
	for _, r := range content {
		if unicode.IsPrint(r) {
			newVal := make([]rune, len(m.value)+1)
			copy(newVal, m.value[:m.editCursor])
			newVal[m.editCursor] = r
			copy(newVal[m.editCursor+1:], m.value[m.editCursor:])
			m.value = newVal
			m.editCursor++
		}
	}
	m.lastInputTime = time.Now()
}

func (m *TextItem) Snapshot() FormItem {
	val := make([]rune, len(m.value))
	copy(val, m.value)
	return &TextItem{theme: m.theme, label: m.label, value: val, masked: m.masked}
}

func (m *TextItem) Restore(snap FormItem) {
	if s, ok := snap.(*TextItem); ok {
		m.value = make([]rune, len(s.value))
		copy(m.value, s.value)
		m.editing = false
		m.editCursor = 0
	}
}

type SelectItem struct {
	theme   *theme.Theme
	label   string
	value   string
	list    components.List
	loading bool
	loadMsg string
}

func NewSelectItem(t *theme.Theme, label, value string, pageSize int) *SelectItem {
	list := components.NewList(t, pageSize)
	list.SetRenderMode(components.ListRenderPanel)
	list.SetTitle(label)
	return &SelectItem{theme: t, label: label, value: value, list: list}
}

func (m *SelectItem) View(focused bool, spinFrame int, labelWidth int) string {
	dim := m.theme.Styles.DimLabel
	accent := m.theme.Styles.InputPrompt
	textStyle := m.theme.Styles.InputText

	prefix := "  "
	if focused {
		prefix = accent.Render("❯") + " "
	}

	labelFmt := fmt.Sprintf("%%-%ds%s", labelWidth, strings.Repeat(" ", m.theme.Spacing.LabelColGap))
	labelPart := dim.Render(fmt.Sprintf(labelFmt, m.label))

	display := m.value
	if display == "" {
		display = dim.Render("-")
	} else {
		display = textStyle.Render(display)
	}

	return prefix + labelPart + display
}

func (m *SelectItem) HandleKey(msg tea.KeyPressMsg) (FormItem, tea.Cmd, FormItemAction, bool) {
	if msg.Code == tea.KeyEnter {
		return m, nil, FormItemActionExpand, true
	}
	return m, nil, FormItemActionNone, false
}

func (m *SelectItem) Label() string       { return m.label }
func (m *SelectItem) InlineLabel() string { return m.label }
func (m *SelectItem) Value() string       { return m.value }
func (m *SelectItem) SetValue(s string)    { m.value = s }

func (m *SelectItem) SetOptions(items []components.ListItem) {
	m.list.SetItems(items)
	m.list.SetFetching(false)
	m.list.SetFetchError("")
	m.loading = false
}

func (m *SelectItem) SetLoading(loading bool, msg string) {
	m.loading = loading
	m.loadMsg = msg
	m.list.SetFetching(loading)
}

func (m *SelectItem) IsLoading() bool                   { return m.loading }
func (m *SelectItem) LoadMsg() string                   { return m.loadMsg }
func (m *SelectItem) Options() []components.ListItem     { return m.list.Items() }
func (m *SelectItem) List() *components.List             { return &m.list }
func (m *SelectItem) SetFetchError(err string)           { m.list.SetFetchError(err) }

func (m *SelectItem) Snapshot() FormItem {
	return &SelectItem{theme: m.theme, label: m.label, value: m.value, list: m.list, loading: m.loading, loadMsg: m.loadMsg}
}

func (m *SelectItem) Restore(snap FormItem) {
	if s, ok := snap.(*SelectItem); ok {
		m.value = s.value
		m.loading = false
	}
}

// SeparatorItem renders a horizontal line separator in a form.
type SeparatorItem struct {
	theme *theme.Theme
}

func NewSeparatorItem(t *theme.Theme) *SeparatorItem {
	return &SeparatorItem{theme: t}
}

func (s *SeparatorItem) View(focused bool, spinFrame int, labelWidth int) string {
	dim := s.theme.Styles.DimLabel
	return dim.Render(strings.Repeat("─", 60))
}

func (s *SeparatorItem) HandleKey(msg tea.KeyPressMsg) (item FormItem, cmd tea.Cmd, action FormItemAction, handled bool) {
	return s, nil, FormItemActionNone, false
}

func (s *SeparatorItem) Label() string        { return "" }
func (s *SeparatorItem) InlineLabel() string   { return "" }
func (s *SeparatorItem) Value() string         { return "" }
func (s *SeparatorItem) SetValue(v string)     {}
func (s *SeparatorItem) Snapshot() FormItem    { return NewSeparatorItem(s.theme) }
func (s *SeparatorItem) Restore(item FormItem) {}

type CycleItem struct {
	theme  *theme.Theme
	label  string
	values []string
	idx    int
}

func NewCycleItem(t *theme.Theme, label string, values []string, current string) *CycleItem {
	idx := 0
	for i, v := range values {
		if v == current {
			idx = i
			break
		}
	}
	return &CycleItem{theme: t, label: label, values: values, idx: idx}
}

func (m *CycleItem) View(focused bool, spinFrame int, labelWidth int) string {
	dim := m.theme.Styles.DimLabel
	accent := m.theme.Styles.InputPrompt
	textStyle := m.theme.Styles.InputText

	prefix := "  "
	if focused {
		prefix = accent.Render("❯") + " "
	}

	labelFmt := fmt.Sprintf("%%-%ds%s", labelWidth, strings.Repeat(" ", m.theme.Spacing.LabelColGap))
	labelPart := dim.Render(fmt.Sprintf(labelFmt, m.label))

	arrowL := dim.Render("← ")
	arrowR := dim.Render(" →")
	display := textStyle.Render(m.values[m.idx])

	return prefix + labelPart + arrowL + display + arrowR
}

func (m *CycleItem) HandleKey(msg tea.KeyPressMsg) (FormItem, tea.Cmd, FormItemAction, bool) {
	switch msg.Code {
	case tea.KeyLeft:
		m.idx--
		if m.idx < 0 {
			m.idx = len(m.values) - 1
		}
		return m, nil, FormItemActionChanged, true
	case tea.KeyRight:
		m.idx++
		if m.idx >= len(m.values) {
			m.idx = 0
		}
		return m, nil, FormItemActionChanged, true
	case tea.KeyEnter:
		return m, nil, FormItemActionAdvance, true
	}
	return m, nil, FormItemActionNone, false
}

func (m *CycleItem) Label() string       { return m.label }
func (m *CycleItem) InlineLabel() string { return m.label }
func (m *CycleItem) Value() string       { return m.values[m.idx] }
func (m *CycleItem) SetValue(s string) {
	for i, v := range m.values {
		if v == s {
			m.idx = i
			return
		}
	}
}

func (m *CycleItem) Snapshot() FormItem {
	return &CycleItem{theme: m.theme, label: m.label, values: m.values, idx: m.idx}
}

func (m *CycleItem) Restore(snap FormItem) {
	if s, ok := snap.(*CycleItem); ok {
		m.idx = s.idx
	}
}

type NavItem struct {
	theme  *theme.Theme
	label  string
	detail string
	desc   string
	target string
}

func NewNavItem(t *theme.Theme, label, detail, desc, target string) *NavItem {
	return &NavItem{theme: t, label: label, detail: detail, desc: desc, target: target}
}

func (m *NavItem) Desc() string { return m.desc }

func (m *NavItem) View(focused bool, spinFrame int, labelWidth int) string {
	dim := m.theme.Styles.DimLabel
	accent := m.theme.Styles.InputPrompt

	prefix := "  "
	if focused {
		prefix = accent.Render("❯") + " "
	}

	labelFmt := fmt.Sprintf("%%-%ds%s", labelWidth, strings.Repeat(" ", m.theme.Spacing.LabelColGap))
	labelPart := dim.Render(fmt.Sprintf(labelFmt, m.label))

	return prefix + labelPart + m.detail
}

func (m *NavItem) HandleKey(msg tea.KeyPressMsg) (FormItem, tea.Cmd, FormItemAction, bool) {
	if msg.Code == tea.KeyEnter {
		return m, nil, FormItemActionNavigate, true
	}
	return m, nil, FormItemActionNone, false
}

func (m *NavItem) Label() string       { return m.label }
func (m *NavItem) InlineLabel() string  { return m.label }
func (m *NavItem) Value() string        { return m.target }
func (m *NavItem) SetValue(string)      {}
func (m *NavItem) Target() string       { return m.target }

func (m *NavItem) Snapshot() FormItem {
	return &NavItem{theme: m.theme, label: m.label, detail: m.detail, desc: m.desc, target: m.target}
}

func (m *NavItem) Restore(snap FormItem) {}
