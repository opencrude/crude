package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// View returns the inline settings string for the chat area
func (p *inlineSettings) View(spinFrame int) string {
	dim := lipgloss.NewStyle().Foreground(colorMuted)
	var b strings.Builder

	if p.isProviderSelect {
		b.WriteString("\n" + lipgloss.NewStyle().Foreground(colorAccent).Render("provider select") + "\n")
		for i, preset := range p.providerPresets {
			indicator := "  "
			if i == p.providerListIdx {
				indicator = lipgloss.NewStyle().Foreground(colorAccent).Render("❯ ")
			}
			name := lipgloss.NewStyle().Foreground(colorMainText).Render(fmt.Sprintf("%-16s", preset.Name))
			help := dim.Render(preset.HelpText)
			b.WriteString(indicator + name + " " + help + "\n")
		}
		return b.String()
	}

	if p.isModelSelect {
		const pageSize = 12
		accentStyle := lipgloss.NewStyle().Foreground(colorAccent)
		errorStyle := lipgloss.NewStyle().Foreground(colorError)
		highlightStyle := lipgloss.NewStyle().Foreground(colorMainText).Background(lipgloss.Color("#1e2a3a"))

		// Header line
		b.WriteString("\n" + accentStyle.Bold(true).Render("model select"))
		if p.fetchingModels {
			b.WriteString(dim.Render("  fetching models..."))
		} else if len(p.availableModels) > 0 {
			b.WriteString(dim.Render(fmt.Sprintf("  %d/%d", len(p.filteredModels), len(p.availableModels))))
		}
		b.WriteString("\n")

		// Search bar
		if p.fetchError != "" {
			b.WriteString(errorStyle.Render("  ✗ "+p.fetchError) + "\n")
		} else {
			searchPrompt := accentStyle.Render("/ ")
			searchText := lipgloss.NewStyle().Foreground(colorMainText).Render(p.modelSearch)
			cursor := blockCursor(spinFrame, colorAccent)
			b.WriteString(searchPrompt + searchText + cursor + "\n")
		}

		b.WriteString(dim.Render(strings.Repeat("─", 54)) + "\n")

		// Model list
		if p.fetchingModels {
			b.WriteString(dim.Render("  waiting for models...") + "\n")
		} else if len(p.filteredModels) == 0 {
			if p.modelSearch != "" {
				b.WriteString(dim.Render("  no matches — press enter to use typed value") + "\n")
			} else {
				b.WriteString(dim.Render("  no models available") + "\n")
			}
		} else {
			end := p.modelListOffset + pageSize
			if end > len(p.filteredModels) {
				end = len(p.filteredModels)
			}
			for i := p.modelListOffset; i < end; i++ {
				m := p.filteredModels[i]
				selected := i == p.modelListIdx

				idStr := m.ID
				if len([]rune(idStr)) > 36 {
					idStr = string([]rune(idStr)[:33]) + "…"
				}

				ctxStr := ""
				if m.ContextWindow > 0 {
					ctxStr = dim.Render(fmt.Sprintf(" %s", formatTokens(m.ContextWindow)))
				}

				var line string
				if selected {
					line = accentStyle.Render("❯ ") + highlightStyle.Render(fmt.Sprintf("%-36s", idStr)) + ctxStr
				} else {
					line = dim.Render("  ") + lipgloss.NewStyle().Foreground(colorFg).Render(fmt.Sprintf("%-36s", idStr)) + ctxStr
				}
				b.WriteString(line + "\n")
			}
		}

		b.WriteString(dim.Render(strings.Repeat("─", 54)) + "\n")
		footer := "↑↓ navigate · enter select · esc cancel"
		if !p.fetchingModels && len(p.filteredModels) > 0 {
			end := p.modelListOffset + pageSize
			if end > len(p.filteredModels) {
				end = len(p.filteredModels)
			}
			if p.modelListOffset > 0 || end < len(p.filteredModels) {
				scrollHint := fmt.Sprintf("%d–%d of %d", p.modelListOffset+1, end, len(p.filteredModels))
				if p.modelListOffset > 0 {
					scrollHint += " · ↑pgup"
				}
				if end < len(p.filteredModels) {
					scrollHint += " · ↓pgdn"
				}
				footer = scrollHint + " · enter select · esc cancel"
			}
		}
		b.WriteString(dim.Render(footer) + "\n")
		if p.fetchingModels && p.modelSearch != "" {
			b.WriteString(dim.Render("  enter will use: ") + lipgloss.NewStyle().Foreground(colorMainText).Render(p.modelSearch) + "\n")
		}
		return b.String()
	}

	if p.isOracleModelSelect {
		const pageSize = 12
		accentStyle := lipgloss.NewStyle().Foreground(colorAccent)
		errorStyle := lipgloss.NewStyle().Foreground(colorError)
		highlightStyle := lipgloss.NewStyle().Foreground(colorMainText).Background(lipgloss.Color("#1e2a3a"))

		b.WriteString(accentStyle.Bold(true).Render("  oracle model select"))
		if p.fetchingOracleModels {
			b.WriteString(dim.Render("  fetching models..."))
		} else if len(p.availableOracleModels) > 0 {
			b.WriteString(dim.Render(fmt.Sprintf("  %d/%d", len(p.filteredOracleModels), len(p.availableOracleModels))))
		}
		b.WriteString("\n")

		if p.fetchOracleError != "" {
			b.WriteString(errorStyle.Render("  ✗ "+p.fetchOracleError) + "\n")
		} else {
			searchPrompt := accentStyle.Render("/ ")
			searchText := lipgloss.NewStyle().Foreground(colorMainText).Render(p.oracleModelSearch)
			cursor := blockCursor(spinFrame, colorAccent)
			b.WriteString(searchPrompt + searchText + cursor + "\n")
		}

		b.WriteString(dim.Render(strings.Repeat("─", 54)) + "\n")

		if p.fetchingOracleModels {
			b.WriteString(dim.Render("  waiting for models...") + "\n")
		} else if len(p.filteredOracleModels) == 0 {
			if p.oracleModelSearch != "" {
				b.WriteString(dim.Render("  no matches — press enter to use typed value") + "\n")
			} else {
				b.WriteString(dim.Render("  no models available") + "\n")
			}
		} else {
			end := p.oracleModelListOffset + pageSize
			if end > len(p.filteredOracleModels) {
				end = len(p.filteredOracleModels)
			}
			for i := p.oracleModelListOffset; i < end; i++ {
				m := p.filteredOracleModels[i]
				selected := i == p.oracleModelListIdx

				idStr := m.ID
				if len([]rune(idStr)) > 36 {
					idStr = string([]rune(idStr)[:33]) + "…"
				}

				ctxStr := ""
				if m.ContextWindow > 0 {
					ctxStr = dim.Render(fmt.Sprintf(" %s", formatTokens(m.ContextWindow)))
				}

				var line string
				if selected {
					line = accentStyle.Render("❯ ") + highlightStyle.Render(fmt.Sprintf("%-36s", idStr)) + ctxStr
				} else {
					line = dim.Render("  ") + lipgloss.NewStyle().Foreground(colorFg).Render(fmt.Sprintf("%-36s", idStr)) + ctxStr
				}
				b.WriteString(line + "\n")
			}
		}

		b.WriteString(dim.Render(strings.Repeat("─", 54)) + "\n")
		footer := "↑↓ navigate · enter select · esc cancel"
		if !p.fetchingOracleModels && len(p.filteredOracleModels) > 0 {
			end := p.oracleModelListOffset + pageSize
			if end > len(p.filteredOracleModels) {
				end = len(p.filteredOracleModels)
			}
			if p.oracleModelListOffset > 0 || end < len(p.filteredOracleModels) {
				scrollHint := fmt.Sprintf("%d–%d of %d", p.oracleModelListOffset+1, end, len(p.filteredOracleModels))
				if p.oracleModelListOffset > 0 {
					scrollHint += " · ↑pgup"
				}
				if end < len(p.filteredOracleModels) {
					scrollHint += " · ↓pgdn"
				}
				footer = scrollHint + " · enter select · esc cancel"
			}
		}
		b.WriteString(dim.Render(footer) + "\n")
		if p.fetchingOracleModels && p.oracleModelSearch != "" {
			b.WriteString(dim.Render("  enter will use: ") + lipgloss.NewStyle().Foreground(colorMainText).Render(p.oracleModelSearch) + "\n")
		}
		return b.String()
	}

	b.WriteString("\n" + lipgloss.NewStyle().Foreground(colorAccent).Bold(true).Render("Settings") + "\n")

	for idx := 0; idx < sfCount; idx++ {
		f := p.fields[idx]
		focused := idx == p.focused && p.focusedBtn == 0

		label := fmt.Sprintf("%-12s", f.label)
		var labelStyled string
		if focused {
			labelStyled = lipgloss.NewStyle().Foreground(colorMainText).Render(label)
		} else {
			labelStyled = dim.Render(label)
		}

		var valueStyled string
		{
			var d string
			if f.masked && len(f.value) > 0 {
				d = strings.Repeat("*", len(f.value))
			} else {
				d = string(f.value)
			}
			if len(d) == 0 {
				d = "-"
			}
			if focused && p.editingField {
				runes := []rune(d)
				cur := p.editCursor
				if cur > len(runes) {
					cur = len(runes)
				}
				if cur < len(runes) {
					left := string(runes[:cur])
					underCursor := string(runes[cur : cur+1])
					right := string(runes[cur+1:])
					valueStyled = lipgloss.NewStyle().Foreground(colorMainText).Render(left) +
						lipgloss.NewStyle().Reverse(true).Render(underCursor) +
						lipgloss.NewStyle().Foreground(colorMainText).Render(right)
				} else {
					valueStyled = lipgloss.NewStyle().Foreground(colorMainText).Render(d) + blockCursor(spinFrame, colorAccent)
				}
			} else {
				valueStyled = lipgloss.NewStyle().Foreground(colorFg).Render(d)
			}
		}

		indicator := "  "
		if focused {
			indicator = lipgloss.NewStyle().Foreground(colorAccent).Render("❯ ")
		}
		if idx == sfThinkingEffort && focused {
			leftArrow := lipgloss.NewStyle().Foreground(colorMuted).Render("← ")
			rightArrow := lipgloss.NewStyle().Foreground(colorMuted).Render(" →")
			b.WriteString(indicator + labelStyled + " " + leftArrow + valueStyled + rightArrow + "\n")
		} else {
			b.WriteString(indicator + labelStyled + " " + valueStyled + "\n")
		}
	}

	b.WriteString("\n")
	if p.focusedBtn == 1 {
		saveStyle := lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
		b.WriteString(saveStyle.Render("❯ [ Save ]"))
	} else {
		saveStyle := lipgloss.NewStyle().Foreground(colorMuted)
		b.WriteString(saveStyle.Render("  [ Save ]"))
	}
	b.WriteString("   ")
	if p.focusedBtn == 2 {
		cancelStyle := lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
		b.WriteString(cancelStyle.Render("❯ [ Cancel ]"))
	} else {
		cancelStyle := lipgloss.NewStyle().Foreground(colorMuted)
		b.WriteString(cancelStyle.Render("  [ Cancel ]"))
	}
	b.WriteString("\n")

	return b.String()
}
