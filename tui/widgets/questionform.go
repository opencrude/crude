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

// QuestionItem describes one question to ask the user.
// Mirrors the JSON shape emitted by the agent.
type QuestionItem struct {
	Question string   `json:"question"`
	Header   string   `json:"header,omitempty"`
	Options  []string `json:"options,omitempty"`
	Multiple bool     `json:"multiple,omitempty"`
}

// QuestionFormResult is returned when the user finishes the form.
type QuestionFormResult struct {
	Answers   map[string]string
	Questions map[string]string
	Cancelled bool
}

// QuestionForm holds the state for an interactive multi-question form.
// It is purely a data + render model — input routing happens in the viewmodel
// so we can reuse the main TextInput for free-text answers (multi-line, etc.)
// rather than reinventing an inline editor.
type QuestionForm struct {
	th      *theme.Theme
	items   []QuestionItem
	answers []string // current value per question (parallel to items)
	cycleIdx []int    // for option-based questions: current option index
	focused int       // currently focused question index
}

func NewQuestionForm(th *theme.Theme) QuestionForm {
	return QuestionForm{th: th}
}

// Reset re-initialises the form from a JSON-encoded questions array.
// Returns the number of questions parsed (0 if parse failed).
func (m *QuestionForm) Reset(questionsJSON string) int {
	m.items = nil
	m.answers = nil
	m.cycleIdx = nil
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
	m.cycleIdx = make([]int, len(items))
	for i, q := range items {
		if len(q.Options) > 0 {
			// default to first option but mark as not-yet-answered (-1 cycleIdx)
			m.cycleIdx[i] = -1
		}
	}
	return len(items)
}

// Items returns the parsed question items.
func (m *QuestionForm) Items() []QuestionItem { return m.items }

// Count returns the number of questions.
func (m *QuestionForm) Count() int { return len(m.items) }

// Focused returns the currently focused question index.
func (m *QuestionForm) Focused() int { return m.focused }

// SetFocused changes the focused question. Bounds-checked.
func (m *QuestionForm) SetFocused(idx int) {
	if idx < 0 || idx >= len(m.items) {
		return
	}
	m.focused = idx
}

// FocusNext advances focus to the next question (wraps around).
func (m *QuestionForm) FocusNext() {
	if len(m.items) == 0 {
		return
	}
	m.focused = (m.focused + 1) % len(m.items)
}

// FocusPrev moves focus to the previous question (wraps around).
func (m *QuestionForm) FocusPrev() {
	if len(m.items) == 0 {
		return
	}
	m.focused--
	if m.focused < 0 {
		m.focused = len(m.items) - 1
	}
}

// FocusedItem returns the currently focused question.
func (m *QuestionForm) FocusedItem() (QuestionItem, bool) {
	if m.focused < 0 || m.focused >= len(m.items) {
		return QuestionItem{}, false
	}
	return m.items[m.focused], true
}

// IsTextQuestion reports whether the focused question is free-text (no options).
func (m *QuestionForm) IsTextQuestion(idx int) bool {
	if idx < 0 || idx >= len(m.items) {
		return false
	}
	return len(m.items[idx].Options) == 0
}

// CycleOption moves the focused option question forward (+1) or backward (-1).
// No-op for text questions.
func (m *QuestionForm) CycleOption(delta int) {
	idx := m.focused
	if idx < 0 || idx >= len(m.items) {
		return
	}
	q := m.items[idx]
	if len(q.Options) == 0 {
		return
	}
	cur := m.cycleIdx[idx]
	if cur < 0 {
		// first interaction → select first option
		cur = 0
	} else {
		cur += delta
		n := len(q.Options)
		cur = ((cur % n) + n) % n
	}
	m.cycleIdx[idx] = cur
	m.answers[idx] = q.Options[cur]
}

// SetAnswer records a free-text answer for the question at idx.
func (m *QuestionForm) SetAnswer(idx int, text string) {
	if idx < 0 || idx >= len(m.items) {
		return
	}
	m.answers[idx] = text
}

// Answer returns the current answer for question idx.
func (m *QuestionForm) Answer(idx int) string {
	if idx < 0 || idx >= len(m.items) {
		return ""
	}
	return m.answers[idx]
}

// Answered reports the number of questions that have a non-empty answer.
func (m *QuestionForm) Answered() int {
	n := 0
	for _, a := range m.answers {
		if strings.TrimSpace(a) != "" {
			n++
		}
	}
	return n
}

// IsAnswered reports whether question idx has been answered.
func (m *QuestionForm) IsAnswered(idx int) bool {
	if idx < 0 || idx >= len(m.answers) {
		return false
	}
	return strings.TrimSpace(m.answers[idx]) != ""
}

// BuildResult assembles the final QuestionFormResult.
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

// TabTitle returns a short tab label for question idx (e.g. "1. Lang").
func (m *QuestionForm) TabTitle(idx int) string {
	if idx < 0 || idx >= len(m.items) {
		return ""
	}
	q := m.items[idx]
	label := q.Header
	if label == "" {
		runes := []rune(q.Question)
		if len(runes) > 12 {
			label = string(runes[:11]) + "…"
		} else {
			label = q.Question
		}
	}
	if runewidth.StringWidth(label) > 10 {
		label = runewidth.Truncate(label, 9, "…")
	}
	return fmt.Sprintf("%d. %s", idx+1, label)
}

// AllAnswered reports whether every question has a non-empty answer.
func (m *QuestionForm) AllAnswered() bool {
	return len(m.items) > 0 && m.Answered() == len(m.items)
}

// Update handles key messages for the question form.
// It manages navigation (↑↓), option cycling (←→), and submission.
// Text input for free-text questions is handled externally via the main input.
func (m QuestionForm) Update(msg tea.Msg) (QuestionForm, QuestionFormResult, bool) {
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, QuestionFormResult{}, false
	}

	switch keyMsg.Type {
	case tea.KeyEsc:
		return m, QuestionFormResult{Cancelled: true}, true
	case tea.KeyUp:
		m.FocusPrev()
		return m, QuestionFormResult{}, false
	case tea.KeyDown:
		m.FocusNext()
		return m, QuestionFormResult{}, false
	case tea.KeyLeft:
		m.CycleOption(-1)
		return m, QuestionFormResult{}, false
	case tea.KeyRight:
		m.CycleOption(1)
		return m, QuestionFormResult{}, false
	case tea.KeyEnter:
		if m.IsTextQuestion(m.focused) {
			return m, QuestionFormResult{}, false
		}
		if m.AllAnswered() {
			return m, m.BuildResult(), true
		}
		m.FocusNext()
		return m, QuestionFormResult{}, false
	case tea.KeyTab:
		if m.AllAnswered() {
			return m, m.BuildResult(), true
		}
		for i := 0; i < len(m.items); i++ {
			next := (m.focused + 1 + i) % len(m.items)
			if !m.IsAnswered(next) {
				m.focused = next
				break
			}
		}
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

// Hint returns a contextual hint for the bottom input area.
func (m QuestionForm) Hint() string {
	if len(m.items) == 0 {
		return ""
	}
	if m.IsTextQuestion(m.focused) {
		q := m.items[m.focused]
		label := q.Header
		if label == "" {
			runes := []rune(q.Question)
			if len(runes) > 20 {
				label = string(runes[:20]) + "…"
			} else {
				label = q.Question
			}
		}
		return fmt.Sprintf("[ enter: submit answer · alt+enter: new line · ↑↓: navigate · esc: cancel ]  ❯ Q%d (%s)", m.focused+1, label)
	}
	hint := "[ ↑↓: navigate · ← →: cycle · enter/tab: next · ctrl+s: submit all · esc: cancel ]"
	progress := fmt.Sprintf("  (%d/%d answered)", m.Answered(), len(m.items))
	return hint + progress
}

// SetWidth is a no-op kept for API compatibility (the form uses width param in View).
func (m *QuestionForm) SetWidth(int) {}

// Dismiss clears all state.
func (m *QuestionForm) Dismiss() {
	m.items = nil
	m.answers = nil
	m.cycleIdx = nil
	m.focused = 0
}

// View renders the form. All tabs render the same view — only the focused
// question's marker and option highlight change.
func (m *QuestionForm) View(spinFrame int, width int) string {
	if len(m.items) == 0 {
		return ""
	}

	dim := lipgloss.NewStyle().Foreground(m.th.Colors.Muted)
	accent := lipgloss.NewStyle().Foreground(m.th.Colors.Accent).Bold(true)
	textStyle := m.th.Styles.InputText
	checkStyle := lipgloss.NewStyle().Foreground(m.th.Colors.Success)
	titleStyle := lipgloss.NewStyle().Foreground(m.th.Colors.Accent).Bold(true)
	indent := strings.Repeat(" ", m.th.Spacing.FormIndent)

	var b strings.Builder
	b.WriteString("\n")

	// Header
	if len(m.items) > 1 {
		progress := fmt.Sprintf("  (%d/%d answered)", m.Answered(), len(m.items))
		b.WriteString(titleStyle.Render("?  questions") + dim.Render(progress))
	} else {
		b.WriteString(titleStyle.Render("?  question"))
	}
	b.WriteString("\n")
	b.WriteString(dim.Render(strings.Repeat("─", 24)))
	b.WriteString("\n\n")

	// Compute label width for alignment
	labelW := 0
	for i := range m.items {
		l := m.questionLabel(i)
		w := runewidth.StringWidth(l)
		if w > labelW {
			labelW = w
		}
	}
	if labelW > 24 {
		labelW = 24
	}

	// Render each question row
	for i := range m.items {
		isFocused := i == m.focused
		isAnswered := m.IsAnswered(i)

		// Prefix marker: ❯ for focused, ✓ for answered, blank otherwise
		var marker string
		switch {
		case isFocused:
			marker = accent.Render("❯ ")
		case isAnswered:
			marker = checkStyle.Render("✓ ")
		default:
			marker = "  "
		}

		// Number + label
		numStr := fmt.Sprintf("%d.", i+1)
		label := m.questionLabel(i)
		// pad label to labelW
		labelPad := labelW - runewidth.StringWidth(label)
		if labelPad < 0 {
			labelPad = 0
		}
		labelPart := label + strings.Repeat(" ", labelPad)

		// Style label by state
		var renderedLabel string
		if isFocused {
			renderedLabel = textStyle.Render(labelPart)
		} else {
			renderedLabel = dim.Render(labelPart)
		}

		// Value column
		valuePart := m.renderValue(i, isFocused)

		b.WriteString(indent + marker + dim.Render(numStr+" ") + renderedLabel + "  " + valuePart + "\n")
	}

	// Detail: show the full question text for focused item if header was used
	if m.focused >= 0 && m.focused < len(m.items) {
		q := m.items[m.focused]
		if q.Header != "" && q.Question != "" && q.Header != q.Question {
			b.WriteString("\n")
			b.WriteString(indent + dim.Render("  ") + dim.Render(q.Question) + "\n")
		}
	}

	return b.String()
}

// questionLabel returns the display label for question idx (Header or truncated Question).
func (m *QuestionForm) questionLabel(idx int) string {
	q := m.items[idx]
	if q.Header != "" {
		return q.Header
	}
	runes := []rune(q.Question)
	if len(runes) > 20 {
		return string(runes[:19]) + "…"
	}
	return q.Question
}

// renderValue renders the value cell for a question (the right-hand column).
func (m *QuestionForm) renderValue(idx int, isFocused bool) string {
	dim := lipgloss.NewStyle().Foreground(m.th.Colors.Muted)
	accent := lipgloss.NewStyle().Foreground(m.th.Colors.Accent)
	textStyle := m.th.Styles.InputText

	q := m.items[idx]
	val := m.answers[idx]

	if len(q.Options) > 0 {
		// Cycle-style: ← Option →
		if val == "" {
			if isFocused {
				return accent.Render("← ") + dim.Render("choose…") + accent.Render(" →")
			}
			return dim.Render("-")
		}
		if isFocused {
			return accent.Render("← ") + textStyle.Render(val) + accent.Render(" →")
		}
		return textStyle.Render(val)
	}

	// Text-style: show value or placeholder
	if val == "" {
		if isFocused {
			return dim.Render("type below… ") + accent.Render("▌")
		}
		return dim.Render("-")
	}
	display := val
	// truncate long values inline
	if runewidth.StringWidth(display) > 40 {
		display = runewidth.Truncate(display, 39, "…")
	}
	if isFocused {
		return textStyle.Render(display)
	}
	return dim.Render(display)
}
