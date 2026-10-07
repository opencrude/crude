package chatprojection

import (
	"strings"
	"testing"
	"time"
)

// TestThinkingOverflowsInlinePromotionRule pins the UI-only promotion rule: a
// block must both read as long and have arrived fast. It never reads a provider
// field, a declared reasoning kind or a reported token count.
func TestThinkingOverflowsInlinePromotionRule(t *testing.T) {
	const width = 70
	long := strings.Repeat("a line of reasoning\n", inlineThinkingBodyRows+2)
	for _, tc := range []struct {
		name     string
		content  string
		started  time.Time
		expected bool
	}{
		{"empty body never promotes", "", time.Now().Add(-time.Second), false},
		{"whitespace body never promotes", "   \n\t", time.Now().Add(-time.Second), false},
		{"short fresh block stays inline", "a line of reasoning", time.Now(), false},
		// Long alone is not enough: a gateway's many short headers accumulate
		// into a long trace over a whole turn and must stay in the strip.
		{"long but slow stays inline", long, time.Now().Add(-60 * time.Second), false},
		// Long and fast promotes.
		{"long burst promotes", long, time.Now().Add(-300 * time.Millisecond), true},
		// A quick trickle of many short paragraphs is exactly a condensed
		// gateway's signature and stays inline.
		{"condensed gateway headers stay inline", strings.Repeat("**Earlier header**\n\n", 40), time.Now().Add(-200 * time.Second), false},
		// Content that arrived with no measurable elapsed time is a burst.
		{"instant content is a burst", long, time.Time{}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ThinkingOverflowsInline(tc.content, 0, tc.started, width); got != tc.expected {
				t.Fatalf("ThinkingOverflowsInline=%v want %v", got, tc.expected)
			}
		})
	}
}

// TestThinkingOverflowsInlineIsWidthSensitive verifies the same block promotes
// only when it genuinely overflows at the width the chat renders at.
func TestThinkingOverflowsInlineIsWidthSensitive(t *testing.T) {
	burst := time.Now().Add(-300 * time.Millisecond)
	if !ThinkingOverflowsInline(strings.Repeat("word ", 40), 0, burst, 20) {
		t.Fatal("a narrow chat did not promote an overflowing block")
	}
	if ThinkingOverflowsInline("short thought", 0, burst, 200) {
		t.Fatal("a wide chat promoted a short block")
	}
}

// TestThinkingOverflowsInlineReplaysRealStreamShapes replays the two stream
// shapes measured live from the proxy account. A reasoning model streaming fast
// promotes once the trace is long; a condensed gateway emitting a short header
// every few seconds never does, however long the turn runs.
func TestThinkingOverflowsInlineReplaysRealStreamShapes(t *testing.T) {
	const width = 70

	// deepseek-v4.1-flash: ~42035 chars / ~600 rows over 57s (~10 rows/s). Replay
	// the measured magnitude and require promotion once it is long.
	reasoning := strings.Repeat("reasoning detail about the design ", 1236)
	started := time.Now().Add(-57 * time.Second)
	if !ThinkingOverflowsInline(reasoning, 0, started, width) {
		t.Fatal("a fast long reasoning trace did not promote")
	}

	// gpt-5.6-luna: ~3466 chars / 69 rows over 217s (~0.32 rows/s), so even when
	// the accumulated trace is past the inline budget it must stay in the strip.
	var luna strings.Builder
	for i := range 60 {
		luna.WriteString("**Planning the design details**\n\n")
		if ThinkingOverflowsInline(luna.String(), 0, time.Now().Add(-217*time.Second), width) {
			t.Fatalf("condensed gateway promoted after header %d", i)
		}
	}
}
