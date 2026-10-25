// Package attachment holds immutable transcript attachment identities and source spans.
package attachment

import (
	"sort"
	"strings"
	"unicode/utf8"
)

// Target names an exact retained reference, not a session-local display label.
// Start and End are rune offsets in the authored message. Label is captured at send.
type Target struct {
	Start       int    `json:"start"`
	End         int    `json:"end"`
	ReferenceID string `json:"reference_id"`
	Label       string `json:"label"`
}

func (t Target) Valid(text string) bool {
	r := []rune(text)
	return t.Start >= 0 && t.End > t.Start && t.End <= len(r) && t.Label != "" && string(r[t.Start:t.End]) == t.Label
}

// Locate captures spans only for explicitly selected identities. Conflicting
// labels fail closed rather than binding an occurrence to an arbitrary reference.
func Locate(text string, selected []Target) []Target {
	var out []Target
	for _, target := range selected {
		if target.Label == "" {
			continue
		}
		ambiguous := false
		for _, other := range selected {
			if other.Label == target.Label && other.ReferenceID != target.ReferenceID {
				ambiguous = true
			}
		}
		if ambiguous {
			continue
		}
		for offset := 0; offset <= len(text)-len(target.Label); {
			at := strings.Index(text[offset:], target.Label)
			if at < 0 {
				break
			}
			at += offset
			t := target
			t.Start = utf8.RuneCountInString(text[:at])
			t.End = t.Start + utf8.RuneCountInString(t.Label)
			out = append(out, t)
			offset = at + len(t.Label)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start < out[j].Start })
	filtered := out[:0]
	end := -1
	for _, target := range out {
		if target.Start >= end {
			filtered = append(filtered, target)
			end = target.End
		}
	}
	return filtered
}
