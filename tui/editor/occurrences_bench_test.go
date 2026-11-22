package editor

import (
	"strings"
	"testing"

	"crude/types/insight"
)

// A provider returns 128 occurrences near the end of a 64 KiB source line.
// Measure installation on Update, separately from provider and frame latency.
func BenchmarkOccurrenceProjectionLongLine(b *testing.B) {
	text := strings.Repeat("x", 64<<10)
	ed := newInlayTestEditor(b, text)
	source, ok := ed.CurrentInsightSource()
	if !ok {
		b.Fatal("source is not acknowledged")
	}
	marks := make([]insight.LSPHighlight, 128)
	for i := range marks {
		start := len(text) - 2*len(marks) + i*2
		marks[i] = insight.LSPHighlight{Range: occurrenceRange(0, start, start+1), Kind: insight.LSPHighlightRead}
	}
	result := insight.LSPHighlights{Boundary: insight.Boundary{Source: source}, Highlights: marks}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := ed.SetOccurrences(&result); err != nil {
			b.Fatal(err)
		}
	}
}
