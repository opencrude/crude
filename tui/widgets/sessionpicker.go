package widgets

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"crude/sessiondb"
	"crude/tui/components"
	"crude/tui/theme"
)

type SessionPicker struct {
	dialog   components.Dialog
	th       *theme.Theme
	sessions []sessiondb.SessionMeta
	selected int
}

func NewSessionPicker(th *theme.Theme, sessions []sessiondb.SessionMeta) SessionPicker {
	buttons := components.NewButtonGroup(th,
		components.Button{Label: "Resume", Key: "enter", Color: th.Colors.Success},
		components.Button{Label: "New Session", Key: "esc", Color: th.Colors.Muted, Cancel: true},
	)
	return SessionPicker{
		dialog:   components.NewDialog(th, "resume session", "⟳", buttons),
		th:       th,
		sessions: sessions,
		selected: 0,
	}
}

func (m SessionPicker) Init() tea.Cmd {
	return nil
}

func (m SessionPicker) Update(msg tea.Msg) (SessionPicker, string, bool) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			if m.selected > 0 {
				m.selected--
			}
			return m, "", false
		case "down", "j":
			if m.selected < len(m.sessions)-1 {
				m.selected++
			}
			return m, "", false
		case "enter":
			if len(m.sessions) > 0 && m.selected < len(m.sessions) {
				return m, m.sessions[m.selected].ID, true
			}
			return m, "", true
		case "esc":
			return m, "", true
		}
	}

	updated, _, done := m.dialog.Update(msg)
	m.dialog = updated
	if done {
		if m.selected < len(m.sessions) {
			return m, m.sessions[m.selected].ID, true
		}
		return m, "", true
	}
	return m, "", false
}

func (m SessionPicker) View() string {
	if len(m.sessions) == 0 {
		m.dialog.SetBody("No sessions found.\n\nPress Esc to start a new session.")
		return m.dialog.View()
	}

	var b strings.Builder
	for i, s := range m.sessions {
		cursor := "  "
		if i == m.selected {
			cursor = "> "
		}
		firstPrompt := ""
		if s.FirstPrompt.Valid {
			firstPrompt = s.FirstPrompt.String
			if len(firstPrompt) > 50 {
				firstPrompt = firstPrompt[:50]
			}
		}

		line := fmt.Sprintf("%s%-15s  %-50s  %d turns · %d msgs · %s",
			cursor, s.ProjectName, firstPrompt, s.TurnCount, s.MessageCount, s.Model)
		if i == m.selected {
			line = m.th.Styles.Highlight.Render(line)
		}
		b.WriteString(line + "\n")
	}

	help := "\n[↑↓ select]  [enter resume]  [esc new session]"
	m.dialog.SetBody(b.String() + help)
	return m.dialog.View()
}
