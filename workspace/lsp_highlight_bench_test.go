package workspace

import (
	"encoding/json"
	"strings"
	"testing"

	"crude/types"
)

// Normalize a long line and 128 provider ranges against the actual source.
func BenchmarkHighlightNormalizationLongLine(b *testing.B) {
	text := strings.Repeat("x", 64<<10)
	rows := make([]map[string]any, 128)
	for i := range rows {
		col := len(text) - 2*len(rows) + i*2
		rows[i] = map[string]any{"range": map[string]any{"start": map[string]int{"line": 0, "character": col}, "end": map[string]int{"line": 0, "character": col + 1}}, "kind": 2}
	}
	raw, err := json.Marshal(rows)
	if err != nil {
		b.Fatal(err)
	}
	response := types.LSPHighlightResponse{WireVersion: 1, JSON: raw}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := normalizeHighlights(text, response, 0, 0); err != nil {
			b.Fatal(err)
		}
	}
}
