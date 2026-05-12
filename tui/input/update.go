package input

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"crude/tui/components"
)

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

		if m.showHint {
			m.showHint = false
		}

		switch msg.Type {
		case tea.KeyCtrlS:
			return m, nil, SettingsOpenEvent{}

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
				m.slashActive = false
				return m, nil, SlashCommandEvent{Command: text}
			}
			m.appendHistory(text)
			m.input.Clear()
			m.slashActive = false
			m.histIdx = -1
			m.histDraft = ""
			m.histPrefix = ""
			return m, nil, SubmitEvent{Text: text}

		case tea.KeyUp:
			if m.input.CursorVisualLine() == 0 {
				m.browseHistoryUp()
				return m, nil, nil
			}
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg, false)
			return m, cmd, nil

		case tea.KeyDown:
			if m.input.CursorVisualLine() >= m.input.TotalVisualLines()-1 {
				m.browseHistoryDown()
				return m, nil, nil
			}
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg, false)
			return m, cmd, nil

		case tea.KeyBackspace, tea.KeyDelete:
			if m.histIdx >= 0 {
				m.histPrefix = strings.TrimSpace(m.input.Value())
				m.histDraft = m.input.Value()
				m.histIdx = -1
			}
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg, false)
			m.syncSlashState()
			return m, cmd, nil

		default:
			if m.histIdx >= 0 {
				m.histPrefix = strings.TrimSpace(m.input.Value())
				m.histDraft = m.input.Value()
				m.histIdx = -1
			}
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg, false)
			m.syncSlashState()
			return m, cmd, nil
		}
	}

	return m, nil, nil
}

func (m *Model) syncSlashState() {
	inputStr := m.input.Value()
	if strings.HasPrefix(inputStr, "/") && !strings.Contains(inputStr, " ") && !strings.Contains(inputStr, "\n") {
		m.slashActive = true
		m.slashList.SetFilter(strings.TrimPrefix(inputStr, "/"))
	} else {
		m.slashActive = false
	}
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

	case tea.KeyTab:
		if item, ok := m.slashList.Selected(); ok {
			m.input.SetValue("/" + item.ID + " ")
		}
		m.slashActive = false
		return m, nil, nil

	case tea.KeyEnter:
		m.slashList, _ = m.slashList.Update(msg)
		if m.slashList.Action() == components.ListActionSelect {
			if item, ok := m.slashList.Selected(); ok {
				m.input.SetValue("/" + item.ID + " ")
				m.slashActive = false
				return m, nil, SlashCommandEvent{Command: "/" + item.ID}
			}
		}
		text := strings.TrimSpace(m.input.Value())
		if text != "" && strings.HasPrefix(text, "/") {
			m.appendHistory(text)
			m.slashActive = false
			return m, nil, SlashCommandEvent{Command: text}
		}
		m.slashActive = false
		return m, nil, nil

	case tea.KeyUp:
		var cmd tea.Cmd
		m.slashList, cmd = m.slashList.Update(msg)
		return m, cmd, nil

	case tea.KeyDown:
		var cmd tea.Cmd
		m.slashList, cmd = m.slashList.Update(msg)
		return m, cmd, nil

	case tea.KeyBackspace, tea.KeyDelete:
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg, false)
		inputStr := m.input.Value()
		if inputStr == "" || !strings.HasPrefix(inputStr, "/") || strings.Contains(inputStr, " ") || strings.Contains(inputStr, "\n") {
			m.slashActive = false
		} else {
			m.slashList.SetFilter(strings.TrimPrefix(inputStr, "/"))
		}
		return m, cmd, nil

	default:
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg, false)
		inputStr := m.input.Value()
		if strings.HasPrefix(inputStr, "/") && !strings.Contains(inputStr, " ") && !strings.Contains(inputStr, "\n") {
			m.slashList.SetFilter(strings.TrimPrefix(inputStr, "/"))
		} else {
			m.slashActive = false
		}
		return m, cmd, nil
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
