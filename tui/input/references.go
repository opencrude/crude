package input

import (
	"sort"
	"strings"

	"crude/sessiondb"
	"crude/tui/components"
)

type ReferenceStore interface {
	CreateReference(sessiondb.CreateRequest) (sessiondb.Reference, error)
	UpdateReferenceContent(id, content string) error
	DeleteReference(id string) error
	GetReference(id string) (sessiondb.Reference, bool)
	ListReferences() []sessiondb.Reference
}

type RefRange struct {
	ID    string
	Start int
	End   int
}

type RefOpenEvent struct{ ID string }

func (RefOpenEvent) event() {}

type RefSelectEvent struct{ ID string }

func (RefSelectEvent) event() {}

func (m *Model) SetReferenceStore(store ReferenceStore) {
	m.refStore = store
	m.applyReferenceRanges()
}

func (m *Model) InsertReference(ref sessiondb.Reference) {
	m.input.InsertString(ref.DisplayName())
	m.applyReferenceRanges()
	m.showHint = false
}

func (m Model) RefRanges() []RefRange {
	if m.refStore == nil {
		return nil
	}
	return FindRefRanges(m.input.Value(), m.refStore.ListReferences())
}

func (m Model) ActiveReferenceID() string {
	pos := m.input.CursorFlat()
	for _, rr := range m.RefRanges() {
		if pos >= rr.Start && pos < rr.End {
			return rr.ID
		}
	}
	return ""
}

func (m Model) ReferencedIDs() []string {
	ranges := m.RefRanges()
	ids := make([]string, 0, len(ranges))
	seen := make(map[string]bool, len(ranges))
	for _, rr := range ranges {
		if rr.ID == "" || seen[rr.ID] {
			continue
		}
		seen[rr.ID] = true
		ids = append(ids, rr.ID)
	}
	return ids
}

func (m *Model) SetValuePreserveReferences(text string) {
	m.input.SetValue(text)
	m.applyReferenceRanges()
}

func (m *Model) applyReferenceRanges() {
	ranges := m.RefRanges()
	atomic := make([]components.AtomicRange, len(ranges))
	for i, rr := range ranges {
		atomic[i] = components.AtomicRange{Start: rr.Start, End: rr.End, ID: rr.ID}
	}
	m.input.SetAtomicRanges(atomic)
	m.input.NormalizeCursorAtomic()
}

func FindRefRanges(text string, refs []sessiondb.Reference) []RefRange {
	// Build label→ID map for fast lookup
	labelMap := make(map[string]string, len(refs))
	for _, ref := range refs {
		if !ref.Active() {
			continue
		}
		label := ref.DisplayName()
		if label != "" {
			labelMap[label] = ref.ID
		}
	}
	if len(labelMap) == 0 {
		return nil
	}

	// Single-pass scan for bracket-delimited tokens: O(N + M)
	var ranges []RefRange
	runes := []rune(text)
	for i := 0; i < len(runes); i++ {
		if runes[i] != '[' {
			continue
		}
		// Find closing bracket
		end := -1
		for j := i + 1; j < len(runes); j++ {
			if runes[j] == ']' {
				end = j + 1 // include the closing bracket
				break
			}
		}
		if end == -1 {
			continue // unclosed bracket, skip
		}
		label := string(runes[i:end])
		if id, ok := labelMap[label]; ok {
			ranges = append(ranges, RefRange{ID: id, Start: i, End: end})
			i = end - 1 // advance past this match
		}
	}

	sort.Slice(ranges, func(i, j int) bool { return ranges[i].Start < ranges[j].Start })
	return ranges
}

func ExpandReferences(text string, refs []sessiondb.Reference) string {
	ranges := FindRefRanges(text, refs)
	if len(ranges) == 0 {
		return text
	}
	sort.Slice(ranges, func(i, j int) bool { return ranges[i].Start < ranges[j].Start })
	runes := []rune(text)
	var b strings.Builder
	last := 0
	for _, rr := range ranges {
		if rr.Start < last {
			continue
		}
		b.WriteString(string(runes[last:rr.Start]))
		if ref, ok := refByID(refs, rr.ID); ok && ref.Active() {
			b.WriteString(ref.AgentText())
		} else {
			b.WriteString("[Removed reference: ")
			b.WriteString(rr.ID)
			b.WriteString("]")
		}
		last = rr.End
	}
	b.WriteString(string(runes[last:]))
	return b.String()
}

func DisplayReferences(text string, refs []sessiondb.Reference) string {
	ranges := FindRefRanges(text, refs)
	if len(ranges) == 0 {
		return text
	}
	sort.Slice(ranges, func(i, j int) bool { return ranges[i].Start < ranges[j].Start })
	runes := []rune(text)
	var b strings.Builder
	last := 0
	for _, rr := range ranges {
		if rr.Start < last {
			continue
		}
		b.WriteString(string(runes[last:rr.Start]))
		if ref, ok := refByID(refs, rr.ID); ok && ref.Active() {
			b.WriteString(ref.DisplayName())
		} else {
			b.WriteString("[Removed Reference]")
		}
		last = rr.End
	}
	b.WriteString(string(runes[last:]))
	return b.String()
}

func refByID(refs []sessiondb.Reference, id string) (sessiondb.Reference, bool) {
	for _, ref := range refs {
		if ref.ID == id {
			return ref, true
		}
	}
	return sessiondb.Reference{}, false
}
