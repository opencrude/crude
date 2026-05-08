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
			selected := p.filteredModels[p.modelListIdx]
			p.fields[sfModel].value = []rune(selected.ID)
			p.modelContextWindow = selected.ContextWindow
		} else if p.modelSearch != "" {
			p.fields[sfModel].value = []rune(p.modelSearch)
			p.modelContextWindow = 0
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

func (p inlineSettings) updateOracleModelSelect(msg tea.KeyMsg) (inlineSettings, tea.Cmd, bool, bool) {
	const pageSize = 12
	switch msg.Type {
	case tea.KeyEsc:
		p.isOracleModelSelect = false
		p.oracleModelSearch = ""
		return p, nil, false, false
	case tea.KeyEnter:
		if !p.fetchingOracleModels && len(p.filteredOracleModels) > 0 && p.oracleModelListIdx >= 0 && p.oracleModelListIdx < len(p.filteredOracleModels) {
			selected := p.filteredOracleModels[p.oracleModelListIdx]
			p.fields[sfOracleModel].value = []rune(selected.ID)
			p.oracleModelContextWindow = selected.ContextWindow
		} else if p.oracleModelSearch != "" {
			p.fields[sfOracleModel].value = []rune(p.oracleModelSearch)
			p.oracleModelContextWindow = 0
		}
		p.isOracleModelSelect = false
		p.oracleModelSearch = ""
		return p, nil, false, false
	case tea.KeyUp:
		if len(p.filteredOracleModels) > 0 {
			p.oracleModelListIdx--
			if p.oracleModelListIdx < 0 {
				p.oracleModelListIdx = len(p.filteredOracleModels) - 1
			}
			p.clampOracleModelOffset(pageSize)
		}
		return p, nil, false, false
	case tea.KeyDown:
		if len(p.filteredOracleModels) > 0 {
			p.oracleModelListIdx++
			if p.oracleModelListIdx >= len(p.filteredOracleModels) {
				p.oracleModelListIdx = 0
			}
			p.clampOracleModelOffset(pageSize)
		}
		return p, nil, false, false
	case tea.KeyPgUp:
		p.oracleModelListIdx -= pageSize
		if p.oracleModelListIdx < 0 {
			p.oracleModelListIdx = 0
		}
		p.clampOracleModelOffset(pageSize)
		return p, nil, false, false
	case tea.KeyPgDown:
		p.oracleModelListIdx += pageSize
		if p.oracleModelListIdx >= len(p.filteredOracleModels) {
			p.oracleModelListIdx = len(p.filteredOracleModels) - 1
		}
		p.clampOracleModelOffset(pageSize)
		return p, nil, false, false
	case tea.KeyBackspace, tea.KeyDelete:
		if len(p.oracleModelSearch) > 0 {
			runes := []rune(p.oracleModelSearch)
			p.oracleModelSearch = string(runes[:len(runes)-1])
			p.updateOracleModelFilter()
		}
		return p, nil, false, false
	default:
		for _, r := range msg.Runes {
			if unicode.IsPrint(r) {
				p.oracleModelSearch += string(r)
				p.updateOracleModelFilter()
			}
		}
	}
	return p, nil, false, false
}

func (p *inlineSettings) updateOracleModelFilter() {
	p.filteredOracleModels = nil
	p.oracleModelListIdx = 0
	p.oracleModelListOffset = 0
	s := strings.ToLower(p.oracleModelSearch)
	for _, m := range p.availableOracleModels {
		if strings.Contains(strings.ToLower(m.ID), s) || strings.Contains(strings.ToLower(m.Name), s) {
			p.filteredOracleModels = append(p.filteredOracleModels, m)
		}
	}
}

func (p *inlineSettings) clampOracleModelOffset(pageSize int) {
	if p.oracleModelListIdx < p.oracleModelListOffset {
		p.oracleModelListOffset = p.oracleModelListIdx
	}
	if p.oracleModelListIdx >= p.oracleModelListOffset+pageSize {
		p.oracleModelListOffset = p.oracleModelListIdx - pageSize + 1
	}
	if p.oracleModelListOffset < 0 {
		p.oracleModelListOffset = 0
	}
}


