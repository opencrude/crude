package input

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"crude/tui/components"
	"crude/tui/rendering"
)

func (m Model) Update(msg tea.Msg, thinking, blocking bool, chatAreaHeight int, showHint bool) (Model, tea.Cmd, Event) {
	m.thinking = thinking
	m.blocking = blocking

	if mouseMsg, ok := msg.(tea.MouseMsg); ok {
		return m.handleMouse(mouseMsg, chatAreaHeight, showHint)
	}

	if m.slashActive {
		return m.updateSlash(msg)
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		if m.showHint {
			m.showHint = false
		}

		// Alt+\ is a fallback newline shortcut that works on Windows Terminal,
		// where Alt+Enter is intercepted as "toggle fullscreen".
		if msg.Alt && msg.Type == tea.KeyRunes && len(msg.Runes) == 1 && msg.Runes[0] == '\\' {
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg, false)
			return m, cmd, nil
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
			m.appendHistory(text)
			m.input.Clear()
			m.slashActive = false
			m.histIdx = -1
			m.histDraft = ""
			m.histPrefix = ""
			if strings.HasPrefix(text, "/") {
				return m, nil, SlashCommandEvent{Command: text}
			}
			if m.thinking && !m.blocking {
				return m, nil, BtwSubmitEvent{Text: text}
			}
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
				cmd := "/" + item.ID
				m.appendHistory(cmd)
				m.input.Clear()
				m.slashActive = false
				m.histIdx = -1
				m.histDraft = ""
				m.histPrefix = ""
				return m, nil, SlashCommandEvent{Command: cmd}
			}
		}
		text := strings.TrimSpace(m.input.Value())
		if text != "" && strings.HasPrefix(text, "/") {
			m.appendHistory(text)
			m.input.Clear()
			m.slashActive = false
			m.histIdx = -1
			m.histDraft = ""
			m.histPrefix = ""
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

func (m Model) handleMouse(msg tea.MouseMsg, chatAreaHeight int, showHint bool) (Model, tea.Cmd, Event) {
	switch {
	case msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft:
		pos, ok := m.MouseToInputPos(msg.X, msg.Y, chatAreaHeight, showHint)
		if ok {
			m.input.SetSelection(pos, pos)
			m.input.SetCursorFlat(pos)
		} else {
			m.input.ClearSelection()
		}

	case msg.Action == tea.MouseActionMotion && m.input.IsSelecting():
		pos, ok := m.MouseToInputPos(msg.X, msg.Y, chatAreaHeight, showHint)
		if ok {
			m.input.UpdateSelectionEnd(pos)
		} else {
			firstY := m.FirstLineY(chatAreaHeight, showHint, m.thinking, m.input.Len())
			if msg.Y < firstY {
				m.input.UpdateSelectionEnd(0)
			} else {
				m.input.UpdateSelectionEnd(m.input.Len())
			}
		}

	case msg.Action == tea.MouseActionRelease && m.input.IsSelecting():
		selected := m.input.SelectedText()
		m.input.ClearSelection()
		if selected != "" {
			return m, rendering.CopyToClipboardCmd(selected), nil
		}

	case msg.Action == tea.MouseActionPress && (msg.Button == tea.MouseButtonWheelUp || msg.Button == tea.MouseButtonWheelDown):
		if msg.Button == tea.MouseButtonWheelUp {
			m.browseHistoryUp()
		} else {
			m.browseHistoryDown()
		}
	}

	return m, nil, nil
}
