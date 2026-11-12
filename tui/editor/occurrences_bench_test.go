package editor

import (
	"strings"
	"testing"

	"crude/types"
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
	marks := make([]types.LSPHighlight, 128)
	for i := range marks {
		start := len(text) - 2*len(marks) + i*2
		marks[i] = types.LSPHighlight{Range: occurrenceRange(0, start, start+1), Kind: types.LSPHighlightRead}
	}
	result := types.LSPHighlights{Boundary: types.InsightBoundary{Source: source}, Highlights: marks}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := ed.SetOccurrences(&result); err != nil {
			b.Fatal(err)
		}
	}
}
