package input

import (
	"fmt"
	"strings"

	"crude/tui/widget"
	tea "github.com/charmbracelet/bubbletea"
)

type Event interface{ event() }

type SubmitEvent struct{ Text string }

func (SubmitEvent) event() {}

type SlashCommandEvent struct{ Command string }

func (SlashCommandEvent) event() {}

type SettingsOpenEvent struct{}

func (SettingsOpenEvent) event() {}

type HistorySaveEvent struct{ History []string }

func (HistorySaveEvent) event() {}

type slashCmd struct {
	name  string
	label string
	desc  string
	icon  string
}

var availableCmds = []slashCmd{
	{name: "/settings", label: "settings", desc: "Open general settings"},
	{name: "/provider", label: "provider", desc: "Select provider"},
	{name: "/model", label: "model", desc: "Select model"},
	{name: "/oracle-model", label: "oracle model", desc: "Select oracle (reasoning) model"},
	{name: "/api-key", label: "api key", desc: "Set API key"},
	{name: "/effort", label: "effort", desc: "Cycle thinking effort (none/low/medium/high/xhigh)"},
	{name: "/undo", label: "undo", desc: "Undo last file operation"},
	{name: "/undo-n", label: "undo N", desc: "Undo last N file operations"},
	{name: "/undo-all", label: "undo all", desc: "Undo all file operations this session"},
	{name: "/changes", label: "changes", desc: "Show files changed this session"},
	{name: "/yolo", label: "yolo", desc: "Disable ALL safety checks (DANGEROUS)"},
	{name: "/help", label: "help", desc: "Show available commands"},
}

type Model struct {
	theme    *widget.Theme
	input    widget.TextInput
	history  []string
	histIdx  int
	histDraft, histPrefix string

	slashActive   bool
	slashIdx      int
	slashOffset   int
	slashFilter   string
	slashFiltered []slashCmd

	showHint bool
	width    int
	thinking bool
	blocking bool
}

func NewModel(t *widget.Theme) Model {
	return Model{
		theme: t,
		input: widget.NewTextInput(t),
	}
}

func (m Model) Update(msg tea.Msg, thinking, blocking bool) (Model, tea.Cmd, Event) {
	m.thinking = thinking
	m.blocking = blocking

	if m.slashActive {
		return m.updateSlash(msg)
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		if thinking && !blocking {
			return m, nil, nil
		}

		switch msg.Type {
		case tea.KeyEnter:
			if msg.Alt {
				var cmd tea.Cmd
				m.input, cmd = m.input.Update(msg, false)
				return m, cmd, nil
			}
			text := strings.TrimSpace(m.input.Value())
			if text == "" {
				return m, nil, nil
			}
			if strings.HasPrefix(text, "/") {
				m.appendHistory(text)
				return m, nil, SlashCommandEvent{Command: text}
			}
			m.appendHistory(text)
			m.input.Clear()
			return m, nil, SubmitEvent{Text: text}
		case tea.KeyUp:
			if m.histIdx < 0 {
				m.histDraft = m.input.Value()
				m.histPrefix = ""
				m.histIdx = len(m.history)
			}
			m.browseHistoryUp()
			return m, nil, nil
		case tea.KeyDown:
			m.browseHistoryDown()
			return m, nil, nil
		default:
			if strings.HasPrefix(m.input.Value(), "/") {
				m.slashActive = true
				m.slashFilter = strings.TrimPrefix(m.input.Value(), "/")
				m.updateSlashFilter()
				m.slashIdx = 0
			}
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg, false)
			return m, cmd, nil
		}
	}

	return m, nil, nil
}

func (m Model) View(width, chatAreaHeight int, settingsActive, confirmActive bool) string {
	var b strings.Builder

	if !m.thinking && m.input.Value() == "" {
		hint := "[ enter: send · alt+enter: new line · ↑↓: history · ctrl+s: settings · esc: exit ]"
		b.WriteString(m.theme.Styles.DimLabel.Render(hint))
		b.WriteByte('\n')
	}

	if m.thinking && !m.blocking {
		style := m.theme.Styles.DimLabel.Italic(true)
		b.WriteString(style.Render("▶ waiting for response..."))
		b.WriteByte('\n')
	}

	b.WriteString(m.input.View(m.InputTextWidth()))

	if m.slashActive && !m.thinking && !strings.Contains(m.input.Value(), "\n") {
		b.WriteByte('\n')
		dim := m.theme.Styles.DimLabel
		highlight := m.theme.Styles.Highlight
		accent := m.theme.Styles.InputPrompt

		end := m.slashOffset + 8
		if end > len(m.slashFiltered) {
			end = len(m.slashFiltered)
		}
		for i := m.slashOffset; i < end; i++ {
			cmd := m.slashFiltered[i]
			if i == m.slashIdx {
				b.WriteString(accent.Render(" ❯ ") + highlight.Render(cmd.label+" "+cmd.desc))
			} else {
				b.WriteString("   " + dim.Render(cmd.label+" "+cmd.desc))
			}
			if i < end-1 || len(m.slashFiltered) > end {
				b.WriteByte('\n')
			}
		}
		if len(m.slashFiltered) > end {
			b.WriteString(dim.Render(fmt.Sprintf("... and %d more", len(m.slashFiltered)-end)))
		}
	}

	return b.String()
}

func (m *Model) SetState(thinking, blocking bool) {
	m.thinking = thinking
	m.blocking = blocking
}

func (m *Model) SetWidth(w int) {
	m.width = w
}

func (m *Model) SetHistory(h []string) {
	m.history = h
}

func (m Model) History() []string {
	return m.history
}

func (m *Model) SetInput(s string) {
	m.input.SetValue(s)
}

func (m *Model) ClearInput() {
	m.input.Clear()
}

func (m Model) Height() int {
	h := m.input.Height()
	if !m.thinking && m.input.Value() == "" {
		h++
	}
	if m.slashActive {
		n := len(m.slashFiltered)
		if n > 9 {
			n = 9
		}
		h += n
	}
	return h
}

func (m Model) InputTextWidth() int {
	w := m.width - 4 - 2
	if w < 1 {
		w = 1
	}
	return w
}

func (m Model) FirstLineY(chatAreaHeight int, showHint, thinking bool, inputLen int) int {
	return 0
}

func (m Model) MouseToInputPos(mouseX, mouseY, chatAreaHeight int, showHint bool) (runeIdx int, ok bool) {
	return 0, false
}

func (m Model) updateSlash(msg tea.Msg) (Model, tea.Cmd, Event) {
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil, nil
	}

	switch keyMsg.Type {
	case tea.KeyEsc:
		m.slashActive = false
		return m, nil, nil
	case tea.KeyEnter, tea.KeyTab:
		if len(m.slashFiltered) > 0 && m.slashIdx >= 0 && m.slashIdx < len(m.slashFiltered) {
			cmd := m.slashFiltered[m.slashIdx]
			m.input.SetValue(cmd.name + " ")
			m.slashActive = false
			m.slashFilter = ""
			return m, nil, SlashCommandEvent{Command: cmd.name}
		}
		m.slashActive = false
		return m, nil, nil
	case tea.KeyUp:
		if len(m.slashFiltered) > 0 {
			m.slashIdx--
			if m.slashIdx < 0 {
				m.slashIdx = len(m.slashFiltered) - 1
			}
			m.clampSlashOffset()
		}
	case tea.KeyDown:
		if len(m.slashFiltered) > 0 {
			m.slashIdx++
			if m.slashIdx >= len(m.slashFiltered) {
				m.slashIdx = 0
			}
			m.clampSlashOffset()
		}
	case tea.KeyBackspace, tea.KeyDelete:
		if len(m.slashFilter) > 0 {
			runes := []rune(m.slashFilter)
			m.slashFilter = string(runes[:len(runes)-1])
			m.updateSlashFilter()
		} else {
			m.slashActive = false
		}
	default:
		for _, r := range keyMsg.Runes {
			m.slashFilter += string(r)
			m.updateSlashFilter()
		}
	}

	return m, nil, nil
}

func (m *Model) updateSlashFilter() {
	m.slashFiltered = nil
	m.slashIdx = 0
	m.slashOffset = 0
	s := strings.ToLower(m.slashFilter)
	for _, c := range availableCmds {
		if strings.HasPrefix(c.name, s) || strings.Contains(strings.ToLower(c.desc), s) {
			m.slashFiltered = append(m.slashFiltered, c)
		}
	}
}

func (m *Model) clampSlashOffset() {
	const slashPageSize = 8
	if m.slashIdx < m.slashOffset {
		m.slashOffset = m.slashIdx
	}
	if m.slashIdx >= m.slashOffset+slashPageSize {
		m.slashOffset = m.slashIdx - slashPageSize + 1
	}
	if m.slashOffset < 0 {
		m.slashOffset = 0
	}
}

func (m *Model) browseHistoryUp() {
	if m.histIdx <= 0 {
		return
	}
	m.histIdx--
	for m.histIdx > 0 {
		entry := m.history[m.histIdx-1]
		if m.histPrefix == "" || strings.HasPrefix(entry, m.histPrefix) {
			break
		}
		m.histIdx--
	}
	if m.histIdx >= 0 && m.histIdx < len(m.history) {
		m.input.SetValue(m.history[m.histIdx])
	}
}

func (m *Model) browseHistoryDown() {
	if m.histIdx >= len(m.history)-1 {
		m.histIdx = len(m.history)
		m.input.SetValue(m.histDraft)
		return
	}
	m.histIdx++
	if m.histIdx < len(m.history) {
		m.input.SetValue(m.history[m.histIdx])
	}
}

func (m *Model) appendHistory(text string) {
	if len(m.history) > 0 && m.history[len(m.history)-1] == text {
		return
	}
	m.history = append(m.history, text)
	if len(m.history) > 500 {
		m.history = m.history[len(m.history)-500:]
	}
}
