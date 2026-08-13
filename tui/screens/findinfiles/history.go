package findinfiles

import (
	"strings"

	"crude/tui/ui"
)

// queryHistoryLimit caps how many submitted queries the session remembers.
const queryHistoryLimit = 20

// queryHistoryStore keeps submitted queries most-recent-first.
type queryHistoryStore struct {
	entries []string
}

// Add records a submitted query: trimmed, deduplicated (move-to-front), and
// capped at queryHistoryLimit. Empty queries are ignored.
func (h *queryHistoryStore) Add(query string) {
	q := strings.TrimSpace(query)
	if q == "" {
		return
	}
	for i, e := range h.entries {
		if e == q {
			h.entries = append(h.entries[:i], h.entries[i+1:]...)
			break
		}
	}
	h.entries = append([]string{q}, h.entries...)
	if len(h.entries) > queryHistoryLimit {
		h.entries = h.entries[:queryHistoryLimit]
	}
}

// Len returns the number of remembered queries.
func (h *queryHistoryStore) Len() int { return len(h.entries) }

// At returns the i-th most recent query (0 = newest), or "".
func (h *queryHistoryStore) At(i int) string {
	if i < 0 || i >= len(h.entries) {
		return ""
	}
	return h.entries[i]
}

// queryHistory is process-wide state: the Find in Files screen is recreated
// on every open, and submitted queries must survive across those instances
// for the lifetime of the session.
var queryHistory queryHistoryStore

// historyStep moves through the submitted-query history while the query
// field is focused. delta +1 steps to older entries, -1 back toward the
// draft the user was typing before browsing started (index -1).
func (s *Screen) historyStep(delta int) ui.EventResult {
	n := queryHistory.Len()
	if n == 0 {
		return ui.EventResult{Handled: true}
	}
	if s.histIdx < 0 {
		s.histDraft = s.Query()
	}
	next := s.histIdx + delta
	if next >= n {
		next = n - 1
	}
	if next < -1 {
		next = -1
	}
	if next == s.histIdx {
		return ui.EventResult{Handled: true}
	}
	s.histIdx = next
	value := s.histDraft
	if next >= 0 {
		value = queryHistory.At(next)
	}
	s.queryField.field.SetValue(value)
	cmd := combineFindCmd(s.queryChanged(), s.queryField.field.Schedule())
	return ui.EventResult{Handled: true, Cmd: cmd, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
}

// HistoryBrowsing reports whether the query field is showing a history entry.
func (s *Screen) HistoryBrowsing() bool { return s.histIdx >= 0 }
