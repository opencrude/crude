package form

import (
	"fmt"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"

	"crude/tui/components"
	"crude/tui/theme"
)

type FormItemAction int

const (
	FormItemActionNone FormItemAction = iota
	FormItemActionExpand
	FormItemActionChanged
)

type FormItem interface {
	View(focused bool, spinFrame int) string
	HandleKey(msg tea.KeyMsg) (item FormItem, cmd tea.Cmd, action FormItemAction, handled bool)
	Label() string
	Value() string
	SetValue(string)
	Snapshot() FormItem
	Restore(FormItem)
}

type TextItem struct {
	theme      *theme.Theme
	label      string
	value      []rune
	masked     bool
	editing    bool
	editCursor int
}

func NewTextItem(t *theme.Theme, label, value string, masked bool) *TextItem {
	return &TextItem{theme: t, label: label, value: []rune(value), masked: masked}
}

func (m *TextItem) View(focused bool, spinFrame int) string {
	dim := m.theme.Styles.DimLabel
	highlight := m.theme.Styles.Highlight

	val := string(m.value)
	if m.masked && val != "" {
		val = strings.Repeat("*", len(m.value))
	}
	display := val
	if display == "" {
		display = dim.Render("-")
	}

	if focused {
		if m.editing {
			cursor := components.BlockCursor(spinFrame, m.theme.Colors.Accent)
			before := string(m.value[:m.editCursor])
			after := string(m.value[m.editCursor:])
			return highlight.Render(fmt.Sprintf(" ❯ %s: %s%s%s", m.label, before, cursor, after))
		}
		return highlight.Render(fmt.Sprintf(" ❯ %s: %s", m.label, display))
	}
	return fmt.Sprintf("   %s: %s", m.label, display)
}

func (m *TextItem) HandleKey(msg tea.KeyMsg) (FormItem, tea.Cmd, FormItemAction, bool) {
	if m.editing {
		switch msg.Type {
		case tea.KeyEnter, tea.KeyEsc:
			m.editing = false
			return m, nil, FormItemActionChanged, true
		case tea.KeyUp, tea.KeyDown:
			return m, nil, FormItemActionNone, false
		case tea.KeyBackspace:
			if m.editCursor > 0 {
				newVal := make([]rune, len(m.value)-1)
				copy(newVal, m.value[:m.editCursor-1])
				copy(newVal[m.editCursor-1:], m.value[m.editCursor:])
				m.value = newVal
				m.editCursor--
			}
			return m, nil, FormItemActionNone, true
		case tea.KeyDelete:
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
			return m, nil, FormItemActionNone, true
		case tea.KeyRight:
			if m.editCursor < len(m.value) {
				m.editCursor++
			}
			return m, nil, FormItemActionNone, true
		case tea.KeyHome:
			m.editCursor = 0
			return m, nil, FormItemActionNone, true
		case tea.KeyEnd:
			m.editCursor = len(m.value)
			return m, nil, FormItemActionNone, true
		default:
			for _, r := range msg.Runes {
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

	if msg.Type == tea.KeyEnter {
		if m.masked {
			m.value = nil
		}
		m.editing = true
		m.editCursor = len(m.value)
		return m, nil, FormItemActionNone, true
	}

	return m, nil, FormItemActionNone, false
}

func (m *TextItem) Label() string      { return m.label }
func (m *TextItem) Value() string      { return string(m.value) }
func (m *TextItem) SetValue(s string)  { m.value = []rune(s) }
func (m *TextItem) IsEditing() bool    { return m.editing }
func (m *TextItem) StartEditing()      { m.editing = true; m.editCursor = len(m.value) }

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
	return &SelectItem{theme: t, label: label, value: value, list: list}
}

func (m *SelectItem) View(focused bool, spinFrame int) string {
	dim := m.theme.Styles.DimLabel
	highlight := m.theme.Styles.Highlight

	display := m.value
	if display == "" {
		display = dim.Render("-")
	}

	if focused {
		return highlight.Render(fmt.Sprintf(" ❯ %s: %s", m.label, display))
	}
	return fmt.Sprintf("   %s: %s", m.label, display)
}

func (m *SelectItem) HandleKey(msg tea.KeyMsg) (FormItem, tea.Cmd, FormItemAction, bool) {
	if msg.Type == tea.KeyEnter {
		return m, nil, FormItemActionExpand, true
	}
	return m, nil, FormItemActionNone, false
}

func (m *SelectItem) Label() string     { return m.label }
func (m *SelectItem) Value() string     { return m.value }
func (m *SelectItem) SetValue(s string) { m.value = s }

func (m *SelectItem) SetOptions(items []components.ListItem) {
	m.list.SetItems(items)
	m.loading = false
}

func (m *SelectItem) SetLoading(loading bool, msg string) {
	m.loading = loading
	m.loadMsg = msg
}

func (m *SelectItem) IsLoading() bool { return m.loading }
func (m *SelectItem) LoadMsg() string { return m.loadMsg }
func (m *SelectItem) Options() []components.ListItem { return m.list.Items() }
func (m *SelectItem) List() *components.List          { return &m.list }

func (m *SelectItem) Snapshot() FormItem {
	return &SelectItem{theme: m.theme, label: m.label, value: m.value, list: m.list, loading: m.loading, loadMsg: m.loadMsg}
}

func (m *SelectItem) Restore(snap FormItem) {
	if s, ok := snap.(*SelectItem); ok {
		m.value = s.value
		m.loading = false
	}
}

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

func (m *CycleItem) View(focused bool, spinFrame int) string {
	dim := m.theme.Styles.DimLabel
	highlight := m.theme.Styles.Highlight

	arrowL := dim.Render(" ← ")
	arrowR := dim.Render(" → ")
	display := m.values[m.idx]

	if focused {
		return highlight.Render(fmt.Sprintf(" ❯ %s: %s%s%s", m.label, arrowL, display, arrowR))
	}
	return fmt.Sprintf("   %s: %s%s%s", m.label, arrowL, display, arrowR)
}

func (m *CycleItem) HandleKey(msg tea.KeyMsg) (FormItem, tea.Cmd, FormItemAction, bool) {
	switch msg.Type {
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
	}
	return m, nil, FormItemActionNone, false
}

func (m *CycleItem) Label() string     { return m.label }
func (m *CycleItem) Value() string     { return m.values[m.idx] }
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
