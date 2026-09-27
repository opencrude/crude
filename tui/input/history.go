package input

import (
	"strings"

	"crude/history"
)

type HistoryRecallEvent struct {
	Entry      history.Entry
	Generation uint64
}

func (HistoryRecallEvent) event() {}

func (m *Model) RecallPrompt(entry history.Entry) HistoryRecallEvent {
	m.historyGeneration++
	m.historyLoading = true
	return HistoryRecallEvent{Entry: entry.Clone(), Generation: m.historyGeneration}
}

func (m Model) HistoryRecallCurrent(generation uint64) bool {
	return m.historyLoading && generation == m.historyGeneration
}

func (m *Model) FinishHistoryRecall(generation uint64, text string, referenceIDs []string, success bool) bool {
	if !m.HistoryRecallCurrent(generation) {
		return false
	}
	m.historyLoading = false
	if success {
		m.UseReferenceIDs(referenceIDs)
		m.SetValuePreserveReferences(text)
	}
	return true
}

func (m *Model) browseHistoryUp() Event {
	if len(m.history) == 0 {
		return nil
	}
	if m.histIdx == -1 {
		m.histDraft = m.input.Value()
		m.histDraftReferenceIDs = m.ReferencedIDs()
		if strings.Contains(m.histDraft, "\n") {
			m.histPrefix = ""
		} else {
			m.histPrefix = strings.TrimSpace(m.histDraft)
		}
	}
	idx := m.findHistoryIndex(m.histIdx)
	if idx < 0 {
		return nil
	}
	m.histIdx = idx
	return m.RecallPrompt(m.history[m.histIdx])
}

func (m *Model) browseHistoryDown() Event {
	if m.histIdx < 0 {
		return nil
	}
	next := m.findHistoryDown(m.histIdx)
	if next < 0 {
		m.resetHistory()
		return nil
	}
	m.histIdx = next
	return m.RecallPrompt(m.history[m.histIdx])
}

func (m *Model) findHistoryIndex(startIdx int) int {
	start := len(m.history) - 1
	if startIdx >= 0 && startIdx < len(m.history) {
		start = startIdx - 1
	}
	for i := start; i >= 0; i-- {
		if m.histPrefix == "" || strings.HasPrefix(strings.ToLower(m.history[i].Text), strings.ToLower(m.histPrefix)) {
			return i
		}
	}
	return -1
}

func (m *Model) findHistoryDown(startIdx int) int {
	for i := startIdx + 1; i < len(m.history); i++ {
		if m.histPrefix == "" || strings.HasPrefix(strings.ToLower(m.history[i].Text), strings.ToLower(m.histPrefix)) {
			return i
		}
	}
	return -1
}

func (m *Model) resetHistory() {
	if m.histIdx >= 0 && m.histIdx < len(m.history) {
		m.SetValuePreserveReferences(m.histDraft)
		m.UseReferenceIDs(m.histDraftReferenceIDs)
	}
	m.histDraft = ""
	m.histPrefix = ""
	m.histIdx = -1
}
