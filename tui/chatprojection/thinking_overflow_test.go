package chatprojection

import (
	"strings"
	"testing"
	"time"
)

// TestThinkingOverflowsInlinePromotionRule pins the one presentation promotion
// rule: a block that has already outgrown the inline form promotes immediately,
// a fast burst that will overflow within the horizon promotes up front, a slow
// trace that would take much longer stays inline, and an empty body never
// promotes. The measurement is in wrapped rows at the supplied width.
func TestThinkingOverflowsInlinePromotionRule(t *testing.T) {
	width := 70 // the test's inline body width
	warm := inlineThinkingWarmUp
	now := time.Now()
	short := "a line of reasoning"
	overflowing := strings.Repeat("a line of reasoning\n", inlineThinkingBodyRows+2)

	for _, tc := range []struct {
		name     string
		content  string
		started  time.Time
		expected bool
	}{
		{"empty body never promotes", "", now.Add(-time.Second), false},
		{"whitespace body never promotes", "   \n\t", now.Add(-time.Second), false},
		{"short fresh block stays inline", short, now, false},
		{"already overflowing promotes", overflowing, now, true},
		// A cold provider start is excluded: the rate is measured from the first
		// token, so a burst just under the budget still promotes because the
		// observed rate projects past it within the horizon.
		{"burst after warm-up promotes", strings.Repeat("reasoning ", 25), now.Add(-warm), true},
		// A slow trickle that would take many seconds to overflow must not be
		// yanked into a pane the reader did not need yet.
		{"slow trace stays inline", "one short line", now.Add(-10 * time.Second), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ThinkingOverflowsInline(tc.content, 0, false, tc.started, width)
			if got != tc.expected {
				t.Fatalf("ThinkingOverflowsInline=%v want %v", got, tc.expected)
			}
		})
	}
}

// TestThinkingOverflowMeasuresTheInlineFormOnly verifies a compact-preview block
// is measured through its latest semantic block, so many short updates never
// promote a summary stream that stays readable inline.
func TestThinkingOverflowMeasuresTheInlineFormOnly(t *testing.T) {
	content := strings.Repeat("brief update\n\n", 80) + "latest block"
	start := len(content) - len("latest block")
	if ThinkingOverflowsInline(content, start, true, time.Now(), 70) {
		t.Fatal("many short summary blocks promoted the compact inline form")
	}
	if !ThinkingOverflowsInline(content, 0, false, time.Now(), 70) {
		t.Fatal("a full-body trace that overflows stayed inline")
	}
	if InlineThinkingBody(content, start, true) != "latest block" {
		t.Fatal("compact preview body is not the latest semantic block")
	}
}
