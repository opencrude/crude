package form

import (
	"fmt"
	"strings"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"crude/tui/components"
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
	lastInputTime time.Time
}

func NewTextItem(t *theme.Theme, label, value string, masked bool) *TextItem {
	return &TextItem{theme: t, label: label, value: []rune(value), masked: masked}
}

func (m *TextItem) View(focused bool, spinFrame int, labelWidth int) string {
	dim := m.theme.Styles.DimLabel
	accent := m.theme.Styles.InputPrompt
	textStyle := m.theme.Styles.InputText

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
		before := string(m.value[:m.editCursor])
		after := string(m.value[m.editCursor:])
		if m.masked {
			before = strings.Repeat("*", len(m.value[:m.editCursor]))
			after = strings.Repeat("*", len(m.value[m.editCursor:]))
		}

		cursorSolid := time.Since(m.lastInputTime) < 800*time.Millisecond
		if m.editCursor < len(m.value) {
			curChar := string(m.value[m.editCursor])
			if m.masked {
				curChar = "*"
			}
			right := after[1:]
			if m.masked && len(after) > 0 {
				right = strings.Repeat("*", len(m.value)-m.editCursor-1)
			}
			if cursorSolid {
				rendered := lipgloss.NewStyle().Reverse(true).Render(curChar)
				return prefix + labelPart + textStyle.Render(before) + rendered + textStyle.Render(right)
			}
			if components.BlinkOn(spinFrame) {
				rendered := lipgloss.NewStyle().
					Background(m.theme.Colors.Accent).
					Foreground(m.theme.Colors.Bg).
					Render(curChar)
				return prefix + labelPart + textStyle.Render(before) + rendered + textStyle.Render(right)
			}
			return prefix + labelPart + textStyle.Render(before) + textStyle.Render(curChar) + textStyle.Render(right)
		}
		if cursorSolid {
			cursorChar := lipgloss.NewStyle().Foreground(m.theme.Colors.Accent).Render("█")
			return prefix + labelPart + textStyle.Render(before) + cursorChar
		}
		cursor := components.BlockCursor(spinFrame, m.theme.Colors.Accent)
		return prefix + labelPart + textStyle.Render(before) + cursor
	}

	display := val
	if display == "" {
		display = dim.Render("-")
	} else {
		display = textStyle.Render(display)
	}

	return prefix + labelPart + display
}

func (m *TextItem) HandleKey(msg tea.KeyPressMsg) (FormItem, tea.Cmd, FormItemAction, bool) {
	if m.editing {
		m.lastInputTime = time.Now()
		switch msg.Code {
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

func (m *TextItem) HandlePaste(content string) {
	if !m.editing {
		return
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
