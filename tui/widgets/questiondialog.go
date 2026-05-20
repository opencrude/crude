package widgets

import (
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"crude/tui/theme"
)

type QuestionResult struct {
	Answer    string
	Custom    bool
	Cancelled bool
}

type QuestionDialog struct {
	th          *theme.Theme
	question    string
	options     []string
	focusedOpt  int
	customInput []rune
	editing     bool
	editCursor  int
}

func NewQuestionDialog(th *theme.Theme) QuestionDialog {
	return QuestionDialog{
		th: th,
	}
}

func (m QuestionDialog) Update(msg tea.Msg) (QuestionDialog, QuestionResult, bool) {
	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		if m.editing {
			switch keyMsg.Type {
			case tea.KeyEsc:
				m.editing = false
				return m, QuestionResult{}, false
			case tea.KeyEnter:
				answer := string(m.customInput)
				if answer != "" {
					return m, QuestionResult{Answer: answer, Custom: true}, true
				}
				return m, QuestionResult{}, false
			case tea.KeyBackspace:
				if m.editCursor > 0 {
					newVal := make([]rune, len(m.customInput)-1)
					copy(newVal, m.customInput[:m.editCursor-1])
					copy(newVal[m.editCursor-1:], m.customInput[m.editCursor:])
					m.customInput = newVal
					m.editCursor--
				}
				return m, QuestionResult{}, false
			case tea.KeyDelete:
				if m.editCursor < len(m.customInput) {
					newVal := make([]rune, len(m.customInput)-1)
					copy(newVal, m.customInput[:m.editCursor])
					copy(newVal[m.editCursor:], m.customInput[m.editCursor+1:])
					m.customInput = newVal
				}
				return m, QuestionResult{}, false
			case tea.KeyLeft:
				if m.editCursor > 0 {
					m.editCursor--
				}
				return m, QuestionResult{}, false
			case tea.KeyRight:
				if m.editCursor < len(m.customInput) {
					m.editCursor++
				}
				return m, QuestionResult{}, false
			default:
				for _, r := range keyMsg.Runes {
					if unicode.IsPrint(r) {
						newVal := make([]rune, len(m.customInput)+1)
						copy(newVal, m.customInput[:m.editCursor])
						newVal[m.editCursor] = r
						copy(newVal[m.editCursor+1:], m.customInput[m.editCursor:])
						m.customInput = newVal
						m.editCursor++
					}
				}
				return m, QuestionResult{}, false
			}
		}

		switch keyMsg.Type {
		case tea.KeyUp:
			if m.focusedOpt > 0 {
				m.focusedOpt--
			}
			return m, QuestionResult{}, false
		case tea.KeyDown:
			if m.focusedOpt < len(m.options) {
				m.focusedOpt++
			}
			return m, QuestionResult{}, false
		case tea.KeyEnter:
			if m.focusedOpt < len(m.options) {
				return m, QuestionResult{Answer: m.options[m.focusedOpt]}, true
			}
			if m.focusedOpt == len(m.options) {
				m.editing = true
				m.editCursor = len(m.customInput)
				return m, QuestionResult{}, false
			}
		case tea.KeyEsc:
			return m, QuestionResult{Cancelled: true}, true
		}
	}

	return m, QuestionResult{}, false
}

func (m QuestionDialog) View(spinFrame int) string {
	dim := lipgloss.NewStyle().Foreground(m.th.Colors.Muted)
	accent := lipgloss.NewStyle().Foreground(m.th.Colors.Accent)
	textStyle := m.th.Styles.InputText
	indent := strings.Repeat(" ", m.th.Spacing.FormIndent)

	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(m.th.Styles.AccentLabel.Render("?  question"))
	b.WriteString("\n")
	b.WriteString(dim.Render(strings.Repeat("─", 14)))
	b.WriteString("\n\n")

	b.WriteString(indent + textStyle.Render(m.question))
	b.WriteString("\n\n")

	for i, opt := range m.options {
		prefix := "  "
		if i == m.focusedOpt {
			prefix = accent.Render("❯") + " "
			b.WriteString(indent + prefix + accent.Render(opt) + "\n")
		} else {
			b.WriteString(indent + prefix + dim.Render(opt) + "\n")
		}
	}

	customPrefix := "  "
	if m.focusedOpt == len(m.options) {
		customPrefix = accent.Render("❯") + " "
	}
	customLabel := dim.Render("Type your own answer")
	if m.editing {
		customLabel = textStyle.Render("✎ " + string(m.customInput))
	}
	b.WriteString(indent + customPrefix + customLabel + "\n")

	b.WriteString("\n")
	b.WriteString(dim.Render(indent + "↑↓ select · enter confirm · esc cancel"))
	b.WriteString("\n")

	return b.String()
}

func (m *QuestionDialog) Reset(question string, options []string) {
	m.question = question
	m.options = options
	m.focusedOpt = 0
	m.customInput = nil
	m.editing = false
	m.editCursor = 0
}

func (m *QuestionDialog) Dismiss() {
	m.question = ""
	m.options = nil
	m.focusedOpt = 0
	m.customInput = nil
	m.editing = false
	m.editCursor = 0
}
