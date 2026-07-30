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

	// ANSI bridge for tests/layout callers that still measure via View.
	// Product paint uses PaintShell into the Host surface.
	_ = chatAreaHeight
	b.WriteString(m.input.View(width, spinFrame, m.thinking))

	if m.showSlashPopup() {
		for i, item := range m.slashList.Items() {
			if i >= slashPageSize {
				break
			}
			marker := "  "
			if i == m.slashList.SelectedIndex() {
				marker = "❯ "
			}
			line := marker + item.Label
			if item.Detail != "" {
				line += "  " + item.Detail
			}
			b.WriteByte('\n')
			b.WriteString(line)
		}
	}

	return b.String()
}
