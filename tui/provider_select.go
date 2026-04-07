package tui

import (
	tea "github.com/charmbracelet/bubbletea"
)

func (p inlineSettings) updateProviderSelect(msg tea.KeyMsg) (inlineSettings, tea.Cmd, bool, bool) {
	switch msg.Type {
	case tea.KeyEsc, tea.KeyBackspace:
		p.isProviderSelect = false
		return p, nil, false, false
	case tea.KeyEnter:
		if p.providerListIdx >= 0 && p.providerListIdx < len(p.providerPresets) {
			selected := p.providerPresets[p.providerListIdx]
			if selected.ID != p.providerID {
				// Provider changed — clear stale model data and reset model field
				p.availableModels = nil
				p.filteredModels = nil
				p.fetchError = ""
				p.fetchingModels = false
				p.fields[sfModel].value = nil

				p.availableEmbedModels = nil
				p.filteredEmbedModels = nil
				p.fetchEmbedError = ""
				p.fetchingEmbedModels = false
				p.fields[sfEmbedModel].value = nil
			}
			p.providerID = selected.ID
			p.fields[sfProvider].value = []rune(selected.ID)
			p.fields[sfBaseURL].value = []rune(selected.BaseURL)
		}
		p.isProviderSelect = false
		return p, nil, false, false
	case tea.KeyUp:
		p.providerListIdx--
		if p.providerListIdx < 0 {
			p.providerListIdx = len(p.providerPresets) - 1
		}
		return p, nil, false, false
	case tea.KeyDown:
		p.providerListIdx++
		if p.providerListIdx >= len(p.providerPresets) {
			p.providerListIdx = 0
		}
		return p, nil, false, false
	}
	return p, nil, false, false
}
