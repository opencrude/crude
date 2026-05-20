package form

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

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
)

type Button struct {
	Label  string
	Key    string
	Color  lipgloss.Color
	Cancel bool
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
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil, FormActionNone
	}

	if keyMsg.Type == tea.KeyCtrlC {
		if m.expanded >= 0 {
			m.expanded = -1
			return m, nil, FormActionNone
		}
		return m, nil, FormActionBack
	}

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

	switch keyMsg.Type {
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
		if m.hasButtons {
			m.focusZone = 1
			m.focusedBtn = 0
		}
	case tea.KeyShiftTab:
		if m.hasButtons {
			m.focusZone = 1
			m.focusedBtn = len(m.buttons) - 1
		} else {
			if m.focused > 0 {
				m.focused--
			} else {
				m.focused = len(m.items) - 1
			}
		}
	}

	return m, nil, FormActionNone
}

func (m Form) updateExpanded(keyMsg tea.KeyMsg) (Form, tea.Cmd, FormAction) {
	switch keyMsg.Type {
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

func (m Form) updateButtons(keyMsg tea.KeyMsg) (Form, tea.Cmd, FormAction) {
	switch keyMsg.Type {
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
	case tea.KeyRight, tea.KeyTab:
		if m.focusedBtn < len(m.buttons)-1 {
			m.focusedBtn++
		}
	case tea.KeyShiftTab:
		if m.focusedBtn > 0 {
			m.focusedBtn--
		} else {
			m.focusZone = 0
			m.focused = len(m.items) - 1
		}
	case tea.KeyEnter:
		if m.focusedBtn == 0 {
			return m, nil, FormActionSave
		}
		return m, nil, FormActionCancel
	default:
		s := keyMsg.String()
		for i, btn := range m.buttons {
			if s == btn.Key {
				if i == 0 {
					return m, nil, FormActionSave
				}
				return m, nil, FormActionCancel
			}
		}
	}

	return m, nil, FormActionNone
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

	dim := m.theme.Styles.DimLabel
	accent := m.theme.Colors.Accent

	sepWidth := width
	if sepWidth < 20 {
		sepWidth = 20
	}

	titleStyle := lipgloss.NewStyle().Foreground(accent).Bold(true)
	b.WriteString(titleStyle.Render(m.title))
	b.WriteString("\n")
	b.WriteString(dim.Render(strings.Repeat("─", sepWidth)))
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
			} else if btn.Cancel && btn.Color != "" {
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
	}
	if h != "" && !strings.HasPrefix(h, "[") {
		h = "[ " + h + " ]"
	}
	return h
}

func (m Form) IsEditing() bool {
	if m.focusZone != 0 {
		return false
	}
	if ti, ok := m.items[m.focused].(*TextItem); ok {
		return ti.IsEditing()
	}
	return false
}

func (m Form) FocusedItem() int {
	if m.focusZone == 0 {
		return m.focused
	}
	return -1
}

func (m *Form) FocusItem(idx int) {
	if idx >= 0 && idx < len(m.items) {
		m.focused = idx
		m.focusZone = 0
		m.expanded = -1
	}
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
