package workspace

import (
	"testing"

	"crude/types"
)

func TestHighlightNormalizationRefusesOccurrencesOutsideTheSource(t *testing.T) {
	text := "a😀b\nvalue\n"
	got, err := normalizeHighlights(text, types.LSPHighlightResponse{WireVersion: 3,
		JSON: []byte(`[{"range":{"start":{"line":0,"character":3},"end":{"line":0,"character":4}},"kind":2}]`)}, 0, 0)
	if err != nil || got.Answer != types.LSPHighlightsFound || len(got.Highlights) != 1 || got.Highlights[0].Kind != types.LSPHighlightRead || got.WireVersion != 3 {
		t.Fatal("valid UTF-16 occurrence changed", got, err)
	}
	for name, raw := range map[string]string{
		"surrogate": `[{"range":{"start":{"line":0,"character":2},"end":{"line":0,"character":4}}}]`,
		"past line": `[{"range":{"start":{"line":1,"character":0},"end":{"line":1,"character":9}}}]`,
		"past eof":  `[{"range":{"start":{"line":7,"character":0},"end":{"line":7,"character":1}}}]`,
	} {
		if _, err := normalizeHighlights(text, types.LSPHighlightResponse{WireVersion: 3, JSON: []byte(raw)}, 0, 0); err == nil {
			t.Fatal("an occurrence outside the exact source was accepted:", name)
		}
	}
	for raw, want := range map[string]string{"null": types.LSPHighlightsNull, "[]": types.LSPHighlightsEmpty} {
		if got, err := normalizeHighlights(text, types.LSPHighlightResponse{WireVersion: 3, JSON: []byte(raw)}, 0, 0); err != nil || got.Answer != want {
			t.Fatal("negative answers lost their kind", raw, got, err)
		}
	}
	if got, err := normalizeHighlights(text, types.LSPHighlightResponse{WireVersion: 3, Unsupported: true}, 0, 0); err != nil || got.Answer != types.LSPHighlightsUnsupported {
		t.Fatal(got, err)
	}
	if _, err := normalizeHighlights(text, types.LSPHighlightResponse{Unsupported: true, JSON: []byte("[]")}, 0, 0); err == nil {
		t.Fatal("unsupported answer carried data")
	}
}
