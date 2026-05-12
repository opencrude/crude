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

	if m.showHint && !m.thinking && m.input.Value() == "" {
		hint := "[ enter: send · alt+enter: new line · ↑↓: history · ctrl+s: settings · esc: exit ]"
		b.WriteString(m.theme.Styles.DimLabel.Render(hint))
		b.WriteByte('\n')
	}

	if m.thinking && !m.blocking {
		style := m.theme.Styles.DimLabel.Italic(true)
		b.WriteString(style.Render("▶ waiting for response..."))
		return b.String()
	}

	b.WriteString(m.input.View(width, spinFrame))

	inputVal := m.input.Value()
	showSlash := m.slashActive && !m.thinking &&
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
