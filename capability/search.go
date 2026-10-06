package capability

import (
	"sort"
	"strings"
	"unicode"

	captypes "crude/types/capability"
)

// Catalog queries are keywords, not literal sentences. Rank partial keyword
// coverage so extra task words cannot hide a relevant named capability. This
// selects descriptors only; activation still requires an exact reviewed ID.
type capabilityQuery struct {
	phrase string
	terms  []string
}

func normalizeCapabilityQuery(text string) string {
	return strings.Join(strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	}), " ")
}

func newCapabilityQuery(text string) capabilityQuery {
	query := capabilityQuery{phrase: normalizeCapabilityQuery(text)}
	seen := make(map[string]bool)
	for _, term := range strings.Fields(query.phrase) {
		if !seen[term] {
			query.terms = append(query.terms, term)
			seen[term] = true
		}
	}
	return query
}

func (query capabilityQuery) score(entry captypes.Entry) int {
	if len(query.terms) == 0 {
		return 1
	}
	name := normalizeCapabilityQuery(entry.Name)
	fields := []struct {
		text   string
		weight int
	}{
		{name, 6},
		{normalizeCapabilityQuery(entry.ID), 4},
		{normalizeCapabilityQuery(entry.Description), 2},
		{normalizeCapabilityQuery(entry.Source + " " + entry.Publisher), 1},
	}
	score := 0
	for _, term := range query.terms {
		for _, field := range fields {
			if strings.Contains(field.text, term) {
				score += field.weight
				break
			}
		}
	}
	if name != "" && query.phrase == name {
		score += 128
	} else if name != "" && strings.Contains(" "+query.phrase+" ", " "+name+" ") {
		score += 64
	}
	return score
}

func rankCapabilityEntries(entries []captypes.Entry, query capabilityQuery, limit int) []captypes.Entry {
	type match struct {
		entry captypes.Entry
		score int
	}
	matches := make([]match, 0, len(entries))
	for _, entry := range entries {
		if score := query.score(entry); score > 0 {
			matches = append(matches, match{entry, score})
		}
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].score != matches[j].score {
			return matches[i].score > matches[j].score
		}
		return matches[i].entry.ID < matches[j].entry.ID
	})
	result := make([]captypes.Entry, 0, min(limit, len(matches)))
	for _, match := range matches[:min(limit, len(matches))] {
		result = append(result, match.entry)
	}
	return result
}
