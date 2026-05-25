package input

import (
	"sort"
	"strings"

	"crude/promptref"
	"crude/tui/components"
)

type ReferenceStore interface {
	CreateReference(promptref.CreateRequest) (promptref.Reference, error)
	UpdateReferenceContent(id, content string) error
	DeleteReference(id string) error
	GetReference(id string) (promptref.Reference, bool)
	ListReferences() []promptref.Reference
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

func PlaceholderFor(id string) string {
	return id
}

func IsPlaceholderRune(r rune) bool {
	return false
}

func (m *Model) SetReferenceStore(store ReferenceStore) {
	m.refStore = store
	m.applyReferenceRanges()
}

func (m *Model) InsertReference(ref promptref.Reference) {
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

func FindRefRanges(text string, refs []promptref.Reference) []RefRange {
	var ranges []RefRange
	for _, ref := range refs {
		if !ref.Active() {
			continue
		}
		label := ref.DisplayName()
		if label == "" {
			continue
		}
		runes := []rune(text)
		labelRunes := []rune(label)
		for i := 0; i <= len(runes)-len(labelRunes); i++ {
			if string(runes[i:i+len(labelRunes)]) == label {
				ranges = append(ranges, RefRange{ID: ref.ID, Start: i, End: i + len(labelRunes)})
				i += len(labelRunes) - 1
			}
		}
	}
	sort.Slice(ranges, func(i, j int) bool { return ranges[i].Start < ranges[j].Start })
	return ranges
}

func ExpandReferences(text string, refs []promptref.Reference) string {
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

func DisplayReferences(text string, refs []promptref.Reference) string {
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

func refByID(refs []promptref.Reference, id string) (promptref.Reference, bool) {
	for _, ref := range refs {
		if ref.ID == id {
			return ref, true
		}
	}
	return promptref.Reference{}, false
}
