package lspedit

import "testing"

func TestTextEditArrayUsesStrictParser(t *testing.T) {
	for _, raw := range []string{"null", "[]", `[{"range":{"start":{"line":0,"character":1},"end":{"line":1,"character":0}},"newText":"界\r\n"}]`} {
		if _, err := ParseTextEdits([]byte(raw)); err != nil {
			t.Fatal(raw, err)
		}
	}
	for _, raw := range []string{`{}`, `[{}]`, `[{"range":{"start":{"line":0,"line":1,"character":0},"end":{"line":1,"character":0}},"newText":"x"}]`, `[{"range":{"start":{"line":0,"character":0},"end":{"line":1,"character":0}},"newText":"\ud800"}]`} {
		if _, err := ParseTextEdits([]byte(raw)); err == nil {
			t.Fatal("malformed formatter output accepted", raw)
		}
	}
}
