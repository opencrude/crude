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
		b.WriteString(lipgloss.NewStyle().Foreground(colorAccent).Render("provider select"))
		b.WriteString("\n")
		for i, preset := range p.providerPresets {
			indicator := "  "
			if i == p.providerListIdx {
				indicator = lipgloss.NewStyle().Foreground(colorAccent).Render("❯ ")
			}
			name := lipgloss.NewStyle().Foreground(colorMainText).Render(fmt.Sprintf("%-16s", preset.Name))
			help := dim.Render(preset.HelpText)
			b.WriteString(indicator + name + " " + help + "\n")
		}
		b.WriteString("\n" + dim.Render("arrows navigate / enter select / esc or backspace cancel") + "\n")
		return b.String()
	}

	if p.isModelSelect {
		const pageSize = 12
		accentStyle := lipgloss.NewStyle().Foreground(colorAccent)
		errorStyle := lipgloss.NewStyle().Foreground(colorError)
		highlightStyle := lipgloss.NewStyle().Foreground(colorMainText).Background(lipgloss.Color("#1e2a3a"))

		// ── Header line ──────────────────────────────────────────
		b.WriteString(accentStyle.Bold(true).Render("  model select"))
		if p.fetchingModels {
			b.WriteString(dim.Render("  fetching models..."))
		} else if len(p.availableModels) > 0 {
			b.WriteString(dim.Render(fmt.Sprintf("  %d/%d", len(p.filteredModels), len(p.availableModels))))
		}
		b.WriteString("\n")

		// ── Search bar ───────────────────────────────────────────
		if p.fetchError != "" {
			b.WriteString(errorStyle.Render("  ✗ "+p.fetchError) + "\n")
		} else {
			searchPrompt := accentStyle.Render("  / ")
			searchText := lipgloss.NewStyle().Foreground(colorMainText).Render(p.modelSearch)
			cursor := blockCursor(spinFrame, colorAccent)
			b.WriteString(searchPrompt + searchText + cursor + "\n")
		}

		b.WriteString(dim.Render("  "+strings.Repeat("─", 54)) + "\n")

		// ── Model list with scrolling ─────────────────────────────
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
				if len([]rune(idStr)) > 44 {
					idStr = string([]rune(idStr)[:41]) + "…"
				}

				var line string
				if selected {
					line = accentStyle.Render(" ❯ ") + highlightStyle.Render(fmt.Sprintf("%-44s", idStr))
				} else {
					line = dim.Render("   ") + lipgloss.NewStyle().Foreground(colorFg).Render(fmt.Sprintf("%-44s", idStr))
				}
				b.WriteString(line + "\n")
			}
		}

		b.WriteString(dim.Render("  "+strings.Repeat("─", 54)) + "\n")
		footer := "  ↑↓ navigate · enter select · esc cancel"
		if !p.fetchingModels && len(p.filteredModels) > 0 {
			end := p.modelListOffset + pageSize
			if end > len(p.filteredModels) {
				end = len(p.filteredModels)
			}
			if p.modelListOffset > 0 || end < len(p.filteredModels) {
				scrollHint := fmt.Sprintf("  %d–%d of %d", p.modelListOffset+1, end, len(p.filteredModels))
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

	if p.isEmbedModelSelect {
		const pageSize = 12
		accentStyle := lipgloss.NewStyle().Foreground(colorAccent)
		errorStyle := lipgloss.NewStyle().Foreground(colorError)
		highlightStyle := lipgloss.NewStyle().Foreground(colorMainText).Background(lipgloss.Color("#1e2a3a"))

		b.WriteString(accentStyle.Bold(true).Render("  embed model select"))
		if p.fetchingEmbedModels {
			b.WriteString(dim.Render("  fetching models..."))
		} else if len(p.availableEmbedModels) > 0 {
			b.WriteString(dim.Render(fmt.Sprintf("  %d/%d", len(p.filteredEmbedModels), len(p.availableEmbedModels))))
		}
		b.WriteString("\n")

		if p.fetchEmbedError != "" {
			b.WriteString(errorStyle.Render("  ✗ "+p.fetchEmbedError) + "\n")
		} else {
			searchPrompt := accentStyle.Render("  / ")
			searchText := lipgloss.NewStyle().Foreground(colorMainText).Render(p.embedModelSearch)
			cursor := blockCursor(spinFrame, colorAccent)
			b.WriteString(searchPrompt + searchText + cursor + "\n")
		}

		b.WriteString(dim.Render("  "+strings.Repeat("─", 54)) + "\n")

		if p.fetchingEmbedModels {
			b.WriteString(dim.Render("  waiting for models...") + "\n")
		} else if len(p.filteredEmbedModels) == 0 {
			if p.embedModelSearch != "" {
				b.WriteString(dim.Render("  no matches — press enter to use typed value") + "\n")
			} else {
				b.WriteString(dim.Render("  no models available") + "\n")
			}
		} else {
			end := p.embedModelListOffset + pageSize
			if end > len(p.filteredEmbedModels) {
				end = len(p.filteredEmbedModels)
			}
			for i := p.embedModelListOffset; i < end; i++ {
				m := p.filteredEmbedModels[i]
				selected := i == p.embedModelListIdx

				idStr := m.ID
				if len([]rune(idStr)) > 44 {
					idStr = string([]rune(idStr)[:41]) + "…"
				}

				var line string
				if selected {
					line = accentStyle.Render(" ❯ ") + highlightStyle.Render(fmt.Sprintf("%-44s", idStr))
				} else {
					line = dim.Render("   ") + lipgloss.NewStyle().Foreground(colorFg).Render(fmt.Sprintf("%-44s", idStr))
				}
				b.WriteString(line + "\n")
			}
		}

		b.WriteString(dim.Render("  "+strings.Repeat("─", 54)) + "\n")
		footer := "  ↑↓ navigate · enter select · esc cancel"
		if !p.fetchingEmbedModels && len(p.filteredEmbedModels) > 0 {
			end := p.embedModelListOffset + pageSize
			if end > len(p.filteredEmbedModels) {
				end = len(p.filteredEmbedModels)
			}
			if p.embedModelListOffset > 0 || end < len(p.filteredEmbedModels) {
				scrollHint := fmt.Sprintf("  %d–%d of %d", p.embedModelListOffset+1, end, len(p.filteredEmbedModels))
				if p.embedModelListOffset > 0 {
					scrollHint += " · ↑pgup"
				}
				if end < len(p.filteredEmbedModels) {
					scrollHint += " · ↓pgdn"
				}
				footer = scrollHint + " · enter select · esc cancel"
			}
		}
		b.WriteString(dim.Render(footer) + "\n")
		if p.fetchingEmbedModels && p.embedModelSearch != "" {
			b.WriteString(dim.Render("  enter will use: ") + lipgloss.NewStyle().Foreground(colorMainText).Render(p.embedModelSearch) + "\n")
		}
		return b.String()
	}

	b.WriteString(lipgloss.NewStyle().Foreground(colorAccent).Bold(true).Render("settings") + "\n")

	for idx := 0; idx < sfCount; idx++ {
		isSSH := idx >= sfSSHHost
		if isSSH && p.backend != "ssh" {
			continue
		}
		if idx == sfSSHHost {
			b.WriteString("\n" + dim.Render("  ssh") + "\n")
		}

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
		if idx == sfBackend {
			var localStr, sshStr string
			if p.backend == "local" {
				localStr = lipgloss.NewStyle().Foreground(colorMainText).Render("local")
				sshStr = dim.Render("ssh")
			} else {
				localStr = dim.Render("local")
				sshStr = lipgloss.NewStyle().Foreground(colorMainText).Render("ssh")
			}
			valueStyled = localStr + dim.Render(" / ") + sshStr
		} else {
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
		b.WriteString(indicator + labelStyled + " " + valueStyled + "\n")
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
