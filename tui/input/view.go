package input

import (
	"fmt"
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
		dim := m.theme.Styles.DimLabel
		highlight := m.theme.Styles.Highlight
		accent := m.theme.Styles.InputPrompt

		end := m.slashOffset + slashPageSize
		if end > len(m.slashFiltered) {
			end = len(m.slashFiltered)
		}
		aboveCount := m.slashOffset
		belowCount := len(m.slashFiltered) - end

		var menuLines []string
		if aboveCount > 0 {
			menuLines = append(menuLines, dim.Render(fmt.Sprintf("  ... %d above", aboveCount)))
		}
		for i := m.slashOffset; i < end; i++ {
			cmd := m.slashFiltered[i]
			marker := ""
			if i == m.slashIdx {
				marker = accent.Render("❯")
			}
			label := fmt.Sprintf("%-16s", cmd.Label)
			if i == m.slashIdx {
				label = highlight.Render(label)
			} else {
				label = dim.Render(label)
			}
			menuLines = append(menuLines, " "+marker+" "+label+" "+dim.Render(cmd.Desc))
		}
		if belowCount > 0 {
			menuLines = append(menuLines, dim.Render(fmt.Sprintf("  ... %d below", belowCount)))
		}

		b.WriteByte('\n')
		b.WriteString(strings.Join(menuLines, "\n"))
	}

	return b.String()
}
