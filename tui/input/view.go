package input

import (
	"strings"
)

func (m Model) View(width, chatAreaHeight int, settingsActive, confirmActive bool, spinFrame int) string {
	var b strings.Builder

	if settingsActive {
		return b.String()
	}
	if confirmActive {
		return b.String()
	}

	// Render via the legacy ANSI View() bridge. The TextArea owns the
	// engine; View returns the same ANSI string the legacy component did.
	b.WriteString(m.input.View(width, spinFrame, m.thinking))

	inputVal := m.input.Value()
	showSlash := m.slashActive &&
		strings.HasPrefix(inputVal, "/") && !strings.Contains(inputVal, " ") && !strings.Contains(inputVal, "\n")

	if showSlash {
		popupView := m.slashList.View(width)
		if popupView != "" {
			b.WriteByte('\n')
			b.WriteString(popupView)
		}
	}

	return b.String()
}
