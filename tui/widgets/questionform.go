package widgets

import (
	"encoding/json"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"

	"crude/tui/theme"
)

type QuestionItem struct {
	Question string   `json:"question"`
	Header   string   `json:"header,omitempty"`
	Options  []string `json:"options,omitempty"`
	Multiple bool     `json:"multiple,omitempty"`
}

type QuestionFormResult struct {
	Answers   map[string]string
	Questions map[string]string
	Cancelled bool
}

type QuestionForm struct {
	th           *theme.Theme
	items        []QuestionItem
	answers      []string
	optionCursor []int
	textMode     []bool
	focused      int
}

func NewQuestionForm(th *theme.Theme) QuestionForm {
	return QuestionForm{th: th}
}

func (m *QuestionForm) Reset(questionsJSON string) int {
	m.items = nil
	m.answers = nil
	m.optionCursor = nil
	m.textMode = nil
	m.focused = 0

	if questionsJSON == "" {
		return 0
	}
	var items []QuestionItem
	if err := json.Unmarshal([]byte(questionsJSON), &items); err != nil || len(items) == 0 {
		return 0
	}
	m.items = items
	m.answers = make([]string, len(items))
	m.optionCursor = make([]int, len(items))
	m.textMode = make([]bool, len(items))
	for i, q := range items {
		if len(q.Options) == 0 {
			m.textMode[i] = true
		}
	}
	return len(items)
}

func (m *QuestionForm) Items() []QuestionItem     { return m.items }
func (m *QuestionForm) Count() int                 { return len(m.items) }
func (m *QuestionForm) Focused() int               { return m.focused }
func (m *QuestionForm) SetFocused(idx int) {
	if idx < 0 || idx >= len(m.items) {
		return
	}
	m.focused = idx
}

func (m *QuestionForm) FocusNext() {
	if len(m.items) == 0 {
		return
	}
	m.focused = (m.focused + 1) % len(m.items)
}

func (m *QuestionForm) FocusPrev() {
	if len(m.items) == 0 {
		return
	}
	m.focused--
	if m.focused < 0 {
		m.focused = len(m.items) - 1
	}
}

func (m *QuestionForm) FocusedItem() (QuestionItem, bool) {
	if m.focused < 0 || m.focused >= len(m.items) {
		return QuestionItem{}, false
	}
	return m.items[m.focused], true
}

func (m *QuestionForm) IsTextQuestion(idx int) bool {
	if idx < 0 || idx >= len(m.items) {
		return false
	}
	if m.textMode == nil {
		return len(m.items[idx].Options) == 0
	}
	return m.textMode[idx]
}

func (m *QuestionForm) SetAnswer(idx int, text string) {
	if idx < 0 || idx >= len(m.items) {
		return
	}
	m.answers[idx] = text
}

func (m *QuestionForm) ExitTextMode(idx int) {
	if idx < 0 || idx >= len(m.items) {
		return
	}
	m.textMode[idx] = false
	m.optionCursor[idx] = len(m.items[idx].Options)
}

func (m *QuestionForm) Answer(idx int) string {
	if idx < 0 || idx >= len(m.answers) {
		return ""
	}
	return m.answers[idx]
}

func (m *QuestionForm) Answered() int {
	n := 0
	for _, a := range m.answers {
		if strings.TrimSpace(a) != "" {
			n++
		}
	}
	return n
}

func (m *QuestionForm) IsAnswered(idx int) bool {
	if idx < 0 || idx >= len(m.answers) {
		return false
	}
	return strings.TrimSpace(m.answers[idx]) != ""
}

func (m *QuestionForm) BuildResult() QuestionFormResult {
	answers := make(map[string]string, len(m.items))
	questions := make(map[string]string, len(m.items))
	for i, q := range m.items {
		key := fmt.Sprintf("%d", i+1)
		answers[key] = m.answers[i]
		questions[key] = q.Question
	}
	return QuestionFormResult{Answers: answers, Questions: questions}
}

func (m *QuestionForm) TabTitle(idx int) string {
	if idx < 0 || idx >= len(m.items) {
		return ""
	}
	q := m.items[idx]
	label := q.Header
	if label == "" {
		label = q.Question
	}
	// Allow generous titles — the tab renderer truncates to fit the
	// dynamically computed sidebar width.
	runes := []rune(label)
	if len(runes) > 25 {
		label = string(runes[:24]) + "…"
	}
	return fmt.Sprintf("%d. %s", idx+1, label)
}

func (m *QuestionForm) TabIcon(idx int) string {
	if idx < 0 || idx >= len(m.items) {
		return ""
	}
	if m.IsAnswered(idx) {
		return "✓"
	}
	return "○"
}

func (m *QuestionForm) AllAnswered() bool {
	return len(m.items) > 0 && m.Answered() == len(m.items)
}

func (m *QuestionForm) rowCount(idx int) int {
	if idx < 0 || idx >= len(m.items) {
		return 0
	}
	return len(m.items[idx].Options) + 3
}

func (m QuestionForm) Update(msg tea.Msg) (QuestionForm, QuestionFormResult, bool) {
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, QuestionFormResult{}, false
	}

	idx := m.focused
	if idx < 0 || idx >= len(m.items) {
		switch keyMsg.Type {
		case tea.KeyEsc:
			return m, QuestionFormResult{Cancelled: true}, true
		}
		return m, QuestionFormResult{}, false
	}

	if m.textMode[idx] {
		switch keyMsg.Type {
		case tea.KeyEsc:
			return m, QuestionFormResult{Cancelled: true}, true
		}
		return m, QuestionFormResult{}, false
	}

	rowCount := m.rowCount(idx)
	ownRow := len(m.items[idx].Options)
	submitRow := ownRow + 1
	cancelRow := ownRow + 2

	switch keyMsg.Type {
	case tea.KeyEsc:
		return m, QuestionFormResult{Cancelled: true}, true
	case tea.KeyUp:
		if m.optionCursor[idx] > 0 {
			m.optionCursor[idx]--
		}
		return m, QuestionFormResult{}, false
	case tea.KeyDown:
		if m.optionCursor[idx] < rowCount-1 {
			m.optionCursor[idx]++
		}
		return m, QuestionFormResult{}, false
	case tea.KeyLeft:
		if m.optionCursor[idx] == cancelRow {
			m.optionCursor[idx] = submitRow
		} else if m.optionCursor[idx] == submitRow {
			m.optionCursor[idx] = cancelRow
		}
		return m, QuestionFormResult{}, false
	case tea.KeyRight:
		if m.optionCursor[idx] == submitRow {
			m.optionCursor[idx] = cancelRow
		} else if m.optionCursor[idx] == cancelRow {
			m.optionCursor[idx] = submitRow
		}
		return m, QuestionFormResult{}, false
	case tea.KeyEnter:
		cur := m.optionCursor[idx]
		q := m.items[idx]
		if cur < len(q.Options) {
			m.answers[idx] = q.Options[cur]
			m.textMode[idx] = false
			if m.AllAnswered() {
				return m, m.BuildResult(), true
			}
			m.FocusNext()
		} else if cur == ownRow {
			m.textMode[idx] = true
		} else if cur == submitRow {
			if m.AllAnswered() {
				return m, m.BuildResult(), true
			}
			m.FocusNext()
		} else if cur == cancelRow {
			return m, QuestionFormResult{Cancelled: true}, true
		}
		return m, QuestionFormResult{}, false
	case tea.KeyTab:
		m.FocusNext()
		return m, QuestionFormResult{}, false
	case tea.KeyShiftTab:
		m.FocusPrev()
		return m, QuestionFormResult{}, false
	}

	if keyMsg.String() == "ctrl+s" {
		if m.AllAnswered() {
			return m, m.BuildResult(), true
		}
		return m, QuestionFormResult{}, false
	}

	return m, QuestionFormResult{}, false
}

func (m *QuestionForm) Hint() string {
	if len(m.items) == 0 {
		return ""
	}
	idx := m.focused
	if idx < 0 || idx >= len(m.items) {
		return ""
	}
	if m.textMode[idx] {
		q := m.items[idx]
		label := q.Header
		if label == "" {
			runes := []rune(q.Question)
			if len(runes) > 20 {
				label = string(runes[:20]) + "…"
			} else {
				label = q.Question
			}
		}
		return fmt.Sprintf("[ enter: submit answer · alt+enter: new line · esc: back · ctrl+s: submit all ]  ❯ Q%d (%s)", idx+1, label)
	}
	ownRow := len(m.items[idx].Options)
	submitRow := ownRow + 1
	cancelRow := ownRow + 2
	cur := m.optionCursor[idx]
	if cur == cancelRow {
		return "[ enter: cancel · ↑↓: navigate ]"
	}
	if cur == submitRow {
		if m.AllAnswered() {
			return "[ enter: submit all · ↑↓: navigate ]"
		}
		return fmt.Sprintf("[ ↑↓: navigate · %d unanswered ]", len(m.items)-m.Answered())
	}
	hint := "[ ↑↓: navigate · enter: select · tab: next question · esc: cancel"
	if m.Count() > 1 {
		hint += fmt.Sprintf(" · %d/%d", m.focused+1, len(m.items))
	}
	hint += " ]"
	return hint
}

func (m *QuestionForm) SetWidth(int) {}

func (m *QuestionForm) Dismiss() {
	m.items = nil
	m.answers = nil
	m.optionCursor = nil
	m.textMode = nil
	m.focused = 0
}

func (m *QuestionForm) View(spinFrame int, width int) string {
	if len(m.items) == 0 {
		return ""
	}
	idx := m.focused
	if idx < 0 || idx >= len(m.items) {
		return ""
	}

	q := m.items[idx]
	dim := lipgloss.NewStyle().Foreground(m.th.Colors.Muted)
	accent := lipgloss.NewStyle().Foreground(m.th.Colors.Accent).Bold(true)
	textStyle := m.th.Styles.InputText
	titleStyle := lipgloss.NewStyle().Foreground(m.th.Colors.Accent).Bold(true)

	var b strings.Builder
	b.WriteString("\n")

	headerLabel := q.Header
	if headerLabel == "" {
		headerLabel = q.Question
	}
	// Use available content width for the title, leaving room for "? " prefix.
	maxLabelW := width - 4
	if maxLabelW < 20 {
		maxLabelW = 20
	}
	if runewidth.StringWidth(headerLabel) > maxLabelW {
		headerLabel = runewidth.Truncate(headerLabel, maxLabelW-1, "…")
	}

	if m.Count() > 1 {
		progress := fmt.Sprintf("  (%d/%d)", m.focused+1, len(m.items))
		b.WriteString(titleStyle.Render("? "+headerLabel) + dim.Render(progress))
	} else {
		b.WriteString(titleStyle.Render("? " + headerLabel))
	}
	b.WriteString("\n")

	if q.Header != "" && q.Question != "" && q.Header != q.Question {
		b.WriteString(dim.Render(q.Question))
		b.WriteString("\n")
	}

	sepW := width
	if sepW < 20 {
		sepW = 20
	}
	b.WriteString(dim.Render(strings.Repeat("─", sepW)))
	b.WriteString("\n")

	cursor := m.optionCursor[idx]
	checkStyle := lipgloss.NewStyle().Foreground(m.th.Colors.Success)
	cancelStyle := lipgloss.NewStyle().Foreground(m.th.Colors.Error).Bold(true)
	ownRow := len(q.Options)
	submitRow := ownRow + 1
	cancelRow := ownRow + 2
	isTextActive := m.textMode[idx]

	for i, opt := range q.Options {
		if !isTextActive && i == cursor {
			marker := accent.Render("❯")
			if m.answers[idx] == opt {
				b.WriteString(marker + " " + textStyle.Render(opt) + " " + checkStyle.Render("✓"))
			} else {
				b.WriteString(marker + " " + textStyle.Render(opt))
			}
		} else {
			if m.answers[idx] == opt && !isTextActive {
				b.WriteString("  " + dim.Render(opt) + " " + checkStyle.Render("✓"))
			} else {
				b.WriteString("  " + dim.Render(opt))
			}
		}
		b.WriteString("\n")
	}

	if isTextActive || (!isTextActive && cursor == ownRow) {
		marker := accent.Render("❯")
		answerText := m.answers[idx]
		if answerText != "" {
			displayText := answerText
			if runewidth.StringWidth(displayText) > sepW-6 {
				displayText = runewidth.Truncate(displayText, sepW-7, "…")
			}
			b.WriteString(marker + " " + textStyle.Render(displayText) + " " + checkStyle.Render("✓"))
		} else {
			b.WriteString(marker + " " + textStyle.Render("Type your own answer…"))
		}
	} else {
		answerText := m.answers[idx]
		if answerText != "" {
			displayText := answerText
			if runewidth.StringWidth(displayText) > sepW-6 {
				displayText = runewidth.Truncate(displayText, sepW-7, "…")
			}
			b.WriteString("  " + dim.Render(displayText) + " " + checkStyle.Render("✓"))
		} else {
			b.WriteString("  " + dim.Render("Type your own answer…"))
		}
	}
	b.WriteString("\n")

	b.WriteString(dim.Render(strings.Repeat("─", sepW)))
	b.WriteString("\n")

	submitLabel := "Next"
	if m.AllAnswered() {
		submitLabel = "Submit"
	}
	var submitBtn, cancelBtn string
	if !isTextActive && cursor == submitRow {
		submitBtn = accent.Render("❯") + " " + dim.Render("[") + " " + submitLabel + " " + dim.Render("]")
	} else {
		submitBtn = "  " + dim.Render("[") + " " + submitLabel + " " + dim.Render("]")
	}
	if !isTextActive && cursor == cancelRow {
		cancelBtn = accent.Render("❯") + " " + dim.Render("[") + " " + cancelStyle.Render("Cancel") + " " + dim.Render("]")
	} else {
		cancelBtn = "  " + dim.Render("[") + " " + cancelStyle.Render("Cancel") + " " + dim.Render("]")
	}
	b.WriteString(submitBtn + "  " + cancelBtn)
	b.WriteString("\n")

	return b.String()
}
