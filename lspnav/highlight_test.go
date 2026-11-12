package lspnav

import "testing"

func TestHighlightsKeepKindsAndRefuseMalformedAnswers(t *testing.T) {
	got, err := ParseHighlights([]byte(`[{"range":{"start":{"line":1,"character":2},"end":{"line":1,"character":5}}},{"range":{"start":{"line":3,"character":0},"end":{"line":3,"character":3}},"kind":3}]`))
	if err != nil || got.Answer != TargetsAnswer || len(got.Highlights) != 2 {
		t.Fatal(got, err)
	}
	if got.Highlights[0].Kind != HighlightText || got.Highlights[1].Kind != HighlightWrite || got.Highlights[1].Range.End.Character != 3 {
		t.Fatal("highlight kinds or ranges changed", got.Highlights)
	}
	for raw, want := range map[string]Answer{"null": NullAnswer, "[]": EmptyAnswer} {
		if got, err := ParseHighlights([]byte(raw)); err != nil || got.Answer != want || len(got.Highlights) != 0 {
			t.Fatal("negative answer changed", raw, got, err)
		}
	}
	for name, raw := range map[string]string{
		"object":   `{"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":1}}}`,
		"kind":     `[{"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":1}},"kind":4}]`,
		"unknown":  `[{"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":1}},"uri":"file:///a"}]`,
		"reversed": `[{"range":{"start":{"line":0,"character":2},"end":{"line":0,"character":1}}}]`,
		"negative": `[{"range":{"start":{"line":-1,"character":0},"end":{"line":0,"character":1}}}]`,
		"missing":  `[{"kind":1}]`,
	} {
		if _, err := ParseHighlights([]byte(raw)); err == nil {
			t.Fatal("malformed highlights were accepted:", name)
		}
	}
}
