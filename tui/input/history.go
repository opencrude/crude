package input

import "strings"

func (m *Model) browseHistoryUp() {
	if len(m.history) == 0 {
		return
	}
	if m.histIdx == -1 {
		m.histDraft = m.input.Value()
		if strings.Contains(m.histDraft, "\n") {
			m.histPrefix = ""
		} else {
			m.histPrefix = strings.TrimSpace(m.histDraft)
		}
	}
	idx := m.findHistoryIndex(m.histIdx)
	if idx < 0 {
		return
	}
	m.histIdx = idx
	m.input.SetValue(m.history[m.histIdx])
}

func (m *Model) browseHistoryDown() {
	if m.histIdx < 0 {
		return
	}
	next := m.findHistoryDown(m.histIdx)
	if next < 0 {
		m.resetHistory()
		return
	}
	m.histIdx = next
	m.input.SetValue(m.history[m.histIdx])
}

func (m *Model) findHistoryIndex(startIdx int) int {
	start := len(m.history) - 1
	if startIdx >= 0 && startIdx < len(m.history) {
		start = startIdx - 1
	}
	for i := start; i >= 0; i-- {
		if m.histPrefix == "" || strings.HasPrefix(m.history[i], m.histPrefix) {
			return i
		}
	}
	return -1
}

func (m *Model) findHistoryDown(startIdx int) int {
	for i := startIdx + 1; i < len(m.history); i++ {
		if m.histPrefix == "" || strings.HasPrefix(m.history[i], m.histPrefix) {
			return i
		}
	}
	return -1
}

func (m *Model) resetHistory() {
	if m.histIdx >= 0 && m.histIdx < len(m.history) {
		m.input.SetValue(m.histDraft)
	}
	m.histDraft = ""
	m.histPrefix = ""
	m.histIdx = -1
}