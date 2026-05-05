package tui

import "strings"

const maxHistorySize = 500

func (m *Model) browseHistoryUp() {
	if len(m.history) == 0 {
		return
	}
	if m.historyIdx == -1 {
		m.historyDraft = m.inputText()
		if strings.Contains(m.historyDraft, "\n") {
			m.historyPrefix = ""
		} else {
			m.historyPrefix = strings.TrimSpace(m.historyDraft)
		}
		m.historyIdx = m.findHistoryIndex(-1)
		if m.historyIdx == -1 {
			return
		}
	} else {
		m.historyIdx = m.findHistoryIndex(m.historyIdx)
		if m.historyIdx == -1 {
			return
		}
	}
	m.setInput(m.history[m.historyIdx])
}

// browseHistoryDown navigates to newer prompts.
// When reaching the end, restores the original draft input.
func (m *Model) browseHistoryDown() {
	if m.historyIdx == -1 {
		return
	}
	next := m.findHistoryDown(m.historyIdx)
	if next == -1 {
		// No more entries — restore draft
		m.resetHistory()
		return
	}
	m.historyIdx = next
	m.setInput(m.history[m.historyIdx])
}

// findHistoryIndex returns the index of the next older history entry
// matching the current prefix. startIdx: -1 means start from newest,
// otherwise search older than startIdx.
func (m *Model) findHistoryIndex(startIdx int) int {
	start := len(m.history) - 1
	if startIdx != -1 {
		start = startIdx - 1
	}
	for i := start; i >= 0; i-- {
		if m.historyPrefix == "" || strings.HasPrefix(m.history[i], m.historyPrefix) {
			return i
		}
	}
	return -1
}

// findHistoryDown returns the index of the next newer history entry
// matching the current prefix. Returns -1 if no more entries (should restore draft).
func (m *Model) findHistoryDown(startIdx int) int {
	for i := startIdx + 1; i < len(m.history); i++ {
		if m.historyPrefix == "" || strings.HasPrefix(m.history[i], m.historyPrefix) {
			return i
		}
	}
	return -1
}

// resetHistory exits history browsing and restores the draft input.
func (m *Model) resetHistory() {
	if m.historyIdx != -1 {
		m.setInput(m.historyDraft)
		m.historyDraft = ""
		m.historyPrefix = ""
		m.historyIdx = -1
	}
}

// appendHistory adds a prompt to history, deduplicating consecutive entries
// and trimming to max size.
func (m *Model) appendHistory(text string) {
	if text == "" {
		return
	}
	// Don't add duplicates of the most recent entry
	if len(m.history) > 0 && m.history[len(m.history)-1] == text {
		return
	}
	m.history = append(m.history, text)
	// Trim oldest entries if over capacity
	if len(m.history) > maxHistorySize {
		m.history = m.history[len(m.history)-maxHistorySize:]
	}
}
