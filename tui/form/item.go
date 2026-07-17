package form

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"crude/tui/components"
	"crude/tui/rendering"
	"crude/tui/theme"
	"crude/tui/ui"
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
	edit          *ui.TextEdit
	masked        bool
	editing       bool
	lastInputTime time.Time
}

func NewTextItem(t *theme.Theme, label, value string, masked bool) *TextItem {
	ed := ui.NewTextEdit()
	ed.SetValue(value)
	return &TextItem{theme: t, label: label, edit: ed, masked: masked}
}

func (m *TextItem) ensureEdit() *ui.TextEdit {
	if m.edit == nil {
		m.edit = ui.NewTextEdit()
	}
	return m.edit
}

func (m *TextItem) View(focused bool, spinFrame int, labelWidth int) string {
	dim := m.theme.Styles.DimLabel
	accent := m.theme.Styles.InputPrompt
	textStyle := m.theme.Styles.InputText
	selStyle := m.theme.Styles.Highlight
	if focused {
		dim = dim.Background(m.theme.Colors.PopupSubtleBg)
		accent = accent.Background(m.theme.Colors.PopupSubtleBg)
		textStyle = textStyle.Background(m.theme.Colors.PopupSubtleBg)
	}

	ed := m.ensureEdit()
	val := ed.Value()
	if m.masked && val != "" {
		val = strings.Repeat("*", ed.Len())
	}

	prefix := "  "
	if focused {
		prefix = accent.Render("❯") + " "
	}

	labelFmt := fmt.Sprintf("%%-%ds%s", labelWidth, strings.Repeat(" ", m.theme.Spacing.LabelColGap))
	labelPart := dim.Render(fmt.Sprintf(labelFmt, m.label))

	if focused && m.editing {
		cur := ed.Cursor()
		n := ed.Len()
		runes := []rune(ed.Value())
		if m.masked {
			runes = make([]rune, n)
			for i := range runes {
				runes[i] = '*'
			}
		}
		before := string(runes[:cur])
		cursorSolid := time.Since(m.lastInputTime) < 800*time.Millisecond
		var mid string
		if cur < n {
			curChar := string(runes[cur])
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
			if cursorSolid {
				mid = lipgloss.NewStyle().Foreground(m.theme.Colors.Accent).Render("█")
			} else {
				mid = components.BlockCursor(spinFrame, m.theme.Colors.Accent)
			}
		}
		right := ""
		if cur < n && len(runes) > cur+1 {
			right = string(runes[cur+1:])
		}

		beforeStyled := m.applySelectionToBefore(before, selStyle, cur)
		midStyled := m.applySelectionToMid(mid, selStyle, cur)
		rightStyled := m.applySelectionToRight(right, selStyle, cur)
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

func (m *TextItem) applySelectionToBefore(before string, selStyle lipgloss.Style, editCursor int) string {
	if !m.HasSelection() {
		return m.theme.Styles.InputText.Render(before)
	}
	selLo, selHi := m.ensureEdit().SelectionRange()
	if editCursor > selLo {
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

func (m *TextItem) applySelectionToMid(mid string, selStyle lipgloss.Style, editCursor int) string {
	if !m.HasSelection() {
		return mid
	}
	selLo, selHi := m.ensureEdit().SelectionRange()
	if selLo <= editCursor && editCursor < selHi {
		return selStyle.Render(mid)
	}
	return mid
}

func (m *TextItem) applySelectionToRight(right string, selStyle lipgloss.Style, editCursor int) string {
	if !m.HasSelection() {
		return m.theme.Styles.InputText.Render(right)
	}
	selLo, selHi := m.ensureEdit().SelectionRange()
	startInRight := selLo - (editCursor + 1)
	endInRight := selHi - (editCursor + 1)
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
	ed := m.ensureEdit()
	if m.editing {
		m.lastInputTime = time.Now()
		ks := msg.String()
		switch ks {
		case "ctrl+c":
			if m.HasSelection() {
				return m, rendering.CopyToClipboardCmd(m.SelectedText()), FormItemActionNone, true
			}
			return m, nil, FormItemActionNone, true
		case "ctrl+x":
			if m.HasSelection() {
				text := m.SelectedText()
				ed.Backspace()
				return m, rendering.CopyToClipboardCmd(text), FormItemActionNone, true
			}
			return m, nil, FormItemActionNone, true
		}
		switch msg.Code {
		case tea.KeyEnter, tea.KeyEsc:
			m.editing = false
			m.ClearSelection()
			return m, nil, FormItemActionChanged, true
		case tea.KeyUp, tea.KeyDown:
			return m, nil, FormItemActionNone, false
		default:
			if ui.HandleTextEditKey(ed, msg) {
				return m, nil, FormItemActionNone, true
			}
			return m, nil, FormItemActionNone, true
		}
	}
	if msg.Code == tea.KeyEnter {
		if m.masked && ed.Len() > 0 {
			ed.SetCursor(ed.Len(), false)
		} else {
			ed.SetCursor(0, false)
		}
		m.editing = true
		m.lastInputTime = time.Now()
		return m, nil, FormItemActionNone, true
	}
	return m, nil, FormItemActionNone, false
}

func (m *TextItem) Label() string       { return m.label }
func (m *TextItem) InlineLabel() string { return m.label }
func (m *TextItem) Value() string       { return m.ensureEdit().Value() }
func (m *TextItem) SetValue(s string)   { m.ensureEdit().SetValue(s) }
func (m *TextItem) IsEditing() bool     { return m.editing }
func (m *TextItem) StartEditing() {
	m.editing = true
	m.ensureEdit().SetCursor(m.ensureEdit().Len(), false)
}

func (m *TextItem) HasSelection() bool  { return m.ensureEdit().HasSelection() }
func (m *TextItem) SelectedText() string { return m.ensureEdit().SelectedText() }

func (m *TextItem) SetSelection(start, end int) {
	ed := m.ensureEdit()
	if start == end {
		ed.ClearSelection()
		return
	}
	ed.SetCursor(start, false)
	ed.SetCursor(end, true)
}

func (m *TextItem) ClearSelection() { m.ensureEdit().ClearSelection() }
func (m *TextItem) SelectAll()      { m.ensureEdit().SelectAll() }

func (m *TextItem) HandlePaste(content string) {
	if !m.editing {
		return
	}
	m.ensureEdit().InsertRunes(content)
	m.lastInputTime = time.Now()
}

func (m *TextItem) Snapshot() FormItem {
	ed := ui.NewTextEdit()
	ed.SetValue(m.ensureEdit().Value())
	return &TextItem{theme: m.theme, label: m.label, edit: ed, masked: m.masked}
}

func (m *TextItem) Restore(snap FormItem) {
	if s, ok := snap.(*TextItem); ok {
		m.ensureEdit().SetValue(s.Value())
		m.editing = false
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
	if focused {
		dim = dim.Background(m.theme.Colors.PopupSubtleBg)
		accent = accent.Background(m.theme.Colors.PopupSubtleBg)
		textStyle = textStyle.Background(m.theme.Colors.PopupSubtleBg)
	}

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
func (m *SelectItem) SetValue(s string)   { m.value = s }

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

func (m *SelectItem) IsLoading() bool                { return m.loading }
func (m *SelectItem) LoadMsg() string                { return m.loadMsg }
func (m *SelectItem) Options() []components.ListItem { return m.list.Items() }
func (m *SelectItem) List() *components.List         { return &m.list }
func (m *SelectItem) SetFetchError(err string)       { m.list.SetFetchError(err) }

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

func (s *SeparatorItem) Label() string         { return "" }
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
	if focused {
		dim = dim.Background(m.theme.Colors.PopupSubtleBg)
		accent = accent.Background(m.theme.Colors.PopupSubtleBg)
		textStyle = textStyle.Background(m.theme.Colors.PopupSubtleBg)
	}

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
	if focused {
		dim = dim.Background(m.theme.Colors.PopupSubtleBg)
		accent = accent.Background(m.theme.Colors.PopupSubtleBg)
	}

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
func (m *NavItem) InlineLabel() string { return m.label }
func (m *NavItem) Value() string       { return m.target }
func (m *NavItem) SetValue(string)     {}
func (m *NavItem) Target() string      { return m.target }

func (m *NavItem) Snapshot() FormItem {
	return &NavItem{theme: m.theme, label: m.label, detail: m.detail, desc: m.desc, target: m.target}
}

func (m *NavItem) Restore(snap FormItem) {}
