package form

import (
	"image/color"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"crude/tui/components"
	"crude/tui/theme"
)

type FormAction int

const (
	FormActionNone FormAction = iota
	FormActionSave
	FormActionCancel
	FormActionBack
	FormActionExpand
	FormActionNavigate
	FormActionReset
)

type Button struct {
	Label  string
	Key    string
	Color  color.Color
	Cancel bool
	Reset  bool
}

type Form struct {
	theme *theme.Theme

	title string
	hint  string

	items []FormItem

	buttons    []Button
	hasButtons bool
	focusedBtn int

	focused   int
	focusZone int
	expanded  int

	snapshot *formSnapshot
}

type formSnapshot struct {
	items []FormItem
}

func NewForm(t *theme.Theme, title, hint string, items ...FormItem) Form {
	return Form{
		theme:    t,
		title:    title,
		hint:     hint,
		items:    items,
		focused:  0,
		expanded: -1,
	}
}

func NewFormWithButtons(t *theme.Theme, title, hint string, buttons []Button, items ...FormItem) Form {
	return Form{
		theme:      t,
		title:      title,
		hint:       hint,
		items:      items,
		buttons:    buttons,
		hasButtons: true,
		focused:    0,
		expanded:   -1,
	}
}

func (m Form) Update(msg tea.Msg) (Form, tea.Cmd, FormAction) {
	// Handle paste messages: route to the currently-editing TextItem
	if pasteMsg, ok := msg.(tea.PasteMsg); ok {
		if m.focusZone == 0 && m.focused >= 0 && m.focused < len(m.items) {
			if ti, ok := m.items[m.focused].(*TextItem); ok && ti.IsEditing() {
				ti.HandlePaste(pasteMsg.Content)
			}
		}
		return m, nil, FormActionNone
	}
	if mouseMsg, ok := msg.(tea.MouseClickMsg); ok {
		return m.updateMouseClick(mouseMsg)
	}
	if wheelMsg, ok := msg.(tea.MouseWheelMsg); ok && m.expanded >= 0 {
		if si, ok := m.items[m.expanded].(*SelectItem); ok {
			updated, cmd := si.List().Update(wheelMsg)
			*si.List() = updated
			return m, cmd, FormActionNone
		}
	}

	if clickMsg, ok := msg.(tea.MouseClickMsg); ok {
		return m.handleMouseClick(clickMsg)
	}

	keyMsg, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil, FormActionNone
	}

	// Ctrl+C used to mean "go back" here, but that clobbered the
	// standard edit shortcut (copy) and left the user with no way
	// to copy text out of a settings field. The "back" affordance
	// is now Esc-only — see the case for tea.KeyEsc below. Ctrl+Q
	// (when intercepted by the viewmodel's handleCancel) closes the
	// overlay entirely.

	if m.expanded >= 0 {
		return m.updateExpanded(keyMsg)
	}

	if m.hasButtons && m.focusZone == 1 {
		return m.updateButtons(keyMsg)
	}

	item := m.items[m.focused]
	updated, cmd, action, handled := item.HandleKey(keyMsg)
	m.items[m.focused] = updated

	if action == FormItemActionExpand {
		if _, ok := m.items[m.focused].(*SelectItem); ok {
			m.expanded = m.focused
		}
		return m, cmd, FormActionExpand
	}

	if action == FormItemActionAdvance {
		if m.focused < len(m.items)-1 {
			m.focused++
		} else if m.hasButtons {
			m.focusZone = 1
			m.focusedBtn = 0
		}
		return m, cmd, FormActionNone
	}

	if action == FormItemActionNavigate {
		return m, cmd, FormActionNavigate
	}

	if handled {
		return m, cmd, FormActionNone
	}

	switch keyMsg.Code {
	case tea.KeyEsc:
		return m, nil, FormActionBack
	case tea.KeyUp:
		if m.focused > 0 {
			m.focused--
		} else if m.hasButtons {
			m.focusZone = 1
			m.focusedBtn = len(m.buttons) - 1
		}
	case tea.KeyDown:
		if m.focused < len(m.items)-1 {
			m.focused++
		} else if m.hasButtons {
			m.focusZone = 1
			m.focusedBtn = 0
		}
	case tea.KeyTab:
		if keyMsg.Mod.Contains(tea.ModShift) {
			if m.focused > 0 {
				m.focused--
			} else if m.hasButtons {
				m.focusZone = 1
				m.focusedBtn = len(m.buttons) - 1
			} else {
				m.focused = len(m.items) - 1
			}
		} else if m.focused < len(m.items)-1 {
			m.focused++
		} else if m.hasButtons {
			m.focusZone = 1
			m.focusedBtn = 0
		} else {
			m.focused = 0
		}
	}

	return m, nil, FormActionNone
}

func (m Form) updateMouseClick(msg tea.MouseClickMsg) (Form, tea.Cmd, FormAction) {
	if msg.Button != tea.MouseLeft {
		return m, nil, FormActionNone
	}

	if m.expanded >= 0 {
		si, ok := m.items[m.expanded].(*SelectItem)
		if !ok {
			return m, nil, FormActionNone
		}
		// Form.View adds one blank line. A panel list then renders title,
		// separator, filter, and separator before its first option.
		const firstOptionRow = 5
		if msg.Y < firstOptionRow {
			return m, nil, FormActionNone
		}
		listMsg := tea.MouseClickMsg{X: msg.X, Y: msg.Y - firstOptionRow, Button: msg.Button}
		updated, cmd := si.List().Update(listMsg)
		*si.List() = updated
		if updated.Action() == components.ListActionSelect {
			if selected, ok := updated.Selected(); ok {
				si.SetValue(selected.ID)
			}
			m.expanded = -1
		}
		return m, cmd, FormActionNone
	}

	hit := m.HitTest(msg.Y)
	if hit.ItemIndex >= 0 {
		m.FocusItem(hit.ItemIndex)
		item := m.items[hit.ItemIndex]
		switch it := item.(type) {
		case *TextItem:
			it.StartEditing()
		case *CycleItem:
			updated, cmd, action, _ := it.HandleKey(tea.KeyPressMsg{Code: tea.KeyRight})
			m.items[hit.ItemIndex] = updated
			if action == FormItemActionNavigate {
				return m, cmd, FormActionNavigate
			}
			return m, cmd, FormActionNone
		case *SelectItem:
			m.expanded = hit.ItemIndex
			if it.List().SelectedID() != it.Value() {
				it.List().SetSelectedID(it.Value())
			}
			return m, nil, FormActionExpand
		case *NavItem:
			return m, nil, FormActionNavigate
		}
		return m, nil, FormActionNone
	}

	if hit.InButtonRow {
		x := msg.X
		for i, btn := range m.buttons {
			buttonW := len([]rune(btn.Label)) + 6 // "  [ label ]" / "❯ [ label ]"
			if x >= 0 && x < buttonW {
				m.focusZone = 1
				m.focusedBtn = i
				if btn.Reset {
					return m, nil, FormActionReset
				}
				if btn.Cancel || i > 0 {
					return m, nil, FormActionCancel
				}
				return m, nil, FormActionSave
			}
			x -= buttonW + 2
		}
	}

	return m, nil, FormActionNone
}

func (m Form) updateExpanded(keyMsg tea.KeyPressMsg) (Form, tea.Cmd, FormAction) {
	switch keyMsg.Code {
	case tea.KeyEsc:
		m.expanded = -1
		return m, nil, FormActionNone
	case tea.KeyEnter:
		if si, ok := m.items[m.expanded].(*SelectItem); ok {
			list := si.List()
			updated, _ := list.Update(keyMsg)
			*list = updated
			switch list.Action() {
			case components.ListActionSelect:
				if selected, ok := list.Selected(); ok {
					si.SetValue(selected.ID)
				}
			case components.ListActionTyped:
				if typed := list.TypedValue(); typed != "" {
					si.SetValue(typed)
				}
			}
		}
		m.expanded = -1
		return m, nil, FormActionNone
	default:
		if si, ok := m.items[m.expanded].(*SelectItem); ok {
			updated, cmd := si.List().Update(keyMsg)
			*si.List() = updated
			return m, cmd, FormActionNone
		}
		return m, nil, FormActionNone
	}
}

func (m Form) updateButtons(keyMsg tea.KeyPressMsg) (Form, tea.Cmd, FormAction) {
	switch keyMsg.Code {
	case tea.KeyEsc:
		return m, nil, FormActionBack
	case tea.KeyUp:
		m.focusZone = 0
		m.focused = len(m.items) - 1
	case tea.KeyDown:
		m.focusZone = 0
		m.focused = 0
	case tea.KeyLeft:
		if m.focusedBtn > 0 {
			m.focusedBtn--
		}
	case tea.KeyRight:
		if m.focusedBtn < len(m.buttons)-1 {
			m.focusedBtn++
		}
	case tea.KeyTab:
		if keyMsg.Mod.Contains(tea.ModShift) {
			if m.focusedBtn > 0 {
				m.focusedBtn--
			} else {
				m.focusZone = 0
				m.focused = len(m.items) - 1
			}
		} else if m.focusedBtn < len(m.buttons)-1 {
			m.focusedBtn++
		}
	case tea.KeyEnter:
		btn := m.buttons[m.focusedBtn]
		if btn.Reset {
			return m, nil, FormActionReset
		}
		if m.focusedBtn == 0 {
			return m, nil, FormActionSave
		}
		return m, nil, FormActionCancel
	default:
		s := keyMsg.String()
		for i, btn := range m.buttons {
			if s == btn.Key {
				if btn.Reset {
					return m, nil, FormActionReset
				}
				if i == 0 {
					return m, nil, FormActionSave
				}
				return m, nil, FormActionCancel
			}
		}
	}

	return m, nil, FormActionNone
}

func (m Form) handleMouseClick(msg tea.MouseClickMsg) (Form, tea.Cmd, FormAction) {
	if msg.Button != tea.MouseLeft {
		return m, nil, FormActionNone
	}
	y := msg.Y
	x := msg.X

	// Expanded mode: route click to the SelectItem's List.
	// The rendered layout is:
	//   Line 0: leading blank
	//   Line 1: title (breadcrumb)
	//   Line 2: separator
	//   Line 3: filter prompt
	//   Line 4: separator
	//   Line 5+: items or status text
	// Items start at form line 5 → listY = formY - 5.
	if m.expanded >= 0 {
		if si, ok := m.items[m.expanded].(*SelectItem); ok {
			listY := y - 5
			if listY >= 0 {
				listMsg := tea.MouseClickMsg{X: x, Y: listY, Button: msg.Button}
				updated, cmd := si.List().Update(listMsg)
				*si.List() = updated
				if si.List().Action() == components.ListActionSelect {
					if selected, ok := si.List().Selected(); ok {
						si.SetValue(selected.ID)
					}
					m.expanded = -1
					m.focusZone = 0
				}
				return m, cmd, FormActionNone
			}
		}
		return m, nil, FormActionNone
	}

	// Non-expanded mode: use HitTest to find the clicked line.
	hit := m.HitTest(y)
	if hit.ItemIndex >= 0 {
		m.FocusItem(hit.ItemIndex)
		idx := hit.ItemIndex
		switch item := m.items[idx].(type) {
		case *TextItem:
			item.StartEditing()
		case *CycleItem:
			item.idx++
			if item.idx >= len(item.values) {
				item.idx = 0
			}
		case *SelectItem:
			m.expanded = idx
			return m, nil, FormActionExpand
		case *NavItem:
			return m, nil, FormActionNavigate
		}
		return m, nil, FormActionNone
	}

	if hit.InButtonRow {
		btnIdx := m.hitTestButtonRow(x)
		if btnIdx >= 0 {
			m.focusedBtn = btnIdx
			m.focusZone = 1
			btn := m.buttons[btnIdx]
			if btn.Reset {
				return m, nil, FormActionReset
			}
			if btnIdx == 0 {
				return m, nil, FormActionSave
			}
			return m, nil, FormActionCancel
		}
	}

	return m, nil, FormActionNone
}

// hitTestButtonRow maps the X coordinate (visible cell column) to a
// button index. Buttons are rendered as:
//
//	❯ [ Label ]  ❯ [ Label2 ]
//	^---width---^  ^---width---^
//
// Each button's visible width is 6 + len(label), and buttons are
// joined with "  " (2-cell gap). Focused and unfocused buttons
// have the same visible width — the "❯" is always 1 cell.
func (m Form) hitTestButtonRow(x int) int {
	cumX := 0
	for i, btn := range m.buttons {
		btnWidth := 6 + len([]rune(btn.Label))
		if x >= cumX && x < cumX+btnWidth {
			return i
		}
		cumX += btnWidth + 2
	}
	return -1
}

func (m Form) calcLabelWidth() int {
	maxW := 0
	for _, item := range m.items {
		l := len(item.InlineLabel())
		if l > maxW {
			maxW = l
		}
	}
	return maxW
}

func (m Form) View(width int, spinFrame int) string {
	var b strings.Builder

	b.WriteString("\n")

	if m.expanded >= 0 {
		if si, ok := m.items[m.expanded].(*SelectItem); ok {
			breadcrumb := m.title + " ❯ " + m.items[m.expanded].Label()
			si.List().SetTitle(breadcrumb)
			if si.List().SelectedID() != si.Value() {
				si.List().SetSelectedID(si.Value())
			}
			si.List().SetSpinFrame(spinFrame)
			b.WriteString(si.List().View(width))
		}
		return b.String()
	}

	accent := m.theme.Colors.PopupTitleFocused

	sepWidth := width
	if sepWidth < 20 {
		sepWidth = 20
	}

	titleStyle := lipgloss.NewStyle().Foreground(accent).Bold(true)
	b.WriteString(titleStyle.Render(m.title))
	b.WriteString("\n")
	b.WriteString(lipgloss.NewStyle().Foreground(m.theme.Colors.PopupDivider).Render(strings.Repeat("─", sepWidth)))
	b.WriteString("\n")

	labelWidth := m.calcLabelWidth()

	for i, item := range m.items {
		focused := m.focusZone == 0 && m.focused == i
		b.WriteString(item.View(focused, spinFrame, labelWidth))
		b.WriteString("\n")
	}

	if m.hasButtons {
		b.WriteString("\n")
		btnDim := m.theme.Styles.ButtonDim

		var btnStrs []string
		for i, btn := range m.buttons {
			if m.focusZone == 1 && m.focusedBtn == i {
				focusedStyle := lipgloss.NewStyle().Foreground(btn.Color).Bold(true)
				btnStrs = append(btnStrs, focusedStyle.Render("❯ [ "+btn.Label+" ]"))
			} else if btn.Cancel && btn.Color != nil {
				cancelStyle := lipgloss.NewStyle().Foreground(btn.Color)
				btnStrs = append(btnStrs, btnDim.Render("  [ ")+cancelStyle.Render(btn.Label)+btnDim.Render(" ]"))
			} else {
				btnStrs = append(btnStrs, btnDim.Render("  [ "+btn.Label+" ]"))
			}
		}
		b.WriteString(strings.Join(btnStrs, "  "))
		b.WriteString("\n")
	}

	return b.String()
}

func (m Form) Hint() string {
	var h string
	if m.expanded >= 0 {
		if si, ok := m.items[m.expanded].(*SelectItem); ok {
			h = si.List().Hint()
		}
	} else if m.hasButtons && m.focusZone == 1 {
		h = "← → choose · enter select · esc back"
	} else if ti, ok := m.items[m.focused].(*TextItem); ok && ti.IsEditing() {
		h = "editing · enter/esc confirm · ctrl+u clear"
	} else {
		h = m.hint
		if ni, ok := m.items[m.focused].(*NavItem); ok && ni.Desc() != "" {
			h = ni.Desc() + " · " + h
		}
	}
	if h != "" && !strings.HasPrefix(h, "[") {
		h = "[ " + h + " ]"
	}
	return h
}

// IsEditing returns true if the currently focused item is a TextItem
// in edit mode.
func (m Form) IsEditing() bool {
	if m.focusZone != 0 {
		return false
	}
	if ti, ok := m.items[m.focused].(*TextItem); ok {
		return ti.IsEditing()
	}
	return false
}

// FormLayout is the result of a HitTest on a Form's rendered output.
// It tells the dispatcher which item (if any) the user clicked, and
// whether the click landed on the button row instead of an item.
type FormLayout struct {
	// ItemIndex is the index of the clicked item, or -1 if the click
	// landed on a non-item line (title, separator, button row, or
	// out of range).
	ItemIndex int
	// InButtonRow is true when the click landed on the button row.
	// In that case, the caller should hit-test X to find the
	// specific button (FocusedBtn is left at -1 by HitTest).
	InButtonRow bool
	// FocusedBtn is set to the button index for a button-row click
	// when X-based hit-test resolves the ambiguity. HitTest itself
	// leaves this at -1; the dispatcher fills it in.
	FocusedBtn int
}

// HitTest maps a rendered-line index (0-based, within the form's
// rendered output) to either an item index, a button-row hit, or
// neither. Used by the mouse dispatcher to wire click-on-form-field
// to focus + (for TextItem) edit mode.
//
// Layout reminder (from form.View):
//
//	Line 0: empty (the leading "\n")
//	Line 1: title
//	Line 2: separator
//	Line 3..3+N-1: items, one per line
//	Optional blank line + button row (when hasButtons)
func (m Form) HitTest(renderedLine int) FormLayout {
	// Items start at line 3.
	if renderedLine >= 3 && renderedLine < 3+len(m.items) {
		return FormLayout{ItemIndex: renderedLine - 3, FocusedBtn: -1}
	}
	// Button row: optional blank line at 3+N, then buttons at 3+N+1.
	if m.hasButtons {
		buttonRow := 3 + len(m.items) + 1
		if renderedLine == buttonRow {
			// We don't have per-button Y info — the caller will
			// further hit-test the X coordinate. Return the row
			// indicator with FocusedBtn=-1 to signal "use X-test".
			return FormLayout{ItemIndex: -1, InButtonRow: true, FocusedBtn: -1}
		}
	}
	return FormLayout{ItemIndex: -1, FocusedBtn: -1}
}

// FocusItem sets the focus to the given item index. Used by the
// mouse dispatcher when the user clicks a form field. Also moves
// focus to the items zone (not the button row).
func (m *Form) FocusItem(idx int) {
	if idx >= 0 && idx < len(m.items) {
		m.focused = idx
		m.focusZone = 0
		m.expanded = -1
	}
}

// FocusedItem returns the currently focused item index, or -1 if
// focus is on the button row. Used by tests and the dispatcher.
func (m Form) FocusedItem() int {
	if m.focusZone == 0 {
		return m.focused
	}
	return -1
}

// FocusedTextItem returns the focused item as a *TextItem (with
// edit-mode helpers) if it is one, or nil otherwise. Used by the
// mouse dispatcher to put a TextItem into edit mode on click.
// Caller must not mutate; the returned pointer is into m.items.
func (m Form) FocusedTextItem() *TextItem {
	if m.focusZone != 0 || m.focused < 0 || m.focused >= len(m.items) {
		return nil
	}
	if ti, ok := m.items[m.focused].(*TextItem); ok {
		return ti
	}
	return nil
}

func (m Form) ExpandedIndex() int {
	return m.expanded
}

func (m *Form) ExpandItem(idx int) {
	if idx >= 0 && idx < len(m.items) {
		if _, ok := m.items[idx].(*SelectItem); ok {
			m.expanded = idx
		}
	}
}

func (m *Form) CollapseExpanded() {
	m.expanded = -1
}

func (m Form) ItemValue(idx int) string {
	if idx >= 0 && idx < len(m.items) {
		return m.items[idx].Value()
	}
	return ""
}

func (m *Form) SetItemValue(idx int, val string) {
	if idx >= 0 && idx < len(m.items) {
		m.items[idx].SetValue(val)
	}
}

func (m *Form) SetItemSelectOptions(idx int, options []components.ListItem) {
	if idx >= 0 && idx < len(m.items) {
		if si, ok := m.items[idx].(*SelectItem); ok {
			si.SetOptions(options)
		}
	}
}

func (m *Form) SetItemLoading(idx int, loading bool, msg string) {
	if idx >= 0 && idx < len(m.items) {
		if si, ok := m.items[idx].(*SelectItem); ok {
			si.SetLoading(loading, msg)
		}
	}
}

func (m *Form) SaveSnapshot() {
	items := make([]FormItem, len(m.items))
	for i, item := range m.items {
		items[i] = item.Snapshot()
	}
	m.snapshot = &formSnapshot{items: items}
}

func (m *Form) RestoreSnapshot() {
	if m.snapshot != nil {
		for i, snap := range m.snapshot.items {
			m.items[i].Restore(snap)
		}
		m.focused = 0
		m.focusZone = 0
		m.expanded = -1
	}
}

func (m Form) IsExpanded() bool {
	return m.expanded >= 0
}

func (m *Form) SetHint(h string) {
	m.hint = h
}

func (m Form) ItemCount() int {
	return len(m.items)
}

func (m *Form) SetItemFetchError(idx int, err string) {
	if idx >= 0 && idx < len(m.items) {
		if si, ok := m.items[idx].(*SelectItem); ok {
			si.SetFetchError(err)
		}
	}
}

func (m Form) IsItemLoading(idx int) bool {
	if idx >= 0 && idx < len(m.items) {
		if si, ok := m.items[idx].(*SelectItem); ok {
			return si.IsLoading()
		}
	}
	return false
}

func (m Form) ItemOptionCount(idx int) int {
	if idx >= 0 && idx < len(m.items) {
		if si, ok := m.items[idx].(*SelectItem); ok {
			return len(si.Options())
		}
	}
	return 0
}

func (m Form) FocusedNavTarget() (string, bool) {
	if m.focusZone == 0 && m.focused >= 0 && m.focused < len(m.items) {
		if ni, ok := m.items[m.focused].(*NavItem); ok {
			return ni.Target(), true
		}
	}
	return "", false
}
