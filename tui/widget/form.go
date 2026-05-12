package widget

import (
	"fmt"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
)

type Field struct {
	Label  string
	Value  []rune
	Masked bool
}

type FormAction int

const (
	FormActionNone FormAction = iota
	FormActionSave
	FormActionCancel
)

type Form struct {
	theme       *Theme
	fields      []Field
	focused     int
	editing     bool
	editCursor  int
	focusedBtn  int
	snapshot    *Form
}

func NewForm(t *Theme, fields ...Field) Form {
	return Form{theme: t, fields: fields}
}

func (m Form) Update(msg tea.Msg) (Form, tea.Cmd, FormAction) {
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil, FormActionNone
	}

	if m.editing {
		return m.updateEditing(keyMsg)
	}

	switch keyMsg.Type {
	case tea.KeyEnter:
		if m.focusedBtn == 0 {
			m.editing = true
			m.editCursor = len(m.fields[m.focused].Value)
		} else if m.focusedBtn == 1 {
			return m, nil, FormActionSave
		} else if m.focusedBtn == 2 {
			return m, nil, FormActionCancel
		}
	case tea.KeyEsc:
		if m.focusedBtn == 2 || m.focusedBtn == 1 {
			return m, nil, FormActionCancel
		}
		m.focusedBtn = 2
	case tea.KeyUp, tea.KeyShiftTab:
		m.focusedBtn--
		if m.focusedBtn < 0 {
			m.focusedBtn = 2
		}
	case tea.KeyDown, tea.KeyTab:
		m.focusedBtn++
		if m.focusedBtn > 2 {
			m.focusedBtn = 0
		}
	}

	return m, nil, FormActionNone
}

func (m Form) updateEditing(msg tea.KeyMsg) (Form, tea.Cmd, FormAction) {
	field := &m.fields[m.focused]

	switch msg.Type {
	case tea.KeyEnter, tea.KeyEsc:
		m.editing = false
	case tea.KeyBackspace:
		if m.editCursor > 0 {
			newVal := make([]rune, len(field.Value)-1)
			copy(newVal, field.Value[:m.editCursor-1])
			copy(newVal[m.editCursor-1:], field.Value[m.editCursor:])
			field.Value = newVal
			m.editCursor--
		}
	case tea.KeyDelete:
		if m.editCursor < len(field.Value) {
			newVal := make([]rune, len(field.Value)-1)
			copy(newVal, field.Value[:m.editCursor])
			copy(newVal[m.editCursor:], field.Value[m.editCursor+1:])
			field.Value = newVal
		}
	case tea.KeyLeft:
		if m.editCursor > 0 {
			m.editCursor--
		}
	case tea.KeyRight:
		if m.editCursor < len(field.Value) {
			m.editCursor++
		}
	case tea.KeyHome:
		m.editCursor = 0
	case tea.KeyEnd:
		m.editCursor = len(field.Value)
	default:
		for _, r := range msg.Runes {
			if unicode.IsPrint(r) {
				newVal := make([]rune, len(field.Value)+1)
				copy(newVal, field.Value[:m.editCursor])
				newVal[m.editCursor] = r
				copy(newVal[m.editCursor+1:], field.Value[m.editCursor:])
				field.Value = newVal
				m.editCursor++
			}
		}
	}

	return m, nil, FormActionNone
}

func (m Form) View(spinFrame int) string {
	var b strings.Builder

	dim := m.theme.Styles.DimLabel
	highlight := m.theme.Styles.Highlight

	for i, f := range m.fields {
		val := string(f.Value)
		if f.Masked && val != "" {
			val = strings.Repeat("*", len(f.Value))
		}
		display := val
		if display == "" {
			display = dim.Render("<empty>")
		}

		if m.focusedBtn == 0 && m.focused == i {
			if m.editing {
				cursor := BlockCursor(spinFrame, m.theme.Colors.Accent)
				before := string(f.Value[:m.editCursor])
				after := string(f.Value[m.editCursor:])
				b.WriteString(highlight.Render(fmt.Sprintf("  ❯ %s: %s%s%s", f.Label, before, cursor, after)))
			} else {
				b.WriteString(highlight.Render(fmt.Sprintf("  ❯ %s: %s", f.Label, display)))
			}
		} else {
			b.WriteString(fmt.Sprintf("    %s: %s", f.Label, display))
		}
		b.WriteString("\n")
	}

	b.WriteString("\n")

	saveBtn := "  [ Save ]"
	cancelBtn := "  [ Cancel ]"
	if m.focusedBtn == 1 {
		saveBtn = highlight.Render("❯ [ Save ]")
	} else {
		saveBtn = dim.Render(saveBtn)
	}
	if m.focusedBtn == 2 {
		cancelBtn = highlight.Render("❯ [ Cancel ]")
	} else {
		cancelBtn = dim.Render(cancelBtn)
	}

	b.WriteString(saveBtn + "  " + cancelBtn)
	b.WriteString("\n")

	return b.String()
}

func (m Form) ToMap() map[string]string {
	result := make(map[string]string, len(m.fields))
	for _, f := range m.fields {
		result[f.Label] = string(f.Value)
	}
	return result
}

func (m *Form) SaveSnapshot() {
	fields := make([]Field, len(m.fields))
	for i, f := range m.fields {
		val := make([]rune, len(f.Value))
		copy(val, f.Value)
		fields[i] = Field{Label: f.Label, Value: val, Masked: f.Masked}
	}
	m.snapshot = &Form{fields: fields}
}

func (m *Form) RestoreSnapshot() {
	if m.snapshot != nil {
		m.fields = m.snapshot.fields
		m.focused = 0
		m.editing = false
		m.focusedBtn = 0
	}
}

func (m Form) FocusedField() int {
	if m.focusedBtn == 0 {
		return m.focused
	}
	return -1
}

func (m *Form) SetFieldValue(idx int, val string) {
	if idx >= 0 && idx < len(m.fields) {
		m.fields[idx].Value = []rune(val)
	}
}

func (m Form) FieldValue(idx int) string {
	if idx >= 0 && idx < len(m.fields) {
		return string(m.fields[idx].Value)
	}
	return ""
}

func (m *Form) FocusField(idx int) {
	if idx >= 0 && idx < len(m.fields) {
		m.focused = idx
		m.focusedBtn = 0
		m.editing = false
	}
}
