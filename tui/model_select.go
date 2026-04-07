package tui

import (
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
)

func (p inlineSettings) updateModelSelect(msg tea.KeyMsg) (inlineSettings, tea.Cmd, bool, bool) {
	const pageSize = 12
	switch msg.Type {
	case tea.KeyEsc:
		p.isModelSelect = false
		p.modelSearch = ""
		return p, nil, false, false
	case tea.KeyEnter:
		if !p.fetchingModels && len(p.filteredModels) > 0 && p.modelListIdx >= 0 && p.modelListIdx < len(p.filteredModels) {
			p.fields[sfModel].value = []rune(p.filteredModels[p.modelListIdx].ID)
		} else if p.modelSearch != "" {
			p.fields[sfModel].value = []rune(p.modelSearch)
		}
		p.isModelSelect = false
		p.modelSearch = ""
		return p, nil, false, false
	case tea.KeyUp:
		if len(p.filteredModels) > 0 {
			p.modelListIdx--
			if p.modelListIdx < 0 {
				p.modelListIdx = len(p.filteredModels) - 1
			}
			p.clampModelOffset(pageSize)
		}
		return p, nil, false, false
	case tea.KeyDown:
		if len(p.filteredModels) > 0 {
			p.modelListIdx++
			if p.modelListIdx >= len(p.filteredModels) {
				p.modelListIdx = 0
			}
			p.clampModelOffset(pageSize)
		}
		return p, nil, false, false
	case tea.KeyPgUp:
		p.modelListIdx -= pageSize
		if p.modelListIdx < 0 {
			p.modelListIdx = 0
		}
		p.clampModelOffset(pageSize)
		return p, nil, false, false
	case tea.KeyPgDown:
		p.modelListIdx += pageSize
		if p.modelListIdx >= len(p.filteredModels) {
			p.modelListIdx = len(p.filteredModels) - 1
		}
		p.clampModelOffset(pageSize)
		return p, nil, false, false
	case tea.KeyBackspace, tea.KeyDelete:
		if len(p.modelSearch) > 0 {
			runes := []rune(p.modelSearch)
			p.modelSearch = string(runes[:len(runes)-1])
			p.updateModelFilter()
		}
		return p, nil, false, false
	default:
		for _, r := range msg.Runes {
			if unicode.IsPrint(r) {
				p.modelSearch += string(r)
				p.updateModelFilter()
			}
		}
	}
	return p, nil, false, false
}

func (p *inlineSettings) updateModelFilter() {
	p.filteredModels = nil
	p.modelListIdx = 0
	p.modelListOffset = 0
	s := strings.ToLower(p.modelSearch)
	for _, m := range p.availableModels {
		if strings.Contains(strings.ToLower(m.ID), s) || strings.Contains(strings.ToLower(m.Name), s) {
			p.filteredModels = append(p.filteredModels, m)
		}
	}
}

func (p *inlineSettings) clampModelOffset(pageSize int) {
	if p.modelListIdx < p.modelListOffset {
		p.modelListOffset = p.modelListIdx
	}
	if p.modelListIdx >= p.modelListOffset+pageSize {
		p.modelListOffset = p.modelListIdx - pageSize + 1
	}
	if p.modelListOffset < 0 {
		p.modelListOffset = 0
	}
}

func (p inlineSettings) updateEmbedModelSelect(msg tea.KeyMsg) (inlineSettings, tea.Cmd, bool, bool) {
	const pageSize = 12
	switch msg.Type {
	case tea.KeyEsc:
		p.isEmbedModelSelect = false
		p.embedModelSearch = ""
		return p, nil, false, false
	case tea.KeyEnter:
		if !p.fetchingEmbedModels && len(p.filteredEmbedModels) > 0 && p.embedModelListIdx >= 0 && p.embedModelListIdx < len(p.filteredEmbedModels) {
			p.fields[sfEmbedModel].value = []rune(p.filteredEmbedModels[p.embedModelListIdx].ID)
		} else if p.embedModelSearch != "" {
			p.fields[sfEmbedModel].value = []rune(p.embedModelSearch)
		}
		p.isEmbedModelSelect = false
		p.embedModelSearch = ""
		return p, nil, false, false
	case tea.KeyUp:
		if len(p.filteredEmbedModels) > 0 {
			p.embedModelListIdx--
			if p.embedModelListIdx < 0 {
				p.embedModelListIdx = len(p.filteredEmbedModels) - 1
			}
			p.clampEmbedModelOffset(pageSize)
		}
		return p, nil, false, false
	case tea.KeyDown:
		if len(p.filteredEmbedModels) > 0 {
			p.embedModelListIdx++
			if p.embedModelListIdx >= len(p.filteredEmbedModels) {
				p.embedModelListIdx = 0
			}
			p.clampEmbedModelOffset(pageSize)
		}
		return p, nil, false, false
	case tea.KeyPgUp:
		p.embedModelListIdx -= pageSize
		if p.embedModelListIdx < 0 {
			p.embedModelListIdx = 0
		}
		p.clampEmbedModelOffset(pageSize)
		return p, nil, false, false
	case tea.KeyPgDown:
		p.embedModelListIdx += pageSize
		if p.embedModelListIdx >= len(p.filteredEmbedModels) {
			p.embedModelListIdx = len(p.filteredEmbedModels) - 1
		}
		p.clampEmbedModelOffset(pageSize)
		return p, nil, false, false
	case tea.KeyBackspace, tea.KeyDelete:
		if len(p.embedModelSearch) > 0 {
			runes := []rune(p.embedModelSearch)
			p.embedModelSearch = string(runes[:len(runes)-1])
			p.updateEmbedModelFilter()
		}
		return p, nil, false, false
	default:
		for _, r := range msg.Runes {
			if unicode.IsPrint(r) {
				p.embedModelSearch += string(r)
				p.updateEmbedModelFilter()
			}
		}
	}
	return p, nil, false, false
}

func (p *inlineSettings) updateEmbedModelFilter() {
	p.filteredEmbedModels = nil
	p.embedModelListIdx = 0
	p.embedModelListOffset = 0
	s := strings.ToLower(p.embedModelSearch)
	for _, m := range p.availableEmbedModels {
		if strings.Contains(strings.ToLower(m.ID), s) || strings.Contains(strings.ToLower(m.Name), s) {
			p.filteredEmbedModels = append(p.filteredEmbedModels, m)
		}
	}
}

func (p *inlineSettings) clampEmbedModelOffset(pageSize int) {
	if p.embedModelListIdx < p.embedModelListOffset {
		p.embedModelListOffset = p.embedModelListIdx
	}
	if p.embedModelListIdx >= p.embedModelListOffset+pageSize {
		p.embedModelListOffset = p.embedModelListIdx - pageSize + 1
	}
	if p.embedModelListOffset < 0 {
		p.embedModelListOffset = 0
	}
}
