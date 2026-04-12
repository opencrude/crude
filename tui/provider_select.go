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
				// Provider changed — load saved provider config if it exists
				if p.getProviderConfigFn != nil {
					savedCfg := p.getProviderConfigFn(selected.ID)
					p.fields[sfAPIKey].value = []rune(savedCfg.APIKey)
					p.fields[sfModel].value = []rune(savedCfg.Model)
					p.fields[sfEmbedModel].value = []rune(savedCfg.EmbedModel)
					if savedCfg.ProviderURL != "" {
						p.fields[sfBaseURL].value = []rune(savedCfg.ProviderURL)
					} else {
						p.fields[sfBaseURL].value = []rune(selected.BaseURL)
					}
				} else {
					// No callback, just use preset defaults
					p.fields[sfBaseURL].value = []rune(selected.BaseURL)
					p.fields[sfModel].value = nil
					p.fields[sfEmbedModel].value = nil
				}
				// Clear model selection state since provider changed
				p.availableModels = nil
				p.filteredModels = nil
				p.fetchError = ""
				p.fetchingModels = false

				p.availableEmbedModels = nil
				p.filteredEmbedModels = nil
				p.fetchEmbedError = ""
				p.fetchingEmbedModels = false
			}
			p.providerID = selected.ID
			p.fields[sfProvider].value = []rune(selected.ID)
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
